# Гео-распределённый стенд Subnet-EVM (изолирован от deploy/subnet)

ОТДЕЛЬНАЯ свежая сеть (networkID 1338) для демо распределённости. НЕ пересекается
с локальным стендом deploy/subnet (профили test/stress).

Все валидаторы (dino/alex/itldc) запечены в genesis как initialStakers.
node-home — локальная RPC-нода (не валидатор). Состав фиксирован в genesis.

Запуск: docs/superpowers/plans/2026-06-07-geo-distributed-subnet-phase0.md
