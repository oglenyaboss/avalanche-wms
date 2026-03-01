#!/usr/bin/env bash
set -euo pipefail

BRANCH=$(git rev-parse --abbrev-ref HEAD)

if [ "$BRANCH" = "main" ] || [ "$BRANCH" = "develop" ] || [ "$BRANCH" = "dev" ]; then
  exit 0
fi

PATTERN="^(feature|fix|hotfix|docs|refactor|chore|test)/[a-z0-9._-]+$"

if ! echo "$BRANCH" | grep -qE "$PATTERN"; then
  echo ""
  echo "❌ Имя ветки не соответствует конвенции"
  echo ""
  echo "Формат: тип/описание-через-дефис"
  echo ""
  echo "Типы: feature fix hotfix docs refactor chore test"
  echo ""
  echo "Примеры:"
  echo "  feature/receiving-endpoint"
  echo "  fix/kafka-connection-timeout"
  echo "  docs/api-documentation"
  echo ""
  echo "Подробнее: docs/CONVENTIONS.md"
  echo ""
  exit 1
fi
