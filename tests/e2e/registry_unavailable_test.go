//go:build e2e

package e2e

import (
	"context"
	"encoding/hex"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ── adapter stop/restore (registry-unavailable window) ──────────────────────

// stopAdapterForTest stops the ledger-adapter container (the registry reader)
// and registers an idempotent t.Cleanup that brings it back and waits for
// health + consumer readiness, so later tests always see a working adapter.
// The returned restore func is safe to call from the test body as well; the
// cleanup then becomes a no-op.
func stopAdapterForTest(t *testing.T, env *env) (restore func()) {
	t.Helper()
	var once sync.Once
	restore = func() { once.Do(func() { restoreAdapterNow(t, env) }) }
	t.Cleanup(restore)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer stopCancel()
	out, err := exec.CommandContext(stopCtx, "docker", "stop", adapterContainer).CombinedOutput()
	require.NoErrorf(t, err, "docker stop %s: %s", adapterContainer, strings.TrimSpace(string(out)))
	return restore
}

// restoreAdapterNow starts the stopped adapter container and blocks until it is
// healthy and its Kafka consumer is reading again (the same readiness waits the
// recreate path uses). Uses a fresh context: t.Cleanup runs after the test's
// own context is already cancelled.
func restoreAdapterNow(t *testing.T, env *env) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	switch status := adapterContainerStatus(ctx); status {
	case "running":
		// already back: a prior restore attempt half-succeeded
	case "":
		t.Fatalf("cannot read %s state from docker inspect", adapterContainer)
	default:
		out, err := exec.CommandContext(ctx, "docker", "start", adapterContainer).CombinedOutput()
		require.NoErrorf(t, err, "docker start %s: %s", adapterContainer, strings.TrimSpace(string(out)))
	}
	require.Truef(t, waitAdapterHealthy(ctx, 90*time.Second),
		"ledger-adapter did not become healthy on :8085 after restore")
	waitAdapterConsuming(t, ctx, env)
}

// adapterContainerStatus returns the docker container state ("running",
// "exited", …) or "" when the container cannot be inspected.
func adapterContainerStatus(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Status}}", adapterContainer).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ── the test ─────────────────────────────────────────────────────────────────

// TestSigned_RevokedKeyRegistryDown_SubmitRejected proves #55 end to end: an
// operator key revoked in the registry by the owner (registry-cli, direct RPC)
// cannot sign while the adapter is down — the submit gets 503
// REGISTRY_UNAVAILABLE and nothing moves in the DB or on chain; once the
// adapter is back, the same submit gets 409 SIGNER_KEY_NOT_ACTIVE because the
// registry now answers and says the key is inactive.
func TestSigned_RevokedKeyRegistryDown_SubmitRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	env := testEnv
	require.NotNil(t, env)

	// 1. Fresh operator (never shared with other tests) and a basket ready for
	// a signed putaway: signed receive → cargoplace closed → products RECEIVED.
	op := newSignedOperator(t, ctx, env)
	sc := setupReceivingScaffold(t, ctx, env, 2)
	run := receiveAndCloseSigned(t, ctx, env, op, sc, 2)
	require.Equal(t, "TABLE_CLOSED", run.result.Status)
	for _, pid := range run.productIDs {
		waitForOnchainCommitted(t, ctx, env, eventIDForAggregate(t, ctx, env, pid, "receiving"))
	}

	// 2. Draft the putaway while the stack is healthy; keep the operation id
	// and the part's action id for the DB assertions.
	draft := draftPlaceToStorageBin(t, env, op, run.productIDs, sc.StorageBinID)
	require.Len(t, draft.Parts, 1)
	actionID, err := uuid.Parse(draft.Parts[0].ActionID)
	require.NoError(t, err)
	operationID, err := uuid.Parse(draft.OperationID)
	require.NoError(t, err)

	// Snapshot of every product before the rejected submit: DB status + bin and
	// the on-chain item status.
	type productSnapshot struct {
		status string
		binID  *uuid.UUID
		chain  uint8
	}
	snapshots := make(map[uuid.UUID]productSnapshot, len(run.productIDs))
	for _, pid := range run.productIDs {
		var snap productSnapshot
		err := env.db.QueryRow(ctx, `
			SELECT status::text, bin_id
			FROM wms_inventory.products
			WHERE product_id = $1`, pid).Scan(&snap.status, &snap.binID)
		require.NoError(t, err, "product snapshot %s", pid)
		snap.chain = callItemStatus(t, ctx, env, pid)
		snapshots[pid] = snap
	}

	// 3. Revoke the operator key: registry-cli signs the owner tx directly
	// against the RPC (not via the adapter) — done before the adapter is stopped.
	keyID := "0x" + hex.EncodeToString(op.keyID[:])
	stdout, stderr, code := runRegistryCLI(t, ctx, "revoke-key", "--key-id", keyID)
	require.Equal(t, 0, code, "revoke-key must exit 0, stderr: %s", stderr)
	require.Contains(t, stdout, "отозван", "revoke-key stdout %q", stdout)

	registryHex, err := readSharedStateFile(ctx, "registry_addr.txt")
	require.NoError(t, err)
	require.False(t, callRegistryKey(t, ctx, env, registryHex, op.keyID).Active,
		"operator key must be inactive in the registry after the CLI revoke")

	// 4. Stop the adapter; the cleanup brings it back even on failure.
	restore := stopAdapterForTest(t, env)

	// 5. Submit the signed draft → 503 REGISTRY_UNAVAILABLE.
	assertions := signDraft(t, op, draft)
	status, errCode, body := submitPlaceToStorageBinRaw(t, env, op.token, draft.OperationID, assertions)
	require.Equalf(t, http.StatusServiceUnavailable, status, "body: %s", body)
	require.Equal(t, "REGISTRY_UNAVAILABLE", errCode)

	// 6. DB: the draft rows are still unsubmitted, no outbox row for the part's
	// action id, products did not move.
	require.Equal(t, 0, mustCount(t, ctx, env,
		`SELECT count(*) FROM public.warehouse_actions WHERE operation_id = $1 AND submitted_at IS NOT NULL`, operationID),
		"a rejected submit must not mark any part submitted")
	require.Equal(t, 1, mustCount(t, ctx, env,
		`SELECT count(*) FROM public.warehouse_actions WHERE operation_id = $1`, operationID),
		"the drafted part must still exist")
	require.Equal(t, 0, outboxCountForEvent(t, ctx, env, actionID),
		"a rejected submit must not emit its signed action")
	for pid, snap := range snapshots {
		var statusNow string
		var binNow *uuid.UUID
		err := env.db.QueryRow(ctx, `
			SELECT status::text, bin_id
			FROM wms_inventory.products
			WHERE product_id = $1`, pid).Scan(&statusNow, &binNow)
		require.NoError(t, err, "product re-read %s", pid)
		require.Equalf(t, snap.status, statusNow, "product %s status must not change on a rejected submit", pid)
		require.Equalf(t, snap.binID, binNow, "product %s bin must not change on a rejected submit", pid)
	}

	// 7. Bring the adapter back (cleanup stays idempotent) and prove the chain
	// state did not move either; then the same submit fails with 409: the
	// registry now answers and says the key is revoked.
	restore()
	for pid, snap := range snapshots {
		require.Equalf(t, snap.chain, callItemStatus(t, ctx, env, pid),
			"on-chain item status of %s must be unchanged", pid)
	}
	status, errCode, body = submitPlaceToStorageBinRaw(t, env, op.token, draft.OperationID, assertions)
	require.Equalf(t, http.StatusConflict, status, "body: %s", body)
	require.Equal(t, "SIGNER_KEY_NOT_ACTIVE", errCode)
}

// mustCount runs a scalar count query and requires success.
func mustCount(t *testing.T, ctx context.Context, env *env, query string, args ...any) int {
	t.Helper()
	var count int
	err := env.db.QueryRow(ctx, query, args...).Scan(&count)
	require.NoError(t, err)
	return count
}
