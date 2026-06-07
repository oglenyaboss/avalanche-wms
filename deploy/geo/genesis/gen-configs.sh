#!/usr/bin/env bash
# gen-configs.sh — render per-validator avalanchego configs from node-config.base.json + hosts.env.
# Outputs (gitignored, contain real IPs): node-config.dino.gen.json, node-config.alex.gen.json.
#
# snow 3/2: all 3 validators (dino/alex/itldc) are baked into genesis, so the validator SET is 3
# even though itldc is offline in Phase 0. sample-size 3 + quorum 2 finalizes on dino+alex and
# halts if only one is up (the kill-node gate proof). track-subnets is empty until the subnet
# exists (Task 5) — re-run with TRACK_SUBNETS=<subnetID> and restart the nodes afterwards.
set -euo pipefail
cd "$(dirname "$0")"
[ -f hosts.env ] || { echo "missing hosts.env (gitignored IP map)" >&2; exit 1; }
# shellcheck disable=SC1091
. ./hosts.env
TRACK="${TRACK_SUBNETS:-}"

render() { # name public-ip bootstrap-ips bootstrap-ids
  jq --arg ip "$2" --arg bips "$3" --arg bids "$4" --arg track "$TRACK" '
    .["public-ip"]    = $ip
    | .["bootstrap-ips"] = $bips
    | .["bootstrap-ids"] = $bids
    | (if $track == "" then . else .["track-subnets"] = $track end)
  ' node-config.base.json > "node-config.$1.gen.json"
  echo "  wrote node-config.$1.gen.json (public-ip=$2 bootstrap=${3:-<seed>} track=${TRACK:-<none>})"
}

echo "TRACK_SUBNETS='${TRACK:-<empty, pre-subnet>}'"
render dino "$DINO_IP" "" ""
render alex "$ALEX_IP" "$DINO_IP:9651" "$DINO_NODEID"
