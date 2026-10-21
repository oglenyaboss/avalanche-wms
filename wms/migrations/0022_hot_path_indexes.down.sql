CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_products_qr_code ON wms_inventory.products(qr_code);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_products_bin_id ON wms_inventory.products(bin_id);
DROP INDEX CONCURRENTLY IF EXISTS wms_ops.idx_assembly_tasks_basket;
DROP INDEX CONCURRENTLY IF EXISTS wms_inventory.idx_products_bin_status;

DELETE FROM public.schema_migrations WHERE version = 22;
