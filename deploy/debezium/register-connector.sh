#!/bin/sh
# POSIX sh — в образе curlimages/curl только busybox ash, bash недоступен.
#
# Регистрирует Debezium outbox-коннектор в Kafka Connect автоматически при
# подъёме стека. Запускается одноразовым контейнером connector-init.
#
# Зачем: сервис debezium стартует пустым Kafka Connect — коннектор появляется
# только когда кто-то регистрирует его в REST API. Этот скрипт делает то же,
# что и `make register-connector`, но сам, без ручного шага.
#
# Идемпотентно и с обновлением конфига: если коннектора нет — POST создаёт его
# (201). Если уже есть (409) — конфиг в топике debezium_configs устарел, поэтому
# PUT-им актуальный config из /connector.json (200) — изменение конфига
# применяется на каждом старте стека, `down -v` для этого больше не нужен.
set -eu

CONNECT_URL="${CONNECT_URL:-http://debezium:8083}"
CONNECTOR_JSON="${CONNECTOR_JSON:-/connector.json}"
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
  409)
    echo "[connector-init] коннектор уже существует — обновляю конфиг (PUT)"
    # ponytail: извлечение объекта config зависит от форматирования файла
    # (2 пробела у "config" и его закрывающей скобки, 4 у свойств); при смене
    # форматирования — заменить на jq/python.
    config_json=$(sed -n '/"config":[[:space:]]*{/,/^  }$/p' "${CONNECTOR_JSON}" | sed '1s/.*/{/')
    put_code=$(curl -s -o /tmp/put-resp.json -w '%{http_code}' \
      -X PUT "${CONNECT_URL}/connectors/outbox-connector/config" \
      -H 'Content-Type: application/json' \
      -d "${config_json}")
    echo "[connector-init] PUT HTTP ${put_code}"
    cat /tmp/put-resp.json 2>/dev/null || true
    echo
    case "${put_code}" in
      200) echo "[connector-init] конфиг коннектора обновлён" ;;
      *)   echo "[connector-init] обновление конфига НЕ удалось (HTTP ${put_code})" >&2; exit 1 ;;
    esac
    ;;
  *)   echo "[connector-init] регистрация НЕ удалась (HTTP ${http_code})" >&2; exit 1 ;;
esac

echo "[connector-init] готово"
