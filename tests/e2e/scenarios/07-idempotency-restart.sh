#!/usr/bin/env bash
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
source "$HERE/lib/env.sh"
source "$HERE/lib/wait_for.sh"
source "$HERE/lib/kafka.sh"

echo "=== 07 idempotency: publish after restart, no on-chain duplicate ==="

EVENT_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
PRODUCT_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
ITEM_ID=$(cast_cmd keccak "$PRODUCT_ID" | tr -d '[:space:]')

# 1. Публикуем receiving, дожимаем до COMMITTED
publish_event "receiving" "$EVENT_ID" "$PRODUCT_ID" '{}'
wait_for_status "$EVENT_ID" "COMMITTED" 30 2
wait_for_item_status "$ITEM_ID" "1" 10 1

# Снимаем tx_hash первой tx для сравнения
TX_BEFORE=$(psql_q "SELECT tx_hash FROM public.onchain_events WHERE event_id='$EVENT_ID'")

# 2. Рестартуем ledger-adapter
echo "  restarting ledger-adapter..."
docker compose restart ledger-adapter >/dev/null
sleep 5  # wait for health

# Wait until healthy again
for _ in $(seq 1 30); do
  s=$(docker inspect --format='{{.State.Health.Status}}' ledger-adapter 2>/dev/null || echo "")
  [ "$s" = "healthy" ] && break
  sleep 2
done

# 3. Re-publish того же event_id+product_id — adapter должен пропустить (Exists=true)
publish_event "receiving" "$EVENT_ID" "$PRODUCT_ID" '{}'
sleep 5

# onchain_events: тот же event_id, тот же tx_hash (НЕ заменился)
TX_AFTER=$(psql_q "SELECT tx_hash FROM public.onchain_events WHERE event_id='$EVENT_ID'")
[ "$TX_BEFORE" = "$TX_AFTER" ] || { echo "FAIL: tx_hash changed after duplicate publish ($TX_BEFORE → $TX_AFTER)"; exit 1; }

# status остался COMMITTED, не переехал в PENDING
status=$(psql_q "SELECT status::text FROM public.onchain_events WHERE event_id='$EVENT_ID'")
[ "$status" = "COMMITTED" ] || { echo "FAIL: status flipped to $status after duplicate"; exit 1; }

echo "  ✓ duplicate publish skipped; tx_hash stable; status COMMITTED"
echo "PASS"
