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

# Helpers
psql_q() {
  psql "$DB_URL" -tAc "$*" | tr -d '[:space:]'
}

cast_cmd() {
  docker run --rm --network blockchain_project_e2e_app_network --entrypoint cast "$CAST_IMAGE" "$@"
}
