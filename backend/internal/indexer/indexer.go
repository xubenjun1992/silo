package indexer

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"github.com/silo-protocol/backend/internal/config"
	"github.com/silo-protocol/backend/internal/database"
	"github.com/silo-protocol/backend/internal/model"
)

const batchSize = 500

type Indexer struct {
	cfg           *config.Config
	interval      time.Duration
	lastIndexedID uint64
	mu            sync.Mutex
}

func New(cfg *config.Config) *Indexer {
	idx := &Indexer{
		cfg:      cfg,
		interval: 15 * time.Second,
	}
	idx.loadCursor()
	return idx
}

func (idx *Indexer) loadCursor() {
	var ss model.SyncState
	err := database.DB.Where("source = ?", "indexer").First(&ss).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		log.Info().Msg("Indexer: no cursor found, starting from latest event")
		var maxID uint64
		if err := database.DB.Model(&model.PoolEvent{}).
			Select("COALESCE(MAX(id), 0)").Scan(&maxID).Error; err != nil {
			log.Error().Err(err).Msg("Indexer: failed to query max event ID, starting from 0")
		}
		idx.lastIndexedID = maxID
		return
	}
	if err != nil {
		log.Error().Err(err).Msg("Indexer: failed to load cursor, starting from 0")
		return
	}
	idx.lastIndexedID = ss.LastBlock
	log.Info().Uint64("cursor", idx.lastIndexedID).Msg("Indexer: loaded cursor")
}

func (idx *Indexer) saveCursor() error {
	return database.DB.Save(&model.SyncState{
		Source:    "indexer",
		LastBlock: idx.lastIndexedID,
		UpdatedAt: time.Now().Unix(),
	}).Error
}

func (idx *Indexer) Start(ctx context.Context) {
	log.Info().Dur("interval", idx.interval).Uint64("cursor", idx.lastIndexedID).Msg("Indexer starting")
	ticker := time.NewTicker(idx.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("Indexer stopped")
			return
		case <-ticker.C:
			idx.reindex()
		}
	}
}

func (idx *Indexer) reindex() {
	if !idx.mu.TryLock() {
		log.Warn().Msg("Indexer: previous reindex still running, skipping this tick")
		return
	}
	defer idx.mu.Unlock()

	var maxID uint64
	if err := database.DB.Model(&model.PoolEvent{}).
		Select("COALESCE(MAX(id), 0)").Scan(&maxID).Error; err != nil {
		log.Error().Err(err).Msg("Indexer: failed to query max event ID")
		return
	}

	if maxID <= idx.lastIndexedID {
		return
	}

	var events []model.PoolEvent
	if err := database.DB.Where("id > ? AND id <= ?", idx.lastIndexedID, maxID).
		Order("id ASC").Limit(batchSize).Find(&events).Error; err != nil {
		log.Error().Err(err).Msg("Indexer: failed to fetch events")
		return
	}

	if len(events) == 0 {
		return
	}

	// Only advance the cursor after each event is successfully saved.
	for i := range events {
		if err := idx.applyDelta(&events[i]); err != nil {
			log.Error().Err(err).Uint64("eventID", events[i].ID).Msg("Indexer: stopping batch due to error, will retry next tick")
			break
		}
		idx.lastIndexedID = events[i].ID
	}

	if err := idx.saveCursor(); err != nil {
		log.Error().Err(err).Msg("Indexer: failed to persist cursor")
	}

	remaining := int(maxID - idx.lastIndexedID)
	log.Debug().
		Int("processed", len(events)).
		Int("remaining", remaining).
		Uint64("cursor", idx.lastIndexedID).
		Msg("Indexer: batch processed")
}

// applyDelta applies a single event to the pool stats. Returns error so the
// caller can stop the batch and avoid advancing the cursor past a failed write.
func (idx *Indexer) applyDelta(e *model.PoolEvent) error {
	var stats model.PoolStats
	err := database.DB.Where("pool_addr = ?", e.PoolAddr).First(&stats).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		stats = model.PoolStats{PoolAddr: e.PoolAddr, TotalLiquidity: "0", TotalDebt: "0"}
	} else if err != nil {
		return err
	}

	liq, err1 := decimal.NewFromString(stats.TotalLiquidity)
	debt, err2 := decimal.NewFromString(stats.TotalDebt)
	amount, err3 := decimal.NewFromString(e.Amount)
	if err1 != nil || err2 != nil || err3 != nil {
		log.Error().
			Str("pool", e.PoolAddr).
			Str("totalLiquidity", stats.TotalLiquidity).
			Str("totalDebt", stats.TotalDebt).
			Str("amount", e.Amount).
			Msg("Indexer: failed to parse decimal, event will be retried")
		return errors.New("decimal parse failure")
	}

	switch e.EventType {
	case "DEPOSIT":
		liq = liq.Add(amount)
	case "WITHDRAW":
		liq = liq.Sub(amount)
	case "BORROW":
		debt = debt.Add(amount)
	case "REPAY":
		liq = liq.Add(amount)
		debt = debt.Sub(amount)
	case "LIQUIDATE":
		debt = debt.Sub(amount)
	}

	stats.TotalLiquidity = liq.String()
	stats.TotalDebt = debt.String()
	stats.UpdatedAt = time.Now().Unix()

	return database.DB.Save(&stats).Error
}
