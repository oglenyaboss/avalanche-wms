BEGIN;

-- Issue #48 (Shipping-2): add an intermediate order status so a partially shipped
-- order is visibly distinct from both ASSEMBLED (nothing shipped yet) and SHIPPED
-- (fully shipped). Placed BEFORE 'SHIPPED' to keep the enum ordinal order aligned
-- with the lifecycle NEW -> ALLOCATED -> ASSEMBLED -> PARTIALLY_SHIPPED -> SHIPPED.
--
-- ALTER TYPE ... ADD VALUE is safe inside a transaction on PostgreSQL 12+ (target is
-- PG 17): the only restriction is that the new value may not be *referenced* in the
-- same transaction, which this migration never does (it is only used at runtime by
-- the WMS). A full type recreate (as in 0007) is unnecessary here because we only
-- add a value — no value is removed or reordered, and the sole dependent object is
-- wms_inventory.orders.status (no view depends on this enum; verified against 0008).
ALTER TYPE wms_inventory.order_status
  ADD VALUE IF NOT EXISTS 'PARTIALLY_SHIPPED' BEFORE 'SHIPPED';

INSERT INTO public.schema_migrations (version) VALUES (10);

COMMIT;
