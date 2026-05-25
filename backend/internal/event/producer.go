package event

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/segmentio/kafka-go"
	"github.com/silo-protocol/backend/internal/config"
	"github.com/silo-protocol/backend/internal/model"
)

// topicEventTypes maps each event type to its own Kafka topic suffix.
// Produces topics like: silo.deposit, silo.borrow, silo.repay, silo.liquidate
var topicEventTypes = map[model.EventType]string{
	model.EventDeposit:   "deposit",
	model.EventWithdraw:  "withdraw",
	model.EventBorrow:    "borrow",
	model.EventRepay:     "repay",
	model.EventLiquidate: "liquidate",
}

// KafkaProducer publishes pool events to per-event-type Kafka topics.
// Partition key = pool address, ensuring ordered delivery per pool.
type KafkaProducer struct {
	writer *kafka.Writer
	prefix string // topic prefix, e.g. "silo"
}

func NewKafkaProducer(cfg *config.Config) (*KafkaProducer, error) {
	brokers := strings.Split(cfg.KafkaBrokers, ",")
	if len(brokers) == 1 && brokers[0] == "" {
		return nil, fmt.Errorf("no Kafka brokers configured")
	}

	writer := &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Balancer:     &kafka.Hash{}, // hash by key → same pool → same partition
		BatchSize:    100,
		BatchTimeout: 0, // flush immediately (real-time events)
		RequiredAcks: kafka.RequireOne,
		Compression:  kafka.Snappy,
	}

	// Auto-create topics for each event type
	prefix := strings.TrimRight(cfg.KafkaTopicPrefix, ".")
	if err := ensureTopics(brokers, prefix, topicEventTypes); err != nil {
		log.Warn().Err(err).Msg("Failed to auto-create Kafka topics (may already exist)")
	}

	log.Info().
		Strs("brokers", brokers).
		Str("prefix", prefix).
		Msg("Kafka producer initialized")

	return &KafkaProducer{writer: writer, prefix: prefix}, nil
}

// Publish sends an event to its corresponding topic.
func (p *KafkaProducer) Publish(ctx context.Context, event *model.PoolEvent) error {
	suffix, ok := topicEventTypes[event.EventType]
	if !ok {
		return fmt.Errorf("unknown event type: %s", event.EventType)
	}
	topic := fmt.Sprintf("%s.%s", p.prefix, suffix)

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	return p.writer.WriteMessages(ctx, kafka.Message{
		Topic: topic,
		Key:   []byte(event.PoolAddr), // partition by pool for ordering
		Value: payload,
	})
}

func (p *KafkaProducer) Close() error {
	return p.writer.Close()
}

// ensureTopics creates topics if they don't already exist.
func ensureTopics(brokers []string, prefix string, types map[model.EventType]string) error {
	conn, err := kafka.Dial("tcp", brokers[0])
	if err != nil {
		return err
	}
	defer conn.Close()

	controller, err := conn.Controller()
	if err != nil {
		return err
	}

	ctrlConn, err := kafka.Dial("tcp", controller.Host)
	if err != nil {
		return err
	}
	defer ctrlConn.Close()

	for _, suffix := range types {
		topic := fmt.Sprintf("%s.%s", prefix, suffix)
		if err := ctrlConn.CreateTopics(kafka.TopicConfig{
			Topic:             topic,
			NumPartitions:     4,
			ReplicationFactor: 1,
		}); err != nil {
			// Topic might already exist — log and continue
			log.Debug().Str("topic", topic).Err(err).Msg("Topic creation skipped")
		}
	}
	return nil
}
