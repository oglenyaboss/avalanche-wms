package consumer

import (
	"testing"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

func TestParse_ValidReceiving(t *testing.T) {
	eventID := uuid.New()
	productID := uuid.New()
	m := kafka.Message{
		Topic: "wms.receiving.v1",
		Key:   []byte(productID.String()),
		Headers: []kafka.Header{
			{Key: "id", Value: []byte(eventID.String())},
		},
	}
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
		t.Errorf("AggregateType: got %q", got.AggregateType)
	}
}

func TestParse_AllTopicsMap(t *testing.T) {
	for topic, want := range topicToAggregate {
		t.Run(topic, func(t *testing.T) {
			m := kafka.Message{
				Topic: topic,
				Key:   []byte(uuid.New().String()),
				Headers: []kafka.Header{
					{Key: "id", Value: []byte(uuid.New().String())},
				},
			}
			got, err := Parse(&m)
			if err != nil {
				t.Fatalf("Parse %q: %v", topic, err)
			}
			if got.AggregateType != want {
				t.Errorf("topic %q → want %q, got %q", topic, want, got.AggregateType)
			}
		})
	}
}

func TestParse_MissingHeader(t *testing.T) {
	m := kafka.Message{
		Topic: "wms.receiving.v1",
		Key:   []byte(uuid.New().String()),
	}
	if _, err := Parse(&m); err == nil {
		t.Fatal("expected error for missing 'id' header")
	}
}

func TestParse_InvalidEventIDUUID(t *testing.T) {
	m := kafka.Message{
		Topic: "wms.receiving.v1",
		Key:   []byte(uuid.New().String()),
		Headers: []kafka.Header{
			{Key: "id", Value: []byte("not-a-uuid")},
		},
	}
	if _, err := Parse(&m); err == nil {
		t.Fatal("expected error for invalid event id UUID")
	}
}

func TestParse_MissingKey(t *testing.T) {
	m := kafka.Message{
		Topic: "wms.receiving.v1",
		Headers: []kafka.Header{
			{Key: "id", Value: []byte(uuid.New().String())},
		},
	}
	if _, err := Parse(&m); err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestParse_UnknownTopic(t *testing.T) {
	m := kafka.Message{
		Topic: "wms.unknown.v1",
		Key:   []byte(uuid.New().String()),
		Headers: []kafka.Header{
			{Key: "id", Value: []byte(uuid.New().String())},
		},
	}
	if _, err := Parse(&m); err == nil {
		t.Fatal("expected error for unknown topic")
	}
}
