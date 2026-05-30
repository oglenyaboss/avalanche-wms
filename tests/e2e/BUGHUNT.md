# Бэклог E2E баг-ханта (линия MR 59)

Ветка `test/wms-outbound-flow-e2e-onchain-fsm`. Подготовлено 2026-05-25 параллельным
проходом из 5 агентов — по одному агенту на слой (receiving / putaway / assembly /
shipping+dispatches / ledger-adapter+contract), — который разметил FSM каждого модуля,
пути ошибок и инварианты, сверил их с существующим набором `tests/e2e/` и проранжировал
латентные дефекты.

Этот файл — устойчивый результат того прохода. В нём перечислены **все** находки, что
выпущено в этом батче и что отложено с предложенной формой сценария. Все ссылки на строки —
относительно worktree на момент прохода; перепроверяйте перед действием.

**Серьёзность:** CRITICAL = потеря данных / тихое расхождение DB↔chain · HIGH = функциональный
баг или застрявшее состояние · MEDIUM = поддерживаемость/устойчивость · LOW = мелочь/стиль.

> ⚠️ Рецепты воспроизведения изначально выведены из анализа кода, **не** из живого прогона
> (на момент написания набор не запускался — конфликт хост-портов держал :5432/:9092).
> **Обновление 2026-05-26:** набор теперь запускается end-to-end (фикс герметичности
> `JWT_SECRET` на чистом checkout), и три off-gate сценария `09`/`10`/`11` **проверены
> вживую** — каждый завершается с ненулевым кодом, воспроизводя S2/S3/N1 соответственно
> (см. §1). Заглушки `t.Skip` `*_pendingFix` остаются только на уровне анализа кода;
> проверяйте перед тем, как доверять.

---

## 0. Статус решения (обновлено 2026-05-30)

После прохода бэклог разобрали на заведённые issue **#44–#50** и починили в четырёх MR —
**все влиты в `dev`** (`!61`/`!62`/`!63`/`!64`).

> ⚠️ Фиксы **в `dev`, ещё не в `master`**. GitLab автозакрывает issue только при мердже в
> дефолтную ветку (`master`), поэтому **#44–#48 на трекере всё ещё _открыты_**; они закроются
> при `dev → master`. «✅» ниже означает **починено в `dev`**, а не выкачено в прод.

**MR → issue → какие дефекты починены:**

| MR | Issue(s) | Закрытые ID из BUGHUNT |
|---|---|---|
| `!61` | #49 | Receiving-1, Shipping-4, Shipping-5, Putaway-4, Dispatches-1, Assembly-6, Assembly-7 |
| `!62` | #50 | Receiving-3/4/5, Putaway-6, Shipping-6, Shipping-8, Assembly-2, Assembly-4, Assembly-5, N4, N7 |
| `!63` | #44, #47 | S2, N1, N2, N9 (#44) · S3 (#47) |
| `!64` | #45, #46, #48 | S1 *(частично)* (#45) · Assembly-1 (#46) · Shipping-2 (#48) |

**Построчный статус** — ✅ починено в `dev` · ⚪ mitigated / by design (фикс не нужен) ·
🔴 ещё открыто:

| ID | Сев. | Статус | Решение |
|---|---|---|---|
| S2 | CRIT | ✅ | Идемпотентный контракт + reconcile-цикл — #44 / `!63` |
| N1 | CRIT | ✅ | Reconcile по таймауту receipt, без преждевременного FAILED — #44 / `!63` |
| Assembly-1 | CRIT | ✅ | Корзины оператора выводятся из БД, а не из памяти процесса — #46 / `!64` |
| S1 | CRIT | 🔴 | **Частично.** Каскад остановлен в источнике (gate на putaway+pick, #45/`!64`). Открыто: ship-gate + read-path `chain_synced` → **#52**; reverse-outbox → **#41** |
| S3 | HIGH | ✅ | Поэлементная изоляция батча в контракте — #47 / `!63` |
| N2 | HIGH | ✅ | Идемпотентность контракта (дубликат eventId больше не ревёртит) — #44 / `!63` |
| N9 | HIGH | ✅ | Дедуп внутри батча в `filterAndMarkPending` — #44 / `!63` |
| Shipping-2 | HIGH | ✅ | Статус `PARTIALLY_SHIPPED` + gate по статусу заказа — #48 / `!64` |
| Receiving-1 | HIGH | ✅ | Фильтр по CLOSED-боксу в close-cargoplace — #49 / `!61` |
| Shipping-5 | HIGH | ✅ | `pgx.ErrNoRows` → 404 — #49 / `!61` |
| Putaway-4 | HIGH | ✅ | Guard `section IS NOT NULL` — #49 / `!61` |
| Dispatches-1 | HIGH | ✅ | `requireOperator` в 3 хэндлерах dispatch — #49 / `!61` |
| Assembly-6 | HIGH | ✅ | nullable `bins.section` сканируется через NullString — #49 / `!61` |
| Assembly-2 | HIGH* | ✅ | Частичный unique-индекс `(product_id) WHERE status='PENDING'` — #50 / `!62` |
| Shipping-4 | MED | ✅ | Маппинг ошибки already-departed — #49 / `!61` |
| Assembly-7 | MED | ✅ | `ErrSKUNotFound` → 404 — #49 / `!61` |
| Receiving-3 | MED | ✅ | Чтение статуса перенесено внутрь tx — #50 / `!62` |
| Receiving-4 | MED | ✅ | Guard от пустого лога при повторном ScanBuffer — #50 / `!62` |
| Receiving-5 | MED | ✅ | Audit-лог на NOT_RECEIVED — #50 / `!62` |
| Putaway-6 | MED | ✅ | Проверка вместимости bin `volume` — #50 / `!62` |
| Shipping-6 | MED | ✅ | Сериализованная нумерация dispatch_code — #49/#50 |
| N4 | MED | ✅ | MarkFailed до публикации в DLQ — #50 / `!62` |
| Assembly-4 | LOW | ✅ | `ErrOrderNotNew` в `mapServiceError` — #50 / `!62` |
| Assembly-5 | LOW | ✅ | Тайбрейкер FIFO `ORDER BY created_at, product_id` — #50 / `!62` |
| Shipping-8 | LOW | ✅ | Допуск (tolerance) на `scheduled_at` — #50 / `!62` |
| N7 | LOW | ✅ | Readiness-проба pool/Kafka/RPC в `/health` — #50 / `!62` |
| Putaway-3 | MED | ⚪ | SQL-guard теперь покрыт e2e-фикстурами — #50 |
| N5 | MED | ⚪ | Безвредно после идемпотентности контракта (#44) |
| N6 | MED | ⚪ | Дубликаты при DLQ-replay скипаются on-chain после #44 |
| N8 | LOW | ⚪ | Поверхность S2 (resubmittable) закрыта #44 |
| Putaway-1 | — | ⚪ | By design — авторитетный `product_ids[]` держит фронтенд |
| Putaway-2 | LOW | ⚪ | By design — каждый товар валидируется независимо |
| **Shipping-3** | HIGH | 🔴 | **Открыто, не заведено.** Скоупинг depart-with-unshipped: два AT_GATE dispatch'а на один destination могут растащить чужой груз. Баг корректности, релевантен и для одного склада. |
| **Receiving-2** | HIGH | 🔴 | **Открыто, не заведено.** Over-receipt: ghost-товар (SKU не из `expected_cargoplace_skus`) попадает на chain. Нужно продуктовое решение (lenient-by-design?). |
| **N3** | HIGH | 🔴 | **Открыто, не заведено.** Cross-aggregate head-of-line blocking в `Flush`; нужна per-aggregate изоляция (редизайн флашера). |
| **Putaway-5** | HIGH | 🔴 | **Открыто, не заведено.** Нет межскладской проверки. В скоупе, только если multi-warehouse — цель; для проверки нужна фикстура со 2-м складом. |
| **Assembly-3** | MED | 🔴 | **Открыто, не заведено.** Аллокация матчит только по `sku_id`, без скоупа по складу. Та же multi-warehouse-оговорка, что у Putaway-5. |

**Открытая работа после этого батча:**
- **Хвост S1** — #52 (ship-gate + read-path; дёшево, defense-in-depth, т.к. источник уже под gate'ом) и #41 (reverse-outbox; архитектурный).
- **#51** — follow-up по ревью идемпотентности в ledger-adapter (nits после `!63`).
- **Shipping-3 · Receiving-2** (HIGH, WMS) и **N3** (HIGH, адаптер — редизайн) — пока не заведены как issue.
- **Putaway-5 · Assembly-3** — multi-warehouse; в скоупе, только если multi-warehouse — цель.

> §2/§3 ниже — анализ **на момент прохода (2026-05-25)**. Этот §0 — текущий overlay
> статуса; при расхождении приоритет у §0.

---

## 1. Выпущено в этом батче

**Зелёные тесты, добавленные в гейт `-tags=e2e` (покрытие корректного поведения):**

| Тест | Закрывает |
|---|---|
| `newMultiProductFixture` + переписанный `TestMultiProduct_AllocatePickAssemble` | Баг харнесса **T11**: старый тест съедал фиксированный сток сида 5+5 и проходил только на свежем стенде. Теперь per-run и перезапускаем в `E2E_USE_EXISTING_STACK`. |
| `TestPartialShipment_MofN` | Spot-отгрузка M из N (контракт БД): заказ/dispatch завершаются только после отгрузки всех товаров; `buffer_remaining` отслеживает остаток. On-chain изоляция отложена — см. §3. |
| `TestAllocate_InsufficientStock` | Контракт нехватки при allocate: HTTP 200 + `insufficient_orders` (не 422), всё-или-ничего (заказ NEW, юнит STORED/не привязан, 0 задач). |
| `TestAuth_OperatorOnlyEndpoints` | Дыра в авторизации: без токена→401 + CUSTOMER→403 для **putaway / assembly / shipping** (раньше только receiving). |

**Off-gate воспроизведения багов (ненулевой код, пока баг жив; 0 после фикса):**

| Артефакт | Доказывает | Статус |
|---|---|---|
| `scenarios/09-s2-crash-recovery.sh` | **S2** (CRITICAL) — закрывает слепое пятно в `07` | ✅ Проверено вживую 2026-05-26 (exit 1, S2 подтверждён: переотправленная SENT-строка → FAILED, при этом COMMITTED on-chain) |
| `scenarios/10-s3-batch-poisoning.sh` | **S3** (HIGH) fan-out на стороне адаптера | ✅ Проверено вживую 2026-05-26 (`BATCH_TIMEOUT=5s`; exit 1, S3 подтверждён: валидный сосед утащен в FAILED) |
| `scenarios/11-receipt-timeout.sh` | **N1** (CRITICAL) | ✅ Проверено вживую 2026-05-26 (`RECEIPT_POLL_TIMEOUT=1ms`; exit 1, N1 подтверждён: смайненная tx оставила в БД FAILED) |

**Документационные заглушки `t.Skip`, добавленные в `known_failures_test.go`** (та же конвенция,
что у S1/S2/S3): `TestAssemblyCartLostOnRestart_pendingFix`,
`TestAdapterN1_ReceiptTimeoutDivergence_pendingFix`, `TestShippingShipBeforeAssembled_pendingFix`,
`TestReceivingOpenBoxReachesChain_pendingFix`.

**Аннотации ложной уверенности** (выглядели так, будто покрывают S2; на деле нет):
`scenarios/07-idempotency-restart.sh` (доводит до COMMITTED до переотправки) и
`ledger-adapter/internal/consumer/flusher_test.go::TestFlusher_StrandedPending_GetsRetried`
(mock-чейн без `_requireNewEvent`).

---

## 2. Продуктовые дефекты (настоящие — не баги тестов)

Отсортировано по серьёзности. «Отслеживается как» → устойчивый артефакт. S1/S2/S3 найдены в
более раннем ревью MR-59; N*/BUG-* — новые из этого прохода.

> **Статус:** текущее решение собрано в §0 (2026-05-30). Таблица ниже — анализ на момент
> прохода; колонка «Отслеживается как» отражает то, что было заведено на момент прохода.

| ID | Сев. | Дефект | Ключевая ссылка | Отслеживается как |
|---|---|---|---|---|
| S1 | CRITICAL | WMS никогда не читает `onchain_events.status`; реверт/DLQ на chain оставляет заказ SHIPPED без компенсации (одностороннее связывание). **Модель данных починена:** миграция 0008 создала VIEW'ы (`v_*_with_chain`), джойнящие через `event_id`; reverse-outbox отложен в issue #41. Go-код по-прежнему не запрашивает chain_status. | `shipping/service.go` Ship; VIEW'ы в `0008_chain_views.up.sql`; нет читателя в `wms/**/*.go` | Заглушка `TestS1_*`; модель данных готова (#24 влит), поведение в ожидании (#41); **→ [#45](https://git.miem.hse.ru/2340/blockchain_project/-/issues/45)** |
| S2 | CRITICAL | Crash-recovery переотправляет PENDING/SENT события → ревёрт контракта `Duplicate eventId` → событие ошибочно FAILED+DLQ, хотя оно прошло. | `consumer/flusher.go:155-180` (скипает только COMMITTED/FAILED), `:117` | Заглушка `TestS2_*` + `scenarios/09`; **→ [#44](https://git.miem.hse.ru/2340/blockchain_project/-/issues/44)** |
| N1 | CRITICAL | Таймаут `WaitReceipt` помечает событие FAILED, пока tx ещё майнится → tx майнится → в БД FAILED против COMMITTED on-chain навсегда. Краш не нужен. | `consumer/flusher.go:124-129`; `RECEIPT_POLL_TIMEOUT` по умолчанию 30s | Заглушка `TestAdapterN1_*` + `scenarios/11`; **→ [#44](https://git.miem.hse.ru/2340/blockchain_project/-/issues/44)** |
| Assembly-1 | CRITICAL | Корзина подбора живёт только в памяти процесса; ни один эндпоинт её не восстанавливает. Рестарт WMS между Pick и ScanShippingBuffer навсегда оставляет товары ASSEMBLED / заказ ALLOCATED. Тот же паттерн в putaway (риск ниже — источник истины фронтенд, корзина лишь счётчик). | `assembly/service.go:18,266-270,286,376`; `putaway/service.go:15-22` | Заглушка `TestAssemblyCartLostOnRestart_*`; **→ [#46](https://git.miem.hse.ru/2340/blockchain_project/-/issues/46)** |
| S3 | HIGH | Один плохой элемент ревёртит всю batch-tx; адаптер затем FAIL+DLQ для всех валидных соседей. | цикл `_batchTransition` в контракте; `consumer/flusher.go:143-153` | Заглушка `TestS3_*` + `scenarios/10`; **→ [#47](https://git.miem.hse.ru/2340/blockchain_project/-/issues/47)** |
| Shipping-2 | HIGH | Ship гейтит только по статусу товара, никогда по статусу заказа. Отгрузка готовых товаров заказа, ещё не перешедшего в ASSEMBLED, оставляет заказ застрявшим в ALLOCATED (никогда не достигнет SHIPPED). Решение: добавить статус PARTIALLY_SHIPPED, разрешить частичную отгрузку, SHIPPED только когда buffer_remaining=0. | `shipping/service.go:147`; `UpdateOrdersShippedConditional` (только status='ASSEMBLED') | Заглушка `TestShippingShipBeforeAssembled_*` (чистый HTTP-репро); **→ [#48](https://git.miem.hse.ru/2340/blockchain_project/-/issues/48)** |
| Receiving-1 | HIGH | `close-cargoplace` эмитит receiving-событие для каждого RECEIVED-товара независимо от статуса бокса, но ScanBuffer двигает только товары из CLOSED-бокса. Товар из OPEN-бокса получает Accepted(1) on-chain с `bin_id` NULL — на chain, но физически неразмещаемый. | `receiving/repository.go` `listProductIDsByCargoplaceTx` (без фильтра по боксу) против `ScanBufferWithLog` (`b.status='CLOSED'`) | Заглушка `TestReceivingOpenBoxReachesChain_*` (чистый HTTP-репро); **→ [#49](https://git.miem.hse.ru/2340/blockchain_project/-/issues/49)** |
| Shipping-5 | HIGH | `GET /dispatches/{id}` для несуществующего id возвращает 503, а не 404 (`pgx.ErrNoRows` не замаплен). | `dispatches/repository.go:121-142`; `dispatches/handler.go:128-131` | **→ [#49](https://git.miem.hse.ru/2340/blockchain_project/-/issues/49)** |
| Shipping-3 | HIGH | Скоупинг depart-with-unshipped: `CountReadyToShipProductsInBuffer` охватывает все bin'ы destination'а; несколько AT_GATE dispatch'ей на один destination могут забрать груз друг друга. | `shipping/repository.go:383-398` | Отложено — §3 |
| Shipping-4 | MEDIUM | Два конкурентных вызова Ship, опустошающих один буфер: один отправляется (200), другой попадает на `UpdateDispatchDeparted` с 0 строк → 409 `DISPATCH_NOT_AT_GATE` (не 500, как было заявлено изначально — ошибка ЗАмаплена). Отгруженные товары закоммичены, но оператор получает вводящую в заблуждение ошибку «dispatch not at gate» вместо «already departed». | `shipping/repository.go:400-415` | **→ [#49](https://git.miem.hse.ru/2340/blockchain_project/-/issues/49)** |
| Putaway-4 | HIGH | Bin с NULL-секцией принимается как место хранения (`section IS DISTINCT FROM 'BUFFER'` истинно для NULL) → товары хранятся в неклассифицированном bin'е, on-chain продвигается до PutAway. | `putaway/repository.go:82-97`; `bins.section` nullable (`0001:216`) | **→ [#49](https://git.miem.hse.ru/2340/blockchain_project/-/issues/49)** |
| Putaway-5 | HIGH | Нет межскладской проверки: товар из буфера склада A можно разместить в bin склада B. | `putaway/repository.go:62-97` | Отложено — §3 (нужен 2-й склад) |
| Receiving-2 | HIGH | Over-receipt: сканирование SKU, которого нет в `expected_cargoplace_skus`, создаёт ghost-товар, попадающий на chain; сводка закрытия скрывает превышение. | `receiving/service.go:498`; нет джойна к `expected_cargoplace_skus` | Отложено — §3 (возможно, lenient-by-design; нужно продуктовое решение) |
| Assembly-2 | HIGH* | Повторная аллокация может породить дублирующую PENDING-задачу + дублирующее picking outbox/chain-событие для одного товара. *Условно: сейчас блокируется guard'ом заказа `status='NEW'`; нет подстраховки в виде частичного unique-индекса `(product_id) WHERE status='PENDING'`. | `assembly_tasks` unique только по `event_id` (`0001:408`) | Отложено — §3 |
| N2 | HIGH | Гонка nonce/broadcast: транзиентная ошибка SendTransaction после попадания tx в mempool оставляет строку PENDING; ретрай переиспользует eventId → `Duplicate eventId` → путь S2. | `chain/client.go:160,197-199` | **→ [#44](https://git.miem.hse.ru/2340/blockchain_project/-/issues/44)** |
| N3 | HIGH | Cross-aggregate head-of-line blocking: упавший ранний sub-batch прерывает весь `Flush`; не связанный поздний aggregate голодает/ретраится бесконечно. | `consumer/flusher.go:79-88` | Отложено — §3 |
| Receiving-3 | MEDIUM | TOCTOU: `ScanCargoplace`/`ScanTableCargoplace` читают статус shipment/cargoplace вне tx; конкурентное закрытие может рассогласовать выдаваемый код ошибки. | `receiving/service.go:189-194,343-365` | Отложено |
| Receiving-4 | MEDIUM | `ScanBuffer` можно вызывать повторно; после первого вызова он двигает 0 строк, но всё равно пишет log-строку, без guard'а. | `receiving/repository.go:596-623` | Отложено |
| Receiving-5 | MEDIUM | `MarkExpectedAsNotReceived` не пишет audit-лог для перехода NOT_RECEIVED по каждому cargoplace. | `receiving/repository.go:286-300` | Отложено |
| Putaway-3 | MEDIUM | Guard статуса размещения только в SQL; юнит-тесты мокают `WithTx` как passthrough, поэтому реальный lock/rollback БД юнит-тестами не покрыт (новые e2e-тесты на фикстурах теперь бьют в реальный путь). | `putaway/service.go:154-175`; `putaway/service_test.go:56-61` | Частично mitigated |
| Putaway-6 | MEDIUM | Нет проверки вместимости bin'а (`bins.volume`) при putaway. | `putaway/repository.go` | Отложено |
| Shipping-6 | MEDIUM | Генерация dispatch code (`count+1`) не атомарна при конкуренции → ложные 503 из-за нарушения unique. | `dispatches/repository.go:67-77` | Отложено |
| N4 | MEDIUM | Публикация в DLQ происходит до MarkFailed; если MarkFailed падает, переотправка дублирует публикацию в DLQ. | `consumer/flusher.go:144-148` | Отложено |
| N5 | MEDIUM | `finalFlush` при шатдауне использует `context.Background()`, переотправляя in-flight SENT-строки по пути S2 на каждом штатном деплое/рестарте. | `consumer/consumer.go:170-186` | Отложено |
| N6 | MEDIUM | DLQ replay: переигранные сообщения снова попадают на терминальные/дублирующие пути; починенный-и-переигранный сосед S3 будет молча пропущен (FAILED терминален). | `dlq/producer.go`; `consumer/flusher.go:167` | Отложено |
| Assembly-3 | MEDIUM | Нет скоупа по складу в аллокации товара (матчит только по `sku_id`). | `assembly/repository.go:113-120` | Отложено (нужен 2-й склад) |
| Putaway-1 | — | **Дизайн-наблюдение, не заведено как дефект:** `scan-storage-bin` принимает `product_ids` напрямую и никогда не сверяется с in-memory корзиной — любой оператор может разместить любой RECEIVED-в-буфере товар. Это **намеренно** (корзина — UI-счётчик; авторитетный `product_ids[]` держит фронтенд — см. комментарий в `putaway/service.go:18-19`). Отмечено здесь как соображение авторизации: нет серверной проверки, что размещающий оператор — тот же, кто сканировал товары. | `putaway/service.go:135,210-229` | Только наблюдение |
| Putaway-2 | LOW | Смешивание между буферами: один вызов `scan-storage-bin` может разместить товары из разных буферных bin'ов (каждый валидируется независимо). | `putaway/service.go:151` | Наблюдение |
| Assembly-4 | LOW | `ErrOrderNotNew` нет в `mapServiceError` → всплыло бы как 500 (сейчас недостижимо за in-tx guard'ом). | `assembly/handler.go:234-256` | Отложено |
| Assembly-5 | LOW | FIFO-аллокация `ORDER BY created_at` без тайбрейкера → недетерминированное назначение bin'а на юнит при равенстве `created_at`. | `assembly/repository.go:118` | Отложено |
| Shipping-8 | LOW | Проверка `scheduled_at` на прошлое использует wall clock без допуска/инъекции. | `dispatches/handler.go:99` | Отложено |
| N7 | LOW | `/health` всегда возвращает 200 — нет readiness-пробы pool/Kafka/RPC, поэтому заклинивший адаптер всё ещё рапортует healthy. | `ledger-adapter/internal/handler/handler.go:24-33` | Отложено |
| N8 | LOW | Строки `InsertPending` не откатываются при ошибке в середине цикла, расширяя поверхность S2 (переотправляемое нетерминальное состояние). | `consumer/flusher.go:172-176` | Отложено |
| Dispatches-1 | HIGH | Хэндлеры dispatches (`GetDispatches`, `NewDispatch`, `GetDispatchByID`) никогда не вызывают `requireOperator`; JWT-middleware валидирует токен, но пользователь с ролью CUSTOMER может листать, создавать и смотреть dispatch'и. Все остальные модули (receiving/putaway/assembly/shipping) форсят OPERATOR-only через `requireOperator`. | `dispatches/handler.go:37-141` (нет вызова `requireOperator`) против `assembly/handler.go:218-231` | **→ [#49](https://git.miem.hse.ru/2340/blockchain_project/-/issues/49)** |
| N9 | HIGH | Дубликат внутри батча: если Kafka переотправляет тот же `event_id` в пределах одного окна flush, `filterAndMarkPending` добавляет его дважды (первая итерация: `InsertPending`; вторая: `Exists=true, status=PENDING` → тоже добавлен). `buildBatchArgs` выдаёт `[id, id]` → ревёрт контракта `Duplicate eventId` → весь батч FAILED+DLQ. Усугубляет S2 при восстановлении. | `consumer/flusher.go:155-180` | **→ [#44](https://git.miem.hse.ru/2340/blockchain_project/-/issues/44)** |
| Assembly-6 | HIGH | `GetBinSectionByID` сканирует nullable `bins.section` в обычный `string`; bin с NULL-секцией вызывает ошибку скана `pgx` → 500 INTERNAL_ERROR при аллокации корзины. Связано с Putaway-4 (принимается bin с NULL-секцией). | `assembly/repository.go:175-186` | **→ [#49](https://git.miem.hse.ru/2340/blockchain_project/-/issues/49)** |
| Assembly-7 | MEDIUM | `GetSKUByID` возвращает `ErrInsufficientStock` на `pgx.ErrNoRows` → allocate с несуществующим SKU возвращает 422 `INSUFFICIENT_STOCK` вместо 404 `SKU_NOT_FOUND`. | `assembly/repository.go:140-155` | **→ [#49](https://git.miem.hse.ru/2340/blockchain_project/-/issues/49)** |

---

## 3. Пробелы в покрытии — корректное поведение

> **Статус (2026-05-30):** решение собрано в §0; строки ниже — на момент прохода.

> **Обновление 2026-05-26 (батч 2):** guard-тесты receiving, putaway, assembly и
> shipping/dispatches ниже реализованы как зелёные тесты в гейте `-tags=e2e`. Оставшиеся
> пробелы помечены ниже *(всё ещё отложено)*.

**Receiving** — ✅ выпущено в `receiving_guards_test.go`
- ✅ `TestReceiving_GateFlow` — scan-ttn → scan-cargoplace → accept-shipment (с кастомной `newGateFixture`)
- ✅ `TestReceiving_ScanTTN_AlreadyClosed` — 409 SHIPMENT_ALREADY_CLOSED
- ✅ `TestReceiving_ScanCargoplace_AlreadyReceived` — 409 CARGOPLACE_ALREADY_RECEIVED
- ✅ `TestReceiving_ScanQR_Duplicate` — 409 QR_ALREADY_EXISTS
- ✅ `TestReceiving_ScanBox_ClosedBox` — 409 BOX_NOT_OPEN (через scan-sku на закрытом боксе)
- ✅ `TestReceiving_ScanSKU_UnknownBarcode` — 404 BARCODE_NOT_FOUND
- ✅ `TestReceiving_ScanBuffer_NonBufferBin` — 400 BIN_NOT_BUFFER
- *(всё ещё отложено)* бокс из другого cargoplace → 400 `BOX_NOT_IN_CARGOPLACE`

**Putaway** — ✅ выпущено в `putaway_guards_test.go`
- ✅ `TestPutaway_DoublePlace_Conflict` — 409 PRODUCT_NOT_IN_BUFFER, дельта outbox 0
- ✅ `TestPutaway_PlaceIntoBufferBin_Rejected` — bin'ы BUFFER/SHIPPING_BUFFER → 404 STORAGE_BIN_NOT_FOUND
- ✅ `TestPutaway_ScanProduct_AlreadyStored` — 409 PRODUCT_NOT_RECEIVED
- ✅ `TestPutaway_PlaceWithBogusProduct_Rollback` — откат tx, P1 остаётся RECEIVED, нет outbox
- *(всё ещё отложено)* **Putaway-4 (дефект):** bin с NULL-секцией сейчас принимается → должно быть 404

**Assembly** — ✅ выпущено в `assembly_guards_test.go`
- ✅ `TestAssembly_CartIsolation` — op1 подбирает, op2 CART_EMPTY, op1 завершает
- ✅ `TestAssembly_DoubleAllocate` — второй allocate → allocated_orders=0, число задач не меняется
- *(всё ещё отложено)* Гонка конкурентного allocate (нужны параллельные горутины)

**Shipping / dispatches** — ✅ выпущено в `shipping_guards_test.go`
- ✅ `TestShipping_DoubleShip_Conflict` — 409 PRODUCT_NOT_IN_BUFFER
- ✅ `TestShipping_ScanDriver_Idempotent` — 200, arrived_at не меняется
- ✅ `TestShipping_ScanDriver_AlreadyDeparted` — 409 DISPATCH_ALREADY_DEPARTED
- ✅ `TestShipping_DestinationMismatch` — 409 DESTINATION_MISMATCH (с ad-hoc вторым destination)
- ✅ `TestDispatches_GetByID_NotFound` — документирует Shipping-5 (сейчас 503, должно быть 404)

**Адаптер (off-gate сценарии)** — *(всё ещё отложено)*
- Воспроизведения N1/N2/N3/N4/N5 (см. ссылки §2). У N1 есть заглушка-сценарий (`11`); остальным нужна инъекция сбоев RPC/chaos.

**Доработки фикстур (on-chain прайминг)** — *(всё ещё отложено)*
- `newMultiProductFixture` вставляет товары сразу в STORED без предшествующих receiving/putaway-событий, поэтому их on-chain `itemStatus` = `None`, и любые порождаемые ими picking/shipping-события ревёртят (падают в FAILED). Поэтому `TestMultiProduct_AllocatePickAssemble` и `TestPartialShipment_MofN` проверяют **только состояние БД**. Чтобы проверить ещё и on-chain частичную изоляцию (отгружено → Shipped(4), пока остаток остаётся Picked(3)), расширьте фикстуру: эмитить по receiving- и putaway-outbox-событию на товар и `waitForOnchainCommitted` для обоих перед возвратом (прайминг каждого товара до PutAway on-chain). Это также убирает остаток FAILED-строк, который фикстура сейчас чистит. Та же предпосылка нужна перед добавлением on-chain-проверок в `TestShippingShipBeforeAssembled_*` (его проверка БД — заказ застрял в ALLOCATED — не требует chain).

---

## 4. Заметки по методологии

- Merge-гейт (`-tags=e2e`) остаётся **зелёным и герметичным**. Продуктовые дефекты
  доказываются **off-gate** артефактами (bash-сценарии) и документируются заглушками
  `t.Skip` — никогда красными тестами в гейте. Удаление `t.Skip` у заглушки и реализация
  её тела превращает её в живой регресс после того, как продукт починен (у S2/S3/N1 также
  есть запускаемый bash-репро).
- Два ранее существовавших теста давали **ложную уверенность** по S2 и теперь
  аннотированы (§1).
- ID агентов-субагентов (для follow-up через SendMessage) и сырые отчёты по модулям лежат
  в транскрипте сессии; этот файл — дистиллированная, устойчивая форма.
