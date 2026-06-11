#!/bin/bash
# run-throughput-geo.sh — end-to-end throughput bench of the WHOLE project against the GEO chain.
#
# Geo-safe adaptation of tests/stress/run-throughput.sh: reuses tests/stress/09-throughput.js and
# setup/stress-throughput-seed.sql VERBATIM (the k6 driver is chain-agnostic — it hammers the real
# WMS HTTP API: receive→putaway→pick→ship → outbox → Debezium → ledger-adapter → chain). Only the
# three geo-hostile spots in the original wrapper are changed:
#   1. adapter/wms recreate goes through deploy/geo/run-app-on-geo.sh so the ledger-adapter KEEPS its
#      geo override (RPC=geo-node-home, CONTRACT=geo) instead of reverting to the local chain;
#   2. compose project is auto-detected from the running adapter (not hardcoded `stresstest`);
#   3. the on-chain TRACE reads the GEO RPC (node-home :9750), not stresstest_shared_state.
#
# Because the k6 driver creates REAL products (qr_code LIKE 'STRESS-QR-TPUT-%') via the WMS flow, a
# run also POPULATES traceability (those products appear in wms_inventory.products with on-chain
# events) — fixing the "empty /trace" that synthetic-UUID load did not.
#
# Defaults are DIALED DOWN for the WAN geo chain (block ~4-6s) vs the local-chain original (~1900/s).
# Usage: ./deploy/geo/run-throughput-geo.sh [VUS] [ITERS]     (default 10 500)
#   env: MAX_DURATION=15m PICK_BATCH=4 ALLOC_EVERY=10 WMS_DB_MAX_CONNS=40
#        ADAPTER_BATCH_SIZE=300 ADAPTER_BATCH_TIMEOUT=2s ADAPTER_PIPELINE_WINDOW=4
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1
export PATH="$HOME/.foundry/bin:$PATH"

# (2) project = whatever owns the running adapter (after the rebuild it's blockchain_project).
DETECTED_PROJECT=$(docker inspect ledger-adapter --format '{{index .Config.Labels "com.docker.compose.project"}}' 2>/dev/null || true)
export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-${DETECTED_PROJECT:-blockchain_project}}"

# (3) geo RPC for the trace step (node-home, host loopback).
. deploy/geo/genesis/chain-ids.env
GEO_RPC="http://localhost:9750/ext/bc/${BLOCKCHAIN_ID}/rpc"

VUS="${1:-10}"; ITERS="${2:-500}"
MAX_DURATION="${MAX_DURATION:-15m}"
PICK_BATCH="${PICK_BATCH:-4}"; ALLOC_EVERY="${ALLOC_EVERY:-10}"
WMS_DB_MAX_CONNS="${WMS_DB_MAX_CONNS:-40}"
ADAPTER_BATCH_SIZE="${ADAPTER_BATCH_SIZE:-300}"
ADAPTER_BATCH_TIMEOUT="${ADAPTER_BATCH_TIMEOUT:-2s}"
ADAPTER_PIPELINE_WINDOW="${ADAPTER_PIPELINE_WINDOW:-4}"

PSQL(){ docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "$1" 2>/dev/null | tr -d '[:space:]'; }
RAW(){ docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "$1" 2>/dev/null; }
QR="STRESS-QR-TPUT-%"

command -v k6 >/dev/null 2>&1 || { echo "FATAL: host k6 not installed (brew install k6)"; exit 1; }

echo "######## GEO THROUGHPUT BENCH — project=$COMPOSE_PROJECT_NAME VUS=$VUS ITERS=$ITERS ########"
echo "  geo RPC: $GEO_RPC"

echo "=== 1. config (KEEP geo override): WMS pool=$WMS_DB_MAX_CONNS | adapter batch=$ADAPTER_BATCH_SIZE timeout=$ADAPTER_BATCH_TIMEOUT window=$ADAPTER_PIPELINE_WINDOW ==="
# (1) recreate via run-app-on-geo.sh → adapter stays geo-pointed (RPC/CONTRACT from the override),
#     wms_app just picks up WMS_DB_MAX_CONNS. --no-deps so postgres/kafka aren't disturbed.
BATCH_SIZE="$ADAPTER_BATCH_SIZE" BATCH_TIMEOUT="$ADAPTER_BATCH_TIMEOUT" PIPELINE_WINDOW="$ADAPTER_PIPELINE_WINDOW" \
WMS_DB_MAX_CONNS="$WMS_DB_MAX_CONNS" \
  deploy/geo/run-app-on-geo.sh up -d --no-deps wms_app ledger-adapter >/dev/null 2>&1
for i in $(seq 1 30); do
  AOK=$(docker logs ledger-adapter 2>&1 | grep -c "\"batch_size\":$ADAPTER_BATCH_SIZE")
  WOK=$(docker exec wms-service sh -c 'curl -s http://localhost:8080/health' 2>/dev/null | grep -cE '"status":"(ok|degraded)"')
  [ "${AOK:-0}" -ge 1 ] && [ "${WOK:-0}" -ge 1 ] && { echo "  adapter+wms ready (try $i)"; break; }
  sleep 2
done
echo "  adapter: $(docker logs ledger-adapter 2>&1 | grep 'config loaded' | tail -1 | grep -oE '"batch_size":[0-9]+')"
echo "  adapter RPC target: $(docker inspect ledger-adapter --format '{{range .Config.Env}}{{println .}}{{end}}' 2>/dev/null | grep -E '^RPC_URL=' | head -1)"

echo "=== 2. cleanup + throughput seed ==="
docker exec -i postgres_db psql -U root -d wms_blockchain_db < tests/stress/setup/stress-cleanup.sql >/dev/null 2>&1
docker exec -i postgres_db psql -U root -d wms_blockchain_db < tests/stress/setup/stress-throughput-seed.sql 2>&1 | tail -1
echo "  destinations SHOP-T*: $(PSQL "SELECT count(*) FROM wms_inventory.destinations WHERE code LIKE 'SHOP-T%'")"
echo "  orders NEW: $(PSQL "SELECT count(*) FROM wms_inventory.orders WHERE external_order_no LIKE 'STRESS-TPUT-ORD-%' AND status='NEW'")"
echo "  cargoplaces RECEIVED_AT_GATE: $(PSQL "SELECT count(*) FROM wms_inventory.cargoplaces WHERE cargoplace_code LIKE 'STRESS-TPUT-CP-%' AND status='RECEIVED_AT_GATE'")"

echo "=== 3. resolve env + JSON fixtures ==="
RECV=$(PSQL "SELECT b.bin_id FROM wms_inventory.bins b JOIN wms_inventory.warehouses w ON w.warehouse_id=b.warehouse_id WHERE b.code='BUFFER-01' AND w.name='Склад Москва-Север' LIMIT 1")
STOR=$(PSQL "SELECT b.bin_id FROM wms_inventory.bins b JOIN wms_inventory.warehouses w ON w.warehouse_id=b.warehouse_id WHERE b.code='A-01-01' AND w.name='Склад Москва-Север' LIMIT 1")
RAW "SELECT COALESCE(json_agg(cargoplace_id ORDER BY cargoplace_code),'[]') FROM wms_inventory.cargoplaces WHERE cargoplace_code LIKE 'STRESS-TPUT-CP-%'" > /tmp/stress-tput-cps.json
RAW "SELECT COALESCE(json_agg(j ORDER BY code),'[]') FROM (
  SELECT d.code AS code, json_build_object(
    'destinationId', d.destination_id,
    'shippingBinId', b.bin_id,
    'dispatchCodes', (SELECT COALESCE(json_agg(od.dispatch_code ORDER BY od.dispatch_code),'[]')
                      FROM wms_inventory.outbound_dispatches od
                      WHERE od.destination_id=d.destination_id AND od.dispatch_code LIKE 'STRESS-TPUT-DSP-%')
  ) AS j
  FROM wms_inventory.destinations d
  JOIN wms_inventory.bins b ON b.destination_id=d.destination_id AND b.section='SHIPPING_BUFFER'
  WHERE d.code LIKE 'SHOP-T%') sub" > /tmp/stress-tput-dests.json
echo "  RECV=$RECV STOR=$STOR | dests: $(grep -o destinationId /tmp/stress-tput-dests.json | wc -l | tr -d ' ')"

echo "=== 4. run 09-throughput (live DB-throughput sampler) ==="
: > /tmp/tput-slope.log
( for i in $(seq 1 600); do
    ts=$(date +%s)
    done_stage=$(PSQL "SELECT count(*) FROM wms_inventory.products WHERE qr_code LIKE '$QR' AND status IN ('STORED','ALLOCATED','ASSEMBLED','READY_TO_SHIP','SHIPPED')")
    picked=$(PSQL "SELECT count(*) FROM wms_inventory.products WHERE qr_code LIKE '$QR' AND status IN ('READY_TO_SHIP','SHIPPED')")
    echo "$ts,$done_stage,$picked" >> /tmp/tput-slope.log
    sleep 3
  done ) &
SAMPLER_PID=$!

RUN_START=$(date +%s)
k6 run --no-usage-report --quiet \
  -e VUS="$VUS" -e ITERS="$ITERS" -e MAX_DURATION="$MAX_DURATION" \
  -e PICK_BATCH="$PICK_BATCH" -e ALLOC_EVERY="$ALLOC_EVERY" -e THINK_MS="${THINK_MS:-0}" \
  -e OPERATOR_COUNT="${OPERATOR_COUNT:-$VUS}" \
  -e RECEIVING_BIN_ID="$RECV" -e STORAGE_BIN_ID="$STOR" \
  tests/stress/09-throughput.js > /tmp/tput-k6.log 2>&1
RUN_END=$(date +%s)
{ kill "$SAMPLER_PID" 2>/dev/null; wait "$SAMPLER_PID" 2>/dev/null; } 2>/dev/null
WALL=$((RUN_END - RUN_START))
echo "  k6 wall: ${WALL}s"
grep -iE "tput_items_stored|tput_items_picked|iterations\.\.|iteration_duration|http_req_failed|level=error" /tmp/tput-k6.log | head -20

echo "=== 5. drain back-half (poll backlog → 0) ==="
for i in $(seq 1 120); do
  B=$(PSQL "SELECT count(*) FROM public.outbox_events oe WHERE NOT EXISTS (SELECT 1 FROM public.onchain_events oc WHERE oc.event_id=oe.event_id)")
  C=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='COMMITTED'")
  F=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='FAILED'")
  S=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='SENT'")
  echo "  t=+$((i*2))s committed=$C failed=$F sent=$S backlog=$B"
  [ "${B:-1}" -le 2 ] && [ "${S:-1}" -le 0 ] && { echo "  fully committed"; break; }
  sleep 2
done

echo "=== 6. RESULTS — sustained front throughput ==="
RAW_SLOPE=$(awk -F, 'NR==1{ft=$1;fp=$3} {lt=$1;lp=$3} END{ if(lt>ft) printf "%.1f", (lp-fp)/(lt-ft); else print "n/a" }' /tmp/tput-slope.log)
PICKED_TOTAL=$(PSQL "SELECT count(*) FROM wms_inventory.products WHERE qr_code LIKE '$QR' AND status IN ('READY_TO_SHIP','SHIPPED')")
SHIPPED_TOTAL=$(PSQL "SELECT count(*) FROM wms_inventory.products WHERE qr_code LIKE '$QR' AND status='SHIPPED'")
echo "  picked (READY_TO_SHIP+SHIPPED): $PICKED_TOTAL | shipped: $SHIPPED_TOTAL"
OVERALL=$(awk -v p="$PICKED_TOTAL" -v w="$WALL" 'BEGIN{ if(w>0) printf "%.1f", p/w; else print "n/a" }')
echo "  k6 wall: ${WALL}s → overall front rate ≈ $OVERALL items/с"
echo "  sampler slope (sustained mid-run): $RAW_SLOPE items/с"
awk -F, 'NR==1{f=$1} NR%2==1{printf "    +%ss  done=%s picked=%s\n", $1-f, $2, $3}' /tmp/tput-slope.log

echo "=== 7. WMS final statuses ($QR) ==="
RAW "SELECT status, count(*) FROM wms_inventory.products WHERE qr_code LIKE '$QR' GROUP BY status ORDER BY 2 DESC"

echo "=== 8. on-chain mix + status ==="
RAW "SELECT aggregate_type, count(*) FROM public.onchain_events GROUP BY aggregate_type ORDER BY 2 DESC"
RAW "SELECT status, count(*) FROM public.onchain_events GROUP BY status ORDER BY 2 DESC"

echo "=== 9. WMS↔chain consistency (scoped to TPUT) ==="
SHIPPED_WMS=$(PSQL "SELECT count(*) FROM wms_inventory.products WHERE qr_code LIKE '$QR' AND status='SHIPPED'")
SHIP_CHAIN=$(PSQL "SELECT count(*) FROM public.onchain_events oe JOIN public.outbox_events ob USING(event_id) JOIN wms_inventory.products p ON p.product_id=ob.aggregate_id WHERE oe.aggregate_type='shipping' AND oe.status='COMMITTED' AND p.qr_code LIKE '$QR'")
echo "  SHIPPED(wms)=$SHIPPED_WMS == shipping COMMITTED(chain)=$SHIP_CHAIN -> $([ "$SHIPPED_WMS" = "$SHIP_CHAIN" ] && echo OK || echo MISMATCH)"

echo "=== 10. on-chain TRACE via GEO node-home (real ItemTransition, not Failed/skip) ==="
STX=$(RAW "SELECT tx_hash FROM public.onchain_events WHERE status='COMMITTED' AND aggregate_type='shipping' AND tx_hash<>'' ORDER BY created_at DESC LIMIT 1" | tr -d '[:space:]')
KTX=$(RAW "SELECT tx_hash FROM public.onchain_events WHERE status='COMMITTED' AND aggregate_type='picking' AND tx_hash<>'' ORDER BY created_at DESC LIMIT 1" | tr -d '[:space:]')
[ -n "$KTX" ] && { echo "  picking tx=$KTX"; cast run "$KTX" --rpc-url "$GEO_RPC" 2>&1 | grep -oE "ItemTransition(Failed)?\(" | sort | uniq -c; } || echo "  no picking tx"
[ -n "$STX" ] && { echo "  shipping tx=$STX"; cast run "$STX" --rpc-url "$GEO_RPC" 2>&1 | grep -oE "ItemTransition(Failed)?\(" | sort | uniq -c; } || echo "  no shipping tx"
echo "######## DONE ########"
