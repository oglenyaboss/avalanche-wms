#!/bin/sh
# run-all.sh — последовательный запуск всех нагрузочных тестов (01–07).
#
# Запускается внутри Docker-контейнера k6 (профиль stress).
# Шаги:
#   1. Очистка предыдущих stress-данных (допустим сбой — первый прогон)
#   2. Засев данных (stress-seed.sql)
#   3. Разрешение UUID ячеек и рейсов через psql
#   4. Последовательный запуск тестов 01–07
#   5. Итоговый отчёт

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

# ── 3. Генерация JSON с UUID грузомест ───────────────────────────────────────
# Тест 05 использует STRESS-TABLE-CP-* (stress-table-cps.json).
# Тест 07 использует STRESS-FLOW-CP-*  (stress-flow-cps.json) — отдельный пул,
# чтобы тест 07 не получал уже закрытые (TABLE_CLOSED) грузоместа из теста 05.
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

# Тест 07 использует отдельный пул: SHOP-5 (чтобы не конкурировать с тестом 06 за заказы SHOP-7).
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

# ── 4. Функция запуска одного теста ──────────────────────────────────────────
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

# ── 5. Прогон сценариев ───────────────────────────────────────────────────────

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

# ── 6. Итог ───────────────────────────────────────────────────────────────────
echo ""
echo "=========================================="
if [ "${FAILED}" -eq 0 ]; then
    echo "  Все 7 тестов пройдены успешно."
else
    echo "  Завершено: ${FAILED} из 7 тестов не прошли пороговые значения."
fi
echo "=========================================="

exit "${FAILED}"
