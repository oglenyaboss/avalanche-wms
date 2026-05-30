#!/usr/bin/env bash
# Общие env vars для e2e сценариев. Вызывать через `source`.

# БД (direct, без docker exec — psql на хосте)
export DB_USER="${DB_USER:-root}"
export DB_PASSWORD="${DB_PASSWORD:-root}"
export DB_NAME="${DB_NAME:-wms_blockchain_db}"
export DB_URL="postgres://${DB_USER}:${DB_PASSWORD}@localhost:5432/${DB_NAME}?sslmode=disable"

# Chain — читаем RPC URL / contract address из shared volume, куда contract-deploy
# их пишет после успешного деплоя.
if [ -z "${RPC_URL:-}" ]; then
  export RPC_URL=$(docker run --rm -v blockchain_project_e2e_shared_state:/s alpine cat /s/rpc_url.txt 2>/dev/null || echo "")
fi
if [ -z "${CONTRACT_ADDR:-}" ]; then
  export CONTRACT_ADDR=$(docker run --rm -v blockchain_project_e2e_shared_state:/s alpine cat /s/contract_addr.txt 2>/dev/null || echo "")
fi

if [ -z "$RPC_URL" ] || [ -z "$CONTRACT_ADDR" ]; then
  echo "FATAL: RPC_URL or CONTRACT_ADDR not found in shared_state volume." >&2
  echo "       Did `docker compose --profile test up contract-deploy` succeed?" >&2
  exit 1
fi

# WMS API (не используется в Kafka-direct сценариях)
export WMS_URL="${WMS_URL:-http://localhost:8081}"

# Foundry image для cast (pinning на stable = reproducible)
export CAST_IMAGE="ghcr.io/foundry-rs/foundry:stable"

# Repo root (lib/env.sh → tests/e2e/lib → repo root two levels up). Used to recreate
# the ledger-adapter with scenario-specific env (BATCH_TIMEOUT / RECEIPT_POLL_TIMEOUT /
# RECONCILE_*), which are env-interpolated in docker-compose.yaml.
_ENV_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export REPO_ROOT="$(cd "$_ENV_DIR/../.." && pwd)"
export COMPOSE_PROJECT="${COMPOSE_PROJECT:-blockchain_project_e2e}"

# Helpers
psql_q() {
  psql "$DB_URL" -tAc "$*" | tr -d '[:space:]'
}

cast_cmd() {
  docker run --rm --network "${COMPOSE_PROJECT}_app_network" --entrypoint cast "$CAST_IMAGE" "$@"
}

# recreate_adapter — пересоздаёт только ledger-adapter, подхватывая текущие exported
# env-overrides (BATCH_TIMEOUT / RECEIPT_POLL_TIMEOUT / RECONCILE_INTERVAL /
# RECONCILE_MIN_AGE). RPC_URL и CONTRACT_ADDR подтягиваются из shared volume через
# *_FILE override, поэтому recreate их не теряет.
recreate_adapter() {
  ( cd "$REPO_ROOT" && docker compose -p "$COMPOSE_PROJECT" --profile test \
      up -d --no-deps --force-recreate ledger-adapter >/dev/null )
}

# wait_adapter [tries=60] — ждёт /health (:8085) пока адаптер не поднимется.
wait_adapter() {
  local tries=${1:-60}
  for _ in $(seq 1 "$tries"); do
    if curl -sf http://localhost:8085/health >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "FATAL: ledger-adapter did not become healthy on :8085" >&2
  return 1
}
