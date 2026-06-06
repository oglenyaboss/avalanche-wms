#!/bin/bash
# Smoke: drive existing 07-full-flow (2000/30) on the STRESS-FLOW pool, adapter LIVE.
# Validates the harness + reveals the 4 aggregate_type names + first FSM-correctness signal.
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1
PSQL(){ docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "$1" 2>/dev/null | tr -d '[:space:]'; }
RAW(){ docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "$1" 2>/dev/null; }
WHID_NAME='Склад Москва-Север'

RECV=$(PSQL "SELECT b.bin_id FROM wms_inventory.bins b JOIN wms_inventory.warehouses w ON w.warehouse_id=b.warehouse_id WHERE b.code='BUFFER-01' AND w.name='$WHID_NAME' LIMIT 1")
STOR=$(PSQL "SELECT b.bin_id FROM wms_inventory.bins b JOIN wms_inventory.warehouses w ON w.warehouse_id=b.warehouse_id WHERE b.code='A-01-01' AND w.name='$WHID_NAME' LIMIT 1")
DEST=$(PSQL "SELECT destination_id FROM wms_inventory.destinations WHERE code='SHOP-5' LIMIT 1")
SHIP=$(PSQL "SELECT bin_id FROM wms_inventory.bins WHERE destination_id='$DEST' AND section='SHIPPING_BUFFER' LIMIT 1")
DISP=$(PSQL "SELECT string_agg(dispatch_code, ',' ORDER BY dispatch_code) FROM wms_inventory.outbound_dispatches WHERE dispatch_code LIKE 'STRESS-FLOW-DSP-%' AND status='SCHEDULED'")
echo "env: RECV=$RECV STOR=$STOR DEST=$DEST SHIP=$SHIP"
echo "dispatches: $DISP"

# cargoplaces JSON, ordered by code (07 indexes by iterationInTest)
RAW "SELECT COALESCE(json_agg(cargoplace_id ORDER BY cargoplace_code),'[]') FROM wms_inventory.cargoplaces WHERE cargoplace_code LIKE 'STRESS-FLOW-CP-%' AND status='RECEIVED_AT_GATE'" | tr -d '[:space:]' > /tmp/stress-flow-cps.json
echo "cps in pool: $(PSQL "SELECT count(*) FROM wms_inventory.cargoplaces WHERE cargoplace_code LIKE 'STRESS-FLOW-CP-%' AND status='RECEIVED_AT_GATE'")"

echo "=== adapter up? ===" ; docker ps --format '{{.Names}}' | grep -q '^ledger-adapter$' && echo "yes" || { echo "starting adapter"; docker compose -p stresstest up -d --no-deps ledger-adapter >/dev/null 2>&1; sleep 3; }

echo "=== snapshot BEFORE ==="
RAW "SELECT aggregate_type, count(*) FROM public.onchain_events GROUP BY aggregate_type ORDER BY 2 DESC"
C0=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='COMMITTED'")
echo "committed_before=$C0"

echo "=== running 07 (2000/30) LIVE ==="
k6 run --no-usage-report --quiet \
  -e RECEIVING_BIN_ID=$RECV -e STORAGE_BIN_ID=$STOR -e DESTINATION_ID=$DEST \
  -e SHIPPING_BIN_ID=$SHIP -e "DISPATCH_CODE=$DISP" \
  tests/stress/07-full-flow.js 2>&1 | tail -25

echo "=== draining (poll backlog) ==="
for i in $(seq 1 30); do
  B=$(PSQL "SELECT count(*) FROM public.outbox_events oe WHERE NOT EXISTS (SELECT 1 FROM public.onchain_events oc WHERE oc.event_id=oe.event_id)")
  C=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='COMMITTED'")
  echo "t=+$((i*2))s committed=$C backlog=$B"
  [ "${B:-1}" -le 5 ] && break
  sleep 2
done

echo "=== snapshot AFTER ==="
RAW "SELECT aggregate_type, count(*) FROM public.onchain_events GROUP BY aggregate_type ORDER BY 2 DESC"
echo "--- status breakdown ---"
RAW "SELECT status, count(*) FROM public.onchain_events GROUP BY status ORDER BY 2 DESC"
echo "--- flow product final WMS statuses (FSM completion) ---"
RAW "SELECT status, count(*) FROM wms_inventory.products WHERE qr_code LIKE 'STRESS-QR-%' GROUP BY status ORDER BY 2 DESC"
echo "DONE"
