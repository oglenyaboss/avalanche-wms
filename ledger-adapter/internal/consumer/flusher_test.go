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
	calls       []string // aggregate_type записи
	callErr     error
	callErrFor  map[string]error // per-aggregate error
	receipt     *types.Receipt
	receiptErr  error
	receiptWait int
}

func (s *stubChain) BatchCall(_ context.Context, aggregateType string, _, _ []*big.Int) (common.Hash, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, aggregateType)
	if s.callErrFor != nil {
		if err, ok := s.callErrFor[aggregateType]; ok {
			return common.Hash{}, err
		}
	}
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
	statuses  map[uuid.UUID]string
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

func makeMessages(n int, aggType string) []*Message {
	msgs := make([]*Message, n)
	for i := range msgs {
		msgs[i] = &Message{
			EventID:       uuid.New(),
			ProductID:     uuid.New(),
			AggregateType: aggType,
			Topic:         "wms.events.v1",
			KafkaMsg:      kafka.Message{Topic: "wms.events.v1"},
		}
	}
	return msgs
}

// --- tests ---------------------------------------------------------------

func TestFlusher_HappyPath(t *testing.T) {
	f, ch, st, dq := newFlusherT()
	msgs := makeMessages(3, "receiving")

	if err := f.Flush(context.Background(), msgs); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(ch.calls) != 1 || ch.calls[0] != "receiving" {
		t.Errorf("expected 1 BatchCall to receiving, got %v", ch.calls)
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
	msgs := makeMessages(2, "receiving")
	for _, m := range msgs {
		st.exists[m.EventID] = true
		st.statuses[m.EventID] = "COMMITTED"
	}

	if err := f.Flush(context.Background(), msgs); err != nil {
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
	msgs := makeMessages(2, "putaway")

	if err := f.Flush(context.Background(), msgs); err != nil {
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
	ch.receipt = &types.Receipt{Status: 0}
	msgs := makeMessages(1, "picking")

	if err := f.Flush(context.Background(), msgs); err != nil {
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
	msgs := makeMessages(1, "receiving")

	err := f.Flush(context.Background(), msgs)
	if err == nil {
		t.Fatal("store error should propagate (transient, retry)")
	}
}

func TestFlusher_EmptyBatch_NoOp(t *testing.T) {
	f, ch, st, _ := newFlusherT()
	if err := f.Flush(context.Background(), nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	if len(ch.calls) != 0 || len(st.inserted) != 0 {
		t.Error("empty batch should be pure no-op")
	}
}

func TestFlusher_PartialDuplicates_ProcessesOnlyNew(t *testing.T) {
	f, ch, st, _ := newFlusherT()
	msgs := makeMessages(3, "shipping")
	st.exists[msgs[0].EventID] = true
	st.statuses[msgs[0].EventID] = "COMMITTED"
	st.exists[msgs[1].EventID] = true
	st.statuses[msgs[1].EventID] = "COMMITTED"

	if err := f.Flush(context.Background(), msgs); err != nil {
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
	f, ch, st, dq := newFlusherT()
	ch.callErr = errors.New("execution reverted")
	dq.publishErr = errors.New("kafka broker down")
	msgs := makeMessages(2, "putaway")

	err := f.Flush(context.Background(), msgs)
	if err == nil {
		t.Fatal("DLQ publish fail should propagate (transient, block offset commit)")
	}
	// DLQ runs before MarkFailed: DLQ fails, so MarkFailed is never reached.
	if len(st.failed) != 0 {
		t.Errorf("DLQ fails first — MarkFailed should not run, got %d", len(st.failed))
	}
	if len(st.committed) != 0 {
		t.Errorf("no commits expected on DLQ fail, got %d", len(st.committed))
	}
}

func TestFlusher_MarkFailedFails_ReturnsError_DLQAlreadyPublished(t *testing.T) {
	f, ch, st, dq := newFlusherT()
	ch.callErr = errors.New("execution reverted")
	st.failedErr = errors.New("db connection refused")
	msgs := makeMessages(1, "picking")

	err := f.Flush(context.Background(), msgs)
	if err == nil {
		t.Fatal("MarkFailed fail should propagate (transient, block offset commit)")
	}
	// DLQ-first ordering: the DLQ message is already published before MarkFailed
	// is attempted, so when MarkFailed fails the DLQ row still exists (count == 1).
	if len(dq.messages) != 1 {
		t.Errorf("DLQ already published before MarkFailed, expected 1, got %d", len(dq.messages))
	}
	if len(st.failed) != 0 {
		t.Errorf("MarkFailed failed -> no rows marked, got %d", len(st.failed))
	}
}

// TestFlusher_StrandedPending_GetsRetried gives FALSE CONFIDENCE about crash recovery
// (bug S2). It asserts a stranded PENDING row is resubmitted and reaches COMMITTED, and
// only passes because the stub chain (stubChain.BatchCall) has no `processedEventIds`
// equivalent — it returns success for the same eventId every call. The REAL contract's
// _requireNewEvent reverts "Duplicate eventId" on a resubmit, so in production this same
// path marks the already-succeeded event FAILED. This test proves "the retry code path
// runs", NOT "resubmission is safe". The true behavior is reproduced by the e2e
// scenario tests/e2e/scenarios/09-s2-crash-recovery.sh. Do not treat a green here as S2
// coverage; a faithful fix must make resubmission of a non-terminal row idempotent.
func TestFlusher_StrandedPending_GetsRetried(t *testing.T) {
	f, ch, st, _ := newFlusherT()
	msgs := makeMessages(1, "receiving")
	st.exists[msgs[0].EventID] = true
	st.statuses[msgs[0].EventID] = "PENDING"

	if err := f.Flush(context.Background(), msgs); err != nil {
		t.Fatalf("retry of stranded PENDING should succeed, got: %v", err)
	}
	if len(ch.calls) != 1 {
		t.Errorf("expected 1 chain call for stranded row, got %d", len(ch.calls))
	}
	if len(st.committed) != 1 {
		t.Errorf("expected 1 COMMITTED after successful retry, got %d", len(st.committed))
	}
	if len(st.inserted) != 0 {
		t.Errorf("InsertPending should skip existing row, got %d inserts", len(st.inserted))
	}
}

func TestFlusher_CommittedEventSkipped(t *testing.T) {
	f, ch, st, dq := newFlusherT()
	msgs := makeMessages(1, "receiving")
	st.exists[msgs[0].EventID] = true
	st.statuses[msgs[0].EventID] = "COMMITTED"

	if err := f.Flush(context.Background(), msgs); err != nil {
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
	f, ch, st, _ := newFlusherT()
	msgs := makeMessages(1, "receiving")
	st.exists[msgs[0].EventID] = true
	st.statuses[msgs[0].EventID] = "FAILED"

	if err := f.Flush(context.Background(), msgs); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(ch.calls) != 0 {
		t.Errorf("no chain call for FAILED event, got %d", len(ch.calls))
	}
}

func TestFlusher_ChainTransientError_NoDLQ_ReturnsError(t *testing.T) {
	f, ch, st, dq := newFlusherT()
	ch.callErr = fmt.Errorf("%w: dial failed", chain.ErrChainTransient)
	msgs := makeMessages(2, "receiving")

	err := f.Flush(context.Background(), msgs)
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
	f, ch, st, dq := newFlusherT()
	ch.callErr = fmt.Errorf("%w: execution reverted: Duplicate eventId", chain.ErrChainRevert)
	msgs := makeMessages(2, "receiving")

	if err := f.Flush(context.Background(), msgs); err != nil {
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
	f, ch, st, dq := newFlusherT()
	ch.callErr = fmt.Errorf("%w: nonce: dial tcp: connection refused", chain.ErrChainTransient)
	msgs := makeMessages(2, "receiving")

	err := f.Flush(context.Background(), msgs)
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

// --- mixed-batch tests ---------------------------------------------------

func TestFlusher_MixedBatch_OrderedByFSM(t *testing.T) {
	f, ch, st, _ := newFlusherT()
	recvA := makeMessages(1, "receiving")
	recvB := makeMessages(1, "receiving")
	putA := makeMessages(1, "putaway")
	// Simulate mixed batch: [recv-A, putaway-A, recv-B] — could arrive in any
	// order within a single Kafka batch window.
	msgs := append(recvA, putA...)
	msgs = append(msgs, recvB...)

	if err := f.Flush(context.Background(), msgs); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	// FSM order: receiving first, then putaway.
	if len(ch.calls) != 2 {
		t.Fatalf("expected 2 chain calls (receiving + putaway), got %v", ch.calls)
	}
	if ch.calls[0] != "receiving" {
		t.Errorf("first call should be receiving, got %q", ch.calls[0])
	}
	if ch.calls[1] != "putaway" {
		t.Errorf("second call should be putaway, got %q", ch.calls[1])
	}
	if len(st.committed) != 3 {
		t.Errorf("all 3 should be committed, got %d", len(st.committed))
	}
}

func TestFlusher_MixedBatch_PartialFailure(t *testing.T) {
	f, ch, st, dq := newFlusherT()
	ch.callErrFor = map[string]error{
		"putaway": fmt.Errorf("%w: Invalid status transition", chain.ErrChainRevert),
	}
	recvMsgs := makeMessages(2, "receiving")
	putMsgs := makeMessages(1, "putaway")
	msgs := append(recvMsgs, putMsgs...)

	// Receiving succeeds, putaway reverts → putaway goes to DLQ.
	// Flush returns nil (all sub-batches handled: recv committed, putaway DLQ'd).
	if err := f.Flush(context.Background(), msgs); err != nil {
		t.Fatalf("partial failure should not propagate: %v", err)
	}
	if len(st.committed) != 2 {
		t.Errorf("receiving events should be committed, got %d", len(st.committed))
	}
	if len(st.failed) != 1 {
		t.Errorf("putaway event should be failed, got %d", len(st.failed))
	}
	if len(dq.messages) != 1 {
		t.Errorf("expected 1 DLQ publish for putaway, got %d", len(dq.messages))
	}
}

func TestFlusher_SingleTypeBatch(t *testing.T) {
	f, ch, st, _ := newFlusherT()
	msgs := makeMessages(5, "shipping")

	if err := f.Flush(context.Background(), msgs); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(ch.calls) != 1 || ch.calls[0] != "shipping" {
		t.Errorf("expected single shipping call, got %v", ch.calls)
	}
	if len(st.committed) != 5 {
		t.Errorf("expected 5 committed, got %d", len(st.committed))
	}
}

func TestFlusher_MixedBatch_TransientMidFSM_StopsAndReturnsError(t *testing.T) {
	f, ch, st, dq := newFlusherT()
	ch.callErrFor = map[string]error{
		"putaway": fmt.Errorf("%w: dial failed", chain.ErrChainTransient),
	}
	recvMsgs := makeMessages(2, "receiving")
	putMsgs := makeMessages(1, "putaway")
	shipMsgs := makeMessages(1, "shipping")
	msgs := append(recvMsgs, putMsgs...)
	msgs = append(msgs, shipMsgs...)

	err := f.Flush(context.Background(), msgs)
	if err == nil {
		t.Fatal("transient mid-FSM should propagate error to block offset commit")
	}
	if len(ch.calls) != 2 {
		t.Errorf("expected 2 calls (receiving ok + putaway transient), got %v", ch.calls)
	}
	if len(st.committed) != 2 {
		t.Errorf("receiving should be committed, got %d", len(st.committed))
	}
	if len(st.failed) != 0 {
		t.Errorf("transient should NOT mark failed, got %d", len(st.failed))
	}
	if len(dq.messages) != 0 {
		t.Errorf("transient should NOT publish DLQ, got %d", len(dq.messages))
	}
}

func TestFlusher_AggregateTypes_ConsistentWithChain(t *testing.T) {
	chainTypes := make(map[string]bool)
	for _, a := range chain.AggregateTypes() {
		chainTypes[a] = true
	}
	for _, a := range fsmOrder {
		if !chainTypes[a] {
			t.Errorf("fsmOrder contains %q but chain.AggregateTypes() does not", a)
		}
	}
	for a := range chainTypes {
		if !validAggregates[a] {
			t.Errorf("chain.AggregateTypes() contains %q but validAggregates does not", a)
		}
	}
}
