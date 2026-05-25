package liquidation

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
	"github.com/silo-protocol/backend/internal/config"
)

const (
	breakerKeyPrefix  = "silo:breaker"
	rateLimitKeyPrefix = "silo:liq:batch"
)

// Lua script for atomic breaker CAS (compare-and-swap).
// KEYS[1] = breaker key
// ARGV[1] = expected version (0 means create-if-not-exists)
// ARGV[2] = new breaker JSON
// Returns: {status, version} where status=1 means success, 0 means version conflict
const breakerCASScript = `
local current = redis.call('GET', KEYS[1])
local expectedVersion = tonumber(ARGV[1])
if current then
    local b = cjson.decode(current)
    if b.version ~= expectedVersion then
        return {0, b.version}
    end
elseif expectedVersion ~= 0 then
    return {0, -1}
end
redis.call('SET', KEYS[1], ARGV[2])
local newB = cjson.decode(ARGV[2])
return {1, newB.version}
`

// MarketCondition determines the current market mode and enforces
// circuit breaker + rate limiting per pool.
//
//	ModeNormal  → deviation <  NormalMaxDeviation, full liquidation flow
//	ModeExtreme → deviation >= NormalMaxDeviation && < ExtremeMaxDeviation, rate-limited
//	ModePaused  → deviation >= ExtremeMaxDeviation OR breaker cooldown active
//
// The breaker uses optimistic locking (CAS via Lua) to prevent concurrent overwrites.
type MarketCondition struct {
	rdb                 *redis.Client
	cfg                 *config.Config
	normalMaxDeviation  decimal.Decimal
	extremeMaxDeviation decimal.Decimal
	breakerCooldown     time.Duration
	extremeMaxLiq       int
	normalMaxLiq        int
	breakerCAS          *redis.Script
}

func NewMarketCondition(rdb *redis.Client, cfg *config.Config) *MarketCondition {
	return &MarketCondition{
		rdb:                 rdb,
		cfg:                 cfg,
		normalMaxDeviation:  decimal.NewFromFloat(cfg.NormalMaxDeviation),
		extremeMaxDeviation: decimal.NewFromFloat(cfg.ExtremeMaxDeviation),
		breakerCooldown:     cfg.CircuitBreakerCooldown,
		extremeMaxLiq:       cfg.CircuitBreakerMaxLiq,
		normalMaxLiq:        cfg.NormalMaxLiqPerBatch,
		breakerCAS:          redis.NewScript(breakerCASScript),
	}
}

/*═══════════════════════════════════════════════════════════════════════════════
   Mode detection (with CAS-protected breaker updates)
   ═══════════════════════════════════════════════════════════════════════════════*/

// Assess evaluates the TWAP result and returns the market mode for a pool.
// Breaker state updates use CAS to prevent concurrent overwrites.
// On CAS failure, the function retries once with fresh state.
func (m *MarketCondition) Assess(ctx context.Context, poolAddr string, twapResult *TWAPResult) MarketMode {
	breaker, err := m.getBreaker(ctx, poolAddr)
	if err != nil {
		log.Warn().Err(err).Str("pool", poolAddr).Msg("Failed to read breaker state, assuming NORMAL")
	}

	// If breaker is open, check cooldown
	if breaker != nil && breaker.Open {
		if time.Now().After(breaker.Cooldown) {
			// Cooldown passed — try to close if deviation normalized
			if twapResult.Deviation.LessThan(m.normalMaxDeviation) {
				newState := *breaker
				newState.Open = false
				newState.Reason = ""
				newState.Version++
				ok, _ := m.casBreaker(ctx, poolAddr, breaker.Version, &newState)
				if ok {
					log.Info().Str("pool", poolAddr).Msg("Circuit breaker closed — deviation normalized")
					return ModeNormal
				}
				// CAS failed — another instance handled it, re-read and fall through
				log.Debug().Str("pool", poolAddr).Msg("Breaker CAS conflict on close, using fresh state")
				breaker, _ = m.getBreaker(ctx, poolAddr)
				if breaker != nil && breaker.Open {
					return ModePaused
				}
				// Breaker was closed by other instance, proceed to deviation check
			} else {
				// Still elevated — extend cooldown with CAS
				newState := *breaker
				newState.Cooldown = time.Now().Add(m.breakerCooldown)
				newState.Version++
				m.casBreaker(ctx, poolAddr, breaker.Version, &newState)
				// If CAS fails, breaker will be re-read next cycle
			}
			return ModePaused
		}
		return ModePaused
	}

	// Breaker not open — assess deviation
	dev := twapResult.Deviation

	if dev.GreaterThanOrEqual(m.extremeMaxDeviation) {
		breaker := &BreakerState{
			PoolAddr:      poolAddr,
			Open:          true,
			OpenedAt:      time.Now(),
			Cooldown:      time.Now().Add(m.breakerCooldown),
			Reason:        fmt.Sprintf("deviation %s exceeds extreme threshold %s", dev.Mul(decimal.NewFromInt(100)).StringFixed(2)+"%", m.extremeMaxDeviation.Mul(decimal.NewFromInt(100)).StringFixed(2)+"%"),
			LastDeviation: dev,
			Version:       0,
		}
		ok, _ := m.casBreaker(ctx, poolAddr, 0, breaker)
		if ok {
			log.Warn().
				Str("pool", poolAddr).
				Str("deviation", dev.String()).
				Msg("Circuit breaker OPEN — liquidations paused")
		}
		return ModePaused
	}

	if dev.GreaterThanOrEqual(m.normalMaxDeviation) {
		return ModeExtreme
	}

	return ModeNormal
}

/*═══════════════════════════════════════════════════════════════════════════════
   Rate limiting
   ═══════════════════════════════════════════════════════════════════════════════*/

// AllowLiquidation checks whether another liquidation is allowed in this batch.
func (m *MarketCondition) AllowLiquidation(ctx context.Context, poolAddr string, mode MarketMode) (int, int) {
	key := rateLimitKey(poolAddr)
	maxAllowed := m.getMaxLiq(mode)

	count, err := m.rdb.Incr(ctx, key).Result()
	if err != nil {
		log.Warn().Err(err).Msg("Rate limit check failed, allowing by default")
		return maxAllowed, maxAllowed
	}

	if count == 1 {
		m.rdb.Expire(ctx, key, m.cfg.ScanInterval*2)
	}

	remaining := maxAllowed - int(count)
	if remaining < 0 {
		remaining = 0
	}

	return maxAllowed, remaining
}

// ResetRateLimit clears the rate counter (called at start of each scan cycle).
func (m *MarketCondition) ResetRateLimit(ctx context.Context, poolAddr string) {
	m.rdb.Del(ctx, rateLimitKey(poolAddr))
}

/*═══════════════════════════════════════════════════════════════════════════════
   Breaker state (Redis-backed, CAS-protected)
   ═══════════════════════════════════════════════════════════════════════════════*/

func (m *MarketCondition) getBreaker(ctx context.Context, poolAddr string) (*BreakerState, error) {
	data, err := m.rdb.Get(ctx, breakerKey(poolAddr)).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var b BreakerState
	if err := json.Unmarshal([]byte(data), &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// casBreaker atomically updates the breaker state using a Lua script.
// expectedVersion must match the current Version field for the update to succeed.
// Use expectedVersion=0 to create a new breaker (only if none exists).
// Returns (success, currentVersion).
func (m *MarketCondition) casBreaker(ctx context.Context, poolAddr string, expectedVersion int64, b *BreakerState) (bool, int64) {
	data, err := json.Marshal(b)
	if err != nil {
		log.Error().Err(err).Str("pool", poolAddr).Msg("Failed to marshal breaker state")
		return false, 0
	}

	key := breakerKey(poolAddr)
	result, err := m.breakerCAS.Run(ctx, m.rdb, []string{key}, expectedVersion, string(data)).Slice()
	if err != nil {
		log.Error().Err(err).Str("pool", poolAddr).Msg("Breaker CAS script failed")
		return false, 0
	}

	status, _ := result[0].(int64)
	version, _ := result[1].(int64)

	if status == 1 {
		// Set TTL on successful update
		m.rdb.Expire(ctx, key, m.breakerCooldown*5)
		return true, version
	}

	log.Debug().
		Str("pool", poolAddr).
		Int64("expected", expectedVersion).
		Int64("actual", version).
		Msg("Breaker CAS conflict")
	return false, version
}

func (m *MarketCondition) getMaxLiq(mode MarketMode) int {
	switch mode {
	case ModeExtreme:
		return m.extremeMaxLiq
	case ModeNormal:
		return m.normalMaxLiq
	default:
		return 0
	}
}

func breakerKey(poolAddr string) string {
	return fmt.Sprintf("%s:%s", breakerKeyPrefix, poolAddr)
}

func rateLimitKey(poolAddr string) string {
	return fmt.Sprintf("%s:%s", rateLimitKeyPrefix, poolAddr)
}
