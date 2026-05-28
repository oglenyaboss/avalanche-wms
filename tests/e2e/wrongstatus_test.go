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

// TestWrongStatus_CloseCargoplaceNotInProgress closes a cargoplace that is still
// RECEIVED_AT_GATE (never scanned to TABLE_IN_PROGRESS). The FSM guard rejects
// it with 409 CARGOPLACE_NOT_IN_PROGRESS.
func TestWrongStatus_CloseCargoplaceNotInProgress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)
	fixture := newOutboundFixture(t, ctx, env) // cargoplace is RECEIVED_AT_GATE

	status, code, _ := postExpectError(t, env, token, "/receiving/table/close-cargoplace", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
	})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "CARGOPLACE_NOT_IN_PROGRESS", code)
}

// TestWrongStatus_PickWithoutTask picks a STORED product that has no assembly
// task. The service rejects it with 409 NO_TASK_FOR_PRODUCT.
func TestWrongStatus_PickWithoutTask(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)

	// A STORED product with no order and no assembly task (e.g. seeded QR-2026-0003/0004).
	var productID uuid.UUID
	err := env.db.QueryRow(ctx, `
		SELECT p.product_id
		FROM wms_inventory.products p
		WHERE p.status = 'STORED'
		  AND p.order_id IS NULL
		  AND NOT EXISTS (
		    SELECT 1 FROM wms_ops.assembly_tasks at WHERE at.product_id = p.product_id
		  )
		LIMIT 1`).Scan(&productID)
	require.NoError(t, err, "no STORED product without a task available")

	status, code, _ := postExpectError(t, env, token, "/assembly/pick", map[string]string{
		"product_id": productID.String(),
	})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "NO_TASK_FOR_PRODUCT", code)
}

// TestWrongStatus_ShipDispatchNotAtGate ships against a SCHEDULED dispatch using
// the matching SHOP-7 shipping buffer (BIN-SHIP-7). The Ship guard order is:
// bin-section check (passes for BIN-SHIP-7), destination match (passes, both
// SHOP-7), then dispatch-at-gate check -> 409 DISPATCH_NOT_AT_GATE.
func TestWrongStatus_ShipDispatchNotAtGate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)
	fixture := newOutboundFixture(t, ctx, env) // dispatch is SCHEDULED
	shipBinID := resolveBinID(t, ctx, env, "BIN-SHIP-7")

	status, code, _ := postExpectError(t, env, token, "/shipping/ship", map[string]any{
		"buffer_bin_id": shipBinID.String(),
		"dispatch_id":   fixture.DispatchID.String(),
		"product_ids":   []string{},
	})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "DISPATCH_NOT_AT_GATE", code)
}
