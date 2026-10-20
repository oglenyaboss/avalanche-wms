//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertCollect is the testify collector used by EventuallyWithT assertions.
type assertCollect = assert.CollectT

// requireProductStatus asserts the persisted status of a product.
func requireProductStatus(t *testing.T, ctx context.Context, env *env, productID uuid.UUID, status string) {
	t.Helper()
	var got string
	err := env.db.QueryRow(ctx, `
		SELECT status::text
		FROM wms_inventory.products
		WHERE product_id = $1`, productID).Scan(&got)
	require.NoError(t, err)
	require.Equal(t, status, got)
}

// requireOrderStatus asserts the persisted status of an order.
func requireOrderStatus(t *testing.T, ctx context.Context, env *env, orderID uuid.UUID, status string) {
	t.Helper()
	var got string
	err := env.db.QueryRow(ctx, `
		SELECT status::text
		FROM wms_inventory.orders
		WHERE order_id = $1`, orderID).Scan(&got)
	require.NoError(t, err)
	require.Equal(t, status, got)
}

// requireDispatchStatus asserts the persisted status of an outbound dispatch.
func requireDispatchStatus(t *testing.T, ctx context.Context, env *env, dispatchID uuid.UUID, status string) {
	t.Helper()
	var got string
	err := env.db.QueryRow(ctx, `
		SELECT status::text
		FROM wms_inventory.outbound_dispatches
		WHERE dispatch_id = $1`, dispatchID).Scan(&got)
	require.NoError(t, err)
	require.Equal(t, status, got)
}

// requireTaskStatus asserts the persisted status of the assembly task for a product.
func requireTaskStatus(t *testing.T, ctx context.Context, env *env, productID uuid.UUID, status string) {
	t.Helper()
	var got string
	err := env.db.QueryRow(ctx, `
		SELECT status::text
		FROM wms_ops.assembly_tasks
		WHERE product_id = $1`, productID).Scan(&got)
	require.NoError(t, err)
	require.Equal(t, status, got)
}

// aggregateActionType maps an aggregate type of a stage to the warehouse action
// type stored in public.warehouse_actions (ADR-0001 §3).
var aggregateActionType = map[string]int16{
	"receiving": 1,
	"putaway":   2,
	"picking":   3,
	"shipping":  4,
}

// outboxCountForAggregate returns the number of submitted signed actions of the
// stage containing the product.
func outboxCountForAggregate(t *testing.T, ctx context.Context, env *env, aggregateID uuid.UUID, aggregateType string) int {
	t.Helper()
	var count int
	err := env.db.QueryRow(ctx, `
		SELECT count(*)
		FROM public.warehouse_actions
		WHERE submitted_at IS NOT NULL
		  AND item_ids @> ARRAY[$1::uuid]
		  AND action_type = $2`, aggregateID, aggregateActionType[aggregateType]).Scan(&count)
	require.NoError(t, err)
	return count
}

// eventIDForAggregate returns the action id of the newest submitted signed action
// of the stage containing the product (ordered by submitted_at DESC).
func eventIDForAggregate(t *testing.T, ctx context.Context, env *env, productID uuid.UUID, aggregateType string) uuid.UUID {
	t.Helper()
	var eventID uuid.UUID
	err := env.db.QueryRow(ctx, `
		SELECT action_id
		FROM public.warehouse_actions
		WHERE submitted_at IS NOT NULL
		  AND item_ids @> ARRAY[$1::uuid]
		  AND action_type = $2
		ORDER BY submitted_at DESC
		LIMIT 1`, productID, aggregateActionType[aggregateType]).Scan(&eventID)
	require.NoError(t, err, "submitted %s action for product %s", aggregateType, productID)
	return eventID
}

// waitForOnchainCommitted blocks until the onchain_events row for an action id
// reaches COMMITTED. The row must be a signed action (aggregate_type
// 'signed_action' — the event id of a signed action is its action_id).
func waitForOnchainCommitted(t *testing.T, ctx context.Context, env *env, eventID uuid.UUID) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assertCollect) {
		var status string
		var dbAggregate string
		err := env.db.QueryRow(ctx, `
			SELECT status::text, aggregate_type
			FROM public.onchain_events
			WHERE event_id = $1`, eventID).Scan(&status, &dbAggregate)
		require.NoError(c, err)
		require.Equal(c, "signed_action", dbAggregate)
		require.Equal(c, "COMMITTED", status)
	}, 90*time.Second, 2*time.Second, "event %s did not become COMMITTED", eventID)
}

// readSharedStateFile reads a file from the e2e compose shared_state volume by
// running a throwaway alpine container with the volume mounted read-only.
func readSharedStateFile(ctx context.Context, name string) (string, error) {
	volume := e2eProjectName + "_shared_state"
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-v", volume+":/shared:ro", "alpine", "cat", "/shared/"+name)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker read shared state %s: %w", name, err)
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return "", fmt.Errorf("shared state %s is empty", name)
	}
	return value, nil
}
