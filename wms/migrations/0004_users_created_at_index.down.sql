BEGIN;

-- Test rollback #4: drop the index.
DROP INDEX IF EXISTS public.idx_users_created_at;

COMMIT;
