-- postgres-tune.sql — STRESS-ONLY OLTP tuning for the throughput harness.
--
-- ⚠️ NOT for production: `synchronous_commit=off` trades crash durability (last ~0.6s of
--    commits can be lost on a hard crash) for throughput. Apply ONLY to the disposable
--    `stresstest` stack, never to a real deployment.
--
-- WHY THIS FILE EXISTS: the documented 1924/с e2e headline DEPENDS on this tuning. It was
-- historically applied by hand via ALTER SYSTEM and lived ONLY inside the postgres volume
-- (postgresql.auto.conf) — it was NEVER in version control. `docker compose down -v` wipes
-- the volume → a fresh stresstest postgres starts on DEFAULTS (`synchronous_commit=on`,
-- shared_buffers=128MB) → the adapter's many small InsertPending/MarkCommitted commits go
-- fsync-bound → e2e collapses to ~455/с (chain starved, 1 batch/block) instead of ~1924/с.
-- The front's bulk inserts are unaffected (group-commit amortizes fsync), which is why the
-- symptom looks paradoxical (pool-30 default run is SLOWER than the documented pool-10).
--
-- APPLY: this file is run via `psql -f` (each ALTER SYSTEM autocommits — they CANNOT be wrapped
-- in a single multi-statement transaction). shared_buffers/wal_buffers need a postgres restart;
-- use tests/stress/setup/apply-postgres-tune.sh which applies + restarts + waits healthy.

ALTER SYSTEM SET synchronous_commit = 'off';        -- DOMINANT knob (~3-4x on this workload)
ALTER SYSTEM SET shared_buffers = '2GB';
ALTER SYSTEM SET effective_cache_size = '8GB';
ALTER SYSTEM SET max_wal_size = '8GB';
ALTER SYSTEM SET min_wal_size = '1GB';
ALTER SYSTEM SET wal_buffers = '64MB';
ALTER SYSTEM SET work_mem = '32MB';
ALTER SYSTEM SET checkpoint_timeout = '20min';
ALTER SYSTEM SET checkpoint_completion_target = '0.9';
-- NB: do NOT set wal_level here — the base compose sets `-c wal_level=logical` (Debezium CDC);
-- a command-line flag overrides postgresql.auto.conf, so leaving it out keeps CDC working.
