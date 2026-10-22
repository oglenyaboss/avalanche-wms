# ADR-0001. Подписанные складские действия

**Статус:** принято
**Дата:** 2026-10-18
**Эпик:** #3, задача #4

## Контекст

Сейчас цепь хранит только статус товара. Все транзакции подписывает один ключ адаптера, детали операции (ячейка, заказ, исполнитель) в цепь не попадают. В outbox пишется событие на каждый товар.

Эпик меняет единицу записи на **действие**: одно подтверждение оператора (закрыл грузоместо, разложил корзину, выгрузил корзину сборки в буфер, отгрузил рейс). Оператор подписывает действие passkey (WebAuthn, P-256), контракт проверяет подпись через precompile secp256r1 по адресу `0x100`.

ADR фиксирует интерфейсы, на которые опираются задачи #8–#21. Прототип лежит в `ledger-adapter/contracts/`, тест-векторы в `testdata/signed-actions/vectors.v1.json`.

## Решение

### 1. Контракты

Два контракта, оба на solc 0.8.28, `evm_version = cancun`.

**`ConsortiumRegistry`** хранит организации и ключи.

```solidity
enum Role { None, Admin, Operator }
struct Key { bytes32 qx; bytes32 qy; bytes16 orgId; Role role; bool active; }
struct KeyAction {
    bytes16 actionId; SignedAction.Type actionType;   // KeyRegister | KeyRevoke
    bytes32 qx; bytes32 qy;                           // ключ, который регистрируют или отзывают
    bytes32 signerKeyId; WebAuthn.WebAuthnAuth auth;  // подпись админа
}

// только владелец (EOA, который задеплоил контракт)
function registerOrganization(bytes16 orgId, string name, bytes32 adminQx, bytes32 adminQy);
function registerAdminKey(bytes16 orgId, bytes32 qx, bytes32 qy);
function revokeKey(bytes32 keyId);                 // аварийный отзыв любого ключа

// кто угодно: полномочия даёт подпись админа
function applyKeyActions(KeyAction[] actions);

function getKey(bytes32 keyId) view returns (Key);
function verifySigner(bytes32 keyId, bytes32 challenge, WebAuthn.WebAuthnAuth auth)
    view returns (bytes16 orgId, Role role, SignedAction.RejectReason reason);
function organizationExists(bytes16) view returns (bool);
function actionState(bytes16) view returns (SignedAction.State);

event OrganizationRegistered(bytes16 indexed orgId, string name);
event KeyRegistered(bytes32 indexed keyId, bytes16 indexed orgId, Role role, bytes32 qx, bytes32 qy, bytes16 actionId);
event KeyRevoked(bytes32 indexed keyId, bytes16 indexed orgId, bytes16 actionId);
event ActionRejected(bytes16 indexed actionId, SignedAction.RejectReason reason);
```

Функции владельца при ошибке делают `revert`: их вызывает CLI, и откат там уместен. В событиях от владельца `actionId = 0`.

Правила ключей:
- админов регистрирует и отзывает только владелец;
- админ подписанным действием регистрирует ключ оператора в своей организации и отзывает ключ оператора своей организации; ключ админа он отозвать не может;
- отзыв окончательный, повторно тот же ключ не регистрируется;
- складские действия может подписать любой активный ключ (и админа, и оператора); ключевые действия только ключ админа.

**`SignedMappingWMS`** (FSM-контракт v2) применяет складские действия и спрашивает Registry о подписи.

```solidity
enum Status { None, Accepted, PutAway, Picked, Shipped }
struct Action {
    bytes16 actionId; SignedAction.Type actionType;   // Receive | PutAway | Pick | Ship
    uint256[] itemIds;                                // строго по возрастанию
    bytes32 detailsHash; bytes32 keyId; WebAuthn.WebAuthnAuth auth;
}

constructor(ConsortiumRegistry registry);
function applyActions(Action[] actions);
function itemStatus(uint256) view returns (Status);
function actionState(bytes16) view returns (SignedAction.State);

event ActionApplied(bytes16 indexed actionId, bytes32 indexed keyId, bytes16 indexed orgId,
                    SignedAction.Type actionType, bytes32 detailsHash, uint256 itemCount);
event ItemMoved(uint256 indexed itemId, bytes16 indexed actionId, Status status);
event ActionRejected(bytes16 indexed actionId, SignedAction.RejectReason reason, uint256 itemIndex);
```

Переходы: `Receive` None→Accepted, `PutAway` Accepted→PutAway, `Pick` PutAway→Picked, `Ship` Picked→Shipped.

`keyId`, организация и `detailsHash` пишутся один раз на действие в `ActionApplied`. На товар пишется только `ItemMoved` с `actionId`: так дешевле, а аудитор собирает картину двумя запросами (см. п. 8).

В #11 к контракту добавляются старые функции «на товар» (`batchAccept` и другие) с прежними сигнатурами. Они работают с тем же `itemStatus`, поэтому процессы переезжают по одному.

Коды перечислений общие, лежат в `SignedAction.sol` и входят в challenge, поэтому менять их нельзя:

| `Type` | код | | `Status` | код | | `State` | код |
|---|---|---|---|---|---|---|---|
| Receive | 1 | | None | 0 | | None | 0 |
| PutAway | 2 | | Accepted | 1 | | Applied | 1 |
| Pick | 3 | | PutAway | 2 | | Rejected | 2 |
| Ship | 4 | | Picked | 3 | | | |
| KeyRegister | 5 | | Shipped | 4 | | | |
| KeyRevoke | 6 | | | | | | |

### 2. Идентификаторы

- UUID в цепи передаётся как `bytes16` с сырыми 16 байтами UUID: `actionId`, `orgId` и UUID внутри деталей. Такая запись обратима: по логу видно исходный UUID.
- Исключение: `itemId = uint256(keccak256(bytes(uuid)))`, где `uuid` — каноническая строка из 36 символов в нижнем регистре. Так считает старый путь (`UUIDToUint256` в адаптере), а v2 делит с ним `itemStatus`.
- `keyId = keccak256(abi.encode(qx, qy))`, то есть keccak от 64 байт публичного ключа. Один ключ нельзя зарегистрировать дважды, даже в разных организациях.
- Для события-действия `outbox_events.event_id = actionId`.

### 3. Challenge

```
TYPEHASH    = keccak256("avalanche-wms/SignedAction/v1")
itemsHash   = keccak256(abi.encodePacked(uint256[] itemIds))   // конкатенация 32-байтных слов
NO_ITEMS    = keccak256("")                                     // для ключевых действий
challenge   = keccak256(abi.encode(TYPEHASH, bytes16 actionId, uint8 actionType, itemsHash, detailsHash))
```

Контракт пересчитывает challenge из полей действия. Подпись над другим набором товаров, другим типом или другими деталями не проходит.

`itemIds` идут строго по возрастанию числового значения. Это одновременно запрет дублей внутри действия. WMS сортирует товары при сборке черновика, адаптер порядок не меняет.

В challenge нет `chainId` и адреса контракта, намеренно. Подписанное действие описывает складской факт, а не транзакцию конкретной цепи. Его можно применить в другом леджере. Повтор внутри леджера блокирует `actionState`. Повтор в чужом консорциуме требует, чтобы там был зарегистрирован тот же ключ; passkey привязан к RP ID конкретной WMS, поэтому этот сценарий не рассматриваем.

### 4. `detailsHash`

```
detailsHash = keccak256(abi.encode(bytes32 salt, <поля типа>...))
```

| Тип | Поля после `salt` |
|---|---|
| Receive | `bytes16 cargoplaceId, bytes16 operatorId, uint64 occurredAtMs` |
| PutAway | `bytes16 storageBinId, bytes16 operatorId, uint64 occurredAtMs` |
| Pick | `bytes16 shippingBufferBinId, bytes16[] orderIds, bytes16 operatorId, uint64 occurredAtMs` |
| Ship | `bytes16 dispatchId, bytes16 shippingBufferBinId, bytes16[] orderIds, bytes16 operatorId, uint64 occurredAtMs` |
| KeyRegister, KeyRevoke | без соли: `keccak256(abi.encode(bytes32 qx, bytes32 qy))`, контракт считает сам |

Правила:
- `salt` — 32 случайных байта на действие. Хранится в БД рядом с действием. Без соли детали перебираются по хешу: ячеек и рейсов немного.
- `operatorId` — UUID пользователя WMS, который подписал. Связь «пользователь ↔ `keyId`» хранится в БД и проверяется сверкой.
- `occurredAtMs` — миллисекунды Unix. WMS обрезает время до миллисекунд до записи в доменные таблицы, иначе хеш не воспроизвести из БД (Postgres хранит микросекунды, JS Date только миллисекунды).
- `orderIds` отсортированы по возрастанию сырых байтов, без повторов.
- Список товаров в детали не входит, он уже в `itemsHash`.
- Водитель в детали отгрузки не входит, его определяет `dispatchId`.
- Каждое поле должно читаться из доменной таблицы, которую меняет действие. Сверка (#17) пересчитывает хеш из доменных таблиц, а не из копии деталей; иначе подмену ячейки в `putaways` не поймать.

### 5. WebAuthn

- Challenge передаётся в `navigator.credentials.get` как 32 байта, `userVerification: "required"`. Контракт требует флаги UP и UV (`requireUV = true`).
- Контракт проверяет `"type":"webauthn.get"`, challenge в base64url без паддинга, флаги и подпись над `sha256(authenticatorData || sha256(clientDataJSON))` (WebAuthn из OpenZeppelin 5.7.0). Origin и `rpIdHash` контракт не проверяет, их проверяет бэкенд WMS при приёме assertion.
- `challengeIndex` и `typeIndex` — позиции `"challenge":"` и `"type":"webauthn.get"` в `clientDataJSON`. Бэкенд находит их поиском подстроки.
- Подпись из браузера приходит в DER. Бэкенд переводит её в `(r, s)` и нормализует `s` к нижней половине: если `s > n/2`, то `s = n − s`. OpenZeppelin отклоняет high-s, а браузеры его не нормализуют.
- Assertion проверяется вручную по тем же правилам, что в контракте, плюс origin и `rpIdHash`. Регистрацию passkey (attestation) проверяет готовая библиотека (#9).

### 6. Семантика применения

- Батч никогда не ревертится целиком. Каждое действие применяется или отклоняется отдельно, с событием.
- Действие атомарно: сначала проверяются все товары, потом пишутся. Один товар не в том статусе отклоняет всё действие, `itemIndex` указывает на первый такой товар.
- Повтор `actionId` со статусом `Applied` или `Rejected` пропускается молча, без событий.
- `actionId` занимается только валидной подписью. Причины 1–3 из таблицы ниже пишут событие, но `actionState` не меняют: кто угодно не может «сжечь» чужой `actionId` мусорной подписью. Остальные причины пишут `Rejected`.

| Код | `RejectReason` | Где | Занимает `actionId` |
|---|---|---|---|
| 1 | UnknownKey | оба | нет |
| 2 | KeyRevoked | оба | нет |
| 3 | BadSignature | оба | нет |
| 4 | WrongType | оба | да |
| 5 | NotAdmin | Registry | да |
| 6 | KeyExists | Registry | да |
| 7 | InvalidPublicKey | Registry | да |
| 8 | WrongOrganization | Registry | да |
| 9 | TargetNotOperator | Registry | да |
| 10 | EmptyItems | FSM | да |
| 11 | ItemsNotAscending | FSM | да |
| 12 | WrongItemStatus | FSM | да |

### 7. Отзыв ключа

Действует состояние ключа на момент исполнения в цепи, льготного окна нет. Действие, подписанное до отзыва и дошедшее до цепи после него, отклоняется с `KeyRevoked`.

С БД это не расходится, потому что порядок в цепи совпадает с порядком коммитов в БД (п. 9). Для этого WMS сериализует подписание и отзыв:
- транзакция складского действия берёт строку ключа `FOR SHARE` и проверяет, что ключ активен в цепи;
- транзакция отзыва берёт ту же строку `FOR UPDATE`.

Тогда действие, закоммиченное до отзыва, идёт в outbox раньше отзыва и в цепи применяется. Действие после отзыва бэкенд не примет. Контракт отклонит только действие, отправленное в обход бэкенда.

Ключ, который ещё не активен в цепи (админ не подтвердил или подтверждение не дошло), WMS для складских действий не принимает. Контракт такие действия отклоняет с `UnknownKey`.

### 8. Как аудитор читает цепь

1. Товар → `itemId` → `getLogs(ItemMoved, topic1 = itemId)` → список `actionId`.
2. `getLogs(ActionApplied, topic1 ∈ actionIds)` → `keyId`, `orgId`, `detailsHash`, номер блока.
3. `registry.getKey(keyId)` → `qx`, `qy`, организация, активность.
4. Для проверки подписи: `eth_getTransactionByHash` из лога, декодировать `applyActions`, найти действие по `actionId`, проверить WebAuthn в браузере (WebCrypto P-256) по правилам п. 5.
5. Детали и соль берутся из бэкенда, `detailsHash` пересчитывается в браузере.

### 9. Outbox, Kafka, адаптер

- Событие-действие пишется в ту же `outbox_events` и тот же топик `wms.events.v1`, что и старые события. Топик остаётся с одной партицией: порядок действий в цепи равен порядку коммитов в БД, и на этом держится п. 7. Старые и новые события не разносятся по разным топикам, иначе в переходный период приёмка действием и раскладка старым путём могут прийти в цепь не в том порядке.
- Строка события-действия: `event_id = actionId`, `aggregate_id = actionId`, `aggregate_type = 'signed_action'`, `event_type = 'wms.action.v1'`, `payload_hash = challenge` (hex), новая колонка `payload jsonb` с конвертом ниже. EventRouter берёт payload из `payload`. У старых событий `payload` пустой, адаптер их value не читает. Что старый путь при этом не ломается, проверяет #10.
- Конверт:

```json
{
  "version": 1,
  "actionId": "01929c5e-6f00-7000-8000-000000001002",
  "actionType": 1,
  "itemIds": ["<uuid товара>", "..."],
  "detailsHash": "0x…",
  "subjectKey": { "qx": "0x…", "qy": "0x…" },
  "keyId": "0x…",
  "webauthn": {
    "authenticatorData": "0x…",
    "clientDataJSON": "{\"type\":\"webauthn.get\",…}",
    "challengeIndex": 23,
    "typeIndex": 1,
    "r": "0x…",
    "s": "0x…"
  }
}
```

  `itemIds` — UUID товаров в порядке возрастания `itemId`. Адаптер переводит их в `uint256` и порядок не меняет. `detailsHash` есть только у складских действий, `subjectKey` только у ключевых, `itemIds` у ключевых пустой.
- Адаптер работает с одним ключом отправки. Порядок nonce одного signer'а сохраняет порядок Kafka.
- Окно адаптера режется на последовательные отрезки одного вида: старые события, складские действия (`SignedMappingWMS.applyActions`), ключевые действия (`ConsortiumRegistry.applyKeyActions`). Отрезки отправляются по порядку. Внутри отрезка старых событий остаётся текущая группировка по этапам. Внутри отрезков действий порядок не меняется: отзыв ключа нельзя переставить раньше действий, которые были до него.
- Батч действий в одной транзакции ограничен 120 000 байт calldata. Других ограничений не нужно: при таком объёме газ не превышает ~100 млн при лимите блока 200 млн.
- Статус действия в WMS: отправлено → `Applied` или `Rejected` с причиной (по событиям receipt).

После #19 переходный legacy-сегмент (старые события на товар) и старые per-product функции контракта (`batchAccept` и другие, `processedEventIds`) удалены: адаптер принимает только `aggregate_type = signed_action` (остальное — в DLQ), и окно режется на два вида отрезков — складские и ключевые действия.

### 10. Размер действия

Узел subnet-evm отклоняет транзакцию больше 131 072 байт (`oversized data`). Действие стоит ~930 байт плюс 32 байта на товар, поэтому одно действие вмещает не больше ~4 066 товаров. Газ ограничивает позже: приёмка 4 060 товаров — 103,8 млн газа, 52% блока.

Лимит WMS — **2 000 товаров на действие**. Это ~65 КБ calldata, остаётся место упаковать в транзакцию ещё действия.

Операцию крупнее лимита бэкенд режет сам:
- черновик возвращает N частей по ≤ 2 000 товаров (товары уже отсортированы по `itemId`, части идут подряд);
- у каждой части свой `actionId`, своя соль и свой challenge, поля деталей одинаковые;
- оператор подтверждает каждую часть (N касаний подряд), клиент отправляет все assertion одним запросом;
- бэкенд проверяет все подписи и в одной транзакции БД применяет операцию и пишет N событий-действий.

В БД операция атомарна. В цепи каждая часть атомарна сама по себе; все части проходят одну проверку состояния, поэтому расхождение возможно только при подмене в обход бэкенда, и его ловит сверка. На практике до лимита доходит только отгрузка крупного буфера.

### 11. Сборка и зависимости

- solc 0.8.28, `evm_version = cancun`. WebAuthn из OpenZeppelin 5.7 требует ≥ 0.8.24 и использует `mcopy`. На subnet-evm 0.8.0 cancun работает: контракты задеплоены и исполнены на живом узле.
- OpenZeppelin Contracts 5.7.0 вендорится в `ledger-adapter/contracts/deps/openzeppelin-contracts/`: только 10 файлов, которые импортируют контракты, без изменений, с лицензией и версией. Деплой не ходит в сеть, `lib/` для деплоя не нужен. Каталог назван `deps`, потому что `vendor/` игнорирует корневой `.gitignore`.
- `forge create` из чистой копии без `lib/` деплоит все три контракта. У `forge create` флаг `--constructor-args` должен стоять последним: он забирает все следующие аргументы.
- В `forge test` precompile `0x100` нет, OpenZeppelin переходит на проверку на Solidity: тесты проходят, но газ подписи там ~290 тыс. вместо ~7 тыс. Газ мерить только на узле.

## Замеры газа

Узел subnet-evm 0.8.0 (avalanchego 1.14.0), лимит блока 200 млн газа. Скрипт `ledger-adapter/contracts/script/GasBench.s.sol`, газ из receipt. Все транзакции успешны, отклонений нет. Действие приёмки пишет новые слоты (0→1), раскладка перезаписывает (1→2). Сборка и отгрузка стоят как раскладка.

| Транзакция | Товаров всего | Приёмка, газ/товар | Раскладка, газ/товар |
|---|---:|---:|---:|
| 1 действие × 1 товар | 1 | 108 605 | 91 555 |
| 1 × 10 | 10 | 33 856 | 16 763 |
| 1 × 100 | 100 | 26 380 | 9 280 |
| 1 × 1 000 | 1 000 | 25 634 | 8 534 |
| 1 × 4 060 | 4 060 | 25 577 | 8 477 |
| 10 действий × 1 | 10 | 81 300 | 64 251 |
| 10 × 10 | 100 | 31 125 | 14 030 |
| 10 × 100 | 1 000 | 26 108 | 9 009 |
| Старый путь, батч из 10 событий | 10 | 51 165 | 34 069 |
| Старый путь, 100 | 100 | 49 141 | 32 040 |
| Старый путь, 1 000 | 1 000 | 48 937 | 31 838 |

Выводы:
- Действие в батче стоит ~56 тыс. газа сверху: подпись, ключ из Registry, запись `actionState`, событие, calldata подписи. Товар внутри действия стоит ~25,5 тыс. при приёмке и ~8,5 тыс. при остальных переходах.
- Начиная с 3 товаров в действии подписанный путь дешевле старого. Старый путь платит на каждый товар запись `processedEventIds` и событие с шестью полями.
- Потолок блока для приёмки: ~4 100 товаров старым путём, ~7 600 действиями по 100 товаров, ~2 460 действиями по одному товару.
- Precompile используется на самом деле. Проверка подписи из тест-вектора через `eth_call` на `0x100` даёт `1`, подменённая — пустой ответ, ~6,9 тыс. газа. Та же транзакция в симуляции Foundry без precompile стоит на ~290 тыс. газа больше.

## Последствия

**Плюсы.** В цепи есть исполнитель, организация и хеш деталей каждого действия. Писать может только держатель ключа, зарегистрированного по цепочке «владелец → админ → оператор». На крупных действиях газ на товар ниже прежнего. Строк в outbox на порядки меньше, а Postgres был узким местом конвейера (замер — #20).

**Минусы и потолки** (помечены `ponytail:` там, где это код):
- Владелец консорциума — корень доверия, его ключ не передаётся. Если понадобится смена владельца, нужна двухшаговая передача.
- Организацию нельзя деактивировать целиком: владелец отзывает её ключи по одному.
- Одна партиция Kafka и один signer ограничивают масштаб. Путь роста — партиция и signer на склад; тогда отзыв ключа придётся рассылать во все партиции или проверять по времени.
- Действие из одного товара дороже старого события (~81 тыс. против ~51 тыс.). Для склада это нормально, единичных подтверждений мало.
- Аудитор проверяет подпись по calldata транзакции, поэтому RPC-узел должен отдавать транзакции по хешу. Обычный полный узел отдаёт.

## Тест-векторы

`testdata/signed-actions/vectors.v1.json`: шесть действий одного жизненного цикла, подписанных тестовым ключом (`signer.privateKey` — только для тестов). Ключевое действие регистрирует второй ключ, четыре складских проводят три товара от приёмки до отгрузки, последнее отзывает второй ключ. Для каждого действия даны входные данные, `itemsHash`, `detailsHash`, `challenge` и assertion WebAuthn. `clientDataJSON` записан в hex (`clientDataJSONHex`), чтобы байты не поменялись при сериализации.

Проверки:
- `SignedActionVectors.t.sol` применяет все векторы к реальным контрактам: все действия `Applied`.
- Независимая сверка на Go (go-ethereum `abi.Arguments.Pack`, `crypto/ecdsa`) сходится по всем полям и подписям.

Go и TypeScript реализации (#10, #17, #18) обязаны проходить эти векторы. Перегенерация: `forge script script/SignedActionVectors.s.sol` из `ledger-adapter/contracts`.

## Как воспроизвести

```bash
cd ledger-adapter/contracts
git clone --depth 1 https://github.com/foundry-rs/forge-std lib/forge-std
forge test
forge script script/GasBench.s.sol:GasBench --rpc-url <RPC> --private-key <ключ с балансом> --broadcast --slow
PROBE_ITEMS=4060 forge script script/GasBench.s.sol:GasBench ...   # предельное действие
```
