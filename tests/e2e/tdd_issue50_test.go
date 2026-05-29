//go:build e2e

package e2e

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

// TestAssembly_PendingTaskUniquePerProduct proves migration 0009
// (0009_assembly_task_unique.up.sql) enforces at most one PENDING assembly_task
// per product via the partial unique index:
//
//	CREATE UNIQUE INDEX idx_assembly_tasks_product_pending
//	  ON wms_ops.assembly_tasks (product_id) WHERE status = 'PENDING';
//
// Issue #50 (Assembly-2): without this index two concurrent allocate calls (or a
// retry) could create duplicate PENDING tasks for the same product, double-booking
// stock. The fix adds the index so the second PENDING insert is rejected.
//
// Test strategy (operates on testEnv.db directly, no HTTP):
//  1. Build an FK-valid fixture (fresh order/product/sku/destination/bin) so the
//     INSERTs satisfy assembly_tasks' NOT NULL + FK columns.
//  2. Insert one PENDING task for the product -> must succeed.
//  3. Insert a SECOND PENDING task for the SAME product, with a DISTINCT event_id
//     -> must fail with a unique violation (SQLSTATE 23505) attributed to the
//     partial index idx_assembly_tasks_product_pending.
//
// The distinct event_id is essential: assembly_tasks also has
// assembly_tasks_event_id_unique UNIQUE(event_id), which exists on BOTH dev and
// the fix branch. Reusing the event_id would raise 23505 on dev too, destroying
// the RED. With distinct event_ids the only thing that can reject the second row
// is the product_id partial index — present only on the fix branch. Asserting the
// violation's ConstraintName == "idx_assembly_tasks_product_pending" further pins
// the failure to the right index rather than any 23505.
//
// Expected RED on dev: migration 0009 not applied, the partial index is absent.
// The second insert (distinct event_id, only the non-unique idx_assembly_tasks_product_id
// covers product_id) SUCCEEDS -> require.Error fails -> RED.
//
// Expected GREEN on fix: the partial index exists -> the second PENDING insert
// raises 23505 with ConstraintName "idx_assembly_tasks_product_pending" -> PASS.
func TestAssembly_PendingTaskUniquePerProduct(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	// FK-valid fixture: one fresh STORED product of a per-run SKU, plus its order,
	// destination, and a real shipping-buffer bin (used here only to satisfy the
	// from_bin_id FK). storedCount=1, orderQty=1.
	fx := newMultiProductFixture(t, ctx, env, "SHOP-7", 1, 1)
	require.Len(t, fx.ProductIDs, 1)
	productID := fx.ProductIDs[0]

	insertPendingTask := func(eventID uuid.UUID) error {
		_, err := env.db.Exec(ctx, `
			INSERT INTO wms_ops.assembly_tasks
				(event_id, order_id, product_id, sku_id, from_bin_id, destination_id, status, occurred_at)
			VALUES ($1, $2, $3, $4, $5, $6, 'PENDING', now())`,
			eventID, fx.OrderID, productID, fx.SKUID, fx.ShippingBinID, fx.DestinationID)
		return err
	}

	// Clean up both inserted tasks regardless of which assertion path runs (the
	// fixture's own cleanup also deletes assembly_tasks by product_id, so this is
	// belt-and-suspenders for the rows this test inserts directly).
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if _, err := env.db.Exec(cleanupCtx,
			`DELETE FROM wms_ops.assembly_tasks WHERE product_id = $1`, productID); err != nil {
			t.Logf("cleanup assembly_tasks for product %s: %v", productID, err)
		}
	})

	// 1. First PENDING task for the product -> succeeds.
	require.NoError(t, insertPendingTask(uuid.New()), "first PENDING task insert should succeed")

	// 2. Second PENDING task, SAME product, DISTINCT event_id -> unique violation.
	err := insertPendingTask(uuid.New())
	require.Error(t, err, "second PENDING task for same product must be rejected by the partial unique index")

	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "error should be a *pgconn.PgError, got %T: %v", err, err)
	require.Equal(t, "23505", pgErr.Code, "expected unique_violation (23505)")
	require.Equal(t, "idx_assembly_tasks_product_pending", pgErr.ConstraintName,
		"violation must come from the product-pending partial unique index, not assembly_tasks_event_id_unique")
}
