//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ── registry CLI runner ──────────────────────────────────────────────────────

var (
	registryCLIOnce sync.Once
	registryCLIPath string
	registryCLIErr  error
)

// buildRegistryCLI compiles ledger-adapter/cmd/registry-cli once per test
// binary and returns the binary path.
func buildRegistryCLI(t *testing.T) string {
	t.Helper()
	registryCLIOnce.Do(func() {
		dir, err := os.MkdirTemp("", "registry-cli-")
		if err != nil {
			registryCLIErr = fmt.Errorf("temp dir: %w", err)
			return
		}
		registryCLIPath = filepath.Join(dir, "registry-cli")
		cmd := exec.Command("go", "build", "-o", registryCLIPath, "./cmd/registry-cli")
		cmd.Dir = filepath.Join(testEnv.repoRoot, "ledger-adapter")
		if out, err := cmd.CombinedOutput(); err != nil {
			registryCLIErr = fmt.Errorf("build registry CLI: %w: %s", err, out)
		}
	})
	require.NoError(t, registryCLIErr, "build registry CLI")
	return registryCLIPath
}

// runRegistryCLI execs the registry-cli binary with the stack's env (RPC_URL,
// REGISTRY_ADDR, OWNER_KEY from the shared state volume) and returns stdout,
// stderr and the exit code. The env carries the owner key, so it is never
// included in failure messages.
func runRegistryCLI(t *testing.T, ctx context.Context, args ...string) (string, string, int) {
	t.Helper()
	registryAddr, err := readSharedStateFile(ctx, "registry_addr.txt")
	require.NoError(t, err)
	ownerKey, err := readSharedStateFile(ctx, "deployer_key.txt")
	require.NoError(t, err)

	cmd := exec.CommandContext(ctx, buildRegistryCLI(t), args...)
	cmd.Env = append(os.Environ(),
		"RPC_URL="+testEnv.rpcURL,
		"REGISTRY_ADDR="+registryAddr,
		"OWNER_KEY="+ownerKey,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run registry-cli %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

// orgFileDTO is the registry-cli org.json format (org.go orgFile).
type orgFileDTO struct {
	OrgID string `json:"orgId"`
	Name  string `json:"name"`
	QX    string `json:"qx"`
	QY    string `json:"qy"`
}

// writeOrgFile writes the org.json for orgID/name/key into a fresh temp file
// and returns its path.
func writeOrgFile(t *testing.T, orgID uuid.UUID, name string, k *ecdsa.PrivateKey) string {
	t.Helper()
	qx, qy := softCoordinates(k)
	f, err := os.CreateTemp(t.TempDir(), "org-*.json")
	require.NoError(t, err)
	path := f.Name()
	data, err := json.Marshal(orgFileDTO{
		OrgID: orgID.String(),
		Name:  name,
		QX:    "0x" + hex.EncodeToString(qx[:]),
		QY:    "0x" + hex.EncodeToString(qy[:]),
	})
	require.NoError(t, err)
	_, err = f.Write(data)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return path
}

// ── tests ────────────────────────────────────────────────────────────────────

// TestRegistryCLI_LiveSmoke runs the real registry-cli against the live local
// chain: register-org (+ duplicate rejected), add-admin-key (+ unknown org
// rejected) and list showing the org with both admin keys active.
func TestRegistryCLI_LiveSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	require.NotNil(t, testEnv)

	orgID := uuid.New()
	name := "cli-smoke-" + orgID.String()[:8]
	key1, key2, key3 := newSoftKey(), newSoftKey(), newSoftKey()
	qx1, qy1 := softCoordinates(key1)
	qx2, qy2 := softCoordinates(key2)
	keyID1 := crypto.Keccak256Hash(qx1[:], qy1[:]).Hex()
	keyID2 := crypto.Keccak256Hash(qx2[:], qy2[:]).Hex()

	// S1: no args → usage error, exit code 2.
	_, _, code := runRegistryCLI(t, ctx)
	require.Equal(t, 2, code, "no args must exit 2")

	// S2: register-org succeeds and reports the committed tx hash.
	orgFile1 := writeOrgFile(t, orgID, name, key1)
	stdout, stderr, code := runRegistryCLI(t, ctx, "register-org", "--file", orgFile1)
	require.Equal(t, 0, code, "register-org must exit 0, stderr: %s", stderr)
	require.True(t,
		strings.HasPrefix(stdout, fmt.Sprintf("организация %s «%s» зарегистрирована, ключ админа %s, tx ", orgID, name, keyID1)),
		"register-org stdout %q", stdout)
	require.Regexp(t, `tx 0x[0-9a-f]{64}\n$`, stdout, "register-org must report the tx hash")

	// S3: the same register-org again → precheck failure, no second tx.
	_, stderr, code = runRegistryCLI(t, ctx, "register-org", "--file", orgFile1)
	require.Equal(t, 1, code, "duplicate register-org must exit 1")
	require.Contains(t, stderr, "registry-cli:", "duplicate register-org stderr %q", stderr)

	// S4: add-admin-key for the same org with a second key succeeds.
	orgFile2 := writeOrgFile(t, orgID, name, key2)
	stdout, stderr, code = runRegistryCLI(t, ctx, "add-admin-key", "--file", orgFile2)
	require.Equal(t, 0, code, "add-admin-key must exit 0, stderr: %s", stderr)
	require.True(t,
		strings.HasPrefix(stdout, fmt.Sprintf("ключ админа %s добавлен в организацию %s, tx ", keyID2, orgID)),
		"add-admin-key stdout %q", stdout)

	// S5: add-admin-key for an unknown org → precheck failure.
	unknownID := uuid.New()
	orgFile3 := writeOrgFile(t, unknownID, "cli-smoke-"+unknownID.String()[:8], key3)
	_, stderr, code = runRegistryCLI(t, ctx, "add-admin-key", "--file", orgFile3)
	require.Equal(t, 1, code, "add-admin-key for unknown org must exit 1, stderr: %s", stderr)

	// S6: list shows our org and both admin keys active.
	stdout, stderr, code = runRegistryCLI(t, ctx, "list")
	require.Equal(t, 0, code, "list must exit 0, stderr: %s", stderr)
	require.Contains(t, stdout, orgID.String()+"  "+name+"\n", "list must show the org")
	for _, keyID := range []string{keyID1, keyID2} {
		require.Regexp(t, "(?m)^  "+regexp.QuoteMeta(keyID)+`\s+admin\s+активен$`,
			stdout, "list must show key %s active", keyID)
	}
}
