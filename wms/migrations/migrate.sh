#!/usr/bin/env bash
set -euo pipefail



# Определить корневой каталог как родительский каталог скрипта
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MIGRATIONS_DIR="${ROOT_DIR}/migrations"

# Параметры подключения
DB_HOST="${DB_HOST:-localhost}"
DB_PORT="${DB_PORT:-5432}"
DB_USER="${DB_USER:-root}"
DB_NAME="${DB_NAME:-wms_blockchain_db}"
DB_PASSWORD="${DB_PASSWORD:-root}"
DB_CONTAINER="${DB_CONTAINER:-postgres_db}"

export PGPASSWORD="${DB_PASSWORD}"

# Проверить, запущен ли PostgreSQL в Docker-контейнере (контейнер существует и running)
is_docker_psql() {
  if command -v docker >/dev/null 2>&1 && docker inspect -f '{{.State.Running}}' "${DB_CONTAINER}" >/dev/null 2>&1; then
    return 0
  fi

  return 1
}

# Выполнить команду psql
run_psql() {
  if is_docker_psql; then
    docker exec -i "${DB_CONTAINER}" psql -U "${DB_USER}" -d "${DB_NAME}" -v ON_ERROR_STOP=1 -q "$@"
    return
  fi

  psql "host=${DB_HOST} port=${DB_PORT} user=${DB_USER} dbname=${DB_NAME}" -v ON_ERROR_STOP=1 -q "$@"
}

# Выполнить SQL-файл:
# - Docker-режим: файл на хосте, передаем через stdin в psql внутри контейнера
# - Локальный режим: напрямую через psql -f
run_psql_file() {
  local file="$1"

  if is_docker_psql; then
    docker exec -i "${DB_CONTAINER}" psql -U "${DB_USER}" -d "${DB_NAME}" -v ON_ERROR_STOP=1 -q < "$file"
    return
  fi

  psql "host=${DB_HOST} port=${DB_PORT} user=${DB_USER} dbname=${DB_NAME}" -v ON_ERROR_STOP=1 -q -f "$file"
}

# Проверить, существует ли таблица schema_migrations
has_migrations_table() {
  run_psql -Atc "SELECT to_regclass('public.schema_migrations') IS NOT NULL;" 2>/dev/null | tail -n 1
}

# Проверить, применена ли версия (есть запись version в schema_migrations)
is_applied() {
  local version="$1"
  run_psql -Atc "SELECT EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = ${version});" 2>/dev/null | tail -n 1
}

# Выполнить один файл миграции
apply_file() {
  local file="$1"
  echo "Applying: ${file##*/}"
  run_psql_file "$file"
}

# Показать подсказку по режимам запуска скрипта.
usage() {
  cat <<EOF
Usage:
  $(basename "$0") [up]
  $(basename "$0") down <version>
  $(basename "$0") down-last
EOF
}

# Найти down-файл по номеру версии (например, 5 -> 0005_*.down.sql).
find_down_file_by_version() {
  local target_version="$1"
  local file
  local base
  local parsed

  shopt -s nullglob
  for file in "${MIGRATIONS_DIR}"/*.down.sql; do
    base="$(basename "$file")"
    parsed="${base%%_*}"
    if [[ "$parsed" =~ ^[0-9]+$ ]] && ((10#${parsed} == target_version)); then
      echo "$file"
      return 0
    fi
  done

  return 1
}

latest_applied_version() {
  # Получить последнюю примененную версию из schema_migrations.
  if [[ "$(has_migrations_table)" != "t" ]]; then
    return 1
  fi

  run_psql -Atc "SELECT max(version) FROM public.schema_migrations;" 2>/dev/null | tail -n 1
}

# Применить все up-миграции по порядку, пропуская уже примененные.
run_up_migrations() {
  shopt -s nullglob
  local up_files=()
  local file
  local base
  local version

  while IFS= read -r file; do
    up_files+=("$file")
  done < <(ls -1 "${MIGRATIONS_DIR}"/*.up.sql 2>/dev/null | sort)

  if ((${#up_files[@]} == 0)); then
    echo "No migrations found in ${MIGRATIONS_DIR}"
    exit 1
  fi

  # Обработать up-миграции по очереди: разобрать версию -> проверить применение -> выполнить, если не применена
  for file in "${up_files[@]}"; do
    base="$(basename "$file")"
    version="${base%%_*}"
    version="${version%%.*}"
    if [[ ! "$version" =~ ^[0-9]+$ ]]; then
      echo "Skip (cannot parse version): ${base}"
      continue
    fi

    # При первом запуске schema_migrations может еще не существовать: выполняем текущую миграцию (обычно 0001_init.up.sql)
    if [[ "$(has_migrations_table)" != "t" ]]; then
      apply_file "$file"
      continue
    fi

    # Если уже применена (есть запись в schema_migrations) — пропустить; иначе выполнить
    if [[ "$(is_applied "$version")" == "t" ]]; then
      echo "Skip (already applied): ${base}"
      continue
    fi

    apply_file "$file"
  done
}

run_down_migration() {
  # Откатить конкретную версию, если она применена и есть соответствующий down-файл.
  local version="$1"
  local down_file

  if [[ "$(has_migrations_table)" != "t" ]]; then
    echo "Cannot rollback: public.schema_migrations does not exist."
    exit 1
  fi

  if ! down_file="$(find_down_file_by_version "$version")"; then
    echo "Rollback file not found for version ${version} in ${MIGRATIONS_DIR}."
    exit 1
  fi

  if [[ "$(is_applied "$version")" != "t" ]]; then
    echo "Version ${version} is not marked as applied in public.schema_migrations."
    exit 1
  fi

  echo "Rolling back: $(basename "$down_file")"
  run_psql_file "$down_file"
}

MODE="${1:-up}"

case "${MODE}" in
  up)
    run_up_migrations
    ;;
  down)
    if (($# != 2)); then
      usage
      exit 1
    fi
    if [[ ! "${2}" =~ ^[0-9]+$ ]]; then
      echo "Invalid version: ${2}. Expected integer."
      exit 1
    fi
    run_down_migration "${2}"
    ;;
  down-last)
    if (($# != 1)); then
      usage
      exit 1
    fi
    last_version="$(latest_applied_version || true)"
    if [[ -z "${last_version}" || "${last_version}" == "NULL" ]]; then
      echo "No applied migrations found to roll back."
      exit 1
    fi
    run_down_migration "${last_version}"
    ;;
  *)
    usage
    exit 1
    ;;
esac

echo "Done."
