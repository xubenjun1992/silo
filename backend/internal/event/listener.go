package event

import (
	"context"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/rs/zerolog/log"
	"github.com/silo-protocol/backend/internal/config"
	"github.com/silo-protocol/backend/internal/model"
	"github.com/silo-protocol/backend/internal/service"
)

const (
	syncSourceName = "listener_checkpoint" // DB sync_state key
	pollInterval   = 15 * time.Second
	wsReconnectDelay = 5 * time.Second
	confirmations  = 12   // reserved for checkpoint; actual safe-block gating is in consumer
	safetyOverlap  = 20
	batchSize      = 2000
)

// Event topic signatures — replace with real keccak256 hashes after compilation.
var (
	TopicDeposited  = common.HexToHash("0x341ee0c4dfe8c58c3f6d6d749b0d0b68c2e7ef3b1ae1fd4ac9b61b4c0b0c1e0") // placeholder
	TopicWithdrawn  = common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000000")
	TopicBorrowed   = common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000000")
	TopicRepaid     = common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000000")
	TopicLiquidated = common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000000")
)

var allTopics = []common.Hash{TopicDeposited, TopicWithdrawn, TopicBorrowed, TopicRepaid, TopicLiquidated}

// Listener fetches on-chain events via two channels and publishes to Kafka.
//
//   Channel 1 — WebSocket subscription (real-time, wsClient)
//   Channel 2 — eth_getLogs polling (backfill, httpClient)
//
// Both channels → Kafka (no direct DB writes).
// The Kafka consumer is responsible for safe-block gating and DB persistence.
type Listener struct {
	cfg        *config.Config
	httpClient *ethclient.Client
	wsClient   *ethclient.Client
	producer   *KafkaProducer
	consumer   *KafkaConsumer
	svc        *service.PoolService // only for checkpoint read/write
	reorg      *ReorgHandler

	mu            sync.Mutex
	lastProcessed uint64

	knownAddresses []common.Address
}

func NewListener(cfg *config.Config, producer *KafkaProducer, consumer *KafkaConsumer, reorg *ReorgHandler) (*Listener, error) {
	httpClient, err := ethclient.Dial(cfg.HttpRpcUrl)
	if err != nil {
		return nil, err
	}
	wsClient, err := ethclient.Dial(cfg.WsRpcUrl)
	if err != nil {
		return nil, err
	}
	return &Listener{
		cfg:            cfg,
		httpClient:     httpClient,
		wsClient:       wsClient,
		producer:       producer,
		consumer:       consumer,
		svc:            service.NewPoolService(),
		reorg:          reorg,
		knownAddresses: []common.Address{common.HexToAddress(cfg.PoolFactory)},
	}, nil
}

/*═══════════════════════════════════════════════════════════════════════════════
   PUBLIC — Start both channels
   ═══════════════════════════════════════════════════════════════════════════════*/

func (l *Listener) Start(ctx context.Context) {
	log.Info().Str("factory", l.cfg.PoolFactory).Msg("Event listener starting (WS + polling → Kafka)")

	l.loadCheckpoint()
	l.backfill(ctx)               // 1. Catch-up
	go l.runWebSocket(ctx)        // 2. Real-time
	go l.runPoller(ctx)           // 3. Gap filler
	go l.runReorgChecker(ctx)     // 4. Chain reorganization detection
}

/*═══════════════════════════════════════════════════════════════════════════════
   CHANNEL 1 — WebSocket subscription
   ═══════════════════════════════════════════════════════════════════════════════*/

func (l *Listener) runWebSocket(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := l.wsSubscribe(ctx); err != nil {
			log.Error().Err(err).Msg("WebSocket error, reconnecting...")
			select {
			case <-ctx.Done():
				return
			case <-time.After(wsReconnectDelay):
			}
		}
	}
}

func (l *Listener) wsSubscribe(ctx context.Context) error {
	query := ethereum.FilterQuery{
		Addresses: l.knownAddresses,
		Topics:    [][]common.Hash{allTopics},
	}

	logsCh := make(chan types.Log, 64)
	sub, err := l.wsClient.SubscribeFilterLogs(ctx, query, logsCh)
	if err != nil {
		return err
	}
	defer sub.Unsubscribe()

	log.Info().Msg("WebSocket subscription established")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-sub.Err():
			return err
		case vLog := <-logsCh:
			l.handleLog(ctx, vLog)
			l.advanceCheckpoint(vLog.BlockNumber)
		}
	}
}

/*═══════════════════════════════════════════════════════════════════════════════
   CHANNEL 2 — eth_getLogs polling
   ═══════════════════════════════════════════════════════════════════════════════*/

func (l *Listener) runPoller(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.backfill(ctx)
		}
	}
}

func (l *Listener) backfill(ctx context.Context) {
	latest, err := l.httpClient.BlockNumber(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get latest block for backfill")
		return
	}
	if latest <= confirmations {
		return
	}

	toBlock := latest - confirmations

	l.mu.Lock()
	fromBlock := l.lastProcessed
	l.mu.Unlock()

	if fromBlock < safetyOverlap {
		fromBlock = 0
	} else {
		fromBlock = fromBlock - safetyOverlap
	}
	if fromBlock < l.cfg.StartBlock {
		fromBlock = l.cfg.StartBlock
	}
	if fromBlock >= toBlock {
		return
	}

	for chunkStart := fromBlock; chunkStart <= toBlock; chunkStart += batchSize {
		chunkEnd := chunkStart + batchSize - 1
		if chunkEnd > toBlock {
			chunkEnd = toBlock
		}

		query := ethereum.FilterQuery{
			FromBlock: new(big.Int).SetUint64(chunkStart),
			ToBlock:   new(big.Int).SetUint64(chunkEnd),
			Addresses: l.knownAddresses,
			Topics:    [][]common.Hash{allTopics},
		}

		logs, err := l.httpClient.FilterLogs(ctx, query)
		if err != nil {
			log.Error().Err(err).
				Uint64("from", chunkStart).
				Uint64("to", chunkEnd).
				Msg("getLogs query failed")
			continue
		}

		for _, vLog := range logs {
			l.handleLog(ctx, vLog)
		}

		log.Debug().
			Uint64("from", chunkStart).
			Uint64("to", chunkEnd).
			Int("events", len(logs)).
			Msg("Backfill chunk → Kafka")
	}

	l.advanceCheckpoint(toBlock)
}

/*═══════════════════════════════════════════════════════════════════════════════
   Shared — parse event → Kafka
   ═══════════════════════════════════════════════════════════════════════════════*/

func (l *Listener) handleLog(ctx context.Context, vLog types.Log) {
	var eventType model.EventType

	switch vLog.Topics[0] {
	case TopicDeposited:
		eventType = model.EventDeposit
	case TopicWithdrawn:
		eventType = model.EventWithdraw
	case TopicBorrowed:
		eventType = model.EventBorrow
	case TopicRepaid:
		eventType = model.EventRepay
	case TopicLiquidated:
		eventType = model.EventLiquidate
	default:
		return
	}

	userAddr := common.HexToAddress("")
	if len(vLog.Topics) > 1 {
		userAddr = common.BytesToAddress(vLog.Topics[1].Bytes())
	}

	event := &model.PoolEvent{
		PoolAddr:  vLog.Address.Hex(),
		EventType: eventType,
		UserAddr:  userAddr.Hex(),
		Amount:    new(big.Int).SetBytes(vLog.Data).String(),
		TxHash:    vLog.TxHash.Hex(),
		LogIndex:  vLog.Index,
		BlockHash: vLog.BlockHash.Hex(),
		BlockNum:  vLog.BlockNumber,
	}

	// → Kafka (split by event type), consumer handles safe-block + DB write
	if err := l.producer.Publish(ctx, event); err != nil {
		log.Error().Err(err).
			Str("tx", vLog.TxHash.Hex()).
			Str("type", string(eventType)).
			Msg("Failed to publish to Kafka")
	}
}

/*═══════════════════════════════════════════════════════════════════════════════
   Checkpoint — track which blocks have been fetched
   ═══════════════════════════════════════════════════════════════════════════════*/

func (l *Listener) loadCheckpoint() {
	last, err := l.svc.GetLastBlock(syncSourceName)
	if err != nil {
		last = l.cfg.StartBlock
		log.Info().Uint64("startBlock", last).Msg("No checkpoint found, using config start block")
	} else {
		log.Info().Uint64("lastBlock", last).Msg("Loaded checkpoint")
	}
	l.mu.Lock()
	l.lastProcessed = last
	l.mu.Unlock()
}

func (l *Listener) advanceCheckpoint(blockNum uint64) {
	l.mu.Lock()
	if blockNum > l.lastProcessed {
		l.lastProcessed = blockNum
	}
	current := l.lastProcessed
	l.mu.Unlock()

	go func() {
		if err := l.svc.UpsertLastBlock(syncSourceName, current); err != nil {
			log.Warn().Err(err).Uint64("block", current).Msg("Failed to persist checkpoint")
		}
	}()
}

/*═══════════════════════════════════════════════════════════════════════════════
   Chain reorganization detection + rollback
   ═══════════════════════════════════════════════════════════════════════════════*/

func (l *Listener) runReorgChecker(ctx context.Context) {
	if l.reorg == nil {
		return
	}

	ticker := time.NewTicker(reorgCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.mu.Lock()
			lastProcessed := l.lastProcessed
			l.mu.Unlock()

			result, err := l.reorg.Check(ctx, lastProcessed)
			if err != nil {
				log.Error().Err(err).Msg("Reorg check failed")
				continue
			}

			if !result.ReorgDetected {
				continue
			}

			// Reorg detected — execute coordinated rollback across all 4 layers.
			// The consumer is passed so its pending buffer (Layer 3) is cleared.
			forkBlock, err := l.reorg.Rollback(ctx, result.ForkBlock, l.consumer)
			if err != nil {
				log.Error().Err(err).Msg("Rollback failed")
				continue
			}

			// Layer 4: Reset listener checkpoint to fork point
			l.ResetCheckpoint(forkBlock)

			// Trigger immediate backfill to re-fetch canonical-chain events
			l.backfill(ctx)
		}
	}
}

// ResetCheckpoint rewinds the listener's lastProcessed to the fork point
// and persists it to DB. This is Layer 4 of the reorg rollback.
func (l *Listener) ResetCheckpoint(forkBlock uint64) {
	l.mu.Lock()
	l.lastProcessed = forkBlock
	current := l.lastProcessed
	l.mu.Unlock()

	if err := l.svc.UpsertLastBlock(syncSourceName, current); err != nil {
		log.Error().Err(err).Uint64("fork", forkBlock).Msg("Failed to persist reorg checkpoint reset")
	} else {
		log.Warn().Uint64("fork", forkBlock).Msg("Listener checkpoint reset to fork block")
	}
}
