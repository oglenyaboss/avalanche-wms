# BatchMappingWMS — справочник разработчика

**Версия:** 1.0
**Дата:** 2026-05-31
**Аудитория:** команда, принимающая сервис на сопровождение (hand-off)

Этот документ описывает **механику** контракта `BatchMappingWMS`: FSM, точные сигнатуры, события, условия revert, идемпотентность, контроль доступа, конвенцию `UUID → uint256` и storage layout.

Архитектурный контекст — **зачем** выбран именно этот контракт, сравнение трёх подходов (HashOnly / MappingWMS / MerkleRootWMS) и TPS-показатели нагрузочного тестирования — см. [BatchMappingWMS — подход](../integration/batch-mapping-approach.md). Здесь это **не дублируется**.

Как контракт встроен в пайплайн (consumer, batch-flusher, идемпотентность на стороне адаптера) — см. [Ledger Adapter Reference](ledger-adapter-reference.md). Общий маппинг операций WMS → блокчейн — [Маппинг WMS → Блокчейн](../integration/blockchain-mapping.md).

**Источник:** `ledger-adapter/contracts/BatchMappingWMS.sol` (Solidity `^0.8.0`, компиляция `solc 0.8.23`, EVM-таргет `paris`, оптимизатор on, 200 runs — см. `contracts/foundry.toml`). Тесты: `ledger-adapter/contracts/BatchMappingWMS.t.sol` (Forge/Foundry).

---

## 1. Конечный автомат (FSM)

Контракт ведёт on-chain статус каждого товара по строгому FSM:

```
None ──accept──► Accepted ──putAway──► PutAway ──pick──► Picked ──ship──► Shipped
```

```solidity
enum Status { None, Accepted, PutAway, Picked, Shipped }
```

Кодируется on-chain как `uint8`. Численные значения:

| Status | uint8 |
|---|:---:|
| `None` | 0 |
| `Accepted` | 1 |
| `PutAway` | 2 |
| `Picked` | 3 |
| `Shipped` | 4 |

Каждое WMS-событие требует, чтобы товар находился в строго определённом исходном статусе; иначе переход не выполняется (поведение зависит от того, batch- или per-item функция — см. §3). Соответствие со статусами сущностей WMS — см. [Жизненные циклы сущностей](../data-model/entity-lifecycle.md).

---

## 2. Storage layout (mappings)

```solidity
mapping(uint256 => Status) public itemStatus;
mapping(uint256 => bool)   public processedEventIds;
```

| Slot | Тип | Ключ | Назначение |
|---|---|---|---|
| `itemStatus` | `mapping(uint256 => Status)` | `keccak256(product_uuid_string)` как `uint256` | текущий FSM-статус товара |
| `processedEventIds` | `mapping(uint256 => bool)` | `keccak256(event_uuid_string)` как `uint256` | флаг «событие уже обработано» (replay-защита) |

Оба mapping'а объявлены `public` — компилятор генерирует геттеры `itemStatus(uint256) → Status` и `processedEventIds(uint256) → bool`, доступные любому вызывающему.

> Это всё состояние контракта. Понятия `batchId` нет — батч это группировка на стороне Go (по `AggregateType` + `fsmOrder`); контракт оперирует только парами `eventId`/`itemId` внутри одного вызова. Нет proxy/upgrade-механизма: изменение поведения требует редеплоя и обновления `CONTRACT_ADDR` в конфиге адаптера.

---

## 3. Batch-функции (рабочий путь адаптера)

Адаптер вызывает **только** batch-функции (карта `aggregateToMethod` в `internal/chain/client.go` мапит на `batch*`).

### Точные сигнатуры

| Функция | `AggregateType` | Переход (`required → next`) |
|---|---|---|
| `batchAccept(uint256[] calldata eventIds, uint256[] calldata itemIds) external` | `receiving` | `None → Accepted` |
| `batchPutAway(uint256[] calldata eventIds, uint256[] calldata itemIds) external` | `putaway` | `Accepted → PutAway` |
| `batchPick(uint256[] calldata eventIds, uint256[] calldata itemIds) external` | `picking` | `PutAway → Picked` |
| `batchShip(uint256[] calldata eventIds, uint256[] calldata itemIds) external` | `shipping` | `Picked → Shipped` |

Все четыре `nonpayable`, делегируют во внутренний `_batchTransition(eventIds, itemIds, requiredStatus, nextStatus)`.

### Логика `_batchTransition` (МЯГКАЯ)

Параллельные массивы `eventIds`/`itemIds`; элемент `i` — пара `(eventIds[i], itemIds[i])`. Обработка по порядку:

1. `require(eventIds.length == itemIds.length, "Array length mismatch")`
2. `require(eventIds.length > 0, "Empty arrays")`
3. Для каждого `i`:
   - `_markEventIfNew(eventIds[i])`; если вернул `false` (дубликат) → `continue` (skip, без события).
   - Иначе проверка `itemStatus[itemId] == requiredStatus`. Если **не** совпадает → `emit ItemTransitionFailed(eventId, itemId, current, requiredStatus)` и `continue` — **транзакция НЕ ревертится**, валидные siblings продолжают обрабатываться.
   - При успехе: `itemStatus[itemId] = nextStatus`, `emit ItemTransition(...)`.

Цикл использует `unchecked { ++i; }` для экономии газа.

> **Один плохой элемент не роняет весь батч.** Kafka at-least-once порождает дубликаты, а один товар в неверном статусе не должен ронять валидные siblings (#44, #47). Поэтому batch-функции пропускают проблемный элемент с наблюдаемым событием вместо revert.

---

## 4. Per-item функции (тесты / CLI)

Существуют для одиночных вызовов; **адаптер их не использует**.

```solidity
function accept(uint256 eventId, uint256 itemId) external   // None → Accepted
function putAway(uint256 eventId, uint256 itemId) external  // Accepted → PutAway
function pick(uint256 eventId, uint256 itemId) external     // PutAway → Picked
function ship(uint256 eventId, uint256 itemId) external     // Picked → Shipped
```

Каждая сначала вызывает `_markEventIfNew(eventId)`; если вернул `false` (дубликат) → `return` (тихий no-op, без revert). Иначе вызывает `_transition`, который СТРОГИЙ:

```solidity
require(current == requiredStatus, "Invalid status transition");
```

> **Строгие vs мягкие — ключевая асимметрия.** Per-item: неверный статус → **revert** `"Invalid status transition"`; дубликат `eventId` → тихий no-op. Batch: оба случая → **skip** (без revert). Прямо задокументировано в `BatchMappingWMS.sol`.

---

## 5. Условия revert

> **Важно для рабочего пути.** Заголовок «нарушение порядка → revert» верен **только** для per-item функций. В продакшене адаптер ходит исключительно через `batch*`, где нарушение порядка FSM (неверный item status) **НЕ** ревертит транзакцию — элемент пропускается с событием `ItemTransitionFailed`, а вся транзакция коммитится. Единственные revert'ы в batch-пути — структурные (длина массивов).

В контракте **нет Solidity custom errors** (синтаксиса `error Foo(...)`). Все revert-условия используют `require(condition, "string")`. Полный перечень revert-строк:

| Revert-строка | Источник | Путь | Условие |
|---|---|---|---|
| `"Array length mismatch"` | `_batchTransition` | batch | `eventIds.length != itemIds.length` |
| `"Empty arrays"` | `_batchTransition` | batch | `eventIds.length == 0` |
| `"Invalid status transition"` | `_transition` | per-item | `itemStatus[itemId] != requiredStatus` |

В batch-пути нарушение порядка FSM **не** ревертит — оно эмитит `ItemTransitionFailed` (см. §6) и пропускает элемент.

---

## 6. События

### `ItemTransition`

Эмитится при **каждом успешном** переходе (и batch-, и per-item функциями).

```solidity
event ItemTransition(
    uint256 indexed eventId,
    uint256 indexed itemId,
    Status previousStatus,
    Status nextStatus,
    address actor,
    uint256 timestamp
);
```

`actor` = `msg.sender`; `timestamp` = `block.timestamp`.

### `ItemTransitionFailed`

Эмитится **только** внутри `_batchTransition`, когда текущий статус товара не совпадает с `requiredStatus`. Транзакция **НЕ** ревертится; `eventId` пропущенного элемента всё равно помечается обработанным (см. §7). Пропуск остаётся наблюдаемым on-chain для chain-status gate (#45) и reconcile-loop (#47).

```solidity
event ItemTransitionFailed(
    uint256 indexed eventId,
    uint256 indexed itemId,
    Status actualStatus,
    Status expectedStatus
);
```

> `block.timestamp` на Avalanche Subnet-EVM имеет секундную гранулярность и контролируется валидатором в допустимых пределах — это не источник точного времени.

---

## 7. Идемпотентность (`processedEventIds`)

On-chain replay-защита реализована через `mapping(uint256 => bool) processedEventIds` и внутренний примитив `_markEventIfNew`:

```solidity
function _markEventIfNew(uint256 eventId) internal returns (bool isNew) {
    if (processedEventIds[eventId]) {
        return false;          // уже обработано → вызывающий делает no-op/skip
    }
    processedEventIds[eventId] = true;
    return true;
}
```

Атомарная проверка-и-пометка: каждый `eventId` исполняется **ровно один раз**. Дубликат (cross-tx redelivery или повтор внутри одного батча) пропускается.

Это нижний из трёх уровней идемпотентности (см. [Ledger Adapter Reference §5](ledger-adapter-reference.md)): intra-batch дедуп (Go) → `INSERT ... ON CONFLICT` (PostgreSQL) → `processedEventIds` (контракт).

**Две важные тонкости** (обе подтверждены тестами и намеренны):

- **Дубликат `eventId` внутри одного батча — побеждает первый.** Первое вхождение `eventId` помечает его обработанным и выполняет переход; второе вхождение того же `eventId` (даже с другим `itemId`) тихо отбрасывается без события. Тест `test_duplicateEventId_withinBatch_skipsSecond`.
- **Poison-элемент потребляет `eventId`, даже если проверка статуса провалилась.** `_markEventIfNew` вызывается **до** проверки `itemStatus`. Поэтому при skip'е из-за неверного статуса `eventId` остаётся помеченным навсегда. На retry того же батча элемент снова пропускается (`processedEventIds[eventId] == true`) — даже если переход так и не произошёл. Тест `test_batchPoisoning_marksPoisonEventProcessed`. Это предотвращает бесконечный retry-loop, но означает, что расхождение off-chain/on-chain для такого товара перманентно без ручной коррекции.

---

## 8. Контроль доступа

**On-chain контроля доступа нет.** В контракте отсутствуют `Ownable`, `AccessControl`, модификаторы `onlyOwner`/`onlyOperator`. Все внешние функции (`accept`, `putAway`, `pick`, `ship`, `batchAccept`, `batchPutAway`, `batchPick`, `batchShip`) вызываемы любым адресом. Поле `actor` в `ItemTransition` лишь записывает `msg.sender`, но ничего не ограничивает.

Фактический барьер — **внешний**: в продакшене транзакции подписывает единственный funded EOA адаптера (приватный ключ из env `PRIVATE_KEY`). Если ключ утечёт, любой актор сможет свободно переводить товары и проигрывать произвольные пары `eventId`/`itemId`. On-chain enforcement'а нет.

---

## 9. Конвенция `UUID → uint256`

И `eventId`, и `itemId`, передаваемые в контракт, выводятся из соответствующих UUID (хранятся как `uuid` в PostgreSQL) через `internal/chain/convert.go`:

```go
func UUIDToUint256(s string) *big.Int {
    h := crypto.Keccak256([]byte(s))   // keccak256 от UTF-8 строки UUID
    return new(big.Int).SetBytes(h)    // big-endian → uint256
}
```

Входная строка — каноническое строковое представление UUID (например `"550e8400-e29b-41d4-a716-446655440000"`). Функция эквивалентна Solidity-выражению `uint256(keccak256(bytes(s)))`.

| WMS | → | Контракт |
|---|---|---|
| `event_id` (uuid) | `keccak256(bytes(uuid))` | `eventId` (uint256), ключ `processedEventIds` |
| `product_id` / `aggregate_id` (uuid) | `keccak256(bytes(uuid))` | `itemId` (uint256), ключ `itemStatus` |

> Вероятность коллизии — на уровне keccak256 (практически пренебрежима, но ненулевая; нигде явно не проверяется).

---

## 10. Гочи и инварианты (сводка)

| Гоча / инвариант | Детали |
|---|---|
| Нет on-chain access control | Любой EOA может вызвать любую функцию; барьер — только приватный ключ адаптера. |
| Нет custom errors | Только `require(cond, "string")`; три revert-строки (см. §5). |
| Строгие vs мягкие функции | Per-item ревертят на неверном статусе; batch — пропускают с событием. Адаптер использует только batch. |
| FSM-порядок — в Go, не в контракте | Контракт не знает о последовательности типов переходов. Порядок `receiving → putaway → picking → shipping` обеспечивает `fsmOrder` в `consumer/flusher.go`. Вызов `batchPutAway` до `batchAccept` не ревертится — эмитит `ItemTransitionFailed`. |
| Дубликат `eventId` в батче | Побеждает первый; второй (даже с другим `itemId`) тихо отбрасывается. |
| Poison потребляет `eventId` | `_markEventIfNew` до проверки статуса → перманентное расхождение без ручной коррекции. |
| Нет `batchId` | Батч — группировка на стороне Go; контракт знает только пары `eventId`/`itemId`. |
| `block.timestamp` | Секундная гранулярность, контролируется валидатором — не точное время. |
| Нет upgrade/proxy | Изменение → редеплой + обновление `CONTRACT_ADDR`. |
| ABI поддерживается вручную | `internal/chain/abi.go` встраивает `BatchMappingWMS.abi.json` (генерация: `forge build` + `jq`). При изменении контракта ABI нужно регенерировать — автопроверки нет. |
