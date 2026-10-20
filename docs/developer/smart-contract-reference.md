# Контракты — справочник разработчика

**Версия:** 2.0
**Дата:** 2026-10-20
**Аудитория:** команда, принимающая проект на сопровождение (hand-off)

Документ описывает on-chain слой: библиотеку `SignedAction`, реестр `ConsortiumRegistry` и FSM-контракт `SignedMappingWMS` — точные сигнатуры, события, коды отклонений, storage. Всё приведено по исходникам `ledger-adapter/contracts/*.sol` (Solidity **0.8.28**, EVM-таргет **cancun**, оптимизатор 200 runs — `ledger-adapter/contracts/foundry.toml`).

Модель подписанных действий (challenge, detailsHash, WebAuthn, семантика применения, замеры газа) зафиксирована в [ADR-0001](../adr/0001-signed-actions.md) — здесь она пересказана ровно настолько, насколько нужно для чтения кода. Как контракты встроены в пайплайн адаптера — [Ledger Adapter Reference](ledger-adapter-reference.md); общий маппинг операций WMS → блокчейн — [Маппинг WMS → Блокчейн](../integration/blockchain-mapping.md).

> `BatchMappingWMS.sol` — старый контракт v1 («событие на товар») — сохранён в репозитории только как базовая линия гео-бенчмарка (`deploy/geo`); адаптер его не вызывает.

---

## 1. Библиотека `SignedAction`

Общие типы и хеши. Коды перечислений входят в challenge, поэтому **менять их нельзя** (ADR-0001 §1).

```solidity
enum Type { None, Receive, PutAway, Pick, Ship, KeyRegister, KeyRevoke }
enum State { None, Applied, Rejected }
enum RejectReason { None, UnknownKey, KeyRevoked, BadSignature, WrongType, NotAdmin,
                    KeyExists, InvalidPublicKey, WrongOrganization, TargetNotOperator,
                    EmptyItems, ItemsNotAscending, WrongItemStatus }
```

| `Type` | код | | `State` | код | | `RejectReason` | код |
|---|:---:|---|---|:---:|---|---|:---:|
| `None` | 0 | | `None` | 0 | | `None` | 0 |
| `Receive` | 1 | | `Applied` | 1 | | `UnknownKey` | 1 |
| `PutAway` | 2 | | `Rejected` | 2 | | `KeyRevoked` | 2 |
| `Pick` | 3 | | | | | `BadSignature` | 3 |
| `Ship` | 4 | | | | | `WrongType` | 4 |
| `KeyRegister` | 5 | | | | | `NotAdmin` | 5 |
| `KeyRevoke` | 6 | | | | | `KeyExists` | 6 |
| | | | | | | `InvalidPublicKey` | 7 |
| | | | | | | `WrongOrganization` | 8 |
| | | | | | | `TargetNotOperator` | 9 |
| | | | | | | `EmptyItems` | 10 |
| | | | | | | `ItemsNotAscending` | 11 |
| | | | | | | `WrongItemStatus` | 12 |

Функции библиотеки (`internal pure`):

```solidity
bytes32 internal constant TYPEHASH = keccak256("avalanche-wms/SignedAction/v1");
bytes32 internal constant NO_ITEMS = keccak256("");

function challenge(bytes16 actionId, Type actionType, bytes32 itemsHash, bytes32 detailsHash)
    returns (bytes32);            // keccak256(abi.encode(TYPEHASH, actionId, actionType, itemsHash, detailsHash))
function hashItems(uint256[] calldata itemIds) returns (bytes32); // keccak256(abi.encodePacked(itemIds))
function keyId(bytes32 qx, bytes32 qy) returns (bytes32);         // keccak256(abi.encode(qx, qy))
```

---

## 2. `ConsortiumRegistry` — реестр организаций и ключей

**Источник:** `ledger-adapter/contracts/ConsortiumRegistry.sol`.

### 2.1. Storage

```solidity
address public immutable owner;                                   // деплоер, корень доверия
mapping(bytes16 orgId => bool) public organizationExists;
mapping(bytes32 keyId => Key) private _keys;
mapping(bytes16 actionId => SignedAction.State) public actionState;

struct Key { bytes32 qx; bytes32 qy; bytes16 orgId; Role role; bool active; }
enum Role { None, Admin, Operator }
```

### 2.2. Функции владельца (revert при ошибке)

Вызывает CLI владельца (`registry-cli`), поэтому откат там уместен. Во всех событиях от владельца `actionId = 0`.

| Функция | Ошибка revert | Событие |
|---|---|---|
| `registerOrganization(bytes16 orgId, string name, bytes32 adminQx, bytes32 adminQy) external onlyOwner` | `OrganizationExists(orgId)`, а также `KeyAlreadyRegistered`/`InvalidPublicKey` из регистрации ключа админа | `OrganizationRegistered(orgId, name)` + `KeyRegistered(админ)` |
| `registerAdminKey(bytes16 orgId, bytes32 qx, bytes32 qy) external onlyOwner` — дополнительный/заменяющий ключ админа | `UnknownOrganization(orgId)`, `KeyAlreadyRegistered(keyId)`, `InvalidPublicKey()` | `KeyRegistered(…, Role.Admin, …, actionId = 0)` |
| `revokeKey(bytes32 keyId) external onlyOwner` — аварийный отзыв любого ключа | `UnknownKey(keyId)`; уже отозванный — тихий no-op (без события) | `KeyRevoked(keyId, orgId, actionId = 0)` |

Ошибки: `NotOwner()`, `OrganizationExists(bytes16)`, `UnknownOrganization(bytes16)`, `KeyAlreadyRegistered(bytes32)`, `InvalidPublicKey()`, `UnknownKey(bytes32)`.

### 2.3. `applyKeyActions(KeyAction[] calldata actions) external`

Ключевые действия: регистрация/отзыв ключа оператора подписью админа. Транзакция **не ревертится** из-за одного отклонённого действия — каждое применяется или отклоняется отдельно (см. §5).

```solidity
struct KeyAction {
    bytes16 actionId; SignedAction.Type actionType;   // KeyRegister | KeyRevoke
    bytes32 qx; bytes32 qy;                           // ключ, который регистрируют или отзывают
    bytes32 signerKeyId; WebAuthn.WebAuthnAuth auth;  // подпись админа
}
```

Порядок применения одного действия:

1. `actionState[actionId] != None` → тихий пропуск (идемпотентность, без событий).
2. Challenge считается контрактом: `challenge(actionId, actionType, NO_ITEMS, keccak256(abi.encode(qx, qy)))` — у ключевых действий нет соли и списка товаров.
3. `verifySigner(signerKeyId, challenge, auth)` — подпись должна принадлежать **активному ключу реестра** (роль проверяется на шаге 4); иначе `ActionRejected` с причиной 1–3 и **без записи `actionState`**.
4. Эффект (`_keyActionEffect`) — проверка полномочий и цели; успех → `actionState = Applied` + событие, отказ → `actionState = Rejected` + `ActionRejected`.

Правила ключей:

- админов регистрирует и отзывает только владелец;
- админ подписанным действием регистрирует ключ оператора **своей** организации и отзывает ключ оператора **своей** организации; ключ админа он отозвать не может (`TargetNotOperator`);
- отзыв окончательный: повторно тот же ключ не регистрируется (`KeyExists` — проверка по `keyId`, глобально по консорциуму, а не по организации);
- регистрируемый ключ проверяется `P256.isValidPublicKey(qx, qy)`.

### 2.4. View-функции

| Функция | Возвращает |
|---|---|
| `getKey(bytes32 keyId) view returns (Key memory)` | структура ключа (qx, qy, orgId, role, active) |
| `verifySigner(bytes32 keyId, bytes32 challenge, WebAuthnAuth calldata auth) view returns (bytes16 orgId, Role role, RejectReason reason)` | проверка подписи активного ключа (`requireUV`); `role == None` → `UnknownKey` |
| `organizationExists(bytes16 orgId) view returns (bool)` | признак организации |
| `actionState(bytes16 actionId) view returns (State)` | исход подписанного ключевого действия |

### 2.5. События

```solidity
event OrganizationRegistered(bytes16 indexed orgId, string name);
event KeyRegistered(bytes32 indexed keyId, bytes16 indexed orgId, Role role,
                    bytes32 qx, bytes32 qy, bytes16 actionId);
event KeyRevoked(bytes32 indexed keyId, bytes16 indexed orgId, bytes16 actionId);
event ActionRejected(bytes16 indexed actionId, RejectReason reason);
```

---

## 3. `SignedMappingWMS` — FSM складских действий

**Источник:** `ledger-adapter/contracts/SignedMappingWMS.sol`. Спрашивает реестр о подписанте, применяется адаптером (`applyActions`).

### 3.1. Storage

```solidity
ConsortiumRegistry public immutable registry;   // задаётся в конструкторе
mapping(uint256 itemId => Status) public itemStatus;
mapping(bytes16 actionId => SignedAction.State) public actionState;

enum Status { None, Accepted, PutAway, Picked, Shipped }   // 0..4

struct Action {
    bytes16 actionId; SignedAction.Type actionType;   // Receive | PutAway | Pick | Ship
    uint256[] itemIds;                                // строго по возрастанию
    bytes32 detailsHash; bytes32 keyId; WebAuthn.WebAuthnAuth auth;
}
```

Состояние контракта — ровно два mapping'а и immutable-адрес реестра; нет proxy/upgrade: изменение поведения требует редеплоя.

### 3.2. `applyActions(Action[] calldata actions) external`

На каждое действие (`_apply`):

1. `actionState[actionId] != None` → тихий пропуск (без событий).
2. Challenge пересчитывается из полей действия: `challenge(actionId, actionType, hashItems(itemIds), detailsHash)`. Подпись над другим набором товаров, другим типом или другими деталями не проходит.
3. `registry.verifySigner(keyId, challenge, auth)` — подписант любой активный ключ реестра (админ или оператор); причины 1–3 → `ActionRejected(reason, 0)` без записи `actionState`.
4. `_transition(actionType)`: неизвестный тип → `WrongType`. Переходы:

```
Receive: None → Accepted        PutAway: Accepted → PutAway
Pick:    PutAway → Picked       Ship:    Picked → Shipped
```

5. `_checkItems` (**view**, до записи): пустой список → `EmptyItems`; `ids[i] <= ids[i-1]` → `ItemsNotAscending(i)`; статус товара ≠ требуемому → `WrongItemStatus(i)`. Один товар не в том статусе отклоняет **всё действие**.
6. Успех: `actionState = Applied`, затем для каждого товара `itemStatus[id] = to` и `ItemMoved`. В конце — `ActionApplied`.

### 3.3. View-функции

| Функция | Возвращает |
|---|---|
| `itemStatus(uint256 itemId) view returns (Status)` | FSM-статус товара |
| `actionState(bytes16 actionId) view returns (State)` | исход подписанного складского действия |
| `registry() view returns (ConsortiumRegistry)` | адрес реестра |

### 3.4. События

```solidity
event ActionApplied(bytes16 indexed actionId, bytes32 indexed keyId, bytes16 indexed orgId,
                    Type actionType, bytes32 detailsHash, uint256 itemCount);
event ItemMoved(uint256 indexed itemId, bytes16 indexed actionId, Status status);
event ActionRejected(bytes16 indexed actionId, RejectReason reason, uint256 itemIndex);
```

`keyId`, организация и `detailsHash` пишутся один раз на действие (`ActionApplied`); на товар — только `ItemMoved` с `actionId`: так дешевле, а аудитор собирает картину двумя запросами (ADR-0001 §8). `itemIndex` в `ActionRejected` указывает на первый товар, заблокировавший действие.

---

## 4. Идентификаторы

| Значение | Правило | Где считать |
|---|---|---|
| `actionId`, `orgId` | сырые 16 байт UUID как `bytes16` — запись обратима, по логу виден исходный UUID | WMS, адаптер, аудит |
| `itemId` | `uint256(keccak256(bytes(uuid)))`, uuid — каноническая строка из 36 символов в нижнем регистре | `UUIDToUint256` в `ledger-adapter/internal/chain/convert.go`; WMS и аудит считают так же |
| `keyId` | `keccak256(abi.encode(qx, qy))` — keccak от 64 байт публичного ключа | контракт (`SignedAction.keyId`), WMS, CLI |

Один ключ нельзя зарегистрировать дважды, даже в разных организациях (`KeyExists` по глобальному `keyId`).

---

## 5. Семантика применения и коды отклонений

- Батч никогда не ревертится целиком: каждое действие применяется или отклоняется отдельно, с событием.
- Действие атомарно: сначала проверяются все товары, потом пишутся.
- Повтор `actionId` со статусом `Applied` или `Rejected` пропускается молча, без событий.
- Причины 1–3 (`UnknownKey`, `KeyRevoked`, `BadSignature`), найденные проверкой подписи (`verifySigner`), пишут событие `ActionRejected`, но `actionState` **не меняют**: кто угодно не может «сжечь» чужой `actionId` мусорной подписью. Те же коды из эффекта ключевого действия (неизвестный или уже отозванный ключ-цель `KeyRevoke`) — обычный `Rejected`. Остальные причины пишут `Rejected`.

| Код | `RejectReason` | Где проверяется | Занимает `actionId` |
|:---:|---|---|:---:|
| 1 | `UnknownKey` | оба контракта | нет (подпись); да — `KeyRevoke` неизвестного ключа |
| 2 | `KeyRevoked` | оба контракта | нет (подпись); да — повторный `KeyRevoke` |
| 3 | `BadSignature` | оба контракта | нет |
| 4 | `WrongType` | оба контракта | да |
| 5 | `NotAdmin` | Registry | да |
| 6 | `KeyExists` | Registry | да |
| 7 | `InvalidPublicKey` | Registry | да |
| 8 | `WrongOrganization` | Registry | да |
| 9 | `TargetNotOperator` | Registry | да |
| 10 | `EmptyItems` | SignedMappingWMS | да |
| 11 | `ItemsNotAscending` | SignedMappingWMS | да |
| 12 | `WrongItemStatus` | SignedMappingWMS | да |

Действует состояние ключа на момент исполнения в цепи, льготного окна нет: действие, подписанное до отзыва и дошедшее до цепи после него, отклоняется с `KeyRevoked`. Порядок в цепи равен порядку коммитов в БД (одна партиция Kafka + один signer адаптера), поэтому с БД это не расходится — ADR-0001 §7.

---

## 6. Challenge и `detailsHash`

```
challenge = keccak256(abi.encode(TYPEHASH, bytes16 actionId, uint8 actionType, itemsHash, detailsHash))
```

В challenge нет `chainId` и адреса контракта — подписанное действие описывает складской факт и переносимо между леджерами. `detailsHash = keccak256(abi.encode(bytes32 salt, <поля типа>…))` — поля по типам действия (ячейка, рейс, заказы, оператор, `occurredAtMs`); у ключевых действий деталей нет. Полную таблицу полей и правила соли/сортировки — [ADR-0001 §4](../adr/0001-signed-actions.md).

## 7. Проверка подписи (WebAuthn)

- Проверка — `WebAuthn.verify(abi.encodePacked(challenge), auth, qx, qy, true)` из OpenZeppelin 5.7 (`ledger-adapter/contracts/deps/openzeppelin-contracts/contracts/utils/cryptography/WebAuthn.sol`): флаги UP и UV (`requireUV = true`), проверка `"type":"webauthn.get"`, challenge в base64url без паддинга, подпись над `sha256(authenticatorData || sha256(clientDataJSON))`.
- Подпись P-256 исполняется precompile secp256r1 по адресу `0x100` (RIP-7212) — на узле subnet-evm он работает (ADR-0001, «Замеры газа»), а в `forge test` его нет: там OpenZeppelin проверяет подпись на Solidity и газ подписи другой (§8). Origin и `rpIdHash` контракт не проверяет: их проверяет бэкенд WMS при приёме assertion.
- `challengeIndex`/`typeIndex` — позиции `"challenge":"` и `"type":"webauthn.get"` в `clientDataJSON`.
- `s` подписи должна быть нормализована к нижней половине (OpenZeppelin отклоняет high-s); нормализацию делает бэкенд WMS.

## 8. Тесты и тест-векторы

- `ledger-adapter/contracts/SignedActions.t.sol` — поведение контрактов (переходы FSM, отклонения, ключевые действия).
- `ledger-adapter/contracts/SignedActionVectors.t.sol` — применяет к реальным контрактам тест-векторы `testdata/signed-actions/vectors.v1.json` (шесть действий одного жизненного цикла); независимая сверка на Go — `wms/internal/signedaction`.
- Запуск и подготовка (`forge-std` в `ledger-adapter/contracts/lib/`) — [README «Контракты»](README.md).

## 9. Гочи и инварианты (сводка)

| Инвариант | Детали |
|---|---|
| Батч не ревертится из-за одного действия | Каждое действие применяется/отклоняется отдельно, событием. В `applyActions`/`applyKeyActions` revert не предусмотрен вовсе; revert-ошибки — только у функций владельца реестра (§2.2). |
| Действие атомарно | Сначала `_checkItems` по всем товарам, потом запись; один товар не в том статусе отклоняет всё действие (`itemIndex`). |
| Повтор `actionId` | `actionState != None` → тихий пропуск без событий. |
| Причины 1–3 не занимают `actionId` | Мусорная подпись не может «сжечь» чужой `actionId`. |
| Владелец — корень доверия | `owner` immutable, задаётся деплоем; смена владельца не предусмотрена. |
| Нет proxy/upgrade | Изменение → редеплой + обновление адресов в `shared_state`. |
| Порядок enum'ов входит в подпись | `Type`/`State`/`RejectReason` менять нельзя — сломаются подписи и разбор логов. |
| ABI обновляется вручную | `ledger-adapter/internal/chain/*.abi.json` и `wms/internal/audit/*.abi.json` — копии из `forge build` (команды — [README «Контракты»](README.md)). |
