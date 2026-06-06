# Нагрузочное тестирование WMS → Блокчейн

Каталог содержит **два слоя** нагрузочного тестирования:

1. **Функциональный набор `01`–`08` + `run-all.sh`** (`make stress`) — пороговые
   проверки отдельных эндпоинтов и стадий. Подробности запуска, сиды и пороги —
   в [`INSTRUCTIONS.md`](./INSTRUCTIONS.md).
2. **Сквозной throughput/profiling-харнесс** (`09-throughput.js` + shell-обёртки) —
   измеряет реальную пропускную способность всего пути и где узкое место. Этот README.

> Все обёртки делают `cd "$(git rev-parse --show-toplevel)"` в начале, поэтому их
> можно запускать из любого каталога — путь к корню репозитория/worktree вычисляется
> автоматически. Стек ожидается под `COMPOSE_PROJECT_NAME=stresstest`.

---

## Что измеряем

| Величина | Смысл |
|---|---|
| **committed переходов/с** (главная единица) | подтверждённые в блокчейне переходы состояния (`onchain_events.status='COMMITTED'`). Цель — **≥1500/с**. Итог берётся как **sustained** (стационарное окно, минус 20с прогрева), а не peak. |
| backlog | `outbox_events` без строки в `onchain_events` — не убегает ли очередь |
| CPU-split | потребление CPU по каждому контейнеру → где узкое место |
| baseFee / gasUsedRatio | не входит ли цепь в ценовое удушение (должен держаться у пола 25 Gwei) |
| WMS↔chain consistency + on-chain TRACE | `shipped(wms) == shipping COMMITTED(chain)`, реальные `ItemTransition` (не skip/Failed) |

Один товар = **4** перехода (receiving → putaway → picking → shipping), каждый пишется в цепь
отдельным событием. Подробная методика и результаты — в
[`../../docs/load-testing/stress-load-tests-full-report.md`](../../docs/load-testing/stress-load-tests-full-report.md).

---

## Основной сценарий — `09-throughput.js`

Сквозной bench всего проекта (receive → putaway → pick → ship → on-chain) во весь опор.
Снимает искусственные тормоза теста 07 двумя приёмами де-контенции:

- **разнос по магазинам** — заказы размазаны по 10 точкам → лок `allocate` не глобальный;
- **планировщик-каденс** — `allocate`/`ship` зовутся не каждым оператором, а раз в
  `ALLOC_EVERY`/`SHIP_EVERY` итераций round-robin (как планирование волны).

Каждый VU = свой оператор (изоляция корзины). Два режима: `shared-iterations` (во весь
опор = потолок) и `constant-arrival-rate` (`PACE_RPS>0` → ровный backlog = честный sustained).

Ключевые env: `VUS`, `ITERS`, `SKU_COUNT`, `PRODUCTS_PER_CP`, `ALLOC_EVERY`, `SHIP_EVERY`,
`PICK_BATCH`, `THINK_MS`, `PACE_RPS`, `RECEIVING_BIN_ID`, `STORAGE_BIN_ID`. Требует
fixtures в `/tmp` — их готовят обёртки (ниже).

---

## Обёртки (`tests/stress/*.sh`)

| Скрипт | Назначение |
|---|---|
| **`profile-e2e-cpu.sh`** | **канон.** Сквозной committed/backlog + **per-container CPU-split**; окна sustained / лучшее-30с / peak. Кнобы: `N_ITEMS`, `SKU_COUNT`, `PER_CP`, `SHIP_EVERY`, `PACE_RPS`, `CPU_SAMPLE`. |
| `run-throughput.sh` | Сквозной bench 09 с тюном pool/adapter, фоновым DB-семплером наклона, дренажом back-half, consistency + on-chain trace (без CPU-split). |
| `profile-front.sh` | Паузит цепь (`docker pause`) → чистый фронт-потолок WMS (как в проде, где фронт и цепь на разных хостах). |
| `profile-e2e.sh` | Committed end-to-end, цепь ON: держит ли весь конвейер ~1500/с с плоским backlog. |
| `bench08.sh` / `bench-sustained.sh` | Блокчейн-слой изолированно: стоп adapter → набить Kafka N×5000 событий → дренаж при `BATCH_SIZE` → наклон = sustained chain-rate. |
| `smoke-fullflow.sh` | Smoke полного FSM (07) на живом адаптере: валидация харнесса + имена aggregate_type. |
| `run-fullflow-clean.sh` | Чистый full-FSM после gas-фикса: 0 OOG, реальные on-chain переходы, mixed-aggregate rate. |
| `validate-bug2.sh` | Отгрузка под конкурентной сборкой: shipping-события, SHIPPED, consistency, 0 FAILED/SENT. |
| `reset-chain.sh` | Пересоздать **только** Subnet-EVM с genesis `targetGas=2B`, сохранив postgres (тюнинг+данные) и kafka. |
| `basefee-sampler.sh` | Лог baseFee + gasUsedRatio каждые 4с в `/tmp/basefee.log` (доказывает fee-market фикс). |

---

## Как воспроизвести заголовок (150k → ~1924/с sustained)

```bash
# 1. поднять WMS с увеличенным пулом — ⚠️ ОБЯЗАТЕЛЬНО (см. ловушку ниже)
WMS_DB_MAX_CONNS=30 docker compose up -d wms_app

# 2. сквозной прогон на 150k (скрипт сам поднимет adapter batch=900/window=8,
#    засидит, прогонит k6, снимет committed-rate + CPU-split)
N_ITEMS=150000 ./tests/stress/profile-e2e-cpu.sh 70
```

> ⚠️ **Pool-ловушка.** `profile-e2e-cpu.sh` поднимает **только** `ledger-adapter`, а `wms_app`
> не трогает. Если не поднять WMS с `WMS_DB_MAX_CONNS=30` вручную, он стартует с дефолтным
> пулом **10** → Postgres простаивает → получите ~**1439/с** вместо 1924. Пул в 30 — это
> «главный рычаг масштаба» из отчёта. Проверка: `docker exec wms-service env | grep WMS_DB_MAX_CONNS`.

Предпосылки: `k6` на хосте, `foundry`/`cast` (для on-chain trace), стек `stresstest`
поднят, применён `setup/stress-throughput-seed.sql` (обёртки делают это сами).

---

## Последний результат (2026-06-06, пересобранные образы, 150k)

**sustained 1946 переходов/с**, 0 FAILED, backlog→~0, baseFee приклеен к 25 Gwei.
CPU: PostgreSQL 6.0 ядра (узкое место), блокчейн 1.6 ядра, бокс насыщен 10/10.
Подробности — `docs/load-testing/stress-load-tests-full-report.md` §10.6.
