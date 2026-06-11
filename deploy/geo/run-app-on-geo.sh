#!/usr/bin/env bash
# run-app-on-geo.sh — drive the WMS app stack against the GEO subnet chain instead of the local
# test-profile chain. Repoints ONLY the ledger-adapter (the wms backend is chain-decoupled via the
# outbox). Additive: layers deploy/geo/docker-compose.geo-app.yaml over the base compose. Does NOT
# use --profile test, does NOT touch deploy/subnet, deploy/contracts, or the shared_state volume.
#
# Sets GEO_RPC_URL / GEO_CONTRACT_ADDR / BLOCKCHAIN_ID from the geo env files, then passes ALL args
# straight through to `docker compose`:
#   deploy/geo/run-app-on-geo.sh config                                            # inspect merged model
#   deploy/geo/run-app-on-geo.sh up -d --force-recreate --no-deps ledger-adapter   # switch adapter to geo
#   deploy/geo/run-app-on-geo.sh logs --tail=50 ledger-adapter
# To point the adapter BACK at the local chain, recreate it from the base compose WITHOUT this file:
#   docker compose -f docker-compose.yaml up -d --force-recreate --no-deps ledger-adapter
set -euo pipefail
cd "$(dirname "$0")/../.."                              # repo root
GEO=deploy/geo
# shellcheck disable=SC1091
. "$GEO/genesis/chain-ids.env"                          # BLOCKCHAIN_ID
[ -f "$GEO/genesis/contract.env" ] && . "$GEO/genesis/contract.env"   # GEO_CONTRACT_ADDR
: "${BLOCKCHAIN_ID:?missing BLOCKCHAIN_ID (deploy/geo/genesis/chain-ids.env)}"
: "${GEO_CONTRACT_ADDR:?missing GEO_CONTRACT_ADDR — run deploy/geo/contracts/deploy-geo.sh first}"
# Container-to-container RPC: the adapter joins geo_default and reaches node-home by name (its RPC
# port is published loopback-only, so host.docker.internal would not reach it).
export BLOCKCHAIN_ID GEO_CONTRACT_ADDR
export GEO_RPC_URL="http://geo-node-home:9650/ext/bc/${BLOCKCHAIN_ID}/rpc"

# The container_name `ledger-adapter` is global (not project-prefixed), so we must operate within the
# compose project that already owns the running stack — which may be `stresstest` (a worktree bring-up)
# rather than the default. Detect it from the running adapter; allow COMPOSE_PROJECT_NAME to override.
DETECTED_PROJECT=$(docker inspect ledger-adapter \
  --format '{{index .Config.Labels "com.docker.compose.project"}}' 2>/dev/null || true)
export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-${DETECTED_PROJECT:-blockchain_project}}"

echo "geo target: project=$COMPOSE_PROJECT_NAME RPC=$GEO_RPC_URL CONTRACT=$GEO_CONTRACT_ADDR" >&2
exec docker compose -p "$COMPOSE_PROJECT_NAME" \
  -f docker-compose.yaml -f "$GEO/docker-compose.geo-app.yaml" "$@"
