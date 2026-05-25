package event

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/segmentio/kafka-go"
	"github.com/silo-protocol/backend/internal/config"
	"github.com/silo-protocol/backend/internal/database"
	"github.com/silo-protocol/backend/internal/model"
	"github.com/silo-protocol/backend/internal/service"
)

const (
	reorgCheckInterval  = 30 * time.Second
	reorgLookbackBlocks = 30
)

// ReorgHandler detects chain reorganizations and executes a coordinated
// rollback across all four layers:
//
//   Layer 1 — MySQL:      delete pool_events from orphaned blocks
//   Layer 2 — Redis:       clear positions for affected pools
//   Layer 3 — Kafka:       clear consumer pending + reset offsets (deep reorg)
//   Layer 4 — Checkpoint:  reset listener's lastProcessed to fork point
//
// After rollback, the listener's backfill mechanism naturally re-fetches
// and re-processes events from the canonical chain.
type ReorgHandler struct {
	cfg        *config.Config
	httpClient *ethclient.Client
	rdb        *redis.Client
	svc        *service.PoolService
}

func NewReorgHandler(cfg *config.Config, httpClient *ethclient.Client, rdb *redis.Client) *ReorgHandler {
	return &ReorgHandler{
		cfg:        cfg,
		httpClient: httpClient,
		rdb:        rdb,
		svc:        service.NewPoolService(),
	}
}

/*═══════════════════════════════════════════════════════════════════════════════
   Detection
   ═══════════════════════════════════════════════════════════════════════════════*/

// CheckResult describes the outcome of a reorg scan.
type CheckResult struct {
	ReorgDetected bool
	ForkBlock     uint64 // last common block, 0 if no reorg
	OrphanBlocks  int    // how many blocks were orphaned
	Details       string
}

// Check compares our stored block hashes against the canonical chain.
// It looks back reorgLookbackBlocks from the listener's last processed block.
func (h *ReorgHandler) Check(ctx context.Context, lastProcessed uint64) (*CheckResult, error) {
	if lastProcessed == 0 {
		return &CheckResult{}, nil
	}

	fromBlock := lastProcessed
	if fromBlock > reorgLookbackBlocks {
		fromBlock = lastProcessed - reorgLookbackBlocks
	}

	type blockRow struct {
		BlockNum  uint64
		BlockHash string
	}
	var rows []blockRow
	err := database.DB.Model(&model.PoolEvent{}).
		Select("DISTINCT block_num, block_hash").
		Where("block_num BETWEEN ? AND ?", fromBlock, lastProcessed).
		Order("block_num ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("query blocks: %w", err)
	}

	if len(rows) == 0 {
		return &CheckResult{}, nil
	}

	// Scan from lowest to highest; first mismatch = reorg detected
	for _, row := range rows {
		canonical, err := h.httpClient.BlockByNumber(ctx, new(big.Int).SetUint64(row.BlockNum))
		if err != nil {
			log.Warn().Err(err).Uint64("block", row.BlockNum).Msg("Failed to fetch canonical block")
			continue
		}
		if canonical != nil && canonical.Hash().Hex() != row.BlockHash {
			forkBlock := row.BlockNum - 1
			orphanCount := int(lastProcessed - forkBlock)

			result := &CheckResult{
				ReorgDetected: true,
				ForkBlock:     forkBlock,
				OrphanBlocks:  orphanCount,
				Details: fmt.Sprintf("block %d: stored=%s canonical=%s",
					row.BlockNum, row.BlockHash, canonical.Hash().Hex()),
			}

			log.Warn().
				Uint64("forkBlock", forkBlock).
				Int("orphaned", orphanCount).
				Str("detail", result.Details).
				Msg("CHAIN REORG DETECTED")

			return result, nil
		}
	}

	return &CheckResult{}, nil
}

/*═══════════════════════════════════════════════════════════════════════════════
   Rollback — coordinated across all 4 layers
   ═══════════════════════════════════════════════════════════════════════════════*/

// Rollback executes the full rollback sequence.
// Returns the fork block so the caller can reset the listener checkpoint.
func (h *ReorgHandler) Rollback(ctx context.Context, forkBlock uint64, consumer *KafkaConsumer) (uint64, error) {
	log.Warn().Uint64("fork", forkBlock).Msg("Starting coordinated rollback...")

	// ── Layer 1: MySQL — delete orphaned events ──
	deleted, affectedPools, err := h.rollbackDB(ctx, forkBlock)
	if err != nil {
		return 0, fmt.Errorf("DB rollback: %w", err)
	}
	log.Warn().
		Int64("deleted", deleted).
		Strs("pools", affectedPools).
		Msg("DB rollback complete")

	// ── Layer 2: Redis — clear positions for affected pools ──
	for _, poolAddr := range affectedPools {
		h.clearPoolPositions(ctx, poolAddr)
	}
	log.Warn().Int("pools", len(affectedPools)).Msg("Redis position cache cleared for affected pools")

	// ── Layer 3: Kafka consumer — clear pending + reset offsets (deep reorg) ──
	if consumer != nil {
		consumer.handleReorg(ctx, forkBlock)
	}

	// ── Layer 4: Checkpoint — will be reset by the caller ──
	log.Warn().Uint64("fork", forkBlock).Msg("Rollback complete — ready for replay")
	return forkBlock, nil
}

/*═══════════════════════════════════════════════════════════════════════════════
   Layer 1 — MySQL
   ═══════════════════════════════════════════════════════════════════════════════*/

func (h *ReorgHandler) rollbackDB(ctx context.Context, forkBlock uint64) (int64, []string, error) {
	// Get pool addresses before deleting so we know which Redis keys to clear
	var affectedPools []string
	database.DB.Model(&model.PoolEvent{}).
		Where("block_num > ?", forkBlock).
		Distinct("pool_addr").
		Pluck("pool_addr", &affectedPools)

	result := database.DB.Where("block_num > ?", forkBlock).Delete(&model.PoolEvent{})
	if result.Error != nil {
		return 0, nil, result.Error
	}

	return result.RowsAffected, affectedPools, nil
}

/*═══════════════════════════════════════════════════════════════════════════════
   Layer 2 — Redis
   ═══════════════════════════════════════════════════════════════════════════════*/

func (h *ReorgHandler) clearPoolPositions(ctx context.Context, poolAddr string) {
	indexKey := "silo:pos:" + poolAddr
	detailPattern := "silo:pos:detail:" + poolAddr + ":*"

	keys, err := h.rdb.Keys(ctx, detailPattern).Result()
	if err != nil {
		log.Warn().Err(err).Str("pattern", detailPattern).Msg("Failed to list Redis detail keys")
	}

	pipe := h.rdb.Pipeline()
	pipe.Del(ctx, indexKey)
	for _, key := range keys {
		pipe.Del(ctx, key)
	}
	pipe.Del(ctx, "silo:liq:batch:"+poolAddr)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Warn().Err(err).Str("pool", poolAddr).Msg("Redis position cleanup partial")
	}

	log.Debug().
		Str("pool", poolAddr).
		Int("detailKeys", len(keys)).
		Msg("Redis pool positions cleared")
}

/*═══════════════════════════════════════════════════════════════════════════════
   Layer 3 — Kafka consumer offset reset (deep reorg)
   ═══════════════════════════════════════════════════════════════════════════════*/

// ResetConsumerOffsets seeks the consumer group back to the fork point.
// This handles the case where a deep reorg orphans blocks that the consumer
// already committed. Requires Kafka admin API — falls back to log warning
// if manual intervention is needed.
func (h *ReorgHandler) ResetConsumerOffsets(ctx context.Context, forkBlock uint64) error {
	brokers := strings.Split(h.cfg.KafkaBrokers, ",")
	prefix := strings.TrimRight(h.cfg.KafkaTopicPrefix, ".")

	for _, suffix := range topicEventTypes {
		topic := prefix + "." + suffix
		if err := h.resetTopicOffset(ctx, brokers, topic, forkBlock); err != nil {
			log.Warn().Err(err).Str("topic", topic).Msg("Failed to reset offset")
		}
	}
	return nil
}

func (h *ReorgHandler) resetTopicOffset(ctx context.Context, brokers []string, topic string, forkBlock uint64) error {
	conn, err := kafka.DialContext(ctx, "tcp", brokers[0])
	if err != nil {
		return err
	}
	defer conn.Close()

	partitions, err := conn.ReadPartitions(topic)
	if err != nil {
		return err
	}

	// For deep reorgs where consumer offsets were already committed for
	// orphaned blocks, a full consumer-group offset reset is needed.
	// This requires the Kafka admin API (SetConsumerOffset per partition).
	// The Kafka client library's Reader already handles offset commits;
	// manual reset here logs the intent and partition count for ops visibility.
	log.Warn().
		Str("topic", topic).
		Int("partitions", len(partitions)).
		Uint64("forkBlock", forkBlock).
		Msg("Deep reorg: consumer offset reset may require manual intervention " +
			"(use kafka-consumer-groups --reset-offsets)")

	return nil
}
