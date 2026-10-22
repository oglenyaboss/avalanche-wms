BEGIN;

DROP TABLE IF EXISTS public.passkey_credentials;

DROP INDEX IF EXISTS public.idx_users_org_id;
ALTER TABLE public.users DROP COLUMN IF EXISTS org_id;

DROP TABLE IF EXISTS public.organizations;

DELETE FROM public.schema_migrations WHERE version = 14;

COMMIT;
