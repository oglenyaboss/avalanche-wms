//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// e2eFixture holds the resolved ids and codes for one outbound-flow run.
type e2eFixture struct {
	CargoplaceID   uuid.UUID
	BoxBarcode     string
	Barcode        string
	QRCode         string
	OrderNo        string
	DispatchCode   string
	SKUID          uuid.UUID
	WarehouseID    int64
	DestinationID  uuid.UUID
	ReceivingBinID uuid.UUID
	StorageBinID   uuid.UUID
	ShippingBinID  uuid.UUID
	OrderID        uuid.UUID
	DispatchID     uuid.UUID
}

// outboundSeedSKU is the dedicated SKU/barcode seeded for outbound e2e flows.
const (
	outboundSeedSKUName = "E2E Seed Outbound SKU"
	outboundSeedBarcode = "4600000099999"
	warehouseName       = "Склад Москва-Север"
	destinationCode     = "SHOP-7"
)

// newOutboundFixture builds a fresh, isolated outbound dataset for a single run.
//
// The dev seed uses ON CONFLICT DO NOTHING, so a second run cannot reset rows a
// previous run mutated. To stay deterministic, every run generates its own
// shipment + cargoplace (RECEIVED_AT_GATE) + expected SKU + order (NEW) +
// order line + dispatch (SCHEDULED), all keyed by a per-run suffix, for SHOP-7
// using the seeded "E2E Seed Outbound SKU" barcode.
//
// The obsolete dedicated seed rows (ORD-2026-E2E-OUTBOUND / DSP-2026-E2E-OUTBOUND)
// were removed from deploy/seed.sql; per-run fixtures fully supersede them, so
// SHOP-7 allocation only ever sees this fixture's freshly inserted NEW order.
func newOutboundFixture(t *testing.T, ctx context.Context, env *env) e2eFixture {
	t.Helper()

	suf := uuid.NewString()[:8]
	fx := e2eFixture{
		BoxBarcode:   "BOX-E2E-" + suf,
		Barcode:      outboundSeedBarcode,
		QRCode:       "QR-E2E-" + suf,
		OrderNo:      "ORD-E2E-" + suf,
		DispatchCode: "DSP-E2E-" + suf,
	}
	ttnCode := "TTN-E2E-" + suf
	cpCode := "CP-E2E-" + suf

	// 1. Inbound shipment in GATE_IN_PROGRESS.
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.inbound_shipments(shipment_id,warehouse_id,ttn_code,status,created_at,updated_at)
		SELECT gen_random_uuid(),w.warehouse_id,$1,'GATE_IN_PROGRESS',now(),now()
		FROM wms_inventory.warehouses w WHERE w.name=$2`, ttnCode, warehouseName)

	// 2. Cargoplace at RECEIVED_AT_GATE, ready for the receiving-table flow.
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.cargoplaces(cargoplace_id,shipment_id,cargoplace_code,status,received_at_gate_at,created_at,updated_at)
		SELECT gen_random_uuid(),sh.shipment_id,$1,'RECEIVED_AT_GATE',now(),now(),now()
		FROM wms_inventory.inbound_shipments sh WHERE sh.ttn_code=$2`, cpCode, ttnCode)

	// 3. One expected unit of the dedicated outbound SKU in the cargoplace.
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.expected_cargoplace_skus(cargoplace_id,sku_id,expected_qty)
		SELECT c.cargoplace_id,s.sku_id,1
		FROM wms_inventory.cargoplaces c
		JOIN wms_inventory.skus s ON s.name=$2
		WHERE c.cargoplace_code=$1`, cpCode, outboundSeedSKUName)

	// 4. NEW order for SHOP-7, owned by the seeded customer.
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.orders(order_id,external_order_no,customer_id,warehouse_id,destination_id,status,created_at,updated_at)
		SELECT gen_random_uuid(),$1,u.user_id,w.warehouse_id,d.destination_id,'NEW'::wms_inventory.order_status,now(),now()
		FROM public.users u
		JOIN wms_inventory.warehouses w ON w.name=$2
		JOIN wms_inventory.destinations d ON d.code=$3 AND d.warehouse_id=w.warehouse_id
		WHERE u.username='customer'`, fx.OrderNo, warehouseName, destinationCode)

	// 5. Order line: one unit of the outbound SKU.
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.order_lines(order_id,sku_id,qty)
		SELECT o.order_id,s.sku_id,1
		FROM wms_inventory.orders o
		JOIN wms_inventory.skus s ON s.name=$2
		WHERE o.external_order_no=$1`, fx.OrderNo, outboundSeedSKUName)

	// 6. SCHEDULED dispatch for SHOP-7.
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.outbound_dispatches(dispatch_id,dispatch_code,warehouse_id,destination_id,vehicle_number,driver_name,driver_phone,status,scheduled_at,created_at,updated_at)
		SELECT gen_random_uuid(),$1,w.warehouse_id,d.destination_id,'E2EVH','E2E Driver','+70000000000','SCHEDULED'::wms_inventory.outbound_dispatch_status,now()+interval '1 day',now(),now()
		FROM wms_inventory.warehouses w
		JOIN wms_inventory.destinations d ON d.code=$2 AND d.warehouse_id=w.warehouse_id
		WHERE w.name=$3`, fx.DispatchCode, destinationCode, warehouseName)

	// Resolve the ids for the per-run codes (mirrors the static loadSeedFixture
	// SELECT but parameterized by this run's TTN/CP/ORD/DSP codes).
	err := env.db.QueryRow(ctx, `
		SELECT
			w.warehouse_id,
			d.destination_id,
			rb.bin_id,
			sb.bin_id,
			shb.bin_id,
			c.cargoplace_id,
			s.sku_id,
			o.order_id,
			od.dispatch_id
		FROM wms_inventory.warehouses w
		JOIN wms_inventory.destinations d ON d.warehouse_id = w.warehouse_id AND d.code = 'SHOP-7'
		JOIN wms_inventory.bins rb ON rb.warehouse_id = w.warehouse_id AND rb.code = 'BUFFER-01'
		JOIN wms_inventory.bins sb ON sb.warehouse_id = w.warehouse_id AND sb.code = 'A-01-01'
		JOIN wms_inventory.bins shb ON shb.warehouse_id = w.warehouse_id AND shb.code = 'BIN-SHIP-7'
		JOIN wms_inventory.inbound_shipments sh ON sh.warehouse_id = w.warehouse_id AND sh.ttn_code = $4
		JOIN wms_inventory.cargoplaces c ON c.shipment_id = sh.shipment_id AND c.cargoplace_code = $5
		JOIN wms_inventory.sku_barcodes sbc ON sbc.barcode = $1
		JOIN wms_inventory.skus s ON s.sku_id = sbc.sku_id
		JOIN wms_inventory.orders o ON o.destination_id = d.destination_id AND o.external_order_no = $2
		JOIN wms_inventory.outbound_dispatches od ON od.destination_id = d.destination_id AND od.dispatch_code = $3
		WHERE w.name = $6`,
		fx.Barcode,
		fx.OrderNo,
		fx.DispatchCode,
		ttnCode,
		cpCode,
		warehouseName,
	).Scan(
		&fx.WarehouseID,
		&fx.DestinationID,
		&fx.ReceivingBinID,
		&fx.StorageBinID,
		&fx.ShippingBinID,
		&fx.CargoplaceID,
		&fx.SKUID,
		&fx.OrderID,
		&fx.DispatchID,
	)
	require.NoError(t, err, "resolve per-run outbound fixture ids")

	// Best-effort teardown: delete the per-run rows by suffix. Order matters for
	// FKs; an FK error just means a downstream row already removed it, so log and
	// continue rather than fail the test on cleanup.
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		// Delete children before parents (FK order). The flow creates rows in
		// wms_ops (putaways/assembly_tasks/shippings/receiving_table/receiving_gate)
		// and wms_inventory (products/boxes/...) that all reference the fixture's
		// cargoplace/order/dispatch, so those must go first.
		cpSel := `(SELECT cargoplace_id FROM wms_inventory.cargoplaces WHERE cargoplace_code=$1)`
		prodSel := `(SELECT product_id FROM wms_inventory.products WHERE cargoplace_id IN ` + cpSel + `)`
		stmts := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM public.onchain_events WHERE event_id IN (SELECT event_id FROM public.outbox_events WHERE aggregate_id IN ` + prodSel + `)`, []any{cpCode}},
			{`DELETE FROM public.outbox_events WHERE aggregate_id IN ` + prodSel, []any{cpCode}},
			{`DELETE FROM wms_ops.putaways WHERE product_id IN ` + prodSel, []any{cpCode}},
			{`DELETE FROM wms_ops.shippings WHERE dispatch_id IN (SELECT dispatch_id FROM wms_inventory.outbound_dispatches WHERE dispatch_code=$1)`, []any{fx.DispatchCode}},
			{`DELETE FROM wms_ops.assembly_tasks WHERE order_id IN (SELECT order_id FROM wms_inventory.orders WHERE external_order_no=$1)`, []any{fx.OrderNo}},
			{`DELETE FROM wms_ops.receiving_table WHERE cargoplace_id IN ` + cpSel, []any{cpCode}},
			{`DELETE FROM wms_ops.receiving_gate WHERE cargoplace_id IN ` + cpSel, []any{cpCode}},
			{`DELETE FROM wms_inventory.products WHERE cargoplace_id IN ` + cpSel, []any{cpCode}},
			{`DELETE FROM wms_inventory.boxes WHERE cargoplace_id IN ` + cpSel, []any{cpCode}},
			{`DELETE FROM wms_inventory.expected_cargoplace_skus WHERE cargoplace_id IN ` + cpSel, []any{cpCode}},
			{`DELETE FROM wms_inventory.order_lines WHERE order_id IN (SELECT order_id FROM wms_inventory.orders WHERE external_order_no=$1)`, []any{fx.OrderNo}},
			{`DELETE FROM wms_inventory.cargoplaces WHERE cargoplace_code=$1`, []any{cpCode}},
			{`DELETE FROM wms_inventory.inbound_shipments WHERE ttn_code=$1`, []any{ttnCode}},
			{`DELETE FROM wms_inventory.orders WHERE external_order_no=$1`, []any{fx.OrderNo}},
			{`DELETE FROM wms_inventory.outbound_dispatches WHERE dispatch_code=$1`, []any{fx.DispatchCode}},
		}
		for _, s := range stmts {
			if _, err := env.db.Exec(cleanupCtx, s.sql, s.args...); err != nil {
				t.Logf("newOutboundFixture cleanup (%s): %v", s.sql, err)
			}
		}
	})

	return fx
}

// mustExec runs a single INSERT/DELETE statement and fails the test on error.
func mustExec(t *testing.T, ctx context.Context, env *env, sql string, args ...any) {
	t.Helper()
	_, err := env.db.Exec(ctx, sql, args...)
	require.NoError(t, err, "exec fixture statement: %s", sql)
}

// resolveBinID returns the bin_id for a bin code at the seeded warehouse.
func resolveBinID(t *testing.T, ctx context.Context, env *env, code string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := env.db.QueryRow(ctx, `
		SELECT b.bin_id
		FROM wms_inventory.bins b
		JOIN wms_inventory.warehouses w ON w.warehouse_id = b.warehouse_id
		WHERE b.code = $1 AND w.name = $2`, code, warehouseName).Scan(&id)
	require.NoErrorf(t, err, "resolve bin %s", code)
	return id
}

// shortSuffix returns a short unique suffix for ad-hoc fixtures.
func shortSuffix() string {
	return uuid.NewString()[:8]
}

// multiProductFixture holds the resolved ids for one per-run multi-product run.
type multiProductFixture struct {
	OrderID       uuid.UUID
	OrderNo       string
	DispatchID    uuid.UUID
	DispatchCode  string
	DestinationID uuid.UUID
	ShippingBinID uuid.UUID
	SKUID         uuid.UUID
	ProductIDs    []uuid.UUID
}

// newMultiProductFixture builds a fresh, isolated multi-product outbound dataset.
//
// Unlike newOutboundFixture (a single product driven through the receiving flow),
// this inserts storedCount pre-STORED products of a fresh, per-run SKU directly,
// so allocate -> pick -> scan-shipping-buffer -> ship can be exercised without
// storedCount receiving/putaway round-trips. A fresh SKU isolates these products
// from seed stock (the seed's Nike/Футболка units) and from concurrent runs, so
// the test is re-runnable in E2E_USE_EXISTING_STACK mode — it does not depend on
// the seeded ORD-2026-0501 the way the original TestMultiProduct did.
//
// orderQty is the order line quantity. Pass orderQty == storedCount for a fully
// allocatable order; pass orderQty > storedCount to exercise insufficient stock.
func newMultiProductFixture(t *testing.T, ctx context.Context, env *env, destinationCode string, storedCount, orderQty int) multiProductFixture {
	t.Helper()

	suf := uuid.NewString()[:8]
	skuName := "E2E MP SKU " + suf
	orderNo := "ORD-MP-" + suf
	dispatchCode := "DSP-MP-" + suf
	ttnCode := "TTN-MP-" + suf
	cpCode := "CP-MP-" + suf
	const storageBinCode = "A-01-01"

	skuID := uuid.New()
	// 1. Fresh per-run SKU so this run's stock never contends with the seed or
	//    with a concurrent run.
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.skus(sku_id,name,created_at,updated_at)
		VALUES ($1,$2,now(),now())`, skuID, skuName)

	// 2. Inbound shipment + cargoplace: products.shipment_id and products.cargoplace_id
	//    are both NOT NULL, so the directly-inserted products still need a parent.
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.inbound_shipments(shipment_id,warehouse_id,ttn_code,status,created_at,updated_at)
		SELECT gen_random_uuid(),w.warehouse_id,$1,'GATE_CLOSED',now(),now()
		FROM wms_inventory.warehouses w WHERE w.name=$2`, ttnCode, warehouseName)
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.cargoplaces(cargoplace_id,shipment_id,cargoplace_code,status,created_at,updated_at)
		SELECT gen_random_uuid(),sh.shipment_id,$1,'TABLE_CLOSED',now(),now()
		FROM wms_inventory.inbound_shipments sh WHERE sh.ttn_code=$2`, cpCode, ttnCode)

	// 3. storedCount pre-STORED products of the fresh SKU in a storage bin, with
	//    order_id NULL so they are allocatable (allocate selects STORED + NULL order).
	productIDs := make([]uuid.UUID, storedCount)
	for i := 0; i < storedCount; i++ {
		pid := uuid.New()
		productIDs[i] = pid
		mustExec(t, ctx, env, `
			INSERT INTO wms_inventory.products(product_id,sku_id,shipment_id,cargoplace_id,qr_code,bin_id,status,created_at,updated_at)
			SELECT $1,s.sku_id,c.shipment_id,c.cargoplace_id,$2,b.bin_id,'STORED',now(),now()
			FROM (SELECT sku_id FROM wms_inventory.skus WHERE name=$3) s
			CROSS JOIN (SELECT shipment_id,cargoplace_id FROM wms_inventory.cargoplaces WHERE cargoplace_code=$4) c
			CROSS JOIN (
				SELECT b.bin_id FROM wms_inventory.bins b
				JOIN wms_inventory.warehouses w ON w.warehouse_id=b.warehouse_id
				WHERE b.code=$5 AND w.name=$6
			) b`,
			pid, fmt.Sprintf("QR-MP-%s-%d", suf, i), skuName, cpCode, storageBinCode, warehouseName)
	}

	// 4. NEW order for the destination with a single line of orderQty units.
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
		WHERE o.external_order_no=$1`, orderNo, skuName, orderQty)

	// 5. SCHEDULED dispatch for the destination.
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.outbound_dispatches(dispatch_id,dispatch_code,warehouse_id,destination_id,vehicle_number,driver_name,driver_phone,status,scheduled_at,created_at,updated_at)
		SELECT gen_random_uuid(),$1,w.warehouse_id,d.destination_id,'E2EMP','E2E MP Driver','+70000000001','SCHEDULED'::wms_inventory.outbound_dispatch_status,now()+interval '1 day',now(),now()
		FROM wms_inventory.warehouses w
		JOIN wms_inventory.destinations d ON d.code=$2 AND d.warehouse_id=w.warehouse_id
		WHERE w.name=$3`, dispatchCode, destinationCode, warehouseName)

	fx := multiProductFixture{
		OrderNo:      orderNo,
		DispatchCode: dispatchCode,
		SKUID:        skuID,
		ProductIDs:   productIDs,
	}

	// Resolve ids. The shipping buffer bin is found by destination + section so we
	// do not hardcode a per-shop bin code.
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
	require.NoError(t, err, "resolve multi-product fixture ids")

	// Best-effort teardown in FK order: onchain + outbox + ops children keyed by
	// product id, then products, then order/line/dispatch/cargoplace/shipment, then the
	// SKU. Unlike newOutboundFixture, this also deletes the picking/shipping outbox rows
	// and their onchain_events (keyed by aggregate_id = product_id / matching event_id)
	// so neither accumulates across runs. Because these products are inserted STORED
	// without prior receiving/putaway, their on-chain itemStatus is None, so the
	// picking/shipping events they generate revert and land FAILED in onchain_events —
	// which is exactly why those rows are cleaned up here.
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		stmts := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM public.onchain_events WHERE event_id IN (SELECT event_id FROM public.outbox_events WHERE aggregate_id = ANY($1::uuid[]))`, []any{productIDs}},
			{`DELETE FROM public.outbox_events WHERE aggregate_id = ANY($1::uuid[])`, []any{productIDs}},
			{`DELETE FROM wms_ops.shippings WHERE product_id = ANY($1::uuid[])`, []any{productIDs}},
			{`DELETE FROM wms_ops.assembly_tasks WHERE product_id = ANY($1::uuid[])`, []any{productIDs}},
			{`DELETE FROM wms_ops.putaways WHERE product_id = ANY($1::uuid[])`, []any{productIDs}},
			{`DELETE FROM wms_inventory.products WHERE product_id = ANY($1::uuid[])`, []any{productIDs}},
			{`DELETE FROM wms_inventory.order_lines WHERE order_id IN (SELECT order_id FROM wms_inventory.orders WHERE external_order_no=$1)`, []any{orderNo}},
			{`DELETE FROM wms_inventory.orders WHERE external_order_no=$1`, []any{orderNo}},
			{`DELETE FROM wms_inventory.outbound_dispatches WHERE dispatch_code=$1`, []any{dispatchCode}},
			{`DELETE FROM wms_inventory.cargoplaces WHERE cargoplace_code=$1`, []any{cpCode}},
			{`DELETE FROM wms_inventory.inbound_shipments WHERE ttn_code=$1`, []any{ttnCode}},
			{`DELETE FROM wms_inventory.skus WHERE sku_id=$1`, []any{skuID}},
		}
		for _, s := range stmts {
			if _, err := env.db.Exec(cleanupCtx, s.sql, s.args...); err != nil {
				t.Logf("newMultiProductFixture cleanup (%s): %v", s.sql, err)
			}
		}
	})

	return fx
}
