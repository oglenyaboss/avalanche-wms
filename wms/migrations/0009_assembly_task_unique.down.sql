DROP INDEX IF EXISTS wms_ops.idx_assembly_tasks_product_pending;

DELETE FROM public.schema_migrations WHERE version = 9;
