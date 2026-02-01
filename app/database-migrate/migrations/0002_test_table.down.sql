BEGIN;

-- Test rollback #2: drop the test table.
DROP TABLE IF EXISTS public.migration_test;

COMMIT;

