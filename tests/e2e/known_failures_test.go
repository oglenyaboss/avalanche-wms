//go:build e2e

package e2e

import "testing"

// These tests document known product defects in the WMS <-> chain integration.
// They are intentionally skipped: the fault injection is NOT implemented, only
// the intended invariant is described, so the suite stays green while the gap is
// tracked. Remove the Skip and implement the body once the product is fixed.

// TestS1_WMSChainDivergence_pendingFix documents S1: WMS never reads
// onchain_events.status, so an on-chain FAILED transition does not roll WMS back.
func TestS1_WMSChainDivergence_pendingFix(t *testing.T) {
	t.Skip("documents S1; pending product fix")

	// Intended assertions (NOT yet implementable without fault injection):
	//
	// 1. Drive a product through a stage (e.g. receiving) so an outbox event is
	//    emitted and the adapter submits it on-chain.
	// 2. Force the on-chain transition to FAIL (e.g. inject an invalid FSM
	//    transition or a revert), so onchain_events.status becomes FAILED.
	// 3. Invariant that SHOULD hold but currently does NOT:
	//      - When onchain_events.status = 'FAILED' for a product's stage event,
	//        the product's WMS status MUST NOT advance past that stage.
	//    Today WMS advances regardless because it never consults
	//    onchain_events.status, so WMS and chain diverge. Assert the product
	//    status is rolled back / held, and surface the divergence.
}

// TestS2_AdapterCrashRecovery_pendingFix documents S2: on restart the adapter
// resubmits SENT/PENDING events, causing a duplicate revert and a false FAILED.
func TestS2_AdapterCrashRecovery_pendingFix(t *testing.T) {
	t.Skip("documents S2; pending product fix")

	// Intended assertions (NOT yet implementable without fault injection):
	//
	// 1. Submit a stage event; let the adapter move it to SENT/PENDING (tx in
	//    flight or mined but not yet reconciled).
	// 2. Restart the ledger-adapter before it records the COMMITTED result.
	// 3. Invariant that SHOULD hold but currently does NOT:
	//      - Recovery MUST be idempotent: a SENT/PENDING event already applied
	//        on-chain must reconcile to COMMITTED, NOT be resubmitted.
	//    Today the adapter resubmits, the contract reverts the duplicate FSM
	//    transition, and the event is marked FAILED even though the original
	//    transition succeeded. Assert the event ends COMMITTED (not FAILED) and
	//    exactly one successful tx exists for it.
}

// TestS3_BatchPoisoning_pendingFix documents S3: one bad event fails the entire
// batch, including valid sibling events.
func TestS3_BatchPoisoning_pendingFix(t *testing.T) {
	t.Skip("documents S3; pending product fix")

	// Intended assertions (NOT yet implementable without fault injection):
	//
	// 1. Build a batch with N valid stage events plus one poisoned event (e.g. an
	//    out-of-order/invalid FSM transition for some item).
	// 2. Submit the batch through the adapter.
	// 3. Invariant that SHOULD hold but currently does NOT:
	//      - A single poisoned event MUST NOT fail its valid siblings. Valid
	//        events in the batch MUST still reach COMMITTED (poison isolation /
	//        per-event failure), while only the bad event is marked FAILED.
	//    Today the whole batch fails atomically, so valid siblings are wrongly
	//    marked FAILED. Assert the N valid events end COMMITTED and only the
	//    poisoned one is FAILED.
}

// ─── Additional defects found in the 2026-05-25 multi-module bug-hunt sweep ───
// Same convention as S1/S2/S3: documented here as t.Skip stubs with the intended
// invariant and a runnable reproduction. The two pure-HTTP ones (shipping
// ship-before-assembled, receiving open-box) can flip into live regressions by
// implementing the body once the product is fixed; see tests/e2e/BUGHUNT.md for the
// full backlog (severity, file:line, and the deferred lower-severity findings).

// TestAssemblyCartLostOnRestart_pendingFix documents a CRITICAL data-integrity bug:
// the assembly pick cart lives only in WMS process memory (assembly/service.go:18,
// 266-270) and no endpoint rebuilds it from the DB. If WMS restarts (or a request
// lands on another instance) between Pick and ScanShippingBuffer, the picked items
// are stranded ASSEMBLED with their order ALLOCATED forever — ScanShippingBuffer
// reads only s.carts (service.go:286) and there is no recovery path.
func TestAssemblyCartLostOnRestart_pendingFix(t *testing.T) {
	t.Skip("documents Assembly BUG-1 (cart not persisted); pending product fix")

	// Reproduction (needs a wms_app restart, so not pure HTTP):
	//  1. Allocate an order; POST /assembly/pick for product P (P -> ASSEMBLED, cart=[P]).
	//  2. Restart WMS:  docker compose -p blockchain_project_e2e restart wms_app
	//  3. POST /assembly/scan-shipping-buffer {buffer_bin_id} -> 409 CART_EMPTY (cart lost).
	//  4. P is stuck: products.status='ASSEMBLED', orders.status='ALLOCATED', unrecoverable.
	// Invariant that SHOULD hold: a restart must not strand picked items — either the
	// cart is persisted (Redis/DB; see the TODO at service.go:376) or ScanShippingBuffer
	// rebuilds it from status='ASSEMBLED' products for the destination.
}

// TestAdapterN1_ReceiptTimeoutDivergence_pendingFix documents a CRITICAL bug found in
// the sweep, distinct from S2 and needing NO crash: on a WaitReceipt timeout the
// flusher marks the event FAILED (ledger-adapter/internal/consumer/flusher.go:124-129)
// even though the tx may still be mining. Under chain congestion the tx mines moments
// later -> chain shows the transition COMMITTED while the DB row is terminal FAILED,
// with no reconciliation path back out of FAILED.
func TestAdapterN1_ReceiptTimeoutDivergence_pendingFix(t *testing.T) {
	t.Skip("documents Adapter N1 (receipt-timeout false FAILED); pending product fix")

	// Runnable reproduction: tests/e2e/scenarios/11-receipt-timeout.sh
	// (lower RECEIPT_POLL_TIMEOUT and slow block production so the receipt poll times
	// out before the tx mines).
	// Invariant that SHOULD hold: a tx that ultimately mines must reconcile to
	// COMMITTED; a poll timeout must be transient (retry/await), never terminal FAILED.
}

// TestShippingShipBeforeAssembled_pendingFix documents a HIGH bug: Ship gates on
// product status (READY_TO_SHIP) but never on order status — shipping/service.go:147
// selects products by status only, and UpdateOrdersShippedConditional only flips orders
// already ASSEMBLED. If only SOME of an order's products are READY_TO_SHIP while the
// order is still ALLOCATED, shipping them succeeds but the order can never reach SHIPPED.
func TestShippingShipBeforeAssembled_pendingFix(t *testing.T) {
	t.Skip("documents Shipping BUG-2 (order stuck ALLOCATED after partial ship); pending product fix")

	// Reproduction (pure HTTP — implement with newMultiProductFixture to flip live):
	//  1. newMultiProductFixture(SHOP-7, storedCount=2, orderQty=2); allocate (order ALLOCATED, 2 tasks).
	//  2. Pick only ONE product P1 (cart=[P1]); leave P2 ALLOCATED/PENDING.
	//  3. POST /assembly/scan-shipping-buffer -> P1 READY_TO_SHIP, order STAYS ALLOCATED
	//     (UpdateOrdersToAssembled needs ALL products READY_TO_SHIP).
	//  4. scan-driver to AT_GATE; POST /shipping/ship {product_ids:[P1]} -> 200, P1 SHIPPED,
	//     orders_completed=0.
	//  5. Bug: order is stuck ALLOCATED — UpdateOrdersShippedConditional requires
	//     status='ASSEMBLED', so it can never become SHIPPED even after P2 ships.
	// Invariant that SHOULD hold: Ship must reject products whose order is not ASSEMBLED
	// (or order completion must not require the ASSEMBLED precondition), so an order is
	// never stranded once its items have shipped.
}

// TestReceivingOpenBoxReachesChain_pendingFix documents a HIGH WMS<->chain divergence:
// close-cargoplace emits a receiving outbox event for every RECEIVED product regardless
// of its box (receiving/repository.go listProductIDsByCargoplaceTx has no box-status
// filter), but ScanBuffer only moves products whose box is CLOSED. A product left in an
// OPEN box keeps bin_id NULL yet gets an Accepted(1) on-chain transition — it is on-chain
// but physically unplaceable, so putaway can never find it.
func TestReceivingOpenBoxReachesChain_pendingFix(t *testing.T) {
	t.Skip("documents Receiving BUG-1 (open-box product reaches chain); pending product fix")

	// Reproduction (pure HTTP — implement to flip live):
	//  1. scan-cargoplace; scan-box (B OPEN); scan-sku; scan-qr (product P RECEIVED, in box B).
	//  2. Do NOT close-box. POST /receiving/table/scan-buffer -> products_placed=0 (P stays bin_id NULL).
	//  3. POST /receiving/table/close-cargoplace -> outbox_events_created>=1 (an event for P).
	//  4. Bug: P has bin_id NULL but a receiving event reaches the chain (itemStatus=Accepted),
	//     and putaway /scan-buffer can never surface it.
	// Invariant that SHOULD hold: close-cargoplace must NOT emit on-chain events for products
	// still in OPEN boxes (or must require all boxes closed first), so every on-chain Accepted
	// item is physically placed and can proceed through putaway.
}
