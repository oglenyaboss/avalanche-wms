#!/usr/bin/env bash
set -euo pipefail

# ewoq — канонический prefunded ключ local avalanchego (P/X/C chains)
EWOQ_KEY='0x56289e99c94b6912bfc12adc093c9b51124f0dc54ac7a766b2bc5ccf558d8027'
RPC_URL='http://avalanchego:9650/ext/bc/C/rpc'

echo "==> Waiting for C-Chain RPC at $RPC_URL"
for i in $(seq 1 60); do
  if curl -sf -X POST -H 'Content-Type: application/json' \
       --data '{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}' \
       "$RPC_URL" > /dev/null; then
    break
  fi
  echo "  waiting ($i/60)..."
  sleep 5
done

CHAIN_ID=$(cast chain-id --rpc-url "$RPC_URL")
echo "==> C-Chain ready, chainID=$CHAIN_ID"

cd /contracts
# /contracts is mounted read-only; write forge artifacts somewhere writable.
export FOUNDRY_OUT=/tmp/forge-out
export FOUNDRY_CACHE_PATH=/tmp/forge-cache
mkdir -p "$FOUNDRY_OUT" "$FOUNDRY_CACHE_PATH"

echo "==> Deploying BatchMappingWMS"
OUT=$(forge create BatchMappingWMS \
  --rpc-url "$RPC_URL" \
  --private-key "$EWOQ_KEY" \
  --broadcast \
  --json)

ADDR=$(echo "$OUT" | jq -r '.deployedTo')
TX=$(echo   "$OUT" | jq -r '.transactionHash')

echo -n "$RPC_URL" > /shared/rpc_url.txt
echo -n "$ADDR"    > /shared/contract_addr.txt
echo -n "$EWOQ_KEY" > /shared/deployer_key.txt
echo "==> Deployed BatchMappingWMS at $ADDR"
echo "    tx $TX"

echo "==> Sanity: itemStatus(0) must be 0 (None)"
STATUS=$(cast call "$ADDR" "itemStatus(uint256)(uint8)" 0 --rpc-url "$RPC_URL")
[ "$STATUS" = "0" ] || { echo "FAIL: itemStatus(0) = $STATUS"; exit 1; }
echo "OK"
