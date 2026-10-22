-- Горячий путь раскладки: составной индекс (bin_id, status) под счётчик/списки буфера вместо BitmapAnd двух одноколонных,
-- одноколонный idx_products_bin_id — его префикс; частичный индекс под корзину оператора (DONE-задачи ещё не выгружены,
-- action_id IS NULL); idx_products_qr_code дублирует уникальный products_qr_code_unique.
--
-- CREATE/DROP INDEX CONCURRENTLY не работает в транзакции; как 0012, файл намеренно без BEGIN/COMMIT.
-- Упавший CONCURRENTLY оставляет INVALID-индекс, который IF NOT EXISTS пропустил бы при повторе, поэтому новые индексы сначала удаляются.
DROP INDEX CONCURRENTLY IF EXISTS wms_inventory.idx_products_bin_status;
DROP INDEX CONCURRENTLY IF EXISTS wms_ops.idx_assembly_tasks_basket;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_products_bin_status ON wms_inventory.products (bin_id, status);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_assembly_tasks_basket ON wms_ops.assembly_tasks (operator_id, destination_id) WHERE status = 'DONE' AND action_id IS NULL;
DROP INDEX CONCURRENTLY IF EXISTS wms_inventory.idx_products_bin_id;
DROP INDEX CONCURRENTLY IF EXISTS wms_inventory.idx_products_qr_code;

INSERT INTO public.schema_migrations (version) VALUES (22) ON CONFLICT (version) DO NOTHING;
