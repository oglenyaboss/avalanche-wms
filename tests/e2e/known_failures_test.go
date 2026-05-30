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

// TestS1_WMSChainDivergence_pendingFix is the live regression for the S1 putaway gate
// (#45): once a product's receiving event is FAILED on-chain, the WMS must refuse to
// advance that product to putaway instead of silently diverging from the ledger.
//
// Scope: this MR adds the putaway and pick gates, which stop the divergence cascade at
// its source — a product blocked at putaway never reaches assembly or shipping. The ship
// gate and read-path chain_synced enrichment are tracked as #45 follow-ups.
func TestS1_WMSChainDivergence_pendingFix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)
	fixture := newOutboundFixture(t, ctx, env)

	// 1. Drive the product through the full receiving flow → RECEIVED in BUFFER-01,
	//    emitting a real receiving outbox event.
	productID := driveReceivingToBuffer(t, env, token, fixture)
	requireProductStatus(t, ctx, env, productID, "RECEIVED")

	// 2. Wait for the receiving event to COMMIT on-chain, THEN force it FAILED
	//    (simulating a chain revert / DLQ). Forcing before the row exists would be a
	//    no-op, so the wait is load-bearing.
	receivingEventID := eventIDForAggregate(t, ctx, env, productID, "receiving")
	waitForOnchainCommitted(t, ctx, env, receivingEventID, "receiving")
	setOnchainStatus(t, ctx, env, receivingEventID, "FAILED")

	// 3. The operator can still scan into the putaway cart, but placement must be gated:
	//    scan-storage-bin → 409 CHAIN_EVENT_REJECTED.
	postJSON[map[string]any](t, env, token, "/putaway/scan-buffer", map[string]string{
		"buffer_bin_id": fixture.ReceivingBinID.String(),
	}, nil)
	postJSON[map[string]any](t, env, token, "/putaway/scan-product", map[string]string{
		"product_id":    productID.String(),
		"buffer_bin_id": fixture.ReceivingBinID.String(),
	}, nil)

	status, code, _ := postExpectError(t, env, token, "/putaway/scan-storage-bin", map[string]any{
		"product_ids":    []string{productID.String()},
		"storage_bin_id": fixture.StorageBinID.String(),
	})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "CHAIN_EVENT_REJECTED", code)

	// 4. The product must NOT advance past RECEIVED and no putaway outbox event may be
	//    emitted — WMS and chain do not diverge.
	requireProductStatus(t, ctx, env, productID, "RECEIVED")
	require.Equal(t, 0, outboxCountForAggregate(t, ctx, env, productID, "putaway"),
		"gated putaway must not emit a putaway outbox event")
}

// TestS2_AdapterCrashRecovery_pendingFix exercises the S2 fix (#44): a redelivered
// event whose row is in a NON-TERMINAL crash window (SENT or PENDING with a tx_hash)
// must reconcile to COMMITTED reusing the original tx — NOT resubmit and get marked
// FAILED on the duplicate-eventId revert.
//
// Ported from tests/e2e/scenarios/09-s2-crash-recovery.sh. No adapter restart: the
// SENT/PENDING window is injected via the DB (the offset-uncommitted crash) and the
// same event_id is redelivered via Kafka, exactly like the bash.
func TestS2_AdapterCrashRecovery_pendingFix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	// Each variant injects a different non-terminal status into the crash window.
	for _, inject := range []string{"SENT", "PENDING"} {
		inject := inject
		t.Run("injected_"+inject, func(t *testing.T) {
			eventID := uuid.New()
			productID := uuid.New()

			// 1. Drive a fresh receiving event to a genuine on-chain COMMITTED.
			publishEvent(t, ctx, "receiving", eventID, productID, "{}")
			require.Truef(t, waitOnchainStatus(t, ctx, env, eventID, "COMMITTED", 60*time.Second),
				"[%s] event did not reach COMMITTED before injection (last=%s)", inject, onchainStatus(t, ctx, env, eventID))
			require.Truef(t, waitItemStatus(t, ctx, env, productID, 1, 20*time.Second),
				"[%s] item did not reach on-chain Accepted(1) before injection", inject)

			tx0 := onchainTxHash(t, ctx, env, eventID)
			require.NotEmptyf(t, tx0, "[%s] committed event must have a tx_hash", inject)

			// 2. Simulate the crash window: force the row non-terminal. The Kafka offset
			//    was (conceptually) never committed, so the broker will redeliver.
			setOnchainStatus(t, ctx, env, eventID, inject)

			// 3. Redeliver the SAME event_id.
			publishEvent(t, ctx, "receiving", eventID, productID, "{}")

			// The fix: reconcile the existing tx forward to COMMITTED, never resubmit.
			ok := waitOnchainStatus(t, ctx, env, eventID, "COMMITTED", 60*time.Second)
			if !ok {
				st := onchainStatus(t, ctx, env, eventID)
				require.NotEqualf(t, "FAILED", st,
					"[%s] event went FAILED after redelivery, but it already COMMITTED on-chain (S2 regression)", inject)
				require.Failf(t, "not committed",
					"[%s] expected COMMITTED after reconcile, got %q", inject, st)
			}

			// tx_hash must be the ORIGINAL mined tx — reconcile reuses it, never resubmits.
			txn := onchainTxHash(t, ctx, env, eventID)
			require.Equalf(t, tx0, txn,
				"[%s] tx_hash changed (%s -> %s); the original tx was discarded, not reused (S2)", inject, tx0, txn)

			// on-chain itemStatus must be unchanged at Accepted(1): no duplicate transition.
			require.EqualValuesf(t, 1, callItemStatus(t, ctx, env, productID),
				"[%s] on-chain itemStatus changed; expected 1 (no duplicate transition)", inject)
		})
	}
}

// TestS3_BatchPoisoning_pendingFix exercises the S3 fix (#47, Design A): one poisoned
// item in a batch must NOT drag its valid siblings to FAILED. The valid siblings reach
// COMMITTED; the poison is skipped on-chain via an ItemTransitionFailed event and its
// DB row ends COMMITTED (the batch tx succeeds).
//
// Ported from tests/e2e/scenarios/10-s3-batch-poisoning.sh. Widens BATCH_TIMEOUT so the
// rapid-fire publishes co-batch into one tx; restores the default adapter on cleanup.
func TestS3_BatchPoisoning_pendingFix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	// Widen the batch window so the sequential in-process publishes land in one tx.
	// 10s is generous: in-process publishes complete in milliseconds.
	t.Cleanup(func() { restoreDefaultAdapter(t, env) })
	recreateAdapterWithEnv(t, ctx, env, map[string]string{"BATCH_TIMEOUT": "10s"})

	const n = 4
	validEvents := make([]uuid.UUID, n)
	validProducts := make([]uuid.UUID, n)
	for i := range validEvents {
		validEvents[i] = uuid.New()
		validProducts[i] = uuid.New()
	}

	// Poison product PX: pre-Accept it via its OWN committed receiving event, so a
	// SECOND receiving event for it (None->Accepted required, but it is already
	// Accepted) is the poison — an itemStatus mismatch.
	poisonProduct := uuid.New()
	poisonFirstEvent := uuid.New()
	publishEvent(t, ctx, "receiving", poisonFirstEvent, poisonProduct, "{}")
	require.Truef(t, waitOnchainStatus(t, ctx, env, poisonFirstEvent, "COMMITTED", 60*time.Second),
		"poison product's first (valid) receiving did not reach COMMITTED")
	require.Truef(t, waitItemStatus(t, ctx, env, poisonProduct, 1, 20*time.Second),
		"poison product did not reach on-chain Accepted(1) after its first receiving")

	// Rapid-fire one batch: N fresh-valid receivings + 1 poisoned receiving (re-accept PX).
	poisonEvent := uuid.New()
	for i := range validEvents {
		publishEvent(t, ctx, "receiving", validEvents[i], validProducts[i], "{}")
	}
	publishEvent(t, ctx, "receiving", poisonEvent, poisonProduct, "{}")

	// 1. Every valid sibling must reach COMMITTED (the core S3 fix — they were dragged
	//    to FAILED before, by the whole-batch revert) and be Accepted(1) on-chain.
	for i := range validEvents {
		require.Truef(t, waitOnchainStatus(t, ctx, env, validEvents[i], "COMMITTED", 60*time.Second),
			"valid sibling %s did not reach COMMITTED (poison fanned out — S3 regression)", validEvents[i])
		require.EqualValuesf(t, 1, callItemStatus(t, ctx, env, validProducts[i]),
			"valid sibling %s on-chain itemStatus != 1", validProducts[i])
	}

	// 2. The poisoned event's row ends COMMITTED — the batch tx succeeded (Design A).
	require.Truef(t, waitOnchainStatus(t, ctx, env, poisonEvent, "COMMITTED", 30*time.Second),
		"poisoned event expected COMMITTED (batch tx succeeded), got %q", onchainStatus(t, ctx, env, poisonEvent))

	// 3. Isolation proven on-chain: the poison item must NOT have advanced — it was
	//    Accepted(1) and a second accept is rejected, so it stays 1 (no double transition).
	require.EqualValuesf(t, 1, callItemStatus(t, ctx, env, poisonProduct),
		"poison item on-chain itemStatus != 1 (expected unchanged — no double transition)")

	// 4. Guard: the N valids + poison must have shared ONE batch tx, else the test
	//    proved nothing about isolation. With BATCH_TIMEOUT=10s and in-process
	//    millisecond-apart publishes this co-batch is reliable, so it is enforced as a
	//    hard precondition (no silent skip). A failure here is a co-batch TIMING issue
	//    (raise BATCH_TIMEOUT / check broker latency), NOT an S3 fix regression.
	allEvents := append(append([]uuid.UUID{}, validEvents...), poisonEvent)
	cobatchTxs := distinctTxHashCount(t, ctx, env, allEvents...)
	require.Equalf(t, 1, cobatchTxs,
		"co-batch PRECONDITION failed: the %d valid events + poison span %d distinct batch txs, not 1 "+
			"— a BATCH_TIMEOUT/broker-timing issue (raise BATCH_TIMEOUT), NOT an S3 fix regression",
		len(allEvents), cobatchTxs)

	// 5. The skip is observable: the shared batch tx emitted an ItemTransitionFailed log.
	batchTx := onchainTxHash(t, ctx, env, poisonEvent)
	require.NotEmpty(t, batchTx, "poison event missing tx_hash after COMMITTED")
	require.Truef(t, txEmittedItemTransitionFailed(t, ctx, env, batchTx),
		"batch tx %s did not emit ItemTransitionFailed for the poison item (S3 isolation not observable)", batchTx)
}

// ─── Additional defects found in the 2026-05-25 multi-module bug-hunt sweep ───
// Same convention as S1/S2/S3: documented here as t.Skip stubs with the intended
// invariant and a runnable reproduction. The two pure-HTTP ones (shipping
// ship-before-assembled, receiving open-box) can flip into live regressions by
// implementing the body once the product is fixed; see tests/e2e/BUGHUNT.md for the
// full backlog (severity, file:line, and the deferred lower-severity findings).

// TestAssemblyCartLostOnRestart_pendingFix is the live regression for Assembly BUG-1 (#46):
// before the fix the pick cart lived only in WMS process memory, so a WMS restart between
// Pick and ScanShippingBuffer stranded the picked product ASSEMBLED with its order ALLOCATED
// forever. Now ScanShippingBuffer derives the cart from DB state (the operator's ASSEMBLED
// products with a DONE task), so the product survives a restart and is placed normally.
func TestAssemblyCartLostOnRestart_pendingFix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)
	fx := newMultiProductFixture(t, ctx, env, "SHOP-7", 1, 1)

	// Allocate and pick the single product (-> ASSEMBLED, task DONE by this operator).
	var allocated allocateData
	postJSON(t, env, token, "/assembly/allocate", map[string]string{
		"destination_id": fx.DestinationID.String(),
	}, &allocated)
	requireOrderStatus(t, ctx, env, fx.OrderID, "ALLOCATED")

	orderProductIDs := productIDsForOrder(t, ctx, env, fx.OrderID)
	require.Len(t, orderProductIDs, 1)
	productID, err := uuid.Parse(orderProductIDs[0])
	require.NoError(t, err)

	var picked pickData
	postJSON(t, env, token, "/assembly/pick", map[string]string{"product_id": productID.String()}, &picked)
	requireProductStatus(t, ctx, env, productID, "ASSEMBLED")

	// Restart the WMS: any in-memory cart is dropped here.
	restartWMS(t, ctx, env)

	// Reuse the SAME operator token: the derived cart is scoped to assembly_tasks.operator_id,
	// so it must match the operator that picked. The JWT is stateless and survives the restart
	// (stable JWT_SECRET; the user persists in the un-restarted DB), and operatorToken would
	// otherwise mint a brand-new operator.

	// The fix: scan-shipping-buffer reconstructs the cart from DB, so the picked product is
	// placed (products_placed=1) instead of being stranded with 409 CART_EMPTY.
	var placed scanShippingBufferData
	postJSON(t, env, token, "/assembly/scan-shipping-buffer", map[string]string{
		"buffer_bin_id": fx.ShippingBinID.String(),
	}, &placed)
	require.Equal(t, 1, placed.ProductsPlaced, "picked product must survive a WMS restart (cart derived from DB)")

	requireProductStatus(t, ctx, env, productID, "READY_TO_SHIP")
	requireOrderStatus(t, ctx, env, fx.OrderID, "ASSEMBLED")
}

// TestAdapterN1_ReceiptTimeoutDivergence_pendingFix documents a CRITICAL bug found in
// the sweep, distinct from S2 and needing NO crash: on a WaitReceipt timeout the
// flusher marks the event FAILED (ledger-adapter/internal/consumer/flusher.go:124-129)
// even though the tx may still be mining. Under chain congestion the tx mines moments
// later -> chain shows the transition COMMITTED while the DB row is terminal FAILED,
// with no reconciliation path back out of FAILED.
func TestAdapterN1_ReceiptTimeoutDivergence_pendingFix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	// Shrink RECEIPT_POLL_TIMEOUT so the flusher's poll always loses the race to mining
	// (transiently marking the row FAILED), and shrink the reconcile cadence so recovery
	// happens within the test's patience window. CONSTRAINT: RECONCILE_MIN_AGE (2s) must
	// stay > RECEIPT_POLL_TIMEOUT (1ms) or the adapter refuses to boot — 2s > 1ms is fine.
	t.Cleanup(func() { restoreDefaultAdapter(t, env) })
	recreateAdapterWithEnv(t, ctx, env, map[string]string{
		"RECEIPT_POLL_TIMEOUT": "1ms",
		"RECONCILE_INTERVAL":   "2s",
		"RECONCILE_MIN_AGE":    "2s",
	})

	eventID := uuid.New()
	productID := uuid.New()

	// Publish a perfectly valid receiving event. With the tiny RECEIPT_POLL_TIMEOUT the
	// adapter (transiently) marks it FAILED on the poll timeout while the tx still mines.
	publishEvent(t, ctx, "receiving", eventID, productID, "{}")

	// The transition DOES land on-chain (the tx mines regardless of the adapter giving up).
	// Enforce this as a hard precondition (no silent skip). 120s cap (vs the bash's 60s):
	// under a fresh full-stack bring-up the broadcast tx has been observed to surface only
	// after ~50-65s. A failure here means the tx never reached the chain — a chain
	// liveness/mining TIMING issue, NOT an N1 reconcile-fix regression.
	require.Truef(t, waitItemStatus(t, ctx, env, productID, 1, 120*time.Second),
		"mining PRECONDITION failed: the receiving transition never reached the chain within 120s "+
			"— chain liveness/mining timing, NOT an N1 reconcile regression")

	// The reconcile loop must pull the (wrongly) FAILED row forward to COMMITTED once the
	// tx is mined. Poll generously to cover min-age (2s) + interval (2s) + slack.
	if !waitOnchainStatus(t, ctx, env, eventID, "COMMITTED", 50*time.Second) {
		st := onchainStatus(t, ctx, env, eventID)
		require.NotEqualf(t, "FAILED", st,
			"event is FAILED in DB but the transition is COMMITTED on-chain — reconcile loop did not recover it (N1 regression)")
		require.Failf(t, "not committed", "expected COMMITTED once the tx mined, got %q", st)
	}

	// tx_hash must be the original mined tx — reconcile reuses it, never resubmits.
	require.NotEmpty(t, onchainTxHash(t, ctx, env, eventID),
		"COMMITTED row has no tx_hash; reconcile must reuse the mined tx")
}

// TestShippingShipBeforeAssembled_pendingFix is the live regression for Shipping-2 (#48):
// an order whose items are shipped before it ever reaches ASSEMBLED must NOT be stranded
// in ALLOCATED. Before the fix, UpdateOrdersShippedConditional only advanced ASSEMBLED
// orders, so shipping a subset of an ALLOCATED order left it ALLOCATED forever. After the
// fix, the order moves to PARTIALLY_SHIPPED — a non-terminal state from which it still
// completes to SHIPPED (PARTIALLY_SHIPPED -> SHIPPED is covered by TestPartialShipment_MofN).
//
// Pure-HTTP / DB-contract test: newMultiProductFixture inserts synthetic STORED products
// with no chain history, so their picking/shipping events revert on-chain and never COMMIT.
// The WMS commits its DB transaction before the chain is touched and the ship path is not
// chain-gated, so the HTTP flow succeeds and every assertion is DB-only.
func TestShippingShipBeforeAssembled_pendingFix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)
	fx := newMultiProductFixture(t, ctx, env, "SHOP-7", 2, 2)

	// Allocate: NEW -> ALLOCATED, two PENDING tasks.
	var allocated allocateData
	postJSON(t, env, token, "/assembly/allocate", map[string]string{
		"destination_id": fx.DestinationID.String(),
	}, &allocated)
	requireOrderStatus(t, ctx, env, fx.OrderID, "ALLOCATED")

	orderProductIDs := productIDsForOrder(t, ctx, env, fx.OrderID)
	require.Len(t, orderProductIDs, 2)
	p1, err := uuid.Parse(orderProductIDs[0])
	require.NoError(t, err)
	p2, err := uuid.Parse(orderProductIDs[1])
	require.NoError(t, err)

	// Pick ONLY P1; P2 stays ALLOCATED with a PENDING task.
	var picked pickData
	postJSON(t, env, token, "/assembly/pick", map[string]string{"product_id": p1.String()}, &picked)

	// Scan the shipping buffer: P1 -> READY_TO_SHIP. The order does NOT reach ASSEMBLED
	// because P2 is still ALLOCATED (UpdateOrdersToAssembled needs every product ready).
	var placed scanShippingBufferData
	postJSON(t, env, token, "/assembly/scan-shipping-buffer", map[string]string{
		"buffer_bin_id": fx.ShippingBinID.String(),
	}, &placed)
	require.Equal(t, 1, placed.ProductsPlaced)
	requireOrderStatus(t, ctx, env, fx.OrderID, "ALLOCATED")
	requireProductStatus(t, ctx, env, p1, "READY_TO_SHIP")
	requireProductStatus(t, ctx, env, p2, "ALLOCATED")

	// Bring the dispatch to the gate and ship P1 only.
	postJSON[map[string]any](t, env, token, "/shipping/scan-buffer", map[string]string{
		"buffer_bin_id": fx.ShippingBinID.String(),
	}, nil)
	var driver scanDriverData
	postJSON(t, env, token, "/shipping/scan-driver", map[string]string{
		"dispatch_code": fx.DispatchCode,
	}, &driver)
	require.Equal(t, "AT_GATE", driver.Status)

	var shipped shipData
	postJSON(t, env, token, "/shipping/ship", map[string]any{
		"buffer_bin_id": fx.ShippingBinID.String(),
		"dispatch_id":   fx.DispatchID.String(),
		"product_ids":   []string{p1.String()},
	}, &shipped)
	require.Equal(t, 1, shipped.ProductsShipped)
	require.Equal(t, 0, shipped.OrdersCompleted, "order is not complete — P2 never shipped")
	require.Equal(t, 1, shipped.OrdersPartiallyShipped, "order moves to PARTIALLY_SHIPPED, not stranded ALLOCATED")

	requireProductStatus(t, ctx, env, p1, "SHIPPED")
	requireProductStatus(t, ctx, env, p2, "ALLOCATED")
	// The fix: before #48 this order was stuck ALLOCATED forever; now it is PARTIALLY_SHIPPED.
	requireOrderStatus(t, ctx, env, fx.OrderID, "PARTIALLY_SHIPPED")
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
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	// Widen BATCH_TIMEOUT so the two back-to-back in-process publishes of the SAME
	// event_id land in ONE flush window (the intra-batch duplicate the fix dedups).
	// key=product_id keeps both on one partition, so they are delivered in-order to
	// the same poll batch.
	t.Cleanup(func() { restoreDefaultAdapter(t, env) })
	recreateAdapterWithEnv(t, ctx, env, map[string]string{"BATCH_TIMEOUT": "10s"})

	eventID := uuid.New()
	productID := uuid.New()

	// Publish the SAME event_id twice, back-to-back. Pre-fix: filterAndMarkPending
	// appended it twice -> buildBatchArgs [id,id] -> contract "Duplicate eventId"
	// revert -> both rows FAILED+DLQ. Post-fix: the flusher dedups within the window
	// and the contract skips an intra-batch duplicate, so the event commits cleanly.
	publishEvent(t, ctx, "receiving", eventID, productID, "{}")
	publishEvent(t, ctx, "receiving", eventID, productID, "{}")

	// The invariant holds on BOTH the intra-batch path (deduped) and the unlikely
	// split-window path (the 2nd becomes an S2-style redelivery, reconciled forward):
	// the event must end COMMITTED, never FAILED.
	if !waitOnchainStatus(t, ctx, env, eventID, "COMMITTED", 60*time.Second) {
		st := onchainStatus(t, ctx, env, eventID)
		require.NotEqualf(t, "FAILED", st,
			"duplicate event_id poisoned the batch: row is FAILED (N9 regression — intra-batch dedup missing)")
		require.Failf(t, "not committed", "expected COMMITTED, got %q", st)
	}

	// On-chain the item is Accepted(1) exactly once (no double transition).
	require.EqualValuesf(t, 1, callItemStatus(t, ctx, env, productID),
		"on-chain itemStatus != 1 after duplicate publish (expected single Accepted transition)")

	// Exactly ONE onchain_events row exists for the event_id (the dup did not create a
	// second row, and it is not FAILED).
	var rowCount int
	require.NoError(t, env.db.QueryRow(ctx,
		`SELECT count(*) FROM public.onchain_events WHERE event_id = $1`, eventID).Scan(&rowCount))
	require.Equalf(t, 1, rowCount, "expected exactly one onchain_events row for the deduped event_id, got %d", rowCount)
}
