# Моки API

Все запросы терминала к `/api/*` перехватывают моки (MSW, папка `mocks/`): `npm run dev` работает без бэкенда, состояние лежит в `localStorage`. Заодно подменяется `navigator.credentials` — подписи passkey делает виртуальный аутентификатор. `NEXT_PUBLIC_API_MOCKS=off` выключает и моки, и эмулятор сканера — запросы уходят в настоящий WMS API.

## Пользователи

Пароль совпадает с логином. Данные — из `mocks/data/seed.ts` и фикстур (`mocks/data/fixtures/passkeys.ts`).

| Логин | Пароль | Роль | Ключ passkey |
|-------|--------|------|--------------|
| `operator` | `operator` | OPERATOR | активен |
| `admin` | `admin` | ADMIN | активен |
| `operator2` | `operator2` | OPERATOR | нет |
| `operator3` | `operator3` | OPERATOR | ожидает подтверждения |
| `operator4` | `operator4` | OPERATOR | активен |
| `auditor` | `auditor` | AUDITOR | нет |
| `customer` | `customer` | CUSTOMER | нет |

- `operator`, `admin`, `operator2`, `operator3`, `operator4` — организация «Демо-склад»; `auditor` и `customer` — без организации.
- Подписывать умеет только ключ, лежащий в виртуальном аутентификаторе: вшитые ключи `operator` и `admin` (`mocks/data/keys.ts`) и ключи, созданные через регистрацию. Ключи `operator3`/`operator4` есть в БД, но не в аутентификаторе — подписать ими нельзя.

## Состояние и сброс

| Ключ localStorage | Что внутри |
|-------------------|------------|
| `wms-mock-db` | вся БД мока (пользователи, ячейки, товары, заказы, `warehouse_actions`, `onchain_events`, …) — seed из `mocks/data/seed.ts` плюс изменения |
| `wms-mock-chain` | режим цепи: `commit` / `fail` / `hold` / `reject:N` |
| `wms-virtual-authenticator` | ключи, созданные через регистрацию в браузере (JSON `[{credentialId, jwk}]`) |
| `wms-virtual-authenticator-mode` | режим аутентификатора: `ok` / `cancel` / `corrupt` |
| `wms-auth` | сессия входа фронтенда (access/refresh токены) |

Сброс данных к seed — в консоли браузера:

```js
localStorage.removeItem("wms-mock-db"); location.reload()
```

Полный сброс (данные, режимы цепи и аутентификатора, сессия):

```js
localStorage.clear()
```

Версия данных проверяется: при несовпадении `dbVersion` база пересоздаётся из seed автоматически.

## Цепь

Каждое отправленное подписанное действие попадает в `onchain_events` со статусом `PENDING` и оседает через 1 с (`chainDelayMs`). Режим оседания выбирается при постановке события в очередь — режим надо выставить до отправки. Режим — `localStorage["wms-mock-chain"]`:

```js
localStorage.setItem("wms-mock-chain", "hold")
```

| Режим | `onchain_events.status` | `reject_reason` | Часть операции (`GET /signed-actions/operations/{id}`) |
|-------|--------------------------|-----------------|--------------------------------------------------------|
| `commit` (по умолчанию) | `COMMITTED` | null | `SENT` → `APPLIED` |
| `fail` | `FAILED` («rejected by contract») | null | `SENT` → `FAILED` |
| `reject:N` | `FAILED` | N | `SENT` → `REJECTED`, `reject_reason: N` |
| `hold` | `PENDING` — не оседает | — | остаётся `SENT` |

Подтверждённое действие ключа делает ключ `ACTIVE` (KeyRegister) или `REVOKED` (KeyRevoke) — реестр применяется после `COMMITTED`.

## Аутентификатор

Виртуальный аутентификатор (`mocks/virtual-authenticator.ts`) подменяет `navigator.credentials` и подписывает вшитым ключом (`operator`, `admin`) или ключом из своего хранилища. Режим — `localStorage["wms-virtual-authenticator-mode"]`:

```js
localStorage.setItem("wms-virtual-authenticator-mode", "corrupt")
```

| Режим | Поведение при подписи |
|-------|-----------------------|
| `ok` (по умолчанию) | подпись корректна |
| `cancel` | пользователь отменил: `DOMException(NotAllowedError)` — фронт отдаёт `SIGNING_CANCELLED` |
| `corrupt` | подпись испорчена — submit отдаёт `400 INVALID_ASSERTION` «Подтверждение не прошло проверку» |

У `operator` и `admin` ключи уже есть. Нюансы фикстурных ключей: у `operator3` draft не пройдёт вовсе (`PENDING` → `409 SIGNER_KEY_NOT_ACTIVE`), а `operator4` получит черновик, но подписать нельзя — в аутентификаторе нет ключа с таким `credential_id` (`NotAllowedError`).

## Ключ администратора

- Seed-ключ `admin` активен и подписывает действия ключей.
- Новый админский ключ создаётся в статусе `PENDING` («ожидает подтверждения») и остаётся им навсегда: в моках нет эмуляции CLI владельца организации — подтверждение/отмена целятся только в ключи роли `OPERATOR`.
- Зарегистрировать ключ при живом не выйдет: `/passkeys/registration/begin` при активном ключе отдаёт `409 PASSKEY_EXISTS` «Ключ уже зарегистрирован».
- Вернуть исходное — сбросом состояния.

## Данные

Id фикстур детерминированы (`mockId`, `mocks/data/fixture-kit.ts`) — значения из колонок id можно подставлять в сценарии как есть. Магазины:

| Магазин | id | Источник |
|---------|----|----------|
| SHOP-5 «Магазин №5 Маросейка» | `01929c5e-0000-7000-8008-000000000001` | seed |
| SHOP-7 «Магазин №7 Петровка» | `01929c5e-0000-7000-8008-000000000002` | seed |
| SHOP-9 «Магазин №9 Тверская» | `01929c5e-0000-7000-8008-000000000301` | fixtures/assembly |

### Раскладка

| Ячейка | id | Секция | Объём | Товары |
|--------|----|--------|-------|--------|
| BUFFER-01 (буфер приёмки) | `01929c5e-0000-7000-8001-000000000006` | BUFFER | 200 | QR-PUT-0001…0005 — RECEIVED, QR-PUT-0006 — STORED |
| A-01-01 | `01929c5e-0000-7000-8001-000000000001` | A | 120 | QR-2026-0003, QR-2026-0009, QR-2026-0010 (STORED) |
| A-01-02 | `01929c5e-0000-7000-8001-000000000002` | A | 120 | QR-2026-0011, QR-2026-0012 (STORED) |
| A-02-01 | `01929c5e-0000-7000-8001-000000000003` | A | 110 | QR-2026-0013, QR-2026-0014, QR-2026-0015 (STORED) |
| B-01-01 | `01929c5e-0000-7000-8001-000000000004` | B | 130 | QR-2026-0016, QR-2026-0017 (STORED) |
| B-01-02 | `01929c5e-0000-7000-8001-000000000005` | B | 130 | QR-2026-0018 (STORED) |
| C-00-00 | `01929c5e-0000-7000-8001-000000000201` | C | 0 | — (объём 0 → `STORAGE_BIN_NOT_FOUND`) |

Товары в буфере приёмки BUFFER-01 (как их отдаёт `POST /putaway/scan-buffer`):

| QR | Товар (SKU) | id | Грузоместо (статус) | Статус |
|----|-------------|----|----------------------|--------|
| QR-PUT-0001 | Кроссовки Nike Air Max 90 | `01929c5e-0000-7000-8006-000000000201` | CP-PUT-001 (TABLE_CLOSED) | RECEIVED |
| QR-PUT-0002 | Футболка Adidas Originals | `01929c5e-0000-7000-8006-000000000202` | CP-PUT-001 (TABLE_CLOSED) | RECEIVED |
| QR-PUT-0003 | Рюкзак Puma Core | `01929c5e-0000-7000-8006-000000000203` | CP-PUT-001 (TABLE_CLOSED) | RECEIVED |
| QR-PUT-0004 | Рюкзак Puma Core | `01929c5e-0000-7000-8006-000000000204` | CP-PUT-003 (TABLE_CLOSED) | RECEIVED; приёмка FAILED в цепи (reject 12) |
| QR-PUT-0005 | Футболка Adidas Originals | `01929c5e-0000-7000-8006-000000000205` | CP-PUT-002 (TABLE_IN_PROGRESS) | RECEIVED |
| QR-PUT-0006 | Куртка The North Face | `01929c5e-0000-7000-8006-000000000206` | CP-PUT-001 (TABLE_CLOSED) | STORED — уже разложен, в списке скана буфера не виден |

### Сборка

Раскладка уже пройдена; задачи сборки:

| Товар (QR · SKU) | id | Из ячейки | Заказ | Магазин | Статус задачи |
|------------------|----|-----------|-------|---------|---------------|
| QR-ASM-0010 · Nike | `01929c5e-0000-7000-8006-000000000320` | A-02-01 | ORD-ASM-010 | SHOP-9 | PENDING |
| QR-ASM-0011 · Adidas | `01929c5e-0000-7000-8006-000000000321` | B-01-02 | ORD-ASM-010 | SHOP-9 | PENDING |
| QR-ASM-0030 · Puma | `01929c5e-0000-7000-8006-000000000340` | A-01-01 | ORD-ASM-030 | SHOP-9 | PENDING; раскладка FAILED (reject 7) → `CHAIN_EVENT_REJECTED` |
| QR-ASM-0020 · Nike | `01929c5e-0000-7000-8006-000000000330` | A-01-01 | ORD-ASM-020 | SHOP-7 | DONE (оператор `operator`) — уже в корзине |

Заказы: ORD-ASM-001 (Puma×2), ORD-ASM-002 (Puma×5), ORD-ASM-003 (Adidas×1) — NEW, SHOP-9; ORD-ASM-010 (Nike+Adidas×1) и ORD-ASM-030 (Puma×1) — ALLOCATED, SHOP-9; ORD-ASM-020 (Nike×1) — ALLOCATED, SHOP-7. STORED-товары под аллокацию: Puma — QR-2026-0003, QR-ASM-0001, QR-ASM-0002; Adidas — QR-2026-0014…0018.

Буферы отгрузки для выгрузки корзины: BIN-SHIP-5 → SHOP-5, BIN-SHIP-7 → SHOP-7, BIN-SHIP-9 → SHOP-9 (пустой), BIN-SHIP-0 — буфер без магазина (`404 DESTINATION_NOT_FOUND`).

### Отгрузка

| Буфер | id | Магазин | Товары (READY_TO_SHIP) |
|-------|----|---------|------------------------|
| BIN-SHIP-5 | `01929c5e-0000-7000-8001-000000000007` | SHOP-5 | QR-SHP-0001 (Nike, ORD-SHP-001), QR-SHP-0002 (Adidas, ORD-SHP-001), QR-SHP-0003 (Puma, ORD-SHP-002) |
| BIN-SHIP-7 | `01929c5e-0000-7000-8001-000000000008` | SHOP-7 | QR-SHP-0005 (Adidas, ORD-SHP-005), QR-SHP-0006 (Nike, ORD-SHP-005) |
| BIN-SHIP-5B | `01929c5e-0000-7000-8001-000000000401` | SHOP-5 | QR-SHP-0007 (Nike, ORD-SHP-007) |
| BIN-SHIP-9 | `01929c5e-0000-7000-8001-000000000301` | SHOP-9 | пуст |

Pick-действие (Picking) в цепи COMMITTED для QR-SHP-0001, 0002, 0003, 0006, 0007; у QR-SHP-0005 его нет.

Рейсы:

| Рейс | id | Магазин | Статус | Машина / водитель |
|------|----|---------|--------|---------------------|
| DSP-SHP-001 | `01929c5e-0000-7000-8009-000000000401` | SHOP-5 | AT_GATE | B111AA199 / Александр Смирнов |
| DSP-SHP-002 | `01929c5e-0000-7000-8009-000000000402` | SHOP-7 | AT_GATE | B222BB299 / Дмитрий Козлов |
| DSP-SHP-003 | `01929c5e-0000-7000-8009-000000000403` | SHOP-5 | DEPARTED | B333CC399 / Егор Волков |
| DSP-SHP-004 | `01929c5e-0000-7000-8009-000000000404` | SHOP-5 | CANCELLED | B444DD499 / Никита Орлов |
| DSP-SHP-005 | `01929c5e-0000-7000-8009-000000000405` | SHOP-7 | SCHEDULED (первый скан → AT_GATE) | B555EE599 / Сергей Морозов |
| DSP-2026-0421-001 | `01929c5e-0000-7000-8009-000000000001` | SHOP-5 | SCHEDULED | A123BC777 / Иван Петров |

## Сценарии ошибок

Коды, статусы и тексты взяты из моков. Сценарии выполняются из свежего состояния (сброс `wms-mock-db`). В сценариях `<X>` и `<QR-…>` — id из раздела «Данные»; «неизвестный UUID» — например `01929c5e-0000-7000-8001-000000999999`. Экраны раскладки/сборки/отгрузки пока заглушки — сценарии гоняйте из консоли браузера:

```js
const login = (username) => fetch("/api/auth/login", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ username, password: username }) }).then((r) => r.json()).then((d) => d.access_token);
const call = (token, path, body) => fetch(`/api${path}`, { method: "POST", headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` }, body: JSON.stringify(body) }).then((r) => r.json());
```

Шаги с подписью выполняет фронт (`signAndSubmit`, `src/lib/signing/sign-and-submit.ts`) — в юнит-тестах так и сделано. В колонке «Тест»: `файл › describe › it`; «%s» — имя шаблона `it.each` без подстановки (grep -F находит его в файле), конкретный кейс определяется по сценарию в столбце «Как вызвать». Ошибки 401/403/`INVALID_REQUEST` до бизнес-проверок — в «Общие ошибки».

### Раскладка

| Ошибка | Эндпоинт | Как вызвать | Тест |
|--------|----------|-------------|------|
| `BUFFER_BIN_NOT_FOUND` 404 «Буферная ячейка не найдена» | POST `/putaway/scan-buffer` | `operator` → `{ buffer_bin_id: "01929c5e-0000-7000-8001-000000999999" }` (неизвестная ячейка) | putaway.test.ts › сканирование буфера и товара › неизвестная буферная ячейка — BUFFER_BIN_NOT_FOUND |
| `BUFFER_BIN_NOT_FOUND` 404 | POST `/putaway/scan-buffer` (и `/scan-product`) | `operator` → `{ buffer_bin_id: <A-01-01> }` — ячейка хранения вместо буфера | putaway.test.ts › сканирование буфера и товара › ячейка хранения вместо буфера — BUFFER_BIN_NOT_FOUND |
| `PRODUCT_NOT_FOUND` 404 «Товар не найден» | POST `/putaway/scan-product` | `operator` → `{ product_id: "01929c5e-0000-7000-8006-000000999999", buffer_bin_id: <BUFFER-01> }` | putaway.test.ts › сканирование буфера и товара › скан товара: %s — %s |
| `PRODUCT_NOT_IN_BUFFER` 409 «Товар не находится в указанной буферной ячейке» | POST `/putaway/scan-product` | `operator` → `{ product_id: <QR-2026-0003> (лежит в A-01-01), buffer_bin_id: <BUFFER-01> }` | putaway.test.ts › сканирование буфера и товара › скан товара: %s — %s |
| `PRODUCT_NOT_RECEIVED` 409 «Товар не в статусе RECEIVED» | POST `/putaway/scan-product` | `operator` → `{ product_id: <QR-PUT-0006> (STORED), buffer_bin_id: <BUFFER-01> }` | putaway.test.ts › сканирование буфера и товара › скан товара: %s — %s |
| `INVALID_REQUEST` 400 «product_ids не может быть пустым» | POST `/putaway/scan-storage-bin/draft` | `operator` → `{ product_ids: [], storage_bin_id: <A-01-01> }` | putaway.test.ts › черновик размещения › %s — %s |
| `INVALID_REQUEST` 400 «product_ids не может содержать более 200 элементов» | POST `/putaway/scan-storage-bin/draft` | `operator` → `{ product_ids: [×201 QR-PUT-0001], storage_bin_id: <A-01-01> }` | putaway.test.ts › черновик размещения › %s — %s |
| `STORAGE_BIN_NOT_FOUND` 404 «Ячейка хранения не найдена» | POST `/putaway/scan-storage-bin/draft` | `operator` → `{ product_ids: [<QR-PUT-0001>], storage_bin_id: "01929c5e-0000-7000-8001-000000999999" }` (неизвестная ячейка) | putaway.test.ts › черновик размещения › неизвестная ячейка-цель — STORAGE_BIN_NOT_FOUND |
| `STORAGE_BIN_NOT_FOUND` 404 | POST `/putaway/scan-storage-bin/draft` | `{ product_ids: [<QR-PUT-0001>], storage_bin_id: <BUFFER-01> }` — буфер приёмки | putaway.test.ts › черновик размещения › ячейка-цель %s — STORAGE_BIN_NOT_FOUND |
| `STORAGE_BIN_NOT_FOUND` 404 | POST `/putaway/scan-storage-bin/draft` | `storage_bin_id: <BIN-SHIP-5>` — буфер отгрузки | putaway.test.ts › черновик размещения › ячейка-цель %s — STORAGE_BIN_NOT_FOUND |
| `STORAGE_BIN_NOT_FOUND` 404 | POST `/putaway/scan-storage-bin/draft` | `storage_bin_id: <C-00-00>` — объём 0 | putaway.test.ts › черновик размещения › ячейка-цель %s — STORAGE_BIN_NOT_FOUND |
| `PRODUCT_NOT_FOUND` 404 «Товар не найден» | POST `/putaway/scan-storage-bin/draft` | `operator` → `{ product_ids: [<QR-PUT-0001>, "01929c5e-0000-7000-8006-000000999999"], storage_bin_id: <A-01-01> }` | putaway.test.ts › черновик размещения › %s — %s |
| `PRODUCT_NOT_IN_BUFFER` 409 «Товар не находится в указанной буферной ячейке» | POST `/putaway/scan-storage-bin/draft` | `{ product_ids: [<QR-PUT-0001>, <QR-2026-0003>], storage_bin_id: <A-01-01> }` — товар в ячейке хранения | putaway.test.ts › черновик размещения › %s — %s |
| `PRODUCT_NOT_RECEIVED` 409 «Товар не в статусе RECEIVED» | POST `/putaway/scan-storage-bin/draft` | `{ product_ids: [<QR-PUT-0001>, <QR-PUT-0006>], storage_bin_id: <A-01-01> }` | putaway.test.ts › черновик размещения › %s — %s |
| `CARGOPLACE_NOT_CLOSED` 409 «Грузоместо товара ещё не закрыто на приёмке» | POST `/putaway/scan-storage-bin/draft` и `/submit` | `{ product_ids: [<QR-PUT-0001>, <QR-PUT-0005>], storage_bin_id: <A-01-01> }` — грузоместо CP-PUT-002 в TABLE_IN_PROGRESS; гейт на submit тот же, но из свежего seed не воспроизводится: грузоместо нельзя «рас-закрыть» через API | putaway.test.ts › черновик размещения › %s — %s |
| `CHAIN_EVENT_REJECTED` 409 «Размещение отклонено: приёмка товара не подтверждена в блокчейне (FAILED)» | POST `/putaway/scan-storage-bin/draft` и `/submit` | `{ product_ids: [<QR-PUT-0001>, <QR-PUT-0004>], storage_bin_id: <A-01-01> }` — приёмка QR-PUT-0004 уже FAILED (reject 12); гейт на submit тот же, но из свежего seed не воспроизводится: событие уже COMMITTED и не меняется | putaway.test.ts › черновик размещения › %s — %s |
| `SIGNER_KEY_NOT_ACTIVE` 409 «Ключ оператора не активен в реестре» | POST `/putaway/scan-storage-bin/draft` и `/submit` | войти `operator2` (без ключа) → draft `{ product_ids: [<QR-PUT-0001>], storage_bin_id: <A-01-01> }`; на submit — если ключ оператора отзывают между черновиком и отправкой (сценарий в тесте ключей) | putaway.test.ts › черновик размещения › у оператора нет ключа — SIGNER_KEY_NOT_ACTIVE; passkeys.test.ts › сквозные сценарии › отзыв ключа оператора блокирует его подписи |
| `DRAFT_STALE` 409 «Состав корзины изменился, подтвердите размещение заново» | POST `/putaway/scan-storage-bin/submit` | `operator`: draft `[<QR-PUT-0001>, <QR-PUT-0002>]`; разместить QR-PUT-0001 (новый draft + подпись + submit); подписать и отправить первый черновик — как в тесте (`signAndSubmit`) | putaway.test.ts › подпись и отправка › товар ушёл из буфера между черновиком и отправкой — DRAFT_STALE |
| `OPERATION_ALREADY_SUBMITTED` 409 «Операция уже отправлена» | POST `/putaway/scan-storage-bin/submit` | `operator`: отправить операцию `[<QR-PUT-0001>]` (draft + подпись + submit), затем повторить тот же submit (двойной клик / вторая вкладка) | putaway.test.ts › подпись и отправка › повторная отправка — OPERATION_ALREADY_SUBMITTED |
| `INVALID_ASSERTION` 400 «Подтверждение не прошло проверку» | POST `/putaway/scan-storage-bin/submit` | `operator`: draft `[<QR-PUT-0001>]` → `localStorage.setItem("wms-virtual-authenticator-mode", "corrupt")` → подписать и отправить | putaway.test.ts › подпись и отправка › подпись не сходится — INVALID_ASSERTION, товары остаются в буфере |

### Сборка

| Ошибка | Эндпоинт | Как вызвать | Тест |
|--------|----------|-------------|------|
| `200` — не ошибка: заказ с недостающими SKU попадает в `insufficient_orders` | POST `/assembly/allocate` | `operator` → `{ destination_id: <SHOP-9> }`: ORD-ASM-001 и ORD-ASM-003 аллоцируются, ORD-ASM-002 — в `insufficient_orders` (не хватает 4 шт. Puma Core) | assembly.test.ts › аллокация › резервирует NEW-заказы целиком, нехватка — в insufficient_orders без частичного заслуживания |
| нет ошибки — `ORDER_NOT_NEW` в моке не реализован: заказ со статусом ≠ NEW просто пропускается (расхождение с доком) | POST `/assembly/allocate` | `operator` → второй `allocate <SHOP-9>`: заказы уже ALLOCATED, ответ `{ allocated_orders: 0, allocated_products: 0, insufficient_orders: [ORD-ASM-002] }` | assembly.test.ts › аллокация › повторная аллокация — заказы не NEW пропускаются без ошибки |
| `PRODUCT_NOT_ALLOCATED` 409 «Товар не в статусе ALLOCATED» | POST `/assembly/pick` | из свежего seed через API не воспроизводится — у всех товаров с PENDING-задачами статус ALLOCATED; нужна прямая правка состояния (в тесте: `product.status = "STORED"; saveDb()`) и pick `<QR-ASM-0011>` | assembly.test.ts › подбор › задача PENDING, но товар не ALLOCATED — PRODUCT_NOT_ALLOCATED |
| `NO_TASK_FOR_PRODUCT` 409 «Нет задачи сборки для этого товара» | POST `/assembly/pick` | `operator` → `{ product_id: <QR-2026-0009> }` — STORED-товар без задачи; проверка в моке только по PENDING — задач в IN_PROGRESS не бывает | assembly.test.ts › подбор › повторный pick и pick без задачи — NO_TASK_FOR_PRODUCT |
| `CHAIN_EVENT_REJECTED` 409 «Подбор отклонён: размещение товара не подтверждено в блокчейне (FAILED)» | POST `/assembly/pick` | `operator` → `{ product_id: <QR-ASM-0030> }` — раскладка товара FAILED (reject 7), задача остаётся PENDING | assembly.test.ts › подбор › FAILED раскладка в цепи — CHAIN_EVENT_REJECTED, задача остаётся PENDING |
| `CART_EMPTY` 409 «Корзина оператора пуста» | POST `/assembly/scan-shipping-buffer/draft` | войти `operator2` → `{ buffer_bin_id: <BIN-SHIP-9> }` — подбирать нечего; у `operator` тот же ответ после выгрузки всей корзины | assembly.test.ts › границы черновика › чужой магазин в корзине — DESTINATION_MISMATCH; у пустого оператора — CART_EMPTY |
| `BIN_NOT_SHIPPING_BUFFER` 400 «Ячейка не является буфером отгрузки» | POST `/assembly/scan-shipping-buffer/draft` | `operator` → `{ buffer_bin_id: <A-01-01> }` или `<BUFFER-01>` — не буфер отгрузки | assembly.test.ts › границы черновика › ячейка не буфер отгрузки / без магазина / не uuid |
| `BIN_NOT_SHIPPING_BUFFER` 400 | POST `/assembly/scan-shipping-buffer/draft` | `operator` → `{ buffer_bin_id: "01929c5e-0000-7000-8001-000000999999" }` — ячейки нет | assembly.test.ts › границы черновика › ячейка не буфер отгрузки / без магазина / не uuid |
| `DESTINATION_MISMATCH` 409 «Буфер отгрузки другого магазина: собранные товары не для этого пункта назначения» | POST `/assembly/scan-shipping-buffer/draft` | `operator` → `{ buffer_bin_id: <BIN-SHIP-9> }` (SHOP-9) — в его корзине QR-ASM-0020 для SHOP-7 | assembly.test.ts › границы черновика › чужой магазин в корзине — DESTINATION_MISMATCH; у пустого оператора — CART_EMPTY |
| `SIGNER_KEY_NOT_ACTIVE` 409 «Ключ оператора не активен в реестре» | POST `/assembly/scan-shipping-buffer/draft` и `/submit` | войти `operator2` → pick `<QR-ASM-0010>` → draft `{ buffer_bin_id: <BIN-SHIP-9> }`; на submit — если ключ оператора отзывают между черновиком и отправкой (сценарий в тесте ключей) | assembly.test.ts › границы черновика › у operator2 нет ключа — SIGNER_KEY_NOT_ACTIVE; passkeys.test.ts › сквозные сценарии › отзыв ключа оператора блокирует его подписи |
| `DRAFT_STALE` 409 «Состав корзины изменился, подтвердите выгрузку заново» | POST `/assembly/scan-shipping-buffer/submit` | `operator`: pick `<QR-ASM-0010>` → draft `<BIN-SHIP-9>`; затем pick `<QR-ASM-0011>` — и подписать и отправить первый черновик (`signAndSubmit`) | assembly.test.ts › подпись и отправка выгрузки › состав корзины изменился после черновика — DRAFT_STALE, товары остаются ASSEMBLED |
| `OPERATION_ALREADY_SUBMITTED` 409 «Операция уже отправлена» | POST `/assembly/scan-shipping-buffer/submit` | `operator`: отправить выгрузку (pick + draft + подпись + submit), затем повторить тот же submit (двойной клик / вторая вкладка) | assembly.test.ts › подпись и отправка выгрузки › повторная отправка — OPERATION_ALREADY_SUBMITTED; corrupt-подпись — INVALID_ASSERTION без записи |
| `INVALID_ASSERTION` 400 «Подтверждение не прошло проверку» | POST `/assembly/scan-shipping-buffer/submit` | `operator`: pick `<QR-ASM-0011>` → draft → `localStorage.setItem("wms-virtual-authenticator-mode", "corrupt")` → подписать и отправить | assembly.test.ts › подпись и отправка выгрузки › повторная отправка — OPERATION_ALREADY_SUBMITTED; corrupt-подпись — INVALID_ASSERTION без записи |

### Отгрузка

| Ошибка | Эндпоинт | Как вызвать | Тест |
|--------|----------|-------------|------|
| `BIN_NOT_SHIPPING_BUFFER` 400 «Ячейка не является буфером отгрузки» | POST `/shipping/scan-buffer` | `operator` → `{ buffer_bin_id: <A-01-01> }` (ячейка хранения) или `<BUFFER-01>` (буфер приёмки) | shipping.test.ts › сканирование буфера отгрузки › %s — BIN_NOT_SHIPPING_BUFFER |
| `BIN_NOT_FOUND` 404 «Ячейка не найдена» | POST `/shipping/scan-buffer` | `{ buffer_bin_id: "01929c5e-0000-7000-8001-000000999999" }` | shipping.test.ts › сканирование буфера отгрузки › неизвестная ячейка — 404 BIN_NOT_FOUND |
| нет ошибки — `BUFFER_EMPTY` у scan-buffer не реализован: мок отдаёт `200` со списком товаров `count: 0` (расхождение с доком) | POST `/shipping/scan-buffer` | `{ buffer_bin_id: <BIN-SHIP-9> }` — буфер SHOP-9 без товаров | shipping.test.ts › сканирование буфера отгрузки › буфер без товаров READY_TO_SHIP — 200 с пустым списком, BUFFER_EMPTY у scan-buffer нет |
| `DISPATCH_NOT_FOUND` 404 «Рейс не найден» | POST `/shipping/scan-driver` | `operator` → `{ dispatch_code: "DSP-НЕТ" }` | shipping.test.ts › сканирование водителя › скан водителя %s — %s %s |
| `DISPATCH_ALREADY_DEPARTED` 409 «Рейс уже отправлен» | POST `/shipping/scan-driver` | `{ dispatch_code: "DSP-SHP-003" }` (DEPARTED) | shipping.test.ts › сканирование водителя › скан водителя %s — %s %s |
| `DISPATCH_CANCELLED` 409 «Рейс отменён» | POST `/shipping/scan-driver` | `{ dispatch_code: "DSP-SHP-004" }` | shipping.test.ts › сканирование водителя › скан водителя %s — %s %s |
| `200` — не ошибка: повторный скан рейса в AT_GATE идемпотентен, `arrived_at` не меняется | POST `/shipping/scan-driver` | `{ dispatch_code: "DSP-SHP-001" }` (уже AT_GATE) — отдаётся как есть; скан DSP-SHP-005 второй раз — тот же `arrived_at` | shipping.test.ts › сканирование водителя › уже AT_GATE — возвращается как есть, arrived_at не меняется · shipping.test.ts › сканирование водителя › SCHEDULED → AT_GATE с arrived_at, повторный скан идемпотентен |
| `DESTINATION_MISMATCH` 409 «Рейс назначен на другую зону» | POST `/shipping/ship/draft` | `operator` → `{ buffer_bin_id: <BIN-SHIP-5> (SHOP-5), dispatch_id: <DSP-SHP-002> (SHOP-7) }` | shipping.test.ts › черновик и отправка отгрузки › поездка не на ту зону — DESTINATION_MISMATCH |
| `DISPATCH_NOT_AT_GATE` 409 «Рейс не находится у ворот» | POST `/shipping/ship/draft` | `{ buffer_bin_id: <BIN-SHIP-5>, dispatch_id: <DSP-2026-0421-001> }` (SCHEDULED) или `<DSP-SHP-004>` (CANCELLED); после полной отгрузки обоих буферов SHOP-5 рейс уходит в DEPARTED — тот же ответ | shipping.test.ts › черновик и отправка отгрузки › bulk на %s — DISPATCH_NOT_AT_GATE |
| `PRODUCT_NOT_IN_BUFFER` 409 «Товар не найден в буфере или не готов к отгрузке» | POST `/shipping/ship/draft` | `operator` → `{ buffer_bin_id: <BIN-SHIP-5>, dispatch_id: <DSP-SHP-001>, product_ids: [<QR-SHP-0007>] }` — товар в другом буфере (BIN-SHIP-5B); подойдёт и STORED QR-2026-0009, и дубль в списке | shipping.test.ts › черновик и отправка отгрузки › spot %s — PRODUCT_NOT_IN_BUFFER |
| `BUFFER_EMPTY` 409 «Буфер отгрузки пуст» | POST `/shipping/ship/draft` | `operator`: spot-отгрузка `[<QR-SHP-0007>]` из BIN-SHIP-5B (DSP-SHP-001), затем bulk-черновик того же буфера без `product_ids` | shipping.test.ts › черновик и отправка отгрузки › буфер опустел после spot — дальнейший bulk даёт BUFFER_EMPTY, рейс ещё у ворот |
| `PICKING_NOT_CONFIRMED` 409 «Сборка части товаров (1 шт.) ещё не подтверждена в цепи. Повторите отгрузку чуть позже» | POST `/shipping/ship/draft` | `operator` → `{ buffer_bin_id: <BIN-SHIP-7>, dispatch_id: <DSP-SHP-002> }` (bulk) — у QR-SHP-0005 нет Pick-подтверждения; spot `[<QR-SHP-0006>]` проходит | shipping.test.ts › черновик и отправка отгрузки › сборка не подтверждена: bulk — PICKING_NOT_CONFIRMED ровно с одним товаром, spot с подтверждённым проходит |
| `SIGNER_KEY_NOT_ACTIVE` 409 «Ключ оператора не активен в реестр» | POST `/shipping/ship/draft` и `/submit` | войти `operator2` → `{ buffer_bin_id: <BIN-SHIP-5>, dispatch_id: <DSP-SHP-001> }`; на submit — если ключ оператора отзывают между черновиком и отправкой (сценарий в тесте ключей) | shipping.test.ts › валидация параметров черновика › у оператора нет ключа — SIGNER_KEY_NOT_ACTIVE; passkeys.test.ts › сквозные сценарии › отзыв ключа оператора блокирует его подписи |
| `DRAFT_STALE` 409 «Состав отгрузки изменился, подтвердите отгрузку заново» | POST `/shipping/ship/submit` | `operator`: bulk-черновик (`<BIN-SHIP-5>`, DSP-SHP-001); отгрузить `[<QR-SHP-0001>]` отдельной операцией; подписать и отправить первый черновик (`signAndSubmit`) | shipping.test.ts › подпись и повторная отправка › состав отгрузки устарел — DRAFT_STALE |
| `OPERATION_ALREADY_SUBMITTED` 409 «Операция уже отправлена» | POST `/shipping/ship/submit` | `operator`: отправить отгрузку (draft + подпись + submit), затем повторить тот же submit (двойной клик / вторая вкладка) | shipping.test.ts › подпись и повторная отправка › повторная отправка — OPERATION_ALREADY_SUBMITTED |
| `INVALID_ASSERTION` 400 «Подтверждение не прошло проверку» | POST `/shipping/ship/submit` | `operator`: draft → `localStorage.setItem("wms-virtual-authenticator-mode", "corrupt")` → подписать и отправить | shipping.test.ts › подпись и повторная отправка › битая подпись — INVALID_ASSERTION, товары остаются в буфере |

## Общие ошибки

`401` — мок отдаёт без конверта, простым текстом `unauthorized`: нет, просрочен или битый access-токен на любом `/api/*` (кроме `/auth/*`; фронт при этом один раз обновляет токен через `/auth/refresh`); неверная пара на `/auth/login` — тот же 401. Фронт превращает его в `ApiError` с кодом `UNAUTHORIZED` и текстом «Сессия истекла, войдите заново».

`403 FORBIDDEN` — конверт с кодом и текстом guard'а эндпоинта:

| Guard | Эндпоинты | Текст |
|-------|-----------|-------|
| operator | приёмка, раскладка, сборка (`/assembly/*` кроме `/allocate`), отгрузка, `/destinations`, `/dispatches` | «Только оператор может выполнять это действие» |
| admin | `/passkeys/organization*`, `/passkeys/actions/*` | «Только администратор может выполнять это действие» |
| adminOrOperator | `/assembly/allocate`, `/passkeys/registration/*`, `/passkeys/me`, `/signed-actions/operations/{id}` | «Требуется роль администратора или оператора» |
| auditorOrAdmin | определён в `mocks/http.ts`, но пока не используется | «Требуется роль аудитора или администратора» |

`INVALID_REQUEST` 400 — до бизнес-проверок: битое тело или лишние поля — «Невалидный JSON в теле запроса»; nil-UUID — «Невалидные входные данные»; не-UUID поле — «<поле> должен быть UUID» (например «buffer_bin_id должен быть UUID», «product_id не uuid»); в сборке — «destination_id обязателен», «неизвестный статус задачи». Создание рейса (`POST /dispatches/`) имитирует тексты Go-бэкенда: «Invalid request body: decode json», «Invalid request body: you need to pass a driver_name», «Invalid request body: you need to pass a vehicle_number», «Invalid request body: scheduled_at should not be past time», «Invalid request: invalid UUID format», «Invalid request: strconv.Atoi: parsing «abc»: invalid syntax».

`500 INTERNAL_ERROR` — «Внутренняя ошибка сервера»: любой непойманный сбой обработчика; специального способа вызвать руками в моках нет.

Примеры тестов: putaway.test.ts › сканирование буфера и товара › не UUID — INVALID_REQUEST с именем поля; putaway.test.ts › сканирование буфера и товара › админ — FORBIDDEN, раскладка только для оператора; assembly.test.ts › задачи сборки › валидация запроса: destination_id → operator_id → status, admin — 403.
