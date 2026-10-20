BEGIN;

-- #18: роль аудитора для read-only доступа к эндпоинту аудита.
ALTER TYPE public.user_role ADD VALUE IF NOT EXISTS 'AUDITOR';

INSERT INTO public.schema_migrations (version) VALUES (20) ON CONFLICT (version) DO NOTHING;

COMMIT;
