//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestPartialShipment_MofN ships a subset (M of N) of one order's products, then the
// remainder, and verifies the partial-ship DB contract:
//   - the order flips to SHIPPED and the dispatch DEPARTS only after every product has
//     shipped (UpdateOrdersShippedConditional + the buffer-empty departure guard);
//   - buffer_remaining reflects the products still staged after a partial ship.
//
// This is a DB-contract test with NO on-chain assertions. The fixture inserts pre-STORED
// products directly, so their on-chain itemStatus is None — the resulting picking/shipping
// events revert the contract FSM (which requires PutAway/Picked) and never COMMIT, exactly
// like the original TestMultiProduct (which is why it, too, asserts only DB state). The
// HTTP calls still succeed because the WMS commits its DB transaction before the chain is
// touched. Proving on-chain partial isolation needs a fixture that first primes each item
// to PutAway on-chain; that follow-up is tracked in BUGHUNT.md. The happy path
// (TestOutboundFlow_EndToEnd) already covers the full on-chain FSM for a received product.
func TestPartialShipment_MofN(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	const productCount = 3
	token := operatorToken(t, env)
	fx := newMultiProductFixture(t, ctx, env, "SHOP-7", productCount, productCount)

	// Allocate the order, then pick every product (DB-level: ASSEMBLED + task DONE).
	var allocated allocateData
	postJSON(t, env, token, "/assembly/allocate", map[string]string{
		"destination_id": fx.DestinationID.String(),
	}, &allocated)
	requireOrderStatus(t, ctx, env, fx.OrderID, "ALLOCATED")

	orderProductIDs := productIDsForOrder(t, ctx, env, fx.OrderID)
	require.Len(t, orderProductIDs, productCount)
	for _, pid := range orderProductIDs {
		var picked pickData
		postJSON(t, env, token, "/assembly/pick", map[string]string{"product_id": pid}, &picked)
	}

	var placed scanShippingBufferData
	postJSON(t, env, token, "/assembly/scan-shipping-buffer", map[string]string{
		"buffer_bin_id": fx.ShippingBinID.String(),
	}, &placed)
	require.Equal(t, productCount, placed.ProductsPlaced)
	requireOrderStatus(t, ctx, env, fx.OrderID, "ASSEMBLED")

	// Bring the dispatch to the gate.
	postJSON[map[string]any](t, env, token, "/shipping/scan-buffer", map[string]string{
		"buffer_bin_id": fx.ShippingBinID.String(),
	}, nil)
	var driver scanDriverData
	postJSON(t, env, token, "/shipping/scan-driver", map[string]string{
		"dispatch_code": fx.DispatchCode,
	}, &driver)
	require.Equal(t, "AT_GATE", driver.Status)

	firstBatch := orderProductIDs[:productCount-1] // M = N-1
	remainder := orderProductIDs[productCount-1:]  // the last one

	// 1) Ship the first M: the order must NOT complete and the dispatch must NOT depart
	//    while the remainder is still staged.
	var firstShip shipData
	postJSON(t, env, token, "/shipping/ship", map[string]any{
		"buffer_bin_id": fx.ShippingBinID.String(),
		"dispatch_id":   fx.DispatchID.String(),
		"product_ids":   firstBatch,
	}, &firstShip)
	require.Equal(t, len(firstBatch), firstShip.ProductsShipped)
	require.Equal(t, 0, firstShip.OrdersCompleted, "order must not complete while products remain")
	require.False(t, firstShip.DispatchDeparted, "dispatch must not depart while the buffer is non-empty")
	require.Equal(t, len(remainder), firstShip.BufferRemaining)

	requireOrderStatus(t, ctx, env, fx.OrderID, "ASSEMBLED")
	requireDispatchStatus(t, ctx, env, fx.DispatchID, "AT_GATE")
	for _, pid := range firstBatch {
		parsed, err := uuid.Parse(pid)
		require.NoError(t, err)
		requireProductStatus(t, ctx, env, parsed, "SHIPPED")
	}
	for _, pid := range remainder {
		parsed, err := uuid.Parse(pid)
		require.NoError(t, err)
		requireProductStatus(t, ctx, env, parsed, "READY_TO_SHIP")
	}

	// 2) Ship the remainder: now the order completes and the dispatch departs.
	var finalShip shipData
	postJSON(t, env, token, "/shipping/ship", map[string]any{
		"buffer_bin_id": fx.ShippingBinID.String(),
		"dispatch_id":   fx.DispatchID.String(),
		"product_ids":   remainder,
	}, &finalShip)
	require.Equal(t, len(remainder), finalShip.ProductsShipped)
	require.Equal(t, 1, finalShip.OrdersCompleted, "order completes once its last product ships")
	require.True(t, finalShip.DispatchDeparted)
	require.Equal(t, 0, finalShip.BufferRemaining)

	requireOrderStatus(t, ctx, env, fx.OrderID, "SHIPPED")
	requireDispatchStatus(t, ctx, env, fx.DispatchID, "DEPARTED")
	for _, pid := range remainder {
		parsed, err := uuid.Parse(pid)
		require.NoError(t, err)
		requireProductStatus(t, ctx, env, parsed, "SHIPPED")
	}
}
