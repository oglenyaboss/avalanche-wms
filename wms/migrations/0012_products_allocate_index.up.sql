-- Load testing (full-pipeline throughput bench, 2026-06-04) found the WMS front — not the
-- blockchain — is the throughput bottleneck, and root-caused it to assembly.Allocate's stock
-- lookup. assembly.Repository.GetAllocateProductsForSKU runs on every allocate call:
--   SELECT product_id, bin_id FROM wms_inventory.products
--   WHERE sku_id = $1 AND status = 'STORED' AND order_id IS NULL
--   ORDER BY created_at, product_id LIMIT $2 FOR UPDATE SKIP LOCKED
-- With only single-column indexes the planner scans idx_products_status (ALL stored stock),
-- post-filters sku_id/order_id, then Sorts by created_at — O(stored inventory) per call, all
-- under FOR UPDATE. Under load this dominated lock waits (pg_stat_activity: LWLock:LockManager
-- + Lock:transactionid). This composite partial index turns it into a direct index range scan
-- of the first N claimable rows already in created_at order: no scan-all, no sort, shorter
-- lock hold. The partial predicate (order_id IS NULL) keeps the index to allocatable stock.
--
-- CREATE INDEX CONCURRENTLY cannot run inside a transaction; like 0009/0011, this file is
-- intentionally NOT wrapped in BEGIN/COMMIT (migrate.sh runs it raw via psql).
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_products_allocate
ON wms_inventory.products (sku_id, status, created_at, product_id)
WHERE order_id IS NULL;

-- ON CONFLICT keeps the version write idempotent: because this file is not wrapped in a
-- transaction (CONCURRENTLY forbids it), a crash between the index build and this INSERT
-- would otherwise leave a re-run hitting the schema_migrations PK. The index itself is
-- already idempotent via IF NOT EXISTS.
INSERT INTO public.schema_migrations (version) VALUES (12)
ON CONFLICT (version) DO NOTHING;
