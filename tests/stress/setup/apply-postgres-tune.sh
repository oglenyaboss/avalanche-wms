#!/bin/bash
# apply-postgres-tune.sh — apply the STRESS-ONLY postgres OLTP tuning (postgres-tune.sql) to the
# running stresstest postgres, then restart it so shared_buffers/wal_buffers take effect.
#
# ⚠️ Run this once after bringing up a FRESH stresstest stack (e.g. after `down -v`). Without it the
# throughput harness tops out at ~455/с instead of ~1924/с — see postgres-tune.sql for the why.
# It is NOT applied automatically and is NOT a base-compose default (synchronous_commit=off is
# unsafe for production). Stress-only, opt-in.
#
# Usage: COMPOSE_PROJECT_NAME=stresstest ./tests/stress/setup/apply-postgres-tune.sh
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1
PG="${PG_CONTAINER:-postgres_db}"
DB="${DB_NAME:-wms_blockchain_db}"
USER="${DB_USER:-root}"

echo "=== applying stress OLTP tuning to $PG ==="
docker exec -i "$PG" psql -U "$USER" -d "$DB" -f - < tests/stress/setup/postgres-tune.sql 2>&1 | tail -3 \
  || { echo "FATAL: ALTER SYSTEM failed (is postgres up?)"; exit 1; }

echo "=== restart $PG (shared_buffers/wal_buffers need it) ==="
docker restart "$PG" >/dev/null 2>&1
for i in $(seq 1 20); do
  docker exec "$PG" pg_isready -U "$USER" -d "$DB" >/dev/null 2>&1 && { echo "  postgres up (try $i)"; break; }
  sleep 2
done

echo "=== verify (synchronous_commit must be off, shared_buffers 2GB) ==="
docker exec "$PG" psql -U "$USER" -d "$DB" -tAc "
  SELECT name||'='||setting||COALESCE(unit,'') FROM pg_settings
  WHERE name IN ('synchronous_commit','shared_buffers','max_wal_size','wal_level')" 2>/dev/null | sed 's/^/  /'
echo "=== NOTE: restart dropped app DB connections — recreate wms_app + ledger-adapter (+ verify CDC slot) ==="
echo "  e.g. WMS_DB_MAX_CONNS=30 docker compose up -d --force-recreate wms_app ledger-adapter"
echo "######## TUNE APPLIED ########"
