# Нагрузочное тестирование WMS — Инструкция по запуску

## Тесты

| Файл | Описание | VUs (peak) | Нужен seed? |
|------|----------|-----------|-------------|
| `01-smoke.js` | Проверка работоспособности (1 VU, 1 итерация) | 1 | Нет |
| `02-health.js` | Стресс GET /health, до 200 VUs | 200 | Нет |
| `03-auth.js` | Нагрузка на /auth/login + /auth/refresh | 50+30 | Нет |
| `04-receiving-gate.js` | КПП-приёмка (scan-ttn → scan-cp × 3 → accept) | 100 | **Да** |
| `05-receiving-table.js` | Приёмка на столе (scan-cp → scan-box → … → close-cp) | 60 | **Да** |
| `06-assembly.js` | Сборка заказов (allocate → tasks → pick → ship-buffer) | 40 | **Да** |
| `07-full-flow.js` | Полный цикл receiving → putaway → assembly → shipping | 30 | **Да** |

---

## Запуск всех тестов одной командой (рекомендуется)

```bash
# Собрать образ k6-stress и запустить все тесты (01–07):
make stress

# Наблюдать за прогрессом:
make stress-logs
```

Команда выполняет:
1. Сборку образа `k6-stress` (grafana/k6 + postgresql-client)
2. Запуск всего стека (postgres, kafka, wms, debezium, …)
3. Ожидание готовности PostgreSQL и WMS
4. Очистку предыдущих stress-данных (`stress-cleanup.sql`)
5. Засев данных (`stress-seed.sql`)
6. Разрешение UUID ячеек/рейсов из БД
7. Последовательный запуск тестов 01–07

Тест считается **выполненным**, если exit code контейнера равен нулю:
```bash
docker inspect k6-stress --format='{{.State.ExitCode}}'
```

---

## Запуск отдельных тестов (локальная установка k6)

```bash
# Запустить только smoke-тест
k6 run tests/stress/01-smoke.js

# Запустить только health-стресс
k6 run tests/stress/02-health.js

# Тест auth
k6 run tests/stress/03-auth.js

# Тест receiving-gate (нужен seed)
docker exec -i postgres_db psql -U root -d wms_blockchain_db \
  < tests/stress/setup/stress-seed.sql
k6 run tests/stress/04-receiving-gate.js

# Тест полного цикла (нужен seed + env vars)
docker exec -i postgres_db psql -U root -d wms_blockchain_db \
  < tests/stress/setup/stress-seed.sql
source <(bash tests/stress/setup/generate-stress-data.sh)
k6 run -e RECEIVING_BIN_ID=$RECEIVING_BIN_ID \
       -e STORAGE_BIN_ID=$STORAGE_BIN_ID \
       -e DESTINATION_ID=$DESTINATION_ID \
       -e SHIPPING_BIN_ID=$SHIPPING_BIN_ID \
       -e DISPATCH_CODE=$DISPATCH_CODE \
       tests/stress/07-full-flow.js
```

---

## Повторный прогон (сброс данных)

```bash
# Очистить stress-данные и пересеять
docker exec -i postgres_db psql -U root -d wms_blockchain_db \
  < tests/stress/setup/stress-cleanup.sql
docker exec -i postgres_db psql -U root -d wms_blockchain_db \
  < tests/stress/setup/stress-seed.sql

# Перезапустить k6
docker compose --profile stress up -d k6
```

---

## Известные ограничения

### Статус-пул: исчерпание данных

Тесты 04, 05, 06 закрывают ресурсы (поставки, грузоместа, заказы) и не могут повторно их
обработать в рамках одного прогона. После исчерпания пула:
- Test 04: scan-ttn возвращает 409 SHIPMENT_ALREADY_CLOSED
- Test 05: scan-cargoplace возвращает 409 (грузоместо уже закрыто)
- Test 06: allocate возвращает 409/422 (нет NEW-заказов или STORED-товаров)

Эти коды объявлены «ожидаемыми» через `http.setResponseCallback`, поэтому **не влияют на**
`http_req_failed`. VU просто пропускает итерацию. Пороговые значения учитывают это поведение.

### ledger-adapter и PRIVATE_KEY

Если `PRIVATE_KEY` не задан или равен нулевому ключу, ledger-adapter не стартует. WMS при этом
показывает `/health` со статусом `degraded`. Бизнес-логика WMS (receiving, putaway, assembly,
shipping) работает полностью — события записываются в `outbox_events` и будут опубликованы в
Kafka при наличии работающего Debezium-коннектора.

Для полного end-to-end (включая блокчейн-подтверждение) задайте в `.env`:
```bash
RPC_URL=http://your-node:8545
PRIVATE_KEY=your_32_byte_hex_key
CONTRACT_ADDR=0xYourContractAddress
```

### Пулы заказов (изолированы по назначению)

Seed создаёт два независимых пула заказов:

| Пул | Код назначения | Код заказа | Тест |
|-----|----------------|------------|------|
| **300 NEW-заказов** | SHOP-7 | `STRESS-ORD-*` | 06-assembly |
| **300 NEW-заказов** | SHOP-5 | `STRESS-FLOW-ORD-*` | 07-full-flow |
| **500 STORED-товаров** | — | — | 06-assembly (pre-seed) |

Тест 06 (40 VU, ~6 мин) потребляет все SHOP-7 заказы в первые секунды, затем продолжает без заказов (возвращает 422, что ожидаемо). Тест 07 использует SHOP-5 заказы — они не конкурируют с тестом 06, и полный цикл receiving→putaway→assembly→shipping успешно завершается.

run-all.sh автоматически разрешает UUID для SHOP-5 (`DESTINATION_ID_07`, `SHIPPING_BIN_ID_07`, `DISPATCH_CODE_07`) и передаёт их в тест 07.

---

## Целевые метрики

| Метрика | Порог |
|---------|-------|
| WMS API p95 | < 250 мс (smoke) / < 1500 мс (под нагрузкой) |
| Auth login p95 | < 1500 мс (bcrypt) |
| Auth refresh p95 | < 300 мс |
| http_req_failed | < 1–5% (в зависимости от теста) |
| Receiving gate p95 | < 250 мс |
| Full flow (receiving+putaway+assembly+shipping) | p95 < 1500–500 мс по шагу |
