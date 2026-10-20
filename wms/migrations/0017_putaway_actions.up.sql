BEGIN;

-- #13: строка putaways ссылается на подписанное действие (часть операции),
-- которое разместило этот товар, — сверка деталей (#17) пересчитывает detailsHash
-- из доменных таблиц (ADR-0001 §4).
ALTER TABLE wms_ops.putaways ADD COLUMN action_id uuid REFERENCES public.warehouse_actions(action_id);
CREATE INDEX IF NOT EXISTS idx_putaways_action_id ON wms_ops.putaways(action_id);

INSERT INTO public.schema_migrations (version) VALUES (17) ON CONFLICT (version) DO NOTHING;

COMMIT;
