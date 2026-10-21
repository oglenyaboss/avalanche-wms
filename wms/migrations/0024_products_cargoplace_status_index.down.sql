BEGIN;

DELETE FROM public.schema_migrations WHERE version = 24;

CREATE INDEX IF NOT EXISTS idx_products_cargoplace_id ON wms_inventory.products(cargoplace_id);
DROP INDEX IF EXISTS wms_inventory.idx_products_cargoplace_status;

COMMIT;
