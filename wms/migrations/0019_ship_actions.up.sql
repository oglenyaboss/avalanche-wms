BEGIN;

-- #15: строка shippings ссылается на подписанное Ship-действие (часть операции),
-- отгрузившее товар, — сверка деталей (#17) пересчитывает detailsHash из доменных
-- таблиц (ADR-0001 §4).
ALTER TABLE wms_ops.shippings ADD COLUMN action_id uuid REFERENCES public.warehouse_actions(action_id);
CREATE INDEX IF NOT EXISTS idx_shippings_action_id ON wms_ops.shippings(action_id);

INSERT INTO public.schema_migrations (version) VALUES (19) ON CONFLICT (version) DO NOTHING;

COMMIT;
