//go:build e2e

package e2e

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestMultiProduct_AllocatePickAssemble allocates a per-run multi-product order
// (a fresh SKU with N STORED products), picks all N, and places them in the
// destination's shipping buffer so the order becomes ASSEMBLED.
//
// It uses a per-run fixture (newMultiProductFixture), NOT the seeded ORD-2026-0501.
// The original version consumed the seed's fixed 5 Nike + 5 Футболка STORED units,
// so a second run in E2E_USE_EXISTING_STACK mode found the order already ALLOCATED
// and failed — harness limitation T11. The per-run fixture makes it re-runnable.
func TestMultiProduct_AllocatePickAssemble(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	const productCount = 5
	token := operatorToken(t, env)
	fx := newMultiProductFixture(t, ctx, env, "SHOP-7", productCount, productCount)
	requireOrderStatus(t, ctx, env, fx.OrderID, "NEW")

	// Allocate the destination. Use >= for the aggregate counts because a leaked
	// NEW order from a failed prior cleanup could add to the totals; the per-product
	// assertions below are scoped to this fixture's order via productIDsForOrder.
	var allocated allocateData
	postJSON(t, env, token, "/assembly/allocate", map[string]string{
		"destination_id": fx.DestinationID.String(),
	}, &allocated)
	require.GreaterOrEqual(t, allocated.AllocatedOrders, 1)
	require.GreaterOrEqual(t, allocated.AllocatedProducts, productCount)
	requireOrderStatus(t, ctx, env, fx.OrderID, "ALLOCATED")

	// Tasks for the destination must include every product of this order.
	var tasks taskListData
	getJSON(t, env, token, "/assembly/tasks?destination_id="+url.QueryEscape(fx.DestinationID.String()), &tasks)
	taskProductSet := map[string]struct{}{}
	for _, task := range tasks.Tasks {
		taskProductSet[task.ProductID] = struct{}{}
	}

	orderProductIDs := productIDsForOrder(t, ctx, env, fx.OrderID)
	require.Len(t, orderProductIDs, productCount, "order should have %d allocated products", productCount)
	for _, pid := range orderProductIDs {
		require.Containsf(t, taskProductSet, pid, "task missing for product %s", pid)
	}

	// Pick each product belonging to this order; the operator cart grows by one each
	// time (operatorToken provisions a fresh operator, so the cart starts empty).
	for i, pid := range orderProductIDs {
		parsed, err := uuid.Parse(pid)
		require.NoError(t, err)

		var picked pickData
		postJSON(t, env, token, "/assembly/pick", map[string]string{
			"product_id": pid,
		}, &picked)
		require.Equal(t, pid, picked.ProductID)
		require.Equal(t, i+1, picked.CartSize)
		requireProductStatus(t, ctx, env, parsed, "ASSEMBLED")
		requireTaskStatus(t, ctx, env, parsed, "DONE")
	}

	// Place the whole cart into the shipping buffer; the order becomes ASSEMBLED.
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
}

// productIDsForOrder returns the product ids currently assigned to an order.
func productIDsForOrder(t *testing.T, ctx context.Context, env *env, orderID uuid.UUID) []string {
	t.Helper()
	rows, err := env.db.Query(ctx, `
		SELECT product_id::text
		FROM wms_inventory.products
		WHERE order_id = $1
		ORDER BY product_id`, orderID)
	require.NoError(t, err)
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	return ids
}
