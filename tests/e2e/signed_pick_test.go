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

// TestSignedPick_EndToEnd drives the signed Pick end to end: two orders picked
// into one operator basket (products delivered by the signed receive + signed
// putaway), draft (challenge checked against the independent keccak) → operator
// passkey signatures → submit → outbox → adapter → SignedMappingWMS, and proves
// the on-chain ActionApplied/ItemMoved events, the detailsHash recomputed from
// the domain tables, the assembly_tasks link and the product timeline.
func TestSignedPick_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	op := newSignedOperator(t, ctx, env)

	// Fresh per-test SKU: the signed allocate is FIFO over ALL STORED products of
	// the SKU, so a shared SKU would hand this test other tests' leftovers.
	skuID, barcode := newFreshOutboundSKU(t, ctx, env)

	// The destination and its shipping buffer bin (one per destination).
	var destinationID, bufferBinID uuid.UUID
	err := env.db.QueryRow(ctx, `
		SELECT d.destination_id, shb.bin_id
		FROM wms_inventory.warehouses w
		JOIN wms_inventory.destinations d ON d.warehouse_id = w.warehouse_id AND d.code = $1
		JOIN wms_inventory.bins shb ON shb.warehouse_id = w.warehouse_id
			AND shb.destination_id = d.destination_id AND shb.section = 'SHIPPING_BUFFER'
		WHERE w.name = $2`, destinationCode, warehouseName).Scan(&destinationID, &bufferBinID)
	require.NoError(t, err, "resolve destination and shipping buffer bin")

	// ── Signed receive of two products of the fresh per-test SKU ──
	sc := setupReceivingScaffoldWithSKU(t, ctx, env, 2, skuID, barcode)
	run := receiveAndCloseSigned(t, ctx, env, op, sc, 2)
	require.Len(t, run.productIDs, 2)
	for _, pid := range run.productIDs {
		e := eventIDForAggregate(t, ctx, env, pid, "receiving")
		waitForOnchainCommitted(t, ctx, env, e, "receiving")
	}

	// ── Signed putaway of both products into the storage bin ──
	productIDStrings := uuidStrings(run.productIDs)
	placedPutaway := placeToStorageBinSigned(t, env, op, run.productIDs, sc.StorageBinID)
	require.Equal(t, 2, placedPutaway.ProductsPlaced)
	for _, pid := range run.productIDs {
		requireProductStatus(t, ctx, env, pid, "STORED")
		e := eventIDForAggregate(t, ctx, env, pid, "putaway")
		waitForOnchainCommitted(t, ctx, env, e, "putaway")
		require.Equal(t, statusPutAway, callItemStatus(t, ctx, env, pid),
			"product should be PutAway(2) on-chain after the signed putaway")
	}

	// ── Two independent NEW orders for the destination, one line of qty 1 each ──
	order1No := "ORD-SP1-" + shortSuffix()
	order2No := "ORD-SP2-" + shortSuffix()
	for _, orderNo := range []string{order1No, order2No} {
		mustExec(t, ctx, env, `
			INSERT INTO wms_inventory.orders(order_id,external_order_no,customer_id,warehouse_id,destination_id,status,created_at,updated_at)
			SELECT gen_random_uuid(),$1,u.user_id,w.warehouse_id,d.destination_id,'NEW'::wms_inventory.order_status,now(),now()
			FROM public.users u
			JOIN wms_inventory.warehouses w ON w.name=$2
			JOIN wms_inventory.destinations d ON d.code=$3 AND d.warehouse_id=w.warehouse_id
			WHERE u.username='customer'`, orderNo, warehouseName, destinationCode)
		mustExec(t, ctx, env, `
			INSERT INTO wms_inventory.order_lines(order_id,sku_id,qty)
			SELECT o.order_id,$2,1
			FROM wms_inventory.orders o
			WHERE o.external_order_no=$1`, orderNo, sc.SKUID)
	}
	resolveOrder := func(orderNo string) uuid.UUID {
		var id uuid.UUID
		err := env.db.QueryRow(ctx, `SELECT order_id FROM wms_inventory.orders WHERE external_order_no=$1`, orderNo).Scan(&id)
		require.NoError(t, err, "resolve order %s", orderNo)
		return id
	}
	order1, order2 := resolveOrder(order1No), resolveOrder(order2No)

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

	// ── Pick all products into the one signed operator's basket ──
	allProducts := append(order1Products, order2Products...)
	for i, pid := range allProducts {
		var picked pickData
		postJSON(t, env, op.token, "/assembly/pick", map[string]string{"product_id": pid}, &picked)
		require.Equal(t, i+1, picked.CartSize)
		parsed, err := uuid.Parse(pid)
		require.NoError(t, err)
		requireProductStatus(t, ctx, env, parsed, "ASSEMBLED")
		requireTaskStatus(t, ctx, env, parsed, "DONE")
	}

	// #14: the pick calls must not write legacy picking outbox rows — the picking
	// event is the signed unload action submitted below.
	var legacyPickingRows int
	err = env.db.QueryRow(ctx, `
		SELECT count(*) FROM public.outbox_events
		WHERE aggregate_id = ANY($1::uuid[]) AND aggregate_type = 'picking'`, productIDStrings).Scan(&legacyPickingRows)
	require.NoError(t, err)
	require.Zero(t, legacyPickingRows, "pick must not write legacy picking outbox rows")

	// ── Draft the signed unload into the destination's shipping buffer ──
	draft := draftUnloadToShippingBuffer(t, env, op, bufferBinID.String())
	require.Equal(t, 3, draft.ActionType, "the unload draft is a signed Pick (type 3)")
	require.Len(t, draft.Parts, 1, "two products draft into exactly one part")
	part := draft.Parts[0]
	actionID, err := uuid.Parse(part.ActionID)
	require.NoError(t, err)

	// Cleanup: mirror the full-chain fixture cleanup for the rows this test creates
	// (runs LIFO before the receiving scaffold cleanup, preserving FK order).
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		cpSel := `(SELECT cargoplace_id FROM wms_inventory.cargoplaces WHERE cargoplace_code=$1)`
		stmts := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM public.onchain_events WHERE event_id = ANY($1::uuid[])
			   OR event_id IN (SELECT event_id FROM public.outbox_events WHERE aggregate_id = ANY($2::uuid[]))`, []any{[]uuid.UUID{actionID}, productIDStrings}},
			{`DELETE FROM public.outbox_events WHERE event_id = ANY($1::uuid[]) OR aggregate_id = ANY($2::uuid[])`, []any{[]uuid.UUID{actionID}, productIDStrings}},
			{`DELETE FROM wms_ops.assembly_tasks WHERE product_id = ANY($1::uuid[])`, []any{productIDStrings}},
			{`DELETE FROM wms_ops.putaways WHERE product_id = ANY($1::uuid[])`, []any{productIDStrings}},
			{`DELETE FROM wms_ops.receiving_table WHERE cargoplace_id IN ` + cpSel, []any{sc.CargoplaceCode}},
			{`DELETE FROM wms_ops.receiving_gate WHERE cargoplace_id IN ` + cpSel, []any{sc.CargoplaceCode}},
			{`DELETE FROM wms_inventory.products WHERE product_id = ANY($1::uuid[])`, []any{productIDStrings}},
			{`DELETE FROM wms_inventory.boxes WHERE cargoplace_id IN ` + cpSel, []any{sc.CargoplaceCode}},
			{`DELETE FROM wms_inventory.order_lines WHERE order_id IN (SELECT order_id FROM wms_inventory.orders WHERE external_order_no = ANY($1::text[]))`, []any{[]string{order1No, order2No}}},
			{`DELETE FROM wms_inventory.orders WHERE external_order_no = ANY($1::text[])`, []any{[]string{order1No, order2No}}},
		}
		for _, stmt := range stmts {
			if _, err := env.db.Exec(cleanupCtx, stmt.sql, stmt.args...); err != nil {
				t.Logf("signed pick cleanup (%s): %v", stmt.sql, err)
			}
		}
	})

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
	require.ElementsMatch(t, run.productIDs, itemIDs, "the draft signs exactly the picked products")

	detailsHashBytes, err := hex.DecodeString(strings.TrimPrefix(part.DetailsHash, "0x"))
	require.NoError(t, err)
	var draftDetailsHash [32]byte
	copy(draftDetailsHash[:], detailsHashBytes)

	challengeRaw, err := decodeBase64URL(part.Challenge)
	require.NoError(t, err)
	var draftChallenge [32]byte
	copy(draftChallenge[:], challengeRaw)
	wantChallenge := softChallenge(actionID, 3, softItemsHash(itemIDs), draftDetailsHash)
	require.Equal(t, wantChallenge[:], draftChallenge[:], "draft challenge is not the ADR-0001 challenge")

	// ── Submit: both orders become ASSEMBLED ──
	placed := submitUnloadToShippingBuffer(t, env, op.token, draft.OperationID, signDraft(t, op, draft))
	require.Equal(t, bufferBinID.String(), placed.BufferBinID)
	require.Equal(t, 2, placed.ProductsPlaced)
	require.Equal(t, 2, placed.OrdersAssembled, "both fully-picked orders become ASSEMBLED")
	require.Equal(t, draft.OperationID, placed.OperationID)
	for _, orderID := range []uuid.UUID{order1, order2} {
		requireOrderStatus(t, ctx, env, orderID, "ASSEMBLED")
	}
	for _, pid := range run.productIDs {
		requireProductStatus(t, ctx, env, pid, "READY_TO_SHIP")
	}

	// ── detailsHash recomputed from the domain tables the action changed ──
	var saltBytes []byte
	err = env.db.QueryRow(ctx, `SELECT salt FROM public.warehouse_actions WHERE action_id = $1`, actionID).Scan(&saltBytes)
	require.NoError(t, err)
	var salt [32]byte
	copy(salt[:], saltBytes)

	var binID uuid.UUID
	err = env.db.QueryRow(ctx, `
		SELECT DISTINCT bin_id FROM wms_inventory.products
		WHERE product_id = ANY($1::uuid[]) AND bin_id IS NOT NULL`, itemIDs).Scan(&binID)
	require.NoError(t, err)
	require.Equal(t, bufferBinID, binID, "products.bin_id must hold the shipping buffer bin")

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

	var operatorID uuid.UUID
	var unloadedAt time.Time
	err = env.db.QueryRow(ctx, `
		SELECT operator_id, unloaded_at FROM wms_ops.assembly_tasks
		WHERE product_id = ANY($1::uuid[]) AND unloaded_at IS NOT NULL LIMIT 1`, itemIDs).Scan(&operatorID, &unloadedAt)
	require.NoError(t, err)
	require.Equal(t, op.userID, operatorID)

	wantDetailsHash := softPickDetailsHash(salt, binID, orderIDs, operatorID, uint64(unloadedAt.UnixMilli()))
	require.Equal(t, wantDetailsHash, draftDetailsHash,
		"details_hash must be reproducible from products.bin_id/order_id, assembly_tasks.operator_id/unloaded_at and the action salt")

	// Every DONE task of the basket carries the action id and the unload time.
	taskRows, err := env.db.Query(ctx, `
		SELECT product_id, action_id, unloaded_at, operator_id, destination_id
		FROM wms_ops.assembly_tasks
		WHERE product_id = ANY($1::uuid[]) AND status = 'DONE'`, itemIDs)
	require.NoError(t, err)
	tasksSeen := 0
	for taskRows.Next() {
		var pid, taskActionID, taskOperatorID, taskDestinationID uuid.UUID
		var taskUnloadedAt time.Time
		require.NoError(t, taskRows.Scan(&pid, &taskActionID, &taskUnloadedAt, &taskOperatorID, &taskDestinationID))
		tasksSeen++
		require.Equal(t, actionID, taskActionID, "assembly_tasks.action_id must reference the signed Pick part")
		require.Equal(t, unloadedAt, taskUnloadedAt)
		require.Equal(t, op.userID, taskOperatorID)
		require.Equal(t, destinationID, taskDestinationID)
	}
	require.NoError(t, taskRows.Err())
	taskRows.Close()
	require.Equal(t, 2, tasksSeen)

	// ── Chain: the action commits, every product is Picked(3) ──
	waitForOnchainCommitted(t, ctx, env, actionID, "picking")
	operationStatus := waitForOperationState(t, ctx, env, op.token, draft.OperationID, "APPLIED", nil)
	require.Len(t, operationStatus.Parts, 1)
	require.Equal(t, part.ActionID, operationStatus.Parts[0].ActionID)
	require.Nil(t, operationStatus.Parts[0].RejectReason)

	for i, pid := range run.productIDs {
		require.Equalf(t, statusPicked, callItemStatus(t, ctx, env, pid),
			"product %d on-chain status should be Picked(3) after the signed unload", i)
	}

	// ── Receipt logs: ActionApplied + ItemMoved of the WMS contract ──
	var txHash string
	err = env.db.QueryRow(ctx, `SELECT tx_hash FROM public.onchain_events WHERE event_id = $1`, actionID).Scan(&txHash)
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
	copy(actionIDTopic[:16], actionID[:])

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
			require.Equal(t, byte(3), data[31], "actionType Pick(3)")
			require.Equal(t, draftDetailsHash[:], data[32:64], "ActionApplied data detailsHash")
			require.Equal(t, int64(len(itemIDs)), bigIntFromWord(data[64:96]).Int64(), "ActionApplied itemCount")
		case len(lg.Topics) == 3 && lg.Topics[0] == itemMovedTopic:
			require.Equal(t, actionIDTopic, lg.Topics[2], "ItemMoved topic2 = actionId")
			require.Equal(t, byte(3), lg.Data[31], "ItemMoved status Picked(3)")
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

	// ── Timeline shows the signed action as the picking step ──
	var timeline traceTimeline
	getJSON(t, env, op.token, "/products/timeline?key="+run.productIDs[0].String(), &timeline)
	pickingStep := false
	for _, st := range timeline.Steps {
		if st.Stage == "picking" && st.EventID == part.ActionID {
			pickingStep = true
		}
	}
	require.True(t, pickingStep, "timeline must show the signed action as the picking step (event_id = action_id)")
}

// TestSignedPick_DraftStaleWhenBasketChanged proves the draft-stale guard: a
// pick made between draft and submit changes the signed set, so the submit is
// rejected with 409 DRAFT_STALE and nothing is written.
func TestSignedPick_DraftStaleWhenBasketChanged(t *testing.T) {
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
	require.GreaterOrEqual(t, allocated.AllocatedOrders, 1)

	orderProductIDs := productIDsForOrder(t, ctx, env, fx.OrderID)
	require.Len(t, orderProductIDs, 2)

	// Pick the first product and draft the unload over the one-item basket.
	var picked1 pickData
	postJSON(t, env, op.token, "/assembly/pick", map[string]string{"product_id": orderProductIDs[0]}, &picked1)
	draft := draftUnloadToShippingBuffer(t, env, op, fx.ShippingBinID.String())
	require.Len(t, draft.Parts, 1)
	require.Len(t, draft.Parts[0].ItemIDs, 1)
	actionID, err := uuid.Parse(draft.Parts[0].ActionID)
	require.NoError(t, err)

	// Pick a SECOND product: the basket no longer matches the signed set.
	var picked2 pickData
	postJSON(t, env, op.token, "/assembly/pick", map[string]string{"product_id": orderProductIDs[1]}, &picked2)

	status, code := submitUnloadToShippingBufferRaw(t, env, op.token, draft.OperationID, signDraft(t, op, draft))
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "DRAFT_STALE", code)

	// Nothing was written: products stay ASSEMBLED, the order stays ALLOCATED,
	// the action stays an unsubmitted draft with no outbox row.
	for _, pid := range orderProductIDs {
		parsed, err := uuid.Parse(pid)
		require.NoError(t, err)
		requireProductStatus(t, ctx, env, parsed, "ASSEMBLED")
	}
	requireOrderStatus(t, ctx, env, fx.OrderID, "ALLOCATED")
	require.Equal(t, 0, outboxCountForEvent(t, ctx, env, actionID))

	var submitted *time.Time
	err = env.db.QueryRow(ctx, `
		SELECT submitted_at FROM public.warehouse_actions WHERE action_id = $1`, actionID).Scan(&submitted)
	require.NoError(t, err)
	require.Nil(t, submitted, "a stale submit must not mark the action submitted")
}
