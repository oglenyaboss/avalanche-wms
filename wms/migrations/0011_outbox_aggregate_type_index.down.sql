DROP INDEX IF EXISTS public.idx_outbox_events_aggregate_id_type;

DELETE FROM public.schema_migrations WHERE version = 11;
