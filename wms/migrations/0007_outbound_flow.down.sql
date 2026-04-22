BEGIN;

-- 1. Вернуть vehicle_number до удаления dispatch_id и outbound_dispatches.
ALTER TABLE wms_ops.shippings
  ADD COLUMN vehicle_number text;

COMMENT ON COLUMN wms_ops.shippings.vehicle_number
  IS 'Vehicle number used for shipping.';

UPDATE wms_ops.shippings AS s
SET vehicle_number = d.vehicle_number
FROM wms_inventory.outbound_dispatches AS d
WHERE s.dispatch_id = d.dispatch_id;

-- 2. Удалить новые FK-колонки в обратном порядке зависимостей.
DROP INDEX IF EXISTS wms_ops.idx_shippings_dispatch_id;
ALTER TABLE wms_ops.shippings DROP COLUMN IF EXISTS dispatch_id;

DROP INDEX IF EXISTS wms_ops.idx_assembly_tasks_destination_id;
ALTER TABLE wms_ops.assembly_tasks DROP COLUMN IF EXISTS destination_id;

DROP INDEX IF EXISTS wms_inventory.idx_bins_destination_id;
ALTER TABLE wms_inventory.bins
  DROP CONSTRAINT IF EXISTS bins_shipping_buffer_requires_destination;
ALTER TABLE wms_inventory.bins DROP COLUMN IF EXISTS destination_id;

DROP INDEX IF EXISTS wms_inventory.idx_orders_destination_id;
ALTER TABLE wms_inventory.orders DROP COLUMN IF EXISTS destination_id;

-- 3. Удалить новые таблицы исходящего потока.
DROP TABLE IF EXISTS wms_inventory.outbound_dispatches;
DROP TABLE IF EXISTS wms_inventory.order_lines;
DROP TABLE IF EXISTS wms_inventory.destinations;

-- 4. Восстановить старый ENUM order_status из 6 значений.
ALTER TYPE wms_inventory.order_status RENAME TO order_status_new;

CREATE TYPE wms_inventory.order_status AS ENUM (
  'NEW',
  'ALLOCATED',
  'ASSEMBLY_IN_PROGRESS',
  'ASSEMBLED',
  'READY_TO_SHIP',
  'SHIPPED'
);

ALTER TABLE wms_inventory.orders
  ALTER COLUMN status DROP DEFAULT;

ALTER TABLE wms_inventory.orders
  ALTER COLUMN status TYPE wms_inventory.order_status
  USING (status::text::wms_inventory.order_status);

ALTER TABLE wms_inventory.orders
  ALTER COLUMN status SET DEFAULT 'NEW';

DROP TYPE wms_inventory.order_status_new;

-- 5. Удалить новый ENUM.
DROP TYPE IF EXISTS wms_inventory.outbound_dispatch_status;

DELETE FROM public.schema_migrations WHERE version = 7;

COMMIT;
