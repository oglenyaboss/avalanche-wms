#!/usr/bin/env bash
# Polling helper'ы. Все возвращают non-zero на timeout.

# wait_for_status <event_id> <expected> [tries=30] [interval_sec=2]
wait_for_status() {
  local event_id=$1
  local expected=$2
  local tries=${3:-30}
  local interval=${4:-2}

  for i in $(seq 1 "$tries"); do
    local status
    status=$(psql_q "SELECT status::text FROM public.onchain_events WHERE event_id='$event_id'" || true)
    if [ "$status" = "$expected" ]; then
      echo "  [$i/$tries] event $event_id → $status ✓"
      return 0
    fi
    echo "  [$i/$tries] event $event_id status=${status:-<none>}, waiting for $expected..."
    sleep "$interval"
  done
  echo "Timeout waiting for event $event_id to become $expected (last=${status:-<none>})" >&2
  return 1
}

# wait_for_item_status <item_id_hex> <expected_uint8> [tries=30] [interval_sec=2]
# Проверяет on-chain itemStatus (0=None, 1=Accepted, 2=PutAway, 3=Picked, 4=Shipped).
wait_for_item_status() {
  local item_id=$1
  local expected=$2
  local tries=${3:-30}
  local interval=${4:-2}

  for i in $(seq 1 "$tries"); do
    local status
    status=$(cast_cmd call "$CONTRACT_ADDR" "itemStatus(uint256)(uint8)" "$item_id" --rpc-url "$RPC_URL" 2>/dev/null | tr -d '[:space:]' || true)
    if [ "$status" = "$expected" ]; then
      echo "  [$i/$tries] item $item_id on-chain status → $status ✓"
      return 0
    fi
    echo "  [$i/$tries] item $item_id on-chain status=${status:-<none>}, waiting for $expected..."
    sleep "$interval"
  done
  echo "Timeout waiting for item $item_id on-chain status $expected" >&2
  return 1
}
