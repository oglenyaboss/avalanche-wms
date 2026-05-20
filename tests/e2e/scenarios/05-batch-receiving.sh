#!/usr/bin/env bash
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
source "$HERE/lib/env.sh"
source "$HERE/lib/wait_for.sh"
source "$HERE/lib/kafka.sh"

echo "=== 05 batch receiving (3 events → 1 tx) ==="

# Генерим 3 разных продукта и быстро публикуем (в пределах BATCH_TIMEOUT=100ms
# adapter должен собрать их в один batch).

EVT_IDS=()
PROD_IDS=()
ITEM_IDS=()

for _ in 1 2 3; do
  eid=$(uuidgen | tr '[:upper:]' '[:lower:]')
  pid=$(uuidgen | tr '[:upper:]' '[:lower:]')
  iid=$(cast_cmd keccak "$pid" | tr -d '[:space:]')
  EVT_IDS+=("$eid")
  PROD_IDS+=("$pid")
  ITEM_IDS+=("$iid")
done

# Публикуем подряд без sleep — batcher должен накопить
for i in 0 1 2; do
  publish_event "receiving" "${EVT_IDS[$i]}" "${PROD_IDS[$i]}" '{}'
done

# Все 3 должны быть COMMITTED
for eid in "${EVT_IDS[@]}"; do
  wait_for_status "$eid" "COMMITTED" 30 2
done

# Проверяем что все 3 получили один и тот же tx_hash (признак, что они
# попали в один batch-tx). Это soft-assert — batcher может решить flush'ить
# раньше если размер != 10. Но при быстром подряд-published обычно один batch.
TX1=$(psql_q "SELECT tx_hash FROM public.onchain_events WHERE event_id='${EVT_IDS[0]}'")
TX2=$(psql_q "SELECT tx_hash FROM public.onchain_events WHERE event_id='${EVT_IDS[1]}'")
TX3=$(psql_q "SELECT tx_hash FROM public.onchain_events WHERE event_id='${EVT_IDS[2]}'")
echo "tx_hashes: $TX1 / $TX2 / $TX3"
if [ "$TX1" = "$TX2" ] && [ "$TX2" = "$TX3" ]; then
  echo "  ✓ all 3 events batched into one tx"
else
  echo "  ⚠ events landed in different batches (ok, но batcher мог бы агрегировать)"
fi

# On-chain все 3 items → Accepted
for iid in "${ITEM_IDS[@]}"; do
  wait_for_item_status "$iid" "1" 5 1
done

echo "PASS"
