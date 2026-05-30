BEGIN;

-- PostgreSQL has no ALTER TYPE ... DROP VALUE, so rolling back PARTIALLY_SHIPPED
-- requires recreating the enum (same rename/recreate/remap dance as 0007).
--
-- Any order currently in PARTIALLY_SHIPPED is collapsed to ASSEMBLED rather than
-- SHIPPED: ASSEMBLED is recoverable (the rolled-back code can still complete the
-- order once its remaining products ship), whereas SHIPPED would be a terminal,
-- misleading state that hides not-yet-shipped products. The collapse is lossy
-- (the "some products already shipped" signal is dropped) — acceptable for a
-- rollback path, flagged here and in the MR.
ALTER TABLE wms_inventory.orders ALTER COLUMN status DROP DEFAULT;

ALTER TYPE wms_inventory.order_status RENAME TO order_status_old;

CREATE TYPE wms_inventory.order_status AS ENUM (
  'NEW',
  'ALLOCATED',
  'ASSEMBLED',
  'SHIPPED'
);

ALTER TABLE wms_inventory.orders
  ALTER COLUMN status TYPE wms_inventory.order_status
  USING (
    CASE status::text
      WHEN 'PARTIALLY_SHIPPED' THEN 'ASSEMBLED'
      ELSE status::text
    END
  )::wms_inventory.order_status;

ALTER TABLE wms_inventory.orders ALTER COLUMN status SET DEFAULT 'NEW';

DROP TYPE wms_inventory.order_status_old;

DELETE FROM public.schema_migrations WHERE version = 10;

COMMIT;
