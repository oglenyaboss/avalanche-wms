BEGIN;

ALTER TABLE wms_ops.receiving_table DROP COLUMN IF EXISTS operation_id;
DROP INDEX IF EXISTS public.idx_warehouse_actions_items;
DROP TABLE IF EXISTS public.warehouse_actions;

DELETE FROM public.schema_migrations WHERE version = 16;

COMMIT;
