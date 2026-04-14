# E2E Tests — Ledger Adapter

Bash-based e2e сценарии для проверки полного pipeline Kafka→adapter→BatchMappingWMS→DB.

## Pre-requisites

- Docker Compose v2.22+
- `psql`, `jq`, `docker` в PATH
- Полный стек запущен через `docker compose --profile test up -d`

## Быстрый запуск

```bash
# 1. Поднять стек + дождаться готовности
docker compose --profile test up -d
./tests/e2e/wait_for_ready.sh

# 2. Прогнать все сценарии
./tests/e2e/run_all.sh

# 3. Очистка
docker compose --profile test down -v
```

## Сценарии

| Файл | Что проверяет |
|---|---|
| `01-receiving-happy.sh` | Single event → wms.receiving.v1 → `batchAccept` → itemStatus=1 (Accepted), onchain_events=COMMITTED |
| `02-putaway-happy.sh` | После Accepted → wms.putaway.v1 → `batchPutAway` → itemStatus=2 (PutAway) |
| `03-picking-happy.sh` | После PutAway → wms.picking.v1 → `batchPick` → itemStatus=3 (Picked) |
| `04-shipping-happy.sh` | После Picked → wms.shipping.v1 → `batchShip` → itemStatus=4 (Shipped) |
| `05-batch-receiving.sh` | 3 события в одну batch-транзакцию, все COMMITTED, один tx_hash |
| `06-revert-invalid-transition.sh` | Попытка batchPick без Accepted/PutAway → chain revert → FAILED + DLQ |
| `07-idempotency-restart.sh` | Adapter restart после PENDING → не дублирует on-chain, дожимает до COMMITTED |

## Структура

```
tests/e2e/
├── README.md
├── wait_for_ready.sh        # дожидается healthy всех сервисов
├── run_all.sh                # последовательно гоняет все scenarios/*.sh
├── lib/
│   ├── env.sh                # DB_URL / RPC_URL / CONTRACT_ADDR из shared volume
│   ├── wait_for.sh           # wait_for_status / wait_for_kafka helper'ы
│   └── kafka.sh              # publish_event helper через docker exec kafka
└── scenarios/
    └── *.sh
```

## Troubleshooting

- **`wait_for_status: timeout`** — смотри `docker logs ledger-adapter`. Часто: parse fail из-за отсутствия header `id` (fix в продьюсере), либо revert на chain (fix в seed data / data contract).
- **`chain-id mismatch`** — проверь `cast chain-id --rpc-url $RPC_URL` — должен быть `43112`.
- **`db connection refused`** — postgres не поднялся. `docker compose ps` → статус postgres.

## Известные ограничения

- Полный happy-path для WMS API (через `POST /receiving/...`) пока не покрыт — зависит от готовности WMS-endpoint'ов и seed-data. Текущие сценарии бьют Kafka напрямую через `kcat` внутри kafka-контейнера, что тестирует поведение адаптера изолированно.
- `07-idempotency-restart.sh` использует `docker compose restart ledger-adapter` — ожидание реинициализации ~10s.
