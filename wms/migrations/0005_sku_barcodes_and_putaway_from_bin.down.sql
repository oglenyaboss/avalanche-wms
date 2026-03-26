BEGIN;

DROP INDEX IF EXISTS wms_ops.idx_putaways_from_bin_id;
ALTER TABLE wms_ops.putaways DROP COLUMN IF EXISTS from_bin_id;
DROP INDEX IF EXISTS wms_inventory.idx_sku_barcodes_sku_id;
DROP TABLE IF EXISTS wms_inventory.sku_barcodes;
DELETE FROM public.schema_migrations WHERE version = 5;

COMMIT;
