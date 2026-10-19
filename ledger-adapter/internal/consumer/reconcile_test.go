package consumer

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"

	"ledger-adapter/internal/chain"
	"ledger-adapter/internal/store"
)

func newReconcilerT(ch *stubChain, st *stubStore) *Reconciler {
	return NewReconciler(ch, st, time.Second, time.Minute, slog.Default())
}

// TestReconciler_MinedTx_MarksCommitted is the core N1 fix: a stuck row whose tx has
// actually mined successfully reconciles forward to COMMITTED.
func TestReconciler_MinedTx_MarksCommitted(t *testing.T) {
	ch := &stubChain{receipt: &types.Receipt{Status: 1}}
	st := newStubStore()
	id := uuid.New()
	st.reconcilable = []store.ReconcileRow{{EventID: id, TxHash: "0xmined"}}

	if err := newReconcilerT(ch, st).reconcileOnce(context.Background()); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !st.committed[id] {
		t.Errorf("a mined stuck row must reconcile to COMMITTED")
	}
	if len(ch.calls) != 0 {
		t.Errorf("reconcile must NEVER resubmit (BatchCall), got %d", len(ch.calls))
	}
}

// TestReconciler_TxNotMined_LeavesRow: a tx still mining is left untouched for a later tick.
func TestReconciler_TxNotMined_LeavesRow(t *testing.T) {
	ch := &stubChain{receiptWait: 99} // always NotFound
	st := newStubStore()
	id := uuid.New()
	st.reconcilable = []store.ReconcileRow{{EventID: id, TxHash: "0xpending"}}

	if err := newReconcilerT(ch, st).reconcileOnce(context.Background()); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(st.committed) != 0 || len(st.failed) != 0 {
		t.Errorf("not-mined tx must be left as-is, got committed=%d failed=%d", len(st.committed), len(st.failed))
	}
}

// TestReconciler_RevertedTx_MarksFailed: a mined-but-reverted tx stays terminal FAILED.
func TestReconciler_RevertedTx_MarksFailed(t *testing.T) {
	ch := &stubChain{receipt: &types.Receipt{Status: 0}}
	st := newStubStore()
	id := uuid.New()
	st.reconcilable = []store.ReconcileRow{{EventID: id, TxHash: "0xrevert"}}

	if err := newReconcilerT(ch, st).reconcileOnce(context.Background()); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if _, ok := st.failed[id]; !ok {
		t.Errorf("reverted tx must MarkFailed")
	}
	if st.committed[id] {
		t.Errorf("reverted tx must not be committed")
	}
}

// TestReconciler_GroupsByTxHash_OneCheckPerTx: events that shared one batch tx are
// reconciled with a single receipt lookup, and all of them resolve together.
func TestReconciler_GroupsByTxHash_OneCheckPerTx(t *testing.T) {
	ch := &stubChain{receipt: &types.Receipt{Status: 1}}
	st := newStubStore()
	a, b := uuid.New(), uuid.New()
	st.reconcilable = []store.ReconcileRow{
		{EventID: a, TxHash: "0xbatch"},
		{EventID: b, TxHash: "0xbatch"},
	}

	if err := newReconcilerT(ch, st).reconcileOnce(context.Background()); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if ch.receiptCalls != 1 {
		t.Errorf("events sharing a tx must need exactly 1 receipt check, got %d", ch.receiptCalls)
	}
	if !st.committed[a] || !st.committed[b] {
		t.Errorf("both events of the shared tx must be committed")
	}
}

// TestReconciler_PerTxErrorIsolation: one tx whose receipt lookup errors must not abort
// the sweep — healthy sibling txs still reconcile, and reconcileOnce returns nil (the
// error is logged-and-isolated, not propagated).
func TestReconciler_PerTxErrorIsolation(t *testing.T) {
	ch := &stubChain{
		receipt: &types.Receipt{Status: 1},
		receiptErrForHash: map[common.Hash]error{
			common.HexToHash("0xbad"): errors.New("rpc down"),
		},
	}
	st := newStubStore()
	good, bad := uuid.New(), uuid.New()
	st.reconcilable = []store.ReconcileRow{
		{EventID: good, TxHash: "0xgood"},
		{EventID: bad, TxHash: "0xbad"},
	}

	if err := newReconcilerT(ch, st).reconcileOnce(context.Background()); err != nil {
		t.Fatalf("per-tx error must be isolated, not propagated from reconcileOnce: %v", err)
	}
	if !st.committed[good] {
		t.Errorf("the healthy tx must still reconcile to COMMITTED despite a sibling error")
	}
	if st.committed[bad] {
		t.Errorf("the errored tx must NOT be committed")
	}
}

// TestReconciler_NoRows_NoOp: an empty sweep touches neither chain nor store.
func TestReconciler_NoRows_NoOp(t *testing.T) {
	ch := &stubChain{receipt: &types.Receipt{Status: 1}}
	st := newStubStore()

	if err := newReconcilerT(ch, st).reconcileOnce(context.Background()); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if ch.receiptCalls != 0 || len(st.committed) != 0 {
		t.Errorf("no stuck rows → pure no-op, got receiptCalls=%d committed=%d", ch.receiptCalls, len(st.committed))
	}
}

// TestReconciler_Run_StopsOnContextCancel: Run returns cleanly when ctx is canceled.
func TestReconciler_Run_StopsOnContextCancel(t *testing.T) {
	ch := &stubChain{receipt: &types.Receipt{Status: 1}}
	st := newStubStore()
	r := NewReconciler(ch, st, 10*time.Millisecond, time.Minute, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run should return nil on ctx cancel, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop within 2s of ctx cancel")
	}
}

// TestReconciler_SignedAction_RejectedReasonRecorded: stuck signed_action-строка
// с успешным receipt'ом разбирается резолвером — отклонённое действие получает
// FAILED + reject_reason (MarkRejected), а не безусловный COMMITTED.
func TestReconciler_SignedAction_RejectedReasonRecorded(t *testing.T) {
	ch := &stubChain{receipt: &types.Receipt{Status: 1}}
	st := newStubStore()
	applied, rejected := uuid.New(), uuid.New()
	st.reconcilable = []store.ReconcileRow{
		{EventID: applied, AggregateType: "signed_action", TxHash: "0xactions"},
		{EventID: rejected, AggregateType: "signed_action", TxHash: "0xactions"},
	}
	r := newReconcilerT(ch, st).WithRegistry(&stubResolver{outcomes: map[uuid.UUID]chain.ActionOutcome{
		applied:  {Applied: true},
		rejected: {Reason: 6},
	}})

	if err := r.reconcileOnce(context.Background()); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !st.committed[applied] {
		t.Errorf("applied action must reconcile to COMMITTED")
	}
	if st.rejected[rejected] != 6 {
		t.Errorf("rejected action must record reason 6, got %d", st.rejected[rejected])
	}
	if st.committed[rejected] {
		t.Errorf("rejected action must NOT be committed")
	}
	if ch.receiptCalls != 1 {
		t.Errorf("one receipt check per tx, got %d", ch.receiptCalls)
	}
}

// TestReconciler_ResolverError_LeavesRows: ошибка резолвера не терминальна —
// sweep изолирует её, строки остаются для следующего tick'а.
func TestReconciler_ResolverError_LeavesRows(t *testing.T) {
	ch := &stubChain{receipt: &types.Receipt{Status: 1}}
	st := newStubStore()
	id := uuid.New()
	st.reconcilable = []store.ReconcileRow{
		{EventID: id, AggregateType: "signed_action", TxHash: "0xactions"},
	}
	r := newReconcilerT(ch, st).WithRegistry(&stubResolver{err: errors.New("rpc down")})

	// reconcileOnce изолирует ошибку по tx и возвращает nil (логирует).
	if err := r.reconcileOnce(context.Background()); err != nil {
		t.Fatalf("resolver error must be isolated: %v", err)
	}
	if len(st.committed) != 0 || len(st.rejected) != 0 || len(st.failed) != 0 {
		t.Errorf("rows must stay untouched, got committed=%v rejected=%v failed=%v", st.committed, st.rejected, st.failed)
	}
}
