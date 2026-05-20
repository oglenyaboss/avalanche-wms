package consumer

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Message — распарсенное kafka-сообщение с идентификаторами event/product и
// типом агрегата (из Kafka header aggregate_type, проставленного Debezium
// EventRouter). KafkaMsg храним полностью для commit-of-offset и DLQ-republish.
//
// Передавать только через pointer (gocritic hugeParam: kafka.Message ~152 байт).
type Message struct {
	EventID       uuid.UUID
	ProductID     uuid.UUID
	AggregateType string // "receiving"/"putaway"/"picking"/"shipping"
	Topic         string
	KafkaMsg      kafka.Message
}

// validAggregates derived from fsmOrder — single source of truth for known types.
var validAggregates = func() map[string]bool {
	m := make(map[string]bool, len(fsmOrder))
	for _, a := range fsmOrder {
		m[a] = true
	}
	return m
}()

// Parse извлекает EventID из header "id", ProductID из m.Key (Debezium outbox
// aggregate_id), AggregateType из header "aggregate_type" (добавлен через
// transforms.outbox.table.fields.additional.placement). Невалидное сообщение —
// error; вызывающий должен коммитнуть offset и отправить в DLQ.
func Parse(m *kafka.Message) (*Message, error) {
	eventIDStr := headerValue(m.Headers, "id")
	if eventIDStr == "" {
		return nil, fmt.Errorf("missing 'id' header (expected event_id UUID)")
	}
	eventID, err := uuid.Parse(eventIDStr)
	if err != nil {
		return nil, fmt.Errorf("parse event_id %q: %w", eventIDStr, err)
	}

	if len(m.Key) == 0 {
		return nil, fmt.Errorf("missing kafka key (expected product_id UUID)")
	}
	productID, err := uuid.Parse(string(m.Key))
	if err != nil {
		return nil, fmt.Errorf("parse product_id %q: %w", string(m.Key), err)
	}

	agg := headerValue(m.Headers, "aggregate_type")
	if agg == "" {
		return nil, fmt.Errorf("missing 'aggregate_type' header")
	}
	if !validAggregates[agg] {
		return nil, fmt.Errorf("unknown aggregate_type: %s", agg)
	}

	return &Message{
		EventID:       eventID,
		ProductID:     productID,
		AggregateType: agg,
		Topic:         m.Topic,
		KafkaMsg:      *m,
	}, nil
}

func headerValue(headers []kafka.Header, key string) string {
	for _, h := range headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}
