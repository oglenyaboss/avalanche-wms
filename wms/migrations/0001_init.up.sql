BEGIN;

-- PostgreSQL 17 baseline schema for WMS_Blockchain.
-- This migration creates all schemas/tables/types "from scratch".

-- ---------------------------------------------------------------------------
-- Migration bookkeeping
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS public.schema_migrations (
  version bigint PRIMARY KEY,
  applied_at timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Schemas
-- ---------------------------------------------------------------------------

CREATE SCHEMA IF NOT EXISTS wms_inventory;
CREATE SCHEMA IF NOT EXISTS wms_ops;

COMMENT ON SCHEMA wms_inventory IS 'WMS core inventory domain (skus, warehouses, bins, inbound_shipments, cargoplaces, boxes, expected_cargoplace_skus, orders, products).';
COMMENT ON SCHEMA wms_ops IS 'WMS operations history domain (append-only tables: receiving_gate, receiving_table, putaways, assembly_tasks, shippings).';

-- ---------------------------------------------------------------------------
-- ENUMs / status ограничения (ENUM)
-- ---------------------------------------------------------------------------
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace WHERE n.nspname = 'public' AND t.typname = 'user_role') THEN
    CREATE TYPE public.user_role AS ENUM ('ADMIN', 'OPERATOR', 'CUSTOMER');
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace WHERE n.nspname = 'public' AND t.typname = 'onchain_event_status') THEN
    CREATE TYPE public.onchain_event_status AS ENUM ('PENDING', 'SENT', 'COMMITTED', 'FAILED');
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace WHERE n.nspname = 'wms_inventory' AND t.typname = 'order_status') THEN
    CREATE TYPE wms_inventory.order_status AS ENUM (
      'NEW',
      'ALLOCATED',
      'ASSEMBLY_IN_PROGRESS',
      'ASSEMBLED',
      'READY_TO_SHIP',
      'SHIPPED'
    );
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace WHERE n.nspname = 'wms_inventory' AND t.typname = 'product_status') THEN
    CREATE TYPE wms_inventory.product_status AS ENUM (
      'RECEIVED',
      'STORED',
      'ALLOCATED',
      'ASSEMBLED',
      'READY_TO_SHIP',
      'SHIPPED'
    );
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace WHERE n.nspname = 'wms_ops' AND t.typname = 'task_status') THEN
    CREATE TYPE wms_ops.task_status AS ENUM ('PENDING', 'IN_PROGRESS', 'DONE', 'CANCELLED');
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace WHERE n.nspname = 'wms_ops' AND t.typname = 'operation_onchain_status') THEN
    CREATE TYPE wms_ops.operation_onchain_status AS ENUM ('PENDING_ONCHAIN', 'ONCHAIN_COMMITTED');
  END IF;
END$$;

-- ---------------------------------------------------------------------------
-- Updated-at helper
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION public.set_updated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$;

COMMENT ON FUNCTION public.set_updated_at() IS 'Trigger helper: set updated_at = now() on UPDATE.';

-- ---------------------------------------------------------------------------
-- Public tables: users, evm_addresses, outbox_events, onchain_events
-- ---------------------------------------------------------------------------

CREATE TABLE public.users (
  user_id uuid PRIMARY KEY,
  username text NOT NULL,
  password_hash text NOT NULL,
  role public.user_role NOT NULL,
  is_active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT users_username_unique UNIQUE (username)
);
CREATE TRIGGER trg_users_set_updated_at
BEFORE UPDATE ON public.users
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();

COMMENT ON TABLE public.users IS 'System users (admins, operators, customers).';
COMMENT ON COLUMN public.users.user_id IS 'Primary key (UUID).';
COMMENT ON COLUMN public.users.username IS 'Unique login/username.';
COMMENT ON COLUMN public.users.password_hash IS 'Password hash (never store plaintext).';
COMMENT ON COLUMN public.users.role IS 'Role: ADMIN/OPERATOR/CUSTOMER.';
COMMENT ON COLUMN public.users.is_active IS 'Soft enable/disable flag.';

CREATE TABLE public.evm_addresses (
  id bigserial PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES public.users(user_id) ON DELETE CASCADE,
  evm_address text NOT NULL,
  onchain_role text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT evm_addresses_user_unique UNIQUE (user_id),
  CONSTRAINT evm_addresses_address_unique UNIQUE (evm_address)
);

CREATE TRIGGER trg_evm_addresses_set_updated_at
BEFORE UPDATE ON public.evm_addresses
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();

COMMENT ON TABLE public.evm_addresses IS 'Mapping users to EVM wallet addresses (cached roles for UI/logic).';
COMMENT ON COLUMN public.evm_addresses.user_id IS 'FK to users.';
COMMENT ON COLUMN public.evm_addresses.evm_address IS 'EVM address (unique).';
COMMENT ON COLUMN public.evm_addresses.onchain_role IS 'Role on-chain (cached); actual permissions are enforced by the blockchain.';

CREATE TABLE public.outbox_events (
  id bigserial PRIMARY KEY,
  event_id uuid NOT NULL,
  aggregate_id uuid NOT NULL,
  aggregate_type text NOT NULL,
  event_type text NOT NULL,
  payload_hash text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT outbox_events_event_id_unique UNIQUE (event_id)
);

COMMENT ON TABLE public.outbox_events IS 'Transactional outbox (PostgreSQL -> Kafka) for exactly-once delivery.';
COMMENT ON COLUMN public.outbox_events.event_id IS 'Globally unique event identifier (exactly-once).';
COMMENT ON COLUMN public.outbox_events.aggregate_id IS 'Business aggregate ID (linked entity ID).';
COMMENT ON COLUMN public.outbox_events.aggregate_type IS 'Aggregate type (RECEIVING/PUTAWAY/etc).';
COMMENT ON COLUMN public.outbox_events.event_type IS 'Kafka route/topic key (e.g., wms.receiving.v1).';
COMMENT ON COLUMN public.outbox_events.payload_hash IS 'Hash of payload for integrity/dedup checks.';

CREATE TABLE public.onchain_events (
  id bigserial PRIMARY KEY,
  event_id uuid NOT NULL,
  aggregate_type text NOT NULL,
  tx_hash text,
  status public.onchain_event_status NOT NULL DEFAULT 'PENDING',
  error_message text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT onchain_events_event_id_unique UNIQUE (event_id)
);

CREATE TRIGGER trg_onchain_events_set_updated_at
BEFORE UPDATE ON public.onchain_events
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();

COMMENT ON TABLE public.onchain_events IS 'On-chain transaction status tracker (fed by Ledger Adapter).';
COMMENT ON COLUMN public.onchain_events.event_id IS 'Matches outbox/ops event_id for end-to-end tracking.';
COMMENT ON COLUMN public.onchain_events.tx_hash IS 'Blockchain transaction hash, set when sending.';
COMMENT ON COLUMN public.onchain_events.status IS 'PENDING/SENT/COMMITTED/FAILED.';
COMMENT ON COLUMN public.onchain_events.error_message IS 'Optional error details on FAILED.';

-- ---------------------------------------------------------------------------
-- wms_inventory domain
-- ---------------------------------------------------------------------------

CREATE TABLE wms_inventory.skus (
  sku_id uuid PRIMARY KEY,
  name text NOT NULL,
  description text,
  volume numeric,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT skus_volume_nonnegative CHECK (volume IS NULL OR volume >= 0)
);

CREATE TRIGGER trg_skus_set_updated_at
BEFORE UPDATE ON wms_inventory.skus
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();

COMMENT ON TABLE wms_inventory.skus IS 'SKU master data.';
COMMENT ON COLUMN wms_inventory.skus.sku_id IS 'Primary key (UUID).';
COMMENT ON COLUMN wms_inventory.skus.volume IS 'Volume in liters (>= 0).';

CREATE TABLE wms_inventory.warehouses (
  warehouse_id bigserial PRIMARY KEY,
  name text NOT NULL,
  address text,
  contact text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT warehouses_name_unique UNIQUE (name)
);

CREATE TRIGGER trg_warehouses_set_updated_at
BEFORE UPDATE ON wms_inventory.warehouses
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();

COMMENT ON TABLE wms_inventory.warehouses IS 'Warehouses.';
COMMENT ON COLUMN wms_inventory.warehouses.warehouse_id IS 'Primary key (bigserial).';

CREATE TABLE wms_inventory.bins (
  bin_id uuid PRIMARY KEY,
  warehouse_id bigint NOT NULL REFERENCES wms_inventory.warehouses(warehouse_id) ON DELETE RESTRICT,
  code text NOT NULL,
  section text,
  volume numeric,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT bins_warehouse_code_unique UNIQUE (warehouse_id, code),
  CONSTRAINT bins_volume_nonnegative CHECK (volume IS NULL OR volume >= 0)
);

CREATE TRIGGER trg_bins_set_updated_at
BEFORE UPDATE ON wms_inventory.bins
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();

COMMENT ON TABLE wms_inventory.bins IS 'Storage bins/locations in warehouses.';
COMMENT ON COLUMN wms_inventory.bins.warehouse_id IS 'FK to warehouses.';
COMMENT ON COLUMN wms_inventory.bins.code IS 'Bin code unique per warehouse.';
COMMENT ON COLUMN wms_inventory.bins.section IS 'Warehouse zone/section.';

CREATE TABLE wms_inventory.inbound_shipments (
  shipment_id uuid PRIMARY KEY,
  warehouse_id bigint NOT NULL REFERENCES wms_inventory.warehouses(warehouse_id) ON DELETE RESTRICT,
  ttn_code text NOT NULL,
  status text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT inbound_shipments_ttn_code_unique UNIQUE (ttn_code)
);

CREATE TRIGGER trg_inbound_shipments_set_updated_at
BEFORE UPDATE ON wms_inventory.inbound_shipments
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();

CREATE TABLE wms_inventory.cargoplaces (
  cargoplace_id uuid PRIMARY KEY,
  shipment_id uuid NOT NULL REFERENCES wms_inventory.inbound_shipments(shipment_id) ON DELETE RESTRICT,
  cargoplace_code text NOT NULL,
  status text NOT NULL,
  received_at_gate_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT cargoplaces_shipment_code_unique UNIQUE (shipment_id, cargoplace_code)
);

CREATE TRIGGER trg_cargoplaces_set_updated_at
BEFORE UPDATE ON wms_inventory.cargoplaces
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();

CREATE TABLE wms_inventory.boxes (
  box_id uuid PRIMARY KEY,
  cargoplace_id uuid NOT NULL REFERENCES wms_inventory.cargoplaces(cargoplace_id) ON DELETE RESTRICT,
  box_barcode text NOT NULL,
  status text NOT NULL DEFAULT 'OPEN',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT boxes_cargoplace_barcode_unique UNIQUE (cargoplace_id, box_barcode)
);

CREATE TRIGGER trg_boxes_set_updated_at
BEFORE UPDATE ON wms_inventory.boxes
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();

CREATE TABLE wms_inventory.expected_cargoplace_skus (
  id bigserial PRIMARY KEY,
  cargoplace_id uuid NOT NULL REFERENCES wms_inventory.cargoplaces(cargoplace_id) ON DELETE CASCADE,
  sku_id uuid NOT NULL REFERENCES wms_inventory.skus(sku_id) ON DELETE RESTRICT,
  expected_qty integer NOT NULL,
  CONSTRAINT expected_cargoplace_skus_unique UNIQUE (cargoplace_id, sku_id),
  CONSTRAINT expected_cargoplace_skus_qty_positive CHECK (expected_qty > 0)
);

CREATE TABLE wms_inventory.orders (
  order_id uuid PRIMARY KEY,
  external_order_no text NOT NULL,
  customer_id uuid NOT NULL REFERENCES public.users(user_id) ON DELETE RESTRICT,
  warehouse_id bigint NOT NULL REFERENCES wms_inventory.warehouses(warehouse_id) ON DELETE RESTRICT,
  status wms_inventory.order_status NOT NULL DEFAULT 'NEW',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT orders_external_no_unique UNIQUE (external_order_no)
);

CREATE TRIGGER trg_orders_set_updated_at
BEFORE UPDATE ON wms_inventory.orders
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();

COMMENT ON TABLE wms_inventory.orders IS 'Customer orders.';
COMMENT ON COLUMN wms_inventory.orders.external_order_no IS 'External order identifier (unique).';
COMMENT ON COLUMN wms_inventory.orders.customer_id IS 'FK to users (CUSTOMER).';
COMMENT ON COLUMN wms_inventory.orders.status IS 'Order status lifecycle.';

CREATE TABLE wms_inventory.products (
  product_id uuid PRIMARY KEY,
  sku_id uuid NOT NULL REFERENCES wms_inventory.skus(sku_id) ON DELETE RESTRICT,
  shipment_id uuid NOT NULL REFERENCES wms_inventory.inbound_shipments(shipment_id) ON DELETE RESTRICT,
  cargoplace_id uuid NOT NULL REFERENCES wms_inventory.cargoplaces(cargoplace_id) ON DELETE RESTRICT,
  box_id uuid REFERENCES wms_inventory.boxes(box_id) ON DELETE SET NULL,
  qr_code text NOT NULL,
  bin_id uuid REFERENCES wms_inventory.bins(bin_id) ON DELETE SET NULL,
  order_id uuid REFERENCES wms_inventory.orders(order_id) ON DELETE SET NULL,
  status wms_inventory.product_status NOT NULL DEFAULT 'RECEIVED',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT products_qr_code_unique UNIQUE (qr_code)
);

CREATE TRIGGER trg_products_set_updated_at
BEFORE UPDATE ON wms_inventory.products
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();

COMMENT ON TABLE wms_inventory.products IS 'Physical item instances stored/allocated/shipped.';
COMMENT ON COLUMN wms_inventory.products.shipment_id IS 'Inbound shipment where the product was received.';
COMMENT ON COLUMN wms_inventory.products.cargoplace_id IS 'Inbound cargoplace where the product was received.';
COMMENT ON COLUMN wms_inventory.products.box_id IS 'Inbound box where the product was scanned (nullable).';
COMMENT ON COLUMN wms_inventory.products.qr_code IS 'Unique item QR code.';
COMMENT ON COLUMN wms_inventory.products.bin_id IS 'Current bin/location (nullable until stored).';
COMMENT ON COLUMN wms_inventory.products.order_id IS 'Allocated order (nullable until allocated).';
COMMENT ON COLUMN wms_inventory.products.status IS 'Product status lifecycle.';

-- ---------------------------------------------------------------------------
-- wms_ops domain (append-only history tables)
-- ---------------------------------------------------------------------------

CREATE TABLE wms_ops.receiving_gate (
  id bigserial PRIMARY KEY,
  ttn_code text,
  cargoplace_code text,
  event_id uuid NOT NULL,
  shipment_id uuid REFERENCES wms_inventory.inbound_shipments(shipment_id) ON DELETE SET NULL,
  cargoplace_id uuid REFERENCES wms_inventory.cargoplaces(cargoplace_id) ON DELETE SET NULL,
  operator_id uuid NOT NULL REFERENCES public.users(user_id) ON DELETE RESTRICT,
  action text NOT NULL,
  occurred_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT receiving_gate_event_id_unique UNIQUE (event_id)
);

COMMENT ON TABLE wms_ops.receiving_gate IS 'Gate receiving history (append-only).';

CREATE TABLE wms_ops.receiving_table (
  id bigserial PRIMARY KEY,
  event_id uuid NOT NULL,
  cargoplace_id uuid NOT NULL REFERENCES wms_inventory.cargoplaces(cargoplace_id) ON DELETE RESTRICT,
  box_id uuid REFERENCES wms_inventory.boxes(box_id) ON DELETE SET NULL,
  operator_id uuid NOT NULL REFERENCES public.users(user_id) ON DELETE RESTRICT,
  action text NOT NULL,
  box_barcode text,
  sku_id uuid REFERENCES wms_inventory.skus(sku_id) ON DELETE RESTRICT,
  qr_code text,
  product_id uuid REFERENCES wms_inventory.products(product_id) ON DELETE RESTRICT,
  buffer_bin_id uuid REFERENCES wms_inventory.bins(bin_id) ON DELETE RESTRICT,
  occurred_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT receiving_table_event_id_unique UNIQUE (event_id)
);

COMMENT ON TABLE wms_ops.receiving_table IS 'Receiving-table operation history (append-only).';

CREATE TABLE wms_ops.putaways (
  id bigserial PRIMARY KEY,
  event_id uuid NOT NULL,
  product_id uuid NOT NULL REFERENCES wms_inventory.products(product_id) ON DELETE RESTRICT,
  bin_id uuid NOT NULL REFERENCES wms_inventory.bins(bin_id) ON DELETE RESTRICT,
  operator_id uuid NOT NULL REFERENCES public.users(user_id) ON DELETE RESTRICT,
  onchain_status wms_ops.operation_onchain_status NOT NULL DEFAULT 'PENDING_ONCHAIN',
  onchain_tx_hash text,
  occurred_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT putaways_event_id_unique UNIQUE (event_id)
);

COMMENT ON TABLE wms_ops.putaways IS 'Putaway operation history (append-only).';

CREATE TABLE wms_ops.assembly_tasks (
  id bigserial PRIMARY KEY,
  event_id uuid NOT NULL,
  order_id uuid NOT NULL REFERENCES wms_inventory.orders(order_id) ON DELETE RESTRICT,
  product_id uuid NOT NULL REFERENCES wms_inventory.products(product_id) ON DELETE RESTRICT,
  sku_id uuid NOT NULL REFERENCES wms_inventory.skus(sku_id) ON DELETE RESTRICT,
  from_bin_id uuid NOT NULL REFERENCES wms_inventory.bins(bin_id) ON DELETE RESTRICT,
  section text,
  status wms_ops.task_status NOT NULL DEFAULT 'PENDING',
  operator_id uuid REFERENCES public.users(user_id) ON DELETE SET NULL,
  onchain_status wms_ops.operation_onchain_status,
  onchain_tx_hash text,
  occurred_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT assembly_tasks_event_id_unique UNIQUE (event_id)
);

CREATE TRIGGER trg_assembly_tasks_set_updated_at
BEFORE UPDATE ON wms_ops.assembly_tasks
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();

COMMENT ON TABLE wms_ops.assembly_tasks IS 'Order picking/assembly tasks (append-only with status updates).';
COMMENT ON COLUMN wms_ops.assembly_tasks.status IS 'Task status: PENDING/IN_PROGRESS/DONE/CANCELLED.';

CREATE TABLE wms_ops.shippings (
  id bigserial PRIMARY KEY,
  event_id uuid NOT NULL,
  product_id uuid NOT NULL REFERENCES wms_inventory.products(product_id) ON DELETE RESTRICT,
  operator_id uuid NOT NULL REFERENCES public.users(user_id) ON DELETE RESTRICT,
  vehicle_number text,
  onchain_status wms_ops.operation_onchain_status NOT NULL DEFAULT 'PENDING_ONCHAIN',
  onchain_tx_hash text,
  shipped_at timestamptz NOT NULL,
  occurred_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT shippings_event_id_unique UNIQUE (event_id)
);

COMMENT ON TABLE wms_ops.shippings IS 'Shipping operation history (append-only).';

-- ---------------------------------------------------------------------------
-- Indexes for main joins/filters (FKs, event_id, status, occurred_at)
-- ---------------------------------------------------------------------------

-- Public
CREATE INDEX IF NOT EXISTS idx_evm_addresses_user_id ON public.evm_addresses(user_id);
CREATE INDEX IF NOT EXISTS idx_outbox_events_aggregate_id ON public.outbox_events(aggregate_id);
CREATE INDEX IF NOT EXISTS idx_outbox_events_event_type ON public.outbox_events(event_type);
CREATE INDEX IF NOT EXISTS idx_outbox_events_created_at ON public.outbox_events(created_at);
CREATE INDEX IF NOT EXISTS idx_onchain_events_status ON public.onchain_events(status);
CREATE INDEX IF NOT EXISTS idx_onchain_events_created_at ON public.onchain_events(created_at);

-- Inventory
CREATE INDEX IF NOT EXISTS idx_bins_warehouse_id ON wms_inventory.bins(warehouse_id);
CREATE INDEX IF NOT EXISTS idx_inbound_shipments_warehouse_id ON wms_inventory.inbound_shipments(warehouse_id);
CREATE INDEX IF NOT EXISTS idx_inbound_shipments_status ON wms_inventory.inbound_shipments(status);
CREATE INDEX IF NOT EXISTS idx_cargoplaces_shipment_id ON wms_inventory.cargoplaces(shipment_id);
CREATE INDEX IF NOT EXISTS idx_cargoplaces_status ON wms_inventory.cargoplaces(status);
CREATE INDEX IF NOT EXISTS idx_boxes_cargoplace_id ON wms_inventory.boxes(cargoplace_id);
CREATE INDEX IF NOT EXISTS idx_boxes_status ON wms_inventory.boxes(status);
CREATE INDEX IF NOT EXISTS idx_expected_cargoplace_skus_cargoplace_id ON wms_inventory.expected_cargoplace_skus(cargoplace_id);
CREATE INDEX IF NOT EXISTS idx_expected_cargoplace_skus_sku_id ON wms_inventory.expected_cargoplace_skus(sku_id);
CREATE INDEX IF NOT EXISTS idx_orders_customer_id ON wms_inventory.orders(customer_id);
CREATE INDEX IF NOT EXISTS idx_orders_warehouse_id ON wms_inventory.orders(warehouse_id);
CREATE INDEX IF NOT EXISTS idx_orders_status ON wms_inventory.orders(status);
CREATE INDEX IF NOT EXISTS idx_products_sku_id ON wms_inventory.products(sku_id);
CREATE INDEX IF NOT EXISTS idx_products_shipment_id ON wms_inventory.products(shipment_id);
CREATE INDEX IF NOT EXISTS idx_products_cargoplace_id ON wms_inventory.products(cargoplace_id);
CREATE INDEX IF NOT EXISTS idx_products_box_id ON wms_inventory.products(box_id);
CREATE INDEX IF NOT EXISTS idx_products_qr_code ON wms_inventory.products(qr_code);
CREATE INDEX IF NOT EXISTS idx_products_bin_id ON wms_inventory.products(bin_id);
CREATE INDEX IF NOT EXISTS idx_products_order_id ON wms_inventory.products(order_id);
CREATE INDEX IF NOT EXISTS idx_products_status ON wms_inventory.products(status);

-- Ops
CREATE INDEX IF NOT EXISTS idx_receiving_gate_shipment_id ON wms_ops.receiving_gate(shipment_id);
CREATE INDEX IF NOT EXISTS idx_receiving_gate_cargoplace_id ON wms_ops.receiving_gate(cargoplace_id);
CREATE INDEX IF NOT EXISTS idx_receiving_gate_operator_id ON wms_ops.receiving_gate(operator_id);
CREATE INDEX IF NOT EXISTS idx_receiving_gate_action ON wms_ops.receiving_gate(action);
CREATE INDEX IF NOT EXISTS idx_receiving_gate_occurred_at ON wms_ops.receiving_gate(occurred_at);

CREATE INDEX IF NOT EXISTS idx_receiving_table_cargoplace_id ON wms_ops.receiving_table(cargoplace_id);
CREATE INDEX IF NOT EXISTS idx_receiving_table_box_id ON wms_ops.receiving_table(box_id);
CREATE INDEX IF NOT EXISTS idx_receiving_table_operator_id ON wms_ops.receiving_table(operator_id);
CREATE INDEX IF NOT EXISTS idx_receiving_table_action ON wms_ops.receiving_table(action);
CREATE INDEX IF NOT EXISTS idx_receiving_table_sku_id ON wms_ops.receiving_table(sku_id);
CREATE INDEX IF NOT EXISTS idx_receiving_table_product_id ON wms_ops.receiving_table(product_id);
CREATE INDEX IF NOT EXISTS idx_receiving_table_buffer_bin_id ON wms_ops.receiving_table(buffer_bin_id);
CREATE INDEX IF NOT EXISTS idx_receiving_table_occurred_at ON wms_ops.receiving_table(occurred_at);

CREATE INDEX IF NOT EXISTS idx_putaways_product_id ON wms_ops.putaways(product_id);
CREATE INDEX IF NOT EXISTS idx_putaways_bin_id ON wms_ops.putaways(bin_id);
CREATE INDEX IF NOT EXISTS idx_putaways_operator_id ON wms_ops.putaways(operator_id);
CREATE INDEX IF NOT EXISTS idx_putaways_onchain_status ON wms_ops.putaways(onchain_status);
CREATE INDEX IF NOT EXISTS idx_putaways_occurred_at ON wms_ops.putaways(occurred_at);

CREATE INDEX IF NOT EXISTS idx_assembly_tasks_order_id ON wms_ops.assembly_tasks(order_id);
CREATE INDEX IF NOT EXISTS idx_assembly_tasks_product_id ON wms_ops.assembly_tasks(product_id);
CREATE INDEX IF NOT EXISTS idx_assembly_tasks_operator_id ON wms_ops.assembly_tasks(operator_id);
CREATE INDEX IF NOT EXISTS idx_assembly_tasks_status ON wms_ops.assembly_tasks(status);
CREATE INDEX IF NOT EXISTS idx_assembly_tasks_occurred_at ON wms_ops.assembly_tasks(occurred_at);

CREATE INDEX IF NOT EXISTS idx_shippings_product_id ON wms_ops.shippings(product_id);
CREATE INDEX IF NOT EXISTS idx_shippings_operator_id ON wms_ops.shippings(operator_id);
CREATE INDEX IF NOT EXISTS idx_shippings_onchain_status ON wms_ops.shippings(onchain_status);
CREATE INDEX IF NOT EXISTS idx_shippings_occurred_at ON wms_ops.shippings(occurred_at);

-- ---------------------------------------------------------------------------
-- Mark migration applied
-- ---------------------------------------------------------------------------

INSERT INTO public.schema_migrations(version) VALUES (1);

COMMIT;
