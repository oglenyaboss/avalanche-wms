BEGIN;

-- Горячий путь раскладки: составной индекс (bin_id, status) под счётчик/списки буфера вместо BitmapAnd двух одноколонных,
-- частичный индекс под корзину оператора (DONE-задачи ещё не выгружены, action_id IS NULL); idx_products_qr_code дублирует уникальный products_qr_code_unique.
CREATE INDEX IF NOT EXISTS idx_products_bin_status ON wms_inventory.products (bin_id, status);
CREATE INDEX IF NOT EXISTS idx_assembly_tasks_basket ON wms_ops.assembly_tasks (operator_id, destination_id) WHERE status = 'DONE' AND action_id IS NULL;
DROP INDEX IF EXISTS wms_inventory.idx_products_qr_code;

INSERT INTO public.schema_migrations (version) VALUES (22) ON CONFLICT (version) DO NOTHING;

COMMIT;
