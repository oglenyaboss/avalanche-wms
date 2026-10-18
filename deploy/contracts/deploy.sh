#!/usr/bin/env bash
set -euo pipefail

# ewoq — канонический prefunded ключ local avalanchego (P/X/C chains)
EWOQ_KEY='0x56289e99c94b6912bfc12adc093c9b51124f0dc54ac7a766b2bc5ccf558d8027'

# RPC_URL — динамический эндпоинт Subnet-EVM (/ext/bc/<blockchainID>/rpc), который
# subnet-init пишет в shared-том. Ждём появления файла, затем используем его.
RPC_FILE=/shared/rpc_url.txt
echo "==> Waiting for subnet RPC URL at $RPC_FILE (written by subnet-init)"
RPC_URL=""
for i in $(seq 1 60); do
  if [ -s "$RPC_FILE" ]; then
    RPC_URL=$(tr -d '[:space:]' < "$RPC_FILE")
    [ -n "$RPC_URL" ] && break
  fi
  echo "  waiting for rpc_url.txt ($i/60)..."
  sleep 5
done
[ -n "$RPC_URL" ] || { echo "FATAL: $RPC_FILE not populated after 5 min"; exit 1; }

echo "==> Waiting for Subnet-EVM RPC at $RPC_URL"
READY=false
for i in $(seq 1 60); do
  if curl -sf -X POST -H 'Content-Type: application/json' \
       --data '{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}' \
       "$RPC_URL" > /dev/null 2>&1; then
    READY=true
    break
  fi
  echo "  waiting ($i/60)..."
  sleep 5
done
[ "$READY" = "true" ] || { echo "FATAL: Subnet-EVM RPC not ready after 5 min"; exit 1; }

CHAIN_ID=$(cast chain-id --rpc-url "$RPC_URL")
echo "==> Subnet-EVM ready, chainID=$CHAIN_ID"

cd /contracts
# /contracts mounted read-only; write forge artifacts somewhere writable.
export FOUNDRY_OUT=/tmp/forge-out
export FOUNDRY_CACHE_PATH=/tmp/forge-cache
mkdir -p "$FOUNDRY_OUT" "$FOUNDRY_CACHE_PATH"

EWOQ_ADDR=0x8db97C7cEcE249c2b98bDC0226Cc4C2A57BF52FC

# live_addr <file>: печатает адрес из файла, если по нему есть байткод. Пусто — файла нет,
# он пустой или кода нет. RPC-ошибка — FATAL: слепой redeploy осиротил бы старый контракт.
# Сообщения идут в stderr, потому что stdout забирает вызывающий через $(...).
live_addr() {
  local file=$1 existing code
  [ -s "$file" ] || return 0
  # `tr -d` — защита от случайного trailing newline (nano и пр. редакторы).
  existing=$(tr -d '[:space:]' < "$file")
  if [ -z "$existing" ]; then
    echo "==> $file is whitespace-only, treating as empty" >&2
    return 0
  fi
  if ! code=$(cast code "$existing" --rpc-url "$RPC_URL" 2>&1); then
    echo "FATAL: cast code for $existing failed: $code" >&2
    echo "       refusing to redeploy blindly — fix RPC or wipe shared_state volume" >&2
    exit 1
  fi
  if [ "${#code}" -gt 4 ]; then
    echo "$existing"
  else
    echo "==> Stale $(basename "$file") ($existing has no code), redeploying" >&2
  fi
}

# Registry деплоится первым (ewoq nonce 0), BatchMappingWMS — вторым (nonce 1).
REG=$(live_addr /shared/registry_addr.txt)
if [ -n "$REG" ]; then
  echo "==> ConsortiumRegistry already deployed at $REG — skipping"
else
  echo "==> Deploying ConsortiumRegistry"
  OUT=$(forge create ConsortiumRegistry \
    --rpc-url "$RPC_URL" \
    --private-key "$EWOQ_KEY" \
    --broadcast \
    --json)
  REG=$(echo "$OUT" | sed -n '/^{$/,/^}$/p' | jq -r '.deployedTo')

  echo "==> Sanity: owner() must be ewoq ($EWOQ_ADDR)"
  OWNER=$(cast call "$REG" "owner()(address)" --rpc-url "$RPC_URL")
  [ "${OWNER,,}" = "${EWOQ_ADDR,,}" ] || { echo "FAIL: owner() = $OWNER"; exit 1; }

  # Persist only after successful sanity
  echo -n "$REG" > /shared/registry_addr.txt
  echo "==> Deployed ConsortiumRegistry at $REG"
fi

CONTRACT=$(live_addr /shared/contract_addr.txt)
if [ -n "$CONTRACT" ]; then
  echo "==> Contract already deployed at $CONTRACT — skipping redeploy"
  # Re-persist auxiliary files in case they were cleared
  echo -n "$RPC_URL"   > /shared/rpc_url.txt
  echo -n "$EWOQ_KEY"  > /shared/deployer_key.txt
  exit 0
fi

echo "==> Deploying BatchMappingWMS"
OUT=$(forge create BatchMappingWMS \
  --rpc-url "$RPC_URL" \
  --private-key "$EWOQ_KEY" \
  --broadcast \
  --json)

ADDR=$(echo "$OUT" | sed -n '/^{$/,/^}$/p' | jq -r '.deployedTo')
TX=$(echo   "$OUT" | sed -n '/^{$/,/^}$/p' | jq -r '.transactionHash')

echo "==> Sanity: itemStatus(0) must be 0 (None)"
STATUS=$(cast call "$ADDR" "itemStatus(uint256)(uint8)" 0 --rpc-url "$RPC_URL")
[ "$STATUS" = "0" ] || { echo "FAIL: itemStatus(0) = $STATUS"; exit 1; }

# Persist only after successful sanity
echo -n "$RPC_URL"   > /shared/rpc_url.txt
echo -n "$ADDR"      > /shared/contract_addr.txt
echo -n "$EWOQ_KEY"  > /shared/deployer_key.txt
echo "==> Deployed BatchMappingWMS at $ADDR"
echo "    tx $TX"
echo "==> ConsortiumRegistry: $REG"
echo "==> BatchMappingWMS:    $ADDR"
echo "OK"
