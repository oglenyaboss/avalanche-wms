-- Prevent duplicate PENDING assembly tasks for the same product.
-- CREATE INDEX CONCURRENTLY cannot run inside a transaction; the custom
-- migrate.sh runner executes each file via psql without wrapping in BEGIN/COMMIT.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_assembly_tasks_product_pending
ON wms_ops.assembly_tasks (product_id)
WHERE status = 'PENDING';

INSERT INTO public.schema_migrations (version) VALUES (9);
