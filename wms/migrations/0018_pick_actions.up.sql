BEGIN;

-- #14: строка assembly_tasks ссылается на подписанное Pick-действие (часть операции),
-- выгрузившее товар в буфер отгрузки, и хранит время выгрузки — сверка деталей (#17)
-- пересчитывает detailsHash из доменных таблиц (ADR-0001 §4).
ALTER TABLE wms_ops.assembly_tasks
    ADD COLUMN action_id uuid REFERENCES public.warehouse_actions(action_id),
    ADD COLUMN unloaded_at timestamptz;
CREATE INDEX IF NOT EXISTS idx_assembly_tasks_action_id ON wms_ops.assembly_tasks(action_id);

INSERT INTO public.schema_migrations (version) VALUES (18) ON CONFLICT (version) DO NOTHING;

COMMIT;
