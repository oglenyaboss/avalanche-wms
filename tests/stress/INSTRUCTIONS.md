# Нагрузочное тестирование WMS — Инструкция по запуску

## Что сделано

Тесты написаны на **k6** (grafana/k6). Файлы размещены в `tests/stress/`.

| Файл | Описание | Нужен seed? |
|------|----------|-------------|
| `01-smoke.js` | Проверка работоспособности (1 VU, 1 итерация) | Нет |
| `02-health.js` | Стресс GET /health, до 500 VUs | Нет |
| `03-auth.js` | Нагрузка на /auth/login + /auth/refresh | Нет |
| `04-receiving-gate.js` | КПП-приёмка (scan-ttn → scan-cp × N → accept) | **Да** |
| `05-receiving-table.js` | Приёмка на столе (scan-cp → scan-box → … → close-cp) | **Да** |
| `06-assembly.js` | Сборка заказов (allocate → tasks → pick → ship-buffer) | **Да** |
| `07-full-flow.js` | Полный цикл receiving → putaway → assembly → shipping | **Да** |

---

## Что нужно исправить / добавить

### 1. Установить k6

k6 — отдельный бинарник, не входит в зависимости проекта.

**Mac (Homebrew):**
```bash
brew install k6
```

**Linux:**
```bash
sudo gpg --no-default-keyring --keyring /usr/share/keyrings/k6-archive-keyring.gpg \
  --keyserver hkp://keyserver.ubuntu.com:80 --recv-keys C5AD17C747E3415A3642D57D77C6C491D6AC1D69
echo "deb [signed-by=/usr/share/keyrings/k6-archive-keyring.gpg] https://dl.k6.io/deb stable main" \
  | sudo tee /etc/apt/sources.list.d/k6.list
sudo apt-get update && sudo apt-get install k6
```

**Docker (альтернатива):**
```bash
docker run --rm -i --network blockchain_project_app_network \
  -e WMS_URL=http://wms-service:8080 \
  grafana/k6:latest run - < tests/stress/01-smoke.js
```

---

### 2. Добавить `admin`-пользователя в seed.sql

Тест `03-auth.js` и хелпер `operatorToken()` в e2e используют учётную запись `admin`.
В `deploy/seed.sql` пользователь `admin` **отсутствует**.

**Добавить в `deploy/seed.sql`** (после строки с `customer`):
```sql
INSERT INTO public.users (user_id, username, password_hash, role, is_active, created_at, updated_at)
VALUES (
  gen_random_uuid(),
  'admin',
  crypt('admin', gen_salt('bf')),
  'ADMIN',
  true,
  now(),
  now()
)
ON CONFLICT (username) DO NOTHING;
```

Или выполнить вручную на работающей БД:
```bash
docker exec postgres_db psql -U root -d wms_blockchain_db -c "
INSERT INTO public.users (user_id, username, password_hash, role, is_active, created_at, updated_at)
VALUES (gen_random_uuid(), 'admin', crypt('admin', gen_salt('bf')), 'ADMIN', true, now(), now())
ON CONFLICT (username) DO NOTHING;
"
```

---

### 3. Засеять тестовые данные (для тестов 04–07)

```bash
# Убедиться, что стек запущен
docker compose ps

# Выполнить seed-скрипт
docker exec -i postgres_db psql -U root -d wms_blockchain_db \
  < tests/stress/setup/stress-seed.sql
```

**Перед повторным прогоном** (сброс состояния):
```bash
docker exec -i postgres_db psql -U root -d wms_blockchain_db \
  < tests/stress/setup/stress-cleanup.sql

docker exec -i postgres_db psql -U root -d wms_blockchain_db \
  < tests/stress/setup/stress-seed.sql
```

---

### 4. Получить UUID ячеек и рейсов (для тестов 06–07)

```bash
# Генерирует строки `export VAR=uuid` и выводит их в stdout
source <(bash tests/stress/setup/generate-stress-data.sh)

# Проверка:
echo "DESTINATION_ID=$DESTINATION_ID"
echo "DISPATCH_CODE=$DISPATCH_CODE"
```

Скрипт требует локальной установки `psql` (клиент PostgreSQL, порт 5432 должен быть проброшен).
Альтернатива без psql на хосте:
```bash
docker exec postgres_db psql -U root -d wms_blockchain_db \
  -tAc "SELECT b.bin_id FROM wms_inventory.bins b
        JOIN wms_inventory.warehouses w ON w.warehouse_id=b.warehouse_id
        WHERE b.code='BUFFER-01' AND w.name='Склад Москва-Север'"
```

---

### 5. Добавить stress-профиль в docker-compose.yaml

Чтобы k6 мог работать внутри Docker-сети (обращаться к `wms-service:8080` вместо `localhost:8081`), нужно добавить сервис в `docker-compose.yaml`:

```yaml
  # ====== stress profile ======
  k6:
    image: grafana/k6:latest
    profiles: [stress]
    environment:
      WMS_URL: http://wms-service:8080
    volumes:
      - ./tests/stress:/tests/stress:ro
    command: run /tests/stress/01-smoke.js
    networks:
      - app_network
    depends_on:
      wms_app:
        condition: service_healthy
```

Запуск конкретного теста:
```bash
# Рекомендуется запускать с --entrypoint переопределением
docker compose --profile stress run --rm k6 \
  run -e WMS_URL=http://wms-service:8080 /tests/stress/02-health.js
```

---

### 6. Добавить make-цели в Makefile

```makefile
# ── Stress tests ─────────────────────────────────────────────────────
stress-smoke: ## Smoke test (1 VU)
	k6 run tests/stress/01-smoke.js

stress-health: ## Health check load (up to 200 VUs)
	k6 run tests/stress/02-health.js

stress-auth: ## Auth endpoints load test
	k6 run tests/stress/03-auth.js

stress-receiving: ## Receiving gate flow (needs stress-seed.sql)
	k6 run tests/stress/04-receiving-gate.js

stress-full: ## Full outbound flow (needs seed + env vars)
	@source <(bash tests/stress/setup/generate-stress-data.sh) && \
	  k6 run tests/stress/07-full-flow.js
```

---

## Запуск всех тестов одной командой (рекомендуется)

```bash
# Собрать образ k6-stress и запустить все тесты (01–07) в фоне:
docker compose --profile stress up -d

# — или через make:
make stress
```

Команда автоматически:
- Собирает образ `k6-stress` (grafana/k6 + postgresql-client)
- Запускает весь стек, если он ещё не поднят
- Ждёт готовности WMS (healthcheck) и PostgreSQL
- Очищает и засевает stress-данные
- Разрешает UUID ячеек/рейсов из БД
- Запускает тесты 01–07 последовательно

Наблюдать за прогрессом:
```bash
docker compose logs -f k6
# — или:
make stress-logs
```

---

## Запуск отдельных тестов (локальная установка k6)

```bash
# 1. Быстрая проверка (не нужен seed)
k6 run tests/stress/01-smoke.js

# 2. Нагрузка на health (не нужен seed)
k6 run tests/stress/02-health.js

# 3. Нагрузка на auth (не нужен seed)
k6 run tests/stress/03-auth.js

# 4. Приёмка на КПП (нужен stress-seed.sql)
k6 run tests/stress/04-receiving-gate.js

# 5. Приёмка на столе (нужен stress-seed.sql)
k6 run tests/stress/05-receiving-table.js

# 6. Сборка заказов (нужен seed + env vars)
source <(bash tests/stress/setup/generate-stress-data.sh)
k6 run tests/stress/06-assembly.js

# 7. Полный цикл (нужен seed + env vars)
source <(bash tests/stress/setup/generate-stress-data.sh)
k6 run tests/stress/07-full-flow.js
```

Кастомный URL (если WMS не на localhost:8081):
```bash
WMS_URL=http://your-host:8081 k6 run tests/stress/01-smoke.js
```

---

## Известные проблемы и исправления

### Исправлено: 04-receiving-gate.js — 0% успеха scan-cargoplace

**Симптом:** scan-ttn возвращает 200, но все вызовы scan-cargoplace завершаются с ошибкой
(`http_req_failed rate ≈ 80%`).

**Причина:** Сервис `receiving.Service.ScanCargoplace` требует, чтобы поставка была в статусе
`GATE_IN_PROGRESS`. Метод `ScanTTN` переводит поставку из `CREATED → GATE_IN_PROGRESS`.
Изначально `stress-seed.sql` создавал поставки со статусом `'EXPECTED'`, который сервис не
распознаёт как допустимый стартовый статус — переход не происходил, поставка оставалась в
`'EXPECTED'`, и scan-cargoplace всегда возвращал `SHIPMENT_NOT_IN_PROGRESS`.

**Исправление:** В `tests/stress/setup/stress-seed.sql` статус inbound_shipments изменён
с `'EXPECTED'` на `'CREATED'`. Грузоместа по-прежнему создаются со статусом `'EXPECTED'`
(это правильно, именно его ожидает `UpdateCargoplaceReceivedAtGate` при переходе в
`RECEIVED_AT_GATE`).

**После применения исправления:**
```bash
# Сбросить и пересеять данные
docker exec -i postgres_db psql -U root -d wms_blockchain_db \
  < tests/stress/setup/stress-cleanup.sql
docker exec -i postgres_db psql -U root -d wms_blockchain_db \
  < tests/stress/setup/stress-seed.sql
# Запустить тест
k6 run tests/stress/04-receiving-gate.js
```

---

## Известные ограничения тестов

### 05-receiving-table.js и 07-full-flow.js
- Тест передаёт `cargoplace_id` как UUID грузоместа, который должен быть разрешён из БД.
- `setup/stress-seed.sql` создаёт грузоместа с кодами `STRESS-TABLE-CP-XXXX`, но UUID
  (нужный для API) не известен заранее.
- **Решение:** добавить в `setup()` запрос к БД через HTTP (если появится inventory-ресурс)
  или заранее сгенерировать файл `data/stress-table-cargoplaces.json` скриптом:

```bash
docker exec postgres_db psql -U root -d wms_blockchain_db \
  -tAc "SELECT json_agg(json_build_object('code', c.cargoplace_code, 'id', c.cargoplace_id))
        FROM wms_inventory.cargoplaces c
        WHERE c.cargoplace_code LIKE 'STRESS-TABLE-CP-%'
        ORDER BY c.cargoplace_code" \
  > tests/stress/data/stress-table-cargoplaces.json
```

Затем в `05-receiving-table.js` использовать:
```javascript
import { SharedArray } from 'k6/data';
const cargoplaces = new SharedArray('cargoplaces', () =>
  JSON.parse(open('../data/stress-table-cargoplaces.json')));
```

### 06-assembly.js
- Требует установки `DESTINATION_ID` и `SHIPPING_BIN_ID` через env vars (см. пункт 4).
- Каждая итерация `allocate` потребляет один NEW-заказ. Seed создаёт 100 заказов.
  При высокой нагрузке они закончатся — VUs начнут получать 422/409 и пропускать итерацию.
  Допустимый `http_req_failed: rate<0.20` учитывает это.
- Перед повторным прогоном запустить `stress-cleanup.sql` + `stress-seed.sql`.

### 07-full-flow.js
- Полный цикл включает блокчейн-события (close-cargoplace, scan-storage-bin, ship).
  Под нагрузкой Ledger Adapter и Kafka могут стать узким местом — это и есть цель теста.
- Тест **не ждёт** on-chain подтверждения (оно асинхронно). Для проверки
  on-chain статуса используйте e2e-сценарии (01–11 в `tests/e2e/scenarios/`).

---

## Целевые метрики (из docs/architecture/system-overview.md)

| Метрика | Цель |
|---------|------|
| WMS API p95 | < 250 мс |
| Auth p95 | < 1500 мс (bcrypt) |
| Пропускная способность | 1500 req/s |
| Error rate | < 1% |
