BEGIN;

-- #19: удалён старый путь «событие на товар». Вьюхи 0008 связывали строки операций с onchain_events
-- по event_id события на товар; у подписанных действий связь идёт через action_id, вьюхи врут PENDING.
DROP VIEW IF EXISTS wms_ops.v_putaways_with_chain;
DROP VIEW IF EXISTS wms_ops.v_shippings_with_chain;
DROP VIEW IF EXISTS wms_ops.v_assembly_tasks_with_chain;

-- Индекс 0011 обслуживал гейт по (aggregate_id, aggregate_type) событий на товар.
DROP INDEX IF EXISTS public.idx_outbox_events_aggregate_id_type;

INSERT INTO public.schema_migrations (version) VALUES (21) ON CONFLICT (version) DO NOTHING;

COMMIT;
