# Нагрузочное тестирование WMS → Блокчейн

Каталог содержит **два слоя** нагрузочного тестирования:

1. **Функциональный набор `01`–`08` + `run-all.sh`** (`make stress`) — пороговые
   проверки отдельных эндпоинтов и стадий. Запуск, сиды, пороги и известные
   ограничения — в разделе [«Функциональный набор `01`–`08`»](#функциональный-набор-0108) ниже.
2. **Сквозной throughput/profiling-харнесс** (`09-throughput.js` + shell-обёртки) —
   измеряет реальную пропускную способность всего пути и где узкое место.

> Все обёртки делают `cd "$(git rev-parse --show-toplevel)"` в начале, поэтому их
> можно запускать из любого каталога — путь к корню репозитория/worktree вычисляется
> автоматически. Стек ожидается под `COMPOSE_PROJECT_NAME=stresstest`.

---

## Что измеряем

| Величина | Смысл |
|---|---|
| **committed переходов/с** (главная единица) | подтверждённые в блокчейне переходы состояния (`onchain_events.status='COMMITTED'`). Цель — **≥1500/с**. Итог берётся как **sustained** (стационарное окно, минус 20с прогрева), а не peak. |
| backlog | `outbox_events` без строки в `onchain_events` — не убегает ли очередь |
| CPU-split | потребление CPU по каждому контейнеру → где узкое место |
| baseFee / gasUsedRatio | не входит ли цепь в ценовое удушение (должен держаться у пола 25 Gwei) |
| WMS↔chain consistency + on-chain TRACE | `shipped(wms) == shipping COMMITTED(chain)`, реальные `ItemTransition` (не skip/Failed) |

Один товар = **4** перехода (receiving → putaway → picking → shipping), каждый пишется в цепь
отдельным событием. Подробная методика и результаты — в
[`../../docs/load-testing/stress-load-tests-full-report.md`](../../docs/load-testing/stress-load-tests-full-report.md).

---

## Основной сценарий — `09-throughput.js`

Сквозной bench всего проекта (receive → putaway → pick → ship → on-chain) во весь опор.
Снимает искусственные тормоза теста 07 двумя приёмами де-контенции:

- **разнос по магазинам** — заказы размазаны по 70 точкам (`N_DEST`) → лок `allocate` не глобальный;
- **планировщик-каденс** — `allocate`/`ship` зовутся не каждым оператором, а раз в
  `ALLOC_EVERY`/`SHIP_EVERY` итераций round-robin (как планирование волны).

Каждый VU = свой оператор (изоляция корзины). Два режима: `shared-iterations` (во весь
опор = потолок) и `constant-arrival-rate` (`PACE_RPS>0` → ровный backlog = честный sustained).

Ключевые env: `VUS`, `ITERS`, `SKU_COUNT`, `PRODUCTS_PER_CP`, `ALLOC_EVERY`, `SHIP_EVERY`,
`PICK_BATCH`, `THINK_MS`, `PACE_RPS`, `RECEIVING_BIN_ID`, `STORAGE_BIN_ID`. Требует
fixtures в `/tmp` — их готовят обёртки (ниже).

---

## Обёртки (`tests/stress/*.sh`)

| Скрипт | Назначение |
|---|---|
| **`profile-e2e-cpu.sh`** | **канон.** Сквозной committed/backlog + **per-container CPU-split**; окна sustained / лучшее-30с / peak. Кнобы: `N_ITEMS`, `SKU_COUNT`, `PER_CP`, `SHIP_EVERY`, `PACE_RPS`, `CPU_SAMPLE`. |
| `run-throughput.sh` | Сквозной bench 09 с тюном pool/adapter, фоновым DB-семплером наклона, дренажом back-half, consistency + on-chain trace (без CPU-split). |
| `profile-front.sh` | Паузит цепь (`docker pause`) → чистый фронт-потолок WMS (как в проде, где фронт и цепь на разных хостах). |
| `profile-e2e.sh` | Committed end-to-end, цепь ON: держит ли весь конвейер ~1500/с с плоским backlog. |
| `bench08.sh` / `bench-sustained.sh` | Блокчейн-слой изолированно: стоп adapter → набить Kafka N×5000 событий → дренаж при `BATCH_SIZE` → наклон = sustained chain-rate. |
| `reset-chain.sh` | Пересоздать **только** Subnet-EVM с genesis `targetGas=2B`, сохранив postgres (тюнинг+данные) и kafka. |
| `basefee-sampler.sh` | Лог baseFee + gasUsedRatio каждые 4с в `/tmp/basefee.log` (доказывает fee-market фикс). |

---

## Как воспроизвести заголовок (150k → ~1924/с sustained)

```bash
# 0. STRESS-тюнинг postgres — ⚠️ ОБЯЗАТЕЛЬНО на свежем стеке (см. tune-ловушку ниже)
COMPOSE_PROJECT_NAME=stresstest ./tests/stress/setup/apply-postgres-tune.sh

# 1. поднять WMS с увеличенным пулом — ⚠️ ОБЯЗАТЕЛЬНО (см. ловушку ниже)
WMS_DB_MAX_CONNS=30 docker compose up -d wms_app

# 2. сквозной прогон на 150k (скрипт сам поднимет adapter batch=900/window=8,
#    засидит, прогонит k6, снимет committed-rate + CPU-split)
N_ITEMS=150000 ./tests/stress/profile-e2e-cpu.sh 70
```

> ⚠️ **Tune-ловушка (важнее pool-ловушки).** Заголовок 1924/с зависит от OLTP-тюнинга postgres
> (`synchronous_commit=off` + буферы), который **жил только в volume** (ALTER SYSTEM) и **не был в
> репозитории**. `docker compose down -v` его стирает → свежий postgres стартует на дефолтах
> (`synchronous_commit=on`) → commit-путь адаптера (масса мелких InsertPending) становится
> fsync-bound → e2e падает до ~**455/с** (цепь голодает, 1 батч/блок), а фронт (булк-инсерты)
> не страдает — отсюда парадокс «pool-30 медленнее pool-10». Лечится `apply-postgres-tune.sh`
> (см. [`setup/postgres-tune.sql`](setup/postgres-tune.sql)). Проверка:
> `docker exec postgres_db psql -U root -d wms_blockchain_db -tAc "SHOW synchronous_commit"` → `off`.

> ⚠️ **Pool-ловушка.** `profile-e2e-cpu.sh` поднимает **только** `ledger-adapter`, а `wms_app`
> не трогает. Если не поднять WMS с `WMS_DB_MAX_CONNS=30` вручную, он стартует с дефолтным
> пулом **10** → Postgres простаивает → получите ~**1439/с** вместо 1924. Пул в 30 — это
> «главный рычаг масштаба» из отчёта. Проверка: `docker exec wms-service env | grep WMS_DB_MAX_CONNS`.

Предпосылки: `k6` на хосте, `foundry`/`cast` (для on-chain trace), стек `stresstest`
поднят, применён `setup/stress-throughput-seed.sql` (обёртки делают это сами).

---

## Последний результат (2026-06-06, пересобранные образы, 150k)

**sustained 1946 переходов/с**, 0 FAILED, backlog→~0, baseFee приклеен к 25 Gwei.
CPU: PostgreSQL 6.0 ядра (узкое место), блокчейн 1.6 ядра, бокс насыщен 10/10.
Подробности — `docs/load-testing/stress-load-tests-full-report.md` §10.6.

---

## Функциональный набор (`01`–`08`)

Пороговый набор: каждый тест бьёт отдельный эндпоинт/стадию и проверяет латентность и
успешность под нагрузкой. Прогоняется целиком через `make stress` (см. ниже).

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

### Запуск всех тестов одной командой (рекомендуется)

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

### Запуск отдельных тестов (локальная установка k6)

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

### Повторный прогон (сброс данных)

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

Теоретический потолок blockchain pipeline при дефолте: `batch_size=10`, `batch_timeout=100 мс`
→ ~100 батчей/с → ~1000 событий/с при одном подписанте. Конвейер (`PIPELINE_WINDOW`,
больший `BATCH_SIZE`) снимает этот серийный потолок — см. throughput-харнесс выше.

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

## Целевые метрики (функциональный набор)

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
