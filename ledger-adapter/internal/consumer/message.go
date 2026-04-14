package consumer

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Message — распарсенное kafka-сообщение с идентификаторами event/product и
// типом агрегата (derivable от topic'а). KafkaMsg храним полностью для commit-
// of-offset и DLQ-republish (оригинальный headers + value).
//
// Передавать только через pointer (gocritic hugeParam: kafka.Message ~152 байт).
type Message struct {
	EventID       uuid.UUID
	ProductID     uuid.UUID
	AggregateType string // "receiving"/"putaway"/"picking"/"shipping"
	Topic         string
	KafkaMsg      kafka.Message
}

// topicToAggregate — mapping WMS-topic → aggregate_type для onchain_events и
// для диспатча в chain.BatchCall. Держим здесь (в consumer), а не в chain,
// потому что aggregate_type — поле persistent слоя (БД), а не chain-специфичное.
var topicToAggregate = map[string]string{
	"wms.receiving.v1": "receiving",
	"wms.putaway.v1":   "putaway",
	"wms.picking.v1":   "picking",
	"wms.shipping.v1":  "shipping",
}

// Parse извлекает EventID из header "id", ProductID из m.Key (Debezium outbox
// aggregate_id), AggregateType из topic'а. Невалидное сообщение — error;
// вызывающий должен коммитнуть offset и отправить в DLQ.
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

	agg, ok := topicToAggregate[m.Topic]
	if !ok {
		return nil, fmt.Errorf("unknown topic: %s", m.Topic)
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
