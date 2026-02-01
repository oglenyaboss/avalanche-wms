BEGIN;

-- Test migration #2: create a simple table for validating multi-step migrations.
CREATE TABLE IF NOT EXISTS public.migration_test (
  id bigserial PRIMARY KEY,
  note text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE public.migration_test IS 'Test table used to validate migrate/rollback scripts.';

INSERT INTO public.schema_migrations(version) VALUES (2);

COMMIT;

