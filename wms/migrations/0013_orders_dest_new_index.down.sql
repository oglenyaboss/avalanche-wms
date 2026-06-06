DROP INDEX IF EXISTS wms_inventory.idx_orders_dest_new;
DELETE FROM public.schema_migrations WHERE version = 13;
