#!/usr/bin/env bash
# gen-configs.sh — render per-validator avalanchego configs from node-config.base.json + hosts.env.
# Outputs (gitignored, contain real IPs): node-config.dino.gen.json, node-config.alex.gen.json.
#
# SYBIL (default false) — Phase 0 ships sybil-protection-enabled=false (permissive): the network is
# distributed, replicated across regions, and survives node loss (kill a node, the rest keep
# serving). It is NOT stake-weighted BFT — a lone node can finalize.
#
# SYBIL=true gives real stake-weighted BFT, but it requires connecting to >= ~73.33% of total stake
# (avalanchego's network-health threshold) or the chains stall ("not connected to enough stake").
# With all 3 validators baked into genesis at equal weight and itldc OFFLINE in Phase 0, dino+alex =
# 66.67% < 73.33% -> stall (verified 2026-06-07). So real BFT is a PHASE 1 thing: with all 3
# validators online, 100% > 73.33%, snow 3/2 enforces quorum, and killing 1 of 3 still finalizes.
#
# track-subnets is empty until the subnet exists (Task 5) — re-run with TRACK_SUBNETS=<id> + restart.
set -euo pipefail
cd "$(dirname "$0")"
[ -f hosts.env ] || { echo "missing hosts.env (gitignored IP map)" >&2; exit 1; }
# shellcheck disable=SC1091
. ./hosts.env
TRACK="${TRACK_SUBNETS:-}"
SYBIL="${SYBIL:-false}"

render() { # name public-ip bootstrap-ips bootstrap-ids
  jq --arg ip "$2" --arg bips "$3" --arg bids "$4" --arg track "$TRACK" --argjson sybil "$SYBIL" '
    .["sybil-protection-enabled"] = $sybil
    | .["public-ip"]     = $ip
    | .["bootstrap-ips"] = $bips
    | .["bootstrap-ids"] = $bids
    | (if $track == "" then . else .["track-subnets"] = $track end)
  ' node-config.base.json > "node-config.$1.gen.json"
  echo "  wrote node-config.$1.gen.json (public-ip=$2 sybil=$SYBIL bootstrap=${3:-<seed>} track=${TRACK:-<none>})"
}

echo "SYBIL=$SYBIL  TRACK_SUBNETS='${TRACK:-<empty, pre-subnet>}'"
render dino "$DINO_IP" "" ""
render alex "$ALEX_IP" "$DINO_IP:9651" "$DINO_NODEID"
