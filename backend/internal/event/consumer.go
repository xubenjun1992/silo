package event

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/rs/zerolog/log"
	"github.com/segmentio/kafka-go"
	"github.com/silo-protocol/backend/internal/config"
	"github.com/silo-protocol/backend/internal/model"
	"github.com/silo-protocol/backend/internal/service"
)

const (
	consumerGroupID      = "silo-consumer"
	consumerPollInterval = 3 * time.Second
)

// OnEventConfirmed is called after an event is safely persisted to DB.
// Used by the liquidation tracker to update Redis positions.
type OnEventConfirmed func(event *model.PoolEvent)

type KafkaConsumer struct {
	cfg         *config.Config
	httpClient  *ethclient.Client
	reader      *kafka.Reader
	svc         *service.PoolService
	onConfirmed OnEventConfirmed
	pending     map[string][]pendingEvent
}

type pendingEvent struct {
	event     *model.PoolEvent
	partition int
	offset    int64
}

func NewKafkaConsumer(cfg *config.Config, onConfirmed OnEventConfirmed) (*KafkaConsumer, error) {
	httpClient, err := ethclient.Dial(cfg.HttpRpcUrl)
	if err != nil {
		return nil, fmt.Errorf("consumer http client: %w", err)
	}

	brokers := strings.Split(cfg.KafkaBrokers, ",")
	prefix := strings.TrimRight(cfg.KafkaTopicPrefix, ".")

	var topics []string
	for _, suffix := range topicEventTypes {
		topics = append(topics, fmt.Sprintf("%s.%s", prefix, suffix))
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     brokers,
		GroupID:     consumerGroupID,
		GroupTopics: topics,
		MinBytes:    1,
		MaxBytes:    10e6,
		MaxWait:     consumerPollInterval,
	})

	log.Info().
		Strs("brokers", brokers).
		Strs("topics", topics).
		Str("group", consumerGroupID).
		Msg("Kafka consumer initialized")

	return &KafkaConsumer{
		cfg:         cfg,
		httpClient:  httpClient,
		reader:      reader,
		svc:         service.NewPoolService(),
		onConfirmed: onConfirmed,
		pending:     make(map[string][]pendingEvent),
	}, nil
}

/*═══════════════════════════════════════════════════════════════════════════════
   PUBLIC — main loop
   ═══════════════════════════════════════════════════════════════════════════════*/

func (c *KafkaConsumer) Start(ctx context.Context) {
	log.Info().
		Uint64("confirmations", c.cfg.SafeConfirmations).
		Msg("Kafka consumer starting (safe-block gated)")

	flushTicker := time.NewTicker(10 * time.Second)
	defer flushTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.reader.Close()
			return

		case <-flushTicker.C:
			c.flushPending(ctx)

		default:
			c.consumeOne(ctx)
		}
	}
}

/*═══════════════════════════════════════════════════════════════════════════════
   Core — read, gate by safe block, write, notify
   ═══════════════════════════════════════════════════════════════════════════════*/

func (c *KafkaConsumer) consumeOne(ctx context.Context) {
	msg, err := c.reader.FetchMessage(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		log.Error().Err(err).Msg("Kafka fetch error")
		time.Sleep(time.Second)
		return
	}

	event, err := parseEvent(msg.Value)
	if err != nil {
		log.Warn().Err(err).Str("topic", msg.Topic).Msg("Failed to parse event, skipping")
		c.commit(msg)
		return
	}

	safeBlock, err := c.safeBlock(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get safe block, retrying later")
		return
	}

	if event.BlockNum <= safeBlock {
		if err := c.svc.InsertEventIgnoreDup(event); err != nil {
			log.Error().Err(err).
				Str("tx", event.TxHash).
				Str("type", string(event.EventType)).
				Msg("DB insert failed")
			return
		}
		c.commit(msg)
		c.notify(event) // → PositionTracker (Redis)
	} else {
		c.park(event, msg.Partition, msg.Offset)
	}
}

// flushPending checks all parked events and commits those whose block is now safe.
func (c *KafkaConsumer) flushPending(ctx context.Context) {
	if len(c.pending) == 0 {
		return
	}

	safeBlock, err := c.safeBlock(ctx)
	if err != nil {
		return
	}

	var remaining int
	for poolAddr, events := range c.pending {
		var kept []pendingEvent
		for _, pe := range events {
			if pe.event.BlockNum <= safeBlock {
				if err := c.svc.InsertEventIgnoreDup(pe.event); err != nil {
					log.Error().Err(err).Str("tx", pe.event.TxHash).Msg("Flush insert failed")
					kept = append(kept, pe)
					continue
				}
				c.reader.CommitMessages(ctx, kafka.Message{
					Topic:     topicForEvent(c.cfg.KafkaTopicPrefix, pe.event.EventType),
					Partition: pe.partition,
					Offset:    pe.offset,
				})
				c.notify(pe.event) // → PositionTracker (Redis)
			} else {
				kept = append(kept, pe)
				remaining++
			}
		}
		if len(kept) == 0 {
			delete(c.pending, poolAddr)
		} else {
			c.pending[poolAddr] = kept
		}
	}

	if remaining > 0 {
		log.Debug().Int("remaining", remaining).Msg("Pending events still waiting for confirmations")
	}
}

/*═══════════════════════════════════════════════════════════════════════════════
   Helpers
   ═══════════════════════════════════════════════════════════════════════════════*/

func (c *KafkaConsumer) notify(event *model.PoolEvent) {
	if c.onConfirmed != nil {
		c.onConfirmed(event)
	}
}

func (c *KafkaConsumer) safeBlock(ctx context.Context) (uint64, error) {
	latest, err := c.httpClient.BlockNumber(ctx)
	if err != nil {
		return 0, err
	}
	confirmations := c.cfg.SafeConfirmations
	if latest <= confirmations {
		return 0, nil
	}
	return latest - confirmations, nil
}

// handleReorg clears all pending events from blocks beyond the fork point.
// Called by ReorgHandler.Rollback during Layer 3 cleanup.
// Committed events on orphaned blocks are handled by Layer 1 (DB rollback);
// this only clears the in-memory pending buffer so no stale events get
// persisted on the next flush.
func (c *KafkaConsumer) handleReorg(ctx context.Context, forkBlock uint64) {
	cleared := 0
	for poolAddr, events := range c.pending {
		var kept []pendingEvent
		for _, pe := range events {
			if pe.event.BlockNum > forkBlock {
				cleared++
			} else {
				kept = append(kept, pe)
			}
		}
		if len(kept) == 0 {
			delete(c.pending, poolAddr)
		} else {
			c.pending[poolAddr] = kept
		}
	}
	if cleared > 0 {
		log.Warn().
			Uint64("forkBlock", forkBlock).
			Int("cleared", cleared).
			Msg("Cleared pending events from orphaned blocks")
	}
}

func (c *KafkaConsumer) park(event *model.PoolEvent, partition int, offset int64) {
	c.pending[event.PoolAddr] = append(c.pending[event.PoolAddr], pendingEvent{
		event:     event,
		partition: partition,
		offset:    offset,
	})
}

func (c *KafkaConsumer) commit(msg kafka.Message) {
	if err := c.reader.CommitMessages(context.Background(), msg); err != nil {
		log.Warn().Err(err).Msg("Offset commit failed")
	}
}

func parseEvent(data []byte) (*model.PoolEvent, error) {
	var event model.PoolEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, err
	}
	return &event, nil
}

func topicForEvent(prefix string, eventType model.EventType) string {
	prefix = strings.TrimRight(prefix, ".")
	suffix, ok := topicEventTypes[eventType]
	if !ok {
		return ""
	}
	return fmt.Sprintf("%s.%s", prefix, suffix)
}
