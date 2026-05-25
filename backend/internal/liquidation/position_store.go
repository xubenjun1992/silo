package liquidation

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

const (
	posIndexPrefix  = "silo:pos"
	posDetailPrefix = "silo:pos:detail"
	activePoolsKey  = "silo:active_pools"
	posTTL          = 24 * time.Hour
)

// PositionStore provides layered Redis storage for borrower positions.
//
//	Layer 1 (hot)  — Redis Sorted Set, indexed by health factor for fast risk scanning.
//	Layer 2 (warm) — Redis JSON detail for each position (fast single-position lookup).
//	Layer 3 (cold) — MySQL pool_events table (historical, not accessed here).
//
// All monetary values use decimal.Decimal for precision. The Sorted Set score uses
// float64 (Redis limitation) which is adequate for health factor ordering (0.0–5.0 range).
type PositionStore struct {
	rdb *redis.Client
}

func NewPositionStore(rdb *redis.Client) *PositionStore {
	return &PositionStore{rdb: rdb}
}

/*═══════════════════════════════════════════════════════════════════════════════
   Write
   ═══════════════════════════════════════════════════════════════════════════════*/

// Upsert adds or updates a position in both the index and detail store.
func (s *PositionStore) Upsert(ctx context.Context, pos *Position) error {
	pos.UpdatedAt = time.Now().Unix()

	// Index: Sorted Set scored by health factor (float64 for Redis — adequate for ordering)
	indexKey := posIndexKey(pos.PoolAddr)
	hf, _ := pos.HealthFactor.Float64()
	if err := s.rdb.ZAdd(ctx, indexKey, redis.Z{
		Score:  hf,
		Member: pos.UserAddr,
	}).Err(); err != nil {
		return fmt.Errorf("upsert index: %w", err)
	}
	s.rdb.Expire(ctx, indexKey, posTTL)

	// Detail: full position as JSON (decimal.Decimal serializes as string for exact precision)
	detailKey := posDetailKey(pos.PoolAddr, pos.UserAddr)
	data, _ := json.Marshal(pos)
	if err := s.rdb.Set(ctx, detailKey, data, posTTL).Err(); err != nil {
		return fmt.Errorf("upsert detail: %w", err)
	}

	s.rdb.SAdd(ctx, activePoolsKey, pos.PoolAddr)

	log.Debug().
		Str("pool", pos.PoolAddr).
		Str("user", pos.UserAddr).
		Str("hf", pos.HealthFactor.String()).
		Msg("Position upserted")

	return nil
}

// Remove deletes a position from Redis (e.g., after full repayment or liquidation).
func (s *PositionStore) Remove(ctx context.Context, poolAddr, userAddr string) error {
	indexKey := posIndexKey(poolAddr)
	detailKey := posDetailKey(poolAddr, userAddr)

	pipe := s.rdb.Pipeline()
	pipe.ZRem(ctx, indexKey, userAddr)
	pipe.Del(ctx, detailKey)
	_, err := pipe.Exec(ctx)
	return err
}

/*═══════════════════════════════════════════════════════════════════════════════
   Read
   ═══════════════════════════════════════════════════════════════════════════════*/

// GetRiskyPositions returns the N most underwater positions for a pool.
// Filters out positions with stale oracle prices (priceTimestamp older than maxPriceAge).
func (s *PositionStore) GetRiskyPositions(ctx context.Context, poolAddr string, limit int,
	maxHealthFactor decimal.Decimal, maxPriceAge int64) ([]LiquidationTarget, error) {

	indexKey := posIndexKey(poolAddr)
	maxHF, _ := maxHealthFactor.Float64()

	members, err := s.rdb.ZRangeByScoreWithScores(ctx, indexKey, &redis.ZRangeBy{
		Min:    "0",
		Max:    fmt.Sprintf("%.4f", maxHF),
		Offset: 0,
		Count:  int64(limit),
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("ZRangeByScore: %w", err)
	}

	now := time.Now().Unix()
	targets := make([]LiquidationTarget, 0, len(members))
	for _, m := range members {
		userAddr := m.Member.(string)
		pos, err := s.GetPosition(ctx, poolAddr, userAddr)
		if err != nil {
			log.Warn().Err(err).Str("pool", poolAddr).Str("user", userAddr).Msg("Position detail missing, skipping")
			s.rdb.ZRem(ctx, indexKey, userAddr)
			continue
		}

		// Filter stale prices
		if maxPriceAge > 0 && pos.PriceTimestamp > 0 {
			age := now - pos.PriceTimestamp
			if age > maxPriceAge {
				log.Warn().
					Str("pool", poolAddr).
					Str("user", userAddr).
					Int64("priceAge", age).
					Int64("maxAge", maxPriceAge).
					Msg("Skipping position — oracle price is stale")
				continue
			}
		}

		targets = append(targets, LiquidationTarget{
			Position:    *pos,
			PriorityScore: decimal.Zero, // computed later with profitability
		})
	}

	return targets, nil
}

// GetPosition returns full position details for a single user.
func (s *PositionStore) GetPosition(ctx context.Context, poolAddr, userAddr string) (*Position, error) {
	detailKey := posDetailKey(poolAddr, userAddr)
	data, err := s.rdb.Get(ctx, detailKey).Result()
	if err != nil {
		return nil, err
	}
	var pos Position
	if err := json.Unmarshal([]byte(data), &pos); err != nil {
		return nil, err
	}
	return &pos, nil
}

// CountPositions returns the total number of active positions in a pool.
func (s *PositionStore) CountPositions(ctx context.Context, poolAddr string) int64 {
	return s.rdb.ZCard(ctx, posIndexKey(poolAddr)).Val()
}

// GetActivePools returns all pool addresses with active positions.
func (s *PositionStore) GetActivePools(ctx context.Context) []string {
	return s.rdb.SMembers(ctx, activePoolsKey).Val()
}

/*═══════════════════════════════════════════════════════════════════════════════
   Batch — update health factors for all positions in a pool (after price change)
   ═══════════════════════════════════════════════════════════════════════════════*/

// RecalcHealthFactors re-computes the health factor for every position in a pool.
//
//	healthFactor = (collateral × colPrice) / (debt × debtPrice × minCollatRatio)
func (s *PositionStore) RecalcHealthFactors(ctx context.Context, poolAddr string,
	colPrice, debtPrice decimal.Decimal, priceTimestamp int64) (int, error) {

	indexKey := posIndexKey(poolAddr)
	members, err := s.rdb.ZRange(ctx, indexKey, 0, -1).Result()
	if err != nil {
		return 0, err
	}

	updated := 0
	for _, userAddr := range members {
		pos, err := s.GetPosition(ctx, poolAddr, userAddr)
		if err != nil {
			continue
		}

		pos.CollateralPrice = colPrice
		pos.DebtPrice = debtPrice
		pos.PriceTimestamp = priceTimestamp
		oldHF := pos.HealthFactor
		pos.HealthFactor = calcHealthFactor(pos.CollateralAmount, pos.DebtAmount,
			colPrice, debtPrice, pos.MinCollatRatio)

		detailKey := posDetailKey(poolAddr, userAddr)
		data, _ := json.Marshal(pos)
		s.rdb.Set(ctx, detailKey, data, posTTL)

		hf, _ := pos.HealthFactor.Float64()
		s.rdb.ZAdd(ctx, indexKey, redis.Z{Score: hf, Member: userAddr})

		one := decimal.NewFromInt(1)
		if pos.HealthFactor.LessThan(one) && oldHF.GreaterThanOrEqual(one) {
			log.Warn().
				Str("pool", poolAddr).
				Str("user", userAddr).
				Str("newHF", pos.HealthFactor.String()).
				Msg("Position crossed liquidation threshold")
		}
		updated++
	}

	return updated, nil
}

/*═══════════════════════════════════════════════════════════════════════════════
   Keys
   ═══════════════════════════════════════════════════════════════════════════════*/

func posIndexKey(poolAddr string) string {
	return fmt.Sprintf("%s:%s", posIndexPrefix, poolAddr)
}

func posDetailKey(poolAddr, userAddr string) string {
	return fmt.Sprintf("%s:%s:%s", posDetailPrefix, poolAddr, userAddr)
}

// calcHealthFactor computes: (collateral × colPrice) / (debt × debtPrice × minCollatRatio)
func calcHealthFactor(collateral, debt, colPrice, debtPrice, minCollatRatio decimal.Decimal) decimal.Decimal {
	if debt.LessThanOrEqual(decimal.Zero) || debtPrice.LessThanOrEqual(decimal.Zero) || minCollatRatio.LessThanOrEqual(decimal.Zero) {
		return decimal.NewFromFloat(999.0)
	}
	collatValue := collateral.Mul(colPrice)
	debtValue := debt.Mul(debtPrice).Mul(minCollatRatio)
	return collatValue.Div(debtValue)
}
