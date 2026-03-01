#!/usr/bin/env bash
set -euo pipefail

MSG=$(head -1 "$1")
PATTERN="^(feat|fix|docs|style|refactor|test|chore|ci|perf|build|revert)(\(.+\))?\!?: .{3,}"

if ! echo "$MSG" | grep -qE "$PATTERN"; then
  echo ""
  echo "❌ Коммит отклонён: сообщение не соответствует Conventional Commits"
  echo ""
  echo "Формат: <тип>(<скоуп>): <описание>"
  echo ""
  echo "Типы:  feat fix docs style refactor test chore ci perf build revert"
  echo "Скоупы: wms ledger-adapter deploy ci db"
  echo ""
  echo "Примеры:"
  echo "  feat(wms): добавить эндпоинт приёмки товаров"
  echo "  fix(ledger-adapter): исправить таймаут подключения к ноде"
  echo "  docs: обновить README"
  echo "  chore(ci): добавить stage деплоя"
  echo ""
  echo "Подробнее: docs/CONVENTIONS.md"
  echo ""
  exit 1
fi
