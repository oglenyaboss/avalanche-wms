//go:build e2e

package e2e

import (
	"context"
	"encoding/hex"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ── signed ship (shipping buffer → trip) DTOs and helpers ────────────────────

// draftShip POSTs the ship draft endpoint: the draft response has the same shape
// as the other signed drafts, so it reuses closeDraftDTO. Empty (or nil)
// productIDs means the whole bin (bulk mode).
func draftShip(t *testing.T, env *env, op *signedOperator, bufferBinID, dispatchID string, productIDs []uuid.UUID) closeDraftDTO {
	t.Helper()
	var draft closeDraftDTO
	postJSON(t, env, op.token, "/shipping/ship/draft", map[string]any{
		"buffer_bin_id": bufferBinID,
		"dispatch_id":   dispatchID,
		"product_ids":   uuidStrings(productIDs),
	}, &draft)
	return draft
}

// draftShipRaw POSTs the ship draft endpoint and returns the raw error envelope
// (status + code) without failing, for guard tests.
func draftShipRaw(t *testing.T, env *env, token, bufferBinID, dispatchID string, productIDs []string) (int, string) {
	t.Helper()
	status, code, _ := postExpectError(t, env, token, "/shipping/ship/draft", map[string]any{
		"buffer_bin_id": bufferBinID,
		"dispatch_id":   dispatchID,
		"product_ids":   productIDs,
	})
	return status, code
}

// submitShip POSTs the ship submit endpoint and requires 200.
func submitShip(t *testing.T, env *env, token, operationID string, assertions []map[string]string) shipData {
	t.Helper()
	var result shipData
	postJSON(t, env, token, "/shipping/ship/submit", map[string]any{
		"operation_id": operationID,
		"assertions":   assertions,
	}, &result)
	return result
}

// shipSigned runs the whole signed ship: draft → sign → submit. Returns the
// submit response plus the draft's operation id (for GET /signed-actions/operations).
func shipSigned(t *testing.T, env *env, op *signedOperator, bufferBinID, dispatchID string, productIDs []uuid.UUID) (shipData, string) {
	t.Helper()
	draft := draftShip(t, env, op, bufferBinID, dispatchID, productIDs)
	assertions := signDraft(t, op, draft)
	return submitShip(t, env, op.token, draft.OperationID, assertions), draft.OperationID
}

// softShipDetailsHash is an independent re-implementation of ADR-0001 §4 for
// Ship: keccak256(abi.encode(bytes32 salt, bytes16 dispatchId, bytes16
// shippingBufferBinId, bytes16[] orderIds, bytes16 operatorId, uint64
// occurredAtMs)). Head: salt, dispatch, bin, offset 0xc0 (6 static words),
// operator, uint64; tail: length, one left-aligned bytes16 per orderId.
// orderIds go ascending by raw bytes without duplicates.
func softShipDetailsHash(salt [32]byte, dispatchID, binID uuid.UUID, orderIDs []uuid.UUID, operatorID uuid.UUID, occurredAtMs uint64) [32]byte {
	orders := sortedUniqueUUIDs(orderIDs)
	words := make([]any, 0, 7+len(orders))
	words = append(words, salt, dispatchID, binID, 0xc0, operatorID, occurredAtMs, len(orders))
	for _, id := range orders {
		words = append(words, id)
	}
	var out [32]byte
	copy(out[:], crypto.Keccak256(softDetailsWords(words...)))
	return out
}

// markPickingConfirmed is a fixture shortcut for DB-contract tests that do not
// exercise the chain: per product it inserts a legacy picking outbox row
// (aggregate_type 'picking', aggregate_id = product) and a COMMITTED
// onchain_events row for that event, so the signed ship's strict gate (picking
// must be COMMITTED on chain) lets the flow pass without a real chain
// round-trip. The rows are picked up by the fixture cleanup (both are keyed by
// aggregate_id = product). Use it only where the real chain is not exercised.
func markPickingConfirmed(t *testing.T, ctx context.Context, env *env, productIDs []uuid.UUID) {
	t.Helper()
	for _, pid := range productIDs {
		eventID := uuid.New()
		mustExec(t, ctx, env, `
			INSERT INTO public.outbox_events (event_id, aggregate_id, aggregate_type, event_type, payload_hash)
			VALUES ($1, $2, 'picking', 'wms.picking.v1', 'fixture')`, eventID, pid)
		mustExec(t, ctx, env, `
			INSERT INTO public.onchain_events (event_id, aggregate_type, status)
			VALUES ($1, 'picking', 'COMMITTED')`, eventID)
	}
}

// parseProductIDs parses product id strings into uuids.
func parseProductIDs(t *testing.T, ids []string) []uuid.UUID {
	t.Helper()
	out := make([]uuid.UUID, 0, len(ids))
	for _, s := range ids {
		id, err := uuid.Parse(s)
		require.NoError(t, err, "parse product id %s", s)
		out = append(out, id)
	}
	return out
}

// ── TestSignedShip_EndToEnd ──────────────────────────────────────────────────

// TestSignedShip_EndToEnd drives the signed Ship end to end: two orders of one
// destination received, put away, picked and unloaded by signed actions (the
// Pick committed on chain), then scan-driver and a signed bulk ship of the
// buffer bin. Asserts the draft challenge against the independent keccak, the
// detailsHash recomputed from the domain tables, the shippings.action_id link,
// the on-chain ActionApplied/ItemMoved events and the final DB state.
func TestSignedShip_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	op := newSignedOperator(t, ctx, env)

	// The destination of the whole flow (orders, buffer bin and dispatch share it).
	var destinationID uuid.UUID
	err := env.db.QueryRow(ctx, `
		SELECT d.destination_id
		FROM wms_inventory.warehouses w
		JOIN wms_inventory.destinations d ON d.warehouse_id = w.warehouse_id AND d.code = $1
		WHERE w.name = $2`, destinationCode, warehouseName).Scan(&destinationID)
	require.NoError(t, err, "resolve destination")

	// A dedicated shipping buffer bin for this run: the bulk ship drafts the WHOLE
	// bin, so a private bin keeps the signed set deterministic.
	shipBinCode := "BIN-SHIP-E2E-" + shortSuffix()
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.bins(bin_id,warehouse_id,destination_id,code,section,created_at,updated_at)
		SELECT gen_random_uuid(),w.warehouse_id,d.destination_id,$1,'SHIPPING_BUFFER',now(),now()
		FROM wms_inventory.warehouses w
		JOIN wms_inventory.destinations d ON d.warehouse_id=w.warehouse_id AND d.code=$2
		WHERE w.name=$3`, shipBinCode, destinationCode, warehouseName)
	shipBinID := resolveBinID(t, ctx, env, shipBinCode)

	// A dedicated SCHEDULED dispatch for the destination.
	dispatchCode := "DSP-SS-" + shortSuffix()
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.outbound_dispatches(dispatch_id,dispatch_code,warehouse_id,destination_id,vehicle_number,driver_name,driver_phone,status,scheduled_at,created_at,updated_at)
		SELECT gen_random_uuid(),$1,w.warehouse_id,d.destination_id,'E2ESS','E2E SS Driver','+70000000003','SCHEDULED'::wms_inventory.outbound_dispatch_status,now()+interval '1 day',now(),now()
		FROM wms_inventory.warehouses w
		JOIN wms_inventory.destinations d ON d.code=$2 AND d.warehouse_id=w.warehouse_id
		WHERE w.name=$3`, dispatchCode, destinationCode, warehouseName)
	var dispatchID uuid.UUID
	err = env.db.QueryRow(ctx, `SELECT dispatch_id FROM wms_inventory.outbound_dispatches WHERE dispatch_code=$1`, dispatchCode).Scan(&dispatchID)
	require.NoError(t, err, "resolve ship dispatch id")

	// ── Signed receive of two products of the seeded SKU ──
	sc := setupReceivingScaffold(t, ctx, env, 2)
	run := receiveAndCloseSigned(t, ctx, env, op, sc, 2)
	require.Len(t, run.productIDs, 2)
	for _, pid := range run.productIDs {
		e := eventIDForAggregate(t, ctx, env, pid, "receiving")
		waitForOnchainCommitted(t, ctx, env, e, "receiving")
	}

	// ── Signed putaway of both products into the storage bin ──
	placedPutaway := placeToStorageBinSigned(t, env, op, run.productIDs, sc.StorageBinID)
	require.Equal(t, 2, placedPutaway.ProductsPlaced)
	for _, pid := range run.productIDs {
		requireProductStatus(t, ctx, env, pid, "STORED")
		e := eventIDForAggregate(t, ctx, env, pid, "putaway")
		waitForOnchainCommitted(t, ctx, env, e, "putaway")
		require.Equal(t, statusPutAway, callItemStatus(t, ctx, env, pid),
			"product should be PutAway(2) on-chain after the signed putaway")
	}

	// ── Two NEW orders for the destination, one line of qty 1 each ──
	orderNo1 := "ORD-SS1-" + shortSuffix()
	orderNo2 := "ORD-SS2-" + shortSuffix()
	for _, orderNo := range []string{orderNo1, orderNo2} {
		mustExec(t, ctx, env, `
			INSERT INTO wms_inventory.orders(order_id,external_order_no,customer_id,warehouse_id,destination_id,status,created_at,updated_at)
			SELECT gen_random_uuid(),$1,u.user_id,w.warehouse_id,d.destination_id,'NEW'::wms_inventory.order_status,now(),now()
			FROM public.users u
			JOIN wms_inventory.warehouses w ON w.name=$2
			JOIN wms_inventory.destinations d ON d.code=$3 AND d.warehouse_id=w.warehouse_id
			WHERE u.username='customer'`, orderNo, warehouseName, destinationCode)
		mustExec(t, ctx, env, `
			INSERT INTO wms_inventory.order_lines(order_id,sku_id,qty)
			SELECT o.order_id,s.sku_id,1
			FROM wms_inventory.orders o
			JOIN wms_inventory.skus s ON s.name=$2
			WHERE o.external_order_no=$1`, orderNo, outboundSeedSKUName)
	}
	resolveOrder := func(orderNo string) uuid.UUID {
		var id uuid.UUID
		err := env.db.QueryRow(ctx, `SELECT order_id FROM wms_inventory.orders WHERE external_order_no=$1`, orderNo).Scan(&id)
		require.NoError(t, err, "resolve order %s", orderNo)
		return id
	}
	order1, order2 := resolveOrder(orderNo1), resolveOrder(orderNo2)

	// Cleanup: mirror the signed-pick fixture cleanup for the rows this test
	// creates (runs LIFO before the receiving scaffold cleanup, preserving FK
	// order). pickActionID/shipActionID are captured after each signed submit.
	var pickActionID, shipActionID uuid.UUID
	productIDStrings := uuidStrings(run.productIDs)
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		actionIDs := []uuid.UUID{pickActionID, shipActionID}
		cpSel := `(SELECT cargoplace_id FROM wms_inventory.cargoplaces WHERE cargoplace_code=$1)`
		stmts := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM public.onchain_events WHERE event_id = ANY($1::uuid[])
			   OR event_id IN (SELECT event_id FROM public.outbox_events WHERE aggregate_id = ANY($2::uuid[]))`, []any{actionIDs, productIDStrings}},
			{`DELETE FROM public.outbox_events WHERE event_id = ANY($1::uuid[]) OR aggregate_id = ANY($2::uuid[])`, []any{actionIDs, productIDStrings}},
			{`DELETE FROM wms_ops.shippings WHERE product_id = ANY($1::uuid[])`, []any{productIDStrings}},
			{`DELETE FROM wms_ops.assembly_tasks WHERE product_id = ANY($1::uuid[])`, []any{productIDStrings}},
			{`DELETE FROM wms_ops.putaways WHERE product_id = ANY($1::uuid[])`, []any{productIDStrings}},
			{`DELETE FROM wms_ops.receiving_table WHERE cargoplace_id IN ` + cpSel, []any{sc.CargoplaceCode}},
			{`DELETE FROM wms_ops.receiving_gate WHERE cargoplace_id IN ` + cpSel, []any{sc.CargoplaceCode}},
			{`DELETE FROM wms_inventory.products WHERE product_id = ANY($1::uuid[])`, []any{productIDStrings}},
			{`DELETE FROM wms_inventory.boxes WHERE cargoplace_id IN ` + cpSel, []any{sc.CargoplaceCode}},
			{`DELETE FROM wms_ops.assembly_tasks WHERE order_id IN (SELECT order_id FROM wms_inventory.orders WHERE external_order_no = ANY($1::text[]))`, []any{[]string{orderNo1, orderNo2}}},
			{`DELETE FROM wms_inventory.order_lines WHERE order_id IN (SELECT order_id FROM wms_inventory.orders WHERE external_order_no = ANY($1::text[]))`, []any{[]string{orderNo1, orderNo2}}},
			{`DELETE FROM wms_inventory.orders WHERE external_order_no = ANY($1::text[])`, []any{[]string{orderNo1, orderNo2}}},
			{`DELETE FROM wms_inventory.outbound_dispatches WHERE dispatch_code=$1`, []any{dispatchCode}},
			{`DELETE FROM wms_inventory.bins WHERE code=$1`, []any{shipBinCode}},
		}
		for _, stmt := range stmts {
			if _, err := env.db.Exec(cleanupCtx, stmt.sql, stmt.args...); err != nil {
				t.Logf("signed ship cleanup (%s): %v", stmt.sql, err)
			}
		}
	})

	// ── Allocate the destination: both orders are picked up ──
	var allocated allocateData
	postJSON(t, env, op.token, "/assembly/allocate", map[string]string{
		"destination_id": destinationID.String(),
	}, &allocated)
	require.GreaterOrEqual(t, allocated.AllocatedOrders, 2)
	require.GreaterOrEqual(t, allocated.AllocatedProducts, 2)

	order1Products := productIDsForOrder(t, ctx, env, order1)
	order2Products := productIDsForOrder(t, ctx, env, order2)
	require.Len(t, order1Products, 1)
	require.Len(t, order2Products, 1)
	allProducts := append(order1Products, order2Products...)
	// The signed chain below is valid only for THIS test's own products: allocate
	// is FIFO over ALL STORED products of the SKU, so if stale leftovers of earlier
	// tests sit in the pool, the pick would chain-fail on foreign items. Guard it.
	require.ElementsMatchf(t, run.productIDs, allProducts,
		"allocate must hand out this test's own signed-chain products, got stale FIFO leftovers")

	for _, pid := range allProducts {
		parsed, err := uuid.Parse(pid)
		require.NoError(t, err)
		var picked pickData
		postJSON(t, env, op.token, "/assembly/pick", map[string]string{"product_id": pid}, &picked)
		requireProductStatus(t, ctx, env, parsed, "ASSEMBLED")
		requireTaskStatus(t, ctx, env, parsed, "DONE")
	}

	// ── Unload the basket into the private shipping buffer (signed Pick) ──
	placed := unloadToShippingBufferSigned(t, env, op, shipBinID.String())
	require.Equal(t, 2, placed.ProductsPlaced)
	require.Equal(t, 2, placed.OrdersAssembled, "both fully-picked orders become ASSEMBLED")
	for _, pid := range run.productIDs {
		requireProductStatus(t, ctx, env, pid, "READY_TO_SHIP")
	}
	pickActionID = eventIDForAggregate(t, ctx, env, run.productIDs[0], "picking")

	// The signed ship gate requires COMMITTED picking: wait for the Pick action.
	for _, pid := range run.productIDs {
		e := eventIDForAggregate(t, ctx, env, pid, "picking")
		waitForOnchainCommitted(t, ctx, env, e, "picking")
		require.Equal(t, statusPicked, callItemStatus(t, ctx, env, pid),
			"product on-chain status should be Picked(3) before shipping")
	}

	// ── Scan buffer + driver ──
	postJSON[map[string]any](t, env, op.token, "/shipping/scan-buffer", map[string]string{
		"buffer_bin_id": shipBinID.String(),
	}, nil)
	var driver scanDriverData
	postJSON(t, env, op.token, "/shipping/scan-driver", map[string]string{
		"dispatch_code": dispatchCode,
	}, &driver)
	require.Equal(t, dispatchID, driver.DispatchID)
	require.Equal(t, "AT_GATE", driver.Status)

	// ── Draft the signed bulk ship of the whole bin ──
	draft := draftShip(t, env, op, shipBinID.String(), dispatchID.String(), nil)
	require.Equal(t, 4, draft.ActionType, "the ship draft is a signed Ship (type 4)")
	require.Len(t, draft.Parts, 1, "two products draft into exactly one part")
	part := draft.Parts[0]
	shipActionID, err = uuid.Parse(part.ActionID)
	require.NoError(t, err)

	itemIDs := make([]uuid.UUID, 0, len(part.ItemIDs))
	for i, raw := range part.ItemIDs {
		id, err := uuid.Parse(raw)
		require.NoError(t, err)
		itemIDs = append(itemIDs, id)
		if i > 0 {
			require.Negativef(t,
				uuidToUint256(part.ItemIDs[i-1]).Cmp(uuidToUint256(raw)),
				"item_ids must ascend by itemId (uint256), position %d", i)
		}
	}
	require.ElementsMatch(t, run.productIDs, itemIDs, "the draft signs exactly the bin's products")

	detailsHashBytes, err := hex.DecodeString(strings.TrimPrefix(part.DetailsHash, "0x"))
	require.NoError(t, err)
	var draftDetailsHash [32]byte
	copy(draftDetailsHash[:], detailsHashBytes)

	challengeRaw, err := decodeBase64URL(part.Challenge)
	require.NoError(t, err)
	var draftChallenge [32]byte
	copy(draftChallenge[:], challengeRaw)
	wantChallenge := softChallenge(shipActionID, 4, softItemsHash(itemIDs), draftDetailsHash)
	require.Equal(t, wantChallenge[:], draftChallenge[:], "draft challenge is not the ADR-0001 challenge")

	// ── Submit: both orders ship, the dispatch departs ──
	shipped := submitShip(t, env, op.token, draft.OperationID, signDraft(t, op, draft))
	require.Equal(t, 2, shipped.ProductsShipped)
	require.Equal(t, 1, shipped.OutboxEventsCreated, "a bin of <2000 products ships with exactly one signed part")
	require.Equal(t, 2, shipped.OrdersCompleted)
	require.True(t, shipped.DispatchDeparted)
	require.Equal(t, 0, shipped.BufferRemaining)
	require.Equal(t, draft.OperationID, shipped.OperationID)

	for _, pid := range run.productIDs {
		requireProductStatus(t, ctx, env, pid, "SHIPPED")
	}
	requireOrderStatus(t, ctx, env, order1, "SHIPPED")
	requireOrderStatus(t, ctx, env, order2, "SHIPPED")
	requireDispatchStatus(t, ctx, env, dispatchID, "DEPARTED")

	// ── detailsHash recomputed from the domain tables the action changed ──
	var saltBytes []byte
	err = env.db.QueryRow(ctx, `SELECT salt FROM public.warehouse_actions WHERE action_id = $1`, shipActionID).Scan(&saltBytes)
	require.NoError(t, err)
	var salt [32]byte
	copy(salt[:], saltBytes)

	var shippingDispatchID, shippingOperatorID uuid.UUID
	var occurredAt, shippedAt time.Time
	err = env.db.QueryRow(ctx, `
		SELECT DISTINCT dispatch_id, operator_id, occurred_at, shipped_at
		FROM wms_ops.shippings
		WHERE product_id = ANY($1::uuid[])`, itemIDs).Scan(&shippingDispatchID, &shippingOperatorID, &occurredAt, &shippedAt)
	require.NoError(t, err)
	require.Equal(t, dispatchID, shippingDispatchID, "shippings.dispatch_id must hold the trip")
	require.Equal(t, op.userID, shippingOperatorID)
	require.Equal(t, occurredAt, shippedAt, "shipped_at must equal the details time (occurred_at)")

	var shippingActionID uuid.UUID
	err = env.db.QueryRow(ctx, `
		SELECT DISTINCT action_id FROM wms_ops.shippings
		WHERE product_id = ANY($1::uuid[]) AND action_id IS NOT NULL`, itemIDs).Scan(&shippingActionID)
	require.NoError(t, err)
	require.Equal(t, shipActionID, shippingActionID, "shippings.action_id must reference the signed Ship part")

	var binID uuid.UUID
	err = env.db.QueryRow(ctx, `
		SELECT DISTINCT bin_id FROM wms_inventory.products
		WHERE product_id = ANY($1::uuid[]) AND bin_id IS NOT NULL`, itemIDs).Scan(&binID)
	require.NoError(t, err)
	require.Equal(t, shipBinID, binID, "products.bin_id must still hold the shipping buffer bin after SHIPPED")

	orderRows, err := env.db.Query(ctx, `
		SELECT DISTINCT order_id FROM wms_inventory.products
		WHERE product_id = ANY($1::uuid[]) AND order_id IS NOT NULL`, itemIDs)
	require.NoError(t, err)
	var orderIDs []uuid.UUID
	for orderRows.Next() {
		var id uuid.UUID
		require.NoError(t, orderRows.Scan(&id))
		orderIDs = append(orderIDs, id)
	}
	require.NoError(t, orderRows.Err())
	orderRows.Close()
	require.ElementsMatch(t, []uuid.UUID{order1, order2}, orderIDs, "orderIds of the action = distinct orders of its items")

	wantDetailsHash := softShipDetailsHash(salt, shippingDispatchID, binID, orderIDs, shippingOperatorID, uint64(occurredAt.UnixMilli()))
	require.Equal(t, wantDetailsHash, draftDetailsHash,
		"details_hash must be reproducible from shippings.dispatch_id/operator_id/occurred_at, products.bin_id/order_id and the action salt")

	// ── Chain: the action commits, every product is Shipped(4) ──
	waitForOnchainCommitted(t, ctx, env, shipActionID, "shipping")
	operationStatus := waitForOperationState(t, ctx, env, op.token, draft.OperationID, "APPLIED", nil)
	require.Len(t, operationStatus.Parts, 1)
	require.Equal(t, part.ActionID, operationStatus.Parts[0].ActionID)
	require.Nil(t, operationStatus.Parts[0].RejectReason)

	for _, pid := range run.productIDs {
		require.Equal(t, statusShipped, callItemStatus(t, ctx, env, pid),
			"product on-chain status should be Shipped(4) after the signed ship")
	}

	// ── Receipt logs: ActionApplied + ItemMoved of the WMS contract ──
	var txHash string
	err = env.db.QueryRow(ctx, `SELECT tx_hash FROM public.onchain_events WHERE event_id = $1`, shipActionID).Scan(&txHash)
	require.NoError(t, err)
	require.NotEmpty(t, txHash)

	client, err := ethclient.DialContext(ctx, env.rpcURL)
	require.NoError(t, err)
	defer client.Close()
	receipt, err := client.TransactionReceipt(ctx, common.HexToHash(txHash))
	require.NoError(t, err)

	actionAppliedTopic := crypto.Keccak256Hash([]byte("ActionApplied(bytes16,bytes32,bytes16,uint8,bytes32,uint256)"))
	itemMovedTopic := crypto.Keccak256Hash([]byte("ItemMoved(uint256,bytes16,uint8)"))
	var actionIDTopic common.Hash
	copy(actionIDTopic[:16], shipActionID[:])

	appliedFound := false
	movedByItem := map[uuid.UUID]bool{}
	for _, lg := range receipt.Logs {
		if lg.Address != common.HexToAddress(env.contractAddr) {
			continue
		}
		switch {
		case len(lg.Topics) == 4 && lg.Topics[0] == actionAppliedTopic:
			appliedFound = true
			require.Equal(t, actionIDTopic, lg.Topics[1], "ActionApplied topic1 = actionId")
			require.Equal(t, common.BytesToHash(op.keyID[:]), lg.Topics[2], "ActionApplied topic2 = signer keyId")
			data := lg.Data
			require.GreaterOrEqual(t, len(data), 96)
			require.Equal(t, byte(4), data[31], "actionType Ship(4)")
			require.Equal(t, draftDetailsHash[:], data[32:64], "ActionApplied data detailsHash")
			require.Equal(t, int64(len(itemIDs)), bigIntFromWord(data[64:96]).Int64(), "ActionApplied itemCount")
		case len(lg.Topics) == 3 && lg.Topics[0] == itemMovedTopic:
			require.Equal(t, actionIDTopic, lg.Topics[2], "ItemMoved topic2 = actionId")
			require.Equal(t, byte(4), lg.Data[31], "ItemMoved status Shipped(4)")
			itemID := new(big.Int).SetBytes(lg.Topics[1][:])
			found := false
			for _, pid := range run.productIDs {
				if uuidToUint256(pid.String()).Cmp(itemID) == 0 {
					found = true
					movedByItem[pid] = true
				}
			}
			require.True(t, found, "ItemMoved for an unknown item %s", itemID.Text(16))
		}
	}
	require.True(t, appliedFound, "receipt has no ActionApplied log")
	require.Len(t, movedByItem, len(itemIDs), "receipt must carry one ItemMoved per product")

	// ── Timeline shows the signed action as the shipping step ──
	var timeline traceTimeline
	getJSON(t, env, op.token, "/products/timeline?key="+run.productIDs[0].String(), &timeline)
	shippingStep := false
	for _, st := range timeline.Steps {
		if st.Stage == "shipping" && st.EventID == part.ActionID {
			shippingStep = true
		}
	}
	require.True(t, shippingStep, "timeline must show the signed action as the shipping step (event_id = action_id)")
}

// ── TestSignedShip_PickingNotConfirmed ───────────────────────────────────────

// TestSignedShip_PickingNotConfirmed proves the strict gate (#15): right after
// the signed unload, with the Pick action's chain rows removed so nothing is
// COMMITTED, the ship draft is refused with 409 PICKING_NOT_CONFIRMED and
// nothing is written — no Ship action (draft or submitted) exists for the
// products and they stay READY_TO_SHIP.
func TestSignedShip_PickingNotConfirmed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	op := newSignedOperator(t, ctx, env)
	fx := newMultiProductFixture(t, ctx, env, "SHOP-7", 2, 2)

	var allocated allocateData
	postJSON(t, env, op.token, "/assembly/allocate", map[string]string{
		"destination_id": fx.DestinationID.String(),
	}, &allocated)
	requireOrderStatus(t, ctx, env, fx.OrderID, "ALLOCATED")

	orderProductIDs := productIDsForOrder(t, ctx, env, fx.OrderID)
	require.Len(t, orderProductIDs, 2)
	for _, pid := range orderProductIDs {
		postJSON[pickData](t, env, op.token, "/assembly/pick", map[string]string{
			"product_id": pid,
		}, nil)
	}
	placed := unloadToShippingBufferSigned(t, env, op, fx.ShippingBinID.String())
	require.Equal(t, 2, placed.ProductsPlaced)
	requireOrderStatus(t, ctx, env, fx.OrderID, "ASSEMBLED")

	// The test owns its products: remove the Pick action's chain rows (outbox +
	// onchain_events) so picking is not COMMITTED for any of them, and nothing
	// can commit it while the test runs.
	products := parseProductIDs(t, orderProductIDs)
	for _, pid := range products {
		pickActionID := eventIDForAggregate(t, ctx, env, pid, "picking")
		mustExec(t, ctx, env, `DELETE FROM public.onchain_events WHERE event_id = $1`, pickActionID)
		mustExec(t, ctx, env, `DELETE FROM public.outbox_events WHERE event_id = $1`, pickActionID)
	}

	// The signed ship draft requires the dispatch at the gate: scan the driver
	// first (SCHEDULED → AT_GATE), so the picking gate is what refuses below.
	var driver scanDriverData
	postJSON(t, env, op.token, "/shipping/scan-driver", map[string]string{
		"dispatch_code": fx.DispatchCode,
	}, &driver)
	require.Equal(t, fx.DispatchID, driver.DispatchID)

	status, code := draftShipRaw(t, env, op.token, fx.ShippingBinID.String(), fx.DispatchID.String(), orderProductIDs)
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "PICKING_NOT_CONFIRMED", code)

	// Nothing was written: no Ship action (draft or submitted) exists for the
	// products and they stay READY_TO_SHIP in the buffer.
	var shipActions int
	err := env.db.QueryRow(ctx, `
		SELECT count(*) FROM public.warehouse_actions
		WHERE action_type = 4 AND item_ids && $1::uuid[]`, products).Scan(&shipActions)
	require.NoError(t, err)
	require.Zero(t, shipActions, "a refused ship draft must not create any Ship action rows")
	for _, pid := range products {
		requireProductStatus(t, ctx, env, pid, "READY_TO_SHIP")
	}
	requireOrderStatus(t, ctx, env, fx.OrderID, "ASSEMBLED")
}

// ── TestSignedShip_PartialThenRest ───────────────────────────────────────────

// TestSignedShip_PartialThenRest spot-ships 1 of 2 fixture products, then the
// rest: the order flips to PARTIALLY_SHIPPED and the dispatch stays AT_GATE
// after the first signed ship; the second one completes the order and departs
// the dispatch. DB-contract only (markPickingConfirmed fixtures, like
// TestPartialShipment_MofN).
func TestSignedShip_PartialThenRest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	op := newSignedOperator(t, ctx, env)
	fx := newMultiProductFixture(t, ctx, env, "SHOP-7", 2, 2)

	var allocated allocateData
	postJSON(t, env, op.token, "/assembly/allocate", map[string]string{
		"destination_id": fx.DestinationID.String(),
	}, &allocated)
	requireOrderStatus(t, ctx, env, fx.OrderID, "ALLOCATED")

	orderProductIDs := productIDsForOrder(t, ctx, env, fx.OrderID)
	require.Len(t, orderProductIDs, 2)
	for _, pid := range orderProductIDs {
		postJSON[pickData](t, env, op.token, "/assembly/pick", map[string]string{
			"product_id": pid,
		}, nil)
	}
	placed := unloadToShippingBufferSigned(t, env, op, fx.ShippingBinID.String())
	require.Equal(t, 2, placed.ProductsPlaced)
	requireOrderStatus(t, ctx, env, fx.OrderID, "ASSEMBLED")

	// Fixture shortcut: the products have no real chain history in this test.
	markPickingConfirmed(t, ctx, env, parseProductIDs(t, orderProductIDs))

	postJSON[map[string]any](t, env, op.token, "/shipping/scan-buffer", map[string]string{
		"buffer_bin_id": fx.ShippingBinID.String(),
	}, nil)
	var driver scanDriverData
	postJSON(t, env, op.token, "/shipping/scan-driver", map[string]string{
		"dispatch_code": fx.DispatchCode,
	}, &driver)
	require.Equal(t, "AT_GATE", driver.Status)

	first := parseProductIDs(t, orderProductIDs[:1])
	rest := parseProductIDs(t, orderProductIDs[1:])

	// 1) Ship the first product: order PARTIALLY_SHIPPED, dispatch AT_GATE.
	firstShip, _ := shipSigned(t, env, op, fx.ShippingBinID.String(), fx.DispatchID.String(), first)
	require.Equal(t, 1, firstShip.ProductsShipped)
	require.Equal(t, 1, firstShip.OutboxEventsCreated, "one signed part per ship")
	require.Equal(t, 0, firstShip.OrdersCompleted, "order must not complete while products remain")
	require.Equal(t, 1, firstShip.OrdersPartiallyShipped, "order flips to PARTIALLY_SHIPPED after a partial ship (#48)")
	require.False(t, firstShip.DispatchDeparted, "dispatch must not depart while the buffer is non-empty")
	require.Equal(t, 1, firstShip.BufferRemaining)
	requireOrderStatus(t, ctx, env, fx.OrderID, "PARTIALLY_SHIPPED")
	requireDispatchStatus(t, ctx, env, fx.DispatchID, "AT_GATE")
	requireProductStatus(t, ctx, env, first[0], "SHIPPED")
	requireProductStatus(t, ctx, env, rest[0], "READY_TO_SHIP")

	// 2) Ship the rest: the order completes and the dispatch departs.
	finalShip, _ := shipSigned(t, env, op, fx.ShippingBinID.String(), fx.DispatchID.String(), rest)
	require.Equal(t, 1, finalShip.ProductsShipped)
	require.Equal(t, 1, finalShip.OutboxEventsCreated)
	require.Equal(t, 1, finalShip.OrdersCompleted, "order completes once its last product ships")
	require.True(t, finalShip.DispatchDeparted)
	require.Equal(t, 0, finalShip.BufferRemaining)
	requireOrderStatus(t, ctx, env, fx.OrderID, "SHIPPED")
	requireDispatchStatus(t, ctx, env, fx.DispatchID, "DEPARTED")
	requireProductStatus(t, ctx, env, rest[0], "SHIPPED")
}
