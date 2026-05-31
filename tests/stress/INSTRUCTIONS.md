# Нагрузочное тестирование WMS — Инструкция по запуску

## Тесты

| Файл | Описание | Executor | VUs | Итерации | Нужен seed? |
|------|----------|----------|-----|----------|-------------|
| `01-smoke.js` | Проверка работоспособности (1 VU, 1 итерация) | shared-iterations | 1 | 1 | Нет |
| `02-health.js` | Стресс GET /health, до 200 VUs | ramping-vus | 200 | ∞ | Нет |
| `03-auth.js` | Нагрузка на /auth/login + /auth/refresh | ramping-vus | 50+30 | ∞ | Нет |
| `04-receiving-gate.js` | КПП-приёмка (scan-ttn → scan-cp × N-1 → accept) | shared-iterations | 50 | 2000 | **Да** |
| `05-receiving-table.js` | Приёмка на столе (scan-cp → scan-box → … → close-cp) | shared-iterations | 60 | 2000 | **Да** |
| `06-assembly.js` | Сборка заказов (allocate → tasks → pick → ship-buffer) | ramping-vus | 40 | ∞ | **Да** |
| `07-full-flow.js` | Полный цикл receiving → putaway → assembly → shipping | shared-iterations | 30 | 2000 | **Да** |
| `08-blockchain-tps.js` | Blockchain TPS: 5000 putaway-событий → outbox → pipeline | shared-iterations | 100 | 5000 | **Да** |

---

## Запуск всех тестов одной командой (рекомендуется)

```bash
# Собрать образ k6-stress и запустить все тесты (01–08):
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
6. Генерацию JSON-файлов UUID грузомест и продуктов для SharedArray
7. Разрешение UUID ячеек/рейсов из БД
8. Последовательный запуск тестов 01–08
9. Мониторинг blockchain pipeline (+60 с после теста 08)

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
       -e DESTINATION_ID=$DESTINATION_ID_07 \
       -e SHIPPING_BIN_ID=$SHIPPING_BIN_ID_07 \
       -e "DISPATCH_CODE=$DISPATCH_CODE_07" \
       tests/stress/07-full-flow.js

# Тест blockchain TPS (нужен seed + UUID продуктов)
docker exec -i postgres_db psql -U root -d wms_blockchain_db \
  < tests/stress/setup/stress-seed.sql
# Генерация /tmp/stress-tps-products.json:
docker exec postgres_db psql -U root -d wms_blockchain_db \
  -tAc "SELECT COALESCE(json_agg(product_id::text ORDER BY qr_code),'[]') \
        FROM wms_inventory.products WHERE qr_code LIKE 'STRESS-TPS-QR-%'" \
  | tr -d '[:space:]' > /tmp/stress-tps-products.json
k6 run -e "WMS_URL=http://localhost:8080" \
       -e "RECEIVING_BIN_ID=<uuid-BUFFER-01>" \
       -e "STORAGE_BIN_ID=<uuid-A-01-01>" \
       tests/stress/08-blockchain-tps.js
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

### Closed-model (shared-iterations) vs Open-model (ramping-vus)

Тесты 04, 05, 07, 08 используют **closed-model executor** (`shared-iterations`):
- Фиксированное число итераций делится между VU; каждая итерация выполняется ровно один раз
- `exec.scenario.iterationInTest` — глобальный уникальный индекс 0…N-1, исключающий коллизии
- После обработки всех N объектов тест завершается естественно

Тесты 02, 03, 06 используют **open-model executor** (`ramping-vus`):
- Подходит для идемпотентных эндпоинтов (`/health`, `/auth`) или операций с неисчерпаемым пулом
- Число итераций неограничено; тест останавливается по таймеру

| Тест | Модель | Причина |
|------|--------|---------|
| 04-receiving-gate | closed | каждый TTN закрывается один раз |
| 05-receiving-table | closed | каждое грузоместо закрывается один раз |
| 07-full-flow | closed | каждое грузоместо + уникальный QR |
| 08-blockchain-tps | closed | каждый продукт переводится RECEIVED→STORED один раз |

### Поведение accept-shipment (тест 04)

WMS автоматически закрывает поставку (`GATE_CLOSED`), когда отсканированы **все** ожидаемые грузоместа. Поэтому тест 04 сканирует N-1 грузомест, затем вызывает `accept-shipment` явно:
- `200` — явное закрытие (последнее CP не было отсканировано)
- `409 SHIPMENT_ALREADY_CLOSED` — авто-закрытие уже произошло (1 CP в поставке)

Оба исхода означают успешное закрытие. В `check()` оба кода засчитываются как пройденные.

### scan-driver 409 в тесте 07

`POST /shipping/scan-driver` переводит рейс в `AT_GATE`. Второй вызов с тем же `dispatch_code` возвращает `409`. При 2000 итерациях и 10 кодах рейсов ~200 итераций на каждый код — первая успешная, остальные получают 409. Это ожидаемо и учтено в `http.setResponseCallback`.

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

### Blockchain TPS: двухуровневое измерение (тест 08)

```
k6 → WMS API → Postgres + outbox_events   ← синхронно (k6 измеряет event/s)
                      ↓ (асинхронно)
Debezium → Kafka → ledger-adapter → Subnet-EVM → onchain_events
                      ↑ run-all.sh измеряет COMMITTED rate через +60 с после теста
```

| Слой | Метрика | Где измеряется |
|------|---------|----------------|
| WMS event rate | `delta(outbox_events) / test_duration` | `run-all.sh` (psql) |
| Blockchain commit rate | `delta(onchain_events WHERE status='COMMITTED') / elapsed` | `run-all.sh` (psql, +60 с) |
| WMS p95 latency | `http_req_duration{step:scan_storage_bin}` p95 < 500 мс | k6 threshold |
| Успешность | `outbox_events_generated` count ≥ 4750 (≥95% из 5000) | k6 Counter threshold |

Теоретический потолок blockchain pipeline: `batch_size=10`, `batch_timeout=100 мс` → ~100 батчей/с → ~1000 событий/с при одном подписанте.

### Пулы одноразовых данных (seed)

| Пул данных | Паттерн | Количество | Тест |
|------------|---------|-----------|------|
| TTN поставки | `STRESS-TTN-*` | 2000 | 04 |
| КПП-грузоместа | `STRESS-CP-*` | 6000 (3 на TTN) | 04 |
| Стол-грузоместа | `STRESS-TABLE-CP-*` | 2000 | 05 |
| FLOW-грузоместа | `STRESS-FLOW-CP-*` | 2000 | 07 |
| STORED-продукты (SHOP-7) | `STRESS-SKU-*` | 1000 продуктов | 06 |
| NEW-заказы SHOP-7 | `STRESS-ORD-*` | 1000 | 06 |
| NEW-заказы SHOP-5 | `STRESS-FLOW-ORD-*` | 1000 | 07 |
| RECEIVED-продукты (TPS) | `STRESS-TPS-QR-*` | 5000 | 08 |

Все паттерны `STRESS-*` покрываются `stress-cleanup.sql` — очистка полностью сбрасывает пулы.

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
| Putaway scan-storage-bin p95 (TPS) | < 500 мс |
| `outbox_events_generated` (тест 08) | count ≥ 4750 (≥95% из 5000) |
| WMS event rate (тест 08) | > 500 событий/с |
| Blockchain commit rate (после теста 08, +60 с) | определяется конфигом ledger-adapter |
