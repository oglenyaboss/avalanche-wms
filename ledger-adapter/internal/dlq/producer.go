package dlq

import (
	"context"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"

	"ledger-adapter/internal/consumer"
)

// Producer публикует failed batch'и в DLQ-topic. Используется flusher'ом через
// consumer.DLQPublisher interface.
type Producer struct {
	w *kafka.Writer
}

// NewProducer создаёт writer с RequireAll acks для durability — DLQ не должен
// терять сообщения даже если broker crashes.
func NewProducer(brokers []string, topic string) *Producer {
	return &Producer{w: &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Topic:        topic,
		Balancer:     &kafka.Hash{},
		RequiredAcks: kafka.RequireAll,
	}}
}

// Close закрывает writer. Должен вызываться при shutdown.
func (p *Producer) Close() error { return p.w.Close() }

// PublishAll отправляет все msgs в DLQ topic с метаданными:
//   - original_topic: откуда пришло сообщение
//   - reason: почему зафейлилось
//   - failed_at: ISO-8601 timestamp
//
// Original payload (key/value/headers) сохраняется как есть — чтобы downstream
// consumer мог re-process после фикса.
func (p *Producer) PublishAll(ctx context.Context, msgs []*consumer.Message, reason string) error {
	if len(msgs) == 0 {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	km := make([]kafka.Message, len(msgs))
	for i, m := range msgs {
		headers := make([]kafka.Header, 0, len(m.KafkaMsg.Headers)+3)
		headers = append(headers, m.KafkaMsg.Headers...)
		headers = append(headers,
			kafka.Header{Key: "original_topic", Value: []byte(m.Topic)},
			kafka.Header{Key: "reason", Value: []byte(reason)},
			kafka.Header{Key: "failed_at", Value: []byte(now)},
		)
		km[i] = kafka.Message{
			Key:     m.KafkaMsg.Key,
			Value:   m.KafkaMsg.Value,
			Headers: headers,
		}
	}
	if err := p.w.WriteMessages(ctx, km...); err != nil {
		return fmt.Errorf("dlq publish %d messages: %w", len(msgs), err)
	}
	return nil
}
