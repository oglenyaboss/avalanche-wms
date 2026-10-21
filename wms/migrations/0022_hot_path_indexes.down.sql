BEGIN;

DELETE FROM public.schema_migrations WHERE version = 22;

DROP INDEX IF EXISTS wms_ops.idx_assembly_tasks_basket;
DROP INDEX IF EXISTS wms_inventory.idx_products_bin_status;
CREATE INDEX IF NOT EXISTS idx_products_qr_code ON wms_inventory.products(qr_code);

COMMIT;
