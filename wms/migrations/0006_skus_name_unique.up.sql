ALTER TABLE wms_inventory.skus
ADD CONSTRAINT skus_name_unique UNIQUE (name);
