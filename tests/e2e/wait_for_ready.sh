#!/usr/bin/env bash
set -euo pipefail

# Ждёт пока весь test-profile стек не будет healthy + contract-deploy завершится.

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib/env.sh
source "$HERE/lib/env.sh" 2>/dev/null || true

echo "=== Waiting for services to be ready ==="

wait_healthy() {
  local container=$1
  local tries=${2:-60}
  for i in $(seq 1 "$tries"); do
    local status
    status=$(docker inspect --format='{{.State.Health.Status}}' "$container" 2>/dev/null || echo "missing")
    if [ "$status" = "healthy" ]; then
      echo "  $container healthy ✓"
      return 0
    fi
    echo "  [$i/$tries] $container: $status"
    sleep 2
  done
  echo "Timeout: $container not healthy" >&2
  return 1
}

wait_completed() {
  local container=$1
  local tries=${2:-60}
  for i in $(seq 1 "$tries"); do
    local status
    status=$(docker inspect --format='{{.State.Status}}' "$container" 2>/dev/null || echo "missing")
    local exitcode
    exitcode=$(docker inspect --format='{{.State.ExitCode}}' "$container" 2>/dev/null || echo "")
    if [ "$status" = "exited" ] && [ "$exitcode" = "0" ]; then
      echo "  $container completed (exit=0) ✓"
      return 0
    fi
    echo "  [$i/$tries] $container: $status (exit=$exitcode)"
    sleep 2
  done
  echo "Timeout: $container not completed" >&2
  return 1
}

# Wait for services without Docker healthchecks, such as distroless containers.
wait_running() {
  local container=$1
  local tries=${2:-60}
  for i in $(seq 1 "$tries"); do
    local running
    running=$(docker inspect --format='{{.State.Running}}' "$container" 2>/dev/null || echo "missing")
    if [ "$running" = "true" ]; then
      echo "  $container running ✓"
      return 0
    fi
    echo "  [$i/$tries] $container: running=$running"
    sleep 2
  done
  echo "Timeout: $container not running" >&2
  return 1
}

# Probe an HTTP health endpoint for distroless services that expose /health
# but have no Docker healthcheck (e.g. ledger-adapter).
wait_health_http() {
  local label=$1 url=$2 tries=${3:-60}
  for i in $(seq 1 "$tries"); do
    if curl -fsS "$url" >/dev/null 2>&1; then echo "  $label healthy ✓"; return 0; fi
    echo "  [$i/$tries] $label not ready"; sleep 2
  done
  echo "Timeout: $label not ready" >&2; return 1
}

wait_healthy postgres_db
wait_healthy kafka
wait_healthy avalanchego
wait_completed contract_deploy
wait_health_http ledger-adapter "http://localhost:8085/health"

echo "=== All services ready ==="
