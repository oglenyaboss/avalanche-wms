#!/bin/bash
# Clean full-FSM re-run after the gas-limit fix, on a fresh stand.
# Seeds STRESS pool, drives 07 (full FSM, live), validates: 0 OOG, real on-chain
# transitions (not skips), mixed-aggregate commit rate, + shipping diagnosis.
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1
export PATH="$HOME/.foundry/bin:$PATH"
PSQL(){ docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "$1" 2>/dev/null | tr -d '[:space:]'; }
RAW(){ docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "$1" 2>/dev/null; }
WH='Склад Москва-Север'

echo "=== seed STRESS pool (base data already applied by db-init) ==="
docker exec -i postgres_db psql -U root -d wms_blockchain_db < tests/stress/setup/stress-seed.sql 2>&1 | tail -3

RECV=$(PSQL "SELECT b.bin_id FROM wms_inventory.bins b JOIN wms_inventory.warehouses w ON w.warehouse_id=b.warehouse_id WHERE b.code='BUFFER-01' AND w.name='$WH' LIMIT 1")
STOR=$(PSQL "SELECT b.bin_id FROM wms_inventory.bins b JOIN wms_inventory.warehouses w ON w.warehouse_id=b.warehouse_id WHERE b.code='A-01-01' AND w.name='$WH' LIMIT 1")
DEST=$(PSQL "SELECT destination_id FROM wms_inventory.destinations WHERE code='SHOP-5' LIMIT 1")
SHIP=$(PSQL "SELECT bin_id FROM wms_inventory.bins WHERE destination_id='$DEST' AND section='SHIPPING_BUFFER' LIMIT 1")
DISP=$(PSQL "SELECT string_agg(dispatch_code, ',' ORDER BY dispatch_code) FROM wms_inventory.outbound_dispatches WHERE dispatch_code LIKE 'STRESS-FLOW-DSP-%' AND status='SCHEDULED'")
echo "env: RECV=$RECV STOR=$STOR DEST=$DEST SHIP=$SHIP disp=$(echo $DISP | tr ',' '\n' | wc -l | tr -d ' ')"
RAW "SELECT COALESCE(json_agg(cargoplace_id ORDER BY cargoplace_code),'[]') FROM wms_inventory.cargoplaces WHERE cargoplace_code LIKE 'STRESS-FLOW-CP-%' AND status='RECEIVED_AT_GATE'" | tr -d '[:space:]' > /tmp/stress-flow-cps.json
echo "flow cps RECEIVED_AT_GATE: $(PSQL "SELECT count(*) FROM wms_inventory.cargoplaces WHERE cargoplace_code LIKE 'STRESS-FLOW-CP-%' AND status='RECEIVED_AT_GATE'")"

echo "=== adapter config (confirm fix image + PIPELINE_WINDOW) ==="
docker logs ledger-adapter 2>&1 | grep -i "config loaded" | tail -1

echo "=== run 07 (2000/30) LIVE ==="
k6 run --no-usage-report --quiet \
  -e RECEIVING_BIN_ID=$RECV -e STORAGE_BIN_ID=$STOR -e DESTINATION_ID=$DEST \
  -e SHIPPING_BIN_ID=$SHIP -e "DISPATCH_CODE=$DISP" \
  tests/stress/07-full-flow.js 2>&1 | grep -iE "checks|http_req_failed|http_reqs|iterations\.|✗|ship:|driver:|allocate" | head -30

echo "=== drain ==="
for i in $(seq 1 40); do
  B=$(PSQL "SELECT count(*) FROM public.outbox_events oe WHERE NOT EXISTS (SELECT 1 FROM public.onchain_events oc WHERE oc.event_id=oe.event_id)")
  C=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='COMMITTED'")
  F=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='FAILED'")
  echo "t=+$((i*2))s committed=$C failed=$F backlog=$B"
  [ "${B:-1}" -le 3 ] && break
  sleep 2
done

echo "=== RESULT: aggregate-type mix ===" && RAW "SELECT aggregate_type, count(*) FROM public.onchain_events GROUP BY aggregate_type ORDER BY 2 DESC"
echo "=== status breakdown (FAILED should be ~0 with fix) ===" && RAW "SELECT status, count(*) FROM public.onchain_events GROUP BY status ORDER BY 2 DESC"
echo "=== WMS product final statuses (FSM completion) ===" && RAW "SELECT status, count(*) FROM wms_inventory.products WHERE qr_code LIKE 'STRESS-QR-%' GROUP BY status ORDER BY 2 DESC"
echo "=== dispatch final statuses (shipping) ===" && RAW "SELECT status, count(*) FROM wms_inventory.outbound_dispatches WHERE dispatch_code LIKE 'STRESS-FLOW-DSP-%' GROUP BY status"

echo "=== on-chain: real transitions vs skips? (trace 1 committed putaway) ==="
RPCRAW=$(docker run --rm -v stresstest_shared_state:/s busybox cat /s/rpc_url.txt 2>/dev/null)
RPC=$(echo "$RPCRAW" | sed 's#subnet-node1:9650#localhost:9650#')
echo "rpc=$RPC"
PTX=$(RAW "SELECT tx_hash FROM public.onchain_events WHERE status='COMMITTED' AND aggregate_type='putaway' AND tx_hash<>'' ORDER BY created_at DESC LIMIT 1" | tr -d '[:space:]')
if [ -n "$PTX" ]; then echo "putaway tx=$PTX"; cast run "$PTX" --rpc-url "$RPC" 2>&1 | grep -oE "ItemTransition(Failed)?\(" | sort | uniq -c; fi

echo "=== shipping diagnosis (if 0 shipping events) — wms ship errors + sample ==="
docker logs wms-service --since 5m 2>&1 | grep -iE "ship|dispatch|отгруз" | grep -iE "error|fail|409|422|400|invalid|not" | tail -8
echo "DONE"
