CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_products_cargoplace_id ON wms_inventory.products(cargoplace_id);
DROP INDEX CONCURRENTLY IF EXISTS wms_inventory.idx_products_cargoplace_status;

DELETE FROM public.schema_migrations WHERE version = 24;
