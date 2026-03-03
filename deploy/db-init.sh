#!/usr/bin/env bash
set -euo pipefail

# ── Config ──────────────────────────────────────────────
DB_HOST="${DB_HOST:-localhost}"
DB_PORT="${DB_PORT:-5432}"
DB_USER="${DB_USER:-root}"
DB_PASSWORD="${DB_PASSWORD:-root}"
DB_NAME="${DB_NAME:-wms_blockchain_db}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-admin}"
DEBEZIUM_PASSWORD="${DEBEZIUM_PASSWORD:-debezium}"

export PGPASSWORD="${DB_PASSWORD}"

run_sql() {
  psql -h "${DB_HOST}" -p "${DB_PORT}" -U "${DB_USER}" -d "${DB_NAME}" \
    -v ON_ERROR_STOP=1 "$@"
}

# ── Step 1: Migrations ─────────────────────────────────
echo "=== Step 1/4: Running database migrations ==="
bash /wms/migrations/migrate.sh

# ── Step 2: Debezium replication user ──────────────────
echo "=== Step 2/4: Creating debezium replication user ==="
if ! run_sql -tAc "SELECT 1 FROM pg_roles WHERE rolname = 'debezium'" | grep -q 1; then
  run_sql -v debezium_pass="${DEBEZIUM_PASSWORD}" <<'EOSQL'
CREATE ROLE debezium WITH LOGIN REPLICATION PASSWORD :'debezium_pass';
EOSQL
  echo "  debezium role created"
else
  echo "  debezium role already exists"
fi
run_sql -c "GRANT SELECT ON ALL TABLES IN SCHEMA public TO debezium;"
run_sql -c "ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO debezium;"
echo "  debezium privileges granted"

# ── Step 3: Outbox publication ─────────────────────────
echo "=== Step 3/4: Creating outbox publication ==="
if ! run_sql -tAc "SELECT 1 FROM pg_publication WHERE pubname = 'outbox_publication'" | grep -q 1; then
  run_sql -c "CREATE PUBLICATION outbox_publication FOR TABLE public.outbox_events;"
  echo "  outbox_publication created"
else
  echo "  outbox_publication already exists"
fi
echo "  outbox_publication ready"

# ── Step 4: Seed admin user ────────────────────────────
echo "=== Step 4/4: Seeding admin user ==="
run_sql -v admin_pass="${ADMIN_PASSWORD}" <<'EOSQL'
CREATE EXTENSION IF NOT EXISTS pgcrypto;
INSERT INTO public.users (user_id, username, password_hash, role, is_active, created_at, updated_at)
VALUES (
  gen_random_uuid(),
  'admin',
  crypt(:'admin_pass', gen_salt('bf')),
  'ADMIN',
  true,
  now(),
  now()
) ON CONFLICT (username) DO NOTHING;
EOSQL
echo "  admin user ready"

echo "=== All init steps completed successfully ==="
