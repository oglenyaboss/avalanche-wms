# Local Avalanche (profile `test`)

Поднимает single-node avalanchego v1.12.2 в режиме `network-id=local`, деплоит `BatchMappingWMS.sol` на **C-Chain** (встроенный EVM avalanchego, chainID `43112`). Используется для локального e2e-тестирования `ledger-adapter` (issue #20).

> **Почему C-Chain, а не custom subnet?**
> Custom subnet c Transaction/Contract Deployer Allow Lists потребовал бы отдельный bootstrap (createSubnet → addValidator → createBlockchain через P-Chain TX), что натыкается на deadlock на fresh local network avalanchego v1.12+: `capacity=0` в Etna fee state до первого блока, SDK не может построить TX. См. отдельный issue `feat(infra): custom subnet with Allow Lists` — решение требует либо ручной сборки raw TX, либо avalanche-cli оркестрации. Для адаптера из issue #20 это не принципиально: Allow List живёт на уровне сабнета, поведение адаптера идентично на C-Chain.

## Quick start

```bash
# Только блокчейн-часть (без wms/kafka/etc)
docker compose --profile test up -d avalanchego
docker compose --profile test up contract-deploy   # one-shot, exit 0 после деплоя

# Проверка:
docker run --rm -v blockchain_project_shared_state:/s alpine cat /s/rpc_url.txt
docker run --rm -v blockchain_project_shared_state:/s alpine cat /s/contract_addr.txt

# Или сразу весь стек (wms + kafka + debezium + adapter + avalanchego):
docker compose --profile test up -d

# Полный сброс:
docker compose --profile test down -v
```

## Components

| Сервис | Тип | Назначение |
|---|---|---|
| `avalanchego` | long-running | single-node avalanchego, `network-id=local`, C-Chain доступен на `/ext/bc/C/rpc` |
| `contract-deploy` | one-shot | `forge create BatchMappingWMS` ewoq-ключом на C-Chain. Сохраняет RPC URL + address + deployer key в shared volume. |

## Files

| Путь | Что |
|---|---|
| `Dockerfile` | avalanchego:v1.12.2 + curl + jq |
| `node-config.json` | network-id=local, http-host=0.0.0.0, http-allowed-hosts=*, sybil-protection=false |

## Prerequisites

- Docker Compose **v2.22+** (`depends_on.required: false` пришёл с Compose Spec 1.20 / Compose v2.22, Oct 2023). Docker Desktop 4.26+ подходит.
- Экспонируем RPC (9650) только на `127.0.0.1` — по-дефолту не доступен с LAN/публичного интерфейса.

## Prefunded accounts

Local avalanchego префандит **ewoq** ключ на всех 3 chains:
- Hex PK: `0x56289e99c94b6912bfc12adc093c9b51124f0dc54ac7a766b2bc5ccf558d8027`
- C-Chain addr: `0x8db97C7cEcE249c2b98bDC0226Cc4C2A57BF52FC`
- Баланс: ~50M AVAX на C-Chain

Этот ключ используется и для деплоя, и как signer для `ledger-adapter` в test-профиле.

**Про идемпотентность:** `avalanche_data` volume персистит состояние между запусками, поэтому nonce ewoq-а растёт. `deploy.sh` проверяет `/shared/contract_addr.txt` и если там адрес с ненулевым bytecode — пропускает повторный деплой. Для полного сброса: `docker compose --profile test down -v`.

## Verify

```bash
RPC=$(docker run --rm -v blockchain_project_shared_state:/s alpine cat /s/rpc_url.txt)
ADDR=$(docker run --rm -v blockchain_project_shared_state:/s alpine cat /s/contract_addr.txt)

cast chain-id --rpc-url "$RPC"                                      # → 43112
cast call "$ADDR" "itemStatus(uint256)(uint8)" 0 --rpc-url "$RPC"   # → 0 (None)
```

## Troubleshooting

| Симптом | Причина | Фикс |
|---|---|---|
| healthcheck avalanchego fail по timeout | network bootstrap долгий на первом запуске | Увеличить `start_period` до 60s |
| `forge create` падает на `Failed to connect` | C-Chain ещё не готов | `contract-deploy` уже ждёт `service_healthy` — если всё равно падает, проверь `docker logs avalanchego` на наличие ошибок genesis |
| `invalid host` в ответах avalanchego | `http-allowed-hosts` не пропускает hostname внутри compose сети | В `node-config.json` стоит `"http-allowed-hosts": "*"` — если ты менял, верни обратно |

## Full reset

```bash
docker compose --profile test down -v
```

## Future work

Custom subnet с Allow Lists — отдельной issue когда доберёмся до Linux-машины или потратим пол-дня на ручной TX encoding через `platform.issueTx`.
