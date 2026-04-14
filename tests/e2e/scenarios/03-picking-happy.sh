#!/usr/bin/env bash
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
source "$HERE/lib/env.sh"
source "$HERE/lib/wait_for.sh"
source "$HERE/lib/kafka.sh"

echo "=== 03 picking happy path (PutAway→Picked) ==="

PRODUCT_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
ITEM_ID=$(cast_cmd keccak "$PRODUCT_ID" | tr -d '[:space:]')

# Prerequisites: Accepted, then PutAway
R=$(uuidgen | tr '[:upper:]' '[:lower:]')
publish_event "wms.receiving.v1" "$R" "$PRODUCT_ID" '{}'
wait_for_status "$R" "COMMITTED" 30 2

P=$(uuidgen | tr '[:upper:]' '[:lower:]')
publish_event "wms.putaway.v1" "$P" "$PRODUCT_ID" '{}'
wait_for_status "$P" "COMMITTED" 30 2
wait_for_item_status "$ITEM_ID" "2" 10 1

# Pick
PICK=$(uuidgen | tr '[:upper:]' '[:lower:]')
publish_event "wms.picking.v1" "$PICK" "$PRODUCT_ID" '{}'
wait_for_status "$PICK" "COMMITTED" 30 2
wait_for_item_status "$ITEM_ID" "3" 10 1

echo "PASS"
