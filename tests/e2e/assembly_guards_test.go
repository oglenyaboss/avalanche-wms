//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestAssembly_CartIsolation proves that the in-memory assembly cart is keyed
// per-operator: operator2 cannot discharge operator1's cart, and operator1's
// cart accumulates picks independently.
//
// Sequence:
//  1. operator1 picks P1 (cart=[P1])
//  2. operator2 scan-shipping-buffer -> 409 CART_EMPTY (op2 has no picks)
//  3. operator1 picks P2 (cart=[P1,P2])
//  4. operator1 scan-shipping-buffer -> 200, products_placed=2, orders_assembled>=1
func TestAssembly_CartIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	// Two products, one order requiring both.
	fx := newMultiProductFixture(t, ctx, env, "SHOP-7", 2, 2)

	op1Token := operatorToken(t, env)
	op2Token := operatorToken(t, env)

	// Allocate the destination (either operator can do this).
	var allocated allocateData
	postJSON(t, env, op1Token, "/assembly/allocate", map[string]string{
		"destination_id": fx.DestinationID.String(),
	}, &allocated)
	require.GreaterOrEqual(t, allocated.AllocatedOrders, 1)
	requireOrderStatus(t, ctx, env, fx.OrderID, "ALLOCATED")

	// Resolve the product IDs assigned to this order.
	orderProductIDs := productIDsForOrder(t, ctx, env, fx.OrderID)
	require.Len(t, orderProductIDs, 2)
	p1 := orderProductIDs[0]
	p2 := orderProductIDs[1]

	// Step 1: operator1 picks P1.
	var picked1 pickData
	postJSON(t, env, op1Token, "/assembly/pick", map[string]string{
		"product_id": p1,
	}, &picked1)
	require.Equal(t, p1, picked1.ProductID)
	require.Equal(t, 1, picked1.CartSize)

	// Step 2: operator2 tries scan-shipping-buffer -> 409 CART_EMPTY.
	status, code, _ := postExpectError(t, env, op2Token, "/assembly/scan-shipping-buffer", map[string]string{
		"buffer_bin_id": fx.ShippingBinID.String(),
	})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "CART_EMPTY", code)

	// Step 3: operator1 picks P2 (cart grows to 2).
	var picked2 pickData
	postJSON(t, env, op1Token, "/assembly/pick", map[string]string{
		"product_id": p2,
	}, &picked2)
	require.Equal(t, p2, picked2.ProductID)
	require.Equal(t, 2, picked2.CartSize)

	// Step 4: operator1 scan-shipping-buffer -> success.
	var placed scanShippingBufferData
	postJSON(t, env, op1Token, "/assembly/scan-shipping-buffer", map[string]string{
		"buffer_bin_id": fx.ShippingBinID.String(),
	}, &placed)
	require.Equal(t, 2, placed.ProductsPlaced)
	require.GreaterOrEqual(t, placed.OrdersAssembled, 1)
	requireOrderStatus(t, ctx, env, fx.OrderID, "ASSEMBLED")

	// Both products should now be READY_TO_SHIP.
	for _, pid := range orderProductIDs {
		parsed, err := uuid.Parse(pid)
		require.NoError(t, err)
		requireProductStatus(t, ctx, env, parsed, "READY_TO_SHIP")
	}
}

// TestAssembly_DoubleAllocate proves that allocating the same destination twice
// is idempotent: the second call contributes zero new orders (the fixture's
// order is already ALLOCATED), the order stays ALLOCATED, and the task count
// does not change.
func TestAssembly_DoubleAllocate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	fx := newMultiProductFixture(t, ctx, env, "SHOP-7", 1, 1)
	token := operatorToken(t, env)

	// First allocate: the fixture's order goes NEW -> ALLOCATED.
	var first allocateData
	postJSON(t, env, token, "/assembly/allocate", map[string]string{
		"destination_id": fx.DestinationID.String(),
	}, &first)
	require.GreaterOrEqual(t, first.AllocatedOrders, 1)
	require.GreaterOrEqual(t, first.AllocatedProducts, 1)
	requireOrderStatus(t, ctx, env, fx.OrderID, "ALLOCATED")

	// Count tasks for this order after the first allocate.
	var tasksBefore taskListData
	getJSON(t, env, token, "/assembly/tasks?destination_id="+url.QueryEscape(fx.DestinationID.String()), &tasksBefore)
	taskCountBefore := len(tasksBefore.Tasks)
	require.GreaterOrEqual(t, taskCountBefore, 1, "at least one task should exist after first allocate")

	// Second allocate: the order is already ALLOCATED, so it contributes 0.
	var second allocateData
	postJSON(t, env, token, "/assembly/allocate", map[string]string{
		"destination_id": fx.DestinationID.String(),
	}, &second)
	require.Equal(t, 0, second.AllocatedOrders, "second allocate must not re-allocate already-ALLOCATED orders")

	// The order and task count must be unchanged.
	requireOrderStatus(t, ctx, env, fx.OrderID, "ALLOCATED")

	var tasksAfter taskListData
	getJSON(t, env, token, "/assembly/tasks?destination_id="+url.QueryEscape(fx.DestinationID.String()), &tasksAfter)
	require.Equal(t, taskCountBefore, len(tasksAfter.Tasks), "task count must not change on double allocate")
}
