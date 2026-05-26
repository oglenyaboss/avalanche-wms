//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAllocate_InsufficientStock verifies the allocate contract when an order
// demands more units than are in stock.
//
// Verified against assembly/service.go: a per-order shortage does NOT error the
// call. Allocate collects the order into insufficient_orders and returns HTTP 200;
// the 422 INSUFFICIENT_STOCK mapping only fires if the whole transaction errors,
// which the per-order path never triggers. Allocation is all-or-nothing per order:
// the early return happens before any UpdateProductAllocated, so the order stays
// NEW, the in-stock unit stays STORED/unbound, and no assembly task is created.
func TestAllocate_InsufficientStock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)

	// 1 unit in stock, an order line demanding 3 → short by 2.
	const (
		inStock  = 1
		orderQty = 3
	)
	fx := newMultiProductFixture(t, ctx, env, "SHOP-7", inStock, orderQty)
	require.Len(t, fx.ProductIDs, inStock)

	// Allocate returns 200 even though this order cannot be satisfied.
	var allocated allocateData
	postJSON(t, env, token, "/assembly/allocate", map[string]string{
		"destination_id": fx.DestinationID.String(),
	}, &allocated)

	// This order must be reported as insufficient (scoped by order id so a leaked
	// NEW order from a prior failed cleanup cannot mask the result).
	var found *insufficientOrderData
	for i := range allocated.InsufficientOrders {
		if allocated.InsufficientOrders[i].OrderID == fx.OrderID.String() {
			found = &allocated.InsufficientOrders[i]
			break
		}
	}
	require.NotNil(t, found, "order should be reported in insufficient_orders")
	require.Len(t, found.MissingSKUs, 1)
	require.Equal(t, fx.SKUID.String(), found.MissingSKUs[0].SKUID)
	require.Equal(t, orderQty-inStock, found.MissingSKUs[0].MissingQty)
	require.NotEmpty(t, found.MissingSKUs[0].SKUName, "missing SKU name should be resolved")

	// All-or-nothing: nothing was reserved for this order.
	requireOrderStatus(t, ctx, env, fx.OrderID, "NEW")

	requireProductStatus(t, ctx, env, fx.ProductIDs[0], "STORED")
	var boundOrder *string
	require.NoError(t, env.db.QueryRow(ctx, `
		SELECT order_id::text FROM wms_inventory.products WHERE product_id = $1`,
		fx.ProductIDs[0]).Scan(&boundOrder))
	require.Nil(t, boundOrder, "in-stock unit must not be bound to the order")

	var taskCount int
	require.NoError(t, env.db.QueryRow(ctx, `
		SELECT count(*) FROM wms_ops.assembly_tasks WHERE order_id = $1`,
		fx.OrderID).Scan(&taskCount))
	require.Equal(t, 0, taskCount, "no assembly task should be created for an insufficient order")
}
