#!/usr/bin/env bash
# Kafka-direct публикация. Используем kafka.Writer через tiny Go-утилиту
# внутри ledger-adapter-образа? Нет — проще через `kafka-console-producer.sh`
# с headers через kafka-go API невозможно, headers нужны.
#
# Поэтому публикуем через kcat (aka kafkacat), пробрасывая headers через -H.
# kcat доступен в edenhill/kcat:1.7.1 image.

publish_event() {
  local topic=$1
  local event_id=$2   # UUID
  local product_id=$3 # UUID
  local payload="${4:-{}}"

  docker run --rm --network blockchain_project_app_network \
    -i edenhill/kcat:1.7.1 \
    -b kafka:9092 \
    -t "$topic" \
    -P \
    -k "$product_id" \
    -H "id=$event_id" \
    <<< "$payload"
}
