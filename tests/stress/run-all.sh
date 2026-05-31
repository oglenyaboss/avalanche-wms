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
     -f /tests/stress/setup/stress-seed.sql > /dev/null

# ── 3. Разрешение UUID ────────────────────────────────────────────────────────
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

if [ -z "${RECEIVING_BIN_ID}" ] || [ -z "${STORAGE_BIN_ID}" ] || \
   [ -z "${DESTINATION_ID}" ]   || [ -z "${SHIPPING_BIN_ID}" ] || \
   [ -z "${DISPATCH_CODE}" ]; then
    echo "[SEED] FATAL: не удалось разрешить один или несколько UUID." >&2
    echo "       Убедитесь, что stress-seed.sql и dev-seed выполнены корректно." >&2
    exit 1
fi

echo "[SEED] Готово:"
echo "  RECEIVING_BIN_ID = ${RECEIVING_BIN_ID}"
echo "  STORAGE_BIN_ID   = ${STORAGE_BIN_ID}"
echo "  DESTINATION_ID   = ${DESTINATION_ID}"
echo "  SHIPPING_BIN_ID  = ${SHIPPING_BIN_ID}"
echo "  DISPATCH_CODE    = ${DISPATCH_CODE}"

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
    -e "DESTINATION_ID=${DESTINATION_ID}" \
    -e "SHIPPING_BIN_ID=${SHIPPING_BIN_ID}" \
    -e "DISPATCH_CODE=${DISPATCH_CODE}"

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
