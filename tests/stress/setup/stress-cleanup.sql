-- stress-cleanup.sql
-- Удаляет данные, созданные stress-seed.sql, для повторного прогона тестов.
-- Порядок DELETE соблюдает FK-зависимости (дети раньше родителей).
--
-- Выполнение:
--   docker exec -i postgres_db psql -U root -d wms_blockchain_db \
--     < tests/stress/setup/stress-cleanup.sql

BEGIN;

-- Подписанные действия над stress-товарами: onchain_events и outbox_events
-- (event_id = action_id) и сами warehouse_actions — до удаления товаров.
CREATE TEMP TABLE stress_actions ON COMMIT DROP AS
SELECT wa.action_id FROM public.warehouse_actions wa
WHERE wa.item_ids && ARRAY(
  SELECT p.product_id FROM wms_inventory.products p WHERE p.qr_code LIKE 'STRESS-%'
);
DELETE FROM public.onchain_events WHERE event_id IN (SELECT action_id FROM stress_actions);
DELETE FROM public.outbox_events WHERE event_id IN (SELECT action_id FROM stress_actions);

-- Ops-таблицы
-- Удаляем отгрузки по товару ИЛИ по рейсу: shippings ссылается и на product_id,
-- и на dispatch_id. Без ветки по dispatch_id рейс STRESS с отгрузкой не-STRESS товара
-- (например, оставшегося в буфере от прежних прогонов) роняет финальный DELETE рейсов
-- по FK shippings_dispatch_id_fkey и откатывает весь cleanup.
DELETE FROM wms_ops.shippings
WHERE product_id IN (
  SELECT product_id FROM wms_inventory.products WHERE qr_code LIKE 'STRESS-%'
)
OR dispatch_id IN (
  SELECT dispatch_id FROM wms_inventory.outbound_dispatches WHERE dispatch_code LIKE 'STRESS-%'
);
DELETE FROM wms_ops.assembly_tasks
WHERE order_id IN (
  SELECT order_id FROM wms_inventory.orders WHERE external_order_no LIKE 'STRESS-%'
);
DELETE FROM wms_ops.putaways
WHERE product_id IN (
  SELECT product_id FROM wms_inventory.products WHERE qr_code LIKE 'STRESS-%'
);
DELETE FROM wms_ops.receiving_table
WHERE cargoplace_id IN (
  SELECT cargoplace_id FROM wms_inventory.cargoplaces
  WHERE cargoplace_code LIKE 'STRESS-%'
);
DELETE FROM wms_ops.receiving_gate
WHERE cargoplace_id IN (
  SELECT cargoplace_id FROM wms_inventory.cargoplaces
  WHERE cargoplace_code LIKE 'STRESS-%'
);

DELETE FROM public.warehouse_actions WHERE action_id IN (SELECT action_id FROM stress_actions);

-- Inventory
DELETE FROM wms_inventory.products WHERE qr_code LIKE 'STRESS-%';
DELETE FROM wms_inventory.boxes WHERE box_barcode LIKE 'STRESS-%';
DELETE FROM wms_inventory.expected_cargoplace_skus
WHERE cargoplace_id IN (
  SELECT cargoplace_id FROM wms_inventory.cargoplaces WHERE cargoplace_code LIKE 'STRESS-%'
);
DELETE FROM wms_inventory.order_lines
WHERE order_id IN (
  SELECT order_id FROM wms_inventory.orders WHERE external_order_no LIKE 'STRESS-%'
);
DELETE FROM wms_inventory.cargoplaces WHERE cargoplace_code LIKE 'STRESS-%';
DELETE FROM wms_inventory.inbound_shipments WHERE ttn_code LIKE 'STRESS-%';
DELETE FROM wms_inventory.orders WHERE external_order_no LIKE 'STRESS-%';
DELETE FROM wms_inventory.outbound_dispatches WHERE dispatch_code LIKE 'STRESS-%';

-- Справочники тестов 06/07 (stress-seed.sql §0.3-0.4): сначала ячейки, потом
-- магазины — bins.destination_id и orders/dispatches ссылаются на destinations.
DELETE FROM wms_inventory.bins WHERE code IN ('BIN-SHIP-S06', 'BIN-SHIP-S07');
DELETE FROM wms_inventory.destinations WHERE code IN ('STRESS-SHOP-06', 'STRESS-SHOP-07');
-- sku_barcodes уходят каскадом (ON DELETE CASCADE от skus).
DELETE FROM wms_inventory.skus WHERE name IN ('STRESS-ASM-SKU', 'STRESS-TPS-SKU');

COMMIT;
