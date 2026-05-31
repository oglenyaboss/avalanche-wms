.PHONY: help up down build logs lint test tidy vendor init migrate seed e2e-test-outbound \
        stress stress-logs stress-smoke stress-health stress-auth stress-receiving stress-full

COMPOSE := docker compose

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ── Docker ──────────────────────────────────────────────
up: ## Start all services
	$(COMPOSE) up -d

down: ## Stop all services
	$(COMPOSE) down

build: ## Rebuild and start all services
	$(COMPOSE) up -d --build

logs: ## Tail logs (usage: make logs s=wms-service)
	$(COMPOSE) logs -f $(s)

ps: ## Show running containers
	$(COMPOSE) ps -a

# ── Go: WMS ─────────────────────────────────────────────
lint-wms: ## Lint WMS service
	cd wms && golangci-lint run --config ../.golangci.yml ./...

test-wms: ## Run WMS tests
	cd wms && go test -race -count=1 ./...

tidy-wms: ## Tidy WMS go.mod
	cd wms && go mod tidy

vendor-wms: ## Rebuild WMS vendor
	cd wms && go mod vendor

# ── Go: Ledger Adapter ──────────────────────────────────
lint-ledger: ## Lint ledger-adapter
	cd ledger-adapter && golangci-lint run --config ../.golangci.yml ./...

test-ledger: ## Run ledger-adapter tests
	cd ledger-adapter && go test -race -count=1 ./...

tidy-ledger: ## Tidy ledger-adapter go.mod
	cd ledger-adapter && go mod tidy

vendor-ledger: ## Rebuild ledger-adapter vendor
	cd ledger-adapter && go mod vendor

# ── Aggregate ───────────────────────────────────────────
lint: lint-wms lint-ledger ## Lint all Go code

test: test-wms test-ledger ## Run all tests

e2e-test-outbound: ## Run full outbound WMS API -> DB -> Kafka -> chain e2e test
	cd tests/e2e && RPC_URL=http://localhost:9650/ext/bc/C/rpc go test -tags=e2e -count=1 -timeout=15m ./...

tidy: tidy-wms tidy-ledger ## Tidy all go.mod

vendor: vendor-wms vendor-ledger ## Rebuild all vendors

# ── Migrations ──────────────────────────────────────────
init: ## Run full infrastructure init (DB migrations + seed + Kafka topics)
	$(COMPOSE) run --rm db-init
	$(COMPOSE) run --rm kafka-init

migrate: ## Run database migrations only
	$(COMPOSE) run --rm db-init bash /wms/migrations/migrate.sh

seed: ## Re-seed development data
	$(COMPOSE) exec postgres psql -U $${DB_USER:-root} -d $${DB_NAME:-wms_blockchain_db} -f /deploy/seed.sql

# ── Pre-commit ──────────────────────────────────────────
hooks-install: ## Install pre-commit hooks
	pre-commit install

hooks-run: ## Run pre-commit on all files
	pre-commit run --all-files

# ── Debezium Connector ──────────────────────────────────

ifneq (,$(wildcard ./.env))
    include .env
    export
endif

.PHONY: register-connector connector-status delete-connector

register-connector:
	@curl -X POST http://localhost:8083/connectors \
  		-H "Content-Type: application/json" \
  		-d @deploy/debezium/connectors/postgres-connector.json

connector-status:
	@curl http://localhost:8083/connectors/outbox-connector/status

delete-connector:
	@curl -X DELETE http://localhost:8083/connectors/outbox-connector

stress: ## Run all stress tests 01–07 in Docker (seeds DB automatically)
	$(COMPOSE) --profile stress build k6
	$(COMPOSE) --profile stress up -d
	@echo ""
	@echo "Stress tests running in background."
	@echo "Follow progress : make stress-logs"
	@echo "Stop container  : docker compose --profile stress stop k6"

stress-logs: ## Tail k6 stress test output
	$(COMPOSE) logs -f k6

stress-smoke: ## Smoke test (1 VU, local k6 required)
	k6 run tests/stress/01-smoke.js

stress-health: ## Health check load (local k6 required)
	k6 run tests/stress/02-health.js

stress-auth: ## Auth endpoints load test (local k6 required)
	k6 run tests/stress/03-auth.js

stress-receiving: ## Receiving gate flow (local k6, needs stress-seed.sql)
	k6 run tests/stress/04-receiving-gate.js

stress-full: ## Full outbound flow (local k6, needs seed + env vars)
	@source <(bash tests/stress/setup/generate-stress-data.sh) && \
	  k6 run tests/stress/07-full-flow.js
