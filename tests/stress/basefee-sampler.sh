#!/bin/bash
# basefee-sampler.sh — log baseFee + gasUsedRatio every 4s to /tmp/basefee.log during a run.
# Proves the fee-market fix: under load baseFee should stay near the 25 Gwei floor (was ×50).
set -u
HRPC=$(cat /tmp/hrpc.txt 2>/dev/null)
: > /tmp/basefee.log
while true; do
  ts=$(date +%s)
  blk=$(curl -s -X POST -H 'content-type:application/json' \
    --data '{"jsonrpc":"2.0","id":1,"method":"eth_getBlockByNumber","params":["latest",false]}' "$HRPC" 2>/dev/null)
  bf=$(echo "$blk" | sed -nE 's/.*"baseFeePerGas":"0x([0-9a-f]+)".*/\1/p')
  gu=$(echo "$blk" | sed -nE 's/.*"gasUsed":"0x([0-9a-f]+)".*/\1/p')
  gl=$(echo "$blk" | sed -nE 's/.*"gasLimit":"0x([0-9a-f]+)".*/\1/p')
  bfg=$(python3 -c "print(f'{int('${bf:-0}',16)/1e9:.0f}')" 2>/dev/null)
  ratio=$(python3 -c "print(f'{int('${gu:-0}',16)/max(1,int('${gl:-1}',16)):.0%}')" 2>/dev/null)
  echo "$ts baseFee=${bfg}Gwei gasUsedRatio=${ratio}" >> /tmp/basefee.log
  sleep 4
done
