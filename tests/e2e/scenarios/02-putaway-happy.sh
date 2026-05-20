#!/usr/bin/env bash
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
source "$HERE/lib/env.sh"
source "$HERE/lib/wait_for.sh"
source "$HERE/lib/kafka.sh"

echo "=== 02 putaway happy path (Accepted→PutAway) ==="

EVENT_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
PRODUCT_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
ITEM_ID=$(cast_cmd keccak "$PRODUCT_ID" | tr -d '[:space:]')

# Накатываем Accepted через receiving (prerequisite)
RECV_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
publish_event "receiving" "$RECV_ID" "$PRODUCT_ID" '{}'
wait_for_status "$RECV_ID" "COMMITTED" 30 2
wait_for_item_status "$ITEM_ID" "1" 10 1

# Теперь putaway
publish_event "putaway" "$EVENT_ID" "$PRODUCT_ID" '{}'
wait_for_status "$EVENT_ID" "COMMITTED" 30 2
wait_for_item_status "$ITEM_ID" "2" 10 1

echo "PASS"
