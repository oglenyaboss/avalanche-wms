BEGIN;

DELETE FROM public.schema_migrations WHERE version = 23;

ALTER INDEX public.idx_warehouse_actions_items RESET (fastupdate);

COMMIT;
