BEGIN;

-- WARNING: Rolling back restores columns with DEFAULT values.
-- Rows committed on-chain will lose their status. Run this backfill after rollback:
--
-- UPDATE wms_ops.putaways p SET onchain_status='ONCHAIN_COMMITTED', onchain_tx_hash=oe.tx_hash
-- FROM public.onchain_events oe WHERE oe.event_id=p.event_id AND oe.status='COMMITTED';
-- (analogous for shippings and assembly_tasks)

DROP VIEW IF EXISTS wms_ops.v_putaways_with_chain;
DROP VIEW IF EXISTS wms_ops.v_shippings_with_chain;
DROP VIEW IF EXISTS wms_ops.v_assembly_tasks_with_chain;

CREATE TYPE wms_ops.operation_onchain_status AS ENUM ('PENDING_ONCHAIN', 'ONCHAIN_COMMITTED');

ALTER TABLE wms_ops.putaways
  ADD COLUMN onchain_status wms_ops.operation_onchain_status NOT NULL DEFAULT 'PENDING_ONCHAIN',
  ADD COLUMN onchain_tx_hash text;

ALTER TABLE wms_ops.shippings
  ADD COLUMN onchain_status wms_ops.operation_onchain_status NOT NULL DEFAULT 'PENDING_ONCHAIN',
  ADD COLUMN onchain_tx_hash text;

ALTER TABLE wms_ops.assembly_tasks
  ADD COLUMN onchain_status wms_ops.operation_onchain_status,
  ADD COLUMN onchain_tx_hash text;

CREATE INDEX idx_putaways_onchain_status ON wms_ops.putaways(onchain_status);
CREATE INDEX idx_shippings_onchain_status ON wms_ops.shippings(onchain_status);

DELETE FROM public.schema_migrations WHERE version = 8;

COMMIT;
