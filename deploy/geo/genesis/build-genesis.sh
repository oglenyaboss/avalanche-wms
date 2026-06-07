#!/usr/bin/env bash
# build-genesis.sh — assemble network-genesis.geo.json (networkID 1338) from validators.json.
#
# Economic params MIRROR the proven deploy/subnet/network-genesis.json sample: each of the 3
# geo validators stakes the same per-validator amount the working single-staker sample used
# (1e15 nAVAX), and the primary-network cChainGenesis string is reused verbatim. Only deltas:
# networkID 1337->1338, a fresh startTime, and initialStakers expanded 1 -> 3 (dino/alex/itldc).
#
# startTime is FROZEN at first generation. Regenerating produces a different network identity,
# so this script refuses to overwrite an existing output. Delete it deliberately to rebuild.
set -euo pipefail
cd "$(dirname "$0")"

VALID=validators.json
SAMPLE=../../subnet/network-genesis.json
OUT=network-genesis.geo.json

[ -f "$VALID" ]  || { echo "missing $VALID" >&2; exit 1; }
[ -f "$SAMPLE" ] || { echo "missing $SAMPLE" >&2; exit 1; }
if [ -f "$OUT" ]; then
  echo "REFUSING to overwrite existing $OUT (startTime is frozen; regenerating forks the network)." >&2
  echo "Delete it deliberately if you really mean to rebuild." >&2
  exit 1
fi

NCOUNT=$(jq '.validators | length' "$VALID")
PER_STAKE=1000000000000000                 # proven per-validator stake from the sample (1e15 nAVAX)
STAKE_TOTAL=$(( PER_STAKE * NCOUNT ))        # divided equally among initialStakers
NOW=$(date +%s)
START=$(( NOW - 3600 ))                      # 1h in the past: valid + recent
LOCK=$(( START + 604800 ))                   # +7 days, mirrors sample's startTime->locktime gap
REWARD="X-custom1t73fa4p4dypa4s3kgufuvr6hmprjclw6yuagv7"
STAKED_SRC="X-custom17vyx67luxklpc6xmve96nnnp5grqzf4sh3jctv"
EWOQ_X="X-custom18jma8ppw3nhx5r4ap8clazz0dps7rv5u9xde7p"
CCHAIN=$(jq -r '.cChainGenesis' "$SAMPLE")   # reuse exact primary C-chain genesis

jq -n \
  --argjson start "$START" --argjson lock "$LOCK" --argjson stake "$STAKE_TOTAL" \
  --arg reward "$REWARD" --arg src "$STAKED_SRC" --arg ewoq "$EWOQ_X" --arg cchain "$CCHAIN" \
  --slurpfile valid "$VALID" '
  {
    networkID: 1338,
    allocations: [
      { ethAddr: "0x0000000000000000000000000000000000000000", avaxAddr: $src,
        initialAmount: 0, unlockSchedule: [ { amount: $stake, locktime: $lock } ] },
      { ethAddr: "0x0000000000000000000000000000000000000000", avaxAddr: $ewoq,
        initialAmount: 30000000000000000,
        unlockSchedule: [ { amount: 20000000000000000, locktime: 0 },
                          { amount: 1000000000000000, locktime: $lock } ] }
    ],
    startTime: $start,
    initialStakeDuration: 31536000,
    initialStakeDurationOffset: 5400,
    initialStakedFunds: [ $src ],
    initialStakers: ( $valid[0].validators | map({
      nodeID: .nodeID, rewardAddress: $reward, delegationFee: 10000,
      signer: { publicKey: .signer.publicKey, proofOfPossession: .signer.proofOfPossession }
    }) ),
    cChainGenesis: $cchain,
    message: "geo-distributed subnet (thesis defense)"
  }' > "$OUT"

echo "wrote $OUT:"
jq '{networkID, startTime, initialStakeDuration,
     stakers: (.initialStakers | map(.nodeID)),
     perStaker: ($stake_total // (.allocations[0].unlockSchedule[0].amount / (.initialStakers|length)))}' \
   --argjson stake_total "$STAKE_TOTAL" "$OUT"
