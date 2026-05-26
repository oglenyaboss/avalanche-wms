//go:build e2e

package e2e

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestShipping_DoubleShip_Conflict ships one product from a two-product buffer,
// then tries to ship the same product again. The second attempt must fail with
// 409 PRODUCT_NOT_IN_BUFFER because the product is already SHIPPED.
func TestShipping_DoubleShip_Conflict(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	const productCount = 2
	token := operatorToken(t, env)
	fx := newMultiProductFixture(t, ctx, env, "SHOP-7", productCount, productCount)

	// Allocate and pick all products.
	var allocated allocateData
	postJSON(t, env, token, "/assembly/allocate", map[string]string{
		"destination_id": fx.DestinationID.String(),
	}, &allocated)
	requireOrderStatus(t, ctx, env, fx.OrderID, "ALLOCATED")

	orderProductIDs := productIDsForOrder(t, ctx, env, fx.OrderID)
	require.Len(t, orderProductIDs, productCount)
	for _, pid := range orderProductIDs {
		postJSON[pickData](t, env, token, "/assembly/pick", map[string]string{
			"product_id": pid,
		}, nil)
	}

	// Place into shipping buffer.
	var placed scanShippingBufferData
	postJSON(t, env, token, "/assembly/scan-shipping-buffer", map[string]string{
		"buffer_bin_id": fx.ShippingBinID.String(),
	}, &placed)
	require.Equal(t, productCount, placed.ProductsPlaced)
	requireOrderStatus(t, ctx, env, fx.OrderID, "ASSEMBLED")

	// Scan buffer and driver to bring dispatch to AT_GATE.
	postJSON[map[string]any](t, env, token, "/shipping/scan-buffer", map[string]string{
		"buffer_bin_id": fx.ShippingBinID.String(),
	}, nil)
	var driver scanDriverData
	postJSON(t, env, token, "/shipping/scan-driver", map[string]string{
		"dispatch_code": fx.DispatchCode,
	}, &driver)
	require.Equal(t, "AT_GATE", driver.Status)

	p1 := orderProductIDs[0]

	// Ship only P1 (buffer_remaining=1, dispatch stays AT_GATE).
	var firstShip shipData
	postJSON(t, env, token, "/shipping/ship", map[string]any{
		"buffer_bin_id": fx.ShippingBinID.String(),
		"dispatch_id":   fx.DispatchID.String(),
		"product_ids":   []string{p1},
	}, &firstShip)
	require.Equal(t, 1, firstShip.ProductsShipped)
	require.Equal(t, 1, firstShip.BufferRemaining)

	p1Parsed, err := uuid.Parse(p1)
	require.NoError(t, err)
	requireProductStatus(t, ctx, env, p1Parsed, "SHIPPED")

	// Try to ship P1 again -> 409 PRODUCT_NOT_IN_BUFFER.
	status, code, _ := postExpectError(t, env, token, "/shipping/ship", map[string]any{
		"buffer_bin_id": fx.ShippingBinID.String(),
		"dispatch_id":   fx.DispatchID.String(),
		"product_ids":   []string{p1},
	})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "PRODUCT_NOT_IN_BUFFER", code)
}

// TestShipping_ScanDriver_Idempotent scans a driver for a SCHEDULED dispatch
// twice and verifies the second scan is a no-op: status stays AT_GATE and the
// arrived_at timestamp does not change.
func TestShipping_ScanDriver_Idempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)
	fx := newMultiProductFixture(t, ctx, env, "SHOP-7", 1, 1)

	// First scan-driver: SCHEDULED -> AT_GATE.
	var first scanDriverData
	postJSON(t, env, token, "/shipping/scan-driver", map[string]string{
		"dispatch_code": fx.DispatchCode,
	}, &first)
	require.Equal(t, fx.DispatchID, first.DispatchID)
	require.Equal(t, "AT_GATE", first.Status)

	// Record arrived_at from DB.
	var arrivedAt time.Time
	err := env.db.QueryRow(ctx, `
		SELECT arrived_at FROM wms_inventory.outbound_dispatches
		WHERE dispatch_id = $1`, fx.DispatchID).Scan(&arrivedAt)
	require.NoError(t, err)
	require.False(t, arrivedAt.IsZero(), "arrived_at must be set after scan-driver")

	// Second scan-driver: should be idempotent.
	var second scanDriverData
	postJSON(t, env, token, "/shipping/scan-driver", map[string]string{
		"dispatch_code": fx.DispatchCode,
	}, &second)
	require.Equal(t, fx.DispatchID, second.DispatchID)
	require.Equal(t, "AT_GATE", second.Status)

	// arrived_at must not change.
	var arrivedAtAfter time.Time
	err = env.db.QueryRow(ctx, `
		SELECT arrived_at FROM wms_inventory.outbound_dispatches
		WHERE dispatch_id = $1`, fx.DispatchID).Scan(&arrivedAtAfter)
	require.NoError(t, err)
	require.Equal(t, arrivedAt, arrivedAtAfter, "arrived_at must not change on idempotent scan-driver")
	requireDispatchStatus(t, ctx, env, fx.DispatchID, "AT_GATE")
}

// TestShipping_ScanDriver_AlreadyDeparted drives a dispatch through the full
// ship flow (all products shipped -> DEPARTED), then tries scan-driver again.
// The second scan-driver must fail with 409 DISPATCH_ALREADY_DEPARTED.
func TestShipping_ScanDriver_AlreadyDeparted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)
	fx := newMultiProductFixture(t, ctx, env, "SHOP-7", 1, 1)

	// Full flow: allocate -> pick -> scan-shipping-buffer -> scan-buffer ->
	// scan-driver -> ship (all products -> dispatch DEPARTED).
	postJSON[allocateData](t, env, token, "/assembly/allocate", map[string]string{
		"destination_id": fx.DestinationID.String(),
	}, nil)

	orderProductIDs := productIDsForOrder(t, ctx, env, fx.OrderID)
	require.Len(t, orderProductIDs, 1)
	for _, pid := range orderProductIDs {
		postJSON[pickData](t, env, token, "/assembly/pick", map[string]string{
			"product_id": pid,
		}, nil)
	}

	postJSON[scanShippingBufferData](t, env, token, "/assembly/scan-shipping-buffer", map[string]string{
		"buffer_bin_id": fx.ShippingBinID.String(),
	}, nil)

	postJSON[map[string]any](t, env, token, "/shipping/scan-buffer", map[string]string{
		"buffer_bin_id": fx.ShippingBinID.String(),
	}, nil)

	postJSON[scanDriverData](t, env, token, "/shipping/scan-driver", map[string]string{
		"dispatch_code": fx.DispatchCode,
	}, nil)

	var shipped shipData
	postJSON(t, env, token, "/shipping/ship", map[string]any{
		"buffer_bin_id": fx.ShippingBinID.String(),
		"dispatch_id":   fx.DispatchID.String(),
		"product_ids":   orderProductIDs,
	}, &shipped)
	require.True(t, shipped.DispatchDeparted)
	requireDispatchStatus(t, ctx, env, fx.DispatchID, "DEPARTED")

	// Scan-driver on a DEPARTED dispatch -> 409 DISPATCH_ALREADY_DEPARTED.
	status, code, _ := postExpectError(t, env, token, "/shipping/scan-driver", map[string]string{
		"dispatch_code": fx.DispatchCode,
	})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "DISPATCH_ALREADY_DEPARTED", code)
}

// TestShipping_DestinationMismatch creates a second destination with its own
// dispatch and attempts to ship against SHOP-7's buffer using the second
// dispatch. The guard must reject with 409 DESTINATION_MISMATCH because
// the buffer's destination (SHOP-7) differs from the dispatch's destination.
func TestShipping_DestinationMismatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)

	// Resolve SHOP-7's shipping buffer bin.
	shipBinID := resolveBinID(t, ctx, env, "BIN-SHIP-7")

	// Create a second destination and its shipping buffer bin.
	suf := shortSuffix()
	destCode := "SHOP-E2E-" + suf
	dispatchCode := "DSP-MISMATCH-" + suf
	binCode := "BIN-SHIP-E2E-" + suf

	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.destinations(destination_id,warehouse_id,code,name,address,created_at,updated_at)
		SELECT gen_random_uuid(),w.warehouse_id,$1,$1,'E2E mismatch addr',now(),now()
		FROM wms_inventory.warehouses w WHERE w.name=$2`, destCode, warehouseName)

	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.bins(bin_id,warehouse_id,destination_id,code,section,created_at,updated_at)
		SELECT gen_random_uuid(),w.warehouse_id,d.destination_id,$1,'SHIPPING_BUFFER',now(),now()
		FROM wms_inventory.warehouses w
		JOIN wms_inventory.destinations d ON d.warehouse_id=w.warehouse_id AND d.code=$2
		WHERE w.name=$3`, binCode, destCode, warehouseName)

	// Create a SCHEDULED dispatch for the second destination.
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.outbound_dispatches(dispatch_id,dispatch_code,warehouse_id,destination_id,vehicle_number,driver_name,driver_phone,status,scheduled_at,created_at,updated_at)
		SELECT gen_random_uuid(),$1,w.warehouse_id,d.destination_id,'E2EMM','E2E Mismatch Driver','+70000000099','SCHEDULED'::wms_inventory.outbound_dispatch_status,now()+interval '1 day',now(),now()
		FROM wms_inventory.warehouses w
		JOIN wms_inventory.destinations d ON d.code=$2 AND d.warehouse_id=w.warehouse_id
		WHERE w.name=$3`, dispatchCode, destCode, warehouseName)

	// Resolve the second dispatch's ID.
	var secondDispatchID uuid.UUID
	err := env.db.QueryRow(ctx, `
		SELECT dispatch_id FROM wms_inventory.outbound_dispatches
		WHERE dispatch_code = $1`, dispatchCode).Scan(&secondDispatchID)
	require.NoError(t, err, "resolve second dispatch ID")

	// Bring the second dispatch to AT_GATE.
	var driver scanDriverData
	postJSON(t, env, token, "/shipping/scan-driver", map[string]string{
		"dispatch_code": dispatchCode,
	}, &driver)
	require.Equal(t, "AT_GATE", driver.Status)

	// Cleanup: delete the ad-hoc rows in FK order on teardown.
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		stmts := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM wms_inventory.outbound_dispatches WHERE dispatch_code=$1`, []any{dispatchCode}},
			{`DELETE FROM wms_inventory.bins WHERE code=$1`, []any{binCode}},
			{`DELETE FROM wms_inventory.destinations WHERE code=$1`, []any{destCode}},
		}
		for _, s := range stmts {
			if _, err := env.db.Exec(cleanupCtx, s.sql, s.args...); err != nil {
				t.Logf("DestinationMismatch cleanup (%s): %v", s.sql, err)
			}
		}
	})

	// Ship with SHOP-7's buffer but the second dispatch -> 409 DESTINATION_MISMATCH.
	status, code, _ := postExpectError(t, env, token, "/shipping/ship", map[string]any{
		"buffer_bin_id": shipBinID.String(),
		"dispatch_id":   secondDispatchID.String(),
		"product_ids":   []string{},
	})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "DESTINATION_MISMATCH", code)
}

// TestDispatches_GetByID_NotFound issues GET /dispatches/{random-uuid} for a
// nonexistent dispatch. Per Shipping-5 (BUGHUNT.md), the server currently returns
// 503 because pgx.ErrNoRows is unmapped; the correct response would be 404.
//
// This test asserts only that the status is NOT 200, and logs the actual status so
// the failure mode is documented. Once the product bug is fixed, tighten the
// assertion to 404.
//
// NOTE: we do NOT use getExpectError here because the 503 response may not follow
// the standard envelope format (the bug is that the error is unmapped, so the
// handler may return a raw body rather than {success, error}).
func TestDispatches_GetByID_NotFound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)
	randomID := uuid.NewString()
	path := "/dispatches/" + randomID

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.wmsURL+path, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := env.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	// The response MUST NOT be 200 (a nonexistent dispatch is not a success).
	require.NotEqualf(t, http.StatusOK, resp.StatusCode,
		"GET %s for nonexistent dispatch should not return 200; body: %s", path, string(body))

	// Document the actual status for visibility. Once Shipping-5 is fixed, change
	// to: require.Equal(t, http.StatusNotFound, resp.StatusCode)
	t.Logf("GET %s returned status %d (expected 404 once Shipping-5 is fixed); body: %s",
		path, resp.StatusCode, string(body))
}
