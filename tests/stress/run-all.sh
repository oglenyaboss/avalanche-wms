#!/bin/sh
# run-all.sh — последовательный запуск всех нагрузочных тестов (01–08).
#
# Запускается внутри Docker-контейнера k6 (профиль stress).
# Шаги:
#   1. Очистка предыдущих stress-данных (допустим сбой — первый прогон)
#   2. Засев данных (stress-seed.sql)
#   3. Генерация JSON-файлов UUID грузомест и продуктов для SharedArray
#   4. Разрешение UUID ячеек и рейсов через psql
#   5. Последовательный запуск тестов 01–08
#   6. Мониторинг blockchain pipeline после теста 08
#   7. Итоговый отчёт

DB_HOST="${DB_HOST:-postgres}"
DB_PORT="${DB_PORT:-5432}"
DB_USER="${DB_USER:-root}"
DB_PASSWORD="${DB_PASSWORD:-root}"
DB_NAME="${DB_NAME:-wms_blockchain_db}"
WMS_URL="${WMS_URL:-http://wms-service:8080}"

export PGPASSWORD="${DB_PASSWORD}"

# Выполнить SQL-запрос и вернуть одну строку без пробелов
psql_q() {
    psql -h "${DB_HOST}" -p "${DB_PORT}" -U "${DB_USER}" -d "${DB_NAME}" \
         -tAc "$1" | tr -d '[:space:]'
}

echo "=========================================="
echo "  WMS Stress Tests — запуск всех сценариев"
echo "  WMS_URL : ${WMS_URL}"
echo "  DB      : ${DB_USER}@${DB_HOST}:${DB_PORT}/${DB_NAME}"
echo "=========================================="

# ── 1. Очистка предыдущих данных ─────────────────────────────────────────────
echo ""
echo "[SEED] Очистка предыдущих stress-данных..."
psql -h "${DB_HOST}" -p "${DB_PORT}" -U "${DB_USER}" -d "${DB_NAME}" \
     -f /tests/stress/setup/stress-cleanup.sql > /dev/null 2>&1 || true

# ── 2. Засев данных ───────────────────────────────────────────────────────────
echo "[SEED] Засев данных (stress-seed.sql)..."
psql -h "${DB_HOST}" -p "${DB_PORT}" -U "${DB_USER}" -d "${DB_NAME}" \
     -f /tests/stress/setup/stress-seed.sql > /dev/null \
    || { echo "[SEED] FATAL: stress-seed.sql завершился с ошибкой." >&2; exit 1; }

# ── 2.5. Регистрация Debezium outbox-коннектора ──────────────────────────────
# Без зарегистрированного коннектора Debezium не следит за public.outbox_events,
# сообщения не попадают в Kafka, ledger-adapter не пишет onchain_events.
DEBEZIUM_URL="${DEBEZIUM_URL:-http://debezium-connect:8083}"

echo ""
echo "[DEBEZIUM] Ожидание готовности Debezium Connect..."
for i in $(seq 1 24); do
    if curl -sf "${DEBEZIUM_URL}/connectors" > /dev/null 2>&1; then
        echo "[DEBEZIUM] Готов (попытка ${i}/24)."
        break
    fi
    if [ "${i}" -eq 24 ]; then
        echo "[DEBEZIUM] WARN: Debezium Connect не ответил за 120s. Продолжаем без гарантии CDC." >&2
    fi
    sleep 5
done

CONN_STATUS=$(curl -sf -o /dev/null -w "%{http_code}" \
    "${DEBEZIUM_URL}/connectors/outbox-connector" 2>/dev/null || echo "000")

if [ "${CONN_STATUS}" = "200" ]; then
    echo "[DEBEZIUM] Коннектор outbox-connector уже зарегистрирован."
else
    echo "[DEBEZIUM] Регистрация коннектора outbox-connector..."
    curl -sf -X POST "${DEBEZIUM_URL}/connectors" \
        -H "Content-Type: application/json" \
        --data-binary '{
  "name": "outbox-connector",
  "config": {
    "connector.class": "io.debezium.connector.postgresql.PostgresConnector",
    "database.hostname": "postgres",
    "database.port": "5432",
    "database.user": "${env:DB_USER}",
    "database.password": "${env:DB_PASSWORD}",
    "database.dbname": "${env:DB_NAME}",
    "topic.prefix": "wms",
    "table.include.list": "public.outbox_events",
    "plugin.name": "pgoutput",
    "slot.name": "debezium_outbox",
    "publication.name": "outbox_publication",
    "publication.autocreate.mode": "filtered",
    "snapshot.mode": "never",
    "max.batch.size": 2048,
    "max.queue.size": 8192,
    "poll.interval.ms": 5000,
    "heartbeat.interval.ms": 10000,
    "transforms": "outbox",
    "transforms.outbox.type": "io.debezium.transforms.outbox.EventRouter",
    "transforms.outbox.table.field.event.id": "event_id",
    "transforms.outbox.table.field.event.key": "aggregate_id",
    "transforms.outbox.table.field.event.type": "event_type",
    "transforms.outbox.table.field.event.payload": "payload_hash",
    "transforms.outbox.route.by.field": "aggregate_type",
    "transforms.outbox.route.topic.replacement": "wms.events.v1",
    "transforms.outbox.table.fields.additional.placement": "aggregate_type:header:aggregate_type",
    "transforms.outbox.table.expand.json.payload": "false",
    "key.converter": "org.apache.kafka.connect.storage.StringConverter",
    "value.converter": "org.apache.kafka.connect.json.JsonConverter",
    "value.converter.schemas.enable": "false"
  }
}' \
        && echo "[DEBEZIUM] Коннектор зарегистрирован." \
        || echo "[DEBEZIUM] WARN: не удалось зарегистрировать коннектор. Проверьте доступность Debezium." >&2

    # Пауза для захвата WAL-слота. snapshot.mode=never → события до регистрации
    # не захватываются; события ПОСЛЕ регистрации попадают в Kafka.
    # Seed-данные не создают outbox-событий, поэтому потерь нет.
    echo "[DEBEZIUM] Пауза 10s для захвата WAL-слота..."
    sleep 10
fi

# ── 3. Генерация JSON с UUID грузомест и продуктов ───────────────────────────
# Тест 05 использует STRESS-TABLE-CP-* (stress-table-cps.json).
# Тест 07 использует STRESS-FLOW-CP-*  (stress-flow-cps.json).
# Тест 08 использует STRESS-TPS-QR-*  (stress-tps-products.json).

echo "[SEED] Генерация /tmp/stress-table-cps.json (тест 05)..."
psql -h "${DB_HOST}" -p "${DB_PORT}" -U "${DB_USER}" -d "${DB_NAME}" \
    -tAc "SELECT COALESCE(json_agg(cargoplace_id::text ORDER BY cargoplace_code), '[]')
          FROM wms_inventory.cargoplaces
          WHERE cargoplace_code LIKE 'STRESS-TABLE-CP-%'" \
    | tr -d '[:space:]' > /tmp/stress-table-cps.json

CP_COUNT=$(psql_q "SELECT count(*) FROM wms_inventory.cargoplaces
                   WHERE cargoplace_code LIKE 'STRESS-TABLE-CP-%'")
if [ "${CP_COUNT:-0}" -eq 0 ]; then
    echo "[SEED] FATAL: грузоместа STRESS-TABLE-CP-* не найдены после засева." >&2
    exit 1
fi
echo "[SEED] /tmp/stress-table-cps.json: ${CP_COUNT} UUID готово"

echo "[SEED] Генерация /tmp/stress-flow-cps.json (тест 07)..."
psql -h "${DB_HOST}" -p "${DB_PORT}" -U "${DB_USER}" -d "${DB_NAME}" \
    -tAc "SELECT COALESCE(json_agg(cargoplace_id::text ORDER BY cargoplace_code), '[]')
          FROM wms_inventory.cargoplaces
          WHERE cargoplace_code LIKE 'STRESS-FLOW-CP-%'" \
    | tr -d '[:space:]' > /tmp/stress-flow-cps.json

FLOW_CP_COUNT=$(psql_q "SELECT count(*) FROM wms_inventory.cargoplaces
                        WHERE cargoplace_code LIKE 'STRESS-FLOW-CP-%'")
if [ "${FLOW_CP_COUNT:-0}" -eq 0 ]; then
    echo "[SEED] FATAL: грузоместа STRESS-FLOW-CP-* не найдены после засева." >&2
    exit 1
fi
echo "[SEED] /tmp/stress-flow-cps.json: ${FLOW_CP_COUNT} UUID готово"

echo "[SEED] Генерация /tmp/stress-tps-products.json (тест 08)..."
psql -h "${DB_HOST}" -p "${DB_PORT}" -U "${DB_USER}" -d "${DB_NAME}" \
    -tAc "SELECT COALESCE(json_agg(product_id::text ORDER BY qr_code), '[]')
          FROM wms_inventory.products
          WHERE qr_code LIKE 'STRESS-TPS-QR-%'" \
    | tr -d '[:space:]' > /tmp/stress-tps-products.json

TPS_PROD_COUNT=$(psql_q "SELECT count(*) FROM wms_inventory.products
                         WHERE qr_code LIKE 'STRESS-TPS-QR-%'")
if [ "${TPS_PROD_COUNT:-0}" -eq 0 ]; then
    echo "[SEED] FATAL: продукты STRESS-TPS-QR-* не найдены после засева." >&2
    exit 1
fi
echo "[SEED] /tmp/stress-tps-products.json: ${TPS_PROD_COUNT} UUID готово"

# ── 4. Разрешение UUID ячеек и рейсов ────────────────────────────────────────
echo "[SEED] Разрешение UUID ячеек и рейсов..."

RECEIVING_BIN_ID=$(psql_q "
  SELECT b.bin_id FROM wms_inventory.bins b
  JOIN wms_inventory.warehouses w ON w.warehouse_id = b.warehouse_id
  WHERE b.code = 'BUFFER-01' AND w.name = 'Склад Москва-Север'
  LIMIT 1")

STORAGE_BIN_ID=$(psql_q "
  SELECT b.bin_id FROM wms_inventory.bins b
  JOIN wms_inventory.warehouses w ON w.warehouse_id = b.warehouse_id
  WHERE b.code = 'A-01-01' AND w.name = 'Склад Москва-Север'
  LIMIT 1")

# SHOP-7: для теста 06
DESTINATION_ID=$(psql_q "
  SELECT d.destination_id FROM wms_inventory.destinations d
  JOIN wms_inventory.warehouses w ON w.warehouse_id = d.warehouse_id
  WHERE d.code = 'SHOP-7' AND w.name = 'Склад Москва-Север'
  LIMIT 1")

SHIPPING_BIN_ID=$(psql_q "
  SELECT b.bin_id FROM wms_inventory.bins b
  WHERE b.destination_id = '${DESTINATION_ID}'
    AND b.section = 'SHIPPING_BUFFER'
  LIMIT 1")

DISPATCH_CODE=$(psql_q "
  SELECT d.dispatch_code FROM wms_inventory.outbound_dispatches d
  WHERE d.dispatch_code LIKE 'STRESS-DSP-%'
    AND d.status = 'SCHEDULED'
  ORDER BY d.dispatch_code
  LIMIT 1")

# SHOP-5: для теста 07 (изолированный пул, не конкурирует с тестом 06)
DESTINATION_ID_07=$(psql_q "
  SELECT d.destination_id FROM wms_inventory.destinations d
  JOIN wms_inventory.warehouses w ON w.warehouse_id = d.warehouse_id
  WHERE d.code = 'SHOP-5' AND w.name = 'Склад Москва-Север'
  LIMIT 1")

SHIPPING_BIN_ID_07=$(psql_q "
  SELECT b.bin_id FROM wms_inventory.bins b
  WHERE b.destination_id = '${DESTINATION_ID_07}'
    AND b.section = 'SHIPPING_BUFFER'
  LIMIT 1")

DISPATCH_CODE_07=$(psql -h "${DB_HOST}" -p "${DB_PORT}" -U "${DB_USER}" -d "${DB_NAME}" \
  -tAc "SELECT string_agg(d.dispatch_code, ',' ORDER BY d.dispatch_code)
        FROM wms_inventory.outbound_dispatches d
        WHERE d.dispatch_code LIKE 'STRESS-FLOW-DSP-%'
          AND d.status = 'SCHEDULED'" | tr -d '[:space:]')

if [ -z "${RECEIVING_BIN_ID}" ]   || [ -z "${STORAGE_BIN_ID}" ]    || \
   [ -z "${DESTINATION_ID}" ]     || [ -z "${SHIPPING_BIN_ID}" ]    || \
   [ -z "${DISPATCH_CODE}" ]      || [ -z "${DESTINATION_ID_07}" ]  || \
   [ -z "${SHIPPING_BIN_ID_07}" ] || [ -z "${DISPATCH_CODE_07}" ]; then
    echo "[SEED] FATAL: не удалось разрешить один или несколько UUID." >&2
    echo "       Убедитесь, что stress-seed.sql и dev-seed выполнены корректно." >&2
    exit 1
fi

echo "[SEED] Готово:"
echo "  RECEIVING_BIN_ID  = ${RECEIVING_BIN_ID}"
echo "  STORAGE_BIN_ID    = ${STORAGE_BIN_ID}"
echo "  DESTINATION_ID    = ${DESTINATION_ID}  (SHOP-7, тест 06)"
echo "  SHIPPING_BIN_ID   = ${SHIPPING_BIN_ID}"
echo "  DISPATCH_CODE     = ${DISPATCH_CODE}"
echo "  DESTINATION_ID_07 = ${DESTINATION_ID_07} (SHOP-5, тест 07)"
echo "  SHIPPING_BIN_ID_07= ${SHIPPING_BIN_ID_07}"
echo "  DISPATCH_CODE_07  = ${DISPATCH_CODE_07}"

# ── 5. Функция запуска одного теста ──────────────────────────────────────────
# Принимает имя файла как $1, остальные аргументы передаются в k6.
# Не прерывает прогон при превышении порогов — собирает счётчик сбоев.
FAILED=0

run_test() {
    NAME="$1"; shift
    echo ""
    echo "══════════════════════════════════════════"
    printf "  %s\n" "${NAME}"
    echo "══════════════════════════════════════════"
    if ! k6 run --no-usage-report \
              -e "WMS_URL=${WMS_URL}" \
              "$@" \
              "/tests/stress/${NAME}"; then
        echo "  [WARN] ${NAME}: тест завершён с ненулевым кодом (порог не пройден или ошибка)"
        FAILED=$((FAILED + 1))
    fi
}

# ── 6. Прогон сценариев 01–07 ─────────────────────────────────────────────────

run_test "01-smoke.js"

run_test "02-health.js"

run_test "03-auth.js"

run_test "04-receiving-gate.js"

run_test "05-receiving-table.js" \
    -e "RECEIVING_BIN_ID=${RECEIVING_BIN_ID}"

run_test "06-assembly.js" \
    -e "DESTINATION_ID=${DESTINATION_ID}" \
    -e "SHIPPING_BIN_ID=${SHIPPING_BIN_ID}"

run_test "07-full-flow.js" \
    -e "RECEIVING_BIN_ID=${RECEIVING_BIN_ID}" \
    -e "STORAGE_BIN_ID=${STORAGE_BIN_ID}" \
    -e "DESTINATION_ID=${DESTINATION_ID_07}" \
    -e "SHIPPING_BIN_ID=${SHIPPING_BIN_ID_07}" \
    -e "DISPATCH_CODE=${DISPATCH_CODE_07}"

# ── 7. Blockchain TPS: тест 08 + мониторинг pipeline ─────────────────────────
# Тест 08 генерирует 5000 putaway-событий через WMS API (синхронная часть).
# После завершения теста run-all.sh ждёт 60s и измеряет, сколько событий
# прошло через весь pipeline (Debezium → Kafka → ledger-adapter → blockchain).
echo ""
echo "══════════════════════════════════════════"
echo "  Blockchain TPS — подготовка мониторинга"
echo "══════════════════════════════════════════"

OUTBOX_BEFORE=$(psql_q "SELECT count(*) FROM public.outbox_events")
ONCHAIN_COMMITTED_BEFORE=$(psql_q "SELECT count(*) FROM public.onchain_events WHERE status = 'COMMITTED'")
TIME_BEFORE=$(date +%s)

echo "  Baseline: outbox_events=${OUTBOX_BEFORE}, onchain_COMMITTED=${ONCHAIN_COMMITTED_BEFORE}"

run_test "08-blockchain-tps.js" \
    -e "RECEIVING_BIN_ID=${RECEIVING_BIN_ID}" \
    -e "STORAGE_BIN_ID=${STORAGE_BIN_ID}"

TIME_AFTER_TEST=$(date +%s)
TEST_DURATION=$((TIME_AFTER_TEST - TIME_BEFORE))
OUTBOX_AFTER=$(psql_q "SELECT count(*) FROM public.outbox_events")
OUTBOX_DELTA=$((OUTBOX_AFTER - OUTBOX_BEFORE))
WMS_EPS=$(echo "scale=1; if (${TEST_DURATION} > 0) ${OUTBOX_DELTA} / ${TEST_DURATION} else 0" \
    | bc 2>/dev/null || echo "n/a")

echo ""
echo "══════════════════════════════════════════"
echo "  Blockchain pipeline: результаты"
echo "══════════════════════════════════════════"
echo ""
echo "  [WMS — синхронная часть]"
echo "    outbox_events создано : ${OUTBOX_DELTA}"
echo "    время теста 08        : ${TEST_DURATION}s"
echo "    WMS event rate        : ${WMS_EPS} events/s"
echo ""
echo "  Ожидание async pipeline (60s)..."
echo "  (Debezium CDC → Kafka → ledger-adapter → Subnet-EVM contract)"
sleep 60

ONCHAIN_COMMITTED_AFTER=$(psql_q "SELECT count(*) FROM public.onchain_events WHERE status = 'COMMITTED'")
ONCHAIN_PENDING=$(psql_q "SELECT count(*) FROM public.onchain_events WHERE status IN ('PENDING', 'SENT')")
ONCHAIN_FAILED=$(psql_q "SELECT count(*) FROM public.onchain_events WHERE status = 'FAILED'")
OUTBOX_BACKLOG=$(psql_q "
  SELECT count(*) FROM public.outbox_events oe
  WHERE NOT EXISTS (
    SELECT 1 FROM public.onchain_events oc WHERE oc.event_id = oe.event_id
  )")
ONCHAIN_DELTA=$((ONCHAIN_COMMITTED_AFTER - ONCHAIN_COMMITTED_BEFORE))
TIME_ELAPSED=$(( $(date +%s) - TIME_BEFORE ))
CHAIN_EPS=$(echo "scale=1; if (${TIME_ELAPSED} > 0) ${ONCHAIN_DELTA} / ${TIME_ELAPSED} else 0" \
    | bc 2>/dev/null || echo "n/a")

echo ""
echo "  [Blockchain — асинхронная часть, +60s после теста]"
echo "    onchain COMMITTED     : +${ONCHAIN_DELTA} (всего ${ONCHAIN_COMMITTED_AFTER})"
echo "    blockchain commit rate: ${CHAIN_EPS} events/s (за ${TIME_ELAPSED}s)"
echo "    onchain PENDING/SENT  : ${ONCHAIN_PENDING}"
echo "    onchain FAILED        : ${ONCHAIN_FAILED}"
echo "    outbox backlog        : ${OUTBOX_BACKLOG} (не обработаны Debezium/Kafka)"
echo ""
if [ "${ONCHAIN_DELTA}" -gt 0 ]; then
    echo "  ✓ Blockchain pipeline активен: ${ONCHAIN_DELTA} событий подтверждено"
elif [ "${OUTBOX_BACKLOG}" -gt 0 ]; then
    echo "  ✗ Blockchain pipeline не обработал события за ${TIME_ELAPSED}s"
    echo "    Проверьте:"
    echo "      1. Debezium-коннектор запущен (docker ps | grep debezium)"
    echo "      2. PRIVATE_KEY задан в .env (не нулевой ключ)"
    echo "      3. CONTRACT_ADDR задан в .env"
    echo "      4. RPC_URL доступен из контейнера ledger-adapter"
else
    echo "  ─ Blockchain pipeline: нет новых событий для обработки"
fi
echo "══════════════════════════════════════════"

# ── 8. Итог ───────────────────────────────────────────────────────────────────
echo ""
echo "=========================================="
if [ "${FAILED}" -eq 0 ]; then
    echo "  Все 8 тестов пройдены успешно."
else
    echo "  Завершено: ${FAILED} из 8 тестов не прошли пороговые значения."
fi
echo "=========================================="

exit "${FAILED}"
