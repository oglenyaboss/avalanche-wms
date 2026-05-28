# Roadmap развития WMS + Blockchain

Составлен 2026-05-28. Текущее состояние: MVP пройден, e2e test suite покрывает основные сценарии, найдены и задокументированы дефекты (BUGHUNT.md), заведены issues #44–#49.

---

## Текущий спринт (issues #44–#49)

Багфиксы по результатам bug hunt. Подробности в issues на GitLab.

| Issue | Суть | Статус |
|-------|------|--------|
| [#49](https://git.miem.hse.ru/2340/blockchain_project/-/issues/49) | 6 quick-fix багов WMS-слоя (auth, error mapping, null handling) | TODO |
| [#44](https://git.miem.hse.ru/2340/blockchain_project/-/issues/44) | Contract idempotency + reconcile loop (S2/N1/N2/N9) | TODO |
| [#47](https://git.miem.hse.ru/2340/blockchain_project/-/issues/47) | Batch poisoning — graceful skip в контракте (S3) | TODO |
| [#45](https://git.miem.hse.ru/2340/blockchain_project/-/issues/45) | Chain status gate — WMS проверяет chain на каждом шаге FSM (S1) | TODO |
| [#46](https://git.miem.hse.ru/2340/blockchain_project/-/issues/46) | Cart persistence — убрать in-memory map из assembly/putaway | TODO |
| [#48](https://git.miem.hse.ru/2340/blockchain_project/-/issues/48) | Partial shipping — статус PARTIALLY_SHIPPED | TODO |

---

## Направления дальнейшего развития

### 1. Reverse Outbox — полная двусторонняя связь Chain↔WMS

**Проблема:** сейчас связь однонаправленная (WMS → chain). Chain status gate (#45) блокирует операции при расхождении, но не восстанавливает корректное состояние автоматически.

**Решение:** adapter при FAILED/revert пишет в обратный Kafka topic. Новый consumer в WMS слушает и выполняет компенсацию:
- Отмена receiving → продукт возвращается в NOT_RECEIVED
- Отмена putaway → продукт возвращается в буфер
- Отмена shipping → заказ возвращается в ASSEMBLED

**Что даёт:** полностью автоматическое восстановление консистентности без ручного вмешательства. Система становится self-healing.

**Сложность:** высокая. Compensation logic для каждого из 4 модулей, обработка edge cases (продукт уже в следующем статусе), тестирование всех комбинаций.

---

### 2. Мульти-складская архитектура

**Проблема:** текущая система работает с одним warehouse. Нет scoping операций по складу, нет cross-warehouse валидации, нет оператор↔склад привязки.

**Решение поэтапно:**

**Этап A — Warehouse scoping:**
- Привязка оператора к warehouse (таблица `operator_warehouses`, контекст в JWT)
- Фильтр по warehouse_id во всех модулях: receiving, putaway, assembly, shipping
- Cross-warehouse валидация: продукт из warehouse A не может быть размещён в bin warehouse B

**Этап B — Inter-warehouse transfers:**
- Новый модуль transfer: перемещение продуктов между складами
- Transfer events на chain (новый transition в контракте)
- Промежуточные статусы: IN_TRANSIT, RECEIVED_AT_DESTINATION

**Этап C — Мульти-warehouse dashboard:**
- Агрегация по складам: stock levels, throughput, bottlenecks
- Cross-warehouse аналитика для планирования

**Что даёт:** масштабирование на сеть складов. Каждый склад — автономная единица с blockchain-верифицированными операциями.

---

### 3. DLQ Management & Replay

**Проблема:** DLQ (Dead Letter Queue) накапливает failed events, но нет инструментов для анализа, replay и мониторинга.

**Решение:**
- Admin UI для просмотра DLQ: event details, failure reason, chain status
- Selective replay: переотправка отдельных events после исправления root cause
- Автоматический retry с backoff для transient failures
- Алертинг: уведомление при накоплении DLQ выше порога

**Что даёт:** операционную прозрачность. Сейчас FAILED events уходят в DLQ и забываются. С replay-механизмом можно восстанавливать данные после инцидентов.

---

### 4. Return-to-Storage & Order Cancellation

**Проблема:** нет flow для возврата товара из shipping buffer обратно в хранение, и нет механизма отмены заказа или его части.

**Решение:**
- Новые endpoints: `POST /shipping/return-to-storage`, `POST /orders/{id}/cancel`
- Reverse state transitions: READY_TO_SHIP → STORED, с outbox event
- Новый chain transition: `returnToStorage(eventId, itemId)` — PutAway-статус на chain восстанавливается или добавляется Returned-статус
- Обработка PARTIALLY_SHIPPED: отмена остатка, финализация заказа

**Что даёт:** полный lifecycle заказа. Сейчас товар может двигаться только вперёд (receiving → shipping). Return flow закрывает кейсы: ошибка оператора, повреждённый товар, отмена заказа клиентом.

---

### 5. Audit & Compliance Reporting

**Проблема:** blockchain хранит полную историю transitions, но нет инструментов для извлечения и представления этих данных.

**Решение:**
- Chain indexer: сервис, читающий events из контракта и складывающий в аналитическую БД
- Compliance reports: кто, когда, какой товар, какой transition, подтверждён ли на chain
- Экспорт в PDF/Excel для внешнего аудита
- Diff-отчёты: расхождения между DB и chain за период

**Что даёт:** реализация ключевого value proposition блокчейна — immutable audit trail. Без reporting layer блокчейн-интеграция остаётся техническим решением; с ним — бизнес-инструментом для compliance и доверия контрагентов.

---

### 6. Мобильное приложение для операторов

**Проблема:** операторы работают через web-интерфейс. На складе удобнее мобильное приложение с камерой для QR-сканирования.

**Решение:**
- React Native / Flutter приложение
- Интеграция камеры для QR-сканирования (заменяет ручной ввод)
- Offline-first: локальная очередь операций при потере сети, sync при восстановлении
- Push-уведомления: chain confirmation, ошибки, новые задачи

**Что даёт:** UX оператора кардинально улучшается. QR-scan через камеру вместо ручного ввода, работа при нестабильном Wi-Fi на складе.

---

## Приоритизация

| Приоритет | Направление | Обоснование |
|-----------|-------------|-------------|
| **Высокий** | Reverse Outbox | Закрывает главную архитектурную проблему — одностороннюю связь |
| **Высокий** | DLQ Management | Операционная необходимость для production |
| **Средний** | Return-to-Storage | Полный lifecycle заказа |
| **Средний** | Мульти-склад (этап A) | Масштабирование бизнеса |
| **Средний** | Audit & Compliance | Бизнес-ценность blockchain |
| **Низкий** | Мульти-склад (этапы B, C) | Актуально при росте бизнеса |
| **Низкий** | Мобильное приложение | UX improvement, не блокер |

> **Примечания:**
> - Chain status visibility (chain_synced в UI, timeline событий по продукту) — не отдельное направление, а часть фронтенда. Закладывается при разработке UI на основе API из #45.
> - Horizontal scaling (stateless WMS, adapter partitioning, DB replicas) — необходимое условие для достижения 1500 TPS, реализуется в рамках текущей работы, а не как отдельное направление.
