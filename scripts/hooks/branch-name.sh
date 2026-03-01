#!/usr/bin/env bash
set -euo pipefail

BRANCH=$(git rev-parse --abbrev-ref HEAD)

# Разрешить sync-пуш через ALLOW_PROTECTED_PUSH=1
if [ "${ALLOW_PROTECTED_PUSH:-}" = "1" ]; then
  exit 0
fi

if [ "$BRANCH" = "main" ] || [ "$BRANCH" = "master" ] || [ "$BRANCH" = "develop" ] || [ "$BRANCH" = "dev" ]; then
  echo ""
  echo "❌ Пуш напрямую в $BRANCH запрещён"
  echo ""
  echo "Создай ветку и открой MR:"
  echo "  git checkout -b fix/my-change"
  echo "  git push -u origin fix/my-change"
  echo ""
  echo "Для синхронизации веток (rebase dev на master):"
  echo "  ALLOW_PROTECTED_PUSH=1 git push origin dev"
  echo ""
  exit 1
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
