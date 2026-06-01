# API-контракт WMS

**Версия:** 2.0
**Дата:** 2026-05-31
**Базовый URL:** `http://localhost:8081`

Краткий человекочитаемый компаньон к машинной спецификации. **Источник истины —**
[`openapi.yaml`](openapi.yaml) (OpenAPI 3.1, написан вручную по коду
`RegisterRoutes`/`main.go`). Интерактивный просмотр — [`swagger-ui.html`](swagger-ui.html).

> Этот файл переписан с нуля: предыдущая версия содержала несуществующие эндпоинты
> (`GET /shipping/orders`, `POST /shipping/verify`), неверный базовый URL
> (`:8080/api/v1`) и устаревшие поля (`assembly_task_id`). Сверяйтесь только с
> `openapi.yaml`.

---

## Как пользоваться

| Артефакт | Назначение |
|----------|-----------|
| [`openapi.yaml`](openapi.yaml) | Полная машинная спецификация: тела, схемы, ошибки, примеры. Импортируйте в codegen/Postman. |
| [`swagger-ui.html`](swagger-ui.html) | Интерактивный UI. Открыть напрямую или раздать `python3 -m http.server 8088` из `docs/api/`. |
| Этот файл | Обзорная таблица эндпоинтов + ключевые соглашения. |

---

## Три формата ответов (не путать)

В системе сосуществуют **три разных формата** — это граница между модулями, а не баг.
Клиент обязан разбирать все три.

### 1. Бизнес-модули — JSON-конверт

`receiving`, `putaway`, `assembly`, `shipping`, `dispatches`. `Content-Type: application/json`.

```json
// Успех
{ "success": true, "data": { /* ... */ }, "error": null }

// Ошибка
{ "success": false, "data": null, "error": { "code": "INVALID_REQUEST", "message": "Невалидный JSON в теле запроса" } }
```

`code` — машинный ASCII, `message` — русский текст. HTTP-коды: `200` успех,
`400` валидация, `401` нет/невалидный JWT, `403` роль не разрешена, `404` не найдено,
`409` конфликт состояния, `500` внутренняя (а в `dispatches` — `503`).

### 2. Модуль `auth` — простой текст

`/auth/login`, `/auth/refresh`, `/auth/register`. Ошибки идут через `http.Error` —
`Content-Type: text/plain`, тело — голая строка с завершающим `\n`.

```text
unauthorized
```

| Статус | Тело |
|--------|------|
| 400 | `invalid request body` |
| 401 | `unauthorized` |
| 403 | `forbidden` |
| 409 | `user already exists` (только register) |
| 500 | `internal server error` |

Успешные ответы `auth` — **сырой JSON без конверта** (`{access_token, refresh_token}`
или `{id, username}`).

### 3. `GET /health` — голый JSON-объект

Ни конверт, ни текст. `{ "status": "ok"|"degraded", "checks": {...}, "time": "..." }`.
`200` при `ok`, `503` при `degraded`.

---

## Аутентификация и RBAC

- Заголовок: `Authorization: Bearer <access_token>` (JWT HS256, claims `user_id`,
  `role`, `type="access"`, `iat`, `exp`).
- Публичные (без токена): `POST /auth/login`, `POST /auth/refresh`, `GET /health`.
- Все остальные требуют валидный JWT (`auth.Middleware` на subrouter'ах).
- RBAC-гейты поверх JWT:

| Гейт | Кто проходит | Где |
|------|--------------|-----|
| ADMIN-only | только `ADMIN` | `POST /auth/register` |
| `RequireAdminOrOperator` | `ADMIN` или `OPERATOR` | `POST /assembly/allocate` (issue #39) |
| `RequireOperator` | только `OPERATOR` | все остальные защищённые эндпоинты |

`ADMIN`, вызывающий OPERATOR-only эндпоинт, получает `403`. Сообщения 401/403 в
бизнес-модулях — на русском (`Требуется авторизация`, `Только оператор может выполнять
это действие`).

---

## Строгий разбор тела

Все тела декодируются с `json.Decoder.DisallowUnknownFields()`. **Любое лишнее поле в
теле → `400`.** В `openapi.yaml` каждая схема запроса помечена
`additionalProperties: false`.

---

## Сводка эндпоинтов

Всего **28 операций** на 27 путях (на `/dispatches/` висят GET и POST). RBAC указан
после auth-гейта. Полные тела, ошибки и примеры — в [`openapi.yaml`](openapi.yaml).

### System

| Метод | Путь | RBAC | Назначение |
|-------|------|------|-----------|
| GET | `/health` | — (публичный) | Health-check (postgres/kafka/ledger). Особый формат, `200`/`503`. |

### Auth (ошибки — простой текст)

| Метод | Путь | RBAC | Назначение |
|-------|------|------|-----------|
| POST | `/auth/login` | — (публичный) | Вход → пара JWT (сырой JSON). |
| POST | `/auth/refresh` | — (публичный) | Обновление access по refresh (сырой JSON). |
| POST | `/auth/register` | Bearer + **ADMIN** | Создать пользователя. `201`, тело `{id, username}`. |

### Receiving — КПП (gate)

| Метод | Путь | RBAC | Назначение |
|-------|------|------|-----------|
| POST | `/receiving/gate/scan-ttn` | OPERATOR | Начать приёмку по ТТН → `GATE_IN_PROGRESS`. |
| POST | `/receiving/gate/scan-cargoplace` | OPERATOR | Грузоместо → `RECEIVED_AT_GATE` (+ авто-закрытие). |
| POST | `/receiving/gate/accept-shipment` | OPERATOR | Закрыть КПП → `GATE_CLOSED`, остальное `NOT_RECEIVED`. |

### Receiving — стол (table)

| Метод | Путь | RBAC | Назначение |
|-------|------|------|-----------|
| POST | `/receiving/table/scan-cargoplace` | OPERATOR | Грузоместо → `TABLE_IN_PROGRESS`, манифест SKU. |
| POST | `/receiving/table/scan-box` | OPERATOR | Upsert коробки. |
| POST | `/receiving/table/scan-sku` | OPERATOR | Резолв SKU по штрихкоду (товар не создаётся). |
| POST | `/receiving/table/scan-qr` | OPERATOR | Создать товар (`RECEIVED`) с уникальным QR. |
| POST | `/receiving/table/close-box` | OPERATOR | Коробка → `CLOSED`. |
| POST | `/receiving/table/scan-buffer` | OPERATOR | Товары из `CLOSED` коробок → буфер. |
| POST | `/receiving/table/close-cargoplace` | OPERATOR | Грузоместо → `TABLE_CLOSED` + **outbox** (`receiving`). |

### Putaway

| Метод | Путь | RBAC | Назначение |
|-------|------|------|-----------|
| POST | `/putaway/scan-buffer` | OPERATOR | Получить `RECEIVED` товары из буфера (read-only). |
| POST | `/putaway/scan-product` | OPERATOR | Проверить товар + `cart_size` (read-only). |
| POST | `/putaway/scan-storage-bin` | OPERATOR | Разместить (`STORED`) + **outbox** (`putaway`). Chain-gate. |

### Assembly

| Метод | Путь | RBAC | Назначение |
|-------|------|------|-----------|
| POST | `/assembly/allocate` | **ADMIN или OPERATOR** | Аллокация на заказы; недостача → `insufficient_orders` (всё ещё `200`). |
| GET | `/assembly/tasks` | OPERATOR | Список задач сборки (query: `destination_id`*, `operator_id`, `status`). |
| POST | `/assembly/pick` | OPERATOR | Пик: товар → `ASSEMBLED`, задача → `DONE`, **outbox** (`picking`). Chain-gate. |
| POST | `/assembly/scan-shipping-buffer` | OPERATOR | Собранное → `READY_TO_SHIP` в буфер отгрузки. |

### Dispatches (внутренние ошибки → `503`)

| Метод | Путь | RBAC | Назначение |
|-------|------|------|-----------|
| GET | `/dispatches/` | OPERATOR | Список рейсов (**слэш обязателен**; `data` может быть `null`). |
| POST | `/dispatches/` | OPERATOR | Создать рейс. **`200` (не `201`)**, `data.dispatch`. |
| GET | `/dispatches/{dispatch_id}` | OPERATOR | Рейс по ID; `data.dispatch`. |

### Destinations

| Метод | Путь | RBAC | Назначение |
|-------|------|------|-----------|
| GET | `/destinations` | OPERATOR | Справочник магазинов-получателей (read-only; query: `warehouse_id` опц., `int64 > 0`). Работают `/destinations` и `/destinations/`. |

### Shipping

| Метод | Путь | RBAC | Назначение |
|-------|------|------|-----------|
| POST | `/shipping/scan-buffer` | OPERATOR | Осмотр буфера отгрузки (read-only). |
| POST | `/shipping/scan-driver` | OPERATOR | Рейс → `AT_GATE` (идемпотентно). |
| POST | `/shipping/ship` | OPERATOR | Отгрузка (bulk/spot) + **outbox** (`shipping`); рейс → `DEPARTED`. |

\* `destination_id` — обязательный query-параметр.

---

## Подводные камни (закреплено кодом и e2e)

- **`/dispatches/` — завершающий слэш обязателен.** gorilla/mux не редиректит;
  `GET /dispatches` (без слэша) не матчится.
- **`POST /dispatches/` возвращает `200`, а не `201`**, и оборачивает результат в
  `data.dispatch`. У `GET /dispatches/{id}` — та же обёртка.
- **`/dispatches` 404 → `message` это строка-сентинел** (`DESTINATION_NOT_FOUND` /
  `DISPATCH_NOT_FOUND`), а не фраза. `code` при этом `NOT_FOUND`.
- **`/putaway/scan-storage-bin`: BUFFER/SHIPPING_BUFFER-ячейка → `404
  STORAGE_BIN_NOT_FOUND`** (не 400). Контринтуитивно.
- **`/assembly/allocate` никогда не отдаёт ошибку при нехватке остатка** — частичная
  аллокация это `200` с заполненным `insufficient_orders`.
- **`product_ids` — это JSON-массив** в `/putaway/scan-storage-bin` и `/shipping/ship`
  (максимум 200 элементов). В `ship` пустой/отсутствующий → bulk-режим.
- **`/shipping/ship` `buffer_remaining` считается по всей SHIPPING_BUFFER-зоне пункта
  назначения**, не по одной ячейке. Рейс отбывает только когда зона пуста.
- **`progress.not_received` всегда `0`** в ответе `gate/scan-cargoplace`; симметрично
  `summary.remaining` всегда `0` в `gate/accept-shipment`.
- **Только `close-cargoplace`, `scan-storage-bin`, `pick`, `ship` пишут в
  `public.outbox_events`** — это точки интеграции с блокчейном. `aggregate_type` для
  стадии сборки — **`picking`**, не `assembly`.

---

## Outbox / блокчейн (сводка)

| Эндпоинт | `aggregate_type` | `event_type` | Переход товара |
|----------|------------------|--------------|----------------|
| `receiving/table/close-cargoplace` | `receiving` | `wms.receiving.v1` | товары созданы со статусом `RECEIVED` |
| `putaway/scan-storage-bin` | `putaway` | `wms.putaway.v1` | `RECEIVED → STORED` |
| `assembly/pick` | `picking` | `wms.picking.v1` | `ALLOCATED → ASSEMBLED` |
| `shipping/ship` | `shipping` | `wms.shipping.v1` | `READY_TO_SHIP → SHIPPED` |

`aggregate_id` всегда `= product_id` (1 событие = 1 товар). Детали маршрута событий —
[`../integration/blockchain-mapping.md`](../integration/blockchain-mapping.md),
[`../integration/data-contract.md`](../integration/data-contract.md),
[`../integration/batch-mapping-approach.md`](../integration/batch-mapping-approach.md).

---

## Связанная документация

- **Бизнес-процесс:** [сквозной путь товара](../business-process/end-to-end-flow.md),
  [диаграммы потоков данных](../business-process/data-flow-diagrams.md)
- **Flow по этапам:** [КПП](../flows/receiving-gate-flow.md),
  [стол](../flows/receiving-table-flow.md), [раскладка](../flows/putaway-flow.md),
  [сборка](../flows/assembly-flow.md), [отгрузка](../flows/shipping-flow.md)
- **Модель данных:** [жизненные циклы сущностей](../data-model/entity-lifecycle.md),
  [ER-диаграмма](../data-model/er-diagram.md), [схема БД](../db/Database_ru_v2.md)
- **Архитектура и процесс:** [обзор системы](../architecture/system-overview.md),
  [конвенции](../CONVENTIONS.md), [гайд по MR](../MR_GUIDE.md)
