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

// ── On-chain status constants ────────────────────────────────────────────────
// statusShipped is already declared in outbound_happy_test.go; the remaining
// FSM enum values are used by the tests in this file.
const (
	statusAccepted uint8 = 1
	statusPutAway  uint8 = 2
	statusPicked   uint8 = 3
	// statusShipped = 4  (declared elsewhere)
)

// ── Full-chain fixture ───────────────────────────────────────────────────────

// fullChainFixture holds the resolved ids for one per-run full-chain dataset
// (receiving + putaway + outbound order/dispatch), with all products driven
// through the real receiving and putaway flows and verified on-chain at each
// stage.
type fullChainFixture struct {
	OrderID       uuid.UUID
	OrderNo       string
	DispatchID    uuid.UUID
	DispatchCode  string
	DestinationID uuid.UUID
	ShippingBinID uuid.UUID
	StorageBinID  uuid.UUID
	ReceivingBinID uuid.UUID
	SKUID         uuid.UUID
	ProductIDs    []uuid.UUID
	// Per-product event IDs for chain ordering assertions; indices match ProductIDs.
	ReceivingEventIDs []uuid.UUID
	PutawayEventIDs   []uuid.UUID
}

// receivingScaffold holds the minimal inbound rows required to drive N products
// through the receiving table flow.
type receivingScaffold struct {
	CargoplaceID   uuid.UUID
	CargoplaceCode string
	TTNCode        string
	SKUID          uuid.UUID
	ReceivingBinID uuid.UUID
	StorageBinID   uuid.UUID
}

// setupReceivingScaffold creates a fresh inbound shipment (GATE_IN_PROGRESS),
// cargoplace (RECEIVED_AT_GATE), and expected_cargoplace_skus row using the
// seeded outbound SKU. It resolves bin IDs and registers cleanup.
func setupReceivingScaffold(t *testing.T, ctx context.Context, env *env, productCount int) receivingScaffold {
	t.Helper()

	suf := shortSuffix()
	ttnCode := "TTN-FC-" + suf
	cpCode := "CP-FC-" + suf

	// Inbound shipment in GATE_IN_PROGRESS.
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.inbound_shipments(shipment_id,warehouse_id,ttn_code,status,created_at,updated_at)
		SELECT gen_random_uuid(),w.warehouse_id,$1,'GATE_IN_PROGRESS',now(),now()
		FROM wms_inventory.warehouses w WHERE w.name=$2`, ttnCode, warehouseName)

	// Cargoplace at RECEIVED_AT_GATE.
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.cargoplaces(cargoplace_id,shipment_id,cargoplace_code,status,received_at_gate_at,created_at,updated_at)
		SELECT gen_random_uuid(),sh.shipment_id,$1,'RECEIVED_AT_GATE',now(),now(),now()
		FROM wms_inventory.inbound_shipments sh WHERE sh.ttn_code=$2`, cpCode, ttnCode)

	// Expected units of the seeded outbound SKU in this cargoplace.
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.expected_cargoplace_skus(cargoplace_id,sku_id,expected_qty)
		SELECT c.cargoplace_id,s.sku_id,$3
		FROM wms_inventory.cargoplaces c
		JOIN wms_inventory.skus s ON s.name=$2
		WHERE c.cargoplace_code=$1`, cpCode, outboundSeedSKUName, productCount)

	// Resolve ids.
	var sc receivingScaffold
	sc.TTNCode = ttnCode
	sc.CargoplaceCode = cpCode
	err := env.db.QueryRow(ctx, `
		SELECT c.cargoplace_id, s.sku_id, rb.bin_id, sb.bin_id
		FROM wms_inventory.cargoplaces c
		JOIN wms_inventory.inbound_shipments sh ON sh.shipment_id = c.shipment_id
		JOIN wms_inventory.warehouses w ON w.warehouse_id = sh.warehouse_id
		JOIN wms_inventory.skus s ON s.name = $2
		JOIN wms_inventory.bins rb ON rb.warehouse_id = w.warehouse_id AND rb.code = 'BUFFER-01'
		JOIN wms_inventory.bins sb ON sb.warehouse_id = w.warehouse_id AND sb.code = 'A-01-01'
		WHERE c.cargoplace_code = $1 AND w.name = $3`,
		cpCode, outboundSeedSKUName, warehouseName,
	).Scan(&sc.CargoplaceID, &sc.SKUID, &sc.ReceivingBinID, &sc.StorageBinID)
	require.NoError(t, err, "resolve receiving scaffold ids")

	// Register cleanup for the scaffold rows; product/box/ops rows are cleaned
	// by the caller's own cleanup since they depend on the API flow.
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		cpSel := `(SELECT cargoplace_id FROM wms_inventory.cargoplaces WHERE cargoplace_code=$1)`
		stmts := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM wms_inventory.expected_cargoplace_skus WHERE cargoplace_id IN ` + cpSel, []any{cpCode}},
			{`DELETE FROM wms_inventory.cargoplaces WHERE cargoplace_code=$1`, []any{cpCode}},
			{`DELETE FROM wms_inventory.inbound_shipments WHERE ttn_code=$1`, []any{ttnCode}},
		}
		for _, s := range stmts {
			if _, err := env.db.Exec(cleanupCtx, s.sql, s.args...); err != nil {
				t.Logf("receivingScaffold cleanup (%s): %v", s.sql, err)
			}
		}
	})

	return sc
}

// newFullChainFixture builds a fresh, isolated full-chain dataset and drives
// productCount products through the real receiving and putaway flows, verifying
// on-chain state at each stage. The returned fixture has all products in STORED
// status with PutAway(2) confirmed on-chain.
func newFullChainFixture(t *testing.T, ctx context.Context, env *env, token string, productCount, orderQty int) fullChainFixture {
	t.Helper()

	suf := shortSuffix()
	orderNo := "ORD-FC-" + suf
	dispatchCode := "DSP-FC-" + suf

	// 1. Create the receiving scaffold (shipment, cargoplace, expected SKUs).
	sc := setupReceivingScaffold(t, ctx, env, productCount)

	// 2. Create order + order line + dispatch for the outbound flow.
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.orders(order_id,external_order_no,customer_id,warehouse_id,destination_id,status,created_at,updated_at)
		SELECT gen_random_uuid(),$1,u.user_id,w.warehouse_id,d.destination_id,'NEW'::wms_inventory.order_status,now(),now()
		FROM public.users u
		JOIN wms_inventory.warehouses w ON w.name=$2
		JOIN wms_inventory.destinations d ON d.code=$3 AND d.warehouse_id=w.warehouse_id
		WHERE u.username='customer'`, orderNo, warehouseName, destinationCode)

	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.order_lines(order_id,sku_id,qty)
		SELECT o.order_id,s.sku_id,$3
		FROM wms_inventory.orders o
		JOIN wms_inventory.skus s ON s.name=$2
		WHERE o.external_order_no=$1`, orderNo, outboundSeedSKUName, orderQty)

	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.outbound_dispatches(dispatch_id,dispatch_code,warehouse_id,destination_id,vehicle_number,driver_name,driver_phone,status,scheduled_at,created_at,updated_at)
		SELECT gen_random_uuid(),$1,w.warehouse_id,d.destination_id,'E2EFC','E2E FC Driver','+70000000002','SCHEDULED'::wms_inventory.outbound_dispatch_status,now()+interval '1 day',now(),now()
		FROM wms_inventory.warehouses w
		JOIN wms_inventory.destinations d ON d.code=$2 AND d.warehouse_id=w.warehouse_id
		WHERE w.name=$3`, dispatchCode, destinationCode, warehouseName)

	// Resolve outbound ids.
	var fx fullChainFixture
	fx.OrderNo = orderNo
	fx.DispatchCode = dispatchCode
	fx.SKUID = sc.SKUID
	fx.StorageBinID = sc.StorageBinID
	fx.ReceivingBinID = sc.ReceivingBinID

	err := env.db.QueryRow(ctx, `
		SELECT d.destination_id, shb.bin_id, o.order_id, od.dispatch_id
		FROM wms_inventory.warehouses w
		JOIN wms_inventory.destinations d ON d.warehouse_id = w.warehouse_id AND d.code = $1
		JOIN wms_inventory.bins shb ON shb.warehouse_id = w.warehouse_id
			AND shb.destination_id = d.destination_id AND shb.section = 'SHIPPING_BUFFER'
		JOIN wms_inventory.orders o ON o.external_order_no = $2
		JOIN wms_inventory.outbound_dispatches od ON od.dispatch_code = $3
		WHERE w.name = $4`,
		destinationCode, orderNo, dispatchCode, warehouseName,
	).Scan(&fx.DestinationID, &fx.ShippingBinID, &fx.OrderID, &fx.DispatchID)
	require.NoError(t, err, "resolve full-chain fixture outbound ids")

	// 3. Drive products through receiving.
	boxBarcode := "BOX-FC-" + suf
	productIDs := make([]uuid.UUID, 0, productCount)

	// Scan the cargoplace at the receiving table.
	postJSON[map[string]any](t, env, token, "/receiving/table/scan-cargoplace", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
	}, nil)

	// Open a single box for all products (same SKU).
	var box scanBoxData
	postJSON(t, env, token, "/receiving/table/scan-box", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
		"box_barcode":   boxBarcode,
	}, &box)
	require.Equal(t, "OPEN", box.Status)

	// Scan SKU once (same barcode for all products).
	var sku scanSKUData
	postJSON(t, env, token, "/receiving/table/scan-sku", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"barcode":       outboundSeedBarcode,
	}, &sku)
	require.Equal(t, sc.SKUID, sku.SKUID)

	// Scan each product's QR code.
	for i := 0; i < productCount; i++ {
		qrCode := fmt.Sprintf("QR-FC-%s-%d", suf, i)

		// Re-scan SKU before each subsequent product (the API may require it
		// per product within the same box).
		if i > 0 {
			var skuAgain scanSKUData
			postJSON(t, env, token, "/receiving/table/scan-sku", map[string]string{
				"cargoplace_id": sc.CargoplaceID.String(),
				"box_id":        box.BoxID.String(),
				"barcode":       outboundSeedBarcode,
			}, &skuAgain)
		}

		var scanned scanQRData
		postJSON(t, env, token, "/receiving/table/scan-qr", map[string]string{
			"cargoplace_id": sc.CargoplaceID.String(),
			"box_id":        box.BoxID.String(),
			"sku_id":        sku.SKUID.String(),
			"qr_code":       qrCode,
		}, &scanned)
		require.Equal(t, "RECEIVED", scanned.Status, "product %d should be RECEIVED", i)
		requireProductStatus(t, ctx, env, scanned.ProductID, "RECEIVED")
		productIDs = append(productIDs, scanned.ProductID)
	}

	// Close box, scan buffer, close cargoplace.
	postJSON[map[string]any](t, env, token, "/receiving/table/close-box", map[string]string{
		"box_id": box.BoxID.String(),
	}, nil)

	postJSON[map[string]any](t, env, token, "/receiving/table/scan-buffer", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
		"buffer_bin_id": sc.ReceivingBinID.String(),
	}, nil)

	var closeCP closeCargoplaceData
	postJSON(t, env, token, "/receiving/table/close-cargoplace", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
	}, &closeCP)
	require.Equal(t, "TABLE_CLOSED", closeCP.Status)
	require.Equal(t, productCount, closeCP.OutboxEventsCreated, "close-cargoplace should create %d outbox events", productCount)

	// 4. Wait for all receiving events on-chain.
	receivingEventIDs := make([]uuid.UUID, productCount)
	for i, pid := range productIDs {
		eid := eventIDForAggregate(t, ctx, env, pid, "receiving")
		receivingEventIDs[i] = eid
		waitForOnchainCommitted(t, ctx, env, eid, "receiving")
		require.Equal(t, statusAccepted, callItemStatus(t, ctx, env, pid),
			"product %d on-chain status should be Accepted(1) after receiving", i)
	}

	// 5. Putaway all products: scan-buffer -> scan-product x N -> scan-storage-bin.
	postJSON[map[string]any](t, env, token, "/putaway/scan-buffer", map[string]string{
		"buffer_bin_id": sc.ReceivingBinID.String(),
	}, nil)

	pidStrings := make([]string, productCount)
	for i, pid := range productIDs {
		pidStrings[i] = pid.String()
		postJSON[map[string]any](t, env, token, "/putaway/scan-product", map[string]string{
			"product_id":    pid.String(),
			"buffer_bin_id": sc.ReceivingBinID.String(),
		}, nil)
	}

	var putaway scanStorageBinData
	postJSON(t, env, token, "/putaway/scan-storage-bin", map[string]any{
		"product_ids":    pidStrings,
		"storage_bin_id": sc.StorageBinID.String(),
	}, &putaway)
	require.Equal(t, productCount, putaway.ProductsPlaced)
	require.Equal(t, productCount, putaway.OutboxEventsCreated)

	for _, pid := range productIDs {
		requireProductStatus(t, ctx, env, pid, "STORED")
	}

	// 6. Wait for all putaway events on-chain.
	putawayEventIDs := make([]uuid.UUID, productCount)
	for i, pid := range productIDs {
		eid := eventIDForAggregate(t, ctx, env, pid, "putaway")
		putawayEventIDs[i] = eid
		waitForOnchainCommitted(t, ctx, env, eid, "putaway")
		require.Equal(t, statusPutAway, callItemStatus(t, ctx, env, pid),
			"product %d on-chain status should be PutAway(2) after putaway", i)
	}

	fx.ProductIDs = productIDs
	fx.ReceivingEventIDs = receivingEventIDs
	fx.PutawayEventIDs = putawayEventIDs

	// Register cleanup for the API-created rows + outbound fixtures. The
	// receivingScaffold cleanup (shipment, cargoplace, expected_skus) is
	// registered separately by setupReceivingScaffold. Cleanups run in LIFO
	// order, so this cleanup (products/ops/outbound) runs before the scaffold
	// cleanup (shipment/cargoplace), preserving FK order.
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		cpSel := `(SELECT cargoplace_id FROM wms_inventory.cargoplaces WHERE cargoplace_code=$1)`
		stmts := []struct {
			sql  string
			args []any
		}{
			// Chain/outbox rows keyed by product_id.
			{`DELETE FROM public.onchain_events WHERE event_id IN (SELECT event_id FROM public.outbox_events WHERE aggregate_id = ANY($1::uuid[]))`, []any{productIDs}},
			{`DELETE FROM public.outbox_events WHERE aggregate_id = ANY($1::uuid[])`, []any{productIDs}},
			// Ops rows.
			{`DELETE FROM wms_ops.shippings WHERE product_id = ANY($1::uuid[])`, []any{productIDs}},
			{`DELETE FROM wms_ops.assembly_tasks WHERE product_id = ANY($1::uuid[])`, []any{productIDs}},
			{`DELETE FROM wms_ops.putaways WHERE product_id = ANY($1::uuid[])`, []any{productIDs}},
			{`DELETE FROM wms_ops.receiving_table WHERE cargoplace_id IN ` + cpSel, []any{sc.CargoplaceCode}},
			{`DELETE FROM wms_ops.receiving_gate WHERE cargoplace_id IN ` + cpSel, []any{sc.CargoplaceCode}},
			// Inventory rows.
			{`DELETE FROM wms_inventory.products WHERE product_id = ANY($1::uuid[])`, []any{productIDs}},
			{`DELETE FROM wms_inventory.boxes WHERE cargoplace_id IN ` + cpSel, []any{sc.CargoplaceCode}},
			// Outbound rows.
			{`DELETE FROM wms_inventory.order_lines WHERE order_id IN (SELECT order_id FROM wms_inventory.orders WHERE external_order_no=$1)`, []any{orderNo}},
			{`DELETE FROM wms_inventory.orders WHERE external_order_no=$1`, []any{orderNo}},
			{`DELETE FROM wms_inventory.outbound_dispatches WHERE dispatch_code=$1`, []any{dispatchCode}},
		}
		for _, s := range stmts {
			if _, err := env.db.Exec(cleanupCtx, s.sql, s.args...); err != nil {
				t.Logf("fullChainFixture cleanup (%s): %v", s.sql, err)
			}
		}
	})

	return fx
}

// ── E2: Full pipeline multi-product test ─────────────────────────────────────

// TestFullChain_MultiProduct (E2) drives 3 products through the FULL pipeline:
// receiving → putaway → allocate → pick → assemble → ship, with on-chain
// verification at every FSM stage. Unlike TestOutboundFlow_EndToEnd (single
// product) and TestMultiProduct_AllocatePickAssemble (pre-STORED, no chain),
// this test proves the entire pipeline works end-to-end for multiple products.
func TestFullChain_MultiProduct(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	const productCount = 3
	token := operatorToken(t, env)
	fx := newFullChainFixture(t, ctx, env, token, productCount, productCount)

	// At this point all products are STORED and PutAway(2) on-chain.
	requireOrderStatus(t, ctx, env, fx.OrderID, "NEW")

	// Build the stage event map for the final FSM ordering assertion.
	stageEventIDs := map[string][]uuid.UUID{
		"receiving": fx.ReceivingEventIDs,
		"putaway":   fx.PutawayEventIDs,
	}

	// ── Allocate ──
	var allocated allocateData
	postJSON(t, env, token, "/assembly/allocate", map[string]string{
		"destination_id": fx.DestinationID.String(),
	}, &allocated)
	require.GreaterOrEqual(t, allocated.AllocatedOrders, 1)
	require.GreaterOrEqual(t, allocated.AllocatedProducts, productCount)
	requireOrderStatus(t, ctx, env, fx.OrderID, "ALLOCATED")

	// Retrieve tasks and verify all fixture products are present.
	var tasks taskListData
	getJSON(t, env, token, "/assembly/tasks?destination_id="+url.QueryEscape(fx.DestinationID.String()), &tasks)
	taskProductSet := map[string]struct{}{}
	for _, task := range tasks.Tasks {
		taskProductSet[task.ProductID] = struct{}{}
	}
	orderProductIDs := productIDsForOrder(t, ctx, env, fx.OrderID)
	require.Len(t, orderProductIDs, productCount)
	for _, pid := range orderProductIDs {
		require.Containsf(t, taskProductSet, pid, "task missing for product %s", pid)
	}

	// ── Pick all products ──
	for i, pid := range orderProductIDs {
		parsed, err := uuid.Parse(pid)
		require.NoError(t, err)
		requireProductStatus(t, ctx, env, parsed, "ALLOCATED")
		requireTaskStatus(t, ctx, env, parsed, "PENDING")

		var picked pickData
		postJSON(t, env, token, "/assembly/pick", map[string]string{
			"product_id": pid,
		}, &picked)
		require.Equal(t, pid, picked.ProductID)
		require.Equal(t, i+1, picked.CartSize)
		requireProductStatus(t, ctx, env, parsed, "ASSEMBLED")
		requireTaskStatus(t, ctx, env, parsed, "DONE")

		pickingEventID := eventIDForAggregate(t, ctx, env, parsed, "picking")
		waitForOnchainCommitted(t, ctx, env, pickingEventID, "picking")
		stageEventIDs["picking"] = append(stageEventIDs["picking"], pickingEventID)

		require.Equal(t, statusPicked, callItemStatus(t, ctx, env, parsed),
			"product %s on-chain status should be Picked(3)", pid)
	}

	// ── Scan shipping buffer → order ASSEMBLED ──
	var placed scanShippingBufferData
	postJSON(t, env, token, "/assembly/scan-shipping-buffer", map[string]string{
		"buffer_bin_id": fx.ShippingBinID.String(),
	}, &placed)
	require.Equal(t, productCount, placed.ProductsPlaced)
	require.GreaterOrEqual(t, placed.OrdersAssembled, 1)
	requireOrderStatus(t, ctx, env, fx.OrderID, "ASSEMBLED")
	for _, pid := range orderProductIDs {
		parsed, err := uuid.Parse(pid)
		require.NoError(t, err)
		requireProductStatus(t, ctx, env, parsed, "READY_TO_SHIP")
	}

	// ── Ship ──
	postJSON[map[string]any](t, env, token, "/shipping/scan-buffer", map[string]string{
		"buffer_bin_id": fx.ShippingBinID.String(),
	}, nil)

	var driver scanDriverData
	postJSON(t, env, token, "/shipping/scan-driver", map[string]string{
		"dispatch_code": fx.DispatchCode,
	}, &driver)
	require.Equal(t, fx.DispatchID, driver.DispatchID)
	require.Equal(t, "AT_GATE", driver.Status)

	var shipped shipData
	postJSON(t, env, token, "/shipping/ship", map[string]any{
		"buffer_bin_id": fx.ShippingBinID.String(),
		"dispatch_id":   fx.DispatchID.String(),
		"product_ids":   orderProductIDs,
	}, &shipped)
	require.Equal(t, productCount, shipped.ProductsShipped)
	require.Equal(t, productCount, shipped.OutboxEventsCreated)
	require.Equal(t, 1, shipped.OrdersCompleted)
	require.True(t, shipped.DispatchDeparted)
	require.Equal(t, 0, shipped.BufferRemaining)
	requireOrderStatus(t, ctx, env, fx.OrderID, "SHIPPED")
	requireDispatchStatus(t, ctx, env, fx.DispatchID, "DEPARTED")

	// Wait for shipping events on-chain and verify final status.
	for _, pid := range orderProductIDs {
		parsed, err := uuid.Parse(pid)
		require.NoError(t, err)
		requireProductStatus(t, ctx, env, parsed, "SHIPPED")

		shippingEventID := eventIDForAggregate(t, ctx, env, parsed, "shipping")
		waitForOnchainCommitted(t, ctx, env, shippingEventID, "shipping")
		stageEventIDs["shipping"] = append(stageEventIDs["shipping"], shippingEventID)

		require.Equal(t, statusShipped, callItemStatus(t, ctx, env, parsed),
			"product %s on-chain status should be Shipped(4)", pid)
	}

	// ── Verify FSM ordering across all 4 stages ──
	requireOnchainFSMOrdering(t, ctx, env, stageEventIDs)
}

// ── E3: Full pipeline partial-ship test ──────────────────────────────────────

// TestFullChain_PartialShip (E3) is identical to E2 through the assembly stage,
// but ships only 2 of 3 products. It verifies that the unshipped product
// remains at Picked(3) on-chain while the shipped products reach Shipped(4).
func TestFullChain_PartialShip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	const productCount = 3
	token := operatorToken(t, env)
	fx := newFullChainFixture(t, ctx, env, token, productCount, productCount)
	requireOrderStatus(t, ctx, env, fx.OrderID, "NEW")

	stageEventIDs := map[string][]uuid.UUID{
		"receiving": fx.ReceivingEventIDs,
		"putaway":   fx.PutawayEventIDs,
	}

	// ── Allocate ──
	var allocated allocateData
	postJSON(t, env, token, "/assembly/allocate", map[string]string{
		"destination_id": fx.DestinationID.String(),
	}, &allocated)
	require.GreaterOrEqual(t, allocated.AllocatedOrders, 1)
	requireOrderStatus(t, ctx, env, fx.OrderID, "ALLOCATED")

	// ── Pick all ──
	orderProductIDs := productIDsForOrder(t, ctx, env, fx.OrderID)
	require.Len(t, orderProductIDs, productCount)

	for i, pid := range orderProductIDs {
		parsed, err := uuid.Parse(pid)
		require.NoError(t, err)

		var picked pickData
		postJSON(t, env, token, "/assembly/pick", map[string]string{
			"product_id": pid,
		}, &picked)
		require.Equal(t, i+1, picked.CartSize)

		pickingEventID := eventIDForAggregate(t, ctx, env, parsed, "picking")
		waitForOnchainCommitted(t, ctx, env, pickingEventID, "picking")
		stageEventIDs["picking"] = append(stageEventIDs["picking"], pickingEventID)

		require.Equal(t, statusPicked, callItemStatus(t, ctx, env, parsed),
			"product %s on-chain status should be Picked(3)", pid)
	}

	// ── Scan shipping buffer → order ASSEMBLED ──
	var placed scanShippingBufferData
	postJSON(t, env, token, "/assembly/scan-shipping-buffer", map[string]string{
		"buffer_bin_id": fx.ShippingBinID.String(),
	}, &placed)
	require.Equal(t, productCount, placed.ProductsPlaced)
	requireOrderStatus(t, ctx, env, fx.OrderID, "ASSEMBLED")

	// ── Partial ship: ship P1 and P2 only ──
	postJSON[map[string]any](t, env, token, "/shipping/scan-buffer", map[string]string{
		"buffer_bin_id": fx.ShippingBinID.String(),
	}, nil)

	var driver scanDriverData
	postJSON(t, env, token, "/shipping/scan-driver", map[string]string{
		"dispatch_code": fx.DispatchCode,
	}, &driver)
	require.Equal(t, "AT_GATE", driver.Status)

	shippedBatch := orderProductIDs[:2]   // P1, P2
	unshippedBatch := orderProductIDs[2:] // P3

	var shipped shipData
	postJSON(t, env, token, "/shipping/ship", map[string]any{
		"buffer_bin_id": fx.ShippingBinID.String(),
		"dispatch_id":   fx.DispatchID.String(),
		"product_ids":   shippedBatch,
	}, &shipped)
	require.Equal(t, 2, shipped.ProductsShipped)
	require.Equal(t, 0, shipped.OrdersCompleted, "order must not complete while products remain")
	require.Equal(t, 1, shipped.OrdersPartiallyShipped, "order flips to PARTIALLY_SHIPPED after a partial ship (#48)")
	require.False(t, shipped.DispatchDeparted, "dispatch must not depart while buffer is non-empty")
	require.Equal(t, 1, shipped.BufferRemaining)

	// #48: partly-shipped order is PARTIALLY_SHIPPED, not ASSEMBLED.
	requireOrderStatus(t, ctx, env, fx.OrderID, "PARTIALLY_SHIPPED")
	requireDispatchStatus(t, ctx, env, fx.DispatchID, "AT_GATE")

	// Wait for shipping events for shipped products → Shipped(4).
	for _, pid := range shippedBatch {
		parsed, err := uuid.Parse(pid)
		require.NoError(t, err)
		requireProductStatus(t, ctx, env, parsed, "SHIPPED")

		shippingEventID := eventIDForAggregate(t, ctx, env, parsed, "shipping")
		waitForOnchainCommitted(t, ctx, env, shippingEventID, "shipping")
		stageEventIDs["shipping"] = append(stageEventIDs["shipping"], shippingEventID)

		require.Equal(t, statusShipped, callItemStatus(t, ctx, env, parsed),
			"shipped product %s on-chain status should be Shipped(4)", pid)
	}

	// Verify unshipped product P3 remains at Picked(3) on-chain.
	for _, pid := range unshippedBatch {
		parsed, err := uuid.Parse(pid)
		require.NoError(t, err)
		requireProductStatus(t, ctx, env, parsed, "READY_TO_SHIP")
		require.Equal(t, statusPicked, callItemStatus(t, ctx, env, parsed),
			"unshipped product %s on-chain status should still be Picked(3)", pid)
	}
}

// ── R2: Multi-box receiving test ─────────────────────────────────────────────

// TestFullChain_MultiBoxReceiving (R2) creates 1 cargoplace with 3 products in
// 3 separate boxes, drives each through the receiving table flow, and verifies
// all 3 receiving events land on-chain as Accepted(1).
func TestFullChain_MultiBoxReceiving(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	const productCount = 3
	token := operatorToken(t, env)
	suf := shortSuffix()

	// Create the scaffold with expected_qty = productCount.
	sc := setupReceivingScaffold(t, ctx, env, productCount)

	// Scan the cargoplace at the table.
	postJSON[map[string]any](t, env, token, "/receiving/table/scan-cargoplace", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
	}, nil)

	// For each product: open a new box, scan SKU, scan QR, close box.
	productIDs := make([]uuid.UUID, 0, productCount)
	for i := 0; i < productCount; i++ {
		boxBarcode := fmt.Sprintf("BOX-R2-%s-%d", suf, i)
		qrCode := fmt.Sprintf("QR-R2-%s-%d", suf, i)

		var box scanBoxData
		postJSON(t, env, token, "/receiving/table/scan-box", map[string]string{
			"cargoplace_id": sc.CargoplaceID.String(),
			"box_barcode":   boxBarcode,
		}, &box)
		require.Equal(t, "OPEN", box.Status, "box %d should be OPEN", i)

		var sku scanSKUData
		postJSON(t, env, token, "/receiving/table/scan-sku", map[string]string{
			"cargoplace_id": sc.CargoplaceID.String(),
			"box_id":        box.BoxID.String(),
			"barcode":       outboundSeedBarcode,
		}, &sku)

		var scanned scanQRData
		postJSON(t, env, token, "/receiving/table/scan-qr", map[string]string{
			"cargoplace_id": sc.CargoplaceID.String(),
			"box_id":        box.BoxID.String(),
			"sku_id":        sku.SKUID.String(),
			"qr_code":       qrCode,
		}, &scanned)
		require.Equal(t, "RECEIVED", scanned.Status, "product %d should be RECEIVED", i)
		productIDs = append(productIDs, scanned.ProductID)

		postJSON[map[string]any](t, env, token, "/receiving/table/close-box", map[string]string{
			"box_id": box.BoxID.String(),
		}, nil)
	}

	// Scan buffer and close cargoplace.
	postJSON[map[string]any](t, env, token, "/receiving/table/scan-buffer", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
		"buffer_bin_id": sc.ReceivingBinID.String(),
	}, nil)

	var closeCP closeCargoplaceData
	postJSON(t, env, token, "/receiving/table/close-cargoplace", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
	}, &closeCP)
	require.Equal(t, "TABLE_CLOSED", closeCP.Status)
	require.Equal(t, productCount, closeCP.OutboxEventsCreated, "should create %d outbox events", productCount)

	// Wait for all receiving events on-chain → Accepted(1).
	for i, pid := range productIDs {
		eid := eventIDForAggregate(t, ctx, env, pid, "receiving")
		waitForOnchainCommitted(t, ctx, env, eid, "receiving")
		require.Equal(t, statusAccepted, callItemStatus(t, ctx, env, pid),
			"product %d on-chain status should be Accepted(1)", i)
	}

	// Register cleanup for API-created rows.
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		cpSel := `(SELECT cargoplace_id FROM wms_inventory.cargoplaces WHERE cargoplace_code=$1)`
		stmts := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM public.onchain_events WHERE event_id IN (SELECT event_id FROM public.outbox_events WHERE aggregate_id = ANY($1::uuid[]))`, []any{productIDs}},
			{`DELETE FROM public.outbox_events WHERE aggregate_id = ANY($1::uuid[])`, []any{productIDs}},
			{`DELETE FROM wms_ops.receiving_table WHERE cargoplace_id IN ` + cpSel, []any{sc.CargoplaceCode}},
			{`DELETE FROM wms_ops.receiving_gate WHERE cargoplace_id IN ` + cpSel, []any{sc.CargoplaceCode}},
			{`DELETE FROM wms_inventory.products WHERE product_id = ANY($1::uuid[])`, []any{productIDs}},
			{`DELETE FROM wms_inventory.boxes WHERE cargoplace_id IN ` + cpSel, []any{sc.CargoplaceCode}},
		}
		for _, s := range stmts {
			if _, err := env.db.Exec(cleanupCtx, s.sql, s.args...); err != nil {
				t.Logf("R2 cleanup (%s): %v", s.sql, err)
			}
		}
	})
}

// ── P2: Batch putaway test ───────────────────────────────────────────────────

// TestFullChain_BatchPutaway (P2) receives 3 products via the full receiving
// flow, then puts away all 3 in a single scan-storage-bin call, and verifies
// all putaway events land on-chain as PutAway(2).
func TestFullChain_BatchPutaway(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	const productCount = 3
	token := operatorToken(t, env)
	suf := shortSuffix()

	// Create the receiving scaffold.
	sc := setupReceivingScaffold(t, ctx, env, productCount)

	// Drive receiving: scan-cargoplace → (scan-box, scan-sku, scan-qr) × N → close-box → scan-buffer → close-cargoplace.
	postJSON[map[string]any](t, env, token, "/receiving/table/scan-cargoplace", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
	}, nil)

	boxBarcode := "BOX-P2-" + suf
	var box scanBoxData
	postJSON(t, env, token, "/receiving/table/scan-box", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
		"box_barcode":   boxBarcode,
	}, &box)

	var sku scanSKUData
	postJSON(t, env, token, "/receiving/table/scan-sku", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"barcode":       outboundSeedBarcode,
	}, &sku)

	productIDs := make([]uuid.UUID, 0, productCount)
	for i := 0; i < productCount; i++ {
		qrCode := fmt.Sprintf("QR-P2-%s-%d", suf, i)

		if i > 0 {
			var skuAgain scanSKUData
			postJSON(t, env, token, "/receiving/table/scan-sku", map[string]string{
				"cargoplace_id": sc.CargoplaceID.String(),
				"box_id":        box.BoxID.String(),
				"barcode":       outboundSeedBarcode,
			}, &skuAgain)
		}

		var scanned scanQRData
		postJSON(t, env, token, "/receiving/table/scan-qr", map[string]string{
			"cargoplace_id": sc.CargoplaceID.String(),
			"box_id":        box.BoxID.String(),
			"sku_id":        sku.SKUID.String(),
			"qr_code":       qrCode,
		}, &scanned)
		require.Equal(t, "RECEIVED", scanned.Status)
		productIDs = append(productIDs, scanned.ProductID)
	}

	postJSON[map[string]any](t, env, token, "/receiving/table/close-box", map[string]string{
		"box_id": box.BoxID.String(),
	}, nil)

	postJSON[map[string]any](t, env, token, "/receiving/table/scan-buffer", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
		"buffer_bin_id": sc.ReceivingBinID.String(),
	}, nil)

	var closeCP closeCargoplaceData
	postJSON(t, env, token, "/receiving/table/close-cargoplace", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
	}, &closeCP)
	require.Equal(t, "TABLE_CLOSED", closeCP.Status)
	require.Equal(t, productCount, closeCP.OutboxEventsCreated)

	// Wait for receiving events on-chain before proceeding to putaway.
	for i, pid := range productIDs {
		eid := eventIDForAggregate(t, ctx, env, pid, "receiving")
		waitForOnchainCommitted(t, ctx, env, eid, "receiving")
		require.Equal(t, statusAccepted, callItemStatus(t, ctx, env, pid),
			"product %d on-chain should be Accepted(1) before putaway", i)
	}

	// Batch putaway: scan-buffer → scan-product × N → scan-storage-bin with all N.
	postJSON[map[string]any](t, env, token, "/putaway/scan-buffer", map[string]string{
		"buffer_bin_id": sc.ReceivingBinID.String(),
	}, nil)

	pidStrings := make([]string, productCount)
	for i, pid := range productIDs {
		pidStrings[i] = pid.String()
		postJSON[map[string]any](t, env, token, "/putaway/scan-product", map[string]string{
			"product_id":    pid.String(),
			"buffer_bin_id": sc.ReceivingBinID.String(),
		}, nil)
	}

	var putaway scanStorageBinData
	postJSON(t, env, token, "/putaway/scan-storage-bin", map[string]any{
		"product_ids":    pidStrings,
		"storage_bin_id": sc.StorageBinID.String(),
	}, &putaway)
	require.Equal(t, productCount, putaway.ProductsPlaced)
	require.Equal(t, productCount, putaway.OutboxEventsCreated)

	for _, pid := range productIDs {
		requireProductStatus(t, ctx, env, pid, "STORED")
	}

	// Wait for all putaway events on-chain → PutAway(2).
	for i, pid := range productIDs {
		eid := eventIDForAggregate(t, ctx, env, pid, "putaway")
		waitForOnchainCommitted(t, ctx, env, eid, "putaway")
		require.Equal(t, statusPutAway, callItemStatus(t, ctx, env, pid),
			"product %d on-chain status should be PutAway(2)", i)
	}

	// Register cleanup.
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		cpSel := `(SELECT cargoplace_id FROM wms_inventory.cargoplaces WHERE cargoplace_code=$1)`
		stmts := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM public.onchain_events WHERE event_id IN (SELECT event_id FROM public.outbox_events WHERE aggregate_id = ANY($1::uuid[]))`, []any{productIDs}},
			{`DELETE FROM public.outbox_events WHERE aggregate_id = ANY($1::uuid[])`, []any{productIDs}},
			{`DELETE FROM wms_ops.putaways WHERE product_id = ANY($1::uuid[])`, []any{productIDs}},
			{`DELETE FROM wms_ops.receiving_table WHERE cargoplace_id IN ` + cpSel, []any{sc.CargoplaceCode}},
			{`DELETE FROM wms_ops.receiving_gate WHERE cargoplace_id IN ` + cpSel, []any{sc.CargoplaceCode}},
			{`DELETE FROM wms_inventory.products WHERE product_id = ANY($1::uuid[])`, []any{productIDs}},
			{`DELETE FROM wms_inventory.boxes WHERE cargoplace_id IN ` + cpSel, []any{sc.CargoplaceCode}},
		}
		for _, s := range stmts {
			if _, err := env.db.Exec(cleanupCtx, s.sql, s.args...); err != nil {
				t.Logf("P2 cleanup (%s): %v", s.sql, err)
			}
		}
	})
}
