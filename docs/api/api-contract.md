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
| ADMIN-only | только `ADMIN` | `POST /auth/register` (проверка роли внутри сервиса) |
| `RequireAdmin` | только `ADMIN` | `GET /passkeys/organization`, `GET /passkeys/organization/admin-key`, `POST /passkeys/organization/keys/reject`, `POST /passkeys/actions/*` |
| `RequireAdminOrOperator` | `ADMIN` или `OPERATOR` | `POST /assembly/allocate` (issue #39), `GET /passkeys/me`, `POST /passkeys/registration/*`, `GET /signed-actions/operations/{operation_id}`, `/analytics/*`, `/products/*` |
| `RequireAuditorOrAdmin` | `AUDITOR` или `ADMIN` | `GET /audit/products/{key}` |
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

Всего **48 операций** на 47 путях (на `/dispatches/` висят GET и POST). RBAC указан
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
| POST | `/receiving/table/close-cargoplace/draft` | OPERATOR | Черновик Receive-действия: части по ≤2000 товаров с `challenge` для passkey. |
| POST | `/receiving/table/close-cargoplace/submit` | OPERATOR | Подписи частей → `TABLE_CLOSED` + **по строке `outbox_events` на часть**. |

### Putaway

| Метод | Путь | RBAC | Назначение |
|-------|------|------|-----------|
| POST | `/putaway/scan-buffer` | OPERATOR | Получить `RECEIVED` товары из буфера (read-only). |
| POST | `/putaway/scan-product` | OPERATOR | Проверить товар + `cart_size` (read-only). |
| POST | `/putaway/scan-storage-bin/draft` | OPERATOR | Черновик PutAway-действия (`STORED` ещё не пишется). Chain-gate. |
| POST | `/putaway/scan-storage-bin/submit` | OPERATOR | Подписи частей → `STORED` + **по строке `outbox_events` на часть**. Chain-gate. |

### Assembly

| Метод | Путь | RBAC | Назначение |
|-------|------|------|-----------|
| POST | `/assembly/allocate` | **ADMIN или OPERATOR** | Аллокация на заказы; недостача → `insufficient_orders` (всё ещё `200`). |
| GET | `/assembly/tasks` | OPERATOR | Список задач сборки (query: `destination_id`*, `operator_id`, `status`). |
| POST | `/assembly/pick` | OPERATOR | Пик: товар → `ASSEMBLED`, задача → `DONE`. **Outbox не пишется.** Chain-gate. |
| POST | `/assembly/scan-shipping-buffer/draft` | OPERATOR | Черновик Pick-действия: корзина → части с `challenge`. |
| POST | `/assembly/scan-shipping-buffer/submit` | OPERATOR | Подписи частей → `READY_TO_SHIP` в буфер + **по строке `outbox_events` на часть**. |

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
| POST | `/shipping/ship/draft` | OPERATOR | Черновик Ship-действия: буфер/spot → части с `challenge`. Гейт `PICKING_NOT_CONFIRMED`. |
| POST | `/shipping/ship/submit` | OPERATOR | Подписи частей → `SHIPPED` + **по строке `outbox_events` на часть**; рейс → `DEPARTED`. |

### Passkeys (открытый ключ P-256 вместо пароля для подписи действий)

| Метод | Путь | RBAC | Назначение |
|-------|------|------|-----------|
| POST | `/passkeys/registration/begin` | ADMIN или OPERATOR | Опции WebAuthn-регистрации (`protocol.CredentialCreation`, JSON `publicKey`); у пользователя не больше одного живого ключа. |
| POST | `/passkeys/registration/finish` | ADMIN или OPERATOR | Attestation → строка `passkey_credentials` в `PENDING`. |
| GET | `/passkeys/me` | ADMIN или OPERATOR | Свой passkey (статус подтягивается из реестра цепи). |
| GET | `/passkeys/organization` | **ADMIN** | Обзор организации: ключи сотрудников и их статусы. |
| GET | `/passkeys/organization/admin-key` | **ADMIN** | Экспорт ключа админа для регистрации в `ConsortiumRegistry`. |
| POST | `/passkeys/organization/keys/reject` | **ADMIN** | Отклонить `PENDING`-регистрацию passkey (`{target_key_id}` → `REJECTED`). |
| POST | `/passkeys/actions/key-register/draft` | **ADMIN** | Черновик `KeyRegister` (action_type 5) для ключа оператора своей организации. |
| POST | `/passkeys/actions/key-revoke/draft` | **ADMIN** | Черновик `KeyRevoke` (action_type 6) для `ACTIVE`-ключа оператора. |
| POST | `/passkeys/actions/{action_id}/submit` | **ADMIN** | Подпись админа → `submitted_at` + **строка `outbox_events`** на ключевое действие. |

### Signed actions (состояние подписанной операции в цепи)

| Метод | Путь | RBAC | Назначение |
|-------|------|------|-----------|
| GET | `/signed-actions/operations/{operation_id}` | ADMIN или OPERATOR | Статус каждой части операции: `action_id`, `state` (`SENT`/`APPLIED`/`REJECTED`/`FAILED`), `reject_reason`. |

### Analytics

| Метод | Путь | RBAC | Назначение |
|-------|------|------|-------------|
| GET | `/analytics/summary` | ADMIN или OPERATOR | Сводные показатели склада: итоги справочников, статусы событий, товаров, заказов и рейсов. |
| GET | `/analytics/onchain` | ADMIN или OPERATOR | Статус событий в блокчейне: счётчики, rate подтверждения, `recent_failed`/`recent_committed`. |
| GET | `/analytics/throughput` | ADMIN или OPERATOR | Объем outbox-событий по дням и этапам (query: `days`, 1..90, по умолчанию 14). |

### Products

| Метод | Путь | RBAC | Назначение |
|-------|------|------|-------------|
| GET | `/products/recent` | ADMIN или OPERATOR | Последние изменённые товары (query: `limit`; нечисловое/≤0 игнорируется, >50 обрезается до 50; по умолчанию 20). |
| GET | `/products/timeline` | ADMIN или OPERATOR | Таймлайн товара со статусами цепи (query: `key` — product_id или QR-код — обязателен). |

### Audit

| Метод | Путь | RBAC | Назначение |
|-------|------|------|-------------|
| GET | `/audit/products/{key}` | AUDITOR или ADMIN | Аудит товара: все отправленные подписанные действия, salt и детали (пересчитанные из БД, **camelCase**). |

\* `destination_id` — обязательный query-параметр.

---

## Подводные камни (закреплено кодом и e2e)

- **`/dispatches/` — завершающий слэш обязателен.** gorilla/mux не редиректит;
  `GET /dispatches` (без слэша) не матчится.
- **`POST /dispatches/` возвращает `200`, а не `201`**, и оборачивает результат в
  `data.dispatch`. У `GET /dispatches/{id}` — та же обёртка.
- **`/putaway/scan-storage-bin/draft`: BUFFER/SHIPPING_BUFFER-ячейка → `404
  STORAGE_BIN_NOT_FOUND`** (не 400). Контринтуитивно.
- **`/assembly/allocate` никогда не отдаёт ошибку при нехватке остатка** — частичная
  аллокация это `200` с заполненным `insufficient_orders`.
- **`product_ids` — это JSON-массив** в `/putaway/scan-storage-bin/draft` и
  `/shipping/ship/draft` (максимум 200 элементов). В `ship/draft` пустой/отсутствующий → bulk-режим.
- **`buffer_remaining` в `/shipping/ship/submit` считается по всей SHIPPING_BUFFER-зоне
  пункта назначения**, не по одной ячейке. Рейс отбывает (`DEPARTED`) только когда зона пуста.
- **`progress.not_received` всегда `0`** в ответе `gate/scan-cargoplace`; симметрично
  `summary.remaining` всегда `0` в `gate/accept-shipment`.
- **Только `/receiving/table/close-cargoplace/submit`, `/putaway/scan-storage-bin/submit`,
  `/assembly/scan-shipping-buffer/submit`, `/shipping/ship/submit` и
  `/passkeys/actions/{action_id}/submit` пишут в `public.outbox_events`** — это точки
  интеграции с блокчейном. `aggregate_type` у них всегда **`signed_action`**, а не
  имя этапа: этап определяется по `warehouse_actions.action_type` (или по
  `key_actions.action_type` для ключевых действий).

---

## Outbox / блокчейн (сводка)

Складские действия — двухшаговый поток: `POST …/draft` строит части операции (≤2000
товаров) с `challenge`, браузер подписывает каждую passkey оператора (WebAuthn P-256),
`POST …/submit` проверяет все подписи и в одной транзакции применяет операцию и пишет
**одну строку `outbox_events` на каждую часть-действие** (`event_id = aggregate_id =
action_id`, `event_type = wms.action.v1`, `payload` — конверт, ADR-0001 §9).

| Эндпоинт (draft → submit) | `action_type` | `aggregate_type` | Топик | Переход товара в цепи |
|---------------------------|---------------|------------------|-------|------------------------|
| `POST /receiving/table/close-cargoplace/{draft,submit}` | `1` Receive | `signed_action` | `wms.events.v1` | None → Accepted |
| `POST /putaway/scan-storage-bin/{draft,submit}` | `2` PutAway | `signed_action` | `wms.events.v1` | Accepted → PutAway |
| `POST /assembly/scan-shipping-buffer/{draft,submit}` | `3` Pick | `signed_action` | `wms.events.v1` | PutAway → Picked |
| `POST /shipping/ship/{draft,submit}` | `4` Ship | `signed_action` | `wms.events.v1` | Picked → Shipped |
| `POST /passkeys/actions/{key-register,key-revoke}/draft` → `/passkeys/actions/{id}/submit` | `5`/`6` KeyRegister/KeyRevoke | `signed_action` | `wms.events.v1` | — (ключевое действие в `ConsortiumRegistry`) |

`/assembly/pick` (подбор с полки) в outbox **не пишет**: Pick-действие создаётся при
выгрузке корзины в буфер. Debezium публикует всё в один топик `wms.events.v1` (key =
`action_id`), Ledger Adapter вызывает `SignedMappingWMS.applyActions` /
`ConsortiumRegistry.applyKeyActions` и ведёт `onchain_events`
(`PENDING → SENT → COMMITTED / FAILED`, `reject_reason` при отказе). Детали маршрута событий —
[`../integration/blockchain-mapping.md`](../integration/blockchain-mapping.md),
[`../integration/data-contract.md`](../integration/data-contract.md).

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
