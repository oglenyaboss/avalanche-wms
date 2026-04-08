# BatchMappingWMS — контекст для основной репы

**Дата:** 2026-03-23
**Источник:** нагрузочное тестирование на Avalanche Subnet-EVM
**Статус:** выбранный подход для MVP

---

## Что это и зачем

Блокчейн используется как **независимый верификатор операций** (не просто лог). Смарт-контракт реализует конечный автомат (FSM) для каждого товара и **отказывает** (`revert`) при нарушении порядка операций — это гарантия на уровне протокола, не кода приложения.

Три исследованных подхода:

| Подход | Принцип | TPS (peak, 100M gasLimit) | Бизнес-правила on-chain |
|---|---|:---:|:---:|
| **HashOnly** | хранит `eventId → keccak256(payload)` | 1,837 | ❌ |
| **MappingWMS** | FSM: `itemId → Status` + проверка переходов | 902 | ✅ |
| **MerkleRootWMS** | глобальный `stateRoot` + Merkle proof | 0.23 | ✅ |

MerkleRootWMS отклонён: из-за глобального `stateRoot` только 1 tx/block может пройти, что даёт 0.23 TPS — на 3 порядка ниже требования 1,500 TPS.

**Выбран: BatchMappingWMS** — MappingWMS + batch-функции (`batchAccept`, `batchPutAway`, `batchPick`, `batchShip`).

---

## Контракт: структура

```solidity
contract BatchMappingWMS {
    enum Status { None, Accepted, PutAway, Picked, Shipped }

    mapping(uint256 => Status) public itemStatus;
    mapping(uint256 => bool)   public processedEventIds;

    // Одиночные функции (для совместимости)
    function accept(uint256 eventId, uint256 itemId) external { ... }
    function putAway(uint256 eventId, uint256 itemId) external { ... }
    function pick(uint256 eventId, uint256 itemId) external { ... }
    function ship(uint256 eventId, uint256 itemId) external { ... }

    // Batch-функции (основной путь в продакшене)
    function batchAccept(uint256[] calldata eventIds, uint256[] calldata itemIds) external { ... }
    function batchPutAway(uint256[] calldata eventIds, uint256[] calldata itemIds) external { ... }
    function batchPick(uint256[] calldata eventIds, uint256[] calldata itemIds) external { ... }
    function batchShip(uint256[] calldata eventIds, uint256[] calldata itemIds) external { ... }
}
```

**Event log** (каждая операция):
```
ItemTransition(eventId, itemId, previousStatus, nextStatus, actor, block.timestamp)
```

---

## Маппинг: WMS таблица → Kafka → контракт

| WMS таблица (PG) | Kafka topic | Контракт | On-chain переход |
|---|---|---|---|
| `receiving_gate` / `receiving_table` | `wms.receiving.v1` | `batchAccept` | None → Accepted |
| `putaways` | `wms.putaway.v1` | `batchPutAway` | Accepted → PutAway |
| `assembly_tasks` | `wms.picking.v1` | `batchPick` | PutAway → Picked |
| `shippings` | `wms.shipping.v1` | `batchShip` | Picked → Shipped |

**Идентификаторы:**
- `eventId` ← `event_id` из `outbox_events` (uuid → uint256 через `uint256(keccak256(event_id))` или sequence)
- `itemId` ← `product_id` из `products` (аналогично)

---

## Поток данных

```
1. UI → WMS (Go):        оператор выполняет операцию
2. WMS → PG:             COMMIT (operations table + outbox_events) — одна транзакция
3. Debezium → Kafka:     CDC публикует outbox запись в topic
4. Kafka → Ledger Adapter: consumer накапливает batch (10–25 сообщений)
5. Ledger Adapter → BatchMappingWMS: batchAccept/batchPutAway/batchPick/batchShip
6. Блокчейн → Ledger Adapter: receipt + event logs (ItemTransition × N)
7. Ledger Adapter → PG:  UPDATE onchain_events SET status='COMMITTED', tx_hash='0x...'
8. UI:                   показывает on-chain статус по product_id
```

**Таблица `onchain_events` не меняется.** При batch: несколько `event_id` получают один `tx_hash` — это корректно.

---

## Производительность (измеренные числа)

**Платформа:** Avalanche Subnet-EVM v0.8.0, Snowman consensus, 5 нод, 1 хост (MacBook)
**Load generator:** Go v2, fire-and-forget, 24 горутины, 2 RPC endpoints

### Batch=10 (основной режим)

| gasLimit | Block TPS (items/s) | Peak TPS (items/s) | Max block fill |
|---|:---:|:---:|:---:|
| **100M** | 1,230 | **1,870** | 99.8% |
| **200M** | 1,820 | **2,510** | 100% |

### Газовая эффективность

| Режим | Gas/item | vs single |
|---|:---:|:---:|
| Single MappingWMS | ~69,100 | — |
| Batch=10 | ~31,000 | **-55%** |
| Batch=25 (расчёт) | ~26,000 | **-62%** |

### Ответ на требование 1,500 TPS

- `gasLimit=100M + batch=10` → 1,870 items/sec → **+25% от требования** ✅
- `gasLimit=200M + batch=10` → 2,510 items/sec → **+67% запас**

### Эволюция от первого теста до финального

| Клиент | gasLimit | Платформа | Контракт | Items/sec |
|---|---|---|---|:---:|
| TypeScript serial | 30M | Besu QBFT | MappingWMS | 240 |
| Go serial | 100M | Avalanche | MappingWMS | 698 |
| Go parallel | 100M | Avalanche | MappingWMS | 902 |
| Go parallel | 200M | Avalanche | MappingWMS | 1,545 |
| Go parallel | 100M | Avalanche | **Batch=10** | 1,870 |
| Go parallel | 200M | Avalanche | **Batch=10** | **2,510** |

**12.7x рост** от первого теста до финального.

---

## Ключевые свойства

**Idempotency (двойная защита):**
1. `processedEventIds[eventId] == true` → revert `DuplicateEventId`
2. State machine: нельзя дважды выполнить `accept` для одного item (статус уже `Accepted`)

**Batch revert:** если один item в batch инвалиден — вся транзакция ревертнёт. Митигация: валидация в Ledger Adapter перед отправкой + DLQ для проблемных events.

**Параллелизуемость:** разные items полностью независимы (разные slots в mapping). Никаких глобальных блокировок.

**State growth не влияет на TPS.** Solidity mapping = O(1) hash table; gas фиксирован EIP-2200/2929 независимо от размера state. Измерено: 0.6% деградация при 100k items в state (в пределах погрешности).

**1 sender account достаточен.** Разница 1 account vs 24 accounts: 2,472 vs 2,516 items/sec (-1.7%, погрешность). Bottleneck — gas/block, не отправка.

---

## Конфигурация для MVP (1,500 TPS)

```
Контракт:      BatchMappingWMS (batch=10)
Platform:      Avalanche Subnet-EVM
gasLimit:      100,000,000 (100M)   ← достаточно для 1,870 items/sec
               200,000,000 (200M)   ← рекомендуемое (запас 67%)
Validators:    5 нод (minimum 4 для BFT)
RPC:           4+ endpoints за load balancer
Block period:  ~2s (on-demand, Snowman consensus)
```

**Железо на ноду (gasLimit=200M, измерено):**
- RAM: ~1.3 GB per node (5 нод = ~6.5 GB суммарно)
- CPU: 4+ cores (EVM single-threaded, cores нужны для consensus/networking)
- Disk: SSD обязателен (state trie = random reads)

---

## Аудит (как проверять)

```sql
-- Все on-chain операции по товару
SELECT oe.event_id, oe.tx_hash, oe.status, oe.aggregate_type, p.occurred_at
FROM onchain_events oe
JOIN putaways p ON p.event_id = oe.event_id
WHERE p.product_id = 'uuid-xxx'
  AND oe.status = 'COMMITTED'
ORDER BY p.occurred_at;
```

Для глубокой проверки: взять `tx_hash` → запросить receipt с блокчейна → декодировать `ItemTransition` events → сравнить itemId/status с PG. Расхождение = индикатор подмены данных в БД.

---

## Масштабирование при росте нагрузки

| Потребность | Решение | Потолок |
|---|---|:---:|
| > 2,000 items/sec | gasLimit → 200M | 2,510 items/sec |
| > 3,000 items/sec | gasLimit=200M + batch=25 | ~4,000 items/sec |
| > 5,000 items/sec | несколько subnet'ов (по складу/региону) | линейный |
| Рост количества items | не влияет (O(1) access) | — |
