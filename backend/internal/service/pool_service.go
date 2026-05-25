package service

import (
	"github.com/silo-protocol/backend/internal/database"
	"github.com/silo-protocol/backend/internal/model"
)

type PoolService struct{}

func NewPoolService() *PoolService {
	return &PoolService{}
}

func (s *PoolService) ListPools() ([]model.Pool, error) {
	var pools []model.Pool
	if err := database.DB.Order("created_at DESC").Find(&pools).Error; err != nil {
		return nil, err
	}
	return pools, nil
}

func (s *PoolService) GetPoolStats(addr string) (*model.PoolStats, error) {
	var stats model.PoolStats
	if err := database.DB.Where("pool_addr = ?", addr).First(&stats).Error; err != nil {
		return nil, err
	}
	return &stats, nil
}

func (s *PoolService) GetEvents(addr string, limit int) ([]model.PoolEvent, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var events []model.PoolEvent
	if err := database.DB.Where("pool_addr = ?", addr).
		Order("block_num DESC, id DESC").
		Limit(limit).
		Find(&events).Error; err != nil {
		return nil, err
	}
	return events, nil
}

func (s *PoolService) InsertEvent(event *model.PoolEvent) error {
	return database.DB.Create(event).Error
}

func (s *PoolService) UpsertPoolStats(stats *model.PoolStats) error {
	return database.DB.Save(stats).Error
}

// ── SyncState (track last processed block) ──

func (s *PoolService) GetLastBlock(source string) (uint64, error) {
	var ss model.SyncState
	err := database.DB.Where("source = ?", source).First(&ss).Error
	if err != nil {
		return 0, err
	}
	return ss.LastBlock, nil
}

func (s *PoolService) UpsertLastBlock(source string, blockNum uint64) error {
	ss := model.SyncState{
		Source:    source,
		LastBlock: blockNum,
	}
	return database.DB.Save(&ss).Error
}

// InsertEventIgnoreDup inserts an event, silently skipping duplicate (txHash, logIndex).
// A single transaction can emit multiple logs of the same event type, so
// (txHash + logIndex) is the true unique key — not (txHash + eventType).
func (s *PoolService) InsertEventIgnoreDup(event *model.PoolEvent) error {
	return database.DB.Where("tx_hash = ? AND log_index = ?", event.TxHash, event.LogIndex).
		FirstOrCreate(event).Error
}
