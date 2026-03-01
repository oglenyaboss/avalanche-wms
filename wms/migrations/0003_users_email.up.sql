BEGIN;

-- Test migration #3: add a nullable email column to users.
ALTER TABLE public.users
  ADD COLUMN IF NOT EXISTS email text;

-- Optional uniqueness to test constraints/index behavior.
CREATE UNIQUE INDEX IF NOT EXISTS ux_users_email ON public.users(email) WHERE email IS NOT NULL;

COMMENT ON COLUMN public.users.email IS 'Optional email for test migration.';

INSERT INTO public.schema_migrations(version) VALUES (3);

COMMIT;
