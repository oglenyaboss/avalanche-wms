BEGIN;

DROP INDEX IF EXISTS public.idx_key_actions_target;
DROP TABLE IF EXISTS public.key_actions;

ALTER TABLE public.onchain_events DROP COLUMN IF EXISTS reject_reason;
ALTER TABLE public.outbox_events DROP COLUMN IF EXISTS payload;

DELETE FROM public.schema_migrations WHERE version = 15;

COMMIT;
