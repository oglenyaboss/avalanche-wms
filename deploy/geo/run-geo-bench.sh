#!/usr/bin/env bash
# run-geo-bench.sh — SUSTAINED load + full node-metrics bench of the WMS pipeline against the
# geo-distributed Subnet-EVM chain (dino MD + alex RO + itldc NL), with a mid-run kill-1-of-3
# fault window. Produces a run-scoped JSON + CSV time-series for the geo report and UI.
#
# What it does, end to end:
#   1. recreate the ledger-adapter GEO-pointed (via run-app-on-geo.sh — keeps the geo override)
#   2. CDC gate: refuse to start unless Debezium connector RUNNING + replication slot active
#      (snapshot.mode=never drops inserts made before the slot exists — the "stuck events" trap)
#   3. cleanup + throughput seed; capture a DB-side RUN_START watermark so EVERY chain metric is
#      scoped to THIS run (the onchain_events table still holds orphan synthetic-burst debris)
#   4. start on-box metric collectors on all 3 validators + a local node-home/DB sampler
#   5. run k6 09-throughput in constant-arrival-rate mode (sustained offered rate for N minutes)
#   6. mid-run: stop alex (RO), hold it down, restart it — captured as a marked fault window
#   7. drain the back-half to zero, fetch node CSVs, compute results → geo-bench-<ts>.json
#
# Reuses VERBATIM: tests/stress/09-throughput.js, setup/stress-throughput-seed.sql, stress-cleanup.sql,
#                  deploy/geo/run-app-on-geo.sh, deploy/geo/metrics/node-collect.sh.
# Honesty note: updated_at-created_at is end-to-end COMMIT/PIPELINE latency (batch wait + send +
#   receipt poll), NOT raw consensus finality. Reported and labelled as "commit latency".
#
# Usage:  deploy/geo/run-geo-bench.sh
#   env (all optional, defaults tuned for the WAN geo chain):
#     PACE_RPS=10 PACE_DURATION=7m VUS=50         # sustained offered iteration rate / duration
#     FAULT=1 FAULT_AT=120 FAULT_DOWN=120         # kill alex at +120s, keep down 120s (0 disables)
#     SAMPLE=5                                    # DB + node sample interval (s)
#     ADAPTER_BATCH_SIZE=300 ADAPTER_BATCH_TIMEOUT=2s ADAPTER_PIPELINE_WINDOW=4 WMS_DB_MAX_CONNS=40
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1
export PATH="$HOME/.foundry/bin:$PATH"
GEO=deploy/geo

# ---- config -----------------------------------------------------------------------------------
PACE_RPS="${PACE_RPS:-10}"
PACE_DURATION="${PACE_DURATION:-7m}"
VUS="${VUS:-50}"
FAULT="${FAULT:-1}"
FAULT_AT="${FAULT_AT:-120}"
FAULT_DOWN="${FAULT_DOWN:-120}"
SAMPLE="${SAMPLE:-5}"
PICK_BATCH="${PICK_BATCH:-4}"; ALLOC_EVERY="${ALLOC_EVERY:-10}"
WMS_DB_MAX_CONNS="${WMS_DB_MAX_CONNS:-40}"
ADAPTER_BATCH_SIZE="${ADAPTER_BATCH_SIZE:-300}"
ADAPTER_BATCH_TIMEOUT="${ADAPTER_BATCH_TIMEOUT:-2s}"
ADAPTER_PIPELINE_WINDOW="${ADAPTER_PIPELINE_WINDOW:-4}"
dur_to_s(){ case "$1" in *m) echo $(( ${1%m}*60 ));; *s) echo "${1%s}";; *) echo "$1";; esac; }
DUR_S=$(dur_to_s "$PACE_DURATION")

ALEX_HOST="${ALEX_HOST:-vpn-alex}"
# name:host:flag(emoji-free for csv) — node-home is local (:9750), validators are remote (:9650 loopback)
NODES_NAME=(dino alex itldc evolvica)
NODES_HOST=(vpn-dino vpn-alex vpn-itldc md-evolvica)
NODES_GEO=(MD RO NL MD)

. "$GEO/genesis/chain-ids.env"
GEO_RPC="http://localhost:9750/ext/bc/${BLOCKCHAIN_ID}/rpc"
TS=$(date +%Y%m%d-%H%M%S)
OUT="$GEO/artifacts/geo-bench-$TS"; mkdir -p "$OUT"
QR="STRESS-QR-TPUT-%"
FAULTLOG="$OUT/fault.log"; : > "$FAULTLOG"

DETECTED_PROJECT=$(docker inspect ledger-adapter --format '{{index .Config.Labels "com.docker.compose.project"}}' 2>/dev/null || true)
export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-${DETECTED_PROJECT:-blockchain_project}}"

PSQL(){ docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "$1" 2>/dev/null | tr -d '[:space:]'; }
RAW(){ docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "$1" 2>/dev/null; }

# ---- safety: never leave alex down if we die mid-fault -----------------------------------------
restore_alex(){
  # If we logged a DOWN but never the matching UP, we died mid-fault — rescue alex.
  if [ -s "$FAULTLOG" ] && grep -q DOWN "$FAULTLOG" && ! grep -q UP "$FAULTLOG"; then
    echo "  [trap] alex may be down (DOWN without UP) — starting it"
    ssh -o ConnectTimeout=15 "$ALEX_HOST" 'systemctl start avalanchego' 2>/dev/null || true
  fi
}
KILL_BG(){ for p in "${BG_PIDS[@]:-}"; do kill "$p" 2>/dev/null || true; done; }
trap 'restore_alex; KILL_BG' EXIT INT TERM
BG_PIDS=()

command -v k6  >/dev/null 2>&1 || { echo "FATAL: host k6 not installed (brew install k6)"; exit 1; }
command -v cast >/dev/null 2>&1 || { echo "FATAL: foundry cast not on PATH"; exit 1; }

echo "######## GEO SUSTAINED BENCH — project=$COMPOSE_PROJECT_NAME ########"
echo "  chain=$BLOCKCHAIN_ID  rate=${PACE_RPS}/s dur=$PACE_DURATION  fault=$FAULT (@+${FAULT_AT}s for ${FAULT_DOWN}s)  out=$OUT"

# ---- 1. adapter geo-pointed + wms pool --------------------------------------------------------
echo "=== 1. recreate adapter (geo) + wms ==="
BATCH_SIZE="$ADAPTER_BATCH_SIZE" BATCH_TIMEOUT="$ADAPTER_BATCH_TIMEOUT" PIPELINE_WINDOW="$ADAPTER_PIPELINE_WINDOW" \
WMS_DB_MAX_CONNS="$WMS_DB_MAX_CONNS" \
  "$GEO/run-app-on-geo.sh" up -d --no-deps wms_app ledger-adapter >/dev/null 2>&1
for i in $(seq 1 30); do
  AOK=$(docker logs ledger-adapter 2>&1 | grep -c "\"batch_size\":$ADAPTER_BATCH_SIZE")
  WOK=$(docker exec wms-service sh -c 'curl -s http://localhost:8080/health' 2>/dev/null | grep -cE '"status":"(ok|degraded)"')
  [ "${AOK:-0}" -ge 1 ] && [ "${WOK:-0}" -ge 1 ] && { echo "  adapter+wms ready (try $i)"; break; }
  sleep 2
done
echo "  adapter RPC: $(docker inspect ledger-adapter --format '{{range .Config.Env}}{{println .}}{{end}}' 2>/dev/null | grep -E '^RPC_URL=' | head -1)"

# ---- 2. CDC gate ------------------------------------------------------------------------------
echo "=== 2. CDC gate (connector RUNNING + slot active) ==="
CONN_STATE=$(curl -s -m 5 localhost:8083/connectors/outbox-connector/status 2>/dev/null | grep -oE '"state":"[A-Z]+"' | head -1)
SLOT_ACTIVE=$(PSQL "SELECT active FROM pg_replication_slots WHERE slot_name='debezium_outbox'")
echo "  connector=$CONN_STATE  slot_active=$SLOT_ACTIVE"
if ! echo "$CONN_STATE" | grep -q RUNNING || [ "$SLOT_ACTIVE" != "t" ]; then
  echo "  !! CDC not ready — restarting connector and waiting"
  curl -s -m 5 -X POST localhost:8083/connectors/outbox-connector/restart >/dev/null 2>&1 || true
  for i in $(seq 1 20); do
    CONN_STATE=$(curl -s -m 5 localhost:8083/connectors/outbox-connector/status 2>/dev/null | grep -oE '"state":"[A-Z]+"' | head -1)
    SLOT_ACTIVE=$(PSQL "SELECT active FROM pg_replication_slots WHERE slot_name='debezium_outbox'")
    echo "$CONN_STATE" | grep -q RUNNING && [ "$SLOT_ACTIVE" = "t" ] && { echo "  CDC ready (try $i)"; break; }
    sleep 2
  done
fi

# ---- 3. cleanup + seed + watermark ------------------------------------------------------------
echo "=== 3. cleanup + seed ==="
docker exec -i postgres_db psql -U root -d wms_blockchain_db < tests/stress/setup/stress-cleanup.sql >/dev/null 2>&1
docker exec -i postgres_db psql -U root -d wms_blockchain_db < tests/stress/setup/stress-throughput-seed.sql 2>&1 | tail -1
RECV=$(PSQL "SELECT b.bin_id FROM wms_inventory.bins b JOIN wms_inventory.warehouses w ON w.warehouse_id=b.warehouse_id WHERE b.code='BUFFER-01' AND w.name='Склад Москва-Север' LIMIT 1")
STOR=$(PSQL "SELECT b.bin_id FROM wms_inventory.bins b JOIN wms_inventory.warehouses w ON w.warehouse_id=b.warehouse_id WHERE b.code='A-01-01' AND w.name='Склад Москва-Север' LIMIT 1")
RAW "SELECT COALESCE(json_agg(cargoplace_id ORDER BY cargoplace_code),'[]') FROM wms_inventory.cargoplaces WHERE cargoplace_code LIKE 'STRESS-TPUT-CP-%'" > /tmp/stress-tput-cps.json
RAW "SELECT COALESCE(json_agg(j ORDER BY code),'[]') FROM (
  SELECT d.code AS code, json_build_object('destinationId', d.destination_id, 'shippingBinId', b.bin_id,
    'dispatchCodes', (SELECT COALESCE(json_agg(od.dispatch_code ORDER BY od.dispatch_code),'[]')
                      FROM wms_inventory.outbound_dispatches od
                      WHERE od.destination_id=d.destination_id AND od.dispatch_code LIKE 'STRESS-TPUT-DSP-%')) AS j
  FROM wms_inventory.destinations d
  JOIN wms_inventory.bins b ON b.destination_id=d.destination_id AND b.section='SHIPPING_BUFFER'
  WHERE d.code LIKE 'SHOP-T%') sub" > /tmp/stress-tput-dests.json
# DB-side watermark: every chain query below is scoped to created_at >= RUN_START. Bulletproof vs
# the orphan synthetic-burst rows still in onchain_events (host/container clock skew avoided).
# ISO-8601 with a 'T' separator — PSQL()'s whitespace strip would mangle the "date time" space
# into an invalid literal, so capture via RAW and swap the single date/time space for 'T'.
RUN_START=$(RAW "SELECT now()" | tr -d '\n' | sed 's/ /T/')
echo "  RECV=$RECV STOR=$STOR dests=$(grep -o destinationId /tmp/stress-tput-dests.json | wc -l | tr -d ' ')  RUN_START=$RUN_START"

# ---- 4. start collectors (on-box validators + local node-home/DB) ------------------------------
echo "=== 4. start metric collectors ==="
NSAMP=$(( (DUR_S + 240) / SAMPLE ))   # safety ceiling; collectors are explicitly stopped at fetch (step 8)
for idx in "${!NODES_NAME[@]}"; do
  h="${NODES_HOST[$idx]}"; n="${NODES_NAME[$idx]}"
  ssh -o ConnectTimeout=15 "$h" 'pkill -f node-collect.sh 2>/dev/null; rm -f /tmp/geo-nodestat.csv' 2>/dev/null
  scp -o ConnectTimeout=15 -q "$GEO/metrics/node-collect.sh" "$h:/tmp/node-collect.sh" 2>/dev/null \
    && ssh -o ConnectTimeout=15 "$h" "nohup sh /tmp/node-collect.sh '$BLOCKCHAIN_ID' '$SAMPLE' '$NSAMP' /tmp/geo-nodestat.csv >/dev/null 2>&1 & echo ok" >/dev/null 2>&1 \
    && echo "  collector launched on $n ($h)" || echo "  !! collector FAILED on $n ($h)"
done
# local sampler: node-home node metrics + run-scoped DB throughput/latency/backlog
DBCSV="$OUT/db.csv"; NHCSV="$OUT/nodehome.csv"
echo "ts,committed,sent,failed,backlog,lat_p50,lat_p95,lat_p99" > "$DBCSV"
echo "ts,height,peers,rss_bytes,cpu_sec" > "$NHCSV"
( for i in $(seq 1 "$NSAMP"); do
    t=$(date +%s)
    C=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='COMMITTED' AND created_at>='$RUN_START'")
    S=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='SENT' AND created_at>='$RUN_START'")
    F=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='FAILED' AND created_at>='$RUN_START'")
    B=$(PSQL "SELECT count(*) FROM public.outbox_events oe WHERE oe.created_at>='$RUN_START' AND NOT EXISTS (SELECT 1 FROM public.onchain_events oc WHERE oc.event_id=oe.event_id)")
    L=$(RAW "SELECT string_agg(x::text,',') FROM (SELECT percentile_disc(ARRAY[0.5,0.95,0.99]) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (updated_at-created_at))) x FROM public.onchain_events WHERE status='COMMITTED' AND created_at>='$RUN_START') q, unnest(q.x) x" | tr -d '[:space:]')
    echo "$t,${C:-0},${S:-0},${F:-0},${B:-0},${L:-,,}" | sed 's/,,/,NA,NA,NA/' >> "$DBCSV"
    m=$(curl -s -m 5 http://localhost:9750/ext/metrics 2>/dev/null)
    nh=$(printf '%s\n' "$m" | grep -F "avalanche_snowman_last_accepted_height{chain=\"$BLOCKCHAIN_ID\"}" | awk '{print $2}')
    np=$(printf '%s\n' "$m" | grep '^avalanche_network_peers ' | awk '{print $2}')
    nr=$(printf '%s\n' "$m" | grep '^avalanche_process_process_resident_memory_bytes' | awk '{print $2}')
    nc=$(printf '%s\n' "$m" | grep '^avalanche_process_process_cpu_seconds_total' | awk '{print $2}')
    echo "$t,${nh:-NA},${np:-NA},${nr:-NA},${nc:-NA}" >> "$NHCSV"
    sleep "$SAMPLE"
  done ) & SAMPLER_PID=$!; BG_PIDS+=("$SAMPLER_PID")
echo "  local DB+node-home sampler pid $SAMPLER_PID"

# ---- 5. fault timer (background) ---------------------------------------------------------------
if [ "$FAULT" = 1 ] && [ "$FAULT_DOWN" -gt 0 ]; then
  ( sleep "$FAULT_AT"
    echo "$(date +%s) DOWN" >> "$FAULTLOG"; echo "  >> [fault] stopping $ALEX_HOST"
    ssh -o ConnectTimeout=15 "$ALEX_HOST" 'systemctl stop avalanchego' 2>/dev/null
    sleep "$FAULT_DOWN"
    echo "  >> [fault] restarting $ALEX_HOST"
    ssh -o ConnectTimeout=15 "$ALEX_HOST" 'systemctl start avalanchego; for i in $(seq 1 60); do curl -s -m2 localhost:9650/ext/health | grep -q "\"healthy\":true" && { echo healthy after ${i}s; break; }; sleep 1; done' 2>/dev/null | grep -v zoxide | sed 's/^/     alex /'
    echo "$(date +%s) UP" >> "$FAULTLOG"
  ) & BG_PIDS+=("$!")
fi

# ---- 6. sustained k6 run ----------------------------------------------------------------------
echo "=== 6. k6 constant-arrival-rate ${PACE_RPS}/s for $PACE_DURATION ==="
RUN_T0=$(date +%s)
k6 run --no-usage-report --quiet \
  -e PACE_RPS="$PACE_RPS" -e PACE_DURATION="$PACE_DURATION" \
  -e VUS="$VUS" -e OPERATOR_COUNT="$VUS" \
  -e PICK_BATCH="$PICK_BATCH" -e ALLOC_EVERY="$ALLOC_EVERY" -e THINK_MS="${THINK_MS:-0}" \
  -e RECEIVING_BIN_ID="$RECV" -e STORAGE_BIN_ID="$STOR" \
  tests/stress/09-throughput.js > "$OUT/k6.log" 2>&1
RUN_T1=$(date +%s)
echo "  k6 wall: $((RUN_T1-RUN_T0))s"
grep -iE "tput_items_(stored|picked|shipped)|iterations\.\.|iteration_duration|http_req_failed|dropped_iterations|level=error" "$OUT/k6.log" | head -20

# ---- 7. drain back-half -----------------------------------------------------------------------
echo "=== 7. drain to zero ==="
for i in $(seq 1 150); do
  B=$(PSQL "SELECT count(*) FROM public.outbox_events oe WHERE oe.created_at>='$RUN_START' AND NOT EXISTS (SELECT 1 FROM public.onchain_events oc WHERE oc.event_id=oe.event_id)")
  S=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='SENT' AND created_at>='$RUN_START'")
  C=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='COMMITTED' AND created_at>='$RUN_START'")
  F=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='FAILED' AND created_at>='$RUN_START'")
  [ $((i % 5)) -eq 1 ] && echo "  t=+$((i*3))s committed=$C sent=$S failed=$F backlog=$B"
  [ "${B:-1}" -le 2 ] && [ "${S:-1}" -le 0 ] && { echo "  drained (committed=$C failed=$F)"; break; }
  sleep 3
done

# ---- 8. fetch node CSVs -----------------------------------------------------------------------
echo "=== 8. fetch on-box node CSVs ==="
for idx in "${!NODES_NAME[@]}"; do
  h="${NODES_HOST[$idx]}"; n="${NODES_NAME[$idx]}"
  ssh -o ConnectTimeout=15 "$h" 'cat /tmp/geo-nodestat.csv' 2>/dev/null > "$OUT/node-$n.csv"
  ssh -o ConnectTimeout=15 "$h" 'pkill -f node-collect.sh 2>/dev/null' 2>/dev/null
  rows=$(wc -l < "$OUT/node-$n.csv" 2>/dev/null | tr -d ' ')
  echo "  $n: $rows rows"
done

# ---- 9. compute results + emit JSON -----------------------------------------------------------
echo "=== 9. results ==="
source "$GEO/metrics/geo-bench-report.sh"   # computes vars + writes $OUT/geo-bench.json from the CSVs
echo "######## DONE → $OUT ########"
