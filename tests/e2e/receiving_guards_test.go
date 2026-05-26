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

// ── Response DTOs for gate-flow endpoints ─────────────────────────────────

type scanTTNData struct {
	ShipmentID          uuid.UUID `json:"shipment_id"`
	TTNCode             string    `json:"ttn_code"`
	Status              string    `json:"status"`
	TotalCargoplaces    int       `json:"total_cargoplaces"`
	ReceivedCargoplaces int       `json:"received_cargoplaces"`
}

type scanGateCargoplaceData struct {
	CargoplaceID   uuid.UUID `json:"cargoplace_id"`
	CargoplaceCode string    `json:"cargoplace_code"`
	Status         string    `json:"status"`
	Progress       struct {
		Total     int `json:"total"`
		Received  int `json:"received"`
		Remaining int `json:"remaining"`
	} `json:"progress"`
}

type acceptShipmentData struct {
	ShipmentID uuid.UUID `json:"shipment_id"`
	Status     string    `json:"status"`
	Summary    struct {
		Total       int `json:"total"`
		Received    int `json:"received"`
		NotReceived int `json:"not_received"`
	} `json:"summary"`
}

// ── Gate-flow fixture ─────────────────────────────────────────────────────

// gateFixture holds per-run identifiers for a CREATED shipment with EXPECTED
// cargoplaces, suitable for gate-flow tests. Unlike newOutboundFixture (which
// creates GATE_IN_PROGRESS / RECEIVED_AT_GATE), this starts at the earliest
// lifecycle state so scan-ttn and scan-cargoplace can be exercised.
type gateFixture struct {
	ShipmentID     uuid.UUID
	TTNCode        string
	CargoplaceCode string // first cargoplace (the one to scan)
	CargoplaceID   uuid.UUID
	CP2Code        string    // second cargoplace (left EXPECTED for accept-shipment)
	CP2ID          uuid.UUID
}

// newGateFixture inserts a CREATED shipment with two EXPECTED cargoplaces into
// the seeded warehouse. Two cargoplaces prevent the auto-close that fires when
// received == total after a single scan-cargoplace (see tryAutoCloseShipment).
func newGateFixture(t *testing.T, ctx context.Context, env *env) gateFixture {
	t.Helper()

	suf := shortSuffix()
	fx := gateFixture{
		TTNCode:        "TTN-GATE-" + suf,
		CargoplaceCode: "CP-GATE-A-" + suf,
		CP2Code:        "CP-GATE-B-" + suf,
	}

	// 1. Shipment in CREATED status.
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.inbound_shipments(shipment_id,warehouse_id,ttn_code,status,created_at,updated_at)
		SELECT gen_random_uuid(),w.warehouse_id,$1,'CREATED',now(),now()
		FROM wms_inventory.warehouses w WHERE w.name=$2`, fx.TTNCode, warehouseName)

	// 2. Two cargoplaces in EXPECTED status.
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.cargoplaces(cargoplace_id,shipment_id,cargoplace_code,status,created_at,updated_at)
		SELECT gen_random_uuid(),sh.shipment_id,$1,'EXPECTED',now(),now()
		FROM wms_inventory.inbound_shipments sh WHERE sh.ttn_code=$2`, fx.CargoplaceCode, fx.TTNCode)

	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.cargoplaces(cargoplace_id,shipment_id,cargoplace_code,status,created_at,updated_at)
		SELECT gen_random_uuid(),sh.shipment_id,$1,'EXPECTED',now(),now()
		FROM wms_inventory.inbound_shipments sh WHERE sh.ttn_code=$2`, fx.CP2Code, fx.TTNCode)

	// 3. Resolve generated UUIDs.
	err := env.db.QueryRow(ctx, `
		SELECT sh.shipment_id
		FROM wms_inventory.inbound_shipments sh
		WHERE sh.ttn_code = $1`, fx.TTNCode).Scan(&fx.ShipmentID)
	require.NoError(t, err, "resolve gate fixture shipment_id")

	err = env.db.QueryRow(ctx, `
		SELECT c.cargoplace_id
		FROM wms_inventory.cargoplaces c
		WHERE c.cargoplace_code = $1`, fx.CargoplaceCode).Scan(&fx.CargoplaceID)
	require.NoError(t, err, "resolve gate fixture cargoplace_id")

	err = env.db.QueryRow(ctx, `
		SELECT c.cargoplace_id
		FROM wms_inventory.cargoplaces c
		WHERE c.cargoplace_code = $1`, fx.CP2Code).Scan(&fx.CP2ID)
	require.NoError(t, err, "resolve gate fixture cp2_id")

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		stmts := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM wms_ops.receiving_gate WHERE cargoplace_id IN (SELECT cargoplace_id FROM wms_inventory.cargoplaces WHERE shipment_id=$1)`, []any{fx.ShipmentID}},
			{`DELETE FROM wms_ops.receiving_gate WHERE shipment_id=$1`, []any{fx.ShipmentID}},
			{`DELETE FROM wms_inventory.cargoplaces WHERE shipment_id=$1`, []any{fx.ShipmentID}},
			{`DELETE FROM wms_inventory.inbound_shipments WHERE shipment_id=$1`, []any{fx.ShipmentID}},
		}
		for _, s := range stmts {
			if _, err := env.db.Exec(cleanupCtx, s.sql, s.args...); err != nil {
				t.Logf("gateFixture cleanup (%s): %v", s.sql, err)
			}
		}
	})

	return fx
}

// ── 1. Gate flow happy path ───────────────────────────────────────────────

// TestReceiving_GateFlow exercises the full gate-flow happy path:
// scan-ttn (CREATED -> GATE_IN_PROGRESS), scan-cargoplace (EXPECTED ->
// RECEIVED_AT_GATE), and accept-shipment (marks missing cargoplaces
// NOT_RECEIVED, closes shipment to GATE_CLOSED).
func TestReceiving_GateFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)
	fx := newGateFixture(t, ctx, env)

	t.Run("scan-ttn moves shipment to GATE_IN_PROGRESS", func(t *testing.T) {
		var result scanTTNData
		postJSON(t, env, token, "/receiving/gate/scan-ttn", map[string]string{
			"ttn_code": fx.TTNCode,
		}, &result)

		require.Equal(t, fx.ShipmentID, result.ShipmentID)
		require.Equal(t, "GATE_IN_PROGRESS", result.Status)
		require.Equal(t, 2, result.TotalCargoplaces)
		require.Equal(t, 0, result.ReceivedCargoplaces)

		// DB assertion: shipment status persisted.
		var dbStatus string
		err := env.db.QueryRow(ctx, `
			SELECT status::text FROM wms_inventory.inbound_shipments WHERE shipment_id=$1`,
			fx.ShipmentID).Scan(&dbStatus)
		require.NoError(t, err)
		require.Equal(t, "GATE_IN_PROGRESS", dbStatus)
	})

	t.Run("scan-cargoplace moves cargoplace to RECEIVED_AT_GATE", func(t *testing.T) {
		var result scanGateCargoplaceData
		postJSON(t, env, token, "/receiving/gate/scan-cargoplace", map[string]string{
			"shipment_id":    fx.ShipmentID.String(),
			"cargoplace_code": fx.CargoplaceCode,
		}, &result)

		require.Equal(t, fx.CargoplaceID, result.CargoplaceID)
		require.Equal(t, "RECEIVED_AT_GATE", result.Status)
		require.Equal(t, 2, result.Progress.Total)
		require.Equal(t, 1, result.Progress.Received)
		require.Equal(t, 1, result.Progress.Remaining)

		// DB assertion: cargoplace status persisted.
		var dbStatus string
		err := env.db.QueryRow(ctx, `
			SELECT status::text FROM wms_inventory.cargoplaces WHERE cargoplace_id=$1`,
			fx.CargoplaceID).Scan(&dbStatus)
		require.NoError(t, err)
		require.Equal(t, "RECEIVED_AT_GATE", dbStatus)

		// Shipment must still be GATE_IN_PROGRESS (not auto-closed, because 1/2 received).
		var shipStatus string
		err = env.db.QueryRow(ctx, `
			SELECT status::text FROM wms_inventory.inbound_shipments WHERE shipment_id=$1`,
			fx.ShipmentID).Scan(&shipStatus)
		require.NoError(t, err)
		require.Equal(t, "GATE_IN_PROGRESS", shipStatus)
	})

	t.Run("accept-shipment marks missing cargoplaces NOT_RECEIVED", func(t *testing.T) {
		var result acceptShipmentData
		postJSON(t, env, token, "/receiving/gate/accept-shipment", map[string]string{
			"shipment_id": fx.ShipmentID.String(),
		}, &result)

		require.Equal(t, fx.ShipmentID, result.ShipmentID)
		require.Equal(t, "GATE_CLOSED", result.Status)
		require.Equal(t, 2, result.Summary.Total)
		require.Equal(t, 1, result.Summary.Received)
		require.Equal(t, 1, result.Summary.NotReceived)

		// DB assertion: shipment closed.
		var shipStatus string
		err := env.db.QueryRow(ctx, `
			SELECT status::text FROM wms_inventory.inbound_shipments WHERE shipment_id=$1`,
			fx.ShipmentID).Scan(&shipStatus)
		require.NoError(t, err)
		require.Equal(t, "GATE_CLOSED", shipStatus)

		// DB assertion: the un-scanned cargoplace is now NOT_RECEIVED.
		var cp2Status string
		err = env.db.QueryRow(ctx, `
			SELECT status::text FROM wms_inventory.cargoplaces WHERE cargoplace_id=$1`,
			fx.CP2ID).Scan(&cp2Status)
		require.NoError(t, err)
		require.Equal(t, "NOT_RECEIVED", cp2Status)
	})
}

// ── 2. ScanTTN on closed shipment ─────────────────────────────────────────

// TestReceiving_ScanTTN_AlreadyClosed verifies that scanning a TTN whose
// shipment is already GATE_CLOSED returns 409 SHIPMENT_ALREADY_CLOSED.
func TestReceiving_ScanTTN_AlreadyClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)

	suf := shortSuffix()
	ttnCode := "TTN-CLOSED-" + suf

	// Insert a shipment already in GATE_CLOSED status.
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.inbound_shipments(shipment_id,warehouse_id,ttn_code,status,created_at,updated_at)
		SELECT gen_random_uuid(),w.warehouse_id,$1,'GATE_CLOSED',now(),now()
		FROM wms_inventory.warehouses w WHERE w.name=$2`, ttnCode, warehouseName)

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if _, err := env.db.Exec(cleanupCtx,
			`DELETE FROM wms_ops.receiving_gate WHERE shipment_id IN (SELECT shipment_id FROM wms_inventory.inbound_shipments WHERE ttn_code=$1)`, ttnCode); err != nil {
			t.Logf("closed-shipment cleanup (gate logs): %v", err)
		}
		if _, err := env.db.Exec(cleanupCtx,
			`DELETE FROM wms_inventory.inbound_shipments WHERE ttn_code=$1`, ttnCode); err != nil {
			t.Logf("closed-shipment cleanup: %v", err)
		}
	})

	status, code, _ := postExpectError(t, env, token, "/receiving/gate/scan-ttn", map[string]string{
		"ttn_code": ttnCode,
	})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "SHIPMENT_ALREADY_CLOSED", code)
}

// ── 3. Duplicate scan-cargoplace ──────────────────────────────────────────

// TestReceiving_ScanCargoplace_AlreadyReceived verifies that scanning a
// cargoplace that is already RECEIVED_AT_GATE returns 409
// CARGOPLACE_ALREADY_RECEIVED.
func TestReceiving_ScanCargoplace_AlreadyReceived(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)
	fx := newGateFixture(t, ctx, env)

	// Drive the first cargoplace through scan-ttn + scan-cargoplace.
	postJSON[scanTTNData](t, env, token, "/receiving/gate/scan-ttn", map[string]string{
		"ttn_code": fx.TTNCode,
	}, nil)

	postJSON[scanGateCargoplaceData](t, env, token, "/receiving/gate/scan-cargoplace", map[string]string{
		"shipment_id":    fx.ShipmentID.String(),
		"cargoplace_code": fx.CargoplaceCode,
	}, nil)

	// Second scan of the same cargoplace must fail.
	status, code, _ := postExpectError(t, env, token, "/receiving/gate/scan-cargoplace", map[string]string{
		"shipment_id":    fx.ShipmentID.String(),
		"cargoplace_code": fx.CargoplaceCode,
	})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "CARGOPLACE_ALREADY_RECEIVED", code)
}

// ── 4. Duplicate QR code ──────────────────────────────────────────────────

// TestReceiving_ScanQR_Duplicate drives a product through the table flow and
// then attempts to scan the same QR code again, expecting 409 QR_ALREADY_EXISTS.
func TestReceiving_ScanQR_Duplicate(t *testing.T) {
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

	// Create a box and scan the SKU.
	var box scanBoxData
	postJSON(t, env, token, "/receiving/table/scan-box", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_barcode":   fixture.BoxBarcode,
	}, &box)

	var sku scanSKUData
	postJSON(t, env, token, "/receiving/table/scan-sku", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"barcode":       fixture.Barcode,
	}, &sku)

	// First scan-qr: success.
	postJSON[scanQRData](t, env, token, "/receiving/table/scan-qr", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"sku_id":        sku.SKUID.String(),
		"qr_code":       fixture.QRCode,
	}, nil)

	// Second scan-qr with the same QR code: must fail.
	status, code, _ := postExpectError(t, env, token, "/receiving/table/scan-qr", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"sku_id":        sku.SKUID.String(),
		"qr_code":       fixture.QRCode,
	})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "QR_ALREADY_EXISTS", code)
}

// ── 5. Scan SKU on a closed box ───────────────────────────────────────────

// TestReceiving_ScanBox_ClosedBox verifies that operating on a closed box
// returns 409 BOX_NOT_OPEN. The trigger is scan-sku (not scan-box) because
// scan-box uses UpsertBox semantics and may return the existing row without
// error; scan-sku is the first operation that validates box-open status.
func TestReceiving_ScanBox_ClosedBox(t *testing.T) {
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

	// Create a box, scan one SKU+QR so the box is non-empty, then close it.
	var box scanBoxData
	postJSON(t, env, token, "/receiving/table/scan-box", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_barcode":   fixture.BoxBarcode,
	}, &box)

	var sku scanSKUData
	postJSON(t, env, token, "/receiving/table/scan-sku", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"barcode":       fixture.Barcode,
	}, &sku)

	postJSON[scanQRData](t, env, token, "/receiving/table/scan-qr", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"sku_id":        sku.SKUID.String(),
		"qr_code":       fixture.QRCode,
	}, nil)

	postJSON[map[string]any](t, env, token, "/receiving/table/close-box", map[string]string{
		"box_id": box.BoxID.String(),
	}, nil)

	// Now scan-sku on the closed box must fail.
	status, code, _ := postExpectError(t, env, token, "/receiving/table/scan-sku", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"barcode":       fixture.Barcode,
	})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "BOX_NOT_OPEN", code)
}

// ── 6. Scan SKU with unknown barcode ──────────────────────────────────────

// TestReceiving_ScanSKU_UnknownBarcode verifies that scan-sku with a barcode
// that does not exist in sku_barcodes returns 404 BARCODE_NOT_FOUND.
func TestReceiving_ScanSKU_UnknownBarcode(t *testing.T) {
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

	// Create a box so we have a valid box_id.
	var box scanBoxData
	postJSON(t, env, token, "/receiving/table/scan-box", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_barcode":   fixture.BoxBarcode,
	}, &box)

	// Scan with a barcode that does not exist in the system.
	status, code, _ := postExpectError(t, env, token, "/receiving/table/scan-sku", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"barcode":       "9999999999999",
	})
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "BARCODE_NOT_FOUND", code)
}

// ── 7. Scan buffer with a non-buffer bin ──────────────────────────────────

// TestReceiving_ScanBuffer_NonBufferBin verifies that scan-buffer with a storage
// bin (section=STORAGE instead of BUFFER) returns 400 BIN_NOT_BUFFER.
func TestReceiving_ScanBuffer_NonBufferBin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)
	fixture := newOutboundFixture(t, ctx, env)

	// Move cargoplace to TABLE_IN_PROGRESS so the scan-buffer endpoint reaches
	// the bin-section validation (guards are checked in order: cargoplace status
	// first, then bin section).
	postJSON[map[string]any](t, env, token, "/receiving/table/scan-cargoplace", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
	}, nil)

	// Use the seeded STORAGE bin (A-01-01) instead of the BUFFER bin.
	storageBinID := resolveBinID(t, ctx, env, "A-01-01")

	status, code, _ := postExpectError(t, env, token, "/receiving/table/scan-buffer", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"buffer_bin_id": storageBinID.String(),
	})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "BIN_NOT_BUFFER", code)
}
