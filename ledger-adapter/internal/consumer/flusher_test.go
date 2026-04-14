package consumer

import (
	"context"
	"errors"
	"log/slog"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// --- моки interfaces -----------------------------------------------------

type stubChain struct {
	mu          sync.Mutex
	calls       []string
	callErr     error
	receipt     *types.Receipt
	receiptErr  error
	receiptWait int // сколько раз возвращать NotFound до успеха
}

func (s *stubChain) BatchCall(_ context.Context, topic string, _, _ []*big.Int) (common.Hash, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, topic)
	if s.callErr != nil {
		return common.Hash{}, s.callErr
	}
	return common.HexToHash("0xdeadbeef"), nil
}

func (s *stubChain) TransactionReceipt(_ context.Context, _ common.Hash) (*types.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.receiptErr != nil {
		return nil, s.receiptErr
	}
	if s.receiptWait > 0 {
		s.receiptWait--
		return nil, ethereum.NotFound
	}
	return s.receipt, nil
}

type stubStore struct {
	mu        sync.Mutex
	exists    map[uuid.UUID]bool
	inserted  map[uuid.UUID]string
	sent      map[uuid.UUID]string
	committed map[uuid.UUID]bool
	failed    map[uuid.UUID]string
	existsErr error
	insertErr error
}

func newStubStore() *stubStore {
	return &stubStore{
		exists:    map[uuid.UUID]bool{},
		inserted:  map[uuid.UUID]string{},
		sent:      map[uuid.UUID]string{},
		committed: map[uuid.UUID]bool{},
		failed:    map[uuid.UUID]string{},
	}
}

func (s *stubStore) Exists(_ context.Context, id uuid.UUID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.existsErr != nil {
		return false, s.existsErr
	}
	return s.exists[id], nil
}

func (s *stubStore) InsertPending(_ context.Context, id uuid.UUID, agg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.insertErr != nil {
		return s.insertErr
	}
	s.inserted[id] = agg
	s.exists[id] = true
	return nil
}

func (s *stubStore) MarkSent(_ context.Context, ids []uuid.UUID, txHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.sent[id] = txHash
	}
	return nil
}

func (s *stubStore) MarkCommitted(_ context.Context, ids []uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.committed[id] = true
	}
	return nil
}

func (s *stubStore) MarkFailed(_ context.Context, ids []uuid.UUID, _, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.failed[id] = reason
	}
	return nil
}

type stubDLQ struct {
	mu       sync.Mutex
	messages [][]*Message
	reasons  []string
}

func (d *stubDLQ) PublishAll(_ context.Context, msgs []*Message, reason string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.messages = append(d.messages, msgs)
	d.reasons = append(d.reasons, reason)
	return nil
}

// --- helpers -------------------------------------------------------------

func newFlusherT() (*Flusher, *stubChain, *stubStore, *stubDLQ) {
	ch := &stubChain{receipt: &types.Receipt{Status: 1}}
	st := newStubStore()
	dq := &stubDLQ{}
	f := NewFlusher(ch, st, dq, 2*time.Second, slog.Default())
	return f, ch, st, dq
}

func makeMessages(n int, topic, aggType string) []*Message {
	msgs := make([]*Message, n)
	for i := range msgs {
		msgs[i] = &Message{
			EventID:       uuid.New(),
			ProductID:     uuid.New(),
			AggregateType: aggType,
			Topic:         topic,
			KafkaMsg:      kafka.Message{Topic: topic},
		}
	}
	return msgs
}

// --- tests ---------------------------------------------------------------

func TestFlusher_HappyPath(t *testing.T) {
	f, ch, st, dq := newFlusherT()
	msgs := makeMessages(3, "wms.receiving.v1", "receiving")

	if err := f.Flush(context.Background(), "wms.receiving.v1", msgs); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(ch.calls) != 1 || ch.calls[0] != "wms.receiving.v1" {
		t.Errorf("expected 1 BatchCall to wms.receiving.v1, got %v", ch.calls)
	}
	if len(st.committed) != 3 {
		t.Errorf("expected 3 committed, got %d", len(st.committed))
	}
	if len(st.failed) != 0 {
		t.Errorf("no failures expected, got %d", len(st.failed))
	}
	if len(dq.messages) != 0 {
		t.Errorf("no DLQ publishes expected, got %d", len(dq.messages))
	}
}

func TestFlusher_Idempotency_SkipsAllExisting(t *testing.T) {
	f, ch, st, dq := newFlusherT()
	msgs := makeMessages(2, "wms.receiving.v1", "receiving")
	// Пометим как existing
	for _, m := range msgs {
		st.exists[m.EventID] = true
	}

	if err := f.Flush(context.Background(), "wms.receiving.v1", msgs); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(ch.calls) != 0 {
		t.Errorf("no chain call expected for all-existing batch, got %d", len(ch.calls))
	}
	if len(dq.messages) != 0 {
		t.Errorf("no DLQ expected, got %d", len(dq.messages))
	}
}

func TestFlusher_ChainCallFails_GoesToDLQ(t *testing.T) {
	f, ch, st, dq := newFlusherT()
	ch.callErr = errors.New("execution reverted: Duplicate eventId")
	msgs := makeMessages(2, "wms.putaway.v1", "putaway")

	if err := f.Flush(context.Background(), "wms.putaway.v1", msgs); err != nil {
		t.Fatalf("chain revert should NOT return error (offsets must commit): %v", err)
	}
	if len(st.failed) != 2 {
		t.Errorf("expected 2 MarkFailed, got %d", len(st.failed))
	}
	if len(dq.messages) != 1 || len(dq.messages[0]) != 2 {
		t.Errorf("expected 1 DLQ batch of 2, got %v", dq.messages)
	}
	if len(st.committed) != 0 {
		t.Errorf("no commits expected on chain fail, got %d", len(st.committed))
	}
}

func TestFlusher_ReceiptStatusZero_GoesToDLQ(t *testing.T) {
	f, ch, st, dq := newFlusherT()
	ch.receipt = &types.Receipt{Status: 0} // reverted on-chain
	msgs := makeMessages(1, "wms.picking.v1", "picking")

	if err := f.Flush(context.Background(), "wms.picking.v1", msgs); err != nil {
		t.Fatalf("revert should NOT return error: %v", err)
	}
	if len(st.failed) != 1 {
		t.Errorf("expected 1 MarkFailed, got %d", len(st.failed))
	}
	if len(dq.messages) != 1 {
		t.Errorf("expected 1 DLQ publish, got %d", len(dq.messages))
	}
}

func TestFlusher_StoreExistsError_ReturnsTransient(t *testing.T) {
	f, _, st, _ := newFlusherT()
	st.existsErr = errors.New("connection refused")
	msgs := makeMessages(1, "wms.receiving.v1", "receiving")

	err := f.Flush(context.Background(), "wms.receiving.v1", msgs)
	if err == nil {
		t.Fatal("store error should propagate (transient, retry)")
	}
}

func TestFlusher_EmptyBatch_NoOp(t *testing.T) {
	f, ch, st, _ := newFlusherT()
	if err := f.Flush(context.Background(), "wms.receiving.v1", nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	if len(ch.calls) != 0 || len(st.inserted) != 0 {
		t.Error("empty batch should be pure no-op")
	}
}

func TestFlusher_PartialDuplicates_ProcessesOnlyNew(t *testing.T) {
	f, ch, st, _ := newFlusherT()
	msgs := makeMessages(3, "wms.shipping.v1", "shipping")
	// Первые 2 уже existing
	st.exists[msgs[0].EventID] = true
	st.exists[msgs[1].EventID] = true

	if err := f.Flush(context.Background(), "wms.shipping.v1", msgs); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(ch.calls) != 1 {
		t.Errorf("expected 1 chain call for the one new msg, got %d", len(ch.calls))
	}
	if len(st.committed) != 1 {
		t.Errorf("expected 1 commit (only new event), got %d", len(st.committed))
	}
}
