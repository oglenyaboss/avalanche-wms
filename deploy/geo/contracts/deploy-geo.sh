#!/usr/bin/env bash
# deploy-geo.sh — deploy ConsortiumRegistry + SignedMappingWMS to the GEO subnet chain via node-home (host loopback :9750).
#
# Host-side mirror of deploy/contracts/deploy.sh: SAME ewoq deployer, SAME deploy order (Registry
# first, then SignedMappingWMS with --constructor-args REG) and SAME sanity checks (owner()==ewoq,
# itemStatus(0)==0, registry()==REG), but it targets the geo subnet chain (networkID 1338, chainID
# 99999) through the local node-home RPC gateway and records both addresses in
# deploy/geo/genesis/contract.env (gitignored).
#
# Isolation (C-5): reuses ledger-adapter/contracts ONLY as read-only Solidity source (forge artifacts
# go to /tmp). Does NOT touch deploy/contracts, the shared_state volume, or any running validator.
#
# Prereqs: node-home up (docker compose -f deploy/geo/docker-compose.geo.yaml up -d), foundry local.
#   deploy/geo/contracts/deploy-geo.sh
set -euo pipefail
cd "$(dirname "$0")"                                   # deploy/geo/contracts
GEO_GENESIS="../genesis"
# shellcheck disable=SC1091
. "$GEO_GENESIS/chain-ids.env"                         # BLOCKCHAIN_ID
: "${BLOCKCHAIN_ID:?set BLOCKCHAIN_ID (deploy/geo/genesis/chain-ids.env)}"

EWOQ_KEY='0x56289e99c94b6912bfc12adc093c9b51124f0dc54ac7a766b2bc5ccf558d8027'  # canonical prefunded key
EWOQ_ADDR=0x8db97C7cEcE249c2b98bDC0226Cc4C2A57BF52FC
RPC_URL="${GEO_DEPLOY_RPC:-http://localhost:9750/ext/bc/${BLOCKCHAIN_ID}/rpc}"  # node-home, host loopback
ENV_OUT="$GEO_GENESIS/contract.env"
CONTRACTS_DIR="../../../ledger-adapter/contracts"     # repo-root/ledger-adapter/contracts (read-only use)

echo "==> geo RPC: $RPC_URL"
CHAIN_ID=$(cast chain-id --rpc-url "$RPC_URL")
[ "$CHAIN_ID" = "99999" ] || { echo "FATAL: expected chainID 99999, got '$CHAIN_ID' — wrong RPC/node?"; exit 1; }
echo "==> geo chain live, chainID=$CHAIN_ID"

# Idempotency: skip redeploy if contract.env already points to a live Registry + SignedMappingWMS on
# the geo chain. A transient RPC error on `cast code` must NOT trigger a blind redeploy (would orphan
# the old contracts under the same ewoq nonce) — fail loudly instead. A legacy contract.env from the
# BatchMappingWMS era (no GEO_REGISTRY_ADDR) can't be trusted either — full redeploy.
if [ -f "$ENV_OUT" ]; then
  # shellcheck disable=SC1090
  . "$ENV_OUT"
  if [ -n "${GEO_CONTRACT_ADDR:-}" ]; then
    if [ -z "${GEO_REGISTRY_ADDR:-}" ]; then
      echo "==> legacy contract.env (BatchMappingWMS, no registry) — redeploying"
    else
      RCODE=$(cast code "$GEO_REGISTRY_ADDR" --rpc-url "$RPC_URL" 2>&1) || \
        { echo "FATAL: cast code for $GEO_REGISTRY_ADDR failed: $RCODE"; echo "       refusing to redeploy blindly — fix RPC or rm $ENV_OUT to force redeploy"; exit 1; }
      CCODE=$(cast code "$GEO_CONTRACT_ADDR" --rpc-url "$RPC_URL" 2>&1) || \
        { echo "FATAL: cast code for $GEO_CONTRACT_ADDR failed: $CCODE"; echo "       refusing to redeploy blindly — fix RPC or rm $ENV_OUT to force redeploy"; exit 1; }
      REG_ONCHAIN=$(cast call "$GEO_CONTRACT_ADDR" "registry()(address)" --rpc-url "$RPC_URL" 2>&1) || REG_ONCHAIN=""
      if [ "${#RCODE}" -gt 4 ] && [ "${#CCODE}" -gt 4 ] && [ "${REG_ONCHAIN,,}" = "${GEO_REGISTRY_ADDR,,}" ]; then
        echo "==> already deployed (Registry $GEO_REGISTRY_ADDR + SignedMappingWMS $GEO_CONTRACT_ADDR) — skipping redeploy"; exit 0
      fi
      if [ "${#CCODE}" -gt 4 ]; then
        echo "FATAL: $GEO_CONTRACT_ADDR is not SignedMappingWMS with registry $GEO_REGISTRY_ADDR — rm $ENV_OUT to force redeploy"; exit 1
      fi
      echo "==> stale contract.env ($GEO_CONTRACT_ADDR has no code), redeploying"
    fi
  fi
fi

cd "$CONTRACTS_DIR"
# Keep forge artifacts out of the source tree (don't churn ledger-adapter/contracts).
export FOUNDRY_OUT=/tmp/forge-geo-out FOUNDRY_CACHE_PATH=/tmp/forge-geo-cache
mkdir -p "$FOUNDRY_OUT" "$FOUNDRY_CACHE_PATH"

echo "==> deploying ConsortiumRegistry to geo chain"
OUT=$(forge create ConsortiumRegistry \
  --rpc-url "$RPC_URL" \
  --private-key "$EWOQ_KEY" \
  --broadcast \
  --json)
REG=$(echo "$OUT" | jq -r '.deployedTo')
[ -n "$REG" ] && [ "$REG" != "null" ] || { echo "FATAL: no deployedTo in forge output: $OUT"; exit 1; }

echo "==> sanity: registry owner() must be ewoq ($EWOQ_ADDR)"
OWNER=$(cast call "$REG" "owner()(address)" --rpc-url "$RPC_URL")
[ "${OWNER,,}" = "${EWOQ_ADDR,,}" ] || { echo "FAIL: owner() = $OWNER"; exit 1; }

echo "==> deploying SignedMappingWMS to geo chain"
OUT=$(forge create SignedMappingWMS \
  --rpc-url "$RPC_URL" \
  --private-key "$EWOQ_KEY" \
  --broadcast \
  --json \
  --constructor-args "$REG")
ADDR=$(echo "$OUT" | jq -r '.deployedTo')
TX=$(echo   "$OUT" | jq -r '.transactionHash')
[ -n "$ADDR" ] && [ "$ADDR" != "null" ] || { echo "FATAL: no deployedTo in forge output: $OUT"; exit 1; }

echo "==> sanity: itemStatus(0) must be 0 (None)"
STATUS=$(cast call "$ADDR" "itemStatus(uint256)(uint8)" 0 --rpc-url "$RPC_URL")
[ "$STATUS" = "0" ] || { echo "FAIL: itemStatus(0) = $STATUS"; exit 1; }

echo "==> sanity: registry() must be the deployed Registry ($REG)"
REG_ONCHAIN=$(cast call "$ADDR" "registry()(address)" --rpc-url "$RPC_URL")
[ "${REG_ONCHAIN,,}" = "${REG,,}" ] || { echo "FAIL: registry() = $REG_ONCHAIN"; exit 1; }

cd - >/dev/null
cat > "$ENV_OUT" <<EOF
# ConsortiumRegistry + SignedMappingWMS on the geo subnet chain (gitignored)
GEO_REGISTRY_ADDR=$REG
GEO_CONTRACT_ADDR=$ADDR
GEO_DEPLOY_TX=$TX
EOF
echo "==> deployed ConsortiumRegistry at $REG"
echo "==> deployed SignedMappingWMS at $ADDR"
echo "    tx $TX"
echo "    recorded -> $ENV_OUT"
echo "OK"
