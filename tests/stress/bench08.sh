#!/bin/bash
# Reusable blockchain-throughput benchmark: reset 5000 TPS products -> flood putaway via k6 08 -> measure on-chain commit rate.
# Usage: bench08.sh <label>
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1
PSQL(){ docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "$1" 2>/dev/null | tr -d '[:space:]'; }
RECV=494767c4-8f72-4002-b355-6f2e32d70c3e   # BUFFER-01
STOR=8c735930-c268-4c02-8c21-0adeb9d03ba4   # A-01-01
LABEL="${1:-bench}"
CFG=$(docker logs ledger-adapter 2>&1 | grep 'config loaded' | tail -1 | grep -oE 'batch_size.:[0-9]+,.batch_timeout.:[0-9]+')
echo "=== bench08 [$LABEL]  adapter: $CFG ==="
docker exec postgres_db psql -U root -d wms_blockchain_db -c \
  "DELETE FROM wms_ops.putaways WHERE product_id IN (SELECT product_id FROM wms_inventory.products WHERE qr_code LIKE 'STRESS-TPS-QR-%'); UPDATE wms_inventory.products SET status='RECEIVED', bin_id='$RECV' WHERE qr_code LIKE 'STRESS-TPS-QR-%';" >/dev/null 2>&1
echo "reset RECEIVED: $(PSQL "SELECT count(*) FROM wms_inventory.products WHERE qr_code LIKE 'STRESS-TPS-QR-%' AND status='RECEIVED'")"
PSQL "SELECT COALESCE(json_agg(product_id::text ORDER BY qr_code),'[]') FROM wms_inventory.products WHERE qr_code LIKE 'STRESS-TPS-QR-%'" > /tmp/stress-tps-products.json
echo "json: $(python3 -c 'import json;print(len(json.load(open("/tmp/stress-tps-products.json"))))') products"
C0=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='COMMITTED'"); T0=$(date +%s)
echo "baseline committed=$C0; flooding 5000 putaway..."
k6 run --no-usage-report --quiet -e WMS_URL=http://localhost:8081 -e RECEIVING_BIN_ID=$RECV -e STORAGE_BIN_ID=$STOR tests/stress/08-blockchain-tps.js >/tmp/k6-08.log 2>&1
TGEN=$(date +%s); CG=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='COMMITTED'")
echo "k6 gen done +$((TGEN-T0))s: $(grep -oE 'outbox_events_generated[. ]+[0-9]+' /tmp/k6-08.log | head -1 | grep -oE '[0-9]+$') outbox; committed so far +$((CG-C0))"
PREV=$CG; PEAK=0
for i in $(seq 1 24); do
  sleep 5
  C=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='COMMITTED'")
  BK=$(PSQL "SELECT count(*) FROM public.outbox_events oe WHERE NOT EXISTS (SELECT 1 FROM public.onchain_events oc WHERE oc.event_id=oe.event_id)")
  R=$(( (C-PREV)/5 )); [ $R -gt $PEAK ] && PEAK=$R
  echo "t=+$((i*5))s committed=$C (+$((C-PREV)) = ${R}/s) backlog=$BK"
  PREV=$C
  [ "${BK:-9999}" -le 5 ] && { echo "drained"; break; }
done
TF=$(date +%s); TOT=$((PREV-C0)); DUR=$(( TF-T0 > 0 ? TF-T0 : 1 ))
echo "=== [$LABEL] RESULT: committed=$TOT  PEAK_SUSTAINED=${PEAK}/s  avg(flood+drain)=$((TOT/DUR))/s ==="
