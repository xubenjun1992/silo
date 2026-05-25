package liquidation

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
	"github.com/silo-protocol/backend/internal/config"
	"github.com/silo-protocol/backend/internal/database"
	"github.com/silo-protocol/backend/internal/model"
)

// PoolConfig is the minimal pool info needed for liquidation monitoring.
type PoolConfig struct {
	Address         string
	DepositAsset    string
	CollateralAsset string
	RiskTier        string
}

// PriceFeeder fetches the current spot price for an asset and its on-chain update timestamp.
type PriceFeeder interface {
	GetPrice(ctx context.Context, asset string) (price decimal.Decimal, updatedAt int64, err error)
}

// Monitor is the main liquidation engine.
//
// Each scan cycle per pool:
//  1. Fetch spot prices + oracle timestamps for deposit + collateral assets
//  2. Record → TWAP rolling window, compute TWAP + deviation (decimal arithmetic)
//  3. Use worst (highest) deviation across the two assets
//  4. Assess market condition → NORMAL / EXTREME / PAUSED (CAS-protected breaker)
//  5. Recalc all Redis-cached positions' health factors using TWAP prices + timestamps
//  6. Scan for liquidatable positions (healthFactor < 1.0), filter stale prices
//  7. Simulate profitability for each target, skip unprofitable ones
//  8. Acquire in-flight lock, rate-limit, and execute in composite priority order
//
// Risk isolation is per-pool: breaker, rate limit, and executions are independent.
type Monitor struct {
	cfg                  *config.Config
	rdb                  *redis.Client
	twap                 *TWAPCalculator
	market               *MarketCondition
	store                *PositionStore
	executor             *LiquidationExecutor
	feeder               PriceFeeder
	priceStalenessSeconds int64
	liquidationLockTTL    time.Duration
}

func NewMonitor(cfg *config.Config, rdb *redis.Client, executor *LiquidationExecutor, feeder PriceFeeder) *Monitor {
	return &Monitor{
		cfg:                   cfg,
		rdb:                   rdb,
		twap:                  NewTWAPCalculator(rdb, cfg),
		market:                NewMarketCondition(rdb, cfg),
		store:                 NewPositionStore(rdb),
		executor:              executor,
		feeder:                feeder,
		priceStalenessSeconds: cfg.PriceStalenessSeconds,
		liquidationLockTTL:    5 * time.Minute,
	}
}

/*═══════════════════════════════════════════════════════════════════════════════
   Main loop
   ═══════════════════════════════════════════════════════════════════════════════*/

func (m *Monitor) Start(ctx context.Context) {
	log.Info().
		Dur("interval", m.cfg.ScanInterval).
		Dur("twapWindow", m.cfg.TWAPWindow).
		Float64("normalDev", m.cfg.NormalMaxDeviation).
		Float64("extremeDev", m.cfg.ExtremeMaxDeviation).
		Int64("maxPriceAge", m.priceStalenessSeconds).
		Msg("Liquidation monitor starting")

	ticker := time.NewTicker(m.cfg.ScanInterval)
	defer ticker.Stop()

	m.scan(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("Liquidation monitor stopped")
			return
		case <-ticker.C:
			m.scan(ctx)
		}
	}
}

/*═══════════════════════════════════════════════════════════════════════════════
   Scan cycle
   ═══════════════════════════════════════════════════════════════════════════════*/

func (m *Monitor) scan(ctx context.Context) {
	pools := m.loadPools(ctx)
	if len(pools) == 0 {
		return
	}

	log.Debug().Int("pools", len(pools)).Msg("Scan cycle start")

	for _, pool := range pools {
		if err := m.scanPool(ctx, pool); err != nil {
			log.Error().Err(err).Str("pool", pool.Address).Msg("Pool scan failed")
		}
	}
}

func (m *Monitor) scanPool(ctx context.Context, pool PoolConfig) error {
	// ── Step 1: Fetch spot prices from oracle ──
	colPrice, colUpdatedAt, err := m.feeder.GetPrice(ctx, pool.CollateralAsset)
	if err != nil {
		return fmt.Errorf("collateral price %s: %w", pool.CollateralAsset, err)
	}
	debtPrice, debtUpdatedAt, err := m.feeder.GetPrice(ctx, pool.DepositAsset)
	if err != nil {
		return fmt.Errorf("deposit price %s: %w", pool.DepositAsset, err)
	}

	// Use the older timestamp as the price timestamp for conservative staleness checks
	priceTimestamp := colUpdatedAt
	if debtUpdatedAt < colUpdatedAt {
		priceTimestamp = debtUpdatedAt
	}

	m.cachePrice(ctx, pool.Address+":col", colPrice)
	m.cachePrice(ctx, pool.Address+":debt", debtPrice)

	// ── Step 2 & 3: TWAP for both assets, get worst deviation ──
	m.twap.RecordPrice(ctx, pool.CollateralAsset, colPrice)
	m.twap.RecordPrice(ctx, pool.DepositAsset, debtPrice)

	colTWAP, _ := m.twap.Compute(ctx, pool.CollateralAsset, colPrice)
	debtTWAP, _ := m.twap.Compute(ctx, pool.DepositAsset, debtPrice)

	worstResult := colTWAP
	if debtTWAP.Deviation.GreaterThan(colTWAP.Deviation) {
		worstResult = debtTWAP
	}

	// ── Step 4: Assess market condition (CAS-protected breaker) ──
	mode := m.market.Assess(ctx, pool.Address, worstResult)

	switch mode {
	case ModePaused:
		log.Warn().
			Str("pool", pool.Address).
			Str("deviation", worstResult.Deviation.String()).
			Msg("Circuit breaker open, skipping pool")
		return nil
	case ModeExtreme:
		log.Warn().
			Str("pool", pool.Address).
			Str("deviation", worstResult.Deviation.String()).
			Msg("Extreme market — rate-limited liquidation")
	}

	m.market.ResetRateLimit(ctx, pool.Address)

	// ── Step 5: Recalc all position health factors with TWAP prices + timestamp ──
	updated, err := m.store.RecalcHealthFactors(ctx, pool.Address, colTWAP.TWAP, debtTWAP.TWAP, priceTimestamp)
	if err != nil {
		return fmt.Errorf("recalc health factors: %w", err)
	}
	if updated > 0 {
		log.Debug().
			Str("pool", pool.Address).
			Int("positions", updated).
			Str("colTWAP", colTWAP.TWAP.String()).
			Str("debtTWAP", debtTWAP.TWAP.String()).
			Msg("Health factors recalculated")
	}

	// ── Step 6: Scan for liquidatable positions (filtering stale prices) ──
	maxAllowed, _ := m.market.AllowLiquidation(ctx, pool.Address, mode)
	if maxAllowed <= 0 {
		return nil
	}

	one := decimal.NewFromInt(1)
	targets, err := m.store.GetRiskyPositions(ctx, pool.Address, maxAllowed*2, one, m.priceStalenessSeconds)
	if err != nil {
		return fmt.Errorf("get risky positions: %w", err)
	}
	if len(targets) == 0 {
		return nil
	}

	// ── Step 7: Profitability simulation ──
	profitableTargets := m.filterProfitable(ctx, targets)

	// Sort by composite priority score descending (highest priority first)
	sort.Slice(profitableTargets, func(i, j int) bool {
		return profitableTargets[i].PriorityScore.GreaterThan(profitableTargets[j].PriorityScore)
	})

	// ── Step 8: Execute with rate limiting + in-flight lock ──
	executed := m.executeLiquidations(ctx, pool.Address, mode, profitableTargets)

	log.Info().
		Str("pool", pool.Address).
		Str("mode", string(mode)).
		Str("colDev", colTWAP.Deviation.String()).
		Str("debtDev", debtTWAP.Deviation.String()).
		Int("candidates", len(targets)).
		Int("profitable", len(profitableTargets)).
		Int("executed", executed).
		Msg("Pool scan complete")

	return nil
}

/*═══════════════════════════════════════════════════════════════════════════════
   Profitability filtering
   ═══════════════════════════════════════════════════════════════════════════════*/

func (m *Monitor) filterProfitable(ctx context.Context, targets []LiquidationTarget) []LiquidationTarget {
	profitable := make([]LiquidationTarget, 0, len(targets))
	for i := range targets {
		if m.executor.CheckProfitability(ctx, &targets[i]) {
			profitable = append(profitable, targets[i])
		} else {
			log.Debug().
				Str("pool", targets[i].PoolAddr).
				Str("borrower", targets[i].UserAddr).
				Str("hf", targets[i].HealthFactor.String()).
				Str("expectedReward", targets[i].ExpectedReward.String()).
				Str("estimatedGasCost", targets[i].EstimatedGasCost.String()).
				Msg("Skipping unprofitable liquidation")
		}
	}
	return profitable
}

/*═══════════════════════════════════════════════════════════════════════════════
   Liquidation execution (with in-flight lock)
   ═══════════════════════════════════════════════════════════════════════════════*/

func (m *Monitor) executeLiquidations(ctx context.Context, poolAddr string, mode MarketMode,
	targets []LiquidationTarget) int {

	executed := 0

	for _, target := range targets {
		// Re-check rate limit
		_, remaining := m.market.AllowLiquidation(ctx, poolAddr, mode)
		if remaining <= 0 {
			log.Info().
				Str("pool", poolAddr).
				Int("executed", executed).
				Msg("Rate limit reached")
			break
		}

		// ── In-flight lock: prevent duplicate liquidation across instances ──
		lockKey := fmt.Sprintf("silo:liq:lock:%s", target.UserAddr)
		acquired, err := m.rdb.SetNX(ctx, lockKey, "1", m.liquidationLockTTL).Result()
		if err != nil || !acquired {
			log.Debug().
				Str("pool", poolAddr).
				Str("borrower", target.UserAddr).
				Msg("In-flight lock held, skipping")
			continue
		}

		result, err := m.executor.Liquidate(ctx, poolAddr, target.UserAddr)
		if err != nil {
			log.Error().Err(err).
				Str("pool", poolAddr).
				Str("borrower", target.UserAddr).
				Str("hf", target.HealthFactor.String()).
				Msg("Liquidation tx failed")
			// Release lock on failure so another attempt can be made later
			m.rdb.Del(ctx, lockKey)
			continue
		}

		result.MarketMode = mode
		result.HealthFactor = target.HealthFactor

		if err := m.executor.WaitForReceipt(ctx, result.TxHash); err != nil {
			result.Success = false
			result.Error = err.Error()
			log.Warn().Err(err).Str("tx", result.TxHash).Msg("Tx not confirmed")
			// Release lock on confirmation failure — position remains liquidatable
			m.rdb.Del(ctx, lockKey)
		} else {
			result.Success = true
			m.store.Remove(ctx, poolAddr, target.UserAddr)
			// Keep lock briefly after success to prevent race with event consumer
			m.rdb.Expire(ctx, lockKey, 30*time.Second)
		}

		log.Info().
			Str("pool", poolAddr).
			Str("borrower", target.UserAddr).
			Str("hf", target.HealthFactor.String()).
			Str("tx", result.TxHash).
			Bool("success", result.Success).
			Str("mode", string(mode)).
			Msg("Liquidation executed")

		executed++
	}

	return executed
}

/*═══════════════════════════════════════════════════════════════════════════════
   Pool loading from DB
   ═══════════════════════════════════════════════════════════════════════════════*/

func (m *Monitor) loadPools(ctx context.Context) []PoolConfig {
	var dbPools []model.Pool
	if err := database.DB.Find(&dbPools).Error; err != nil {
		log.Error().Err(err).Msg("Failed to load pools from DB")
		return nil
	}

	pools := make([]PoolConfig, 0, len(dbPools))
	for _, p := range dbPools {
		pools = append(pools, PoolConfig{
			Address:         p.Address,
			DepositAsset:    p.DepositAsset,
			CollateralAsset: p.CollateralAsset,
			RiskTier:        p.RiskTier,
		})
	}
	return pools
}

/*═══════════════════════════════════════════════════════════════════════════════
   RecordAndAssess (used by external feeders to push prices in)
   ═══════════════════════════════════════════════════════════════════════════════*/

func (m *Monitor) RecordAndAssess(ctx context.Context, asset string, spotPrice decimal.Decimal) (MarketMode, *TWAPResult) {
	_ = m.twap.RecordPrice(ctx, asset, spotPrice)

	twapResult, err := m.twap.Compute(ctx, asset, spotPrice)
	if err != nil {
		log.Error().Err(err).Str("asset", asset).Msg("TWAP computation failed")
		return ModeNormal, nil
	}

	mode := m.market.Assess(ctx, asset, twapResult)
	return mode, twapResult
}

// cachePrice stores the latest spot price in Redis so the consumer's
// PositionTracker callback can read it without making an RPC call.
func (m *Monitor) cachePrice(ctx context.Context, key string, price decimal.Decimal) {
	redisKey := "silo:price:" + key
	if err := m.rdb.Set(ctx, redisKey, price.String(), 5*time.Minute).Err(); err != nil {
		log.Warn().Err(err).Str("key", redisKey).Msg("Failed to cache price")
	}
}
