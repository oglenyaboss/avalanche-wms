BEGIN;

DELETE FROM public.schema_migrations WHERE version = 21;

-- Вьюхи восстановлены дословно из 0008_chain_views.up.sql (шаг 2).
CREATE VIEW wms_ops.v_putaways_with_chain AS
SELECT
  p.id, p.event_id, p.product_id, p.from_bin_id, p.bin_id,
  p.operator_id, p.occurred_at, p.created_at,
  COALESCE(oe.status::text, 'PENDING') AS chain_status,
  oe.tx_hash                           AS chain_tx_hash,
  oe.error_message                     AS chain_error_message,
  oe.updated_at                        AS chain_updated_at
FROM wms_ops.putaways p
LEFT JOIN public.onchain_events oe USING (event_id);

CREATE VIEW wms_ops.v_shippings_with_chain AS
SELECT
  s.id, s.event_id, s.product_id, s.operator_id, s.dispatch_id,
  s.shipped_at, s.occurred_at, s.created_at,
  COALESCE(oe.status::text, 'PENDING') AS chain_status,
  oe.tx_hash                           AS chain_tx_hash,
  oe.error_message                     AS chain_error_message,
  oe.updated_at                        AS chain_updated_at
FROM wms_ops.shippings s
LEFT JOIN public.onchain_events oe USING (event_id);

CREATE VIEW wms_ops.v_assembly_tasks_with_chain AS
SELECT
  t.id, t.event_id, t.order_id, t.product_id, t.sku_id,
  t.from_bin_id, t.section, t.destination_id,
  t.status, t.operator_id, t.occurred_at, t.created_at, t.updated_at,
  COALESCE(oe.status::text, 'PENDING') AS chain_status,
  oe.tx_hash                           AS chain_tx_hash,
  oe.error_message                     AS chain_error_message,
  oe.updated_at                        AS chain_updated_at
FROM wms_ops.assembly_tasks t
LEFT JOIN public.onchain_events oe USING (event_id);

CREATE INDEX IF NOT EXISTS idx_outbox_events_aggregate_id_type
ON public.outbox_events (aggregate_id, aggregate_type);

COMMIT;
