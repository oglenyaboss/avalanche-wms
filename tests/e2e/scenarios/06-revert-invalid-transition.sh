#!/usr/bin/env bash
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
source "$HERE/lib/env.sh"
source "$HERE/lib/wait_for.sh"
source "$HERE/lib/kafka.sh"

echo "=== 06 revert: pick without receiving → FAILED + DLQ ==="

EVENT_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
PRODUCT_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
ITEM_ID=$(cast_cmd keccak "$PRODUCT_ID" | tr -d '[:space:]')

# Продукт в состоянии None (0) → batchPick требует PutAway (2) → revert ожидаем.
publish_event "picking" "$EVENT_ID" "$PRODUCT_ID" '{}'

# Adapter должен поймать revert на EstimateGas, MarkFailed, DLQ publish
wait_for_status "$EVENT_ID" "FAILED" 30 2

# On-chain itemStatus остаётся 0 (None) — revert не меняет state
on_chain=$(cast_cmd call "$CONTRACT_ADDR" "itemStatus(uint256)(uint8)" "$ITEM_ID" --rpc-url "$RPC_URL" | tr -d '[:space:]')
[ "$on_chain" = "0" ] || { echo "FAIL: on-chain status should be 0 after revert, got $on_chain"; exit 1; }

# error_message в БД заполнен
err_msg=$(psql_q "SELECT COALESCE(error_message, '') FROM public.onchain_events WHERE event_id='$EVENT_ID'" || true)
if [ -z "$err_msg" ]; then
  echo "FAIL: error_message пуст, ожидался revert reason"
  exit 1
fi
echo "  ✓ error_message записан: $err_msg"

echo "PASS"
