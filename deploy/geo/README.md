# Гео-распределённый стенд Subnet-EVM (изолирован от deploy/subnet)

ОТДЕЛЬНАЯ свежая сеть (networkID 1338) для демо распределённости. НЕ пересекается
с локальным стендом deploy/subnet (профили test/stress).

Все валидаторы (dino/alex/itldc) запечены в genesis как initialStakers.
node-home — локальная RPC-нода (не валидатор). Состав фиксирован в genesis.

## Запуск

Сеть и приложение поднимаются скриптами в этой директории:

- `docker-compose.geo.yaml` — валидаторы (dino/alex/itldc) + node-home RPC-нода
- `run-app-on-geo.sh` — поднять WMS-стек поверх гео-сети
- `run-geo-bench.sh` / `run-throughput-geo.sh` — нагрузочные прогоны по гео-сети
- `genesis/` — genesis с запечёнными initialStakers; `addvalidators/`, `addprimary/` — Go-утилиты добавления валидаторов

## Подписанные действия

Гео-бенч работает на подписанных действиях (#52): на geo-цепи задеплоены
`ConsortiumRegistry` + `SignedMappingWMS`, адаптеру передаётся `REGISTRY_ADDR`.
Порядок запуска:

1. `deploy/geo/contracts/deploy-geo.sh` — Registry + SignedMappingWMS → `genesis/contract.env`;
2. `deploy/geo/run-app-on-geo.sh up -d --force-recreate --no-deps ledger-adapter`;
3. операторы: `COMPOSE=$PWD/deploy/geo/run-app-on-geo.sh sh -c 'cd tests/e2e && go run ./cmd/stress-provision -operators <VUS>'`
   (регистрирует организацию в GEO-реестре; перезаписывает `tests/stress/.signed`);
4. `run-throughput-geo.sh` / `run-geo-bench.sh`.

Метрики считают товары: `warehouse_actions.item_ids` (одна outbox/onchain-строка = одно
подписанное действие на N товаров; «events» в `geo-bench.json` = подписанные действия).

## Allow-list

В genesis гео-цепи (`genesis/chain-genesis.json`, байт-идентичная копия
`deploy/subnet/chain-genesis.json`) включены allow-list отправителей транзакций
(`txAllowListConfig`) и деплоеров контрактов (`contractDeployerAllowListConfig`);
админ обоих списков — ewoq. Последствия для гео:

- Genesis запекается в `CreateChainTx`: включение/любая смена genesis = **новая цепь** =
  новый `BLOCKCHAIN_ID` → обновить `genesis/chain-ids.env`, перерендерить/раздать конфиги
  узлам (`track-subnets`) и рестартнуть валидаторы, передеплоить контракт
  (`contracts/deploy-geo.sh`).
- Адаптер подписывает ключом ewoq (админ allow-list) — отдельная выдача tx-роли не
  нужна; `fund-signers.sh` нужен только для `ledger-adapter/cmd/blaster`.
- Добавление нового адреса на живой цепи — транзакцией админа через прекомпайл;
  полный раздел (роли, процедуры `cast`, поведение сети):
  [deploy/subnet/README.md, раздел «Allow-list»](../subnet/README.md).

Метрики и отчёты прогонов — в `metrics/` и [`../../docs/load-testing/`](../../docs/load-testing/) (`geo-distributed-report.md`, `geo-throughput-rootcause.md`).
