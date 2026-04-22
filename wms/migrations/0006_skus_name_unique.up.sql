BEGIN;

ALTER TABLE wms_inventory.skus
  ADD CONSTRAINT skus_name_unique UNIQUE (name);

INSERT INTO public.schema_migrations(version)
VALUES (6)
ON CONFLICT (version) DO NOTHING;

COMMIT;
