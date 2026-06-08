#!/usr/bin/env bash
# fund-signers.sh — фондит N детерминированных signer-аккаунтов из ewoq на geo-цепи
# для multi-signer ledger-adapter'а. Flusher шардит in-flight tx по этим аккаунтам
# (каждый ограничен TxPoolAccountSlots узла → N аккаунтов = N× in-flight ёмкость →
# насыщение WAN-цепи; single signer её голодит). Ключи выводятся ДЕТЕРМИНИРОВАННО из
# публичного seed keccak("geo-ms-signer-v1:<i>") — воспроизводимо для ВКР и не секрет
# на disposable test-цепи. Печатает PRIVATE_KEYS (comma-separated) в stdout.
#
# Usage:
#   N=8 deploy/geo/fund-signers.sh                 # печатает PRIVATE_KEYS
#   PRIVATE_KEYS="$(N=8 deploy/geo/fund-signers.sh)" \
#     deploy/geo/run-app-on-geo.sh up -d --force-recreate --no-deps ledger-adapter
set -euo pipefail
export PATH="$HOME/.foundry/bin:$PATH"
cd "$(git rev-parse --show-toplevel)"
# shellcheck disable=SC1091
. deploy/geo/genesis/chain-ids.env # BLOCKCHAIN_ID

GEO_RPC="${GEO_RPC:-http://localhost:9750/ext/bc/${BLOCKCHAIN_ID}/rpc}"
EWOQ="${EWOQ_KEY:-0x56289e99c94b6912bfc12adc093c9b51124f0dc54ac7a766b2bc5ccf558d8027}" # genesis-funded
N="${N:-8}"
FUND="${FUND:-100ether}"

command -v cast >/dev/null || { echo "FATAL: foundry cast not on PATH" >&2; exit 1; }
EADDR=$(cast wallet address --private-key "$EWOQ")
nonce=$(cast nonce "$EADDR" --rpc-url "$GEO_RPC")

# Fire N funding txs async с явными nonce'ами (ewoq nonce монотонен) — иначе
# concurrent cast-send'ы дрались бы за один nonce.
keys=()
for i in $(seq 0 $((N - 1))); do
  pk=$(cast keccak "geo-ms-signer-v1:$i")
  addr=$(cast wallet address --private-key "$pk")
  keys+=("$pk")
  cast send --private-key "$EWOQ" --rpc-url "$GEO_RPC" --async --nonce $((nonce + i)) "$addr" --value "$FUND" >/dev/null
  echo "  funded signer$i $addr ($FUND)" >&2
done

# Ждём, пока все N funding-tx замайнятся (ewoq nonce продвинулся на N).
for _ in $(seq 1 60); do
  [ "$(cast nonce "$EADDR" --rpc-url "$GEO_RPC")" -ge "$((nonce + N))" ] && break
  sleep 2
done

# Verify балансы + эмит PRIVATE_KEYS на stdout (ключи — на stdout, логи — на stderr).
for pk in "${keys[@]}"; do
  addr=$(cast wallet address --private-key "$pk")
  [ "$(cast balance "$addr" --rpc-url "$GEO_RPC")" = "0" ] && { echo "FATAL: $addr unfunded" >&2; exit 1; }
done
echo "  all $N signers funded ✓" >&2
(
  IFS=','
  echo "${keys[*]}"
)
