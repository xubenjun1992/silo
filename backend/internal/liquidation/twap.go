package liquidation

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
	"github.com/silo-protocol/backend/internal/config"
)

const twapKeyPrefix = "silo:twap"

// TWAPCalculator maintains a rolling price window in Redis and computes
// Time-Weighted Average Price to protect against single-block price manipulation.
//
// Redis structure: Sorted Set per asset
//
//	Key:   silo:twap:{asset}
//	Score: unix timestamp in seconds
//	Value: price as decimal string (full precision)
//
// All price arithmetic uses decimal.Decimal to avoid float64 accumulation errors.
type TWAPCalculator struct {
	rdb    *redis.Client
	window time.Duration
	grain  time.Duration
}

func NewTWAPCalculator(rdb *redis.Client, cfg *config.Config) *TWAPCalculator {
	return &TWAPCalculator{
		rdb:    rdb,
		window: cfg.TWAPWindow,
		grain:  cfg.TWAPGranularity,
	}
}

// RecordPrice snapshots a spot price into the rolling window.
func (t *TWAPCalculator) RecordPrice(ctx context.Context, asset string, price decimal.Decimal) error {
	now := time.Now().Unix()
	key := twapKey(asset)

	pipe := t.rdb.Pipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: price.String()})
	cutoff := now - int64(t.window.Seconds())
	pipe.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("%d", cutoff))
	pipe.Expire(ctx, key, t.window*2)
	_, err := pipe.Exec(ctx)
	return err
}

// Compute calculates TWAP for an asset over the configured window using decimal arithmetic.
func (t *TWAPCalculator) Compute(ctx context.Context, asset string, spotPrice decimal.Decimal) (*TWAPResult, error) {
	now := time.Now().Unix()
	cutoff := now - int64(t.window.Seconds())
	key := twapKey(asset)

	samples, err := t.rdb.ZRangeByScoreWithScores(ctx, key, &redis.ZRangeBy{
		Min: fmt.Sprintf("%d", cutoff),
		Max: fmt.Sprintf("%d", now),
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("redis ZRangeByScore: %w", err)
	}

	if len(samples) < 2 {
		return &TWAPResult{
			Asset:       asset,
			SpotPrice:   spotPrice,
			TWAP:        spotPrice,
			Deviation:   decimal.Zero,
			SampleCount: len(samples),
			WindowSec:   int64(t.window.Seconds()),
		}, nil
	}

	// Time-weighted average: TWAP = Σ(price_i × Δt_i) / total_time
	// All arithmetic in decimal.Decimal to avoid float64 accumulation error.
	weightedSum := decimal.Zero
	totalWeight := decimal.Zero
	var prevPrice decimal.Decimal
	var prevTs int64

	for i, s := range samples {
		price, err := decimal.NewFromString(s.Member.(string))
		if err != nil {
			log.Warn().Err(err).Str("raw", s.Member.(string)).Msg("TWAP: failed to parse price sample, skipping")
			continue
		}
		ts := int64(s.Score)

		if i == 0 {
			prevPrice = price
			prevTs = ts
			continue
		}

		dt := decimal.NewFromFloat(float64(ts - prevTs))
		weightedSum = weightedSum.Add(prevPrice.Mul(dt))
		totalWeight = totalWeight.Add(dt)
		prevPrice = price
		prevTs = ts
	}

	twap := spotPrice
	if totalWeight.GreaterThan(decimal.Zero) {
		twap = weightedSum.Div(totalWeight)
	}

	deviation := decimal.Zero
	if twap.GreaterThan(decimal.Zero) {
		deviation = spotPrice.Sub(twap).Abs().Div(twap)
	}

	log.Debug().
		Str("asset", asset).
		Str("spot", spotPrice.String()).
		Str("twap", twap.String()).
		Str("deviation", deviation.String()).
		Int("samples", len(samples)).
		Msg("TWAP computed")

	return &TWAPResult{
		Asset:       asset,
		SpotPrice:   spotPrice,
		TWAP:        twap,
		Deviation:   deviation,
		SampleCount: len(samples),
		WindowSec:   int64(t.window.Seconds()),
	}, nil
}

func twapKey(asset string) string {
	return fmt.Sprintf("%s:%s", twapKeyPrefix, asset)
}
