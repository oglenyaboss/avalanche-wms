//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
)

// eventsTopic is the unified WMS outbox topic the adapter consumes.
const eventsTopic = "wms.events.v1"

// kafkaBootstrap is the host-mapped Kafka listener (docker-compose maps 9092:9092).
const kafkaBootstrap = "localhost:9092"

// The broker advertises itself as kafka:9092 (KAFKA_ADVERTISED_LISTENERS), which is
// not resolvable from the host. We bootstrap against localhost:9092 and rewrite the
// advertised address back to 127.0.0.1 in the Transport.Dial hook, so an in-process
// kafka-go producer works from the host without an /etc/hosts entry or a throwaway
// kcat container. This is far faster than `docker run kcat` per publish (~1-2s each),
// which matters for N9 where two publishes of the same action must co-batch within
// one flush window.
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
			// key=action_id → stable partition (Hash balancer), matching the
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

// publishAction publishes a signed-action envelope to wms.events.v1 exactly like
// the Debezium EventRouter: key=actionId, headers id=actionId and
// aggregate_type=signed_action, value = the envelope JSON encoded as a JSON
// string (Debezium emits the payload column as a string).
func publishAction(t *testing.T, ctx context.Context, actionID uuid.UUID, payload []byte) {
	t.Helper()
	value, err := json.Marshal(string(payload))
	require.NoError(t, err)
	msg := kafka.Message{
		Key:   []byte(actionID.String()),
		Value: value,
		Headers: []kafka.Header{
			{Key: "id", Value: []byte(actionID.String())},
			{Key: "aggregate_type", Value: []byte("signed_action")},
		},
	}
	wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	require.NoErrorf(t, eventsWriter().WriteMessages(wctx, msg),
		"publish signed action %s", actionID)
}

// adminReceive builds a Receive of n fresh items signed by the consortium admin
// key; it is applied on chain when published (an ADMIN key may sign warehouse
// actions, ADR-0001 §1). Publish the returned payload with publishAction.
func adminReceive(t *testing.T, ctx context.Context, env *env, n int) (actionID uuid.UUID, items []uuid.UUID, payload []byte) {
	t.Helper()
	org := requireConsortium(t, ctx, env)
	actionID, items, _, payload = receiveEnvelope(t, org.adminKey, softKeyID(softCoordinates(org.adminKey)), n)
	return actionID, items, payload
}

// closeEventsWriter flushes and closes the shared writer. Wired from TestMain-adjacent
// teardown is unnecessary (process exits), but tests may call it; safe to skip.
func closeEventsWriter() {
	if kafkaWriter != nil {
		_ = kafkaWriter.Close()
	}
}
