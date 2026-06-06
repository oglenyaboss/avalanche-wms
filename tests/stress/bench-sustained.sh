#!/bin/bash
# Sustained chain-throughput bench: stop adapter, queue CYCLES x 5000 events in Kafka,
# then drain at BATCH_SIZE and measure the slope (peak 2s-window = sustained rate).
# Usage: bench-sustained.sh <label> <batch_size> <cycles>
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1
PSQL(){ docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "$1" 2>/dev/null | tr -d '[:space:]'; }
BK(){ PSQL "SELECT count(*) FROM public.outbox_events oe WHERE NOT EXISTS (SELECT 1 FROM public.onchain_events oc WHERE oc.event_id=oe.event_id)"; }
COM(){ PSQL "SELECT count(*) FROM public.onchain_events WHERE status='COMMITTED'"; }
# bin UUID резолвим динамически (на свежем сиде они случайные): из склада, где лежат TPS-товары.
WH=$(PSQL "SELECT b.warehouse_id FROM wms_inventory.bins b JOIN wms_inventory.products p ON p.bin_id=b.bin_id WHERE p.qr_code LIKE 'STRESS-TPS-QR-%' LIMIT 1")
RECV=$(PSQL "SELECT bin_id FROM wms_inventory.bins WHERE code='BUFFER-01' AND warehouse_id=$WH")
STOR=$(PSQL "SELECT bin_id FROM wms_inventory.bins WHERE code='A-01-01' AND warehouse_id=$WH")
LABEL="${1:-sustained}"; BS="${2:-3500}"; CYCLES="${3:-4}"
echo "=== bench-sustained [$LABEL] bs=$BS, ${CYCLES}x5000 backlog ==="
docker compose -p stresstest stop ledger-adapter >/dev/null 2>&1; echo "[adapter stopped — queueing backlog]"
ING_START=$(date +%s)
for c in $(seq 1 "$CYCLES"); do
  docker exec postgres_db psql -U root -d wms_blockchain_db -c "DELETE FROM wms_ops.putaways WHERE product_id IN (SELECT product_id FROM wms_inventory.products WHERE qr_code LIKE 'STRESS-TPS-QR-%'); UPDATE wms_inventory.products SET status='RECEIVED', bin_id='$RECV' WHERE qr_code LIKE 'STRESS-TPS-QR-%';" >/dev/null 2>&1
  PSQL "SELECT COALESCE(json_agg(product_id::text ORDER BY qr_code),'[]') FROM wms_inventory.products WHERE qr_code LIKE 'STRESS-TPS-QR-%'" > /tmp/stress-tps-products.json
  k6 run --no-usage-report --quiet -e WMS_URL=http://localhost:8081 -e RECEIVING_BIN_ID=$RECV -e STORAGE_BIN_ID=$STOR tests/stress/08-blockchain-tps.js >/dev/null 2>&1
  echo "  cycle $c done — backlog=$(BK)"
done
ING_ELAPSED=$(( $(date +%s) - ING_START ))
C0=$(COM); QUEUED=$(BK); TARGET=$((C0+QUEUED)); T0=$(date +%s)
[ "$ING_ELAPSED" -gt 0 ] && ING_RATE=$(( QUEUED / ING_ELAPSED )) || ING_RATE=0
echo "[ingestion: queued $QUEUED events (WMS→outbox→Kafka) in ${ING_ELAPSED}s = ${ING_RATE}/s front-half]"
echo "[start adapter bs=$BS — draining $QUEUED backlog from committed=$C0, target=$TARGET]"
BATCH_SIZE=$BS BATCH_TIMEOUT=2s PIPELINE_WINDOW=${PIPELINE_WINDOW:-3} docker compose -p stresstest up -d --force-recreate --no-deps ledger-adapter >/dev/null 2>&1
echo "[adapter up: BATCH_SIZE=$BS BATCH_TIMEOUT=2s PIPELINE_WINDOW=${PIPELINE_WINDOW:-3}]"
# Меряем COMMITTED (on-chain), НЕ backlog: на быстром subnet-evm бэклог (outbox→onchain_events)
# сливается мгновенно, а коммиты подтверждаются позже — выход по backlog недосчитывал бы.
# Стоп: достигнут TARGET, либо плато (бэклог слит + коммиты не растут 2 цикла = провалы tx).
PREV=$C0; PEAK=0; STALL=0
for i in $(seq 1 60); do
  sleep 2
  C=$(COM); B=$(BK)
  R=$(( (C-PREV)/2 )); [ $R -gt $PEAK ] && PEAK=$R
  echo "t=+$((i*2))s committed=$C (+$((C-PREV)) = ${R}/s) backlog=$B"
  if [ "$((C-PREV))" -eq 0 ] && [ "${B:-9999}" -le 10 ]; then STALL=$((STALL+1)); else STALL=0; fi
  PREV=$C
  [ "$C" -ge "$TARGET" ] && { echo "all committed"; break; }
  [ "$STALL" -ge 2 ] && { echo "plateau — коммиты встали, бэклог слит (вероятны провалы tx)"; break; }
done
FAILED=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='FAILED'")
echo "=== [$LABEL] PEAK_SUSTAINED=${PEAK}/s  committed=$((PREV-C0))/$QUEUED  total_failed=$FAILED ==="
