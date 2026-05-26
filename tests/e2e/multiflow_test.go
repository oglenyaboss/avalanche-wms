//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestMultiOrder_AllocateAndShip allocates two independent orders for SHOP-7 and
// ships all their products in a single dispatch. This exercises A3 (multi-order
// allocation) and S4 (multi-order dispatch) by proving that:
//   - allocate picks up both NEW orders for the destination,
//   - a single operator cart can accumulate picks from multiple orders,
//   - scan-shipping-buffer flushes both orders into ASSEMBLED,
//   - ship with one dispatch completes both orders and departs the dispatch,
//   - the second fixture's dispatch is left SCHEDULED (never used).
//
// This is a DB-contract test with NO on-chain assertions (pre-STORED products
// skip receiving/putaway, so their on-chain itemStatus is None and the resulting
// picking/shipping events revert — same as TestPartialShipment_MofN).
func TestMultiOrder_AllocateAndShip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	// One operator for both orders: carts are keyed per-operator in-memory,
	// so both orders' picks accumulate in the same cart.
	token := operatorToken(t, env)

	// Two independent fixtures for SHOP-7, each with 1 pre-STORED product and
	// an order line of qty=1. Each fixture creates its own SKU, order, and
	// dispatch, so they are fully isolated.
	fx1 := newMultiProductFixture(t, ctx, env, "SHOP-7", 1, 1)
	fx2 := newMultiProductFixture(t, ctx, env, "SHOP-7", 1, 1)

	requireOrderStatus(t, ctx, env, fx1.OrderID, "NEW")
	requireOrderStatus(t, ctx, env, fx2.OrderID, "NEW")
	requireDispatchStatus(t, ctx, env, fx1.DispatchID, "SCHEDULED")
	requireDispatchStatus(t, ctx, env, fx2.DispatchID, "SCHEDULED")

	// 1. Allocate SHOP-7: both orders should be picked up. Use >= because a
	//    leaked NEW order from a failed prior cleanup could inflate totals.
	var allocated allocateData
	postJSON(t, env, token, "/assembly/allocate", map[string]string{
		"destination_id": fx1.DestinationID.String(),
	}, &allocated)
	require.GreaterOrEqual(t, allocated.AllocatedOrders, 2)
	require.GreaterOrEqual(t, allocated.AllocatedProducts, 2)
	requireOrderStatus(t, ctx, env, fx1.OrderID, "ALLOCATED")
	requireOrderStatus(t, ctx, env, fx2.OrderID, "ALLOCATED")

	// 2. Get tasks for the destination and verify both orders' products appear.
	var tasks taskListData
	getJSON(t, env, token, "/assembly/tasks?destination_id="+url.QueryEscape(fx1.DestinationID.String()), &tasks)
	taskProductSet := map[string]struct{}{}
	for _, task := range tasks.Tasks {
		taskProductSet[task.ProductID] = struct{}{}
	}

	order1Products := productIDsForOrder(t, ctx, env, fx1.OrderID)
	order2Products := productIDsForOrder(t, ctx, env, fx2.OrderID)
	require.Len(t, order1Products, 1)
	require.Len(t, order2Products, 1)
	for _, pid := range order1Products {
		require.Containsf(t, taskProductSet, pid, "task missing for order1 product %s", pid)
	}
	for _, pid := range order2Products {
		require.Containsf(t, taskProductSet, pid, "task missing for order2 product %s", pid)
	}

	// 3. Pick all products from both orders into the single operator cart.
	allProductIDs := append(order1Products, order2Products...)
	for i, pid := range allProductIDs {
		var picked pickData
		postJSON(t, env, token, "/assembly/pick", map[string]string{
			"product_id": pid,
		}, &picked)
		require.Equal(t, pid, picked.ProductID)
		require.Equal(t, i+1, picked.CartSize)
	}

	// 4. Flush the cart into the SHOP-7 shipping buffer; both orders become ASSEMBLED.
	// fx1.ShippingBinID == fx2.ShippingBinID (single bin per destination).
	var placed scanShippingBufferData
	postJSON(t, env, token, "/assembly/scan-shipping-buffer", map[string]string{
		"buffer_bin_id": fx1.ShippingBinID.String(),
	}, &placed)
	require.Equal(t, 2, placed.ProductsPlaced)
	require.GreaterOrEqual(t, placed.OrdersAssembled, 2)
	requireOrderStatus(t, ctx, env, fx1.OrderID, "ASSEMBLED")
	requireOrderStatus(t, ctx, env, fx2.OrderID, "ASSEMBLED")
	for _, pid := range allProductIDs {
		parsed, err := uuid.Parse(pid)
		require.NoError(t, err)
		requireProductStatus(t, ctx, env, parsed, "READY_TO_SHIP")
	}

	// 5. Scan buffer + driver: bring fx1's dispatch to AT_GATE.
	postJSON[map[string]any](t, env, token, "/shipping/scan-buffer", map[string]string{
		"buffer_bin_id": fx1.ShippingBinID.String(),
	}, nil)

	var driver scanDriverData
	postJSON(t, env, token, "/shipping/scan-driver", map[string]string{
		"dispatch_code": fx1.DispatchCode,
	}, &driver)
	require.Equal(t, fx1.DispatchID, driver.DispatchID)
	require.Equal(t, "AT_GATE", driver.Status)
	requireDispatchStatus(t, ctx, env, fx1.DispatchID, "AT_GATE")

	// 6. Ship all products with fx1's dispatch. Both orders complete, dispatch departs.
	var shipped shipData
	postJSON(t, env, token, "/shipping/ship", map[string]any{
		"buffer_bin_id": fx1.ShippingBinID.String(),
		"dispatch_id":   fx1.DispatchID.String(),
		"product_ids":   allProductIDs,
	}, &shipped)
	require.Equal(t, 2, shipped.ProductsShipped)
	require.GreaterOrEqual(t, shipped.OrdersCompleted, 2)
	require.True(t, shipped.DispatchDeparted)
	require.Equal(t, 0, shipped.BufferRemaining)

	// 7. Final state assertions.
	requireOrderStatus(t, ctx, env, fx1.OrderID, "SHIPPED")
	requireOrderStatus(t, ctx, env, fx2.OrderID, "SHIPPED")
	requireDispatchStatus(t, ctx, env, fx1.DispatchID, "DEPARTED")
	// fx2's dispatch was never used; it must stay SCHEDULED.
	requireDispatchStatus(t, ctx, env, fx2.DispatchID, "SCHEDULED")
	for _, pid := range allProductIDs {
		parsed, err := uuid.Parse(pid)
		require.NoError(t, err)
		requireProductStatus(t, ctx, env, parsed, "SHIPPED")
	}
}

// TestMultiCargoplace_Receiving exercises the gate flow for a shipment with 3
// cargoplaces, then processes one cargoplace through the table flow (R3).
//
// Gate phase:
//   - scan-ttn opens the shipment (CREATED -> GATE_IN_PROGRESS)
//   - scan-cargoplace x3 moves all cargoplaces to RECEIVED_AT_GATE
//   - the shipment auto-closes to GATE_CLOSED when received == total
//
// Table phase (CP1 only):
//   - scan-cargoplace -> scan-box -> scan-sku -> scan-qr -> close-box ->
//     scan-buffer -> close-cargoplace
//   - CP1 reaches TABLE_CLOSED with 1 outbox event created
//   - CP2 and CP3 remain at RECEIVED_AT_GATE (not processed at table)
func TestMultiCargoplace_Receiving(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)

	// ── Fixture: CREATED shipment with 3 EXPECTED cargoplaces ────────────
	suf := shortSuffix()
	ttnCode := "TTN-MC-" + suf
	cpCodes := [3]string{
		fmt.Sprintf("CP-MC-%s-0", suf),
		fmt.Sprintf("CP-MC-%s-1", suf),
		fmt.Sprintf("CP-MC-%s-2", suf),
	}
	boxBarcode := "BOX-MC-" + suf
	qrCode := "QR-MC-" + suf

	// Insert shipment.
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.inbound_shipments(shipment_id,warehouse_id,ttn_code,status,created_at,updated_at)
		SELECT gen_random_uuid(),w.warehouse_id,$1,'CREATED',now(),now()
		FROM wms_inventory.warehouses w WHERE w.name=$2`, ttnCode, warehouseName)

	// Resolve shipment ID.
	var shipmentID uuid.UUID
	err := env.db.QueryRow(ctx, `
		SELECT shipment_id FROM wms_inventory.inbound_shipments WHERE ttn_code=$1`, ttnCode).Scan(&shipmentID)
	require.NoError(t, err, "resolve shipment_id for %s", ttnCode)

	// Insert 3 EXPECTED cargoplaces, each with 1 expected unit of the seed SKU.
	cpIDs := [3]uuid.UUID{}
	for i, cpCode := range cpCodes {
		mustExec(t, ctx, env, `
			INSERT INTO wms_inventory.cargoplaces(cargoplace_id,shipment_id,cargoplace_code,status,created_at,updated_at)
			SELECT gen_random_uuid(),sh.shipment_id,$1,'EXPECTED',now(),now()
			FROM wms_inventory.inbound_shipments sh WHERE sh.ttn_code=$2`, cpCode, ttnCode)

		mustExec(t, ctx, env, `
			INSERT INTO wms_inventory.expected_cargoplace_skus(cargoplace_id,sku_id,expected_qty)
			SELECT c.cargoplace_id,s.sku_id,1
			FROM wms_inventory.cargoplaces c
			JOIN wms_inventory.skus s ON s.name=$2
			WHERE c.cargoplace_code=$1`, cpCode, outboundSeedSKUName)

		err := env.db.QueryRow(ctx, `
			SELECT cargoplace_id FROM wms_inventory.cargoplaces WHERE cargoplace_code=$1`, cpCode).Scan(&cpIDs[i])
		require.NoError(t, err, "resolve cargoplace_id for %s", cpCode)
	}

	// Resolve receiving buffer bin for the table flow.
	receivingBinID := resolveBinID(t, ctx, env, "BUFFER-01")

	// Cleanup in FK order. Processing CP1 through the table flow creates
	// products, boxes, receiving_table rows, receiving_gate rows, outbox and
	// onchain events, so all of these must be deleted before parents.
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		cpSel := `(SELECT cargoplace_id FROM wms_inventory.cargoplaces WHERE shipment_id=$1)`
		prodSel := `(SELECT product_id FROM wms_inventory.products WHERE cargoplace_id IN ` + cpSel + `)`
		stmts := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM public.onchain_events WHERE event_id IN (SELECT event_id FROM public.outbox_events WHERE aggregate_id IN ` + prodSel + `)`, []any{shipmentID}},
			{`DELETE FROM public.outbox_events WHERE aggregate_id IN ` + prodSel, []any{shipmentID}},
			{`DELETE FROM wms_ops.receiving_table WHERE cargoplace_id IN ` + cpSel, []any{shipmentID}},
			{`DELETE FROM wms_ops.receiving_gate WHERE cargoplace_id IN ` + cpSel, []any{shipmentID}},
			{`DELETE FROM wms_ops.receiving_gate WHERE shipment_id=$1`, []any{shipmentID}},
			{`DELETE FROM wms_inventory.products WHERE cargoplace_id IN ` + cpSel, []any{shipmentID}},
			{`DELETE FROM wms_inventory.boxes WHERE cargoplace_id IN ` + cpSel, []any{shipmentID}},
			{`DELETE FROM wms_inventory.expected_cargoplace_skus WHERE cargoplace_id IN ` + cpSel, []any{shipmentID}},
			{`DELETE FROM wms_inventory.cargoplaces WHERE shipment_id=$1`, []any{shipmentID}},
			{`DELETE FROM wms_inventory.inbound_shipments WHERE shipment_id=$1`, []any{shipmentID}},
		}
		for _, s := range stmts {
			if _, err := env.db.Exec(cleanupCtx, s.sql, s.args...); err != nil {
				t.Logf("multiCargoplace cleanup (%s): %v", s.sql, err)
			}
		}
	})

	// ── Gate phase ───────────────────────────────────────────────────────

	// scan-ttn: CREATED -> GATE_IN_PROGRESS.
	var ttnResult scanTTNData
	postJSON(t, env, token, "/receiving/gate/scan-ttn", map[string]string{
		"ttn_code": ttnCode,
	}, &ttnResult)
	require.Equal(t, shipmentID, ttnResult.ShipmentID)
	require.Equal(t, "GATE_IN_PROGRESS", ttnResult.Status)
	require.Equal(t, 3, ttnResult.TotalCargoplaces)
	require.Equal(t, 0, ttnResult.ReceivedCargoplaces)

	// scan-cargoplace for each of the 3 cargoplaces, verifying progress.
	for i, cpCode := range cpCodes {
		var cpResult scanGateCargoplaceData
		postJSON(t, env, token, "/receiving/gate/scan-cargoplace", map[string]string{
			"shipment_id":     shipmentID.String(),
			"cargoplace_code": cpCode,
		}, &cpResult)
		require.Equal(t, cpIDs[i], cpResult.CargoplaceID)
		require.Equal(t, "RECEIVED_AT_GATE", cpResult.Status)
		require.Equal(t, 3, cpResult.Progress.Total)
		require.Equal(t, i+1, cpResult.Progress.Received)
		require.Equal(t, 3-(i+1), cpResult.Progress.Remaining)
	}

	// After the 3rd scan-cargoplace, received == total so the shipment may
	// auto-close. Check DB status and only call accept-shipment if still open.
	var shipmentStatus string
	err = env.db.QueryRow(ctx, `
		SELECT status::text FROM wms_inventory.inbound_shipments WHERE shipment_id=$1`,
		shipmentID).Scan(&shipmentStatus)
	require.NoError(t, err)

	if shipmentStatus == "GATE_IN_PROGRESS" {
		var accepted acceptShipmentData
		postJSON(t, env, token, "/receiving/gate/accept-shipment", map[string]string{
			"shipment_id": shipmentID.String(),
		}, &accepted)
		require.Equal(t, "GATE_CLOSED", accepted.Status)
		require.Equal(t, 3, accepted.Summary.Received)
		require.Equal(t, 0, accepted.Summary.NotReceived)
	} else {
		require.Equal(t, "GATE_CLOSED", shipmentStatus, "shipment should be auto-closed after all 3 cargoplaces received")
	}

	// All 3 cargoplaces must be RECEIVED_AT_GATE; shipment must be GATE_CLOSED.
	for i, cpID := range cpIDs {
		var cpStatus string
		err := env.db.QueryRow(ctx, `
			SELECT status::text FROM wms_inventory.cargoplaces WHERE cargoplace_id=$1`, cpID).Scan(&cpStatus)
		require.NoError(t, err)
		require.Equalf(t, "RECEIVED_AT_GATE", cpStatus, "cargoplace %d (%s) should be RECEIVED_AT_GATE", i, cpCodes[i])
	}
	var finalShipStatus string
	err = env.db.QueryRow(ctx, `
		SELECT status::text FROM wms_inventory.inbound_shipments WHERE shipment_id=$1`,
		shipmentID).Scan(&finalShipStatus)
	require.NoError(t, err)
	require.Equal(t, "GATE_CLOSED", finalShipStatus)

	// ── Table phase: process CP1 only ────────────────────────────────────

	// scan-cargoplace at the table: RECEIVED_AT_GATE -> TABLE_IN_PROGRESS.
	postJSON[map[string]any](t, env, token, "/receiving/table/scan-cargoplace", map[string]string{
		"cargoplace_id": cpIDs[0].String(),
	}, nil)

	// scan-box: create a box inside CP1.
	var box scanBoxData
	postJSON(t, env, token, "/receiving/table/scan-box", map[string]string{
		"cargoplace_id": cpIDs[0].String(),
		"box_barcode":   boxBarcode,
	}, &box)
	require.Equal(t, "OPEN", box.Status)

	// scan-sku: identify the SKU by its barcode.
	var sku scanSKUData
	postJSON(t, env, token, "/receiving/table/scan-sku", map[string]string{
		"cargoplace_id": cpIDs[0].String(),
		"box_id":        box.BoxID.String(),
		"barcode":       outboundSeedBarcode,
	}, &sku)

	// scan-qr: create the product inside the box.
	var scannedProduct scanQRData
	postJSON(t, env, token, "/receiving/table/scan-qr", map[string]string{
		"cargoplace_id": cpIDs[0].String(),
		"box_id":        box.BoxID.String(),
		"sku_id":        sku.SKUID.String(),
		"qr_code":       qrCode,
	}, &scannedProduct)
	require.Equal(t, "RECEIVED", scannedProduct.Status)
	requireProductStatus(t, ctx, env, scannedProduct.ProductID, "RECEIVED")

	// close-box -> scan-buffer -> close-cargoplace.
	postJSON[map[string]any](t, env, token, "/receiving/table/close-box", map[string]string{
		"box_id": box.BoxID.String(),
	}, nil)

	postJSON[map[string]any](t, env, token, "/receiving/table/scan-buffer", map[string]string{
		"cargoplace_id": cpIDs[0].String(),
		"buffer_bin_id": receivingBinID.String(),
	}, nil)

	var closeCP closeCargoplaceData
	postJSON(t, env, token, "/receiving/table/close-cargoplace", map[string]string{
		"cargoplace_id": cpIDs[0].String(),
	}, &closeCP)
	require.Equal(t, "TABLE_CLOSED", closeCP.Status)
	require.Equal(t, 1, closeCP.OutboxEventsCreated)

	// Per-product outbox assertion: the product created via scan-qr should have
	// exactly 1 receiving outbox event.
	require.Equal(t, 1, outboxCountForAggregate(t, ctx, env, scannedProduct.ProductID, "receiving"))

	// CP1 is TABLE_CLOSED.
	var cp1Status string
	err = env.db.QueryRow(ctx, `
		SELECT status::text FROM wms_inventory.cargoplaces WHERE cargoplace_id=$1`, cpIDs[0]).Scan(&cp1Status)
	require.NoError(t, err)
	require.Equal(t, "TABLE_CLOSED", cp1Status)

	// CP2 and CP3 remain at RECEIVED_AT_GATE (not processed at table).
	for _, cpID := range cpIDs[1:] {
		var status string
		err := env.db.QueryRow(ctx, `
			SELECT status::text FROM wms_inventory.cargoplaces WHERE cargoplace_id=$1`, cpID).Scan(&status)
		require.NoError(t, err)
		require.Equal(t, "RECEIVED_AT_GATE", status, "unprocessed cargoplace should remain RECEIVED_AT_GATE")
	}
}
