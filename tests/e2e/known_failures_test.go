//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

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
	t.Skip("S2 FIXED in #44 (contract idempotency + flusher reconcile-not-resubmit); live regression: tests/e2e/scenarios/09-s2-crash-recovery.sh")

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
	t.Skip("S3 FIXED in #47 (batch poison isolation: skip + ItemTransitionFailed, no whole-batch revert); live regression: tests/e2e/scenarios/10-s3-batch-poisoning.sh")

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
	t.Skip("N1 FIXED in #44 (background reconcile loop pulls a mined-but-FAILED row to COMMITTED); live regression: tests/e2e/scenarios/11-receipt-timeout.sh")

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

// TestReceivingOpenBoxReachesChain documents a HIGH WMS<->chain divergence
// (Receiving-1): close-cargoplace must NOT emit a receiving outbox event for a
// product that is still in an OPEN box. ScanBuffer only places (sets bin_id for)
// products whose box is CLOSED, so an open-box product keeps bin_id NULL; if
// close-cargoplace nonetheless emits its outbox event, the product gets an
// on-chain Accepted transition while being physically unplaceable, and putaway
// can never surface it.
//
// Expected RED on dev / GREEN on fix:
//   - dev: receiving.Repository.listProductIDsByCargoplaceTx selects every
//     RECEIVED product of the cargoplace with no box-status filter, so close-
//     cargoplace inserts a 'receiving' outbox event for the open-box product
//     P — outboxCountForAggregate(P,"receiving") >= 1.
//   - fix: the query JOINs boxes and requires b.status='CLOSED', so the open-box
//     product P is excluded from the emit list — outboxCountForAggregate(P,
//     "receiving") == 0.
//
// close-cargoplace returns 200 on both branches (the fix only narrows the emit
// list; it adds no precondition), so the flow drives entirely through postJSON.
// The only product-level receiving outbox insert is this close-cargoplace path
// (insertOutboxEventsTx), so the count for P is exactly the open-box leak.
func TestReceivingOpenBoxReachesChain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)
	fixture := newOutboundFixture(t, ctx, env)

	// Move cargoplace to TABLE_IN_PROGRESS.
	postJSON[map[string]any](t, env, token, "/receiving/table/scan-cargoplace", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
	}, nil)

	// Open a box (left OPEN — we deliberately never close it).
	var box scanBoxData
	postJSON(t, env, token, "/receiving/table/scan-box", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_barcode":   fixture.BoxBarcode,
	}, &box)

	// Scan the SKU and the QR so product P is RECEIVED inside the OPEN box.
	var sku scanSKUData
	postJSON(t, env, token, "/receiving/table/scan-sku", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"barcode":       fixture.Barcode,
	}, &sku)

	var qr scanQRData
	postJSON(t, env, token, "/receiving/table/scan-qr", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"sku_id":        sku.SKUID.String(),
		"qr_code":       fixture.QRCode,
	}, &qr)
	require.Equal(t, "RECEIVED", qr.Status)
	requireProductStatus(t, ctx, env, qr.ProductID, "RECEIVED")

	// Do NOT close-box. scan-buffer places only CLOSED-box products, so the open-box
	// product P is not placed: products_placed must be 0 and P keeps bin_id NULL.
	var placed scanStorageBinData
	postJSON(t, env, token, "/receiving/table/scan-buffer", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"buffer_bin_id": fixture.ReceivingBinID.String(),
	}, &placed)
	require.Equal(t, 0, placed.ProductsPlaced, "open-box product must not be placed by scan-buffer")

	var binID *uuid.UUID
	err := env.db.QueryRow(ctx, `
		SELECT bin_id FROM wms_inventory.products WHERE product_id = $1`, qr.ProductID).Scan(&binID)
	require.NoError(t, err)
	require.Nil(t, binID, "open-box product must still have bin_id NULL after scan-buffer")

	// close-cargoplace (200 on both branches). On dev it emits a receiving event
	// for P; on fix it filters P out.
	var closed closeCargoplaceData
	postJSON(t, env, token, "/receiving/table/close-cargoplace", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
	}, &closed)

	// Invariant: no receiving outbox event may exist for the open-box product P.
	count := outboxCountForAggregate(t, ctx, env, qr.ProductID, "receiving")
	require.Equalf(t, 0, count,
		"close-cargoplace emitted %d receiving outbox event(s) for open-box product %s; an OPEN-box product (bin_id NULL) must never reach the chain (Receiving-1)",
		count, qr.ProductID)
}

// ─── Defects found in the 2026-05-26 backend audit sweep ────────────────────

// TestDispatchesAuth_CustomerAccess documents Dispatches-1: the three dispatches
// handlers (GetDispatches, NewDispatch, GetDispatchByID) must reject a CUSTOMER-role
// JWT with 403 FORBIDDEN, like every other protected module (receiving/putaway/
// assembly/shipping).
//
// Expected RED on dev / GREEN on fix:
//   - dev: none of the dispatches handlers call requireOperator, so a valid
//     CUSTOMER token passes straight through: GET /dispatches/ returns 200, POST
//     /dispatches/ returns 200/4xx-from-body, and GET /dispatches/{id} returns
//     200/4xx — never 403.
//   - fix: each handler calls requireOperator first, returning 403 FORBIDDEN for
//     any non-OPERATOR role before any body parsing or lookup.
//
// requireOperator runs before body parsing and before any DB lookup, so the dummy
// body and the random {dispatch_id} never need to be valid: the 403 is decided
// purely by the CUSTOMER role.
func TestDispatchesAuth_CustomerAccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	// CUSTOMER token: authenticated but wrong role -> must be 403 FORBIDDEN.
	customerToken := loginAs(t, env, "customer", "customer")

	// getStatusForbidden issues a GET with the CUSTOMER token and asserts 403.
	// Mirrors the manual request style of TestDispatches_GetByID_NotFound: the dev
	// 200 (or any non-403) body is not guaranteed to be enveloped, so we assert on
	// the status code only.
	getStatusForbidden := func(t *testing.T, path string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.wmsURL+path, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+customerToken)

		resp, err := env.httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equalf(t, http.StatusForbidden, resp.StatusCode,
			"GET %s with CUSTOMER token must be 403 FORBIDDEN (Dispatches-1)", path)
	}

	t.Run("GET /dispatches/ -> 403 FORBIDDEN", func(t *testing.T) {
		getStatusForbidden(t, "/dispatches/")
	})

	t.Run("POST /dispatches/ -> 403 FORBIDDEN", func(t *testing.T) {
		// requireOperator rejects before the body is parsed; the destination_id is
		// well-formed only so the request is otherwise valid. dispatches'
		// respondWithError emits the standard {success:false, error:{code,message}}
		// envelope, so postExpectError can decode the 403 error.code.
		status, code, _ := postExpectError(t, env, customerToken, "/dispatches/", map[string]string{
			"destination_id": uuid.NewString(),
		})
		require.Equal(t, http.StatusForbidden, status)
		require.Equal(t, "FORBIDDEN", code)
	})

	t.Run("GET /dispatches/{id} -> 403 FORBIDDEN", func(t *testing.T) {
		getStatusForbidden(t, "/dispatches/"+uuid.NewString())
	})
}

// TestAdapterN9_IntraBatchDuplicate_pendingFix documents N9: if Kafka redelivers
// the same event_id within a single flush window, filterAndMarkPending appends it
// twice. buildBatchArgs produces [id, id], the contract reverts on duplicate
// eventId, and the entire batch is marked FAILED+DLQ — even though all other
// events are valid. Compounds S2 on crash recovery.
func TestAdapterN9_IntraBatchDuplicate_pendingFix(t *testing.T) {
	t.Skip("N9 FIXED in #44 (contract skips intra-batch duplicates + flusher dedups within a flush window); covered by Foundry test_duplicateEventId_withinBatch_skipsSecond + flusher_test TestFlusher_IntraBatchDuplicate_Deduped")

	// Reproduction (needs Kafka message injection):
	//  1. Set BATCH_SIZE=2 and BATCH_TIMEOUT=10s on ledger-adapter.
	//  2. Inject two Kafka messages with identical event_id into the same partition.
	//  3. Observe: filterAndMarkPending (flusher.go:155-180) appends the event twice,
	//     buildBatchArgs produces [id, id], contract reverts "Duplicate eventId",
	//     both rows → FAILED in onchain_events.
	// Invariant that SHOULD hold: filterAndMarkPending must deduplicate within the
	// current batch (e.g., track seen event_ids in a set) so a redelivered message
	// within one flush window does not poison the batch.
}
