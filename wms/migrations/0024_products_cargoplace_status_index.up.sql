-- Товары грузоместа выбираются по (cargoplace_id, status): с одноколонными индексами планировщик
-- берёт BitmapAnd с idx_products_status, набитым мёртвыми версиями. Одноколонный cargoplace — префикс нового.
--
-- CREATE/DROP INDEX CONCURRENTLY не работает в транзакции; как 0012, файл намеренно без BEGIN/COMMIT.
-- Упавший CONCURRENTLY оставляет INVALID-индекс, который IF NOT EXISTS пропустил бы при повторе, поэтому новый индекс сначала удаляется.
DROP INDEX CONCURRENTLY IF EXISTS wms_inventory.idx_products_cargoplace_status;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_products_cargoplace_status ON wms_inventory.products (cargoplace_id, status);
DROP INDEX CONCURRENTLY IF EXISTS wms_inventory.idx_products_cargoplace_id;

INSERT INTO public.schema_migrations (version) VALUES (24) ON CONFLICT (version) DO NOTHING;
