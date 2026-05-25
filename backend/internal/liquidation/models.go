package liquidation

import (
	"time"

	"github.com/shopspring/decimal"
)

/*═══════════════════════════════════════════════════════════════════════════════
   Market condition
   ═══════════════════════════════════════════════════════════════════════════════*/

type MarketMode string

const (
	ModeNormal  MarketMode = "NORMAL"
	ModeExtreme MarketMode = "EXTREME"
	ModePaused  MarketMode = "PAUSED"
)

/*═══════════════════════════════════════════════════════════════════════════════
   TWAP snapshot
   ═══════════════════════════════════════════════════════════════════════════════*/

// PricePoint is a single price observation stored in the TWAP rolling window.
type PricePoint struct {
	Asset     string          `json:"asset"`
	Price     decimal.Decimal `json:"price"`
	Timestamp int64           `json:"ts"`
}

// TWAPResult holds the computed TWAP + spot deviation.
type TWAPResult struct {
	Asset       string          `json:"asset"`
	SpotPrice   decimal.Decimal `json:"spot"`
	TWAP        decimal.Decimal `json:"twap"`
	Deviation   decimal.Decimal `json:"deviation"` // |spot - twap| / twap
	SampleCount int             `json:"samples"`
	WindowSec   int64           `json:"window_sec"`
}

/*═══════════════════════════════════════════════════════════════════════════════
   User position (cached in Redis)
   ═══════════════════════════════════════════════════════════════════════════════*/

// Position is a borrower's position snapshot used for liquidation monitoring.
type Position struct {
	PoolAddr         string          `json:"poolAddr"`
	UserAddr         string          `json:"userAddr"`
	CollateralAmount decimal.Decimal `json:"collateral"`
	DebtAmount       decimal.Decimal `json:"debt"`
	CollateralPrice  decimal.Decimal `json:"colPrice"`
	DebtPrice        decimal.Decimal `json:"debtPrice"`
	HealthFactor     decimal.Decimal `json:"healthFactor"` // < 1 = liquidatable
	MinCollatRatio   decimal.Decimal `json:"minCollatRatio"`
	UpdatedAt        int64           `json:"updatedAt"`
	PriceTimestamp   int64           `json:"priceTimestamp"` // when the oracle price was last updated on-chain
}

// LiquidationTarget is a position queued for liquidation, sorted by risk + profitability.
type LiquidationTarget struct {
	Position
	PriorityScore   decimal.Decimal `json:"priority"`     // composite: risk + profit weight
	ExpectedReward  decimal.Decimal `json:"expectedReward"` // estimated liquidation bonus
	EstimatedGasCost decimal.Decimal `json:"estimatedGasCost"`
	IsProfitable    bool            `json:"isProfitable"`
}

/*═══════════════════════════════════════════════════════════════════════════════
   Circuit breaker state
   ═══════════════════════════════════════════════════════════════════════════════*/

type BreakerState struct {
	PoolAddr      string          `json:"poolAddr"`
	Open          bool            `json:"open"`
	OpenedAt      time.Time       `json:"openedAt"`
	Cooldown      time.Time       `json:"cooldown"`
	Reason        string          `json:"reason"`
	LastDeviation decimal.Decimal `json:"lastDeviation"`
	Version       int64           `json:"version"` // CAS version for optimistic locking
}

/*═══════════════════════════════════════════════════════════════════════════════
   Liquidation result
   ═══════════════════════════════════════════════════════════════════════════════*/

type LiquidationResult struct {
	TxHash       string          `json:"txHash"`
	PoolAddr     string          `json:"poolAddr"`
	Borrower     string          `json:"borrower"`
	DebtRepaid   decimal.Decimal `json:"debtRepaid"`
	Collateral   decimal.Decimal `json:"collateralSeized"`
	Reward       decimal.Decimal `json:"reward"`
	HealthFactor decimal.Decimal `json:"healthFactor"`
	MarketMode   MarketMode      `json:"marketMode"`
	Success      bool            `json:"success"`
	Error        string          `json:"error,omitempty"`
	Timestamp    int64           `json:"timestamp"`
}
