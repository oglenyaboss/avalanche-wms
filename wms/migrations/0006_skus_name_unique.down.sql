BEGIN;

ALTER TABLE wms_inventory.skus
DROP CONSTRAINT IF EXISTS skus_name_unique;

DELETE FROM public.schema_migrations
WHERE version = 6;

COMMIT;
