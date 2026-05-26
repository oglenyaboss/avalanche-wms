//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	compose "github.com/testcontainers/testcontainers-go/modules/compose"
	"github.com/testcontainers/testcontainers-go/wait"
)

// e2eProjectName isolates the e2e compose stack from any dev stack sharing the
// repo's default project name. Compose auto-prefixes volumes and networks with
// the project name, so Down(RemoveVolumes(true)) only ever touches
// blockchain_project_e2e_* resources and never wipes a developer's dev stack.
const e2eProjectName = "blockchain_project_e2e"

var testEnv *env

type env struct {
	repoRoot     string
	wmsURL       string
	debeziumURL  string
	dbURL        string
	rpcURL       string
	contractAddr string
	db           *pgxpool.Pool
	httpClient   *http.Client
	stack        compose.ComposeStack
}

// TestMain is the entry point for the e2e test package: it prepares the test environment, runs the tests, and cleans up resources.
func TestMain(m *testing.M) {
	// 1. Allow individual helper debugging by skipping TestMain environment setup through an environment variable.
	if os.Getenv("E2E_SKIP_TESTMAIN") == "true" {
		os.Exit(m.Run())
	}

	// 2. Set an overall timeout for e2e environment setup so Docker or external services cannot hang forever.
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	// 3. Resolve the repository root; Docker Compose files and Debezium config are loaded relative to this path.
	// Move two directories up and convert the result to an absolute path.
	root, err := filepath.Abs("../..")
	if err != nil {
		log.Fatalf("resolve repo root: %v", err)
	}

	// 4. Initialize the test environment config; environment variables override local defaults when present.
	testEnv = &env{
		repoRoot:    root,
		wmsURL:      getenv("WMS_URL", "http://localhost:8081"),
		debeziumURL: getenv("DEBEZIUM_URL", "http://localhost:8083"),
		dbURL:       getenv("DB_URL", "postgres://root:root@localhost:5432/wms_blockchain_db?sslmode=disable"),
		rpcURL:      getenv("RPC_URL", "http://localhost:9650/ext/bc/C/rpc"),
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}

	// 5. Start the Docker Compose test profile by default; set E2E_USE_EXISTING_STACK=true to use an existing stack.
	if os.Getenv("E2E_USE_EXISTING_STACK") != "true" {
		// The compose loader requires the referenced .env file to exist because the
		// debezium service declares it via `env_file` (Required), even though we
		// inject the variables below through WithEnv. Materialize it from the
		// example file when absent so the loader does not fail.
		if err := ensureEnvFile(root); err != nil {
			log.Fatalf("ensure .env: %v", err)
		}

		stack, err := compose.NewDockerComposeWith(
			compose.WithStackFiles(filepath.Join(root, "docker-compose.yaml")),
			compose.StackIdentifier(e2eProjectName),
			compose.WithProfiles("test"),
		)
		if err != nil {
			log.Fatalf("create compose stack: %v", err)
		}
		testEnv.stack = stack

		// 6. Remove old containers and volumes before startup by default so every e2e run starts from a clean environment.
		if os.Getenv("E2E_SKIP_STACK_RESET") != "true" {
			_ = stack.Down(ctx, compose.RemoveOrphans(true), compose.RemoveVolumes(true))
		}

		// 7. Inject test environment variables, start compose, and wait for WMS and ledger-adapter health checks.
		err = stack.
			WithEnv(map[string]string{
				"JWT_SECRET":        getenv("JWT_SECRET", "e2e-test-secret-at-least-32-chars"),
				"DB_USER":           getenv("DB_USER", "root"),
				"DB_PASSWORD":       getenv("DB_PASSWORD", "root"),
				"DB_NAME":           getenv("DB_NAME", "wms_blockchain_db"),
				"SEED_DATA":         getenv("SEED_DATA", "true"),
				"DEBEZIUM_PASSWORD": getenv("DEBEZIUM_PASSWORD", "debezium"),
			}).
			WaitForService("ledger-adapter", wait.ForHTTP("/health").WithPort("8085/tcp").WithStartupTimeout(3*time.Minute)).
			WaitForService("wms_app", wait.ForHTTP("/health").WithPort("8080/tcp").WithStartupTimeout(4*time.Minute)).
			Up(ctx, compose.Wait(true), compose.RunServices(
				"postgres",
				"db-init",
				"kafka",
				"kafka-init",
				"debezium",
				"avalanchego",
				"contract-deploy",
				"ledger-adapter",
				"wms_app",
			))
		if err != nil {
			log.Fatalf("start compose stack: %v", err)
		}
	}

	// 8. Connect to the PostgreSQL instance exposed on the host; tests query the DB directly for state assertions.
	pool, err := pgxpool.New(ctx, testEnv.dbURL)
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
	testEnv.db = pool

	// 9. Wait for WMS HTTP health readiness so later API requests do not hit a service that is still starting.
	if err := waitForHTTP(ctx, testEnv.httpClient, testEnv.wmsURL+"/health", http.StatusOK, 2*time.Minute); err != nil {
		log.Fatalf("wait WMS health: %v", err)
	}

	// 10. Register and wait for the Debezium connector so outbox_events can be synced to Kafka.
	if err := registerDebeziumConnector(ctx, testEnv); err != nil {
		log.Fatalf("register debezium connector: %v", err)
	}
	if err := waitForDebeziumConnector(ctx, testEnv, 2*time.Minute); err != nil {
		log.Fatalf("wait debezium connector: %v", err)
	}

	// 11. Resolve the smart contract address; if the environment variable is empty or zero, read the deployment result from the shared_state volume.
	if isUnsetContractAddr(testEnv.contractAddr) {
		testEnv.contractAddr = getenv("CONTRACT_ADDR", "")
	}
	if isUnsetContractAddr(testEnv.contractAddr) {
		addr, err := readSharedStateFile(ctx, "contract_addr.txt")
		if err != nil {
			log.Fatalf("read contract address: %v", err)
		}
		testEnv.contractAddr = addr
	}

	// 12. Run the actual test cases.
	code := m.Run()

	// 13. Close the database connection.
	if testEnv.db != nil {
		testEnv.db.Close()
	}

	// 14. Clean up the Docker Compose stack by default; set E2E_KEEP_STACK=true to keep it for investigation.
	if testEnv.stack != nil && os.Getenv("E2E_KEEP_STACK") != "true" {
		downCtx, downCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer downCancel()
		_ = testEnv.stack.Down(downCtx, compose.RemoveOrphans(true), compose.RemoveVolumes(true))
	}

	os.Exit(code)
}

// ensureEnvFile materializes a .env file at the repo root from .env.example when
// one does not already exist. The testcontainers compose loader treats the
// debezium service's env_file as Required and fails to parse the stack if it is
// missing, so this guarantees the file is present before the stack is created.
func ensureEnvFile(root string) error {
	envPath := filepath.Join(root, ".env")
	if _, err := os.Stat(envPath); err == nil {
		return nil
	}
	src, err := os.ReadFile(filepath.Join(root, ".env.example"))
	if err != nil {
		return fmt.Errorf("read .env.example: %w", err)
	}
	return os.WriteFile(envPath, src, 0o600)
}

// getenv reads an environment variable and returns the fallback when the variable is empty.
func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

// isUnsetContractAddr reports whether the contract address is unset; an empty string and the zero address are both treated as unset.
func isUnsetContractAddr(addr string) bool {
	addr = strings.TrimSpace(addr)
	return addr == "" || strings.EqualFold(addr, "0x0000000000000000000000000000000000000000")
}
