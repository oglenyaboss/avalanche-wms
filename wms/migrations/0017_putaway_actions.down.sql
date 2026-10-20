BEGIN;

DROP INDEX IF EXISTS wms_ops.idx_putaways_action_id;
ALTER TABLE wms_ops.putaways DROP COLUMN IF EXISTS action_id;

DELETE FROM public.schema_migrations WHERE version = 17;

COMMIT;
