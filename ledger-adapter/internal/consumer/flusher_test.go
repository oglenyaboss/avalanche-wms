package consumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"

	"ledger-adapter/internal/chain"
	"ledger-adapter/internal/store"
)

// --- моки interfaces -----------------------------------------------------

type stubChain struct {
	mu                sync.Mutex
	calls             []string         // "signed_action" на каждый SendCalldata
	calldataLens      []int            // размер data каждого SendCalldata (по порядку)
	calldatas         [][]byte         // data каждого SendCalldata (копия, по порядку)
	tos               []common.Address // адрес каждого SendCalldata (по порядку)
	minGases          []uint64         // minGas каждого SendCalldata (по порядку)
	receiptCalls      int              // сколько раз дёрнули TransactionReceipt
	callErr           error
	callErrFor        map[string]error // per-aggregate error
	receipt           *types.Receipt
	receiptErr        error
	receiptErrForHash map[common.Hash]error // per-tx-hash receipt error
	receiptWait       int
}

func (s *stubChain) SendCalldata(_ context.Context, to common.Address, data []byte, minGas uint64) (common.Hash, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, aggregateSignedAction)
	s.calldataLens = append(s.calldataLens, len(data))
	s.calldatas = append(s.calldatas, append([]byte(nil), data...))
	s.tos = append(s.tos, to)
	s.minGases = append(s.minGases, minGas)
	if s.callErrFor != nil {
		if err, ok := s.callErrFor[aggregateSignedAction]; ok {
			return common.Hash{}, err
		}
	}
	if s.callErr != nil {
		return common.Hash{}, s.callErr
	}
	return common.HexToHash("0xca11da7a"), nil
}

func (s *stubChain) TransactionReceipt(_ context.Context, h common.Hash) (*types.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receiptCalls++
	if s.receiptErrForHash != nil {
		if err, ok := s.receiptErrForHash[h]; ok {
			return nil, err
		}
	}
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
	rejected  map[uuid.UUID]uint8
	existsErr error
	statusErr error
	insertErr error
	failedErr error

	reconcilable []store.ReconcileRow
}

func newStubStore() *stubStore {
	return &stubStore{
		exists:    map[uuid.UUID]bool{},
		statuses:  map[uuid.UUID]string{},
		inserted:  map[uuid.UUID]string{},
		sent:      map[uuid.UUID]string{},
		committed: map[uuid.UUID]bool{},
		failed:    map[uuid.UUID]string{},
		rejected:  map[uuid.UUID]uint8{},
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

func (s *stubStore) StatusAndTx(_ context.Context, id uuid.UUID) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.statusErr != nil {
		return "", "", s.statusErr
	}
	status := "PENDING"
	if st, ok := s.statuses[id]; ok {
		status = st
	}
	// sent[] хранит tx_hash, выставленный MarkSent; пусто = tx ещё не отправлялась.
	return status, s.sent[id], nil
}

func (s *stubStore) ListReconcilable(_ context.Context, _ time.Duration) ([]store.ReconcileRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.statusErr != nil {
		return nil, s.statusErr
	}
	return s.reconcilable, nil
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

func (s *stubStore) MarkRejected(_ context.Context, ids []uuid.UUID, _ string, reason uint8) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.rejected[id] = reason
		s.failed[id] = fmt.Sprintf("action rejected: reason %d", reason)
		s.statuses[id] = "FAILED"
	}
	return nil
}

// stubResolver — программируемые исходы подписанных действий для Confirm/reconcile:
// outcomes — исходы из логов receipt'а (LogOutcomes), states — известные исходы
// по actionState (StateOutcome; отсутствующий id = State.None). addr — адрес
// контракта (необязательный; zero → дефолт 0x…aa), чтобы тесты различали
// warehouse- и registry-резолверы.
type stubResolver struct {
	outcomes map[uuid.UUID]chain.ActionOutcome
	states   map[uuid.UUID]chain.ActionOutcome
	err      error
	addr     common.Address
}

func (r *stubResolver) LogOutcomes(_ *types.Receipt) (map[uuid.UUID]chain.ActionOutcome, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.outcomes, nil
}

func (r *stubResolver) StateOutcome(_ context.Context, id uuid.UUID) (chain.ActionOutcome, bool, error) {
	if r.err != nil {
		return chain.ActionOutcome{}, false, r.err
	}
	o, ok := r.states[id]
	return o, ok, nil
}

func (r *stubResolver) Address() common.Address {
	if r.addr != (common.Address{}) {
		return r.addr
	}
	return common.HexToAddress("0x00000000000000000000000000000000000000aa")
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

// makeWarehouseMessages — n складских действий.
func makeWarehouseMessages(n int) []*Message {
	msgs := make([]*Message, n)
	for i := range msgs {
		msgs[i] = makeWarehouseMessage(`{}`)
	}
	return msgs
}

// appliedResolver — warehouse-резолвер, у которого все msgs применены (Applied)
// в логах receipt'а.
func appliedResolver(msgs ...*Message) *stubResolver {
	outcomes := make(map[uuid.UUID]chain.ActionOutcome, len(msgs))
	for _, m := range msgs {
		outcomes[m.EventID] = chain.ActionOutcome{Applied: true}
	}
	return &stubResolver{outcomes: outcomes}
}

// makeActionMessage — Message подписанного действия. clientDataJSON задаёт размер
// calldata (единственное поле заметной длины) — им управляем чанкингом в тестах.
func makeActionMessage(clientDataJSON string) *Message {
	id := uuid.New()
	return &Message{
		EventID:       id,
		AggregateType: aggregateSignedAction,
		Action: &chain.KeyAction{
			ActionId:   [16]byte(id),
			ActionType: 5,
			Auth: chain.WebAuthnAuth{
				ChallengeIndex:    big.NewInt(23),
				TypeIndex:         big.NewInt(1),
				AuthenticatorData: []byte("authdata"),
				ClientDataJSON:    clientDataJSON,
			},
		},
		Topic:    "wms.events.v1",
		KafkaMsg: kafka.Message{Topic: "wms.events.v1"},
	}
}

// makeWarehouseMessage — Message складского действия. clientDataJSON задаёт размер
// calldata (единственное поле заметной длины) — им управляем чанкингом в тестах.
func makeWarehouseMessage(clientDataJSON string) *Message {
	id := uuid.New()
	return &Message{
		EventID:       id,
		AggregateType: aggregateSignedAction,
		WarehouseAction: &chain.WarehouseAction{
			ActionId:    [16]byte(id),
			ActionType:  1,
			ItemIds:     []*big.Int{chain.UUIDToUint256(uuid.New().String())},
			DetailsHash: [32]byte{0xde},
			KeyId:       [32]byte{0xad},
			Auth: chain.WebAuthnAuth{
				ChallengeIndex:    big.NewInt(23),
				TypeIndex:         big.NewInt(1),
				AuthenticatorData: []byte("authdata"),
				ClientDataJSON:    clientDataJSON,
			},
		},
		Topic:    "wms.events.v1",
		KafkaMsg: kafka.Message{Topic: "wms.events.v1"},
	}
}

// setMaxActionCalldata подменяет package-лимит на время теста.
func setMaxActionCalldata(t *testing.T, limit int) {
	t.Helper()
	orig := maxActionCalldata
	maxActionCalldata = limit
	t.Cleanup(func() { maxActionCalldata = orig })
}

// --- tests ---------------------------------------------------------------

func TestFlusher_HappyPath(t *testing.T) {
	f, ch, st, dq := newFlusherT()
	msgs := makeWarehouseMessages(3)
	f.WithWarehouseActions(appliedResolver(msgs...))

	if err := f.Flush(context.Background(), msgs); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(ch.calls) != 1 || ch.calls[0] != aggregateSignedAction {
		t.Errorf("expected 1 SendCalldata to warehouse, got %v", ch.calls)
	}
	if len(st.committed) != 3 {
		t.Errorf("expected 3 committed, got %d", len(st.committed))
	}
	for _, m := range msgs {
		if !st.committed[m.EventID] {
			t.Errorf("event %s must be COMMITTED, got %v", m.EventID, st.committed)
		}
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
	msgs := makeWarehouseMessages(2)
	f.WithWarehouseActions(appliedResolver(msgs...))
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
	msgs := makeWarehouseMessages(2)
	f.WithWarehouseActions(appliedResolver(msgs...))

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
	msgs := makeWarehouseMessages(1)
	f.WithWarehouseActions(appliedResolver(msgs...))

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
	msgs := makeWarehouseMessages(1)
	f.WithWarehouseActions(appliedResolver(msgs...))

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
	msgs := makeWarehouseMessages(3)
	f.WithWarehouseActions(appliedResolver(msgs...))
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
	msgs := makeWarehouseMessages(2)
	f.WithWarehouseActions(appliedResolver(msgs...))

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
	msgs := makeWarehouseMessages(1)
	f.WithWarehouseActions(appliedResolver(msgs...))

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

// TestFlusher_StrandedPending_GetsRetried covers a PENDING row that was never broadcast
// (no tx_hash): InsertPending ran but MarkSent did not, so resubmitting is the correct,
// safe recovery — there is no in-flight tx to clash with, and contract idempotency
// (#44) backstops it anyway. The dangerous case (a row that ALREADY has a tx_hash) is
// covered separately by the reconcile-not-resubmit tests below, which keep tx_hash
// stable instead of re-broadcasting (S2 — scenarios/09-s2-crash-recovery.sh).
func TestFlusher_StrandedPending_GetsRetried(t *testing.T) {
	f, ch, st, _ := newFlusherT()
	msgs := makeWarehouseMessages(1)
	f.WithWarehouseActions(appliedResolver(msgs...))
	st.exists[msgs[0].EventID] = true
	st.statuses[msgs[0].EventID] = "PENDING" // no st.sent → tx_hash empty → resubmit-safe

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

// TestFlusher_RedeliveredSentRow_ReconcilesNoResubmit covers S2: a SENT row already has
// a broadcast tx_hash, so a redelivery must reconcile that tx (not resubmit). The mined
// tx resolves to COMMITTED and tx_hash stays stable — no second send.
func TestFlusher_RedeliveredSentRow_ReconcilesNoResubmit(t *testing.T) {
	f, ch, st, dq := newFlusherT() // ch.receipt = Status 1
	m := makeWarehouseMessages(1)[0]
	f.WithWarehouseActions(appliedResolver(m))
	st.exists[m.EventID] = true
	st.statuses[m.EventID] = "SENT"
	st.sent[m.EventID] = "0xabc123" // already broadcast

	if err := f.Flush(context.Background(), []*Message{m}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(ch.calls) != 0 {
		t.Errorf("must NOT resubmit a row with tx_hash, got %d chain calls", len(ch.calls))
	}
	if !st.committed[m.EventID] {
		t.Errorf("reconcile of a mined tx must MarkCommitted")
	}
	if len(st.failed) != 0 || len(dq.messages) != 0 {
		t.Errorf("no FAILED/DLQ expected, got failed=%d dlq=%d", len(st.failed), len(dq.messages))
	}
	if st.sent[m.EventID] != "0xabc123" {
		t.Errorf("tx_hash must stay stable, got %q", st.sent[m.EventID])
	}
}

// TestFlusher_RedeliveredPendingRowWithTx_ReconcilesNoResubmit covers scenario 09's
// PENDING variant: status was reset to PENDING but tx_hash retained — the discriminator
// is tx_hash PRESENCE, not the status string, so this must still reconcile (not resubmit).
func TestFlusher_RedeliveredPendingRowWithTx_ReconcilesNoResubmit(t *testing.T) {
	f, ch, st, _ := newFlusherT()
	m := makeWarehouseMessages(1)[0]
	f.WithWarehouseActions(appliedResolver(m))
	st.exists[m.EventID] = true
	st.statuses[m.EventID] = "PENDING"
	st.sent[m.EventID] = "0xdef456" // tx_hash present despite PENDING status

	if err := f.Flush(context.Background(), []*Message{m}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(ch.calls) != 0 {
		t.Errorf("tx_hash present → reconcile, not resubmit; got %d calls", len(ch.calls))
	}
	if !st.committed[m.EventID] {
		t.Errorf("expected reconcile → COMMITTED")
	}
}

// TestFlusher_RedeliveredRow_TxNotMined_LeftForLoop: if the existing tx is not yet mined,
// the redelivery path neither resubmits nor marks terminal — it leaves the row for the
// background reconcile loop.
func TestFlusher_RedeliveredRow_TxNotMined_LeftForLoop(t *testing.T) {
	f, ch, st, _ := newFlusherT()
	ch.receiptWait = 5 // TransactionReceipt returns ethereum.NotFound
	m := makeWarehouseMessages(1)[0]
	f.WithWarehouseActions(appliedResolver(m))
	st.exists[m.EventID] = true
	st.statuses[m.EventID] = "SENT"
	st.sent[m.EventID] = "0xstillmining"

	if err := f.Flush(context.Background(), []*Message{m}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(ch.calls) != 0 {
		t.Errorf("must not resubmit, got %d", len(ch.calls))
	}
	if len(st.committed) != 0 || len(st.failed) != 0 {
		t.Errorf("tx not mined → row left as-is, got committed=%d failed=%d", len(st.committed), len(st.failed))
	}
}

// TestFlusher_RedeliveredRow_TxReverted_MarksFailed: an already-broadcast tx that mined
// but reverted resolves to terminal FAILED — still no resubmit.
func TestFlusher_RedeliveredRow_TxReverted_MarksFailed(t *testing.T) {
	f, ch, st, _ := newFlusherT()
	ch.receipt = &types.Receipt{Status: 0} // mined but reverted
	m := makeWarehouseMessages(1)[0]
	f.WithWarehouseActions(appliedResolver(m))
	st.exists[m.EventID] = true
	st.statuses[m.EventID] = "SENT"
	st.sent[m.EventID] = "0xreverted"

	if err := f.Flush(context.Background(), []*Message{m}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(ch.calls) != 0 {
		t.Errorf("must not resubmit, got %d", len(ch.calls))
	}
	if _, ok := st.failed[m.EventID]; !ok {
		t.Errorf("reverted tx → MarkFailed")
	}
}

// TestFlusher_IntraBatchDuplicate_Deduped covers N9: the same event_id redelivered within
// one flush window is collapsed to a single batch entry (no [id,id] reaching the contract).
func TestFlusher_IntraBatchDuplicate_Deduped(t *testing.T) {
	f, ch, st, _ := newFlusherT()
	m := makeWarehouseMessages(1)[0]
	f.WithWarehouseActions(appliedResolver(m))
	dup := *m // identical EventID
	msgs := []*Message{m, &dup}

	if err := f.Flush(context.Background(), msgs); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(ch.calls) != 1 {
		t.Errorf("expected 1 chain call (deduped), got %d", len(ch.calls))
	}
	if len(st.committed) != 1 {
		t.Errorf("expected 1 committed, got %d", len(st.committed))
	}
	if len(st.inserted) != 1 {
		t.Errorf("dup must not InsertPending twice, got %d", len(st.inserted))
	}
}

// TestFlusher_RedeliveredRow_ReceiptRPCError_Propagates: a non-NotFound receipt error
// while reconciling a redelivered row is transient — it propagates (blocking the offset
// commit) and the row is left untouched: no resubmit, no terminal mark, no DLQ.
func TestFlusher_RedeliveredRow_ReceiptRPCError_Propagates(t *testing.T) {
	f, ch, st, dq := newFlusherT()
	ch.receiptErr = errors.New("rpc connection refused") // non-NotFound
	m := makeWarehouseMessages(1)[0]
	f.WithWarehouseActions(appliedResolver(m))
	st.exists[m.EventID] = true
	st.statuses[m.EventID] = "SENT"
	st.sent[m.EventID] = "0xinflight"

	err := f.Flush(context.Background(), []*Message{m})
	if err == nil {
		t.Fatal("non-NotFound receipt error during reconcile must propagate (transient, no offset commit)")
	}
	if len(ch.calls) != 0 {
		t.Errorf("must not resubmit, got %d", len(ch.calls))
	}
	if len(st.committed) != 0 || len(st.failed) != 0 {
		t.Errorf("error path must not mark terminal, got committed=%d failed=%d", len(st.committed), len(st.failed))
	}
	if len(dq.messages) != 0 {
		t.Errorf("no DLQ on transient receipt error, got %d", len(dq.messages))
	}
}

func TestFlusher_CommittedEventSkipped(t *testing.T) {
	f, ch, st, dq := newFlusherT()
	msgs := makeWarehouseMessages(1)
	f.WithWarehouseActions(appliedResolver(msgs...))
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
	msgs := makeWarehouseMessages(1)
	f.WithWarehouseActions(appliedResolver(msgs...))
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
	msgs := makeWarehouseMessages(2)
	f.WithWarehouseActions(appliedResolver(msgs...))

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
	msgs := makeWarehouseMessages(2)
	f.WithWarehouseActions(appliedResolver(msgs...))

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
	msgs := makeWarehouseMessages(2)
	f.WithWarehouseActions(appliedResolver(msgs...))

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

// --- Send / Confirm split (pipeline primitives) --------------------------

func TestFlusher_SendDoesNotWaitForReceipt(t *testing.T) {
	f, ch, st, _ := newFlusherT()
	msgs := makeWarehouseMessages(3)
	f.WithWarehouseActions(appliedResolver(msgs...))

	inflight, err := f.Send(context.Background(), msgs)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(inflight) != 1 {
		t.Fatalf("expected 1 inflight tx (one warehouse segment), got %d", len(inflight))
	}
	if inflight[0].Aggregate != aggregateSignedAction {
		t.Errorf("aggregate: want signed_action, got %q", inflight[0].Aggregate)
	}
	if len(inflight[0].IDs) != 3 {
		t.Errorf("expected 3 ids in inflight, got %d", len(inflight[0].IDs))
	}
	if ch.receiptCalls != 0 {
		t.Errorf("Send must NOT poll receipts, got %d receipt calls", ch.receiptCalls)
	}
	if len(st.sent) != 3 {
		t.Errorf("expected 3 MarkSent rows, got %d", len(st.sent))
	}
	if len(st.committed) != 0 {
		t.Errorf("Send must not commit, got %d", len(st.committed))
	}
}

func TestFlusher_ConfirmCommits(t *testing.T) {
	f, ch, st, _ := newFlusherT() // ch.receipt = Status 1
	msgs := makeWarehouseMessages(2)
	f.WithWarehouseActions(appliedResolver(msgs...))
	inflight, err := f.Send(context.Background(), msgs)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := f.Confirm(context.Background(), &inflight[0]); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if len(st.committed) != 2 {
		t.Errorf("expected 2 committed, got %d", len(st.committed))
	}
	for _, m := range msgs {
		if !st.committed[m.EventID] {
			t.Errorf("event %s must be COMMITTED, got %v", m.EventID, st.committed)
		}
	}
	if ch.receiptCalls == 0 {
		t.Error("Confirm should poll the receipt")
	}
}

func TestFlusher_ConfirmRevertToDLQ(t *testing.T) {
	f, ch, st, dq := newFlusherT()
	msgs := makeWarehouseMessages(1)
	f.WithWarehouseActions(appliedResolver(msgs...))
	inflight, err := f.Send(context.Background(), msgs)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	ch.receipt = &types.Receipt{Status: 0} // mined but reverted
	if err := f.Confirm(context.Background(), &inflight[0]); err != nil {
		t.Fatalf("confirm revert should record failure, not return error: %v", err)
	}
	if len(st.failed) != 1 {
		t.Errorf("expected 1 MarkFailed, got %d", len(st.failed))
	}
	if len(dq.messages) != 1 {
		t.Errorf("expected 1 DLQ publish, got %d", len(dq.messages))
	}
	if len(st.committed) != 0 {
		t.Errorf("no commit on revert, got %d", len(st.committed))
	}
}

// TestFlusher_ConfirmStall_NoDLQ_StaysSent: a tx that can't be confirmed within
// the timeout (or on RPC failure) returns ErrConfirmStalled and leaves rows SENT
// (visible, not lost) — NO DLQ, NO MarkFailed. The pipeline turns this into
// recovery; the reconcile loop later catches the tx if it eventually mines.
func TestFlusher_ConfirmStall_NoDLQ_StaysSent(t *testing.T) {
	f, ch, st, dq := newFlusherT()
	msgs := makeWarehouseMessages(2)
	f.WithWarehouseActions(appliedResolver(msgs...))
	inflight, err := f.Send(context.Background(), msgs)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	ch.receiptErr = errors.New("rpc: connection reset") // WaitReceipt can't confirm
	err = f.Confirm(context.Background(), &inflight[0])
	if !errors.Is(err, ErrConfirmStalled) {
		t.Fatalf("stall must return ErrConfirmStalled, got %v", err)
	}
	if len(dq.messages) != 0 {
		t.Errorf("stall must NOT publish DLQ, got %d", len(dq.messages))
	}
	if len(st.failed) != 0 {
		t.Errorf("stall must NOT MarkFailed, got %d", len(st.failed))
	}
	if len(st.committed) != 0 {
		t.Errorf("stall must NOT commit, got %d", len(st.committed))
	}
}

// --- сегменты окна (складские / ключевые действия) -----------------------

// TestFlusher_SplitSegments_WarehouseKey: окно [w1, w2, k1, w3] режется на
// 3 сегмента в порядке следования.
func TestFlusher_SplitSegments_WarehouseKey(t *testing.T) {
	w1, w2, w3 := makeWarehouseMessage(`{}`), makeWarehouseMessage(`{}`), makeWarehouseMessage(`{}`)
	k := makeActionMessage(`{}`)
	msgs := []*Message{w1, w2, k, w3}

	segs := splitSegments(msgs)
	if len(segs) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(segs))
	}
	want := []segmentKind{segmentWarehouse, segmentKey, segmentWarehouse}
	for i, w := range want {
		for _, m := range segs[i] {
			if got := segmentOf(m); got != w {
				t.Errorf("segment %d: kind = %d, want %d", i, got, w)
			}
		}
	}
	if len(segs[0]) != 2 || len(segs[1]) != 1 || len(segs[2]) != 1 {
		t.Fatalf("segment sizes: got %v", []int{len(segs[0]), len(segs[1]), len(segs[2])})
	}
	if segs[0][0] != w1 || segs[0][1] != w2 || segs[1][0] != k || segs[2][0] != w3 {
		t.Errorf("segment contents out of order")
	}
}

// TestFlusher_MixedWindow_WarehouseKeyOrdered: окно [w1, k1, w2] уходит тремя tx
// строго в порядке окна — складские и ключевые действия не сливаются в одну tx,
// каждая уходит на адрес своего контракта.
func TestFlusher_MixedWindow_WarehouseKeyOrdered(t *testing.T) {
	f, ch, _, _ := newFlusherT()
	w1, w2 := makeWarehouseMessage(`{}`), makeWarehouseMessage(`{}`)
	k1 := makeActionMessage(`{}`)
	wh := appliedResolver(w1, w2)
	reg := &stubResolver{
		outcomes: map[uuid.UUID]chain.ActionOutcome{k1.EventID: {Applied: true}},
		addr:     common.HexToAddress("0x00000000000000000000000000000000000000bb"),
	}
	f.WithWarehouseActions(wh)
	f.WithRegistry(reg)

	inflight, err := f.Send(context.Background(), []*Message{w1, k1, w2})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(inflight) != 3 {
		t.Fatalf("expected 3 inflight tx in window order, got %d", len(inflight))
	}
	wantIDs := [][]uuid.UUID{{w1.EventID}, {k1.EventID}, {w2.EventID}}
	for i, want := range wantIDs {
		if len(inflight[i].IDs) != 1 || inflight[i].IDs[0] != want[0] {
			t.Errorf("inflight %d ids: got %v, want %v", i, inflight[i].IDs, want)
		}
	}
	wantTos := []common.Address{wh.Address(), reg.Address(), wh.Address()}
	for i, want := range wantTos {
		if ch.tos[i] != want {
			t.Errorf("tx %d to: got %s, want %s (full: %v)", i, ch.tos[i], want, ch.tos)
		}
	}
}

// TestFlusher_ActionsChunkedByCalldataLimit: два действия, лимит_calldata ниже
// размера пары → две SendCalldata в порядке действий, обе потом COMMITTED.
func TestFlusher_ActionsChunkedByCalldataLimit(t *testing.T) {
	f, ch, st, _ := newFlusherT()
	a1 := makeActionMessage(strings.Repeat("a", 10))
	a2 := makeActionMessage(strings.Repeat("b", 400))

	single1, err := chain.PackApplyKeyActions([]chain.KeyAction{*a1.Action})
	if err != nil {
		t.Fatalf("pack a1: %v", err)
	}
	single2, err := chain.PackApplyKeyActions([]chain.KeyAction{*a2.Action})
	if err != nil {
		t.Fatalf("pack a2: %v", err)
	}
	pair, err := chain.PackApplyKeyActions([]chain.KeyAction{*a1.Action, *a2.Action})
	if err != nil {
		t.Fatalf("pack pair: %v", err)
	}
	limit := len(pair) - 1
	if len(single1) >= limit || len(single2) >= limit {
		t.Fatalf("test setup broken: single1=%d single2=%d limit=%d", len(single1), len(single2), limit)
	}
	setMaxActionCalldata(t, limit)

	res := &stubResolver{outcomes: map[uuid.UUID]chain.ActionOutcome{
		a1.EventID: {Applied: true},
		a2.EventID: {Applied: true},
	}}
	f.WithRegistry(res)

	if err := f.Flush(context.Background(), []*Message{a1, a2}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(ch.calls) != 2 || ch.calls[0] != aggregateSignedAction || ch.calls[1] != aggregateSignedAction {
		t.Fatalf("expected 2 SendCalldata calls in order, got %v", ch.calls)
	}
	if ch.calldataLens[0] != len(single1) || ch.calldataLens[1] != len(single2) {
		t.Errorf("chunk sizes: got %v, want [%d %d] (по одному действию)", ch.calldataLens, len(single1), len(single2))
	}
	if !st.committed[a1.EventID] || !st.committed[a2.EventID] {
		t.Errorf("both actions committed, got %v", st.committed)
	}
}

// TestFlusher_ConfirmSignedAction_Outcomes: receipt успешной applyKeyActions-tx
// разбирается резолвером: Applied → COMMITTED, reject reason 6 → FAILED +
// reject_reason (MarkRejected), DLQ нет.
func TestFlusher_ConfirmSignedAction_Outcomes(t *testing.T) {
	f, _, st, dq := newFlusherT()
	x := makeActionMessage(`{"type":"webauthn.get","x":1}`)
	y := makeActionMessage(`{"type":"webauthn.get","y":2}`)
	f.WithRegistry(&stubResolver{outcomes: map[uuid.UUID]chain.ActionOutcome{
		x.EventID: {Applied: true},
		y.EventID: {Reason: 6},
	}})

	inflight, err := f.Send(context.Background(), []*Message{x, y})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(inflight) != 1 || inflight[0].Aggregate != aggregateSignedAction {
		t.Fatalf("expected 1 signed_action inflight tx, got %+v", inflight)
	}
	if err := f.Confirm(context.Background(), &inflight[0]); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if !st.committed[x.EventID] {
		t.Errorf("applied action must be COMMITTED")
	}
	if st.rejected[y.EventID] != 6 {
		t.Errorf("rejected action must record reason 6, got %d", st.rejected[y.EventID])
	}
	if st.statuses[y.EventID] != "FAILED" {
		t.Errorf("rejected action must be FAILED, got %q", st.statuses[y.EventID])
	}
	if st.committed[y.EventID] || len(dq.messages) != 0 {
		t.Errorf("rejected action must NOT be committed or DLQ'd")
	}
}

// TestFlusher_ActionsWithoutRegistry_FailedAndDLQ: без контракта ключевые действия
// терминально фейлятся («applyKeyActions: contract not configured») и уходят в DLQ,
// tx не строится.
func TestFlusher_ActionsWithoutRegistry_FailedAndDLQ(t *testing.T) {
	f, ch, st, dq := newFlusherT() // registry не подключён
	msgs := []*Message{makeActionMessage(`{}`), makeActionMessage(`{}`)}

	if err := f.Flush(context.Background(), msgs); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(ch.calls) != 0 {
		t.Errorf("no chain calls without registry, got %v", ch.calls)
	}
	for _, m := range msgs {
		if st.failed[m.EventID] != "applyKeyActions: contract not configured" {
			t.Errorf("event %s: want failed with 'applyKeyActions: contract not configured', got %q", m.EventID, st.failed[m.EventID])
		}
	}
	if len(dq.messages) != 1 || len(dq.messages[0]) != 2 {
		t.Errorf("expected 1 DLQ batch of 2, got %v", dq.messages)
	}
}

// TestFlusher_WarehouseSegment_SendsToWMS: сегмент складских действий уходит
// SendCalldata на адрес warehouse-резолвера, calldata анпакится в те же действия;
// Confirm разбирает исходы этим резолвером.
func TestFlusher_WarehouseSegment_SendsToWMS(t *testing.T) {
	f, ch, st, dq := newFlusherT()
	w1 := makeWarehouseMessage(`{"type":"webauthn.get","a":1}`)
	w2 := makeWarehouseMessage(`{"type":"webauthn.get","b":2}`)
	wh := &stubResolver{outcomes: map[uuid.UUID]chain.ActionOutcome{
		w1.EventID: {Applied: true},
		w2.EventID: {Applied: true},
	}}
	f.WithWarehouseActions(wh)

	inflight, err := f.Send(context.Background(), []*Message{w1, w2})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(inflight) != 1 || inflight[0].Aggregate != aggregateSignedAction {
		t.Fatalf("expected 1 signed_action inflight tx, got %+v", inflight)
	}

	if len(ch.tos) != 1 || ch.tos[0] != wh.Address() {
		t.Errorf("SendCalldata to = %v, want %v", ch.tos, wh.Address())
	}
	want, err := chain.PackApplyActions([]chain.WarehouseAction{*w1.WarehouseAction, *w2.WarehouseAction})
	if err != nil {
		t.Fatalf("pack want: %v", err)
	}
	if len(ch.calldatas) != 1 || string(ch.calldatas[0]) != string(want) {
		t.Errorf("calldata mismatch: got %d bytes, want %d (pack of both actions)", len(ch.calldatas[0]), len(want))
	}

	if err := f.Confirm(context.Background(), &inflight[0]); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if !st.committed[w1.EventID] || !st.committed[w2.EventID] {
		t.Errorf("both warehouse actions committed, got %v", st.committed)
	}
	if len(dq.messages) != 0 {
		t.Errorf("no DLQ expected, got %d", len(dq.messages))
	}
}

// TestFlusher_WarehouseSegmentWithoutResolver_TerminalFail: без warehouse-резолвера
// складские действия терминально фейлятся с именем метода в причине, tx не строится.
func TestFlusher_WarehouseSegmentWithoutResolver_TerminalFail(t *testing.T) {
	f, ch, st, dq := newFlusherT()
	msgs := []*Message{makeWarehouseMessage(`{}`), makeWarehouseMessage(`{}`)}

	if err := f.Flush(context.Background(), msgs); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(ch.calls) != 0 {
		t.Errorf("no chain calls without warehouse resolver, got %v", ch.calls)
	}
	for _, m := range msgs {
		if st.failed[m.EventID] != "applyActions: contract not configured" {
			t.Errorf("event %s: want 'applyActions: contract not configured', got %q", m.EventID, st.failed[m.EventID])
		}
	}
	if len(dq.messages) != 1 || len(dq.messages[0]) != 2 {
		t.Errorf("expected 1 DLQ batch of 2, got %v", dq.messages)
	}
}

// TestFlusher_WarehouseChunkPassesGasFloor: складской чанк уходит в SendCalldata
// с minGas = детерминированной нижней границей по числу товаров самого чанка —
// estimate под пайплайном видит дешёвый reject-путь, а к майнингу действие
// применяется с записью на каждый товар (без границы → OOG → revert всего tx).
func TestFlusher_WarehouseChunkPassesGasFloor(t *testing.T) {
	f, ch, _, _ := newFlusherT()
	receive := makeWarehouseMessage(`{}`)
	receive.WarehouseAction.ItemIds = []*big.Int{big.NewInt(1), big.NewInt(2), big.NewInt(3)}
	ship := makeWarehouseMessage(`{}`)
	ship.WarehouseAction.ActionType = 4 // Ship
	ship.WarehouseAction.ItemIds = []*big.Int{big.NewInt(1), big.NewInt(2), big.NewInt(3), big.NewInt(4), big.NewInt(5)}
	f.WithWarehouseActions(appliedResolver(receive, ship))

	if _, err := f.Send(context.Background(), []*Message{receive, ship}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(ch.minGases) != 1 {
		t.Fatalf("expected 1 SendCalldata, got %d", len(ch.minGases))
	}
	// 21_000 база + 2×70_000 действия + 3×26_000 приёмка + 5×9_000 отгрузка.
	want := uint64(21_000 + 2*70_000 + 3*26_000 + 5*9_000)
	if got := ch.minGases[0]; got != want {
		t.Errorf("minGas: got %d, want %d", got, want)
	}
}

// TestFlusher_KeyActionsNoGasFloor: ключевые действия уходят с minGas = 0 —
// нижняя граница газа считается только для applyActions.
func TestFlusher_KeyActionsNoGasFloor(t *testing.T) {
	f, ch, _, _ := newFlusherT()
	k := makeActionMessage(`{}`)
	f.WithRegistry(&stubResolver{outcomes: map[uuid.UUID]chain.ActionOutcome{k.EventID: {Applied: true}}})

	if _, err := f.Send(context.Background(), []*Message{k}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(ch.minGases) != 1 {
		t.Fatalf("expected 1 SendCalldata, got %d", len(ch.minGases))
	}
	if got := ch.minGases[0]; got != 0 {
		t.Errorf("key actions minGas: got %d, want 0", got)
	}
}
