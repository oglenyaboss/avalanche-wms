# Локальный стенд Avalanche Subnet-EVM (`profile: test`)

Воспроизводимый, container-native стенд **Subnet-EVM** для локального e2e- и
нагрузочного тестирования `ledger-adapter`. Заменяет старый C-Chain-стенд
(`deploy/avalanche/`, удалён) — контракт теперь живёт на настоящем кастомном сабнете
(chainID **99999**), а не на встроенном C-Chain (coreth).

## Почему Subnet-EVM (и почему это теперь воспроизводимо)

C-Chain-стенд был временным решением: поднятие кастомного сабнета на свежей локальной
сети раньше упиралось в deadlock (`createSubnet → addValidator → createBlockchain`
натыкался на Etna fee-state `capacity=0`). Оказалось, что на **avalanchego v1.14.0** это
решается через Go wallet SDK — тот же движок, что использует avalanche-cli. Стенд
поднимает «сырой» узел avalanchego и одноразовый Go-bootstrap полностью в Docker, без
host-side `avalanche-cli` и без proxy-хаков.

## Сервисы

| Сервис | Тип | Роль |
|---|---|---|
| `subnet-node1` | long-running | «Сырой» `avalanchego:v1.14.0` (build `subnet-node/Dockerfile` = базовый образ + запиненный плагин subnet-evm v0.8.0 под нужную архитектуру — **нативно arm64 / amd64, без QEMU**). Отдаёт цепочку на `/ext/bc/<blockchainID>/rpc`. Стартует, уже трекая детерминированный subnetID (`--track-subnets`), поэтому новая цепочка подхватывается **на лету, без рестарта**. |
| `subnet-init` | one-shot | Bootstrap через Go wallet SDK (`subnet-init/main.go`): `CreateSubnet → AddSubnetValidator → CreateChain` ключом ewoq (локальная подпись), идемпотентно. Пишет динамический RPC URL в `/shared/rpc_url.txt`. |
| `contract-deploy` | one-shot | `forge create BatchMappingWMS` ключом ewoq на RPC сабнета (берёт его из `/shared/rpc_url.txt`). Сохраняет RPC URL + адрес + deployer-key в том `shared_state`. Идемпотентно (пропускает, если живой контракт уже записан). |

`ledger-adapter` читает `RPC_URL_FILE` / `CONTRACT_ADDR_FILE` / `PRIVATE_KEY_FILE` из
`shared_state` — миграция его **не меняет**.

## Запуск

```bash
# весь test-стек (wms + kafka + debezium + adapter + цепочка сабнета):
docker compose --profile test up -d

# только поднятие цепочки + контракт:
docker compose --profile test up -d subnet-node1 subnet-init contract-deploy

# полный сброс (свежая цепочка — стирает subnet_data + shared_state):
docker compose --profile test down -v
```

Доступ к цепочке с хоста — только loopback на `127.0.0.1:9650`; динамический путь RPC —
`/ext/bc/<blockchainID>/rpc` (сам blockchainID лежит в `shared_state:/rpc_url.txt`).

## Префандженый ключ (ewoq)

Канонический ключ локальной сети, префандженый на P/X и на EVM-цепочке сабнета:

- EVM-адрес: `0x8db97C7cEcE249c2b98bDC0226Cc4C2A57BF52FC`
- `BatchMappingWMS` детерминированно деплоится в `0x52C84043CD9c865236f11d9Fc9F56aa003c1f922`
  (CREATE с nonce 0 у ewoq) на каждой свежей цепочке.

## Файлы

| Путь | Назначение |
|---|---|
| `network-genesis.json` | Genesis сети avalanchego (network-id 1337), P-Chain с префандженым ewoq. |
| `chain-genesis.json` | Genesis EVM-цепочки сабнета (chainId 99999) — тюнингованный feeConfig (gasLimit 200M, targetBlockRate 1). |
| `node-config.json` | Флаги узла: sybil-off, `http-host 0.0.0.0`, `http-allowed-hosts "*"`, преднабитый `track-subnets`, plugin-dir. |
| `staking/` | Публичные креды staker1 локальной сети (только для тестов — см. `staking/README.md`). |
| `subnet-node/Dockerfile` | avalanchego + плагин subnet-evm v0.8.0 (под `TARGETARCH`). |
| `subnet-init/` | Bootstrap через Go wallet SDK (one-shot). |

## Траблшутинг

| Симптом | Причина / решение |
|---|---|
| RPC изнутри сети отвечает `403 Forbidden` | allowlist avalanchego против DNS-rebinding. В `node-config.json` должно стоять `"http-allowed-hosts": "*"`. |
| `subnet-init` падает с `created subnetID … != expected …` | несвежий `subnet_data` в частичном состоянии → преднабитый `--track-subnets` больше не совпадает. Решение: `docker compose --profile test down -v`. |
| RPC цепочки не отдаёт `0x1869f` | узел не трекает сабнет, либо VMID плагина ≠ VMID в CreateChainTx. Оба используют канонический `srEXiWaHuhNyGwPUi444Tu47ZEDwxTWrbQiuD7FmgSAQ6X7Dy`. |
