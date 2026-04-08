#!/usr/bin/env bash
set -euo pipefail

KAFKA_BROKER="${KAFKA_BROKER:-kafka:9092}"

TOPICS=(
  "__debezium-heartbeat.wms"
  "wms.receiving.v1"
  "wms.putaway.v1"
  "wms.picking.v1"
  "wms.shipping.v1"
  "wms.dlq.v1"
)

echo "=== Creating Kafka topics ==="

for topic in "${TOPICS[@]}"; do
  echo "  Creating topic: ${topic}"
  /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server "${KAFKA_BROKER}" \
    --create --if-not-exists \
    --topic "${topic}" \
    --partitions 1 \
    --replication-factor 1
done

echo "=== All Kafka topics created ==="
