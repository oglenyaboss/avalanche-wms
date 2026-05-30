//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ── onchain_events DB helpers (psql_q equivalents over testEnv.db) ─────────────

// onchainStatus returns the onchain_events.status for an event id, or "" if no row
// exists yet. Mirrors `psql_q "SELECT status::text ... WHERE event_id=..."`.
func onchainStatus(t *testing.T, ctx context.Context, env *env, eventID uuid.UUID) string {
	t.Helper()
	var status string
	err := env.db.QueryRow(ctx,
		`SELECT status::text FROM public.onchain_events WHERE event_id = $1`, eventID).Scan(&status)
	if err != nil {
		return ""
	}
	return status
}

// onchainTxHash returns the onchain_events.tx_hash for an event id ("" if NULL/absent).
func onchainTxHash(t *testing.T, ctx context.Context, env *env, eventID uuid.UUID) string {
	t.Helper()
	var tx *string
	err := env.db.QueryRow(ctx,
		`SELECT tx_hash FROM public.onchain_events WHERE event_id = $1`, eventID).Scan(&tx)
	if err != nil || tx == nil {
		return ""
	}
	return *tx
}

// setOnchainStatus forces the onchain_events.status for an event id. Used to inject
// the S2 crash-recovery window (a non-terminal SENT/PENDING row whose Kafka offset
// was never committed). Mirrors `psql_q "UPDATE public.onchain_events SET status=..."`.
func setOnchainStatus(t *testing.T, ctx context.Context, env *env, eventID uuid.UUID, status string) {
	t.Helper()
	tag, err := env.db.Exec(ctx,
		`UPDATE public.onchain_events SET status = $2, updated_at = now() WHERE event_id = $1`,
		eventID, status)
	require.NoErrorf(t, err, "force status=%s for event %s", status, eventID)
	require.EqualValuesf(t, 1, tag.RowsAffected(),
		"expected exactly one onchain_events row for %s when forcing status=%s", eventID, status)
}

// distinctTxHashCount returns the number of distinct non-null tx_hash values across
// the given event ids. The S3/N9 co-batch guard: the events must share ONE batch tx
// (count == 1) or the test proved nothing about poison isolation / dedup.
func distinctTxHashCount(t *testing.T, ctx context.Context, env *env, eventIDs ...uuid.UUID) int {
	t.Helper()
	ids := make([]string, len(eventIDs))
	for i, id := range eventIDs {
		ids[i] = id.String()
	}
	var n int
	err := env.db.QueryRow(ctx, `
		SELECT COUNT(DISTINCT tx_hash)
		FROM public.onchain_events
		WHERE event_id = ANY($1) AND tx_hash IS NOT NULL AND tx_hash <> ''`, ids).Scan(&n)
	require.NoError(t, err)
	return n
}

// ── polling helpers (wait_for.sh equivalents) ─────────────────────────────────

// waitOnchainStatus blocks until the onchain_events row for eventID reaches the
// expected status, polling every 2s up to timeout. Returns true on success, false on
// timeout (so callers can branch into a richer failure message, like the bash). It
// does NOT fail the test itself. Mirrors wait_for_status.
func waitOnchainStatus(t *testing.T, ctx context.Context, env *env, eventID uuid.UUID, expected string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if onchainStatus(t, ctx, env, eventID) == expected {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(2 * time.Second)
	}
}

// waitOnchainRowExists blocks until ANY onchain_events row exists for eventID (any
// status), polling every 2s up to timeout. The row is written by InsertPending at the
// very start of flushSubBatch — BEFORE any chain call — so its mere existence proves the
// adapter's Kafka consumer pulled the message and started flushing. This is the
// group-membership signal the consumer-readiness probe needs (see waitAdapterConsuming);
// it deliberately does NOT require a terminal status, because under N1's
// RECEIPT_POLL_TIMEOUT=1ms a warmup event lands FAILED — still a row, still proof.
func waitOnchainRowExists(t *testing.T, ctx context.Context, env *env, eventID uuid.UUID, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if onchainStatus(t, ctx, env, eventID) != "" {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(2 * time.Second)
	}
}

// waitItemStatus blocks until the on-chain itemStatus enum for productID equals
// expected, polling every 2s up to timeout. Returns true on success, false on
// timeout. Mirrors wait_for_item_status; reuses the existing callItemStatus reader.
func waitItemStatus(t *testing.T, ctx context.Context, env *env, productID uuid.UUID, expected uint8, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if callItemStatus(t, ctx, env, productID) == expected {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(2 * time.Second)
	}
}

// ── adapter recreate / restore (env.sh recreate_adapter + wait_adapter) ────────

const adapterHealthURL = "http://localhost:8085/health"
const adapterContainer = "ledger-adapter"

// adapterNetwork / adapterSharedVolume / adapterPortMap mirror the compose service
// definition (docker-compose.yaml ledger-adapter + env.sh). The recreate re-creates
// the container by hand against the EXISTING project network rather than via
// `docker compose up`, which reconciles (and tries to recreate) a network that
// testcontainers/compose-go created with different labels — that reconcile fails on
// the sibling endpoints (kafka/wms/...) and leaves the adapter stopped+detached.
const adapterNetwork = e2eProjectName + "_app_network"
const adapterSharedVolume = e2eProjectName + "_shared_state"
const adapterPortMap = "8085:8085"

// adapterTimingVars are the four env knobs the scenarios tune. They are STRIPPED from
// the inherited env on every recreate and re-applied explicitly (override value, or
// the compose default below) so a prior scenario's value never leaks into a later
// recreate — most importantly the restore-to-default path.
var adapterTimingDefaults = map[string]string{
	"BATCH_TIMEOUT":        "100ms",
	"RECEIPT_POLL_TIMEOUT": "30s",
	"RECONCILE_INTERVAL":   "30s",
	"RECONCILE_MIN_AGE":    "1m",
}

// recreateAdapterWithEnv re-creates ONLY the ledger-adapter container with the given
// timing-env overrides (BATCH_TIMEOUT / RECEIPT_POLL_TIMEOUT / RECONCILE_INTERVAL /
// RECONCILE_MIN_AGE). A nil/empty overrides map restores the compose defaults. It
// inherits the running adapter's stable env (DB_URL, RPC_URL_FILE, KAFKA_BROKERS, …)
// and image via `docker inspect`, then `docker rm -f` + `docker run` attached to the
// existing project network. Blocks until /health is ready. Mirrors env.sh
// recreate_adapter + wait_adapter, but network-safe under testcontainers.
//
// CONSTRAINT: the adapter validates RECONCILE_MIN_AGE > RECEIPT_POLL_TIMEOUT at
// startup or it refuses to boot. Callers must never set minAge <= pollTimeout.
func recreateAdapterWithEnv(t *testing.T, ctx context.Context, env *env, overrides map[string]string) {
	t.Helper()

	image, inheritedEnv := inspectAdapter(t, ctx)

	// Build the final env: inherited stable vars (timing vars stripped) + the four
	// timing vars set explicitly (override, else compose default).
	envArgs := make([]string, 0, len(inheritedEnv)+len(adapterTimingDefaults))
	for _, kv := range inheritedEnv {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if _, isTiming := adapterTimingDefaults[name]; isTiming {
			continue // re-added explicitly below
		}
		envArgs = append(envArgs, "-e", kv)
	}
	for name, def := range adapterTimingDefaults {
		val := def
		if ov, ok := overrides[name]; ok {
			val = ov
		}
		envArgs = append(envArgs, "-e", name+"="+val)
	}

	// rm -f the old container, then run a fresh one attached to the project network.
	rm := exec.CommandContext(ctx, "docker", "rm", "-f", adapterContainer)
	if out, err := rm.CombinedOutput(); err != nil {
		t.Logf("docker rm -f %s (continuing): %s", adapterContainer, strings.TrimSpace(string(out)))
	}

	runArgs := []string{
		"run", "-d",
		"--name", adapterContainer,
		"--network", adapterNetwork,
		"-p", adapterPortMap,
		"-v", adapterSharedVolume + ":/shared:ro",
	}
	runArgs = append(runArgs, envArgs...)
	runArgs = append(runArgs, image)

	run := exec.CommandContext(ctx, "docker", runArgs...)
	out, err := run.CombinedOutput()
	require.NoErrorf(t, err, "recreate ledger-adapter (overrides=%v): %s", overrides, string(out))

	require.Truef(t, waitAdapterHealthy(ctx, 90*time.Second),
		"ledger-adapter did not become healthy on :8085 after recreate (overrides=%v)", overrides)

	// /health only proves the HTTP server is up — it returns 200 well before the Kafka
	// consumer rejoins its group after the `docker rm -f` (SIGKILL) above. A SIGKILLed
	// kafka-go member lingers in the broker for the 30s session timeout, so a fresh
	// consumer (especially when two recreates stack) must wait out a rebalance before it
	// is assigned the partition and starts reading. Block until the consumer is actually
	// consuming, so callers never publish into that blind window (the N9 flake).
	waitAdapterConsuming(t, ctx, env)
}

// adapterConsumingTimeout caps the wait for a freshly-recreated adapter's consumer to
// rejoin its group and start reading. Sized for the worst case: a hard-killed member's
// 30s session timeout plus a broker rebalance settle, observed to push the blind window
// past 60s under a loaded single-Kafka e2e host.
const adapterConsumingTimeout = 90 * time.Second

// waitAdapterConsuming proves the recreated adapter's Kafka consumer has joined the
// group and is reading its partition — not merely that /health is 200. It publishes a
// throwaway warmup event with FRESH event_id AND product_id and waits for its
// onchain_events row to appear (any status). Because the row is created by InsertPending
// before any chain call, "row exists" is a clean group-membership signal.
//
// The warmup uses distinct random UUIDs, so it can never collide with a test's
// event_id/product_id-scoped assertions, and it accretes harmlessly: no fixture cleanup
// matches its random product_id (it has no outbox row — it is published straight to
// Kafka), and no assertion counts total onchain_events rows. aggregate_type "receiving"
// keeps it on the valid None->Accepted path (commits cleanly under default config; lands
// FAILED only under N1's 1ms poll — still a row).
func waitAdapterConsuming(t *testing.T, ctx context.Context, env *env) {
	t.Helper()
	warmupEvent := uuid.New()
	warmupProduct := uuid.New()
	publishEvent(t, ctx, "receiving", warmupEvent, warmupProduct, "{}")
	require.Truef(t, waitOnchainRowExists(t, ctx, env, warmupEvent, adapterConsumingTimeout),
		"ledger-adapter consumer did not start processing within %s after recreate: warmup "+
			"event %s never produced an onchain_events row (consumer not in-group / partition unassigned)",
		adapterConsumingTimeout, warmupEvent)
}

// inspectAdapter returns the running adapter's image ref and its full env list
// (KEY=VALUE entries) via `docker inspect`. Used so the recreated container keeps all
// stable compose-injected config (DB_URL, RPC_URL_FILE, KAFKA_BROKERS, DLQ_TOPIC, …).
func inspectAdapter(t *testing.T, ctx context.Context) (image string, envList []string) {
	t.Helper()

	imgCmd := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.Config.Image}}", adapterContainer)
	imgOut, err := imgCmd.CombinedOutput()
	require.NoErrorf(t, err, "inspect adapter image: %s", string(imgOut))
	image = strings.TrimSpace(string(imgOut))
	require.NotEmpty(t, image, "adapter image came back empty from docker inspect")

	// One env var per line. Values may contain '=' (e.g. DB_URL) — only split on key.
	envCmd := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{range .Config.Env}}{{println .}}{{end}}", adapterContainer)
	envOut, err := envCmd.CombinedOutput()
	require.NoErrorf(t, err, "inspect adapter env: %s", string(envOut))
	for _, line := range strings.Split(string(envOut), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		envList = append(envList, line)
	}
	require.NotEmpty(t, envList, "adapter env came back empty from docker inspect")
	return image, envList
}

// waitAdapterHealthy polls the adapter /health until 200 OK or timeout. Mirrors
// wait_adapter.
func waitAdapterHealthy(ctx context.Context, timeout time.Duration) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(timeout)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, adapterHealthURL, nil)
		if err == nil {
			resp, derr := client.Do(req)
			if derr == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return true
				}
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(1 * time.Second)
	}
}

// restoreDefaultAdapter recreates the adapter with NO overrides (compose defaults) so
// later gate tests see the production-config adapter. Wired via t.Cleanup with a fresh
// context (the test's ctx may already be cancelled at cleanup time). Mirrors the
// `trap restore_adapter EXIT` in the bash scenarios.
func restoreDefaultAdapter(t *testing.T, env *env) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	recreateAdapterWithEnv(t, cleanupCtx, env, nil)
}

// txEmittedItemTransitionFailed reports whether the receipt for txHash contains an
// ItemTransitionFailed(uint256,uint256,uint8,uint8) log (topic0 == keccak of the
// signature). This is how S3 proves the poison item was SKIPPED (Design A) rather
// than reverting the whole batch — the on-chain-observable evidence of isolation.
// Mirrors the bash `cast keccak "ItemTransitionFailed(...)"` + receipt grep.
func txEmittedItemTransitionFailed(t *testing.T, ctx context.Context, env *env, txHash string) bool {
	t.Helper()
	client, err := ethclient.DialContext(ctx, env.rpcURL)
	require.NoError(t, err)
	defer client.Close()

	receipt, err := client.TransactionReceipt(ctx, common.HexToHash(txHash))
	require.NoErrorf(t, err, "receipt for batch tx %s", txHash)

	sigHash := crypto.Keccak256Hash([]byte("ItemTransitionFailed(uint256,uint256,uint8,uint8)"))
	for _, lg := range receipt.Logs {
		if len(lg.Topics) > 0 && lg.Topics[0] == sigHash {
			return true
		}
	}
	return false
}
