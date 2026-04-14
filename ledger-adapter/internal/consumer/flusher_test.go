package consumer

import (
	"context"
	"errors"
	"fmt"
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

	"ledger-adapter/internal/chain"
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
	statuses  map[uuid.UUID]string // "PENDING"/"SENT"/"COMMITTED"/"FAILED"
	inserted  map[uuid.UUID]string
	sent      map[uuid.UUID]string
	committed map[uuid.UUID]bool
	failed    map[uuid.UUID]string
	existsErr error
	statusErr error
	insertErr error
	failedErr error
}

func newStubStore() *stubStore {
	return &stubStore{
		exists:    map[uuid.UUID]bool{},
		statuses:  map[uuid.UUID]string{},
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

func (s *stubStore) Status(_ context.Context, id uuid.UUID) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.statusErr != nil {
		return "", s.statusErr
	}
	if st, ok := s.statuses[id]; ok {
		return st, nil
	}
	// Дефолт для existing row без явного статуса — PENDING (retry-able).
	return "PENDING", nil
}

func (s *stubStore) InsertPending(_ context.Context, id uuid.UUID, agg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.insertErr != nil {
		return s.insertErr
	}
	s.inserted[id] = agg
	s.exists[id] = true
	s.statuses[id] = "PENDING"
	return nil
}

func (s *stubStore) MarkSent(_ context.Context, ids []uuid.UUID, txHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.sent[id] = txHash
		s.statuses[id] = "SENT"
	}
	return nil
}

func (s *stubStore) MarkCommitted(_ context.Context, ids []uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.committed[id] = true
		s.statuses[id] = "COMMITTED"
	}
	return nil
}

func (s *stubStore) MarkFailed(_ context.Context, ids []uuid.UUID, _, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failedErr != nil {
		return s.failedErr
	}
	for _, id := range ids {
		s.failed[id] = reason
		s.statuses[id] = "FAILED"
	}
	return nil
}

type stubDLQ struct {
	mu         sync.Mutex
	messages   [][]*Message
	reasons    []string
	publishErr error
}

func (d *stubDLQ) PublishAll(_ context.Context, msgs []*Message, reason string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.publishErr != nil {
		return d.publishErr
	}
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
	// Пометим как existing + COMMITTED — terminal статус, должны скипнуться.
	for _, m := range msgs {
		st.exists[m.EventID] = true
		st.statuses[m.EventID] = "COMMITTED"
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
	// Первые 2 уже existing в terminal COMMITTED — скипнутся.
	st.exists[msgs[0].EventID] = true
	st.statuses[msgs[0].EventID] = "COMMITTED"
	st.exists[msgs[1].EventID] = true
	st.statuses[msgs[1].EventID] = "COMMITTED"

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

func TestFlusher_DLQPublishFails_ReturnsError_NoCommit(t *testing.T) {
	// Когда chain revert'ит И DLQ down — Flush должен вернуть error, чтобы
	// offsets не закоммитились. После reorder (DLQ first) MarkFailed НЕ
	// вызывается при DLQ-сбое — short-circuit on error. Retry на next tick.
	f, ch, st, dq := newFlusherT()
	ch.callErr = errors.New("execution reverted")
	dq.publishErr = errors.New("kafka broker down")
	msgs := makeMessages(2, "wms.putaway.v1", "putaway")

	err := f.Flush(context.Background(), "wms.putaway.v1", msgs)
	if err == nil {
		t.Fatal("DLQ publish fail should propagate (transient, block offset commit)")
	}
	// После reorder: DLQ вызывается первым, short-circuit — MarkFailed не должен
	// был выполниться.
	if len(st.failed) != 0 {
		t.Errorf("MarkFailed should NOT run when DLQ fails (reordered), got %d", len(st.failed))
	}
	if len(st.committed) != 0 {
		t.Errorf("no commits expected on DLQ fail, got %d", len(st.committed))
	}
}

func TestFlusher_MarkFailedFails_ReturnsError_DLQAlreadyPublished(t *testing.T) {
	// После reorder: DLQ publish happens first. Если затем MarkFailed падает —
	// Flush возвращает error (offsets не коммитятся), DLQ-запись уже есть,
	// row остаётся PENDING. На retry DLQ опубликуется повторно (дубликат в
	// DLQ-topic приемлем, consumer там должен быть idempotent), MarkFailed
	// снова попытается и в итоге пройдёт.
	f, ch, st, dq := newFlusherT()
	ch.callErr = errors.New("execution reverted")
	st.failedErr = errors.New("db connection refused")
	msgs := makeMessages(1, "wms.picking.v1", "picking")

	err := f.Flush(context.Background(), "wms.picking.v1", msgs)
	if err == nil {
		t.Fatal("MarkFailed fail should propagate (transient, block offset commit)")
	}
	// DLQ был первым, он успел отработать до того как упал MarkFailed.
	if len(dq.messages) != 1 {
		t.Errorf("DLQ should publish before MarkFailed (reordered), got %d", len(dq.messages))
	}
	if len(st.failed) != 0 {
		t.Errorf("MarkFailed failed -> no rows marked, got %d", len(st.failed))
	}
}

func TestFlusher_StrandedPending_GetsRetried(t *testing.T) {
	// Row залип в PENDING (MarkSent упал в прошлом tick). Сейчас Exists=true,
	// Status=PENDING — не terminal → прогоняем через chain снова. InsertPending
	// не вызывается (NULL-op из-за уже существующей записи), MarkSent проходит.
	f, ch, st, _ := newFlusherT()
	msgs := makeMessages(1, "wms.receiving.v1", "receiving")
	st.exists[msgs[0].EventID] = true
	st.statuses[msgs[0].EventID] = "PENDING"

	if err := f.Flush(context.Background(), "wms.receiving.v1", msgs); err != nil {
		t.Fatalf("retry of stranded PENDING should succeed, got: %v", err)
	}
	if len(ch.calls) != 1 {
		t.Errorf("expected 1 chain call for stranded row, got %d", len(ch.calls))
	}
	if len(st.committed) != 1 {
		t.Errorf("expected 1 COMMITTED after successful retry, got %d", len(st.committed))
	}
	// InsertPending не должен был вызваться — row уже существовал.
	if len(st.inserted) != 0 {
		t.Errorf("InsertPending should skip existing row, got %d inserts", len(st.inserted))
	}
}

func TestFlusher_CommittedEventSkipped(t *testing.T) {
	// Row уже COMMITTED от прошлого успешного flush'а. Должен полностью
	// скипнуться — ни chain call, ни InsertPending.
	f, ch, st, dq := newFlusherT()
	msgs := makeMessages(1, "wms.receiving.v1", "receiving")
	st.exists[msgs[0].EventID] = true
	st.statuses[msgs[0].EventID] = "COMMITTED"

	if err := f.Flush(context.Background(), "wms.receiving.v1", msgs); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(ch.calls) != 0 {
		t.Errorf("no chain call expected for COMMITTED event, got %d", len(ch.calls))
	}
	if len(dq.messages) != 0 {
		t.Errorf("no DLQ expected, got %d", len(dq.messages))
	}
	if len(st.inserted) != 0 {
		t.Errorf("no InsertPending expected, got %d", len(st.inserted))
	}
}

func TestFlusher_FailedEventSkipped(t *testing.T) {
	// Terminal FAILED — тоже terminal, скип.
	f, ch, st, _ := newFlusherT()
	msgs := makeMessages(1, "wms.receiving.v1", "receiving")
	st.exists[msgs[0].EventID] = true
	st.statuses[msgs[0].EventID] = "FAILED"

	if err := f.Flush(context.Background(), "wms.receiving.v1", msgs); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(ch.calls) != 0 {
		t.Errorf("no chain call for FAILED event, got %d", len(ch.calls))
	}
}

func TestFlusher_ChainTransientError_NoDLQ_ReturnsError(t *testing.T) {
	// ErrChainTransient (RPC down) — не DLQ'им, возвращаем error → offsets
	// не коммитятся → ретрай.
	f, ch, st, dq := newFlusherT()
	ch.callErr = fmt.Errorf("%w: dial failed", chain.ErrChainTransient)
	msgs := makeMessages(2, "wms.receiving.v1", "receiving")

	err := f.Flush(context.Background(), "wms.receiving.v1", msgs)
	if err == nil {
		t.Fatal("transient chain error should propagate")
	}
	if !errors.Is(err, chain.ErrChainTransient) {
		t.Errorf("error chain should preserve ErrChainTransient, got %v", err)
	}
	if len(st.failed) != 0 {
		t.Errorf("transient error should NOT mark FAILED, got %d", len(st.failed))
	}
	if len(dq.messages) != 0 {
		t.Errorf("transient error should NOT publish DLQ, got %d", len(dq.messages))
	}
}

func TestFlusher_ChainRevertError_GoesToDLQ(t *testing.T) {
	// ErrChainRevert (terminal) — DLQ + MarkFailed, возвращаем nil (offsets
	// commit'ятся).
	f, ch, st, dq := newFlusherT()
	ch.callErr = fmt.Errorf("%w: execution reverted: Duplicate eventId", chain.ErrChainRevert)
	msgs := makeMessages(2, "wms.receiving.v1", "receiving")

	if err := f.Flush(context.Background(), "wms.receiving.v1", msgs); err != nil {
		t.Fatalf("revert should return nil (DLQ success), got: %v", err)
	}
	if len(st.failed) != 2 {
		t.Errorf("expected 2 MarkFailed, got %d", len(st.failed))
	}
	if len(dq.messages) != 1 {
		t.Errorf("expected 1 DLQ publish, got %d", len(dq.messages))
	}
}

func TestFlusher_ChainTransientError_PropagatesAsError_NoDLQ(t *testing.T) {
	// Regression guard для MR !35 M7: не-EstimateGas RPC error (например
	// PendingNonceAt дропнулся из-за dial tcp refused) — должен быть
	// classified как ErrChainTransient, НЕ терминальный → offsets не
	// коммитим, DLQ/FAILED не пишем, retry при следующем tick.
	f, ch, st, dq := newFlusherT()
	ch.callErr = fmt.Errorf("%w: nonce: dial tcp: connection refused", chain.ErrChainTransient)
	msgs := makeMessages(2, "wms.receiving.v1", "receiving")

	err := f.Flush(context.Background(), "wms.receiving.v1", msgs)
	if err == nil {
		t.Fatal("transient chain error should propagate (no offset commit)")
	}
	if !errors.Is(err, chain.ErrChainTransient) {
		t.Errorf("expected ErrChainTransient wrap, got %v", err)
	}
	if len(st.failed) != 0 {
		t.Errorf("transient should NOT mark failed, got %d", len(st.failed))
	}
	if len(dq.messages) != 0 {
		t.Errorf("transient should NOT publish DLQ, got %d", len(dq.messages))
	}
}
