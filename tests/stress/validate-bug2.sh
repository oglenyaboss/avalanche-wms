#!/bin/bash
# validate-bug2.sh — проверка фикса BUG 2 (отгрузка под конкурентной сборкой).
# Чистый прогон 07-full-flow на свежем STRESS-пуле, адаптер LIVE.
# Критерии BUG 2: появляются shipping-события, товары доходят до SHIPPED,
# WMS↔chain консистентны (shipped_wms == shipping_committed), 0 FAILED/0 SENT.
#
# Usage: ./validate-bug2.sh [VUS] [ITERS]   (default 30 2000)
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1
export PATH="$HOME/.foundry/bin:$PATH"
VUS="${1:-30}"; ITERS="${2:-2000}"; MAX_DURATION="${MAX_DURATION:-15m}"
PSQL(){ docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "$1" 2>/dev/null | tr -d '[:space:]'; }
RAW(){ docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "$1" 2>/dev/null; }
WH='Склад Москва-Север'

echo "############ VALIDATE BUG 2 — VUS=$VUS ITERS=$ITERS ############"

echo "=== 1. cleanup + reseed (свежий STRESS-пул) ==="
docker exec -i postgres_db psql -U root -d wms_blockchain_db < tests/stress/setup/stress-cleanup.sql >/dev/null 2>&1
docker exec -i postgres_db psql -U root -d wms_blockchain_db < tests/stress/setup/stress-seed.sql 2>&1 | tail -2
# Тест 07 теперь STANDALONE: выделенный SKU 'STRESS-FLOW-SKU' (stress-seed §0.2) →
# allocate матчит только товары потока. Пул теста 06 (STRESS-PROD-QR, 'E2E Seed
# Outbound SKU') НЕ трогается — это проверяется после прогона (06-пул должен остаться
# целиком STORED). Никаких хаков.
echo "  flow SKU mapping: $(PSQL "SELECT count(*) FROM wms_inventory.sku_barcodes WHERE barcode='STRESS-FLOW-BC-01'") (ожидаем 1)"
echo "  06-пул STRESS-PROD-QR STORED до прогона: $(PSQL "SELECT count(*) FROM wms_inventory.products WHERE qr_code LIKE 'STRESS-PROD-QR%' AND status='STORED'")"

echo "=== 2. seed sanity ==="
echo "  operators stress-op-*: $(PSQL "SELECT count(*) FROM public.users WHERE username LIKE 'stress-op-%'")  (ожидаем 30)"
echo "  flow cargoplaces RECEIVED_AT_GATE: $(PSQL "SELECT count(*) FROM wms_inventory.cargoplaces WHERE cargoplace_code LIKE 'STRESS-FLOW-CP-%' AND status='RECEIVED_AT_GATE'")"
echo "  flow orders NEW: $(PSQL "SELECT count(*) FROM wms_inventory.orders WHERE external_order_no LIKE 'STRESS-FLOW-ORD-%' AND status='NEW'")  (ожидаем 2000)"
echo "  flow dispatches SCHEDULED: $(PSQL "SELECT count(*) FROM wms_inventory.outbound_dispatches WHERE dispatch_code LIKE 'STRESS-FLOW-DSP-%' AND status='SCHEDULED'")"
echo "  onchain_events после cleanup: $(PSQL "SELECT count(*) FROM public.onchain_events")"

RECV=$(PSQL "SELECT b.bin_id FROM wms_inventory.bins b JOIN wms_inventory.warehouses w ON w.warehouse_id=b.warehouse_id WHERE b.code='BUFFER-01' AND w.name='$WH' LIMIT 1")
STOR=$(PSQL "SELECT b.bin_id FROM wms_inventory.bins b JOIN wms_inventory.warehouses w ON w.warehouse_id=b.warehouse_id WHERE b.code='A-01-01' AND w.name='$WH' LIMIT 1")
DEST=$(PSQL "SELECT destination_id FROM wms_inventory.destinations WHERE code='SHOP-5' LIMIT 1")
SHIP=$(PSQL "SELECT bin_id FROM wms_inventory.bins WHERE destination_id='$DEST' AND section='SHIPPING_BUFFER' LIMIT 1")
DISP=$(PSQL "SELECT string_agg(dispatch_code, ',' ORDER BY dispatch_code) FROM wms_inventory.outbound_dispatches WHERE dispatch_code LIKE 'STRESS-FLOW-DSP-%' AND status='SCHEDULED'")
RAW "SELECT COALESCE(json_agg(cargoplace_id ORDER BY cargoplace_code),'[]') FROM wms_inventory.cargoplaces WHERE cargoplace_code LIKE 'STRESS-FLOW-CP-%' AND status='RECEIVED_AT_GATE'" | tr -d '[:space:]' > /tmp/stress-flow-cps.json
echo "  env: RECV=$RECV STOR=$STOR DEST=$DEST SHIP=$SHIP"
echo "  adapter: $(docker logs ledger-adapter 2>&1 | grep -iE 'config loaded|pipeline_window' | tail -1)"

echo "=== 3. run 07 (VUS=$VUS ITERS=$ITERS) LIVE ==="
k6 run --no-usage-report --quiet \
  -e VUS="$VUS" -e ITERS="$ITERS" -e MAX_DURATION="$MAX_DURATION" \
  -e RECEIVING_BIN_ID="$RECV" -e STORAGE_BIN_ID="$STOR" -e DESTINATION_ID="$DEST" \
  -e SHIPPING_BIN_ID="$SHIP" -e "DISPATCH_CODE=$DISP" \
  tests/stress/07-full-flow.js 2>&1 | grep -iE "checks|http_req_failed|iteration_duration|iterations\.|teardown:|✓|✗|level=err|WARN" | head -40

echo "=== 4. drain (poll backlog → 0) ==="
for i in $(seq 1 60); do
  B=$(PSQL "SELECT count(*) FROM public.outbox_events oe WHERE NOT EXISTS (SELECT 1 FROM public.onchain_events oc WHERE oc.event_id=oe.event_id)")
  C=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='COMMITTED'")
  F=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='FAILED'")
  S=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='SENT'")
  echo "  t=+$((i*2))s committed=$C failed=$F sent=$S backlog=$B"
  [ "${B:-1}" -le 2 ] && { echo "  backlog drained"; break; }
  sleep 2
done

echo "=== 5. RESULTS ==="
echo "--- on-chain aggregate_type mix (KEY: есть ли 'shipping'?) ---"
RAW "SELECT aggregate_type, count(*) FROM public.onchain_events GROUP BY aggregate_type ORDER BY 2 DESC"
echo "--- on-chain status (KEY: FAILED~0, SENT~0) ---"
RAW "SELECT status, count(*) FROM public.onchain_events GROUP BY status ORDER BY 2 DESC"
echo "--- WMS product final statuses STRESS-QR-% (KEY: SHIPPED > 0) ---"
RAW "SELECT status, count(*) FROM wms_inventory.products WHERE qr_code LIKE 'STRESS-QR-%' GROUP BY status ORDER BY 2 DESC"
echo "--- dispatch final statuses (ожидаем ≥1 DEPARTED, остальные SCHEDULED) ---"
RAW "SELECT status, count(*) FROM wms_inventory.outbound_dispatches WHERE dispatch_code LIKE 'STRESS-FLOW-DSP-%' GROUP BY status"

echo "=== 6. WMS↔chain consistency ==="
SHIPPED_WMS=$(PSQL "SELECT count(*) FROM wms_inventory.products WHERE qr_code LIKE 'STRESS-QR-%' AND status='SHIPPED'")
# Скоупим on-chain счёт по товарам потока (через outbox.aggregate_id=product_id),
# иначе остаточные не-STRESS события из прежних прогонов раздувают счётчик.
SHIP_CHAIN=$(PSQL "SELECT count(*) FROM public.onchain_events oe JOIN public.outbox_events ob USING(event_id) JOIN wms_inventory.products p ON p.product_id=ob.aggregate_id WHERE oe.aggregate_type='shipping' AND oe.status='COMMITTED' AND p.qr_code LIKE 'STRESS-QR-%'")
PICK_WMS=$(PSQL "SELECT count(*) FROM wms_inventory.products WHERE qr_code LIKE 'STRESS-QR-%' AND status IN ('ASSEMBLED','READY_TO_SHIP','SHIPPED')")
PICK_CHAIN=$(PSQL "SELECT count(*) FROM public.onchain_events oe JOIN public.outbox_events ob USING(event_id) JOIN wms_inventory.products p ON p.product_id=ob.aggregate_id WHERE oe.aggregate_type='picking' AND oe.status='COMMITTED' AND p.qr_code LIKE 'STRESS-QR-%'")
echo "  SHIPPED(wms)=$SHIPPED_WMS  ==  shipping COMMITTED(chain)=$SHIP_CHAIN   -> $([ "$SHIPPED_WMS" = "$SHIP_CHAIN" ] && echo OK || echo MISMATCH)"
echo "  picked+(wms)=$PICK_WMS    ~=  picking COMMITTED(chain)=$PICK_CHAIN"
echo "  STANDALONE: 06-пул STRESS-PROD-QR STORED после прогона = $(PSQL "SELECT count(*) FROM wms_inventory.products WHERE qr_code LIKE 'STRESS-PROD-QR%' AND status='STORED'") (ожидаем 2000 — 07 не трогает чужой пул)"

echo "=== 7. on-chain TRACE (PASS BAR: real ItemTransition, не Failed/skip) ==="
RPCRAW=$(docker run --rm -v stresstest_shared_state:/s busybox cat /s/rpc_url.txt 2>/dev/null)
RPC=$(echo "$RPCRAW" | sed 's#subnet-node1:9650#localhost:9650#')
echo "  rpc=$RPC"
KTX=$(RAW "SELECT tx_hash FROM public.onchain_events WHERE status='COMMITTED' AND aggregate_type='picking' AND tx_hash<>'' ORDER BY created_at DESC LIMIT 1" | tr -d '[:space:]')
STX=$(RAW "SELECT tx_hash FROM public.onchain_events WHERE status='COMMITTED' AND aggregate_type='shipping' AND tx_hash<>'' ORDER BY created_at DESC LIMIT 1" | tr -d '[:space:]')
if [ -n "$KTX" ]; then echo "  picking  tx=$KTX"; cast run "$KTX" --rpc-url "$RPC" 2>&1 | grep -oE "ItemTransition(Failed)?\(" | sort | uniq -c; else echo "  no committed picking tx"; fi
if [ -n "$STX" ]; then echo "  shipping tx=$STX"; cast run "$STX" --rpc-url "$RPC" 2>&1 | grep -oE "ItemTransition(Failed)?\(" | sort | uniq -c; else echo "  no committed shipping tx"; fi
echo "############ DONE ############"
