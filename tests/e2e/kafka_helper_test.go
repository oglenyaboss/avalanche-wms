//go:build e2e

package e2e

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
)

// eventsTopic is the unified WMS outbox topic the adapter consumes. Mirrors
// lib/kafka.sh EVENTS_TOPIC.
const eventsTopic = "wms.events.v1"

// kafkaBootstrap is the host-mapped Kafka listener (docker-compose maps 9092:9092).
const kafkaBootstrap = "localhost:9092"

// The broker advertises itself as kafka:9092 (KAFKA_ADVERTISED_LISTENERS), which is
// not resolvable from the host. We bootstrap against localhost:9092 and rewrite the
// advertised address back to 127.0.0.1 in the Transport.Dial hook, so an in-process
// kafka-go producer works from the host without an /etc/hosts entry or a throwaway
// kcat container. This is far faster than `docker run kcat` per publish (~1-2s each),
// which matters for S3/N9 where several publishes must co-batch within one flush
// window.
const advertisedBroker = "kafka:9092"
const dialTarget = "127.0.0.1:9092"

var (
	kafkaWriterOnce sync.Once
	kafkaWriter     *kafka.Writer
)

// eventsWriter returns a process-wide kafka.Writer for the events topic. A single
// shared writer keeps per-publish latency in the millisecond range so rapid-fire
// publishes land in one adapter flush window.
func eventsWriter() *kafka.Writer {
	kafkaWriterOnce.Do(func() {
		kafkaWriter = &kafka.Writer{
			Addr:  kafka.TCP(kafkaBootstrap),
			Topic: eventsTopic,
			// key=product_id → stable partition (Hash balancer), matching the
			// Debezium EventRouter routing the adapter expects. Same key always
			// lands on the same partition, so an intra-window duplicate (N9) is
			// guaranteed in-order on one partition.
			Balancer:     &kafka.Hash{},
			RequiredAcks: kafka.RequireAll,
			BatchTimeout: 5 * time.Millisecond,
			Transport: &kafka.Transport{
				Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
					if addr == advertisedBroker {
						addr = dialTarget
					}
					return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, addr)
				},
			},
		}
	})
	return kafkaWriter
}

// publishEvent publishes one outbox-style event to wms.events.v1 exactly like
// lib/kafka.sh publish_event: key=product_id, headers id=<event_id> and
// aggregate_type=<receiving|putaway|picking|shipping>, value={} (or payload).
// aggregateType is one of "receiving", "putaway", "picking", "shipping".
func publishEvent(t *testing.T, ctx context.Context, aggregateType string, eventID, productID uuid.UUID, payload string) {
	t.Helper()
	if payload == "" {
		payload = "{}"
	}
	msg := kafka.Message{
		Key:   []byte(productID.String()),
		Value: []byte(payload),
		Headers: []kafka.Header{
			{Key: "id", Value: []byte(eventID.String())},
			{Key: "aggregate_type", Value: []byte(aggregateType)},
		},
	}
	wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	require.NoErrorf(t, eventsWriter().WriteMessages(wctx, msg),
		"publish %s event %s (product %s)", aggregateType, eventID, productID)
}

// closeEventsWriter flushes and closes the shared writer. Wired from TestMain-adjacent
// teardown is unnecessary (process exits), but tests may call it; safe to skip.
func closeEventsWriter() {
	if kafkaWriter != nil {
		_ = kafkaWriter.Close()
	}
}
