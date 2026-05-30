-- Issue #45: the WMS->chain status gate (ledger.CheckChainStatus) resolves a product's
-- on-chain event by joining public.onchain_events to public.outbox_events on event_id and
-- filtering outbox_events by (aggregate_id, aggregate_type). The existing single-column
-- idx_outbox_events_aggregate_id leaves aggregate_type as a post-filter; a composite index
-- makes the gate a single indexed lookup, which matters because it runs on every
-- putaway placement and pick.
--
-- CREATE INDEX CONCURRENTLY cannot run inside a transaction; like 0009, this file is
-- intentionally NOT wrapped in BEGIN/COMMIT (migrate.sh runs it raw via psql).
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_outbox_events_aggregate_id_type
ON public.outbox_events (aggregate_id, aggregate_type);

-- ON CONFLICT keeps the version write idempotent: because this file is not wrapped in a
-- transaction (CONCURRENTLY forbids it), a crash between the index build and this INSERT
-- would otherwise leave a re-run hitting the schema_migrations PK (version) on the second
-- attempt. The index itself is already idempotent via IF NOT EXISTS.
INSERT INTO public.schema_migrations (version) VALUES (11)
ON CONFLICT (version) DO NOTHING;
