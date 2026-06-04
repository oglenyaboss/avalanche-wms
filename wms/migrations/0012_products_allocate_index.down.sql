DROP INDEX IF EXISTS wms_inventory.idx_products_allocate;

DELETE FROM public.schema_migrations WHERE version = 12;
