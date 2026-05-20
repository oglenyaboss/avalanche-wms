#!/usr/bin/env bash
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
source "$HERE/lib/env.sh"
source "$HERE/lib/wait_for.sh"
source "$HERE/lib/kafka.sh"

echo "=== 04 shipping happy path (Picked→Shipped) ==="

PRODUCT_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
ITEM_ID=$(cast_cmd keccak "$PRODUCT_ID" | tr -d '[:space:]')

# Prerequisites
for agg in receiving putaway picking; do
  E=$(uuidgen | tr '[:upper:]' '[:lower:]')
  publish_event "$agg" "$E" "$PRODUCT_ID" '{}'
  wait_for_status "$E" "COMMITTED" 30 2
done
wait_for_item_status "$ITEM_ID" "3" 10 1

# Ship
SHIP=$(uuidgen | tr '[:upper:]' '[:lower:]')
publish_event "shipping" "$SHIP" "$PRODUCT_ID" '{}'
wait_for_status "$SHIP" "COMMITTED" 30 2
wait_for_item_status "$ITEM_ID" "4" 10 1

echo "PASS"
