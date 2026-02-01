BEGIN;

-- Test migration #4: add an index to validate rollback of non-table objects.
CREATE INDEX IF NOT EXISTS idx_users_created_at ON public.users(created_at);

INSERT INTO public.schema_migrations(version) VALUES (4);

COMMIT;

