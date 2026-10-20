//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ── audit CLI runner ─────────────────────────────────────────────────────────

var (
	auditBinOnce sync.Once
	auditBinPath string
	auditBinErr  error
)

// buildAuditCLI compiles wms/cmd/audit once per test binary and returns the binary path.
func buildAuditCLI(t *testing.T) string {
	t.Helper()
	auditBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "audit-cli-")
		if err != nil {
			auditBinErr = fmt.Errorf("temp dir: %w", err)
			return
		}
		auditBinPath = filepath.Join(dir, "audit")
		cmd := exec.Command("go", "build", "-o", auditBinPath, "./cmd/audit")
		cmd.Dir = filepath.Join(testEnv.repoRoot, "wms")
		if out, err := cmd.CombinedOutput(); err != nil {
			auditBinErr = fmt.Errorf("build audit CLI: %w: %s", err, out)
		}
	})
	require.NoError(t, auditBinErr, "build audit CLI")
	return auditBinPath
}

// runAudit execs the audit CLI with --json and the stack's env and returns
// stdout; exit codes other than 0 (clean) and 1 (findings) fail the test.
func runAudit(t *testing.T, ctx context.Context, env *env, args ...string) ([]byte, int) {
	t.Helper()
	registryAddr, err := readSharedStateFile(ctx, "registry_addr.txt")
	require.NoError(t, err)
	dbu, err := url.Parse(env.dbURL)
	require.NoError(t, err)
	password, _ := dbu.User.Password()

	cmdEnv := append(os.Environ(),
		"RPC_URL="+env.rpcURL,
		"CONTRACT_ADDR="+env.contractAddr,
		"REGISTRY_ADDR="+registryAddr,
		"DB_HOST="+dbu.Hostname(),
		"DB_PORT="+dbu.Port(),
		"POSTGRES_USER="+dbu.User.Username(),
		"POSTGRES_PASSWORD="+password,
		"POSTGRES_DB="+strings.TrimPrefix(dbu.Path, "/"),
	)

	cmd := exec.CommandContext(ctx, buildAuditCLI(t), append([]string{"--json"}, args...)...)
	cmd.Env = cmdEnv
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run audit %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	if code != 0 && code != 1 {
		t.Fatalf("audit %v exited %d, stderr: %s", args, code, stderr.String())
	}
	return stdout.Bytes(), code
}

// ── CLI JSON shapes (only the asserted fields) ───────────────────────────────

type auditFindingDTO struct {
	ActionID string `json:"actionId"`
	Kind     string `json:"kind"`
	Detail   string `json:"detail"`
}

type auditStageDTO struct {
	ActionID     string            `json:"actionId"`
	ActionType   uint8             `json:"actionType"`
	Status       string            `json:"status"`
	OperatorID   string            `json:"operatorId"`
	RejectReason uint8             `json:"rejectReason"`
	DetailsHash  string            `json:"detailsHash"`
	Items        string            `json:"items"`
	Signer       string            `json:"signer"`
	Signature    string            `json:"signature"`
	Findings     []auditFindingDTO `json:"findings"`
}

type auditReportDTO struct {
	ProductID string          `json:"productId"`
	Stages    []auditStageDTO `json:"stages"`
}

type auditFindingsDTO struct {
	Findings []auditFindingDTO `json:"findings"`
}

func parseAuditReport(t *testing.T, stdout []byte) auditReportDTO {
	t.Helper()
	var report auditReportDTO
	require.NoError(t, json.Unmarshal(stdout, &report), "audit product JSON: %s", stdout)
	return report
}

func parseAuditFindings(t *testing.T, stdout []byte) []auditFindingDTO {
	t.Helper()
	var dto auditFindingsDTO
	require.NoError(t, json.Unmarshal(stdout, &dto), "audit all JSON: %s", stdout)
	return dto.Findings
}

func containsFinding(findings []auditFindingDTO, actionID uuid.UUID, kind string) bool {
	for _, f := range findings {
		if f.ActionID == actionID.String() && f.Kind == kind {
			return true
		}
	}
	return false
}

// ── shared fixture: signed receive + signed putaway, both committed ─────────

type auditFixture struct {
	op              *signedOperator
	productIDs      []uuid.UUID
	receiveActionID uuid.UUID
	putawayActionID uuid.UUID
	storageBinID    uuid.UUID
}

// signedPutawayFixture drives the signed receive of 2 products and the signed
// putaway of the whole basket into the scaffold storage bin, waits until both
// actions are COMMITTED on chain and resolves both action ids from the DB.
func signedPutawayFixture(t *testing.T, ctx context.Context, env *env) auditFixture {
	t.Helper()
	const productCount = 2
	op := newSignedOperator(t, ctx, env)
	sc := setupReceivingScaffold(t, ctx, env, productCount)
	run := receiveAndCloseSigned(t, ctx, env, op, sc, productCount)
	require.Equal(t, "TABLE_CLOSED", run.result.Status)
	for _, pid := range run.productIDs {
		waitForOnchainCommitted(t, ctx, env, eventIDForAggregate(t, ctx, env, pid, "receiving"), "receiving")
	}

	require.Equal(t, productCount, placeToStorageBinSigned(t, env, op, run.productIDs, sc.StorageBinID).ProductsPlaced)

	fx := auditFixture{op: op, productIDs: run.productIDs, storageBinID: sc.StorageBinID}
	var err error
	err = env.db.QueryRow(ctx, `
		SELECT action_id FROM public.warehouse_actions
		WHERE action_type = 2 AND item_ids @> ARRAY[$1::uuid]`, fx.productIDs[0]).Scan(&fx.putawayActionID)
	require.NoError(t, err, "putaway action id")
	waitForOnchainCommitted(t, ctx, env, fx.putawayActionID, "putaway")

	err = env.db.QueryRow(ctx, `
		SELECT action_id FROM public.warehouse_actions
		WHERE action_type = 1 AND item_ids @> ARRAY[$1::uuid]`, fx.productIDs[0]).Scan(&fx.receiveActionID)
	require.NoError(t, err, "receive action id")
	return fx
}

// auditStagesByID indexes the report stages by action id.
func auditStagesByID(t *testing.T, stdout []byte) map[string]auditStageDTO {
	t.Helper()
	report := parseAuditReport(t, stdout)
	stages := make(map[string]auditStageDTO, len(report.Stages))
	for _, s := range report.Stages {
		stages[s.ActionID] = s
	}
	return stages
}

// requireAppliedOK asserts one stage is APPLIED with all four checks OK.
func requireAppliedOK(t *testing.T, s auditStageDTO, name string) {
	t.Helper()
	require.Equalf(t, "APPLIED", s.Status, "%s status", name)
	require.Equalf(t, "OK", s.DetailsHash, "%s detailsHash", name)
	require.Equalf(t, "OK", s.Items, "%s items", name)
	require.Equalf(t, "OK", s.Signer, "%s signer", name)
	require.Equalf(t, "OK", s.Signature, "%s signature", name)
}

// ── tests ────────────────────────────────────────────────────────────────────

// TestAudit_SignedProductMatches runs the CLI against a freshly signed product:
// both stages APPLIED, every check OK, operator matches the signing user.
func TestAudit_SignedProductMatches(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	env := testEnv
	require.NotNil(t, env)

	fx := signedPutawayFixture(t, ctx, env)
	stdout, code := runAudit(t, ctx, env, "product", fx.productIDs[0].String())
	require.Equal(t, 0, code, "clean product must exit 0")

	stages := auditStagesByID(t, stdout)
	rec, ok := stages[fx.receiveActionID.String()]
	require.True(t, ok, "no receive stage in %s", stdout)
	pa, ok := stages[fx.putawayActionID.String()]
	require.True(t, ok, "no putaway stage in %s", stdout)
	requireAppliedOK(t, rec, "receive")
	requireAppliedOK(t, pa, "putaway")
	require.Equal(t, fx.op.userID.String(), rec.OperatorID)
	require.Equal(t, fx.op.userID.String(), pa.OperatorID)
}

// TestAudit_BinChangedInDB_DetailsHashMismatch tampers the putaways bin in the
// DB: the CLI catches the detailsHash mismatch for the product and in `all`.
func TestAudit_BinChangedInDB_DetailsHashMismatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	env := testEnv
	require.NotNil(t, env)

	fx := signedPutawayFixture(t, ctx, env)
	var otherBin uuid.UUID
	err := env.db.QueryRow(ctx, `
		SELECT bin_id FROM wms_inventory.bins WHERE bin_id <> $1 LIMIT 1`, fx.storageBinID).Scan(&otherBin)
	require.NoError(t, err)
	mustExec(t, ctx, env, `UPDATE wms_ops.putaways SET bin_id = $1 WHERE action_id = $2`, otherBin, fx.putawayActionID)

	stdout, code := runAudit(t, ctx, env, "product", fx.productIDs[0].String())
	require.Equal(t, 1, code, "forged bin must exit 1")
	stages := auditStagesByID(t, stdout)
	pa, ok := stages[fx.putawayActionID.String()]
	require.True(t, ok, "no putaway stage in %s", stdout)
	require.Equal(t, "MISMATCH", pa.DetailsHash, "putaway detailsHash check")
	require.True(t, containsFinding(pa.Findings, fx.putawayActionID, "DETAILS_HASH_MISMATCH"), "putaway findings %v", pa.Findings)
	rec, ok := stages[fx.receiveActionID.String()]
	require.True(t, ok, "no receive stage in %s", stdout)
	require.Equal(t, "OK", rec.DetailsHash, "receive stage must stay OK")

	stdout, code = runAudit(t, ctx, env, "all")
	require.Equal(t, 1, code, "all must exit 1 with findings")
	require.True(t, containsFinding(parseAuditFindings(t, stdout), fx.putawayActionID, "DETAILS_HASH_MISMATCH"),
		"all findings must contain the tampered putaway")
}

// TestAudit_ActionRowDeleted_MissingInDB deletes the putaway action row (and
// unlinks the domain row): the chain-only action is reported MISSING_IN_DB.
func TestAudit_ActionRowDeleted_MissingInDB(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	env := testEnv
	require.NotNil(t, env)

	fx := signedPutawayFixture(t, ctx, env)
	mustExec(t, ctx, env, `UPDATE wms_ops.putaways SET action_id = NULL WHERE action_id = $1`, fx.putawayActionID)
	mustExec(t, ctx, env, `DELETE FROM public.warehouse_actions WHERE action_id = $1`, fx.putawayActionID)

	stdout, code := runAudit(t, ctx, env, "product", fx.productIDs[0].String())
	require.Equal(t, 1, code, "deleted action row must exit 1")
	stages := auditStagesByID(t, stdout)
	pa, ok := stages[fx.putawayActionID.String()]
	require.True(t, ok, "no putaway stage in %s", stdout)
	require.Equal(t, "MISSING_IN_DB", pa.Status)
	require.True(t, containsFinding(pa.Findings, fx.putawayActionID, "MISSING_IN_DB"), "findings %v", pa.Findings)
}

// TestAudit_BackdatedRow_MissingOnChain inserts a backdated copy of the putaway
// row with no onchain event: the CLI reports MISSING_ON_CHAIN for the fake id.
func TestAudit_BackdatedRow_MissingOnChain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	env := testEnv
	require.NotNil(t, env)

	fx := signedPutawayFixture(t, ctx, env)
	newActionID := uuid.New()
	newOperationID := uuid.New()
	mustExec(t, ctx, env, `
		INSERT INTO public.warehouse_actions
			(action_id, operation_id, part_index, action_type, signer_credential_id, created_by, item_ids,
			 salt, details_hash, challenge, details, created_at, submitted_at)
		SELECT $2, $3, 0, action_type, signer_credential_id, created_by, item_ids,
		       salt, details_hash, challenge, details, now() - interval '1 day', now() - interval '1 day'
		FROM public.warehouse_actions WHERE action_id = $1`, fx.putawayActionID, newActionID, newOperationID)
	t.Cleanup(func() {
		_, _ = env.db.Exec(context.Background(), `DELETE FROM public.warehouse_actions WHERE action_id = $1`, newActionID)
	})

	stdout, code := runAudit(t, ctx, env, "product", fx.productIDs[0].String())
	require.Equal(t, 1, code, "backdated row must exit 1")
	stages := auditStagesByID(t, stdout)
	fake, ok := stages[newActionID.String()]
	require.True(t, ok, "no stage for the backdated id in %s", stdout)
	require.Equal(t, "MISSING_ON_CHAIN", fake.Status)
	require.True(t, containsFinding(fake.Findings, newActionID, "MISSING_ON_CHAIN"), "findings %v", fake.Findings)
}
