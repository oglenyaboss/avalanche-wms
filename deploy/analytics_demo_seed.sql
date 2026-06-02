-- =============================================================================
-- DEMO-ONLY synthetic data for the warehouse analytics dashboard (/analytics).
-- =============================================================================
-- Purpose: populate public.outbox_events + public.onchain_events with a realistic
--   14-day, multi-stage event stream so the analytics page (especially the
--   mandatory onchain confirmation hero and the throughput chart) renders richly
--   on a fresh local stack, where the CDC→Kafka→ledger-adapter→Avalanche pipeline
--   may not have produced committed rows.
--
-- Safety:
--   * Touches ONLY the two append-only event-tracking tables. No inventory,
--     orders, dispatches, or operational rows are altered — zero impact on the
--     real warehouse flow.
--   * Idempotent: event_ids are deterministic (md5 of a 'demo-...' key) and every
--     INSERT is guarded by ON CONFLICT DO NOTHING / NOT EXISTS, so re-running is
--     a no-op.
--   * NOT for production. To remove, see the companion DELETE at the bottom
--     (commented out).
--
-- Run: docker exec -i postgres_db psql -U root -d wms_blockchain_db < deploy/analytics_demo_seed.sql
-- =============================================================================

BEGIN;

-- 1) Synthetic outbox events: 14 trailing days × 4 FSM stages, organic volume.
WITH days AS (SELECT generate_series(0, 13) AS day_offset),
     stages AS (SELECT unnest(ARRAY['receiving', 'putaway', 'picking', 'shipping']) AS aggregate_type),
     gen AS (
         SELECT
             d.day_offset,
             s.aggregate_type,
             g AS n,
             md5('demo-' || d.day_offset || '-' || s.aggregate_type || '-' || g)::uuid AS event_id,
             (current_date - d.day_offset)::timestamptz + (g * interval '11 minutes') AS created_at
         FROM days d
         CROSS JOIN stages s
         CROSS JOIN generate_series(
             1,
             -- per-stage daily volume, varied by day so the chart looks organic
             4 + (d.day_offset % 5) * 2 + CASE s.aggregate_type
                 WHEN 'receiving' THEN 7
                 WHEN 'putaway' THEN 5
                 WHEN 'picking' THEN 3
                 ELSE 2 END
         ) AS g
     )
INSERT INTO public.outbox_events (event_id, aggregate_id, aggregate_type, event_type, payload_hash, created_at)
SELECT
    event_id,
    md5('agg-' || event_id::text)::uuid,
    aggregate_type,
    aggregate_type || '.demo',
    md5(event_id::text),
    created_at
FROM gen
ON CONFLICT (event_id) DO NOTHING;

-- 2) Onchain status for every outbox event missing one (covers the synthetic
--    rows above AND any pre-existing real events). Deterministic status mix per
--    event_id: ~80% COMMITTED, ~8% SENT, ~7% PENDING, ~5% FAILED.
INSERT INTO public.onchain_events (event_id, aggregate_type, tx_hash, status, error_message, created_at, updated_at)
SELECT
    ob.event_id,
    ob.aggregate_type,
    CASE WHEN st.status IN ('COMMITTED', 'SENT')
         THEN '0x' || md5('tx-a-' || ob.event_id::text) || md5('tx-b-' || ob.event_id::text)
         ELSE NULL END,
    st.status::onchain_event_status,
    CASE WHEN st.status = 'FAILED' THEN
        CASE WHEN (('x' || substr(md5(ob.event_id::text), 9, 8))::bit(32)::int) % 2 = 0
             THEN 'execution reverted: Invalid status transition'
             ELSE 'execution reverted: Duplicate eventId' END
    ELSE NULL END,
    ob.created_at,
    ob.created_at + interval '4 seconds'
FROM public.outbox_events ob
CROSS JOIN LATERAL (
    SELECT CASE
        WHEN (('x' || substr(md5(ob.event_id::text), 1, 8))::bit(32)::int & 2147483647) % 100 < 80 THEN 'COMMITTED'
        WHEN (('x' || substr(md5(ob.event_id::text), 1, 8))::bit(32)::int & 2147483647) % 100 < 88 THEN 'SENT'
        WHEN (('x' || substr(md5(ob.event_id::text), 1, 8))::bit(32)::int & 2147483647) % 100 < 95 THEN 'PENDING'
        ELSE 'FAILED'
    END AS status
) st
WHERE NOT EXISTS (
    SELECT 1 FROM public.onchain_events oe WHERE oe.event_id = ob.event_id
);

COMMIT;

-- To roll back the demo data (removes synthetic + any onchain rows seeded above):
--   DELETE FROM public.onchain_events
--    WHERE event_id IN (SELECT event_id FROM public.outbox_events WHERE event_type LIKE '%.demo');
--   DELETE FROM public.outbox_events WHERE event_type LIKE '%.demo';
