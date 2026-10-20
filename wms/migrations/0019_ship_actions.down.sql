BEGIN;

DROP INDEX IF EXISTS wms_ops.idx_shippings_action_id;
ALTER TABLE wms_ops.shippings DROP COLUMN IF EXISTS action_id;

DELETE FROM public.schema_migrations WHERE version = 19;

COMMIT;
