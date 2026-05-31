#!/usr/bin/env bash
# generate-stress-data.sh
#
# Запрашивает из БД UUID нужных ячеек, магазинов и рейсов,
# выводит команды `export` для использования в тестах 06 и 07.
#
# Использование:
#   source tests/stress/setup/generate-stress-data.sh
# или:
#   eval "$(tests/stress/setup/generate-stress-data.sh)"
#
# После выполнения будут установлены переменные:
#   RECEIVING_BIN_ID   — UUID ячейки буфера приёмки (BUFFER-01)
#   STORAGE_BIN_ID     — UUID ячейки хранения (A-01-01)
#   DESTINATION_ID     — UUID направления SHOP-7
#   SHIPPING_BIN_ID    — UUID буфера отгрузки для SHOP-7
#   DISPATCH_CODE      — первый доступный рейс STRESS-DSP-* в статусе SCHEDULED

set -euo pipefail

DB_URL="${DB_URL:-postgres://root:root@localhost:5432/wms_blockchain_db?sslmode=disable}"

query() {
  psql "$DB_URL" -tAc "$1" | tr -d '[:space:]'
}

RECEIVING_BIN_ID=$(query "
  SELECT b.bin_id FROM wms_inventory.bins b
  JOIN wms_inventory.warehouses w ON w.warehouse_id = b.warehouse_id
  WHERE b.code = 'BUFFER-01' AND w.name = 'Склад Москва-Север'
  LIMIT 1
")

STORAGE_BIN_ID=$(query "
  SELECT b.bin_id FROM wms_inventory.bins b
  JOIN wms_inventory.warehouses w ON w.warehouse_id = b.warehouse_id
  WHERE b.code = 'A-01-01' AND w.name = 'Склад Москва-Север'
  LIMIT 1
")

DESTINATION_ID=$(query "
  SELECT d.destination_id FROM wms_inventory.destinations d
  JOIN wms_inventory.warehouses w ON w.warehouse_id = d.warehouse_id
  WHERE d.code = 'SHOP-7' AND w.name = 'Склад Москва-Север'
  LIMIT 1
")

SHIPPING_BIN_ID=$(query "
  SELECT b.bin_id FROM wms_inventory.bins b
  WHERE b.destination_id = '${DESTINATION_ID}'
    AND b.section = 'SHIPPING_BUFFER'
  LIMIT 1
")

DISPATCH_CODE=$(query "
  SELECT d.dispatch_code FROM wms_inventory.outbound_dispatches d
  WHERE d.dispatch_code LIKE 'STRESS-DSP-%'
    AND d.status = 'SCHEDULED'
  ORDER BY d.dispatch_code
  LIMIT 1
")

if [ -z "$RECEIVING_BIN_ID" ] || [ -z "$STORAGE_BIN_ID" ] || \
   [ -z "$DESTINATION_ID" ]   || [ -z "$SHIPPING_BIN_ID" ] || \
   [ -z "$DISPATCH_CODE" ]; then
  echo "FATAL: one or more IDs could not be resolved. Run stress-seed.sql first." >&2
  exit 1
fi

echo "export RECEIVING_BIN_ID=${RECEIVING_BIN_ID}"
echo "export STORAGE_BIN_ID=${STORAGE_BIN_ID}"
echo "export DESTINATION_ID=${DESTINATION_ID}"
echo "export SHIPPING_BIN_ID=${SHIPPING_BIN_ID}"
echo "export DISPATCH_CODE=${DISPATCH_CODE}"

# Дополнительно: UUID грузомест для тестов 05 и 07 (первые 20 для проверки)
echo ""
echo "# Пример первых 5 UUID грузомест для receiving-table (05):"
psql "$DB_URL" -tAc "
  SELECT c.cargoplace_id, c.cargoplace_code
  FROM wms_inventory.cargoplaces c
  WHERE c.cargoplace_code LIKE 'STRESS-TABLE-CP-%'
  ORDER BY c.cargoplace_code
  LIMIT 5
" | while IFS='|' read -r uuid code; do
  echo "#   $code → $uuid"
done
