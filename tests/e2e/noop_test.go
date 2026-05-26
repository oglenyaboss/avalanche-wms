//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestNoop_AllocateNothing allocates a freshly-created destination that has no
// orders at all, so allocation is a clean no-op: zero orders, zero products.
func TestNoop_AllocateNothing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)

	suf := shortSuffix()
	code := "SHOP-NOOP-" + suf
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.destinations(destination_id,code,name,address,warehouse_id,created_at,updated_at)
		SELECT gen_random_uuid(),$1,$2,'NOOP test',w.warehouse_id,now(),now()
		FROM wms_inventory.warehouses w WHERE w.name=$3`,
		code, "Noop destination "+suf, warehouseName)

	var destinationID uuid.UUID
	err := env.db.QueryRow(ctx, `
		SELECT destination_id FROM wms_inventory.destinations WHERE code = $1`, code).Scan(&destinationID)
	require.NoError(t, err)

	t.Cleanup(func() {
		if _, err := env.db.Exec(context.Background(),
			`DELETE FROM wms_inventory.destinations WHERE code = $1`, code); err != nil {
			t.Logf("noop cleanup: %v", err)
		}
	})

	var allocated allocateData
	postJSON(t, env, token, "/assembly/allocate", map[string]string{
		"destination_id": destinationID.String(),
	}, &allocated)
	require.Equal(t, 0, allocated.AllocatedOrders)
	require.Equal(t, 0, allocated.AllocatedProducts)
}
