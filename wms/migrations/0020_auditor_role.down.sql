BEGIN;

DELETE FROM public.schema_migrations WHERE version = 20;

-- ponytail: PostgreSQL не умеет удалять значение enum — 'AUDITOR' остаётся в типе public.user_role.

COMMIT;
