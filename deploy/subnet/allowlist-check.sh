#!/usr/bin/env bash
# allowlist-check.sh — приёмка issue #5 «Allow-list отправителей транзакций в genesis
# сабнета». На ПОДНЯТОЙ цепи доказывает:
#   ШАГ 1 — genesis реально содержит оба allow-list (ewoq — Admin в txAllowList и
#           contractDeployerAllowList);
#   ШАГ 4 — tx с адреса вне списка сеть отклоняет [критерий 2 issue #5];
#   ШАГ 6 — deploy с адреса вне deployer-списка отклоняется [критерий 2, список деплоеров];
#   ШАГ 5/7 — админ добавляет адрес в список/ы транзакцией setEnabled на живой цепи,
#           без пересоздания цепи [критерий 3 issue #5];
#   ШАГ 8/9 — после добавления адрес может деплоить и слать tx.
# Genesis правится только оффлайн (читается один раз при CreateChain, смена = пересоздание
# цепи); этот скрипт ничего в genesis не пишет — только живые tx админа.
#
# Foundry на хосте не нужен: cast вызывается docker-образом ghcr.io/foundry-rs/foundry:stable.
# Требуются: docker, curl, jq.
#
# Usage:
#   deploy/subnet/allowlist-check.sh
#   RPC_URL=http://127.0.0.1:9650/ext/bc/<blockchainID>/rpc deploy/subnet/allowlist-check.sh
#
# Переменные окружения:
#   RPC_URL   — RPC цепи; если задан, используется как есть (детект пропускается)
#   NODE_URI  — API узла для детекта (default http://127.0.0.1:9650)
#   SUBNET_ID — сабнет, чью цепь искать при детекте (default — track-subnets из node-config.json)
#   EWOQ_KEY  — приватник админа allow-list (default ewoq, он же админ в genesis)
set -euo pipefail

RPC_URL="${RPC_URL:-}"
NODE_URI="${NODE_URI:-http://127.0.0.1:9650}"
NODE_URI="${NODE_URI%/}"
SUBNET_ID="${SUBNET_ID:-$(jq -r '."track-subnets"' "$(dirname "$0")/node-config.json")}"
EWOQ="${EWOQ_KEY:-0x56289e99c94b6912bfc12adc093c9b51124f0dc54ac7a766b2bc5ccf558d8027}" # ewoq, genesis alloc
TX_ALLOW_LIST=0x0200000000000000000000000000000000000002
DEPLOYER_ALLOW_LIST=0x0200000000000000000000000000000000000000

for tool in curl jq docker; do
  command -v "$tool" >/dev/null || { echo "FATAL: $tool not on PATH" >&2; exit 1; }
done

# cast только через docker (на хосте foundry нет).
cast() { docker run --rm --network host --entrypoint cast ghcr.io/foundry-rs/foundry:stable "$@"; }

if [ -z "$RPC_URL" ]; then
  echo "==> RPC_URL не задан, обнаруживаю blockchainID сабнета $SUBNET_ID на $NODE_URI"
  RESP=$(curl -s -X POST \
    -H 'Content-Type: application/json' \
    --data '{"jsonrpc":"2.0","id":1,"method":"platform.getBlockchains","params":[]}' \
    "$NODE_URI/ext/bc/P") || { echo "FATAL: $NODE_URI недоступен — стенд поднят?" >&2; exit 1; }
  CHAIN_ID=$(jq -r --arg sub "$SUBNET_ID" \
    '.result.blockchains[] | select(.subnetID==$sub) | .id' <<<"$RESP")
  [ -n "$CHAIN_ID" ] || { echo "FATAL: на $NODE_URI нет цепи сабнета $SUBNET_ID — цепь пересоздана с новым genesis?" >&2; exit 1; }
  RPC_URL="$NODE_URI/ext/bc/$CHAIN_ID/rpc"
fi
echo "==> RPC цепи: $RPC_URL"

EWOQ_ADDR=$(cast wallet address --private-key "$EWOQ")
echo "==> ewoq (админ обоих allow-list): $EWOQ_ADDR"

ERRFILE=$(mktemp)
trap 'rm -f "$ERRFILE"' EXIT

echo "==> ШАГ 1: genesis-санити — ewoq Admin обоих allow-list"
ROLE_TX=$(cast call "$TX_ALLOW_LIST" 'readAllowList(address)(uint256)' "$EWOQ_ADDR" --rpc-url "$RPC_URL")
ROLE_DP=$(cast call "$DEPLOYER_ALLOW_LIST" 'readAllowList(address)(uint256)' "$EWOQ_ADDR" --rpc-url "$RPC_URL")
if [ "$ROLE_TX" != "2" ] || [ "$ROLE_DP" != "2" ]; then
  echo "FAIL: ewoq role tx=$ROLE_TX deployer=$ROLE_DP, ожидалось 2 (Admin) — genesis без allow-list / цепь не пересоздана с новым genesis" >&2
  exit 1
fi
echo "  ✓ ewoq Admin: txAllowList=$ROLE_TX contractDeployerAllowList=$ROLE_DP"

echo "==> ШАГ 2: свежий адрес вне allow-list"
NEW=$(cast wallet new --json | jq -r '.[0]')
NEW_ADDR=$(jq -r '.address' <<<"$NEW")
NEW_PK=$(jq -r '.private_key' <<<"$NEW")
echo "  новый адрес: $NEW_ADDR"
ROLE_NEW=$(cast call "$TX_ALLOW_LIST" 'readAllowList(address)(uint256)' "$NEW_ADDR" --rpc-url "$RPC_URL")
if [ "$ROLE_NEW" != "0" ]; then
  echo "FAIL: $NEW_ADDR role=$ROLE_NEW, ожидалось 0 (нет в списке)" >&2
  exit 1
fi
echo "  ✓ $NEW_ADDR вне списка (роль 0)"

echo "==> ШАГ 3: фондирование нового адреса tx-ом админа ewoq (1 ether)"
cast send "$NEW_ADDR" --value 1ether --private-key "$EWOQ" --rpc-url "$RPC_URL" >/dev/null
for _ in $(seq 1 30); do
  [ "$(cast balance "$NEW_ADDR" --rpc-url "$RPC_URL")" != "0" ] && break
  sleep 2
done
BAL=$(cast balance "$NEW_ADDR" --rpc-url "$RPC_URL")
if [ "$BAL" = "0" ]; then
  echo "FAIL: $NEW_ADDR не получил баланс (balance=$BAL)" >&2
  exit 1
fi
echo "  ✓ $NEW_ADDR funded ($BAL wei)"

echo "==> ШАГ 4 [критерий 2]: tx с адреса вне списка ДОЛЖНА быть отклонена"
if cast send "$EWOQ_ADDR" --value 1wei --private-key "$NEW_PK" --rpc-url "$RPC_URL" >"$ERRFILE" 2>&1; then
  echo "FAIL: tx с $NEW_ADDR (вне allow-list) ПРОШЛА — allow-list не работает" >&2
  exit 1
fi
if ! grep -q "cannot issue transaction from non-allow listed address" "$ERRFILE"; then
  echo "FAIL: tx отклонена с другой ошибкой:" >&2
  sed 's/^/    /' "$ERRFILE" >&2
  exit 1
fi
echo "  ✓ mempool отклонил tx: cannot issue transaction from non-allow listed address"

echo "==> ШАГ 5 [критерий 3]: админ ewoq добавляет $NEW_ADDR в tx-allow-list на живой цепи"
cast send "$TX_ALLOW_LIST" 'setEnabled(address)' "$NEW_ADDR" --private-key "$EWOQ" --rpc-url "$RPC_URL" >/dev/null
ROLE_NEW=""
for _ in $(seq 1 10); do
  ROLE_NEW=$(cast call "$TX_ALLOW_LIST" 'readAllowList(address)(uint256)' "$NEW_ADDR" --rpc-url "$RPC_URL")
  [ "$ROLE_NEW" = "1" ] && break
  sleep 2
done
if [ "$ROLE_NEW" != "1" ]; then
  echo "FAIL: $NEW_ADDR role=$ROLE_NEW после setEnabled, ожидалось 1 (Enabled)" >&2
  exit 1
fi
echo "  ✓ $NEW_ADDR разрешён (роль 1 Enabled)"

echo "==> ШАГ 6 [критерий 2, список деплоеров]: deploy с адреса вне deployer-allow-list ДОЛЖЕН упасть"
# Отправитель уже разрешён в tx-списке (ШАГ 5), но не в списке деплоеров — проверка
# прекомпайлом 0x02…00 срабатывает на уровне EVM: tx.origin ... is not authorized to deploy a contract.
if cast send --private-key "$NEW_PK" --rpc-url "$RPC_URL" --create 6001600155 >"$ERRFILE" 2>&1; then
  echo "FAIL: deploy с $NEW_ADDR (вне deployer-allow-list) ПРОШЁЛ — deployer-allow-list не работает" >&2
  exit 1
fi
if ! grep -q "is not authorized to deploy a contract" "$ERRFILE"; then
  echo "FAIL: deploy упал с другой ошибкой:" >&2
  sed 's/^/    /' "$ERRFILE" >&2
  exit 1
fi
echo "  ✓ deploy отклонён: tx.origin $NEW_ADDR is not authorized to deploy a contract"

echo "==> ШАГ 7 [критерий 3, список деплоеров]: админ разрешает $NEW_ADDR в deployer-allow-list"
cast send "$DEPLOYER_ALLOW_LIST" 'setEnabled(address)' "$NEW_ADDR" --private-key "$EWOQ" --rpc-url "$RPC_URL" >/dev/null
ROLE_DP_NEW=""
for _ in $(seq 1 10); do
  ROLE_DP_NEW=$(cast call "$DEPLOYER_ALLOW_LIST" 'readAllowList(address)(uint256)' "$NEW_ADDR" --rpc-url "$RPC_URL")
  [ "$ROLE_DP_NEW" = "1" ] && break
  sleep 2
done
if [ "$ROLE_DP_NEW" != "1" ]; then
  echo "FAIL: $NEW_ADDR deployer-role=$ROLE_DP_NEW после setEnabled, ожидалось 1 (Enabled)" >&2
  exit 1
fi
echo "  ✓ $NEW_ADDR разрешён деплоить (роль 1 Enabled)"

echo "==> ШАГ 8: deploy с разрешённого адреса теперь ДОЛЖЕН пройти"
cast send --private-key "$NEW_PK" --rpc-url "$RPC_URL" --create 6001600155 >/dev/null
echo "  ✓ deploy с разрешённого адреса прошёл"

echo "==> ШАГ 9: повторная tx с $NEW_ADDR теперь ДОЛЖНА пройти"
cast send "$EWOQ_ADDR" --value 1wei --private-key "$NEW_PK" --rpc-url "$RPC_URL" >/dev/null
echo "  ✓ tx с разрешённого адреса прошла"

echo "==> ИТОГ: приёмка issue #5 пройдена"
echo "  ✓ ШАГ 1: genesis-санити (ewoq Admin txAllowList + contractDeployerAllowList)"
echo "  ✓ ШАГ 2: новый адрес вне списка (роль 0)"
echo "  ✓ ШАГ 3: фондирование админом снаружи списка"
echo "  ✓ ШАГ 4 [критерий 2]: tx вне списка отклонена mempool-ом"
echo "  ✓ ШАГ 5 [критерий 3]: админ добавил адрес setEnabled-ом на живой цепи"
echo "  ✓ ШАГ 6 [критерий 2]: deploy вне deployer-списка отклонён"
echo "  ✓ ШАГ 7 [критерий 3]: админ разрешил деплой на живой цепи"
echo "  ✓ ШАГ 8: deploy с разрешённого адреса прошёл"
echo "  ✓ ШАГ 9: tx с разрешённого адреса прошла"
exit 0
