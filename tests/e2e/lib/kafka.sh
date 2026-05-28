#!/usr/bin/env bash
# Kafka-direct публикация через kcat (edenhill/kcat:1.7.1).
# Sends to unified topic wms.events.v1 with aggregate_type header.

EVENTS_TOPIC="wms.events.v1"

publish_event() {
  local aggregate_type=$1 # "receiving" | "putaway" | "picking" | "shipping"
  local event_id=$2       # UUID
  local product_id=$3     # UUID
  local payload="${4:-{}}"

  docker run --rm --network blockchain_project_e2e_app_network \
    -i edenhill/kcat:1.7.1 \
    -b kafka:9092 \
    -t "$EVENTS_TOPIC" \
    -P \
    -k "$product_id" \
    -H "id=$event_id" \
    -H "aggregate_type=$aggregate_type" \
    <<< "$payload"
}
