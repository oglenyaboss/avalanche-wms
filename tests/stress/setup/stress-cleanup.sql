-- stress-cleanup.sql
-- Удаляет данные, созданные stress-seed.sql, для повторного прогона тестов.
-- Порядок DELETE соблюдает FK-зависимости (дети раньше родителей).
--
-- Выполнение:
--   docker exec -i postgres_db psql -U root -d wms_blockchain_db \
--     < tests/stress/setup/stress-cleanup.sql

BEGIN;

-- onchain_events и outbox_events для stress-продуктов и stress-грузомест
DELETE FROM public.onchain_events
WHERE event_id IN (
  SELECT oe.event_id FROM public.outbox_events oe
  WHERE oe.aggregate_id IN (
    SELECT p.product_id FROM wms_inventory.products p
    WHERE p.qr_code LIKE 'STRESS-%'
    UNION
    SELECT c.cargoplace_id FROM wms_inventory.cargoplaces c
    WHERE c.cargoplace_code LIKE 'STRESS-%'
  )
);
DELETE FROM public.outbox_events
WHERE aggregate_id IN (
  SELECT p.product_id FROM wms_inventory.products p
  WHERE p.qr_code LIKE 'STRESS-%'
  UNION
  SELECT c.cargoplace_id FROM wms_inventory.cargoplaces c
  WHERE c.cargoplace_code LIKE 'STRESS-%'
);

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

COMMIT;
