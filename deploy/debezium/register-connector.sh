#!/bin/sh
# POSIX sh — в образе curlimages/curl только busybox ash, bash недоступен.
#
# Регистрирует Debezium outbox-коннектор в Kafka Connect автоматически при
# подъёме стека. Запускается одноразовым контейнером connector-init.
#
# Зачем: сервис debezium стартует пустым Kafka Connect — коннектор появляется
# только когда кто-то POST-ит его конфиг в REST API. Этот скрипт делает тот же
# POST, что и `make register-connector`, но сам, без ручного шага.
#
# Идемпотентно: 201 (создан) и 409 (уже есть) — успех. В distributed-режиме
# конфиг хранится в топике debezium_configs, так что на обычном рестарте
# коннектор уже есть → 409 → ok; регистрация реально нужна лишь на чистом
# kafka-volume (первый запуск / после `down -v`).
#
# ВАЖНО: при 409 конфиг НЕ обновляется. Изменил postgres-connector.json —
# применяй через `down -v` либо вручную PUT /connectors/outbox-connector/config.
set -eu

CONNECT_URL="http://debezium:8083"
CONNECTOR_JSON="/connector.json"
MAX_ATTEMPTS=60   # ~120s суммарно при sleep 2

echo "[connector-init] жду готовности Kafka Connect REST API на ${CONNECT_URL} ..."
attempt=0
until curl -sf -o /dev/null "${CONNECT_URL}/connectors"; do
  attempt=$((attempt + 1))
  if [ "${attempt}" -ge "${MAX_ATTEMPTS}" ]; then
    echo "[connector-init] Connect не поднялся за ~120s — выхожу с ошибкой" >&2
    exit 1
  fi
  sleep 2
done
echo "[connector-init] Connect готов, регистрирую outbox-connector ..."

http_code=$(curl -s -o /tmp/resp.json -w '%{http_code}' \
  -X POST "${CONNECT_URL}/connectors" \
  -H 'Content-Type: application/json' \
  -d @"${CONNECTOR_JSON}")

echo "[connector-init] HTTP ${http_code}"
cat /tmp/resp.json 2>/dev/null || true
echo

case "${http_code}" in
  201) echo "[connector-init] коннектор создан" ;;
  409) echo "[connector-init] коннектор уже существует — ok" ;;
  *)   echo "[connector-init] регистрация НЕ удалась (HTTP ${http_code})" >&2; exit 1 ;;
esac

echo "[connector-init] готово"
