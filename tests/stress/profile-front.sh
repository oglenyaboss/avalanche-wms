#!/bin/bash
# profile-front.sh — изоляция и профилирование PER-ITEM фронт-потолка WMS.
#
# Чейн (subnet-node1 + ledger-adapter) ПАУЗИТСЯ (docker pause → 0% CPU, без
# срабатывания restart-политики) → меряем ЧИСТЫЙ ingestion фронта
# (receive→putaway→pick→outbox) без конкуренции за 10 ядер от avalanchego.
# В проде фронт и чейн на разных хостах — это и есть его «интринсик» потолок.
#
# Профайлер каждые 2с:
#   - pg_stat_activity по (wait_event_type, wait_event): различает
#       CPU|running  = бэкенд молотит на CPU (БД CPU-bound)
#       Lock         = ждёт row-lock (контеншн на строках)
#       LWLock:WAL*  = ждёт запись WAL (write/IO-bound)
#       IO:*         = ждёт диск
#       Client:ClientRead = БД ждёт приложение → узкое место в WMS-аппе/сети, НЕ в БД
#   - docker stats CPU% по контейнерам (wms/postgres/kafka/debezium)
#
# Usage: ./profile-front.sh [VUS]      (default 70; 14000 айтемов/70 маг из сида)
#   env: ITERS=14000 MAX_DURATION=10m PICK_BATCH=4 ALLOC_EVERY=10
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1
export PATH="$HOME/.foundry/bin:$PATH"
export COMPOSE_PROJECT_NAME=stresstest

VUS="${1:-70}"
ITERS="${ITERS:-14000}"
MAX_DURATION="${MAX_DURATION:-10m}"
PICK_BATCH="${PICK_BATCH:-4}"; ALLOC_EVERY="${ALLOC_EVERY:-10}"
SKU_COUNT="${SKU_COUNT:-1}"      # разнос товаров по N SKU (1 = одно-SKU). Синхронит сид и бенч.
QR="STRESS-QR-TPUT-%"
PSQL(){ docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "$1" 2>/dev/null | tr -d '[:space:]'; }
RAW(){  docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "$1" 2>/dev/null; }

echo "######## FRONT PROFILE — VUS=$VUS (chain PAUSED) ########"

echo "=== 0. пауза чейна (free cores) ==="
docker pause subnet-node1 ledger-adapter >/dev/null 2>&1
echo "  paused: subnet-node1, ledger-adapter"
echo "  up/active: $(docker ps --filter status=running --format '{{.Names}}' | tr '\n' ' ')"

echo "=== 1. cleanup + reseed (нужны RECEIVED_AT_GATE айтемы; N_SKU=$SKU_COUNT) ==="
docker exec -i postgres_db psql -U root -d wms_blockchain_db < tests/stress/setup/stress-cleanup.sql >/dev/null 2>&1
# Временный сид с нужным N_SKU (оригинал не трогаем; \set в файле перебивает -v, поэтому sed).
sed "s/N_SKU [0-9][0-9]*/N_SKU ${SKU_COUNT}/" tests/stress/setup/stress-throughput-seed.sql > /tmp/tput-seed.sql
docker exec -i postgres_db psql -U root -d wms_blockchain_db < /tmp/tput-seed.sql 2>&1 | tail -1
echo "  cargoplaces RECEIVED_AT_GATE: $(PSQL "SELECT count(*) FROM wms_inventory.cargoplaces WHERE cargoplace_code LIKE 'STRESS-TPUT-CP-%' AND status='RECEIVED_AT_GATE'")"
echo "  SKU STRESS-TPUT-SKU-*:        $(PSQL "SELECT count(*) FROM wms_inventory.skus WHERE name LIKE 'STRESS-TPUT-SKU-%'")"
echo "  orders NEW across dest:        $(PSQL "SELECT count(DISTINCT destination_id) FROM wms_inventory.orders WHERE external_order_no LIKE 'STRESS-TPUT-ORD-%' AND status='NEW'")"
echo "  pool: $(docker exec wms-service env 2>/dev/null | grep WMS_DB_MAX_CONNS)"

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

echo "=== 3. run 09 + профайлер (pg_stat_activity + docker CPU) ==="
: > /tmp/front-prof.log
: > /tmp/front-slope.log
# (a) Профайлер БД: чем заняты активные клиентские бэкенды (wait_event) + горячие запросы.
( for i in $(seq 1 600); do
    ts=$(date +%s)
    RAW "SELECT 'WAIT|'||COALESCE(wait_event_type,'CPU')||'|'||COALESCE(wait_event,'running')||'|'||count(*)
         FROM pg_stat_activity
         WHERE state='active' AND pid<>pg_backend_pid() AND backend_type='client backend'
         GROUP BY wait_event_type, wait_event" | sed "s/^/$ts /" >> /tmp/front-prof.log
    RAW "SELECT 'QRY|'||count(*)||'|'||regexp_replace(left(query,80),'[[:space:]]+',' ','g')
         FROM pg_stat_activity
         WHERE state='active' AND pid<>pg_backend_pid() AND backend_type='client backend'
         GROUP BY regexp_replace(left(query,80),'[[:space:]]+',' ','g')" | sed "s/^/$ts /" >> /tmp/front-prof.log
    sleep 2
  done ) & DBPID=$!
# (b) Семплер throughput фронта (наклон picked = items/с).
( for i in $(seq 1 600); do
    ts=$(date +%s)
    st=$(PSQL "SELECT count(*) FROM wms_inventory.products WHERE qr_code LIKE '$QR' AND status IN ('STORED','ALLOCATED','ASSEMBLED','READY_TO_SHIP','SHIPPED')")
    pk=$(PSQL "SELECT count(*) FROM wms_inventory.products WHERE qr_code LIKE '$QR' AND status IN ('READY_TO_SHIP','SHIPPED')")
    echo "$ts,$st,$pk" >> /tmp/front-slope.log
    sleep 3
  done ) & SLPID=$!
# (c) Семплер CPU контейнеров (docker stats --no-stream ~ 1.5с) + k6 на ХОСТЕ
#     (k6 не контейнер; ворует ядра у системы — считаем его как "k6-host").
( for i in $(seq 1 300); do
    ts=$(date +%s)
    docker stats --no-stream --format '{{.Name}} {{.CPUPerc}}' 2>/dev/null \
      | grep -iE 'wms-service|postgres_db|kafka|debezium' | sed "s/^/$ts CPU /" >> /tmp/front-prof.log
    k6c=$(ps -A -o %cpu=,comm= 2>/dev/null | awk 'tolower($2) ~ /k6/ {s+=$1} END{printf "%.0f", s+0}')
    echo "$ts CPU k6-host ${k6c}%" >> /tmp/front-prof.log
  done ) & CPUPID=$!

trap '{ kill $DBPID $SLPID $CPUPID; } 2>/dev/null' EXIT

RUN_START=$(date +%s)
k6 run --no-usage-report --quiet \
  -e VUS="$VUS" -e ITERS="$ITERS" -e MAX_DURATION="$MAX_DURATION" \
  -e PICK_BATCH="$PICK_BATCH" -e ALLOC_EVERY="$ALLOC_EVERY" -e THINK_MS=0 \
  -e SKU_COUNT="$SKU_COUNT" \
  -e OPERATOR_COUNT="$VUS" -e RECEIVING_BIN_ID="$RECV" -e STORAGE_BIN_ID="$STOR" \
  tests/stress/09-throughput.js > /tmp/front-k6.log 2>&1
RUN_END=$(date +%s)
{ kill $DBPID $SLPID $CPUPID; wait $DBPID $SLPID $CPUPID; } 2>/dev/null
WALL=$((RUN_END-RUN_START))

echo "  k6 wall: ${WALL}s"
grep -iE "tput_items_stored|tput_items_picked|iterations\.\.|iteration_duration|http_req_failed|teardown:|level=error" /tmp/front-k6.log | head -20

echo "=== 4. RESULTS: front throughput ==="
SLOPE=$(awk -F, 'NR==1{t0=$1;p0=$3} {t1=$1;p1=$3} END{ if(t1>t0) printf "%.1f",(p1-p0)/(t1-t0); else print "0" }' /tmp/front-slope.log)
echo "  picked slope (items/с): $SLOPE"
echo "  --- slope (ts,done,picked) ---"; awk -F, 'NR%2==1{print "    "$0}' /tmp/front-slope.log

echo "=== 5. PROFILE: чем заняты бэкенды БД (сумма active-сэмплов, % по wait) ==="
echo "    Client:ClientRead↑ => БД ждёт WMS-апп (узкое во фронте, не в БД)"
echo "    CPU:running↑        => БД CPU-bound | LWLock:WAL*↑ => WAL/IO | Lock↑ => row-контеншн"
awk '/WAIT\|/{ split($2,a,"|"); key=a[2]":"a[3]; c[key]+=a[4]; tot+=a[4] }
     END{ for(k in c) printf "%.1f|%d|%s\n",100*c[k]/tot,c[k],k }' /tmp/front-prof.log \
  | sort -t'|' -k1 -rn | head -12 \
  | while IFS='|' read pct cnt key; do printf "    %6s%%  n=%-6s  %s\n" "$pct" "$cnt" "$key"; done

echo "=== 5b. PROFILE: горячие запросы (доля active-сэмплов = доля CPU postgres) ==="
awk '/ QRY\|/{ line=$0; sub(/^[0-9]+ QRY\|/,"",line); p=index(line,"|");
               cnt=substr(line,1,p-1); q=substr(line,p+1); c[q]+=cnt; tot+=cnt }
     END{ for(k in c) printf "%.1f|%d|%s\n",100*c[k]/tot,c[k],k }' /tmp/front-prof.log \
  | sort -t'|' -k1 -rn | head -12 \
  | while IFS='|' read pct cnt q; do printf "    %6s%%  n=%-6s  %s\n" "$pct" "$cnt" "$q"; done

echo "=== 6. PROFILE: CPU по контейнерам (avg/peak; хост = 10 ядер = 1000%) ==="
awk '$2=="CPU"{ n=$3; v=$4; gsub(/%/,"",v); sum[n]+=v; cnt[n]++; if(v+0>mx[n]+0)mx[n]=v }
     END{ for(k in sum) printf "    %-16s avg=%7.1f%%  peak=%7.1f%%\n", k, sum[k]/cnt[k], mx[k] }' /tmp/front-prof.log

echo "=== restore: docker unpause subnet-node1 ledger-adapter (ВРУЧНУЮ после анализа) ==="
echo "######## DONE ########"
