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

Метрики и отчёты прогонов — в `metrics/` и [`../../docs/load-testing/`](../../docs/load-testing/) (`geo-distributed-report.md`, `geo-throughput-rootcause.md`).
