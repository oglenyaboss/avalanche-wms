BEGIN;

-- Test rollback #3: remove email column + its index.
DROP INDEX IF EXISTS public.ux_users_email;

ALTER TABLE public.users
  DROP COLUMN IF EXISTS email;

COMMIT;
