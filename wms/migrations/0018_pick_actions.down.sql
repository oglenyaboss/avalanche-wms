BEGIN;

DROP INDEX IF EXISTS wms_ops.idx_assembly_tasks_action_id;
ALTER TABLE wms_ops.assembly_tasks
    DROP COLUMN IF EXISTS action_id,
    DROP COLUMN IF EXISTS unloaded_at;

DELETE FROM public.schema_migrations WHERE version = 18;

COMMIT;
