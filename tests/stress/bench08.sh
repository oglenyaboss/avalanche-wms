#!/bin/bash
# Reusable blockchain-throughput benchmark: reset 5000 TPS products -> flood putaway via k6 08 -> measure on-chain commit rate.
# k6 08 бьёт подписанный API: нужны tests/stress/.signed/operators.json (stress-provision).
# Usage: bench08.sh <label>
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1
PSQL(){ docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "$1" 2>/dev/null | tr -d '[:space:]'; }
# Счётчики в ТОВАРАХ (warehouse_actions.item_ids), а не в строках outbox: одно
# подписанное действие пишет одну outbox-строку на много товаров.
COM(){ PSQL "SELECT COALESCE(sum(cardinality(wa.item_ids)),0) FROM public.warehouse_actions wa JOIN public.onchain_events oe ON oe.event_id=wa.action_id WHERE oe.status='COMMITTED'"; }
GEN(){ PSQL "SELECT COALESCE(sum(cardinality(item_ids)),0) FROM public.warehouse_actions WHERE submitted_at IS NOT NULL"; }
PEND(){ echo $(( $(GEN) - $(COM) )); }
# bin UUID резолвим динамически (на свежем сиде они случайные): из склада, где лежат TPS-товары.
WH=$(PSQL "SELECT b.warehouse_id FROM wms_inventory.bins b JOIN wms_inventory.products p ON p.bin_id=b.bin_id WHERE p.qr_code LIKE 'STRESS-TPS-QR-%' LIMIT 1")
RECV=$(PSQL "SELECT bin_id FROM wms_inventory.bins WHERE code='BUFFER-01' AND warehouse_id=$WH")   # BUFFER-01
STOR=$(PSQL "SELECT bin_id FROM wms_inventory.bins WHERE code='A-01-01' AND warehouse_id=$WH")   # A-01-01
LABEL="${1:-bench}"
CFG=$(docker logs ledger-adapter 2>&1 | grep 'config loaded' | tail -1 | grep -oE 'batch_size.:[0-9]+,.batch_timeout.:[0-9]+')
echo "=== bench08 [$LABEL]  adapter: $CFG ==="
docker exec postgres_db psql -U root -d wms_blockchain_db -c \
  "DELETE FROM wms_ops.putaways WHERE product_id IN (SELECT product_id FROM wms_inventory.products WHERE qr_code LIKE 'STRESS-TPS-QR-%'); UPDATE wms_inventory.products SET status='RECEIVED', bin_id='$RECV' WHERE qr_code LIKE 'STRESS-TPS-QR-%';" >/dev/null 2>&1
echo "reset RECEIVED: $(PSQL "SELECT count(*) FROM wms_inventory.products WHERE qr_code LIKE 'STRESS-TPS-QR-%' AND status='RECEIVED'")"
PSQL "SELECT COALESCE(json_agg(product_id::text ORDER BY qr_code),'[]') FROM wms_inventory.products WHERE qr_code LIKE 'STRESS-TPS-QR-%'" > /tmp/stress-tps-products.json
echo "json: $(python3 -c 'import json;print(len(json.load(open("/tmp/stress-tps-products.json"))))') products"
C0=$(COM); T0=$(date +%s)
echo "baseline committed items=$C0; flooding 5000 putaway..."
k6 run --no-usage-report --quiet -e WMS_URL=http://localhost:8081 -e RECEIVING_BIN_ID=$RECV -e STORAGE_BIN_ID=$STOR tests/stress/08-blockchain-tps.js >/tmp/k6-08.log 2>&1
TGEN=$(date +%s); CG=$(COM)
echo "k6 gen done +$((TGEN-T0))s: $(grep -oE 'items_placed[. ]+[0-9]+' /tmp/k6-08.log | head -1 | grep -oE '[0-9]+$') товаров; committed so far +$((CG-C0))"
PREV=$CG; PEAK=0
for i in $(seq 1 24); do
  sleep 5
  C=$(COM)
  BK=$(PEND)
  R=$(( (C-PREV)/5 )); [ $R -gt $PEAK ] && PEAK=$R
  echo "t=+$((i*5))s committed_items=$C (+$((C-PREV)) = ${R}/s) pending_items=$BK"
  PREV=$C
  [ "${BK:-9999}" -le 5 ] && { echo "drained"; break; }
done
TF=$(date +%s); TOT=$((PREV-C0)); DUR=$(( TF-T0 > 0 ? TF-T0 : 1 ))
echo "=== [$LABEL] RESULT: committed_items=$TOT  PEAK_SUSTAINED=${PEAK}/s  avg(flood+drain)=$((TOT/DUR))/s ==="
