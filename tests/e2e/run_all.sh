#!/usr/bin/env bash
set -euo pipefail

# Прогон всех сценариев в алфавитном порядке. Любой фейл → stop.
# Каждый сценарий изолирован (свои event_id / product_id), но они шарят
# on-chain state (последовательные переходы Accepted→PutAway→Picked→Shipped),
# поэтому порядок 01→04 важен.

HERE="$(cd "$(dirname "$0")" && pwd)"
SCENARIOS_DIR="$HERE/scenarios"

if [ ! -d "$SCENARIOS_DIR" ]; then
  echo "FATAL: $SCENARIOS_DIR не существует" >&2
  exit 1
fi

total=0
passed=0
for s in "$SCENARIOS_DIR"/*.sh; do
  total=$((total + 1))
  name=$(basename "$s")
  echo "=========================================="
  echo "▶ Running $name"
  echo "=========================================="
  if bash "$s"; then
    passed=$((passed + 1))
    echo "✓ $name PASSED"
  else
    echo "✗ $name FAILED"
    echo "Aborting further scenarios (fail-fast)."
    exit 1
  fi
  echo
done

echo "=========================================="
echo "All $passed/$total scenarios PASSED"
echo "=========================================="
