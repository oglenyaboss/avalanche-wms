package consumer

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/segmentio/kafka-go"
)

// TestConsumer_ParseFail_GoesToDLQ проверяет что handleParseFailure публикует
// kafka-сообщение в DLQ с reason-префиксом "parse failed:". Это делает H5:
// раньше parse-fail тихо коммитился без DLQ → сообщение терялось.
func TestConsumer_ParseFail_GoesToDLQ(t *testing.T) {
	dq := &stubDLQ{}
	c := &Consumer{
		dlq:   dq,
		topic: "wms.events.v1",
		log:   slog.Default(),
	}

	// Сообщение без header 'id' — Parse вернёт ошибку.
	m := &kafka.Message{
		Topic:   "wms.events.v1",
		Key:     []byte("some-key"),
		Value:   []byte(`{"foo":"bar"}`),
		Offset:  42,
		Headers: []kafka.Header{},
	}

	err := c.handleParseFailure(context.Background(), m, errors.New("missing 'id' header"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dq.messages) != 1 {
		t.Fatalf("expected 1 DLQ publish, got %d", len(dq.messages))
	}
	if len(dq.messages[0]) != 1 {
		t.Errorf("expected 1 message in DLQ batch, got %d", len(dq.messages[0]))
	}
	stub := dq.messages[0][0]
	if stub.Topic != "wms.events.v1" {
		t.Errorf("Topic: got %q", stub.Topic)
	}
	if string(stub.KafkaMsg.Value) != `{"foo":"bar"}` {
		t.Errorf("KafkaMsg.Value payload должен сохраниться: got %q", stub.KafkaMsg.Value)
	}
	if len(dq.reasons) != 1 || !strings.HasPrefix(dq.reasons[0], "parse failed:") {
		t.Errorf("reason should start with 'parse failed:', got %q", dq.reasons)
	}
}

func TestConsumer_ParseFail_DLQDown_ReturnsError(t *testing.T) {
	// Когда DLQ сам недоступен — handleParseFailure возвращает ошибку,
	// caller должен НЕ коммитить offset.
	dq := &stubDLQ{publishErr: errors.New("kafka broker down")}
	c := &Consumer{
		dlq:   dq,
		topic: "wms.events.v1",
		log:   slog.Default(),
	}
	m := &kafka.Message{Topic: "wms.events.v1"}

	err := c.handleParseFailure(context.Background(), m, errors.New("parse err"))
	if err == nil {
		t.Fatal("DLQ down: expected error propagation, got nil")
	}
}
