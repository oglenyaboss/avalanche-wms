// audit — CLI сверки подписанных складских действий БД с доменными таблицами и цепью (ADR-0001 §4).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"wms/internal/audit"
	"wms/internal/platform/postgres"
)

const usage = `usage:
  audit [--json] product <product-uuid|qr-code>
  audit [--json] action <action-uuid>
  audit [--json] all

окружение: RPC_URL, CONTRACT_ADDR, REGISTRY_ADDR (или <NAME>_FILE);
DB_HOST, DB_PORT, POSTGRES_USER, POSTGRES_PASSWORD, POSTGRES_DB
`

var errUsage = errors.New("usage")

func main() {
	code, err := run(os.Args[1:])
	if errors.Is(err, errUsage) {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "audit: %v\n", err)
		os.Exit(2)
	}
	if code != 0 {
		os.Exit(code)
	}
}

func run(args []string) (int, error) {
	asJSON := false
	if len(args) > 0 && args[0] == "--json" {
		asJSON = true
		args = args[1:]
	}
	if len(args) == 0 {
		return 0, errUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	switch args[0] {
	case "product":
		if len(args) != 2 {
			return 0, errUsage
		}
		return runProduct(ctx, args[1], asJSON)
	case "action":
		if len(args) != 2 {
			return 0, errUsage
		}
		return runAction(ctx, args[1], asJSON)
	case "all":
		if len(args) != 1 {
			return 0, errUsage
		}
		return runAll(ctx, asJSON)
	default:
		return 0, errUsage
	}
}

type app struct {
	auditor *audit.Auditor
	repo    *audit.Repository
	pool    *pgxpool.Pool
	chain   *audit.Chain
}

func (a *app) close() {
	a.chain.Close()
	a.pool.Close()
}

func build(ctx context.Context) (*app, error) {
	rpcURL := envOrFile("RPC_URL")
	contractAddr := envOrFile("CONTRACT_ADDR")
	registryAddr := envOrFile("REGISTRY_ADDR")
	if rpcURL == "" || contractAddr == "" || registryAddr == "" {
		return nil, errors.New("RPC_URL, CONTRACT_ADDR и REGISTRY_ADDR обязательны")
	}
	pool, err := postgres.NewPool(ctx,
		envOr("POSTGRES_USER", "root"), envOr("POSTGRES_PASSWORD", "root"),
		envOr("DB_HOST", "localhost"), envOr("DB_PORT", "5432"),
		envOr("POSTGRES_DB", "wms_blockchain_db"), 4)
	if err != nil {
		return nil, fmt.Errorf("connect db: %w", err)
	}
	repo := audit.NewRepository(pool)
	chain, err := audit.DialChain(ctx, rpcURL, common.HexToAddress(contractAddr), common.HexToAddress(registryAddr))
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &app{auditor: audit.NewAuditor(repo, chain), repo: repo, pool: pool, chain: chain}, nil
}

func runProduct(ctx context.Context, key string, asJSON bool) (int, error) {
	a, err := build(ctx)
	if err != nil {
		return 0, err
	}
	defer a.close()
	productID, err := a.repo.ProductID(ctx, key)
	if err != nil {
		return 0, err
	}
	report, err := a.auditor.Product(ctx, productID)
	if err != nil {
		return 0, err
	}
	if asJSON {
		return exitCode(report.Findings()), writeJSON(report)
	}
	printReport(report)
	return exitCode(report.Findings()), nil
}

func runAction(ctx context.Context, rawID string, asJSON bool) (int, error) {
	actionID, err := uuid.Parse(rawID)
	if err != nil {
		return 0, errUsage
	}
	a, err := build(ctx)
	if err != nil {
		return 0, err
	}
	defer a.close()
	stage, err := a.auditor.Action(ctx, actionID)
	if err != nil {
		return 0, err
	}
	if asJSON {
		return exitCode(stage.Findings), writeJSON(stage)
	}
	printStage(stage)
	return exitCode(stage.Findings), nil
}

func runAll(ctx context.Context, asJSON bool) (int, error) {
	a, err := build(ctx)
	if err != nil {
		return 0, err
	}
	defer a.close()
	findings, err := a.auditor.All(ctx)
	if err != nil {
		return 0, err
	}
	if asJSON {
		return exitCode(findings), writeJSON(struct {
			Findings []audit.Finding `json:"findings"`
		}{Findings: findings})
	}
	printFindings(findings)
	return exitCode(findings), nil
}

func exitCode(findings []audit.Finding) int {
	if len(findings) > 0 {
		return 1
	}
	return 0
}

func writeJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	return nil
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envOrFile(name string) string {
	if path := os.Getenv(name + "_FILE"); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return os.Getenv(name)
}
