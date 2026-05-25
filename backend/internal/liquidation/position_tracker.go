package liquidation

import (
	"context"
	"math/big"

	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
	"github.com/silo-protocol/backend/internal/model"
)

// PositionTracker updates Redis-cached positions in response to on-chain events.
// Called by the Kafka consumer after an event is confirmed to a safe block and
// persisted to MySQL.
type PositionTracker struct {
	store *PositionStore
}

func NewPositionTracker(store *PositionStore) *PositionTracker {
	return &PositionTracker{store: store}
}

// Apply updates the cached position based on an event.
func (t *PositionTracker) Apply(ctx context.Context, event *model.PoolEvent,
	colPrice, debtPrice, minCollatRatio decimal.Decimal, priceTimestamp int64) {

	poolAddr := event.PoolAddr
	userAddr := event.UserAddr

	amount := parseAmount(event.Amount)

	switch event.EventType {
	case model.EventDeposit:
		t.handleDeposit(ctx, poolAddr, userAddr, amount, colPrice, debtPrice, minCollatRatio, priceTimestamp)

	case model.EventWithdraw:
		t.handleWithdraw(ctx, poolAddr, userAddr, amount, colPrice, debtPrice, minCollatRatio, priceTimestamp)

	case model.EventBorrow:
		t.handleBorrow(ctx, poolAddr, userAddr, amount, colPrice, debtPrice, minCollatRatio, priceTimestamp)

	case model.EventRepay:
		t.handleRepay(ctx, poolAddr, userAddr, amount, colPrice, debtPrice, minCollatRatio, priceTimestamp)

	case model.EventLiquidate:
		t.store.Remove(ctx, poolAddr, userAddr)
		log.Info().
			Str("pool", poolAddr).
			Str("user", userAddr).
			Msg("Position removed after liquidation")
	}
}

func (t *PositionTracker) handleDeposit(ctx context.Context, poolAddr, userAddr string,
	amount, colPrice, debtPrice, minCollatRatio decimal.Decimal, priceTimestamp int64) {

	pos := t.getOrCreate(ctx, poolAddr, userAddr, colPrice, debtPrice, minCollatRatio, priceTimestamp)
	pos.CollateralAmount = pos.CollateralAmount.Add(amount)
	pos.CollateralPrice = colPrice
	pos.DebtPrice = debtPrice
	pos.PriceTimestamp = priceTimestamp
	pos.HealthFactor = calcHealthFactor(pos.CollateralAmount, pos.DebtAmount, colPrice, debtPrice, pos.MinCollatRatio)
	t.store.Upsert(ctx, pos)
}

func (t *PositionTracker) handleWithdraw(ctx context.Context, poolAddr, userAddr string,
	amount, colPrice, debtPrice, minCollatRatio decimal.Decimal, priceTimestamp int64) {

	pos := t.getOrCreate(ctx, poolAddr, userAddr, colPrice, debtPrice, minCollatRatio, priceTimestamp)
	pos.CollateralAmount = pos.CollateralAmount.Sub(amount)
	if pos.CollateralAmount.LessThan(decimal.Zero) {
		pos.CollateralAmount = decimal.Zero
	}
	pos.CollateralPrice = colPrice
	pos.DebtPrice = debtPrice
	pos.PriceTimestamp = priceTimestamp
	pos.HealthFactor = calcHealthFactor(pos.CollateralAmount, pos.DebtAmount, colPrice, debtPrice, pos.MinCollatRatio)
	t.store.Upsert(ctx, pos)
}

func (t *PositionTracker) handleBorrow(ctx context.Context, poolAddr, userAddr string,
	amount, colPrice, debtPrice, minCollatRatio decimal.Decimal, priceTimestamp int64) {

	pos := t.getOrCreate(ctx, poolAddr, userAddr, colPrice, debtPrice, minCollatRatio, priceTimestamp)
	pos.DebtAmount = pos.DebtAmount.Add(amount)
	pos.CollateralPrice = colPrice
	pos.DebtPrice = debtPrice
	pos.PriceTimestamp = priceTimestamp
	pos.HealthFactor = calcHealthFactor(pos.CollateralAmount, pos.DebtAmount, colPrice, debtPrice, pos.MinCollatRatio)

	one := decimal.NewFromInt(1)
	if pos.HealthFactor.LessThan(one) {
		log.Warn().
			Str("pool", poolAddr).
			Str("user", userAddr).
			Str("hf", pos.HealthFactor.String()).
			Msg("ALERT: position below liquidation threshold")
	}

	t.store.Upsert(ctx, pos)
}

func (t *PositionTracker) handleRepay(ctx context.Context, poolAddr, userAddr string,
	amount, colPrice, debtPrice, minCollatRatio decimal.Decimal, priceTimestamp int64) {

	pos := t.getOrCreate(ctx, poolAddr, userAddr, colPrice, debtPrice, minCollatRatio, priceTimestamp)
	pos.DebtAmount = pos.DebtAmount.Sub(amount)
	if pos.DebtAmount.LessThan(decimal.Zero) {
		pos.DebtAmount = decimal.Zero
	}
	pos.CollateralPrice = colPrice
	pos.DebtPrice = debtPrice
	pos.PriceTimestamp = priceTimestamp
	pos.HealthFactor = calcHealthFactor(pos.CollateralAmount, pos.DebtAmount, colPrice, debtPrice, pos.MinCollatRatio)

	if pos.CollateralAmount.Equal(decimal.Zero) && pos.DebtAmount.Equal(decimal.Zero) {
		t.store.Remove(ctx, poolAddr, userAddr)
		log.Debug().Str("pool", poolAddr).Str("user", userAddr).Msg("Position fully closed")
		return
	}

	t.store.Upsert(ctx, pos)
}

func (t *PositionTracker) getOrCreate(ctx context.Context, poolAddr, userAddr string,
	colPrice, debtPrice, minCollatRatio decimal.Decimal, priceTimestamp int64) *Position {

	pos, err := t.store.GetPosition(ctx, poolAddr, userAddr)
	if err != nil {
		if minCollatRatio.LessThanOrEqual(decimal.Zero) {
			minCollatRatio = decimal.NewFromFloat(1.2)
		}
		pos = &Position{
			PoolAddr:        poolAddr,
			UserAddr:        userAddr,
			MinCollatRatio:  minCollatRatio,
			CollateralPrice: colPrice,
			DebtPrice:       debtPrice,
			PriceTimestamp:  priceTimestamp,
		}
	}
	return pos
}

// parseAmount converts a decimal string (uint256) to decimal.Decimal with 18 decimals normalization.
func parseAmount(raw string) decimal.Decimal {
	n := new(big.Int)
	n.SetString(raw, 10)
	amount := decimal.NewFromBigInt(n, 0)
	divisor := decimal.NewFromInt(10).Pow(decimal.NewFromInt(18))
	return amount.Div(divisor)
}
