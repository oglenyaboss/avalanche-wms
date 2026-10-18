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
| `contract-deploy` | one-shot | `forge create` ключом ewoq на RPC сабнета (берёт его из `/shared/rpc_url.txt`): сначала `ConsortiumRegistry`, затем `BatchMappingWMS`. Сохраняет RPC URL, адреса (`registry_addr.txt`, `contract_addr.txt`) и deployer-key в том `shared_state`. Идемпотентно по каждому контракту (пропускает, если живой контракт уже записан). |

`ledger-adapter` читает `RPC_URL_FILE` / `CONTRACT_ADDR_FILE` / `REGISTRY_ADDR_FILE` / `PRIVATE_KEY_FILE` из
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
- `ConsortiumRegistry` детерминированно деплоится в `0x52C84043CD9c865236f11d9Fc9F56aa003c1f922`
  (CREATE с nonce 0 у ewoq) на каждой свежей цепочке; владелец реестра — ewoq.
- `BatchMappingWMS` детерминированно деплоится в `0x17aB05351fC94a1a67Bf3f56DdbB941aE6c63E25`
  (CREATE с nonce 1 у ewoq) на каждой свежей цепочке.

## Allow-list отправителей транзакций и деплоеров

В config genesis EVM-цепочки включены оба пермиссионных прекомпайла:
`txAllowListConfig` (allow-list отправителей транзакций) и
`contractDeployerAllowListConfig` (allow-list деплоеров контрактов) — у обоих
`blockTimestamp: 0`, админ — ewoq (`adminAddresses: ["0x8db97C7cEcE249c2b98bDC0226Cc4C2A57BF52FC"]`).
Genesis единый: локальная копия `deploy/subnet/chain-genesis.json` и гео-копия
`deploy/geo/genesis/chain-genesis.json` байт-идентичны — править всегда обе.

Роли адреса в списке (возвращаемое значение `readAllowList`):

| Роль | Значение | Права |
|---|---|---|
| None | 0 | нет доступа |
| Enabled | 1 | может слать транзакции (для списка деплоеров — деплоить контракты) |
| Admin | 2 | может слать транзакции **и** менять список через прекомпайл (`setEnabled` / `setNone` / `setAdmin`) |
| Manager | 3 | недоступен до апгрейда Durango — **не использовать** |

Прекомпайл-адреса: tx-allow-list — `0x0200000000000000000000000000000000000002`,
contract-deployer-allow-list — `0x0200000000000000000000000000000000000000`.
Интерфейс обоих одинаков: `readAllowList(address) returns (uint256)`, `setAdmin(address)`,
`setEnabled(address)`, `setNone(address)`.

**Кто есть кто.** ewoq — админ обоих списков; он же владелец консорциума в `ConsortiumRegistry`
и ключ деплоя контрактов (one-shot `contract-deploy`, `forge create ConsortiumRegistry` и
`forge create BatchMappingWMS`) и ключ отправки адаптера
(`shared_state:/deployer_key.txt`). Ротация ключа адаптера (issue #7) — это добавление
нового адреса в tx-список транзакцией админа (процедура ниже); genesis при этом не
трогается.

**Процедура на живой цепи.** Genesis читается один раз при `CreateChain`, поэтому список
меняется на работающей цепи только транзакциями через прекомпайл, админом. `cast` на
хосте не установлен — используется docker-образ foundry; `RPC_URL` локального стенда —
динамический путь из `shared_state:/rpc_url.txt` (`/ext/bc/<blockchainID>/rpc`):

```bash
# посмотреть роль адреса (0=None, 1=Enabled, 2=Admin):
docker run --rm --network host --entrypoint cast ghcr.io/foundry-rs/foundry:stable call 0x0200000000000000000000000000000000000002 'readAllowList(address)(uint256)' 0xАДРЕС --rpc-url "$RPC_URL"

# разрешить слать tx (от имени админа; <ewoq> — приватный ключ ewoq, публичный dev-ключ из .gitleaks.toml):
docker run --rm --network host --entrypoint cast ghcr.io/foundry-rs/foundry:stable send 0x0200000000000000000000000000000000000002 'setEnabled(address)' 0xАДРЕС --private-key <ewoq> --rpc-url "$RPC_URL"

# отозвать право: 'setNone(address)'; выдать админа: 'setAdmin(address)' — тот же send, другая сигнатура
docker run --rm --network host --entrypoint cast ghcr.io/foundry-rs/foundry:stable send 0x0200000000000000000000000000000000000002 'setNone(address)' 0xАДРЕС --private-key <ewoq> --rpc-url "$RPC_URL"
docker run --rm --network host --entrypoint cast ghcr.io/foundry-rs/foundry:stable send 0x0200000000000000000000000000000000000002 'setAdmin(address)' 0xАДРЕС --private-key <ewoq> --rpc-url "$RPC_URL"
```

**Поведение сети.** Транзакция с адреса вне tx-списка отбрасывается mempool-ом:
`eth_sendRawTransaction` возвращает ошибку
`cannot issue transaction from non-allow listed address`. Деплой контракта с адреса вне
deployer-списка проходит mempool (если отправитель разрешён в tx-списке), но падает
в EVM: `tx.origin <адрес> is not authorized to deploy a contract`. Поэтому
multi-signer-адреса адаптера (`PRIVATE_KEYS`) обязаны присутствовать в списке — на
гео-стенде их разрешает `fund-signers.sh` (после фондирования выдаёт signer-ам роль
`Enabled` от админа).

Проверка стенда целиком (сам находит RPC):

```bash
bash deploy/subnet/allowlist-check.sh
```

**Важно.** Смена genesis = пересоздание цепи: genesis запекается в `CreateChainTx`,
`blockchainID` меняется, `subnetID` остаётся. Локально —
`docker compose --profile test down -v` или `tests/stress/reset-chain.sh`; последствия
для гео-сети — в [deploy/geo/README.md](../geo/README.md).

## Файлы

| Путь | Назначение |
|---|---|
| `network-genesis.json` | Genesis сети avalanchego (network-id 1337), P-Chain с префандженым ewoq. Стейк валидатора действует 10 лет (до 2036-05-30); после этой даты `subnet-init` на свежем стенде не пройдёт. |
| `chain-genesis.json` | Genesis EVM-цепочки сабнета (chainId 99999) — тюнингованный feeConfig (gasLimit 200M, targetBlockRate 1, **targetGas 2B** — рекалибровка fee-market под нагрузку, иначе baseFee растёт ×50 и адаптер залипает на `rpc-tx-fee-cap`; коммит `50ce2515`); включены allow-list прекомпайлы (`txAllowListConfig` + `contractDeployerAllowListConfig`, админ ewoq — см. раздел «Allow-list» выше). |
| `node-config.json` | Флаги узла: sybil-off, `http-host 0.0.0.0`, `http-allowed-hosts "*"`, преднабитый `track-subnets`, plugin-dir, `max-stake-duration`/`stake-minting-period` под 10-летний стейк из genesis. |
| `staking/` | Публичные креды staker1 локальной сети (только для тестов — см. `staking/README.md`). |
| `subnet-node/Dockerfile` | avalanchego + плагин subnet-evm v0.8.0 (под `TARGETARCH`). |
| `subnet-init/` | Bootstrap через Go wallet SDK (one-shot). |

## Траблшутинг

| Симптом | Причина / решение |
|---|---|
| RPC изнутри сети отвечает `403 Forbidden` | allowlist avalanchego против DNS-rebinding. В `node-config.json` должно стоять `"http-allowed-hosts": "*"`. |
| `subnet-init` падает с `created subnetID … != expected …` | несвежий `subnet_data` в частичном состоянии → преднабитый `--track-subnets` больше не совпадает. Решение: `docker compose --profile test down -v`. |
| RPC цепочки не отдаёт `0x1869f` | узел не трекает сабнет, либо VMID плагина ≠ VMID в CreateChainTx. Оба используют канонический `srEXiWaHuhNyGwPUi444Tu47ZEDwxTWrbQiuD7FmgSAQ6X7Dy`. |
| `eth_estimateGas`: `gas required exceeds allowance (50000000)` при крупных пачках | дефолтный кап `rpc-gas-cap` subnet-evm = 50M на оценку газа. **Намеренно не переопределяется:** финальная архитектура — конвейер из множества tx с умеренной пачкой (`BATCH_SIZE`, default 10), per-tx оценка ≪ 50M, кап не упирается. Бьёт только при гигантских пачках (≳1800 событий/tx — отвергнутый ранний путь «одна огромная tx»); если к ним вернётесь, поднимите `rpc-gas-cap` в chain-config узла. |
