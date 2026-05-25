package model

import "time"

// Pool represents an isolated lending pool.
type Pool struct {
	ID              uint64    `json:"id" gorm:"primaryKey"`
	Address         string    `json:"address" gorm:"uniqueIndex;size:42"`
	DepositAsset    string    `json:"depositAsset" gorm:"size:42"`
	CollateralAsset string    `json:"collateralAsset" gorm:"size:42"`
	RiskTier        string    `json:"riskTier" gorm:"size:10"`
	CreatedAt       time.Time `json:"createdAt"`
}

// Event enum for pool activities.
type EventType string

const (
	EventDeposit    EventType = "DEPOSIT"
	EventWithdraw   EventType = "WITHDRAW"
	EventBorrow     EventType = "BORROW"
	EventRepay      EventType = "REPAY"
	EventLiquidate  EventType = "LIQUIDATE"
)

// PoolEvent stores on-chain events for off-chain querying.
// Uniqueness: (txHash, logIndex) — a single transaction can emit
// multiple logs of the same event type (e.g. batch liquidations).
type PoolEvent struct {
	ID        uint64    `json:"id" gorm:"primaryKey"`
	PoolAddr  string    `json:"poolAddr" gorm:"index;size:42"`
	EventType EventType `json:"eventType" gorm:"size:20;index"`
	UserAddr  string    `json:"userAddr" gorm:"size:42;index"`
	Amount    string    `json:"amount" gorm:"size:78"` // uint256 as decimal string
	TxHash    string    `json:"txHash" gorm:"size:66;uniqueIndex:idx_tx_log"`
	LogIndex  uint      `json:"logIndex" gorm:"uniqueIndex:idx_tx_log"`
	BlockHash string    `json:"blockHash" gorm:"size:66;index"` // for reorg detection
	BlockNum  uint64    `json:"blockNum"`
	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
}

// SyncState tracks the last processed block for each event source.
type SyncState struct {
	Source       string `json:"source" gorm:"primaryKey;size:100"` // e.g. "pool_events"
	LastBlock    uint64 `json:"lastBlock"`
	UpdatedAt    int64  `json:"updatedAt"`
}

// PoolStats is the current state of a pool, updated by the indexer.
type PoolStats struct {
	PoolAddr         string  `json:"poolAddr" gorm:"uniqueIndex;size:42"`
	TotalLiquidity   string  `json:"totalLiquidity"`
	TotalDebt        string  `json:"totalDebt"`
	UtilizationRate  float64 `json:"utilizationRate"`
	BorrowRate       float64 `json:"borrowRate"`
	SupplyRate       float64 `json:"supplyRate"`
	MinCollateralPct float64 `json:"minCollateralPct"`
	UpdatedAt        int64   `json:"updatedAt"`
}
