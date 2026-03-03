.PHONY: help up down build logs lint test tidy vendor init migrate

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

tidy: tidy-wms tidy-ledger ## Tidy all go.mod

vendor: vendor-wms vendor-ledger ## Rebuild all vendors

# ── Migrations ──────────────────────────────────────────
init: ## Run database & Kafka init (migrations, seed, topics)
	$(COMPOSE) run --rm db-init

migrate: ## Run database migrations only (alias for init)
	$(COMPOSE) run --rm db-init

# ── Pre-commit ──────────────────────────────────────────
hooks-install: ## Install pre-commit hooks
	pre-commit install

hooks-run: ## Run pre-commit on all files
	pre-commit run --all-files
