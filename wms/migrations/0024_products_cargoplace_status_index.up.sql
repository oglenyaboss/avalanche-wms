BEGIN;

-- Товары грузоместа выбираются по (cargoplace_id, status): с одноколонными индексами планировщик
-- берёт BitmapAnd с idx_products_status, набитым мёртвыми версиями. Одноколонный cargoplace — префикс нового.
CREATE INDEX IF NOT EXISTS idx_products_cargoplace_status ON wms_inventory.products (cargoplace_id, status);
DROP INDEX IF EXISTS wms_inventory.idx_products_cargoplace_id;

INSERT INTO public.schema_migrations (version) VALUES (24) ON CONFLICT (version) DO NOTHING;

COMMIT;
