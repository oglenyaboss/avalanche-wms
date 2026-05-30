package consumer

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"

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
