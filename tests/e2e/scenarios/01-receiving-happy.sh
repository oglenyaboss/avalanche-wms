#!/usr/bin/env bash
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
source "$HERE/lib/env.sh"
source "$HERE/lib/wait_for.sh"
source "$HERE/lib/kafka.sh"

echo "=== 01 receiving happy path ==="

EVENT_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
PRODUCT_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
echo "event_id=$EVENT_ID product_id=$PRODUCT_ID"

# item_id = uint256(keccak256(bytes(product_id-uuid-string)))
ITEM_ID=$(cast_cmd keccak "$PRODUCT_ID" | tr -d '[:space:]')
echo "item_id=$ITEM_ID"

# 1. Pre-check: on-chain itemStatus должен быть 0 (None) до отправки.
pre=$(cast_cmd call "$CONTRACT_ADDR" "itemStatus(uint256)(uint8)" "$ITEM_ID" --rpc-url "$RPC_URL" | tr -d '[:space:]')
[ "$pre" = "0" ] || { echo "FAIL: initial status != 0, got $pre"; exit 1; }

# 2. Publish в wms.receiving.v1 с header id=$EVENT_ID, key=$PRODUCT_ID
publish_event "receiving" "$EVENT_ID" "$PRODUCT_ID" '{"type":"receiving_close"}'
echo "published to kafka"

# 3. Ждём транзит PENDING→SENT→COMMITTED в onchain_events (max 60s)
wait_for_status "$EVENT_ID" "COMMITTED" 30 2

# 4. On-chain должен быть 1 (Accepted)
wait_for_item_status "$ITEM_ID" "1" 10 1

echo "PASS"
