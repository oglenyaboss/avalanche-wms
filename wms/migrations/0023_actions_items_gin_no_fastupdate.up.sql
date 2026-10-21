BEGIN;

-- Гейты цепи ищут по idx_warehouse_actions_items на каждом шаге; с fastupdate вставки копятся в pending list,
-- и каждый поиск сканирует его целиком. Без него вставка пишет сразу в дерево GIN, поиск — только по дереву.
ALTER INDEX public.idx_warehouse_actions_items SET (fastupdate = off);

INSERT INTO public.schema_migrations (version) VALUES (23) ON CONFLICT (version) DO NOTHING;

COMMIT;
