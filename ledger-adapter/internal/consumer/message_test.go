package consumer

import (
	"testing"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

func makeKafkaMsg(topic, eventID, productID, aggregateType string) kafka.Message {
	headers := []kafka.Header{
		{Key: "id", Value: []byte(eventID)},
	}
	if aggregateType != "" {
		headers = append(headers, kafka.Header{Key: "aggregate_type", Value: []byte(aggregateType)})
	}
	return kafka.Message{
		Topic:   topic,
		Key:     []byte(productID),
		Headers: headers,
	}
}

func TestParse_ValidReceiving(t *testing.T) {
	eventID := uuid.New()
	productID := uuid.New()
	m := makeKafkaMsg("wms.events.v1", eventID.String(), productID.String(), "receiving")

	got, err := Parse(&m)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.EventID != eventID {
		t.Errorf("EventID: got %s, want %s", got.EventID, eventID)
	}
	if got.ProductID != productID {
		t.Errorf("ProductID: got %s, want %s", got.ProductID, productID)
	}
	if got.AggregateType != "receiving" {
		t.Errorf("AggregateType: got %q, want %q", got.AggregateType, "receiving")
	}
}

func TestParse_AllAggregateTypes(t *testing.T) {
	for _, agg := range []string{"receiving", "putaway", "picking", "shipping"} {
		t.Run(agg, func(t *testing.T) {
			m := makeKafkaMsg("wms.events.v1", uuid.New().String(), uuid.New().String(), agg)
			got, err := Parse(&m)
			if err != nil {
				t.Fatalf("Parse %q: %v", agg, err)
			}
			if got.AggregateType != agg {
				t.Errorf("want %q, got %q", agg, got.AggregateType)
			}
		})
	}
}

func TestParse_MissingIDHeader(t *testing.T) {
	m := kafka.Message{
		Topic: "wms.events.v1",
		Key:   []byte(uuid.New().String()),
		Headers: []kafka.Header{
			{Key: "aggregate_type", Value: []byte("receiving")},
		},
	}
	if _, err := Parse(&m); err == nil {
		t.Fatal("expected error for missing 'id' header")
	}
}

func TestParse_MissingAggregateTypeHeader(t *testing.T) {
	m := kafka.Message{
		Topic: "wms.events.v1",
		Key:   []byte(uuid.New().String()),
		Headers: []kafka.Header{
			{Key: "id", Value: []byte(uuid.New().String())},
		},
	}
	if _, err := Parse(&m); err == nil {
		t.Fatal("expected error for missing 'aggregate_type' header")
	}
}

func TestParse_InvalidEventIDUUID(t *testing.T) {
	m := makeKafkaMsg("wms.events.v1", "not-a-uuid", uuid.New().String(), "receiving")
	if _, err := Parse(&m); err == nil {
		t.Fatal("expected error for invalid event id UUID")
	}
}

func TestParse_MissingKey(t *testing.T) {
	m := kafka.Message{
		Topic: "wms.events.v1",
		Headers: []kafka.Header{
			{Key: "id", Value: []byte(uuid.New().String())},
			{Key: "aggregate_type", Value: []byte("receiving")},
		},
	}
	if _, err := Parse(&m); err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestParse_UnknownAggregateType(t *testing.T) {
	m := makeKafkaMsg("wms.events.v1", uuid.New().String(), uuid.New().String(), "unknown")
	if _, err := Parse(&m); err == nil {
		t.Fatal("expected error for unknown aggregate_type")
	}
}
