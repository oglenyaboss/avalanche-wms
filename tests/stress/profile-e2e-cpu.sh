#!/bin/bash
# profile-e2e-cpu.sh — instrumented clean e2e: committed-items/backlog table + PER-CONTAINER CPU split.
# Goal: see EXACTLY where the 10 cores go during a co-resident run, so we pick the right lever
# (front scan-overhead vs chain vs kafka/debezium vs k6-host tax) to push sustained committed→1500.
# Run goes through the SIGNED API (draft → P-256 software signature → submit); the metric unit
# is item transitions of COMMITTED signed actions (see section 8 for per-action losses).
#
# Usage: ./profile-e2e-cpu.sh [VUS]   env: SKU_COUNT=70 N_ITEMS=24000
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1
export PATH="$HOME/.foundry/bin:$PATH"
export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-stresstest}"

VUS="${1:-70}"; SKU_COUNT="${SKU_COUNT:-70}"; N_ITEMS="${N_ITEMS:-24000}"
PER_CP="${PER_CP:-10}"                       # товаров на грузоместо (реалистичная паллета)
DISP="${DISP:-50}"                           # рейсов на магазин (для длинного soak поднять: ~ticks/dest)
N_CP=$(( N_ITEMS / PER_CP ))                 # грузомест = итераций k6
ITERS="${ITERS:-$N_CP}"; MAX_DURATION="${MAX_DURATION:-15m}"
PICK_BATCH="${PICK_BATCH:-12}"; ALLOC_EVERY="${ALLOC_EVERY:-4}"
BATCH_SIZE="${BATCH_SIZE:-900}"; PIPELINE_WINDOW="${PIPELINE_WINDOW:-8}"; BATCH_TIMEOUT="${BATCH_TIMEOUT:-2s}"
QR="STRESS-QR-TPUT-%"
PSQL(){ docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "$1" 2>/dev/null | tr -d '[:space:]'; }
RAW(){  docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "$1" 2>/dev/null; }
CONTAINERS="postgres_db wms-service kafka debezium-connect ledger-adapter subnet-node1"

# Сырые результаты прогона: весь stdout + логи семплеров и k6 (коммитятся).
OUT="tests/stress/results/$(date -u +%Y%m%d-%H%M%S)-$(git rev-parse --short HEAD)"
mkdir -p "$OUT"
exec > >(tee "$OUT/summary.txt") 2>&1

# Операторы с активными ключами — prerequisite подписанного прогона (#20).
OPS_FILE="tests/stress/.signed/operators.json"
if [ ! -s "$OPS_FILE" ]; then
  echo "нет $OPS_FILE — run: cd tests/e2e && go run ./cmd/stress-provision -operators $VUS" >&2
  exit 1
fi
if [ "$(grep -o '"username"' "$OPS_FILE" | wc -l)" -lt "$VUS" ]; then
  echo "в $OPS_FILE меньше $VUS операторов — run: cd tests/e2e && go run ./cmd/stress-provision -operators $VUS" >&2
  exit 1
fi

echo "######## E2E+CPU — VUS=$VUS SKU=$SKU_COUNT ITEMS=$N_ITEMS PER_CP=$PER_CP CP=$N_CP adapter=$BATCH_SIZE/$BATCH_TIMEOUT/$PIPELINE_WINDOW ########"

echo "=== 0. chain up + adapter ($BATCH_SIZE/$BATCH_TIMEOUT/$PIPELINE_WINDOW) ==="
docker unpause subnet-node1 ledger-adapter >/dev/null 2>&1 || true
BATCH_SIZE=$BATCH_SIZE BATCH_TIMEOUT=$BATCH_TIMEOUT PIPELINE_WINDOW=$PIPELINE_WINDOW docker compose up -d ledger-adapter >/dev/null 2>&1
for i in $(seq 1 30); do
  docker logs ledger-adapter 2>&1 | grep -q "\"batch_size\":$BATCH_SIZE" && \
  docker exec wms-service sh -c 'curl -s http://localhost:8080/health' 2>/dev/null | grep -qE '"status":"(ok|degraded)"' && { echo "  ready (try $i)"; break; }
  sleep 2
done
echo "  adapter: $(docker logs ledger-adapter 2>&1 | grep 'config loaded' | tail -1 | grep -oE '"batch_size":[0-9]+,"batch_timeout":[0-9]+,"pipeline_window":[0-9]+')"

echo "=== 1. cleanup + multi-SKU seed (N_CP=$N_CP × PER_CP=$PER_CP = $N_ITEMS товаров) ==="
docker exec -i postgres_db psql -U root -d wms_blockchain_db < tests/stress/setup/stress-cleanup.sql >/dev/null 2>&1
sed -e "s/N_SKU [0-9][0-9]*/N_SKU ${SKU_COUNT}/" -e "s/N_ITEMS [0-9][0-9]*/N_ITEMS ${N_ITEMS}/" \
    -e "s/N_PER_CP [0-9][0-9]*/N_PER_CP ${PER_CP}/" -e "s/N_CP [0-9][0-9]*/N_CP ${N_CP}/" \
    -e "s/N_DISP_PER_DEST [0-9][0-9]*/N_DISP_PER_DEST ${DISP}/" \
  tests/stress/setup/stress-throughput-seed.sql > /tmp/tput-seed.sql
docker exec -i postgres_db psql -U root -d wms_blockchain_db < /tmp/tput-seed.sql 2>&1 | tail -1
echo "  RECEIVED_AT_GATE(грузомест): $(PSQL "SELECT count(*) FROM wms_inventory.cargoplaces WHERE cargoplace_code LIKE 'STRESS-TPUT-CP-%' AND status='RECEIVED_AT_GATE'") | SKU: $(PSQL "SELECT count(*) FROM wms_inventory.skus WHERE name LIKE 'STRESS-TPUT-SKU-%'") | orders: $(PSQL "SELECT count(*) FROM wms_inventory.orders WHERE external_order_no LIKE 'STRESS-TPUT-ORD-%'")"

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

echo "=== 3. baseline (item transitions) ==="
OC_BASE=$(PSQL "SELECT COALESCE(sum(cardinality(wa.item_ids)),0) FROM public.warehouse_actions wa JOIN public.onchain_events oe ON oe.event_id = wa.action_id WHERE oe.status='COMMITTED'")
GEN_BASE=$(PSQL "SELECT COALESCE(sum(cardinality(item_ids)),0) FROM public.warehouse_actions WHERE submitted_at IS NOT NULL")
echo "  committed_items_base=$OC_BASE generated_items_base=$GEN_BASE"

echo "=== 4. run + committed-items sampler (4s) + CPU sampler (2s) ==="
: > /tmp/e2e-live.log; : > /tmp/e2e-cpu.log
# committed items / generated items / backlog sampler (единица — переходы товаров)
( for i in $(seq 1 400); do
    ts=$(date +%s)
    comm=$(PSQL "SELECT COALESCE(sum(cardinality(wa.item_ids)),0) FROM public.warehouse_actions wa JOIN public.onchain_events oe ON oe.event_id = wa.action_id WHERE oe.status='COMMITTED'")
    gen=$(PSQL "SELECT COALESCE(sum(cardinality(item_ids)),0) FROM public.warehouse_actions WHERE submitted_at IS NOT NULL")
    echo "$ts,$((comm-OC_BASE)),$((gen-GEN_BASE)),$((gen-GEN_BASE-(comm-OC_BASE)))" >> /tmp/e2e-live.log
    sleep 4
  done ) & SLPID=$!
# CPU sampler: per-container docker stats + k6 host cpu (optional — CPU_SAMPLE=0 to skip for a lean run)
CPUPID=""
if [ "${CPU_SAMPLE:-1}" = "1" ]; then
( while true; do
    ts=$(date +%s)
    stats=$(docker stats --no-stream --format '{{.Name}}={{.CPUPerc}}' $CONTAINERS 2>/dev/null | tr '\n' ';' | tr -d ' %')
    k6cpu=$(ps -A -o %cpu=,comm= 2>/dev/null | awk 'tolower($0) ~ /k6/ {s+=$1} END{printf "%.0f", s}')
    echo "$ts;k6=$k6cpu;$stats" >> /tmp/e2e-cpu.log
  done ) & CPUPID=$!
fi
trap '{ kill $SLPID $CPUPID; } 2>/dev/null' EXIT

RUN_START=$(date +%s)
k6 run --no-usage-report --quiet \
  -e VUS="$VUS" -e ITERS="$ITERS" -e MAX_DURATION="$MAX_DURATION" \
  -e PICK_BATCH="$PICK_BATCH" -e ALLOC_EVERY="$ALLOC_EVERY" -e THINK_MS=0 \
  -e SKU_COUNT="$SKU_COUNT" -e PRODUCTS_PER_CP="$PER_CP" -e SHIP_EVERY="${SHIP_EVERY:-4}" \
  -e PACE_RPS="${PACE_RPS:-0}" -e PACE_DURATION="${PACE_DURATION:-150s}" \
  -e RECEIVING_BIN_ID="$RECV" -e STORAGE_BIN_ID="$STOR" \
  tests/stress/09-throughput.js > /tmp/e2e-k6.log 2>&1
RUN_END=$(date +%s); WALL=$((RUN_END-RUN_START))
echo "  k6 wall: ${WALL}s"
grep -iE "tput_items_stored|tput_items_picked|tput_items_shipped|iterations\.\.|http_req_failed|teardown:" /tmp/e2e-k6.log | head -8
{ kill $SLPID $CPUPID; } 2>/dev/null

echo "=== 5. committed items table ==="
awk -F, 'NR==1{t0=$1} { rel=$1-t0; printf "  +%-3ss  committed_items=%-9s generated_items=%-9s backlog=%-7s\n", rel, $2, $3, $4 }' /tmp/e2e-live.log

echo "=== 6. committed-items rate during run ==="
# (a) полное окно (с рампой) — как раньше, для сопоставимости
awk -F, -v rs="$RUN_START" -v wall="$WALL" '
  $1<=rs+wall { if(first=="" && $2>0){first=$2; ft=$1} last=$2; lt=$1 }
  END{ if(lt>ft && first!="") printf "  committed-items rate (всё окно, вкл. прогрев): %.0f /s (%d→%d за %dс)\n",(last-first)/(lt-ft),first,last,lt-ft }' /tmp/e2e-live.log
# (b) УСТОЙЧИВОЕ окно: пропускаем первые WARMUP с (чейн заполняет pipeline-окно ~15с),
#     конец = конец генерации (rel<=WALL). Это и есть честный sustained committed.
WARMUP="${WARMUP:-20}"
awk -F, -v rs="$RUN_START" -v wall="$WALL" -v wu="$WARMUP" '
  { rel=$1-rs }
  rel>=wu && rel<=wall { if(first==""){first=$2; ft=$1} last=$2; lt=$1 }
  END{ if(lt>ft) printf "  committed-items rate (sustained, после +%dс прогрева): %.0f /s (%d→%d за %dс)\n",wu,(last-first)/(lt-ft),first,last,lt-ft }' /tmp/e2e-live.log
# (c) лучшее непрерывное 30с-окно (скользящее) — для «видно ≥1500»
awk -F, -v rs="$RUN_START" -v wall="$WALL" '
  $1<=rs+wall { ts[n]=$1; cm[n]=$2; n++ }
  END{ best=0; for(i=0;i<n;i++){ for(j=i+1;j<n;j++){ if(ts[j]-ts[i]>=28 && ts[j]-ts[i]<=34){ r=(cm[j]-cm[i])/(ts[j]-ts[i]); if(r>best)best=r } } } if(best>0) printf "  committed-items rate (лучшее ~30с-окно): %.0f /s\n",best }' /tmp/e2e-live.log
awk -F, 'NR>1{ if(p!=""){ d=$2-p; dt=$1-pt; if(dt>0){r=d/dt; if(r>mx)mx=r} } p=$2; pt=$1 } END{ printf "  peak committed-items rate (одно окно): %.0f /s\n", mx }' /tmp/e2e-live.log

echo "=== 7. CPU split (avg over steady window, skip first 20s ramp) — % of one core; box=1000% ==="
awk -F';' -v rs="$RUN_START" -v wall="$WALL" '
  { ts=$1; if(ts < rs+20 || ts > rs+wall) next; n++
    for(i=2;i<=NF;i++){ split($i,kv,"="); if(kv[1]!=""){ sum[kv[1]]+=kv[2]; cnt[kv[1]]++ } } }
  END{
    if(n==0){print "  (no steady samples)"; exit}
    total=0
    # stable order
    order="k6 postgres_db wms-service kafka debezium-connect ledger-adapter subnet-node1"
    m=split(order,O," ")
    for(j=1;j<=m;j++){ k=O[j]; if(cnt[k]>0){ a=sum[k]/cnt[k]; total+=a; printf "  %-18s %6.0f%%  (%.1f cores)\n",k,a,a/100 } }
    printf "  %-18s %6.0f%%  (%.1f cores)  [box=1000%%]\n","TOTAL",total,total/100
    printf "  samples=%d over steady window\n", n
  }' /tmp/e2e-cpu.log

echo "=== 8. losses ==="
echo "  окно: warehouse_actions.created_at >= to_timestamp($RUN_START)"
ACTIONS_SUBMITTED=$(PSQL "SELECT count(*) FROM public.warehouse_actions WHERE submitted_at IS NOT NULL AND created_at >= to_timestamp($RUN_START)")
ACTIONS_COMMITTED=$(PSQL "SELECT count(*) FROM public.warehouse_actions wa JOIN public.onchain_events oe ON oe.event_id = wa.action_id WHERE oe.status='COMMITTED' AND wa.created_at >= to_timestamp($RUN_START)")
ACTIONS_REJECTED=$(PSQL "SELECT count(*) FROM public.warehouse_actions wa JOIN public.onchain_events oe ON oe.event_id = wa.action_id WHERE oe.status='FAILED' AND oe.reject_reason IS NOT NULL AND wa.created_at >= to_timestamp($RUN_START)")
ACTIONS_FAILED=$(PSQL "SELECT count(*) FROM public.warehouse_actions wa JOIN public.onchain_events oe ON oe.event_id = wa.action_id WHERE oe.status='FAILED' AND oe.reject_reason IS NULL AND wa.created_at >= to_timestamp($RUN_START)")
ACTIONS_PENDING=$(PSQL "SELECT count(*) FROM public.warehouse_actions wa LEFT JOIN public.onchain_events oe ON oe.event_id = wa.action_id WHERE wa.submitted_at IS NOT NULL AND wa.created_at >= to_timestamp($RUN_START) AND (oe.event_id IS NULL OR oe.status IN ('PENDING','SENT'))")
echo "  submitted: $ACTIONS_SUBMITTED | COMMITTED: $ACTIONS_COMMITTED | rejected (reject_reason): $ACTIONS_REJECTED | failed (tx потерян): $ACTIONS_FAILED | still pending: $ACTIONS_PENDING"
echo "  items committed по типам действий:"
RAW "SELECT wa.action_type, count(*), COALESCE(sum(cardinality(wa.item_ids)),0) FROM public.warehouse_actions wa JOIN public.onchain_events oe ON oe.event_id = wa.action_id WHERE oe.status='COMMITTED' AND wa.created_at >= to_timestamp($RUN_START) GROUP BY wa.action_type ORDER BY wa.action_type" \
  | awk -F'|' '{ name=($1==1?"receive":$1==2?"putaway":$1==3?"pick":$1==4?"ship":"?"); printf "    %s (type %s): actions=%s items=%s\n", name, $1, $2, $3 }'

echo "=== 9. gas per item ==="
if ! command -v cast >/dev/null 2>&1; then
  echo "  cast не найден (foundry) — секция пропущена"
else
  # RPC — как в run-throughput.sh/reset-chain.sh: URL из shared-тома (его пишет subnet-init),
  # хост-адрес вместо subnet-node1 (в образе adapter distroless — без cat/shell).
  RPC_VOL="${COMPOSE_PROJECT_NAME}_shared_state"
  RPC="${RPC_URL:-$(docker run --rm -v "${RPC_VOL}:/s" busybox cat /s/rpc_url.txt 2>/dev/null | tr -d '[:space:]' | sed 's#subnet-node1:9650#localhost:9650#')}"
  if [ -z "$RPC" ]; then
    echo "  RPC не определён (RPC_URL или shared_state/rpc_url.txt) — секция пропущена"
  else
    mapfile -t TXS < <(RAW "SELECT DISTINCT oe.tx_hash FROM public.onchain_events oe JOIN public.warehouse_actions wa ON wa.action_id = oe.event_id WHERE oe.status='COMMITTED' AND oe.tx_hash IS NOT NULL AND wa.created_at >= to_timestamp($RUN_START) LIMIT 20")
    if [ "${#TXS[@]}" -eq 0 ] || [ -z "${TXS[0]}" ]; then
      echo "  нет COMMITTED tx за прогон"
    else
      declare -A GAS_BY_TYPE=() ITEMS_BY_TYPE=()
      for tx in "${TXS[@]}"; do
        [ -z "$tx" ] && continue
        gas=$(cast receipt "$tx" gasUsed --rpc-url "$RPC" 2>/dev/null | tr -d '[:space:]')
        if [ -z "$gas" ]; then echo "  tx ${tx:0:18}…: gasUsed недоступен (RPC=$RPC)"; continue; fi
        IFS='|' read -r acts items tmin tmax <<< "$(RAW "SELECT count(*), COALESCE(sum(cardinality(wa.item_ids)),0), COALESCE(min(wa.action_type),0), COALESCE(max(wa.action_type),0) FROM public.onchain_events oe JOIN public.warehouse_actions wa ON wa.action_id = oe.event_id WHERE oe.tx_hash='$tx' AND wa.created_at >= to_timestamp($RUN_START)")"
        if [ "${items:-0}" -gt 0 ] 2>/dev/null; then
          printf "  tx %s…: gas=%s actions=%s items=%s gas/item=%.0f\n" "${tx:0:18}" "$gas" "$acts" "$items" "$(awk -v g="$gas" -v i="$items" 'BEGIN{printf "%.0f", g/i}')"
        else
          printf "  tx %s…: gas=%s actions=%s items=0\n" "${tx:0:18}" "$gas" "$acts"
        fi
        # mean gas/item по типу — только однотипные tx
        if [ "${tmin:-0}" -gt 0 ] 2>/dev/null && [ "$tmin" = "$tmax" ] && [ "${items:-0}" -gt 0 ] 2>/dev/null; then
          GAS_BY_TYPE[$tmax]=$(( ${GAS_BY_TYPE[$tmax]:-0} + gas ))
          ITEMS_BY_TYPE[$tmax]=$(( ${ITEMS_BY_TYPE[$tmax]:-0} + items ))
        fi
      done
      for t in "${!GAS_BY_TYPE[@]}"; do
        case "$t" in 1) name=receive;; 2) name=putaway;; 3) name=pick;; 4) name=ship;; *) name="?";; esac
        printf "  mean gas/item, тип %s: %s\n" "$name" "$(awk -v g="${GAS_BY_TYPE[$t]}" -v i="${ITEMS_BY_TYPE[$t]}" 'BEGIN{printf "%.0f", g/i}')"
      done
    fi
  fi
fi

echo "=== 10. raw results ==="
cp /tmp/e2e-live.log /tmp/e2e-cpu.log /tmp/e2e-k6.log "$OUT/" 2>/dev/null
echo "  RAW RESULTS: $OUT (summary.txt, e2e-live.log, e2e-cpu.log, e2e-k6.log)"
echo "######## DONE ########"
