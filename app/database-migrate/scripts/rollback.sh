#!/usr/bin/env bash
# Скрипт отката миграций БД (откат последней / до указанной версии / на N шагов)
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MIGRATIONS_DIR="${ROOT_DIR}/migrations"

DB_HOST="${DB_HOST:-localhost}"
DB_PORT="${DB_PORT:-5432}"
DB_USER="${DB_USER:-root}"
DB_NAME="${DB_NAME:-wms_blockchain_db}"
DB_PASSWORD="${DB_PASSWORD:-root}"
DB_CONTAINER="${DB_CONTAINER:-postgres_db}"

export PGPASSWORD="${DB_PASSWORD}"

usage() {
  cat <<'USAGE'
Usage:
  ./scripts/rollback.sh                 откat последней миграции (по умолчанию)
  ./scripts/rollback.sh --last          то же самое
  ./scripts/rollback.sh --steps N       откатить последние N миграций
  ./scripts/rollback.sh --to VERSION    откатить до версии VERSION (оставить VERSION как примененную)
  ./scripts/rollback.sh --all           откатить все (эквивалентно --to 0)

USAGE
}

# Проверить, можно ли использовать psql в Docker-контейнере (контейнер существует и running)
is_docker_psql() {
  if command -v docker >/dev/null 2>&1 && docker inspect -f '{{.State.Running}}' "${DB_CONTAINER}" >/dev/null 2>&1; then
    return 0
  fi
  return 1
}

# Выполнить команду psql (в приоритете контейнер; иначе локальное подключение)
run_psql() {
  if is_docker_psql; then
    docker exec -i "${DB_CONTAINER}" psql -U "${DB_USER}" -d "${DB_NAME}" -v ON_ERROR_STOP=1 -q "$@"
    return
  fi
  psql "host=${DB_HOST} port=${DB_PORT} user=${DB_USER} dbname=${DB_NAME}" -v ON_ERROR_STOP=1 -q "$@"
}

# Выполнить SQL-файл:
run_psql_file() {
  local file="$1"
  if is_docker_psql; then
    docker exec -i "${DB_CONTAINER}" psql -U "${DB_USER}" -d "${DB_NAME}" -v ON_ERROR_STOP=1 -q < "$file"
    return
  fi
  psql "host=${DB_HOST} port=${DB_PORT} user=${DB_USER} dbname=${DB_NAME}" -v ON_ERROR_STOP=1 -q -f "$file"
}

# Извлечь номер версии из имени файла:
extract_version() {
  local base version
  base="$(basename "$1")"
  version="${base%%_*}"
  version="${version%%.*}"
  version="$(echo "$version" | sed 's/^0*//')"
  if [[ -z "$version" ]]; then
    echo "0"
  else
    echo "$version"
  fi
}

# Проверить, существует ли таблица учета миграций (если нет — миграции не применялись или БД очищена)
has_migrations_table() {
  run_psql -Atc "SELECT to_regclass('public.schema_migrations') IS NOT NULL;" 2>/dev/null | tail -n 1
}

# Получить список примененных версий (по убыванию)
get_applied_versions_desc() {
  run_psql -Atc "SELECT version::text FROM public.schema_migrations ORDER BY version DESC;" 2>/dev/null
}

# Найти путь к down-миграции по номеру версии
find_down_file_for_version() {
  local want="$1"
  local i
  for i in "${!DOWN_VERSIONS[@]}"; do
    if [[ "${DOWN_VERSIONS[$i]}" == "$want" ]]; then
      echo "${DOWN_FILES[$i]}"
      return 0
    fi
  done
  return 1
}

MODE="last"
STEPS=""
TARGET=""

while (($#)); do
  case "$1" in
    --last)
      MODE="last"
      shift
      ;;
    --steps)
      MODE="steps"
      STEPS="${2:-}"
      shift 2
      ;;
    --to)
      MODE="to"
      TARGET="${2:-}"
      shift 2
      ;;
    --all)
      MODE="to"
      TARGET="0"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

shopt -s nullglob
down_files=()
while IFS= read -r file; do
  down_files+=("$file")
done < <(ls -1 "${MIGRATIONS_DIR}"/*.down.sql 2>/dev/null | sort)

if ((${#down_files[@]} == 0)); then
  echo "No down migrations found in ${MIGRATIONS_DIR}"
  exit 1
fi


DOWN_VERSIONS=()
DOWN_FILES=()
for file in "${down_files[@]}"; do
  v="$(extract_version "$file")"
  DOWN_VERSIONS+=("$v")
  DOWN_FILES+=("$file")
done

if [[ "$(has_migrations_table)" != "t" ]]; then
  echo "Nothing to rollback: public.schema_migrations does not exist in ${DB_NAME}."
  exit 0
fi

applied_versions=()
while IFS= read -r v; do
  [[ -z "$v" ]] && continue
  applied_versions+=("$v")
done < <(get_applied_versions_desc)

if ((${#applied_versions[@]} == 0)); then
  echo "Nothing to rollback: no applied versions found."
  exit 0
fi

case "$MODE" in
  last)
    MODE="steps"
    STEPS="1"
    ;;
  steps)
    if [[ -z "$STEPS" || ! "$STEPS" =~ ^[0-9]+$ || "$STEPS" -le 0 ]]; then
      echo "--steps requires a positive integer." >&2
      exit 2
    fi
    ;;
  to)
    if [[ -z "$TARGET" || ! "$TARGET" =~ ^[0-9]+$ ]]; then
      echo "--to requires an integer version (0 allowed)." >&2
      exit 2
    fi
    ;;
  *)
    echo "Internal error: unknown mode ${MODE}" >&2
    exit 2
    ;;
esac

echo "Running destructive rollback."


rolled_back=0
for v in "${applied_versions[@]}"; do
  do_rollback="false"
  if [[ "$MODE" == "steps" ]]; then
    if (( rolled_back < STEPS )); then
      do_rollback="true"
    fi
  else
    if (( v > TARGET )); then
      do_rollback="true"
    fi
  fi

  if [[ "$do_rollback" != "true" ]]; then
    break
  fi

  down_file="$(find_down_file_for_version "$v" || true)"
  if [[ -z "${down_file:-}" ]]; then
    echo "Missing down migration for version ${v}." >&2
    exit 1
  fi

  echo "Rolling back version ${v}: $(basename "$down_file")"
  run_psql_file "$down_file"

  if [[ "$(has_migrations_table)" == "t" ]]; then
    run_psql -c "DELETE FROM public.schema_migrations WHERE version = ${v};" >/dev/null 2>&1 || true
  fi

  rolled_back=$((rolled_back + 1))
done

echo "Done."
