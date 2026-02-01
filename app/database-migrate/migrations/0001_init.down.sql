BEGIN;

-- Destructive rollback: drops all WMS schemas/tables/types created in 0001_init.up.sql.

-- Drop domain schemas (cascades dependent tables/types)
DROP SCHEMA IF EXISTS wms_ops CASCADE;
DROP SCHEMA IF EXISTS wms_inventory CASCADE;

-- -- If a newer migration introduced an event registry, clean it up too (safe no-op if absent).
-- DROP FUNCTION IF EXISTS public.ensure_event_registry();
-- DROP TABLE IF EXISTS public.events;

-- Drop public tables (order matters due to FK dependencies)
DROP TABLE IF EXISTS public.evm_addresses;
DROP TABLE IF EXISTS public.onchain_events;
DROP TABLE IF EXISTS public.outbox_events;
DROP TABLE IF EXISTS public.users;

-- Helper function
DROP FUNCTION IF EXISTS public.set_updated_at();

-- Public ENUMs
DROP TYPE IF EXISTS public.onchain_event_status;
DROP TYPE IF EXISTS public.user_role;

-- Migration bookkeeping (last)
DROP TABLE IF EXISTS public.schema_migrations;

COMMIT;
