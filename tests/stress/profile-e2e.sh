#!/bin/bash
# profile-e2e.sh — COMMITTED END-TO-END throughput (chain ON, sustained).
#
# Фронт уже даёт ~1560 переходов/с ГЕНЕРАЦИИ (profile-front.sh). Этот тест отвечает
# на ПОДТВЕРЖДЁННУЮ единицу: держит ли ВЕСЬ пайплайн (WMS→outbox→Debezium→adapter→
# Subnet-EVM) ~1500 COMMITTED переходов/с с ПЛОСКИМ backlog. Чейн НЕ на паузе;
# адаптер на сильном профиле (900/2s/8); pg затюнен; multi-SKU; index 0012.
#
# Живой семплер каждые 3с: committed (onchain COMMITTED delta), generated (outbox delta),
# backlog (generated-committed = in-flight), picked. Если backlog не растёт, а
# committed-rate ≈ front-rate → пайплайн держит этот темп committed end-to-end.
#
# Usage: ./profile-e2e.sh [VUS]   env: SKU_COUNT=70 N_ITEMS=30000 ITERS MAX_DURATION
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1
export PATH="$HOME/.foundry/bin:$PATH"
export COMPOSE_PROJECT_NAME=stresstest

VUS="${1:-70}"
SKU_COUNT="${SKU_COUNT:-70}"
N_ITEMS="${N_ITEMS:-30000}"
ITERS="${ITERS:-$N_ITEMS}"
MAX_DURATION="${MAX_DURATION:-15m}"
PICK_BATCH="${PICK_BATCH:-4}"; ALLOC_EVERY="${ALLOC_EVERY:-10}"
QR="STRESS-QR-TPUT-%"
PSQL(){ docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "$1" 2>/dev/null | tr -d '[:space:]'; }
RAW(){  docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "$1" 2>/dev/null; }

echo "######## E2E COMMITTED — VUS=$VUS SKU=$SKU_COUNT ITEMS=$N_ITEMS (chain ON) ########"

echo "=== 0. chain up + adapter strong (900/2s/8) ==="
docker unpause subnet-node1 ledger-adapter >/dev/null 2>&1 || true
BATCH_SIZE=900 BATCH_TIMEOUT=2s PIPELINE_WINDOW=8 docker compose up -d ledger-adapter >/dev/null 2>&1
for i in $(seq 1 30); do
  docker logs ledger-adapter 2>&1 | grep -q '"batch_size":900' && \
  docker exec wms-service sh -c 'curl -s http://localhost:8080/health' 2>/dev/null | grep -qE '"status":"(ok|degraded)"' && { echo "  adapter+wms ready (try $i)"; break; }
  sleep 2
done
echo "  adapter: $(docker logs ledger-adapter 2>&1 | grep 'config loaded' | tail -1 | grep -oE '"batch_size":[0-9]+,"batch_timeout":[0-9]+,"pipeline_window":[0-9]+')"
echo "  running: $(docker ps --filter status=running --format '{{.Names}}' | tr '\n' ' ')"

echo "=== 1. cleanup + multi-SKU seed (N_SKU=$SKU_COUNT N_ITEMS=$N_ITEMS) ==="
docker exec -i postgres_db psql -U root -d wms_blockchain_db < tests/stress/setup/stress-cleanup.sql >/dev/null 2>&1
sed -e "s/N_SKU [0-9][0-9]*/N_SKU ${SKU_COUNT}/" -e "s/N_ITEMS [0-9][0-9]*/N_ITEMS ${N_ITEMS}/" \
  tests/stress/setup/stress-throughput-seed.sql > /tmp/tput-seed.sql
docker exec -i postgres_db psql -U root -d wms_blockchain_db < /tmp/tput-seed.sql 2>&1 | tail -1
echo "  RECEIVED_AT_GATE: $(PSQL "SELECT count(*) FROM wms_inventory.cargoplaces WHERE cargoplace_code LIKE 'STRESS-TPUT-CP-%' AND status='RECEIVED_AT_GATE'") | SKU: $(PSQL "SELECT count(*) FROM wms_inventory.skus WHERE name LIKE 'STRESS-TPUT-SKU-%'")"

echo "=== 2. fixtures ==="
RECV=$(PSQL "SELECT b.bin_id FROM wms_inventory.bins b JOIN wms_inventory.warehouses w ON w.warehouse_id=b.warehouse_id WHERE b.code='BUFFER-01' AND w.name='Склад Москва-Север' LIMIT 1")
STOR=$(PSQL "SELECT b.bin_id FROM wms_inventory.bins b JOIN wms_inventory.warehouses w ON w.warehouse_id=b.warehouse_id WHERE b.code='A-01-01' AND w.name='Склад Москва-Север' LIMIT 1")
RAW "SELECT COALESCE(json_agg(cargoplace_id ORDER BY cargoplace_code),'[]') FROM wms_inventory.cargoplaces WHERE cargoplace_code LIKE 'STRESS-TPUT-CP-%'" > /tmp/stress-tput-cps.json
RAW "SELECT COALESCE(json_agg(j ORDER BY code),'[]') FROM (
  SELECT d.code AS code, json_build_object(
    'destinationId', d.destination_id, 'shippingBinId', b.bin_id,
    'dispatchCodes', (SELECT COALESCE(json_agg(od.dispatch_code ORDER BY od.dispatch_code),'[]')
                      FROM wms_inventory.outbound_dispatches od
                      WHERE od.destination_id=d.destination_id AND od.dispatch_code LIKE 'STRESS-TPUT-DSP-%')
  ) AS j FROM wms_inventory.destinations d
  JOIN wms_inventory.bins b ON b.destination_id=d.destination_id AND b.section='SHIPPING_BUFFER'
  WHERE d.code LIKE 'SHOP-T%') sub" > /tmp/stress-tput-dests.json
echo "  RECV=$RECV STOR=$STOR | dests: $(grep -o destinationId /tmp/stress-tput-dests.json | wc -l | tr -d ' ')"

echo "=== 3. baseline onchain/outbox ==="
OC_BASE=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='COMMITTED'")
OB_BASE=$(PSQL "SELECT count(*) FROM public.outbox_events")
echo "  committed_base=$OC_BASE outbox_base=$OB_BASE"

echo "=== 4. run 09 (chain ON) + LIVE committed/backlog sampler (3s) ==="
: > /tmp/e2e-live.log
( for i in $(seq 1 800); do
    ts=$(date +%s)
    comm=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='COMMITTED'")
    outb=$(PSQL "SELECT count(*) FROM public.outbox_events")
    pick=$(PSQL "SELECT count(*) FROM wms_inventory.products WHERE qr_code LIKE '$QR' AND status IN ('READY_TO_SHIP','SHIPPED')")
    echo "$ts,$((comm-OC_BASE)),$((outb-OB_BASE)),$((outb-OB_BASE-(comm-OC_BASE))),$pick" >> /tmp/e2e-live.log
    sleep 3
  done ) & SLPID=$!
trap '{ kill $SLPID; } 2>/dev/null' EXIT

RUN_START=$(date +%s)
k6 run --no-usage-report --quiet \
  -e VUS="$VUS" -e ITERS="$ITERS" -e MAX_DURATION="$MAX_DURATION" \
  -e PICK_BATCH="$PICK_BATCH" -e ALLOC_EVERY="$ALLOC_EVERY" -e THINK_MS=0 \
  -e SKU_COUNT="$SKU_COUNT" -e OPERATOR_COUNT="$VUS" \
  -e RECEIVING_BIN_ID="$RECV" -e STORAGE_BIN_ID="$STOR" \
  tests/stress/09-throughput.js > /tmp/e2e-k6.log 2>&1
RUN_END=$(date +%s); WALL=$((RUN_END-RUN_START))
echo "  k6 wall: ${WALL}s (front-генерация закончилась, дренируем хвост)"
grep -iE "tput_items_stored|tput_items_picked|iterations\.\.|http_req_failed|teardown:|level=error" /tmp/e2e-k6.log | head -10

# Дренируем хвост: продолжаем семплить committed/backlog пока backlog→0
for i in $(seq 1 120); do
  ts=$(date +%s)
  comm=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='COMMITTED'")
  outb=$(PSQL "SELECT count(*) FROM public.outbox_events")
  sent=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='SENT'")
  bk=$(PSQL "SELECT count(*) FROM public.outbox_events oe WHERE NOT EXISTS (SELECT 1 FROM public.onchain_events oc WHERE oc.event_id=oe.event_id)")
  echo "$ts,$((comm-OC_BASE)),$((outb-OB_BASE)),$bk,DRAIN" >> /tmp/e2e-live.log
  [ "${bk:-1}" -le 2 ] && [ "${sent:-1}" -le 0 ] && break
  sleep 3
done
{ kill $SLPID; wait $SLPID; } 2>/dev/null

echo "=== 5. LIVE-таблица (ts_rel, committed, generated, backlog, picked) ==="
awk -F, -v ws="$RUN_START" 'NR==1{t0=$1} { rel=$1-t0; tag=(NF>=5 && $5=="DRAIN")?"DRN":""; printf "  +%-3ss  committed=%-7s generated=%-7s backlog=%-7s picked=%-7s %s\n", rel, $2, $3, $4, $5, tag }' /tmp/e2e-live.log

echo "=== 6. RESULTS ==="
# committed-rate в окне РАЗГОНА→конца прогона (live, пока фронт генерил): берём первый и
# последний сэмпл ВНУТРИ wall-окна (rel<=WALL), считаем наклон committed.
awk -F, -v wall="$WALL" -v rs="$RUN_START" '
  $1<=rs+wall {
    if(first=="" && $2>0){first=$2; ft=$1}
    last=$2; lt=$1
  }
  END{
    if(lt>ft && first!="") printf "  committed-rate ВО ВРЕМЯ прогона (live, chain on): %.0f переходов/с (%d→%d за %dс)\n", (last-first)/(lt-ft), first, last, lt-ft
    else print "  (мало точек для наклона во время прогона)"
  }' /tmp/e2e-live.log
# Пиковый committed-rate в любом 3с-окне (включая дренаж).
awk -F, 'NR>1{ if(p!=""){ d=$2-p; dt=$1-pt; if(dt>0){r=d/dt; if(r>mx)mx=r} } p=$2; pt=$1 } END{ printf "  пиковый committed-rate (любое окно): %.0f переходов/с\n", mx }' /tmp/e2e-live.log
# Backlog: рос или плато?
awk -F, 'NR>1 && $5!="DRAIN"{ if($4>mxb)mxb=$4; lb=$4 } END{ printf "  backlog во время прогона: max=%s, последний(до дренажа)=%s\n", mxb, lb }' /tmp/e2e-live.log
echo "  front: $(grep tput_items_picked /tmp/e2e-k6.log | grep -oE '[0-9.]+/s' | head -1) picked"

echo "=== 7. итог onchain + consistency + статусы ==="
PSQL "SELECT 'committed='||count(*) FROM public.onchain_events WHERE status='COMMITTED'" | sed 's/^/  /'
PSQL "SELECT '  failed='||count(*)||' sent='||(SELECT count(*) FROM public.onchain_events WHERE status='SENT') FROM public.onchain_events WHERE status='FAILED'"
SHIP_WMS=$(PSQL "SELECT count(*) FROM wms_inventory.products WHERE qr_code LIKE '$QR' AND status='SHIPPED'")
SHIP_CH=$(PSQL "SELECT count(*) FROM public.onchain_events oe JOIN public.outbox_events o2 ON o2.event_id=oe.event_id WHERE oe.status='COMMITTED' AND o2.aggregate_type='shipping'")
echo "  SHIPPED(wms)=$SHIP_WMS  shipping COMMITTED(chain)=$SHIP_CH  -> $([ "$SHIP_WMS" = "$SHIP_CH" ] && echo OK || echo 'note: chain>wms includes prior runs')"
echo "######## DONE ########"
