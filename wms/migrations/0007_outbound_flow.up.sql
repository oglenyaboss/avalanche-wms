BEGIN;

-- 1. Новый ENUM для исходящих рейсов.
CREATE TYPE wms_inventory.outbound_dispatch_status AS ENUM (
  'SCHEDULED',
  'AT_GATE',
  'DEPARTED',
  'CANCELLED'
);

-- 2. Новые справочники/таблицы исходящего потока.
CREATE TABLE wms_inventory.destinations (
  destination_id uuid PRIMARY KEY,
  code text NOT NULL,
  name text NOT NULL,
  address text,
  warehouse_id bigint NOT NULL REFERENCES wms_inventory.warehouses(warehouse_id) ON DELETE RESTRICT,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT destinations_code_unique UNIQUE (code)
);

CREATE TRIGGER trg_destinations_set_updated_at
BEFORE UPDATE ON wms_inventory.destinations
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();

COMMENT ON TABLE wms_inventory.destinations
  IS 'Магазины-получатели для исходящих заказов, буферов отгрузки и рейсов.';
COMMENT ON COLUMN wms_inventory.destinations.code
  IS 'Внешний код магазина-получателя. Глобально уникален.';
COMMENT ON COLUMN wms_inventory.destinations.warehouse_id
  IS 'Склад, который обслуживает магазин-получатель.';

CREATE INDEX idx_destinations_warehouse_id
  ON wms_inventory.destinations(warehouse_id);

CREATE TABLE wms_inventory.order_lines (
  id bigserial PRIMARY KEY,
  order_id uuid NOT NULL REFERENCES wms_inventory.orders(order_id) ON DELETE CASCADE,
  sku_id uuid NOT NULL REFERENCES wms_inventory.skus(sku_id) ON DELETE RESTRICT,
  qty integer NOT NULL,
  CONSTRAINT order_lines_order_sku_unique UNIQUE (order_id, sku_id),
  CONSTRAINT order_lines_qty_positive CHECK (qty > 0)
);

COMMENT ON TABLE wms_inventory.order_lines
  IS 'Плановый состав заказа: какие SKU и в каком количестве нужно собрать.';
COMMENT ON COLUMN wms_inventory.order_lines.qty
  IS 'Требуемое количество единиц данного SKU в заказе.';

CREATE INDEX idx_order_lines_order_id
  ON wms_inventory.order_lines(order_id);

CREATE INDEX idx_order_lines_sku_id
  ON wms_inventory.order_lines(sku_id);

CREATE TABLE wms_inventory.outbound_dispatches (
  dispatch_id uuid PRIMARY KEY,
  dispatch_code text NOT NULL,
  warehouse_id bigint NOT NULL REFERENCES wms_inventory.warehouses(warehouse_id) ON DELETE RESTRICT,
  destination_id uuid NOT NULL REFERENCES wms_inventory.destinations(destination_id) ON DELETE RESTRICT,
  vehicle_number text NOT NULL,
  driver_name text NOT NULL,
  driver_phone text,
  status wms_inventory.outbound_dispatch_status NOT NULL DEFAULT 'SCHEDULED',
  scheduled_at timestamptz NOT NULL,
  arrived_at timestamptz,
  departed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT outbound_dispatches_dispatch_code_unique UNIQUE (dispatch_code)
);

CREATE TRIGGER trg_outbound_dispatches_set_updated_at
BEFORE UPDATE ON wms_inventory.outbound_dispatches
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();

COMMENT ON TABLE wms_inventory.outbound_dispatches
  IS 'Плановые исходящие рейсы, заранее созданные внешней логистикой до погрузки.';
COMMENT ON COLUMN wms_inventory.outbound_dispatches.dispatch_code
  IS 'Внешний код рейса / QR-код водителя для идентификации исходящего рейса.';
COMMENT ON COLUMN wms_inventory.outbound_dispatches.vehicle_number
  IS 'Номер транспортного средства, назначенного на исходящий рейс.';

CREATE INDEX idx_outbound_dispatches_destination_id
  ON wms_inventory.outbound_dispatches(destination_id);

CREATE INDEX idx_outbound_dispatches_status
  ON wms_inventory.outbound_dispatches(status);

CREATE INDEX idx_outbound_dispatches_warehouse_id
  ON wms_inventory.outbound_dispatches(warehouse_id);

CREATE INDEX idx_outbound_dispatches_scheduled_at
  ON wms_inventory.outbound_dispatches(scheduled_at);

-- 3. Упрощение order_status: пересоздаём ENUM с 4 значениями.
ALTER TYPE wms_inventory.order_status RENAME TO order_status_old;

CREATE TYPE wms_inventory.order_status AS ENUM (
  'NEW',
  'ALLOCATED',
  'ASSEMBLED',
  'SHIPPED'
);

ALTER TABLE wms_inventory.orders
  ALTER COLUMN status DROP DEFAULT;

ALTER TABLE wms_inventory.orders
  ALTER COLUMN status TYPE wms_inventory.order_status
  USING (
    CASE status::text
      WHEN 'ASSEMBLY_IN_PROGRESS' THEN 'ALLOCATED'
      WHEN 'READY_TO_SHIP' THEN 'ASSEMBLED'
      ELSE status::text
    END
  )::wms_inventory.order_status;

ALTER TABLE wms_inventory.orders
  ALTER COLUMN status SET DEFAULT 'NEW';

DROP TYPE wms_inventory.order_status_old;

-- 4. Изменение существующих таблиц.
ALTER TABLE wms_inventory.orders
  ADD COLUMN destination_id uuid NOT NULL
  REFERENCES wms_inventory.destinations(destination_id) ON DELETE RESTRICT;

COMMENT ON COLUMN wms_inventory.orders.destination_id
  IS 'Магазин-получатель, для которого заказ собирается и отгружается.';

CREATE INDEX idx_orders_destination_id
  ON wms_inventory.orders(destination_id);

ALTER TABLE wms_inventory.bins
  ADD COLUMN destination_id uuid
  REFERENCES wms_inventory.destinations(destination_id) ON DELETE RESTRICT;

ALTER TABLE wms_inventory.bins
  ADD CONSTRAINT bins_shipping_buffer_requires_destination
  CHECK (section IS DISTINCT FROM 'SHIPPING_BUFFER' OR destination_id IS NOT NULL);

COMMENT ON COLUMN wms_inventory.bins.destination_id
  IS 'Магазин-получатель, закрепленный за ячейкой SHIPPING_BUFFER; для остальных ячеек NULL.';

CREATE INDEX idx_bins_destination_id
  ON wms_inventory.bins(destination_id)
  WHERE section = 'SHIPPING_BUFFER';

ALTER TABLE wms_ops.assembly_tasks
  ADD COLUMN destination_id uuid NOT NULL
  REFERENCES wms_inventory.destinations(destination_id) ON DELETE RESTRICT;

COMMENT ON COLUMN wms_ops.assembly_tasks.destination_id
  IS 'Денормализованный магазин-получатель, скопированный из заказа.';

CREATE INDEX idx_assembly_tasks_destination_id
  ON wms_ops.assembly_tasks(destination_id);

ALTER TABLE wms_ops.shippings
  ADD COLUMN dispatch_id uuid NOT NULL
  REFERENCES wms_inventory.outbound_dispatches(dispatch_id) ON DELETE RESTRICT;

COMMENT ON COLUMN wms_ops.shippings.dispatch_id
  IS 'Исходящий рейс, использованный при выполнении операции отгрузки.';

CREATE INDEX idx_shippings_dispatch_id
  ON wms_ops.shippings(dispatch_id);

ALTER TABLE wms_ops.shippings
  DROP COLUMN vehicle_number;

INSERT INTO public.schema_migrations(version) VALUES (7);

COMMIT;
