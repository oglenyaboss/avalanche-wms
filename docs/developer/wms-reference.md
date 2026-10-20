# Документация разработчика: WMS-сервис (code-level)

**Аудитория:** инженерная команда, продолжающая проект. Цель — дать карту пакетов, ключевых типов, интерфейсов и сервисов WMS-монолита, чтобы вы могли расширять модули, не перечитывая весь код.

**Что это НЕ:** не описание бизнес-процессов и не контракт API. Эти материалы лежат в отдельных документах — мы ссылаемся на них, а не дублируем:

| Что нужно | Где смотреть |
|-----------|--------------|
| Сквозной путь товара (Приёмка → Раскладка → Сборка → Отгрузка) | [business-process/end-to-end-flow.md](../business-process/end-to-end-flow.md) |
| Диаграммы потоков данных | [business-process/data-flow-diagrams.md](../business-process/data-flow-diagrams.md) |
| Пошаговые flow по этапам | [flows/receiving-gate-flow.md](../flows/receiving-gate-flow.md), [flows/receiving-table-flow.md](../flows/receiving-table-flow.md), [flows/putaway-flow.md](../flows/putaway-flow.md), [flows/assembly-flow.md](../flows/assembly-flow.md), [flows/shipping-flow.md](../flows/shipping-flow.md) |
| Жизненный цикл сущностей, ER-диаграмма | [data-model/entity-lifecycle.md](../data-model/entity-lifecycle.md), [data-model/er-diagram.md](../data-model/er-diagram.md) |
| Схема БД (таблицы, колонки, enum, миграции) | [db/Database_ru_v2.md](../db/Database_ru_v2.md) |
| Маппинг WMS → блокчейн, контракт данных | [integration/blockchain-mapping.md](../integration/blockchain-mapping.md), [integration/data-contract.md](../integration/data-contract.md) |
| Обзор архитектуры | [architecture/system-overview.md](../architecture/system-overview.md) |
| Контракт HTTP API (request/response по эндпоинтам) | [api/openapi.yaml](../api/openapi.yaml) — источник истины; [api/api-contract.md](../api/api-contract.md) — компаньон |
| Конвенции кода/git, гайд по MR | [CONVENTIONS.md](../CONVENTIONS.md), [MR_GUIDE.md](../MR_GUIDE.md) |

> [!NOTE] Про OpenAPI и контракт API
> Источник истины по API — машиночитаемый [api/openapi.yaml](../api/openapi.yaml) (OpenAPI 3.1) и сгенерированный из него [api/api-contract.md](../api/api-contract.md). **Источник истины по маршрутам всегда — функции `RegisterRoutes` в каждом модуле** (`wms/internal/<module>/handler.go`). Этот документ намеренно содержит **verbatim DTO с json-тегами** (см. § 5); за полным контрактом ошибок/примеров идите в OpenAPI.

---

## 1. Обзор сервиса и структура `internal/`

WMS — это **модульный монолит** на Go: один бинарь (`wms/cmd/wms`), один процесс, один пул PostgreSQL. Каждый этап склада — отдельный Go-пакет под `wms/internal/`. Между WMS и блокчейном нет синхронных вызовов на «горячем пути»: в той же транзакции, что и бизнес-мутация, WMS пишет строку в `public.outbox_events` (паттерн transactional outbox). Единица записи — **подписанное складское действие**, а не товар (ADR-0001): `POST …/draft` кладёт черновик операции (части ≤ 2000 товаров) в `public.warehouse_actions`, оператор подписывает каждую часть passkey, `POST …/submit` проверяет подписи и пишет по строке outbox на часть. Строки читает Debezium (CDC) и публикует в Kafka `wms.events.v1`; отдельный процесс `ledger-adapter` применяет их в Avalanche Subnet-EVM.

### Структура пакетов

```
wms/
├── cmd/wms/main.go                  Точка входа: config → pool → kafka → ledger → wiring → router
└── internal/
    ├── auth/                        JWT-аутентификация, Middleware, регистрация пользователей
    ├── receiving/                   Этап 1–2: приёмка на КПП + на столе (11 эндпоинтов)
    ├── putaway/                     Этап 3: раскладка buffer → storage (4 эндпоинта)
    ├── assembly/                    Этап 4: аллокация + сборка/пикинг (5 эндпоинтов)
    ├── shipping/                    Этап 5: отгрузка (4 эндпоинта)
    ├── dispatches/                  CRUD исходящих рейсов (3 эндпоинта, без блокчейна)
    ├── destinations/                Справочник магазинов-получателей (1 эндпоинт, read-only)
    ├── domain/                      Общие доменные модели + enum-статусы (без логики)
    ├── signedaction/                WebAuthn-подпись, challenge и detailsHash, конверт действия (без БД)
    ├── signedops/                   Черновик и части операции, черновик/приём подписей, запись outbox
    ├── passkeys/                    passkey пользователей, регистрация в реестре, ключевые действия админа
    ├── audit/                       Сверка БД ↔ цепь: CLI #17 и HTTP для роли AUDITOR
    ├── ledger/                      Клиент ledger-adapter + DB-гейты CheckChainStatus/UnconfirmedProducts
    └── platform/
        ├── postgres/                Фабрика *pgxpool.Pool
        ├── kafka/                   Возвращает *kafkago.Conn (segmentio/kafka-go); не обёртка, а голое соединение
        ├── httpserver/              Обёртка net/http.Server (в main не используется — см. § 5)
        └── httputil/               Конверт ответа + RBAC-гейты
```

### Правило зависимостей: `handler → service → repository`

Каждый бизнес-модуль повторяет один и тот же трёхслойный паттерн. Зависимости направлены строго в одну сторону:

```
HTTP request
   ↓
Handler   (handler.go)   — декодирование JSON, RBAC-гейт, маппинг ошибок → HTTP-статус, сериализация конверта
   ↓
Service   (service.go)   — бизнес-логика, FSM-переходы, транзакции через repo.WithTx, гейты CheckChainStatus/UnconfirmedProducts
   ↓
Repository (repository.go) — весь SQL, управление транзакциями, запись строки outbox на часть подписанного действия
   ↓
PostgreSQL (*pgxpool.Pool)
```

- **Handler** держит `*Service` (конкретный указатель, не интерфейс).
- **Service** держит `<module>Repository` — **неэкспортируемый интерфейс**, объявленный в самом пакете. Единственная реальная реализация — `*Repository`; интерфейс существует ради `WithTx` (передаёт в колбэк копию репозитория, обёрнутую в `pgx.Tx`) и ради моков в unit-тестах.
- **Repository** — конкретный `*Repository` поверх `*pgxpool.Pool`.

> [!NOTE] Честно про «интерфейсы»
> В WMS нет ни одного **экспортируемого** интерфейса для DI между модулями. Конструкторы `NewService(repo <module>Repository)` принимают неэкспортируемый интерфейс; `NewHandler(svc *Service)` и `NewRepository(db *pgxpool.Pool)` работают с конкретными типами. `ledger.Client` — тоже конкретная структура, интерфейса `LedgerClient` не существует. Единственный «широкий» интерфейс — `ledger.RowQuerier` (минимальный `QueryRow`, удовлетворяется и пулом, и транзакцией). Не выдумывайте интерфейсы там, где их нет: подменяемость в тестах достигается через пакетно-приватные `<module>Repository`-интерфейсы и моки в `*_test.go`.

### Точка входа и монтирование роутеров (`cmd/wms/main.go`)

`main()`: загружает конфиг → открывает пул PostgreSQL → открывает соединение Kafka → опционально создаёт `ledger.Client` → собирает все модули (`repo → service → handler`) → строит `gorilla/mux`-роутер. Каждый бизнес-модуль монтируется на свой `PathPrefix(...).Subrouter()` с навешенным `auth.Middleware`:

```go
receivingRouter := r.PathPrefix("/receiving").Subrouter()
receivingRouter.Use(auth.Middleware([]byte(cfg.JWTSecret)))
receivingHandler.RegisterRoutes(receivingRouter)
// ... аналогично putaway, assembly, dispatches, destinations, shipping
```

`authHandler.RegisterRoutes(r)` навешивается на **корневой** роутер (без middleware на саброутере); `GET /health` — тоже на корне, без auth.

---

## 2. Платформенный слой (`internal/platform`, `internal/ledger`, `internal/config`)

### 2.1 `platform/postgres`

```go
func NewPool(ctx context.Context, user, password, host, port, dbname string) (*pgxpool.Pool, error)
```

Строит DSN `postgres://user:password@host:port/dbname?sslmode=disable`, фиксирует `MaxConns=10`, `MinConns=2`, делает `Ping`. Все модули делят один пул.

> [!WARNING] Инварианты postgres
> - `sslmode=disable` **зашит** безусловно — TLS к БД включить нельзя без правки кода.
> - Границы пула (`MaxConns=10`, `MinConns=2`) захардкожены, env-переменных для них нет.
> - При неудачном `Ping` вызывает `pool.Close()` перед возвратом ошибки (без утечки соединений).

### 2.2 `platform/kafka`

```go
func NewConn(broker string) (*kafkago.Conn, error)
```

Возвращает «голое» `*kafkago.Conn` — используется только для проверки брокеров на старте (`conn.Brokers()`). Реальные producer/consumer создаются отдельно (в текущем коде продьюсеров в WMS нет — публикацию в Kafka делает Debezium из outbox-таблицы).

### 2.3 `platform/httpserver`

```go
func New(port string, handler http.Handler) *Server
func (s *Server) Start() error
func (s *Server) Stop(ctx context.Context) error
```

Обёртка над `net/http.Server` с таймаутами `Read=15s`, `Write=15s`, `Idle=60s` и graceful-shutdown. **Важно:** `main.go` этот пакет **не использует** — там вызывается напрямую `http.ListenAndServe(...)` без graceful shutdown (см. § 5, gotchas). Пакет готов к подключению, но пока «висит».

### 2.4 `platform/httputil` — конверт ответа и RBAC-гейты

Единый JSON-конверт всех бизнес-ответов:

```go
type Envelope struct {
    Success bool      `json:"success"`
    Data    any       `json:"data"`
    Error   *APIError `json:"error"`
}
type APIError struct {
    Code    string `json:"code"`
    Message string `json:"message"`
}
```

Успех: `{"success":true,"data":<payload>,"error":null}`. Ошибка: `{"success":false,"data":null,"error":{"code":"...","message":"..."}}`.

Хелперы записи:

| Функция | Поведение |
|---------|-----------|
| `WriteJSON(w http.ResponseWriter, status int, payload any)` | Ставит `Content-Type: application/json`, пишет статус, кодирует payload. Ошибка кодирования **только логируется** (не пробрасывается). |
| `WriteError(w http.ResponseWriter, status int, code, message string)` | Обёртка: строит failure-`Envelope` и зовёт `WriteJSON`. |

**RBAC-гейты** (читают identity из контекста запроса, а не из заголовка — поэтому вызываются **после** `auth.Middleware`):

```go
func RequireOperator(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool)
func RequireAdminOrOperator(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool)
```

Внутренний общий гейт (неэкспортируемый):

```go
func requireRole(w http.ResponseWriter, r *http.Request, forbiddenMsg string, allowed ...domain.UserRole) (uuid.UUID, bool)
```

| Гейт | Разрешённые роли | 401 (нет identity) | 403 (не та роль) |
|------|------------------|--------------------|------------------|
| `RequireOperator` | только `OPERATOR` | `UNAUTHORIZED` / «Требуется авторизация» | `FORBIDDEN` / «Только оператор может выполнять это действие» |
| `RequireAdminOrOperator` | `ADMIN` или `OPERATOR` | то же | `FORBIDDEN` / «Требуется роль администратора или оператора» |

При успехе гейт **ничего не пишет** в `w` и возвращает `(userID, true)`. При провале сам пишет конверт ошибки и возвращает `(uuid.Nil, false)` — хендлер обязан сразу `return`.

> [!NOTE] `RequireOperator` **блокирует** `ADMIN` (отдаёт 403). Отдельного `RequireAdmin`-гейта нет — если понадобится admin-only эндпоинт, добавьте новую экспортируемую функцию в `authz.go`.

### 2.5 `ledger` — клиент адаптера и DB-гейт

**`ledger.Client`** (`client.go`) — конкретная структура, HTTP-таймаут 5s:

```go
func NewClient(baseURL string) *Client
func (c *Client) HealthCheck() error
```

| Метод | Вызывает эндпоинт адаптера | Поведение |
|-------|----------------------------|-----------|
| `HealthCheck()` | `GET {baseURL}/health` | `nil` только при HTTP 200; тело не читается. |

Это **единственный** эндпоинт ledger-adapter, который WMS вызывает по HTTP. Всё остальное взаимодействие — через общую БД (outbox).

**`ledger.CheckChainStatus`** (`chain_status.go`) — это **свободная функция, не метод `Client`**. Чистый DB-гейт, блокирующий продвижение FSM, если у товара есть `FAILED` он-чейн-действие на нужном этапе:

```go
func CheckChainStatus(ctx context.Context, q RowQuerier, productIDs []uuid.UUID, aggregateType string) error
```

Один `SELECT EXISTS(...)` join `public.warehouse_actions` ⋈ `public.onchain_events` по `wa.action_id = oe.event_id`, фильтр `wa.item_ids && $1::uuid[]`, `wa.action_type = $2` (номер из `actionTypeByAggregate`), `oe.status = 'FAILED'`. Возвращает `ErrChainEventRejected`, если найдена хоть одна `FAILED`-строка; `nil` иначе (включая случай «действия ещё нет» — отсутствие = ещё не зеркалировано = пропуск). Пустой `productIDs` → сразу `nil`.

**`ledger.UnconfirmedProducts`** — обратный гейт для отгрузки: возвращает товары, у которых нет `COMMITTED` действия нужного этапа (используется shipping — `409 PICKING_NOT_CONFIRMED`).

Константы этапов (**единственный источник истины** для обоих гейтов):

```go
const (
    AggregateReceiving = "receiving"
    AggregatePutaway   = "putaway"
    AggregatePicking   = "picking"   // НЕ "assembly"
    AggregateShipping  = "shipping"
)
var ErrChainEventRejected = errors.New("CHAIN_EVENT_REJECTED")
// actionTypeByAggregate: receiving→1, putaway→2, picking→3, shipping→4
```

> [!WARNING] Ключ этапа сборки/пикинга — `"picking"`, не `"assembly"`. Использование `"assembly"` даст `action_type = 0` — гейт молча пропустит всё.

### 2.6 `config` — все переменные окружения

```go
type Config struct {
    Port, DBHost, DBPort, DBUser, DBPassword, DBName, KafkaBroker, LedgerAdapterURL, JWTSecret string
    JWTAccessTTL, JWTRefreshTTL time.Duration
}
func Load() (*Config, error)
```

| Env-переменная | Поле | Тип | Default | Обязательна | Валидация |
|----------------|------|-----|---------|-------------|-----------|
| `PORT` | `Port` | string | `8080` | нет | — |
| `DB_HOST` | `DBHost` | string | `localhost` | нет | — |
| `DB_PORT` | `DBPort` | string | `5432` | нет | — |
| `POSTGRES_USER` | `DBUser` | string | `root` | нет | — |
| `POSTGRES_PASSWORD` | `DBPassword` | string | `root` | нет | — |
| `POSTGRES_DB` | `DBName` | string | `wms_blockchain_db` | нет | — |
| `KAFKA_BROKER` | `KafkaBroker` | string | `localhost:9092` | нет | — |
| `LEDGER_ADAPTER_URL` | `LedgerAdapterURL` | string | `""` | формально нет, **фактически нужна** | пустая строка принимается → отказ в рантайме |
| `JWT_SECRET` | `JWTSecret` | string | нет | **да — `Load()` вернёт ошибку** | непустая после trim; не из блок-листа; `len(secret) >= 32` байт (байтовая длина trimmed-значения, не число код-поинтов Unicode) |
| `JWT_ACCESS_TTL` | `JWTAccessTTL` | duration | `15m` | нет | `time.ParseDuration`; невалидное значение → warning + default |
| `JWT_REFRESH_TTL` | `JWTRefreshTTL` | duration | `168h` (7 дней) | нет | то же |

Блок-лист `JWT_SECRET` (отклоняются с ошибкой): `change-me`, `dev-secret`, `replace-me`, `replace-with-a-random-32-byte-secret`, `replace-with-a-random-64-character-secret`.

> [!WARNING] Тихие отказы конфига
> - `LEDGER_ADAPTER_URL=""` принимается `Load()`, но `HealthCheck()` тогда дёрнет относительный `GET /health` и упадёт в рантайме, не на старте.
> - Невалидный `JWT_ACCESS_TTL`/`JWT_REFRESH_TTL` → warning в лог + default, без ошибки. Проверяйте логи.

---

## 3. Аутентификация и авторизация (`internal/auth`)

JWT-аутентификация (HS256), три роли, RBAC поверх. **Auth-эндпоинты намеренно не используют JSON-конверт** — они отвечают plain-text через `http.Error`, в отличие от бизнес-модулей. Бизнес-логику login/refresh/register см. в коде; здесь — контракт типов и гейтов.

### 3.1 Роли

```go
type UserRole string // domain/user_role.go
const (
    UserRoleAdmin    UserRole = "ADMIN"
    UserRoleOperator UserRole = "OPERATOR"
    UserRoleCustomer UserRole = "CUSTOMER"
)
```

DB-enum `public.user_role` совпадает 1:1. Роль `CUSTOMER` валидна для регистрации, но **ни один эндпоинт её не принимает** — пользователь-CUSTOMER всегда получит 403 на складских маршрутах.

### 3.2 JWT: claims и TTL

```go
type tokenClaims struct {       // service.go (неэкспортируемый)
    UserID string `json:"user_id"`
    Role   string `json:"role,omitempty"`
    Type   string `json:"type"`   // "access" | "refresh"
    jwt.RegisteredClaims          // IssuedAt, ExpiresAt
}
```

- `Role` помечен `omitempty` и **отсутствует в refresh-токенах** — refresh несёт только `UserID` и `Type`. После refresh роль перечитывается из БД; смена роли вступает в силу на следующем refresh, не мгновенно.
- Access TTL: `JWT_ACCESS_TTL` (default 15m). Refresh TTL: `JWT_REFRESH_TTL` (default 168h).
- Подпись HS256 секретом `JWT_SECRET`. Нет `Subject`/`Issuer`/`Audience`/`JWTID`.

> [!WARNING] Нет отзыва и ротации токенов. Блэк-листа/сессий нет. Украденный refresh-токен валиден до `ExpiresAt` (до 7 дней). Единственный серверный механизм инвалидации — `is_active = false`, и он срабатывает только при следующем Login/Refresh.

### 3.3 Middleware и контекст

```go
func Middleware(jwtSecret []byte) func(http.Handler) http.Handler
func ContextWithIdentity(ctx context.Context, userID uuid.UUID, role domain.UserRole) context.Context
func UserIDFromCtx(ctx context.Context) uuid.UUID      // uuid.Nil если нет
func UserRoleFromCtx(ctx context.Context) domain.UserRole // "" если нет
```

`Middleware` извлекает `Authorization: Bearer <token>`, валидирует как access-токен (`type=access`), затем зовёт `ContextWithIdentity`. Любой провал → plain-text `"unauthorized"` + 401. `ContextWithIdentity` — **единственный писатель** ключей контекста `ctxUserID`/`ctxUserRole` (тип ключа — неэкспортируемый `ctxKey`, защита от коллизий). Роль хранится в контексте как `string`, читается с приведением обратно в `domain.UserRole`. **Размещение:** `ContextWithIdentity` — в `auth/context.go`; `UserIDFromCtx`, `UserRoleFromCtx`, тип `ctxKey` и константы `ctxUserID`/`ctxUserRole` — в `auth/handler.go`.

### 3.4 Маршруты auth (`RegisterRoutes`)

| Эндпоинт | Auth | Роль | Тело / ответ |
|----------|------|------|--------------|
| `POST /auth/login` | нет (публичный) | — | `{username,password}` → `200` `TokenPair` (plain JSON, **не конверт**) |
| `POST /auth/refresh` | нет (публичный) | — | `{refresh_token}` → `200` `TokenPair` (plain JSON) |
| `POST /auth/register` | Bearer access | только `ADMIN` (двойная проверка) | `{username,password,role}` → `201` `{id,username}` (plain JSON) |

> [!NOTE] `/auth/register` обёрнут в `Middleware` **прямо на маршруте** (`router.Handle("/auth/register", Middleware(...)(handler))`), а не через саброутер. Двойная проверка ADMIN: сперва по claim из JWT (`actorRole == ADMIN`), затем повторный fetch актора из БД (`IsActive && Role == ADMIN`). JWT с `role=ADMIN` для понижённого в БД пользователя будет отклонён.

**DTO (verbatim).** Все request-структуры декодируются с `DisallowUnknownFields` → лишнее поле = 400.

`loginRequest` (request `POST /auth/login`):

| Поле | JSON-тег | Go-тип | Примечание |
|------|----------|--------|-----------|
| `Username` | `username` | `string` | обязательно (пусто → 401) |
| `Password` | `password` | `string` | обязательно (пусто → 401) |

`refreshRequest` (request `POST /auth/refresh`):

| Поле | JSON-тег | Go-тип | Примечание |
|------|----------|--------|-----------|
| `RefreshToken` | `refresh_token` | `string` | обязательно |

`registerRequest` (request `POST /auth/register`):

| Поле | JSON-тег | Go-тип | Примечание |
|------|----------|--------|-----------|
| `Username` | `username` | `string` | обязательно, непустое |
| `Password` | `password` | `string` | 6–72 байта (нижняя/верхняя граница bcrypt) |
| `Role` | `role` | `string` | `ADMIN` / `OPERATOR` / `CUSTOMER` |

`registerResponse` (response `POST /auth/register`, plain JSON, не конверт):

| Поле | JSON-тег | Go-тип |
|------|----------|--------|
| `ID` | `id` | `uuid.UUID` |
| `Username` | `username` | `string` |

`TokenPair` (response login/refresh, plain JSON, не конверт):

| Поле | JSON-тег | Go-тип |
|------|----------|--------|
| `AccessToken` | `access_token` | `string` |
| `RefreshToken` | `refresh_token` | `string` |

Пароли: bcrypt, `DefaultCost` (10); длина 6–72 байта (верхняя граница — лимит bcrypt).

**Интерфейс `UserRepository`** (`service.go`) и его методы:

```go
type UserRepository interface {
    CreateUser(ctx context.Context, user *domain.User) error
    GetUserByUsername(ctx context.Context, username string) (*domain.User, error)
    GetUserByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
}
```

| Метод | Поведение |
|-------|-----------|
| `CreateUser` | INSERT в `public.users`; маппит unique-violation `23505` → `ErrUserExists`. |
| `GetUserByUsername` | `SELECT ... WHERE username=$1`; роль читается как `string`, кастится в `domain.UserRole`. |
| `GetUserByID` | `SELECT ... WHERE user_id=$1`. |

Конструкторы auth: `NewService(repo UserRepository, jwtSecret string, accessTTL, refreshTTL time.Duration) *Service` (принимает интерфейс — для моков), `NewHandler(svc *Service) *Handler`, `NewRepository(db *pgxpool.Pool) *Repository`.

### 3.5 Карта эндпоинт → требуемая роль (RBAC-гейты)

Все бизнес-маршруты сперва проходят `auth.Middleware` (JWT обязателен → 401 без него), затем гейт в хендлере:

| Эндпоинт | Гейт в хендлере | Допустимые роли |
|----------|-----------------|-----------------|
| `POST /receiving/**` (все 11) | `RequireOperator` | `OPERATOR` |
| `POST /putaway/**` (все 4) | `RequireOperator` | `OPERATOR` |
| `POST /assembly/allocate` | **`RequireAdminOrOperator`** | `ADMIN`, `OPERATOR` |
| `GET /assembly/tasks` | `RequireOperator` | `OPERATOR` |
| `POST /assembly/pick` | `RequireOperator` | `OPERATOR` |
| `POST /assembly/scan-shipping-buffer/{draft,submit}` | `RequireOperator` | `OPERATOR` |
| `POST /shipping/**` (все 4) | `RequireOperator` | `OPERATOR` |
| `GET|POST /dispatches/**` (все 3) | `RequireOperator` | `OPERATOR` |
| `GET /destinations` | `RequireOperator` | `OPERATOR` |
| `GET /signed-actions/operations/{id}` | `RequireAdminOrOperator` | `ADMIN`, `OPERATOR` |
| `GET /passkeys/me`, `POST /passkeys/registration/*` | `RequireAdminOrOperator` | `ADMIN`, `OPERATOR` |
| `GET /passkeys/organization*`, `POST /passkeys/actions/*` | **`RequireAdmin`** | `ADMIN` |
| `GET /audit/*` | `RequireAuditorOrAdmin` | `AUDITOR`, `ADMIN` |
| `POST /auth/register` | — (проверка ADMIN внутри сервиса) | `ADMIN` |

> [!NOTE] Единственное исключение из «OPERATOR-only» среди бизнес-эндпоинтов — `POST /assembly/allocate` (`RequireAdminOrOperator`). Это сознательное изменение ветки `refactor/issue-39-admin-operator-access` (MR #39): админ-координация без отдельного operator-аккаунта.

---

## 4. Доменный слой (`internal/domain`)

Пакет `domain` — общий слой типов. **Никакой логики**, только структуры, type-alias и string-константы enum. Импортируется всеми модулями. Полную схему БД (таблицы, колонки, CHECK-констрейнты, миграции) см. в [db/Database_ru_v2.md](../db/Database_ru_v2.md); переходы статусов с бизнес-точки — в [data-model/entity-lifecycle.md](../data-model/entity-lifecycle.md). Здесь — каталог моделей и enum как карта типов.

### 4.1 Каталог доменных моделей

| Файл / тип | DB-таблица | Назначение |
|------------|------------|------------|
| `warehouse.go` / `Warehouse` | `wms_inventory.warehouses` | Мастер-запись склада |
| `inbound_shipment.go` / `InboundShipment` | `wms_inventory.inbound_shipments` | Входящая поставка по TTN; `Status` — **plain string** |
| `cargoplace.go` / `Cargoplace` | `wms_inventory.cargoplaces` | Грузоместо (паллета/контейнер); `Status` — **plain string** |
| `box.go` / `Box` | `wms_inventory.boxes` | Коробка внутри грузоместа; `Status` — **plain string** |
| `sku.go` / `SKU` | `wms_inventory.skus` | Каталожная позиция (тип товара) |
| `product.go` / `Product` | `wms_inventory.products` | Экземпляр товара; `Status ProductStatus` (typed enum) |
| `order.go` / `Order` | `wms_inventory.orders` | Заголовок исходящего заказа; `Status OrderStatus` |
| `order_line.go` / `OrderLine` | `wms_inventory.order_lines` | Строка заказа (SKU + qty) |
| `destination.go` / `Destination` | `wms_inventory.destinations` | Магазин-получатель |
| `bin.go` / `Bin` | `wms_inventory.bins` | Ячейка хранения; `Section` (`"BUFFER"`/`"SHIPPING_BUFFER"`/storage) |
| `assembly_task.go` / `AssemblyTask` | `wms_ops.assembly_tasks` | Задача пикинга; `Status TaskStatus` |
| `outbound_dispatch.go` / `OutboundDispatch` | `wms_inventory.outbound_dispatches` | Плановый исходящий рейс; `Status OutboundDispatchStatus` |
| `expected_cargoplace_sku.go` / `ExpectedCargoplaceSku` | `wms_inventory.expected_cargoplace_skus` | Предзаявленный манифест по грузоместу |
| `receiving_gate.go` / `ReceivingGateEntry` | `wms_ops.receiving_gate_log` | Аудит-лог КПП (append-only) |
| `receiving_table.go` / `ReceivingTableEntry` | `wms_ops.receiving_table_log` | Аудит-лог стола (append-only) |
| `putaway.go` / `Putaway` | `wms_ops.putaways` | Событие раскладки product→bin |
| `shipping.go` / `Shipping` | `wms_ops.shippings` | Событие отгрузки |
| `outbox_event.go` / `OutboxEvent` | `public.outbox_events` | Outbox-строка для роутинга в ledger |
| `user.go` / `User` | `public.users` | Аккаунт; `Role UserRole` |
| `user_role.go` / `UserRole` | (enum `public.user_role`) | Роль доступа |
| `evm_address.go` / `EvmAddress` | `public.evm_addresses` | EVM-адрес пользователя (**не используется** вне domain) |

### 4.2 Enum-статусы: значения и FSM-переходы

#### `ProductStatus` (typed, `product_status.go`) — DB-enum `wms_inventory.product_status`

| Константа | Значение |
|-----------|----------|
| `ProductStatusReceived` | `RECEIVED` |
| `ProductStatusStored` | `STORED` |
| `ProductStatusAllocated` | `ALLOCATED` |
| `ProductStatusAssembled` | `ASSEMBLED` |
| `ProductStatusReadyToShip` | `READY_TO_SHIP` |
| `ProductStatusShipped` | `SHIPPED` |

| From | To | Чем триггерится |
|------|----|-----------------|
| (новый) | `RECEIVED` | receiving — ScanQR вставляет product |
| `RECEIVED` | `STORED` | putaway — `UpdateProductStorage` (ставит `bin_id` + статус) |
| `STORED` | `ALLOCATED` | assembly — `Allocate` (ставит `order_id` + статус) |
| `ALLOCATED` | `ASSEMBLED` | assembly — `Pick`; предусловие: `CheckChainStatus(AggregatePutaway)` не FAILED |
| `ASSEMBLED` | `READY_TO_SHIP` | assembly — `SubmitUnloadToShippingBuffer` / `MoveProductsToShippingBuffer` (подписанный Pick) |
| `READY_TO_SHIP` | `SHIPPED` | shipping — `SubmitShip` / `BatchUpdateProductsShipped` (подписанный Ship) |

#### `OrderStatus` (typed, `order_status.go`) — DB-enum `wms_inventory.order_status`

| Константа | Значение |
|-----------|----------|
| `OrderStatusNew` | `NEW` |
| `OrderStatusAllocated` | `ALLOCATED` |
| `OrderStatusAssembled` | `ASSEMBLED` |
| `OrderStatusPartiallyShipped` | `PARTIALLY_SHIPPED` |
| `OrderStatusShipped` | `SHIPPED` |

| From | To | Чем триггерится |
|------|----|-----------------|
| (новый) | `NEW` | заказ создан внешне |
| `NEW` | `ALLOCATED` | assembly — после `Allocate` |
| `ALLOCATED` | `ASSEMBLED` | assembly — `UpdateOrdersToAssembled` (все products → `READY_TO_SHIP`) |
| `ALLOCATED`/`ASSEMBLED`/`PARTIALLY_SHIPPED` | `PARTIALLY_SHIPPED` | shipping — `UpdateOrdersShippedConditional` (отгружена часть) |
| `ALLOCATED`/`ASSEMBLED`/`PARTIALLY_SHIPPED` | `SHIPPED` | shipping — `UpdateOrdersShippedConditional` (отгружено всё) |

> Финальный DB-enum после миграций 0007+0010 совпадает с Go: `NEW, ALLOCATED, ASSEMBLED, PARTIALLY_SHIPPED, SHIPPED`.

#### `TaskStatus` (typed, `task_status.go`) — DB-enum `wms_ops.task_status`

| Константа | Значение |
|-----------|----------|
| `TaskStatusPending` | `PENDING` |
| `TaskStatusInProgress` | `IN_PROGRESS` |
| `TaskStatusDone` | `DONE` |
| `TaskStatusCancelled` | `CANCELLED` (`//nolint:misspell` — точное значение DB-enum) |

| From | To | Чем триггерится |
|------|----|-----------------|
| (новый) | `PENDING` | assembly — `InsertAssemblyTask` на Allocate |
| `PENDING` | `DONE` | assembly — `MarkTaskDone` на Pick (`WHERE status='PENDING'`) |

> [!NOTE] `IN_PROGRESS` и `CANCELLED` валидны в DB-enum и принимаются как фильтр в `GET /assembly/tasks`, но **ни один сервис-метод не переводит задачу в эти статусы** — мёртвые состояния в текущей реализации FSM.

#### `OutboundDispatchStatus` (typed, `outbound_dispatch_status.go`) — DB-enum (миграция 0007)

| Константа | Значение |
|-----------|----------|
| `OutboundDispatchStatusScheduled` | `SCHEDULED` |
| `OutboundDispatchStatusAtGate` | `AT_GATE` |
| `OutboundDispatchStatusDeparted` | `DEPARTED` |
| `OutboundDispatchStatusCancelled` | `CANCELLED` (`//nolint:misspell`) |

| From | To | Чем триггерится |
|------|----|-----------------|
| (новый) | `SCHEDULED` | рейс создан (`dispatches` модуль или внешне) |
| `SCHEDULED` | `AT_GATE` | shipping — `ScanDriver` / `UpdateDispatchToAtGate` |
| `AT_GATE` | `DEPARTED` | shipping — `Ship` / `UpdateDispatchDeparted` (буфер пуст) |
| `DEPARTED` | — | терминальный; повторный `ScanDriver` → `ErrDispatchAlreadyDeparted` |
| `CANCELLED` | — | терминальный; `ScanDriver` → `ErrDispatchCancelled`. **Кода, ставящего `CANCELLED`, в WMS нет.** |

#### Untyped string-статусы (receiving)

`Cargoplace.Status`, `Box.Status`, `InboundShipment.Status` — **plain `string`**, не typed enum. Допустимые значения — неэкспортируемые константы в `receiving/service.go`:

| Сущность | Значения | Переходы |
|----------|----------|----------|
| `InboundShipment.Status` | `CREATED`, `GATE_IN_PROGRESS`, `GATE_CLOSED` | `CREATED`→`GATE_IN_PROGRESS` (ScanTTN) → `GATE_CLOSED` (AcceptShipment/auto-close) |
| `Cargoplace.Status` | `EXPECTED`, `RECEIVED_AT_GATE`, `NOT_RECEIVED`, `TABLE_IN_PROGRESS`, `TABLE_CLOSED` | `EXPECTED`→`RECEIVED_AT_GATE` (ScanCargoplace) или `NOT_RECEIVED` (AcceptShipment); `RECEIVED_AT_GATE`→`TABLE_IN_PROGRESS` (table/ScanCargoplace) → `TABLE_CLOSED` (CloseCargoplace) |
| `Box.Status` | `OPEN`, `CLOSED` | `OPEN`→`CLOSED` (CloseBox) |

> [!WARNING] Для этих трёх статусов нет typed-enum в Go и нет CHECK-констрейнта на уровне domain — OpenAPI/документация должны описывать их как закрытый строковый enum вручную.

#### Строка `outbox_events` подписанного действия

Событий на товар больше нет: одна строка = одна **часть подписанного действия**. Её пишет `signedops.WriteActions` (или `passkeys.SubmitKeyAction` для ключевых действий) — единственные два INSERT'а в `public.outbox_events`:

| Колонка | Значение |
|---------|----------|
| `event_id` / `aggregate_id` | `warehouse_actions.action_id` (для ключевых — `key_actions.action_id`) |
| `aggregate_type` | всегда `signed_action` |
| `event_type` | всегда `wms.action.v1` |
| `payload_hash` | challenge (hex) |
| `payload` (jsonb) | подписанный конверт действия (ADR-0001 §9) |

Этап и тип действия определяются по `warehouse_actions.action_type` (1 Receive, 2 PutAway, 3 Pick, 4 Ship) или `key_actions.action_type` (5/6). Константы этапов в `ledger/chain_status.go` (§ 2.5) теперь ключи для `actionTypeByAggregate`, а не значения `aggregate_type`. `domain.OutboxEvent` — старый DTO без `payload`, в коде не используется.

### 4.3 Прочие domain-инварианты

- **`User.PasswordHash` НЕ имеет `json:"-"`** (`json:"password_hash"`). Маршалинг `User` напрямую утечёт хеш — каждый хендлер обязан проецировать в DTO без этого поля (что auth и делает через `registerResponse`).
- `Putaway`/`Shipping` domain-структуры **не содержат** колонок `onchain_status`/`payload_hash`; самих колонок `onchain_status` в `wms_ops` больше нет (удалены миграцией 0008), статус выводится из `onchain_events` по `action_id`.
- `EvmAddress.OnchainRole` — `text` без enum; структура вообще не используется вне пакета domain.
- DB-enum без Go-типа: `public.onchain_event_status` (`PENDING/SENT/COMMITTED/FAILED`) — живёт внутри `ledger`; `wms_ops.operation_onchain_status` удалён миграцией 0008.

---

## 5. Бизнес-модули

Общие свойства всех бизнес-модулей (если не сказано иное):

- Слои `handler → service → repository`, неэкспортируемый интерфейс `<module>Repository` в `service.go`.
- Конструкторы: `NewRepository(db *pgxpool.Pool) *Repository`, `NewService(repo <module>Repository) *Service`, `NewHandler(svc *Service) *Handler`.
- Все DTO декодируются с `DisallowUnknownFields()` → лишнее поле = 400.
- UUID приходят строками, парсятся `uuid.Parse` в хендлере (невалидный → 400 до сервиса).
- Ответы — конверт `{success,data,error}` (у receiving/putaway/assembly/shipping — **локальные копии** `envelope`/`apiErrObj` той же формы; у dispatches — своя `ApiError`, см. § 5.6).
- `code` в ошибке — машиночитаемый ASCII; `message` — текст **на русском**.
- gorilla/mux **не редиректит** trailing slash — маршруты регистрируются точным путём.
- **Полный request/response контракт и коды ошибок** — в [api/openapi.yaml](../api/openapi.yaml) (источник истины) и таблицах ниже. **Бизнес-смысл шагов** — в `flows/*`.

### 5.1 `receiving` — приёмка (этапы 1–2)

**Назначение:** двухфазная приёмка. Фаза «gate»: идентификация поставки по TTN и физический пересчёт грузомест на КПП. Фаза «table»: распаковка на столе — коробки, скан штрихкодов + QR, перенос в буфер, закрытие грузоместа подписанным Receive-действием (`draft → passkey → submit`). Бизнес-flow: [flows/receiving-gate-flow.md](../flows/receiving-gate-flow.md), [flows/receiving-table-flow.md](../flows/receiving-table-flow.md).

**Request-DTO (verbatim, `handler.go`, неэкспортируемые).** Все UUID-поля приходят `string`, парсятся `uuid.Parse` в хендлере.

| Структура (эндпоинт) | Поле | JSON-тег | Go-тип |
|----------------------|------|----------|--------|
| `scanTTNRequest` (gate/scan-ttn) | `TTNCode` | `ttn_code` | `string` |
| `scanGateCargoplaceRequest` (gate/scan-cargoplace) | `ShipmentID` | `shipment_id` | `string` (UUID) |
| | `CargoplaceCode` | `cargoplace_code` | `string` |
| `acceptShipmentRequest` (gate/accept-shipment) | `ShipmentID` | `shipment_id` | `string` (UUID) |
| `scanTableCargoplaceRequest` (table/scan-cargoplace) | `CargoplaceID` | `cargoplace_id` | `string` (UUID) |
| `scanBoxRequest` (table/scan-box) | `CargoplaceID` | `cargoplace_id` | `string` (UUID) |
| | `BoxBarcode` | `box_barcode` | `string` |
| `scanSKURequest` (table/scan-sku) | `CargoplaceID` | `cargoplace_id` | `string` (UUID) |
| | `BoxID` | `box_id` | `string` (UUID) |
| | `Barcode` | `barcode` | `string` |
| `scanQRRequest` (table/scan-qr) | `CargoplaceID` | `cargoplace_id` | `string` (UUID) |
| | `BoxID` | `box_id` | `string` (UUID) |
| | `SKUID` | `sku_id` | `string` (UUID) |
| | `QRCode` | `qr_code` | `string` |
| `closeBoxRequest` (table/close-box) | `BoxID` | `box_id` | `string` (UUID) |
| `scanBufferRequest` (table/scan-buffer) | `CargoplaceID` | `cargoplace_id` | `string` (UUID) |
| | `BufferBinID` | `buffer_bin_id` | `string` (UUID) |
| `closeCargoplaceDraftRequest` (table/close-cargoplace/draft) | `CargoplaceID` | `cargoplace_id` | `string` (UUID) |

Тело `table/close-cargoplace/submit` — общая структура `signedops.SubmitRequest` (не локальная копия): `OperationID operation_id string`, `Assertions assertions []{ActionID action_id string, signedaction.AssertionRequest}`; `AssertionRequest` — `CredentialID credential_id`, `AuthenticatorData authenticator_data`, `ClientDataJSON client_data_json`, `Signature signature`, все четыре поля в base64url без паддинга.

**Response-DTO (verbatim, `types.go`, экспортируемые):**

`ScanTTNResult`:

| Поле | JSON-тег | Go-тип |
|------|----------|--------|
| `ShipmentID` | `shipment_id` | `uuid.UUID` |
| `TTNCode` | `ttn_code` | `string` |
| `Status` | `status` | `string` |
| `Cargoplaces` | `cargoplaces` | `[]cargoplaceView` |
| `TotalCargoplaces` | `total_cargoplaces` | `int` |
| `ReceivedCargoplaces` | `received_cargoplaces` | `int` |

`cargoplaceView` (вложенный): `CargoplaceID cargoplace_id uuid.UUID`, `CargoplaceCode cargoplace_code string`, `Status status string`.

`ScanGateCargoplaceResult`:

| Поле | JSON-тег | Go-тип |
|------|----------|--------|
| `CargoplaceID` | `cargoplace_id` | `uuid.UUID` |
| `CargoplaceCode` | `cargoplace_code` | `string` |
| `Status` | `status` | `string` |
| `ReceivedAtGateAt` | `received_at_gate_at` | `time.Time` |
| `Progress` | `progress` | `progress` |

`progress` (вложенный): `Total total int`, `Received received int`, `Remaining remaining int`, `NotReceived not_received int`. **NB:** в `ScanGateCargoplaceResult` поле `not_received` всегда 0 (не заполняется); в `AcceptShipmentResult.Summary` поле `remaining` всегда 0 (не заполняется).

`AcceptShipmentResult`: `ShipmentID shipment_id uuid.UUID`, `Status status string`, `Summary summary progress`.

`ScanTableCargoplaceResult`:

| Поле | JSON-тег | Go-тип |
|------|----------|--------|
| `CargoplaceID` | `cargoplace_id` | `uuid.UUID` |
| `CargoplaceCode` | `cargoplace_code` | `string` |
| `Status` | `status` | `string` |
| `ExpectedSKUs` | `expected_skus` | `[]ExpectedSKU` |
| `TotalExpected` | `total_expected` | `int` |

`ExpectedSKU`: `SKUID sku_id uuid.UUID`, `SKUName sku_name string`, `ExpectedQty expected_qty int`.

`ScanBoxResult`: `BoxID box_id uuid.UUID`, `BoxBarcode box_barcode string`, `Status status string`.

`ScanSKUResult`: `SKUID sku_id uuid.UUID`, `SKUName sku_name string`, `Barcode barcode string`, `Message message string` (всегда `"Наклейте QR на товар"`).

`ScanQRResult`:

| Поле | JSON-тег | Go-тип |
|------|----------|--------|
| `ProductID` | `product_id` | `uuid.UUID` |
| `SKUID` | `sku_id` | `uuid.UUID` |
| `SKUName` | `sku_name` | `string` |
| `QRCode` | `qr_code` | `string` |
| `Status` | `status` | `domain.ProductStatus` |
| `Progress` | `progress` | `receivingProgress` |

`receivingProgress`: `ReceivedInCargoplace received_in_cargoplace int`, `ExpectedInCargoplace expected_in_cargoplace int`.

`CloseBoxResult`: `BoxID box_id uuid.UUID`, `Status status string`, `ProductsInBox products_in_box int`.

`ScanBufferResult`: `BufferBinID buffer_bin_id uuid.UUID`, `BufferCode buffer_code string`, `ProductsPlaced products_placed int`.

`CloseCargoplaceResult`:

| Поле | JSON-тег | Go-тип |
|------|----------|--------|
| `CargoplaceID` | `cargoplace_id` | `uuid.UUID` |
| `OperationID` | `operation_id` | `uuid.UUID` |
| `Status` | `status` | `string` |
| `Summary` | `summary` | `CloseCargoplaceSummary` |
| `OutboxEventsCreated` | `outbox_events_created` | `int` (число частей-действий, не товаров) |

`CloseCargoplaceSummary`: `ProductsReceived products_received int`, `ProductsExpected products_expected int`, `Shortage shortage int`, `ShortageBySKU shortage_by_sku []ShortageBySKU`. `ShortageBySKU`: `SKUName sku_name string`, `Expected expected int`, `Received received int`, `Shortage shortage int`.

**Методы Service** (`*Service`):

| Сигнатура | Поведение |
|-----------|-----------|
| `ScanTTN(ctx, operatorID, ttnCode) (*ScanTTNResult, error)` | Поиск поставки по TTN; если `CREATED` → `GATE_IN_PROGRESS` в транзакции (race-safe: перепроверка на `ErrShipmentNotFound`); лог `SCAN_TTN`. |
| `ScanCargoplace(ctx, operatorID, shipmentID, cargoplaceCode) (*ScanGateCargoplaceResult, error)` | В одной транзакции: грузоместо → `RECEIVED_AT_GATE` (status-guard), лог `SCAN_CARGOPLACE`, `tryAutoCloseShipment` (всё принято → `GATE_CLOSED`). |
| `AcceptShipment(ctx, operatorID, shipmentID) (*AcceptShipmentResult, error)` | Ручное закрытие КПП: оставшиеся `EXPECTED` → `NOT_RECEIVED`, поставка → `GATE_CLOSED`, логи `SHIPMENT_ACCEPTED`/`MARK_NOT_RECEIVED`. |
| `ScanTableCargoplace(ctx, operatorID, cargoplaceID) (*ScanTableCargoplaceResult, error)` | `RECEIVED_AT_GATE` → `TABLE_IN_PROGRESS`; вернуть манифест ожидаемых SKU. |
| `ScanBox(ctx, operatorID, cargoplaceID, boxBarcode) (*ScanBoxResult, error)` | Upsert коробки; отказ если существующая `CLOSED`. |
| `ScanSKU(ctx, operatorID, cargoplaceID, boxID, barcode) (*ScanSKUResult, error)` | Резолв barcode→SKU; **product НЕ создаётся**; лог `SCAN_SKU`. |
| `ScanQR(ctx, operatorID, cargoplaceID, boxID, skuID, qrCode) (*ScanQRResult, error)` | Вставка `Product` со статусом `RECEIVED` и уникальным QR; лог `SCAN_QR`. Дубль QR → `ErrQRAlreadyExists`. |
| `CloseBox(ctx, operatorID, boxID) (*CloseBoxResult, error)` | `OPEN`→`CLOSED`; проверка что родитель ещё `TABLE_IN_PROGRESS`. |
| `ScanBuffer(ctx, operatorID, cargoplaceID, bufferBinID) (*ScanBufferResult, error)` | Перенос всех `RECEIVED` товаров из `CLOSED` коробок в буфер (`section=BUFFER`, case-insensitive). |
| `CloseCargoplace` | удалён: вместо него `DraftCloseCargoplace` и `SubmitCloseCargoplace` (см. ниже). |

**Подписанное закрытие грузоместа** (`close_signed.go`) — двухшаговый поток вместо удалённого `CloseCargoplace`:

| Сигнатура | Поведение |
|-----------|-----------|
| `DraftCloseCargoplace(ctx, operatorID, cargoplaceID) (*signedops.Operation, rpID string, error)` | Guard статуса `TABLE_IN_PROGRESS`, `ListClosableProductIDs` → `signedops.NewOperation(TypeReceive, …)`: товары сортируются по `itemId` и режутся на части ≤ `signedops.MaxItemsPerAction` (2000), у каждой — свой `action_id`, соль и challenge; `occurredAt` обрезан до миллисекунд. Пишет только `warehouse_actions`. |
| `SubmitCloseCargoplace(ctx, operatorID, operationID, assertions) (*CloseCargoplaceResult, error)` | Загружает операцию, проверяет владельца и `TypeReceive`, `submitted_at IS NULL`, активный ключ-подписант; `signedops.Envelopes` верифицирует WebAuthn каждой части (type, challenge, origin, rpIdHash, UP/UV, low-s). Затем `CloseCargoplaceWithOutbox` в одной транзакции: lock грузоместа → сверка состава товаров с подписанным (`DRAFT_STALE`) → lock ключа `FOR SHARE` → `TABLE_CLOSED` → лог `CLOSE_CARGO` (с `operation_id`) → по строке `outbox_events` на каждую часть. |

**Интерфейс `receivingRepository`** (`service.go`) — полный список сигнатур:

```go
type receivingRepository interface {
    WithTx(ctx context.Context, fn func(receivingRepository) error) error
    GetShipmentByTTN(ctx context.Context, ttnCode string) (*domain.InboundShipment, error)
    GetShipmentByID(ctx context.Context, shipmentID uuid.UUID) (*domain.InboundShipment, error)
    ListCargoplacesByShipment(ctx context.Context, shipmentID uuid.UUID) ([]domain.Cargoplace, error)
    GetCargoplaceByShipmentAndCode(ctx context.Context, shipmentID uuid.UUID, cargoplaceCode string) (*domain.Cargoplace, error)
    GetCargoplaceByID(ctx context.Context, cargoplaceID uuid.UUID) (*domain.Cargoplace, error)
    GetCargoplaceByIDForUpdate(ctx context.Context, cargoplaceID uuid.UUID) (*domain.Cargoplace, error)
    UpdateShipmentStatus(ctx context.Context, shipmentID uuid.UUID, newStatus, expectedStatus string) error
    UpdateCargoplaceReceivedAtGate(ctx context.Context, cargoplaceID uuid.UUID, newStatus, expectedStatus string, receivedAt time.Time) error
    UpdateCargoplaceStatus(ctx context.Context, cargoplaceID uuid.UUID, newStatus, expectedStatus string) error
    MarkExpectedAsNotReceived(ctx context.Context, shipmentID uuid.UUID, notReceivedStatus string, operatorID uuid.UUID) error
    CountCargoplaces(ctx context.Context, shipmentID uuid.UUID) (int, error)
    CountCargoplacesByStatus(ctx context.Context, shipmentID uuid.UUID, status string) (int, error)
    ListExpectedSKUsByCargoplace(ctx context.Context, cargoplaceID uuid.UUID) ([]ExpectedSKU, error)
    UpsertBox(ctx context.Context, cargoplaceID uuid.UUID, boxBarcode string, status string) (*domain.Box, error)
    GetBoxByCargoplaceAndBarcode(ctx context.Context, cargoplaceID uuid.UUID, boxBarcode string) (*domain.Box, error)
    GetBoxByID(ctx context.Context, boxID uuid.UUID) (*domain.Box, error)
    UpdateBoxStatus(ctx context.Context, boxID uuid.UUID, status string) error
    CountProductsByBox(ctx context.Context, boxID uuid.UUID) (int, error)
    GetSKUByBarcode(ctx context.Context, barcode string) (*domain.SKU, error)
    GetSKUByID(ctx context.Context, skuID uuid.UUID) (*domain.SKU, error)
    InsertProduct(ctx context.Context, product *domain.Product) error
    GetBinByID(ctx context.Context, binID uuid.UUID) (*domain.Bin, error)
    ScanBufferWithLog(ctx context.Context, cargoplaceID uuid.UUID, bufferBinID uuid.UUID, logParams *TableLogParams) (int, error)
    CloseCargoplaceWithOutbox(ctx context.Context, params *CloseCargoplaceParams) (*CloseCargoplaceTxResult, error)
    ListClosableProductIDs(ctx context.Context, cargoplaceID uuid.UUID) ([]uuid.UUID, error)
    CountProductsByCargoplace(ctx context.Context, cargoplaceID uuid.UUID) (int, error)
    CountExpectedItemsByCargoplace(ctx context.Context, cargoplaceID uuid.UUID) (int, error)
    InsertReceivingGateLog(ctx context.Context, params *GateLogParams) error
    InsertReceivingTableLog(ctx context.Context, params *TableLogParams) error
}
```

`WithTx` подменяет `r.q` на `pgx.Tx`; используется всеми методами сервиса **кроме** `CloseCargoplace` (который зовёт `CloseCargoplaceWithOutbox` напрямую). `UpdateShipmentStatus`/`UpdateCargoplaceStatus` возвращают `ErrShipmentNotFound`/`...NotFound` при `RowsAffected=0` — это намеренный CAS-сигнал «кто-то уже сделал переход», сервис перепроверяет статус.

```go
func (r *Repository) CloseCargoplaceWithOutbox(ctx, params *CloseCargoplaceParams) (*CloseCargoplaceTxResult, error)
```

> [!WARNING] `CloseCargoplaceWithOutbox` управляет **собственной** транзакцией (`r.db.Begin`), а НЕ через `WithTx`. Явный комментарий в коде запрещает оборачивать его в `WithTx` — иначе две разные коннекции из пула сломают `FOR UPDATE`-изоляцию. Сервис зовёт его напрямую.

**Эндпоинты** (все `POST`, JWT + `RequireOperator`):

| Маршрут | Назначение | Outbox |
|---------|-----------|--------|
| `/receiving/gate/scan-ttn` | старт приёмки по TTN | — |
| `/receiving/gate/scan-cargoplace` | приём грузоместа на КПП | — |
| `/receiving/gate/accept-shipment` | ручное закрытие КПП | — |
| `/receiving/table/scan-cargoplace` | старт обработки на столе | — |
| `/receiving/table/scan-box` | идентификация/создание коробки | — |
| `/receiving/table/scan-sku` | резолв SKU по barcode | — |
| `/receiving/table/scan-qr` | регистрация product + QR | — |
| `/receiving/table/close-box` | закрытие коробки | — |
| `/receiving/table/scan-buffer` | перенос в буфер | — |
| `/receiving/table/close-cargoplace/draft` | черновик Receive-действия (части с `challenge`) | пишет `warehouse_actions` |
| `/receiving/table/close-cargoplace/submit` | подписи частей → финализация грузоместа | **`signed_action` / `wms.action.v1`**, 1 строка на часть (event_id = action_id) |

**Outbox:** пишется только на подписанном пути. INSERT в `public.outbox_events` есть ровно в двух местах: `wms/internal/signedops/repository.go` (`WriteActions` — все четыре складских `/submit`) и `wms/internal/passkeys/actions_repository.go` (`SubmitKeyAction`). Событий на товар больше нет. Идемпотентность — через status-guard на `cargoplaces` (повторный вызов → `CARGOPLACE_NOT_IN_PROGRESS` до любого INSERT в outbox) и `submitted_at` операции (`OPERATION_ALREADY_SUBMITTED`, 409). Уникального констрейнта на `(aggregate_id, payload_hash)` нет.

### 5.2 `putaway` — раскладка (этап 3)

**Назначение:** перенос товаров из буфера в постоянную ячейку хранения; на финальном шаге `RECEIVED`→`STORED` подписанным PutAway-действием (`draft → passkey → submit`). Business-flow: [flows/putaway-flow.md](../flows/putaway-flow.md).

**DTO (verbatim, `handler.go`, экспортируемые).** Все ID — строки UUID (в DTO нет `uuid.UUID`).

`ScanBufferRequest` → `ScanBufferResponse`:

| DTO | Поле | JSON-тег | Go-тип |
|-----|------|----------|--------|
| `ScanBufferRequest` | `BufferBinID` | `buffer_bin_id` | `string` |
| `ScanBufferResponse` | `BufferBinID` | `buffer_bin_id` | `string` |
| | `BufferCode` | `buffer_code` | `string` |
| | `Products` | `products` | `[]ProductBufferItemResponse` |
| | `TotalProducts` | `total_products` | `int` |
| `ProductBufferItemResponse` | `ProductID` | `product_id` | `string` |
| | `SKUName` | `sku_name` | `string` |
| | `QRCode` | `qr_code` | `string` |
| | `Status` | `status` | `string` |

`ScanProductRequest` → `ScanProductResponse`:

| DTO | Поле | JSON-тег | Go-тип |
|-----|------|----------|--------|
| `ScanProductRequest` | `ProductID` | `product_id` | `string` |
| | `BufferBinID` | `buffer_bin_id` | `string` |
| `ScanProductResponse` | `ProductID` | `product_id` | `string` |
| | `SKUName` | `sku_name` | `string` |
| | `QRCode` | `qr_code` | `string` |
| | `CartSize` | `cart_size` | `int` (DB-derived) |

`placeDraftRequest` → `ScanStorageBinResponse` (`place_signed_handler.go`):

| DTO | Поле | JSON-тег | Go-тип |
|-----|------|----------|--------|
| `placeDraftRequest` (scan-storage-bin/draft) | `ProductIDs` | `product_ids` | `[]string` (1..200) |
| | `StorageBinID` | `storage_bin_id` | `string` |
| `ScanStorageBinResponse` (submit) | `StorageBinID` | `storage_bin_id` | `string` |
| | `StorageBinCode` | `storage_bin_code` | `string` |
| | `ProductsPlaced` | `products_placed` | `int` |
| | `OutboxEventsCreated` | `outbox_events_created` | `int` (число частей, не товаров) |
| | `OperationID` | `operation_id` | `string` |

**Интерфейс `putawayRepository`** (`service.go`):

```go
type putawayRepository interface {
    WithTx(ctx context.Context, fn func(putawayRepository) error) error
    GetBufferBinByID(ctx context.Context, bufferBinID uuid.UUID) (*domain.Bin, error)
    GetStorageBinByID(ctx context.Context, storageBinID uuid.UUID) (*domain.Bin, error)
    ListProductsByBufferBin(ctx context.Context, bufferBinID uuid.UUID) ([]*ProductBufferItem, error)
    GetProductByID(ctx context.Context, productID uuid.UUID) (*domain.Product, error)
    GetProductsByIDForUpdate(ctx context.Context, productID uuid.UUID) (*domain.Product, error)
    GetSKUByProductID(ctx context.Context, productID uuid.UUID) (*domain.SKU, error)
    UpdateProductStorage(ctx context.Context, productID, binID uuid.UUID) error
    InsertPutaway(ctx context.Context, params *InsertPutawayParams) error
    LockOperation(ctx context.Context, operationID, createdBy uuid.UUID) (*signedops.Operation, error)
    LockSigner(ctx context.Context, credentialID []byte) error
    WriteActions(ctx context.Context, op *signedops.Operation, payloads [][]byte) error
    CheckChainStatus(ctx context.Context, productIDs []uuid.UUID, aggregateType string) error
    CountReceivedInBuffer(ctx context.Context, bufferBinID uuid.UUID) (int, error)
}
```

Sentinel-ошибки (`errors.go`) → HTTP: `ErrBufferBinNotFound`/`ErrStorageBinNotFound`/`ErrProductNotFound` → 404; `ErrProductNotInBuffer`/`ErrProductNotReceived`/`ErrDraftStale` → 409; `ErrInvalidInput` → 400; `ledger.ErrChainEventRejected` → 409; `passkeys.ErrSignerKeyNotActive` → 409 `SIGNER_KEY_NOT_ACTIVE`.

**Методы Service:**

| Сигнатура | Поведение |
|-----------|-----------|
| `GetBufferProducts(ctx, operatorID, bufferBinID) (*ScanBufferResponse, error)` | Read-only: список `RECEIVED` товаров в буфере (`section=BUFFER`). |
| `AddToPutawayCart(ctx, operatorID, productID, bufferBinID) (*ScanProductResponse, error)` | Read-only валидация product; `CartSize` — DB-derived `CountReceivedInBuffer` (не in-memory корзина). |
| `DraftPlaceToStorageBin(ctx, operatorID, productIDs, storageBinID) (*signedops.Operation, rpID string, error)` | Единственный мутирующий путь — шаг 1: chain-gate, `signedops.NewOperation(TypePutAway, …)` с частями ≤ 2000, запись `warehouse_actions`. |
| `SubmitPlaceToStorageBin(ctx, operatorID, operationID, assertions) (*ScanStorageBinResponse, error)` | Шаг 2: проверка всех assertion, повторный chain-gate на подписанном наборе, затем транзакция и запись outbox. |

**Гейт chain-status:** в обоих шагах `repo.CheckChainStatus(ctx, productIDs, AggregateReceiving)` — если у товара `FAILED` receiving-действие → `ErrChainEventRejected` (409), без мутаций.

**Эндпоинты** (все `POST`, JWT + `RequireOperator`):

| Маршрут | Outbox |
|---------|--------|
| `/putaway/scan-buffer` | — (read-only) |
| `/putaway/scan-product` | — (read-only) |
| `/putaway/scan-storage-bin/draft` | — (пишет `warehouse_actions`); лимит `product_ids` — `maxProductIDsPerRequest = 200` |
| `/putaway/scan-storage-bin/submit` | **`signed_action` / `wms.action.v1`**, 1 строка на часть (event_id = action_id) |

В транзакции `scan-storage-bin/submit`: блокировка товаров `FOR UPDATE` по возрастанию `product_id` → проверка буфера и статуса `RECEIVED` (иначе `DRAFT_STALE`) → `LockOperation`/`LockSigner` → `UpdateProductStorage` (`STORED`) → `InsertPutaway` (`wms_ops.putaways`, у строки — `action_id` своей части) → `WriteActions` (bulk outbox). `storage_bin_id` не должен быть `BUFFER`/`SHIPPING_BUFFER` и обязан иметь `volume > 0`.

### 5.3 `assembly` — аллокация и сборка (этап 4)

**Назначение:** аллокация склада на заказы, выдача pick-листа, фиксация пика (без outbox), выгрузка корзины в shipping-buffer подписанным Pick-действием. Business-flow: [flows/assembly-flow.md](../flows/assembly-flow.md).

**DTO (verbatim, `types.go`, экспортируемые).** Все ID — строки UUID.

`AllocateRequest` → `AllocateResponse`:

| DTO | Поле | JSON-тег | Go-тип |
|-----|------|----------|--------|
| `AllocateRequest` | `DestinationID` | `destination_id` | `string` |
| `AllocateResponse` | `AllocatedOrders` | `allocated_orders` | `int` |
| | `AllocatedProducts` | `allocated_products` | `int` |
| | `InsufficientOrders` | `insufficient_orders` | `[]InsufficientOrder` |
| `InsufficientOrder` | `OrderID` | `order_id` | `string` |
| | `MissingSKUs` | `missing_skus` | `[]InsufficientSKU` |
| `InsufficientSKU` | `SKUID` | `sku_id` | `string` |
| | `SKUName` | `sku_name` | `string` |
| | `MissingQty` | `missing_qty` | `int` |

`TaskResponse` (response `GET /assembly/tasks`):

| DTO | Поле | JSON-тег | Go-тип |
|-----|------|----------|--------|
| `TaskResponse` | `Tasks` | `tasks` | `[]TaskItem` |
| `TaskItem` | `TaskID` | `task_id` | `string` |
| | `ProductID` | `product_id` | `string` |
| | `QRCode` | `qr_code` | `string` |
| | `SKUName` | `sku_name` | `string` |
| | `FromBinCode` | `from_bin_code` | `string` |
| | `FromBinSection` | `from_bin_section` | `string` |
| | `OrderNo` | `order_no` | `string` |

`PickRequest` → `PickResponse`:

| DTO | Поле | JSON-тег | Go-тип |
|-----|------|----------|--------|
| `PickRequest` | `ProductID` | `product_id` | `string` |
| `PickResponse` | `ProductID` | `product_id` | `string` |
| | `CartSize` | `cart_size` | `int` (DB-derived) |

`ScanShippingBufferRequest` → `ScanShippingBufferResponse`:

| DTO | Поле | JSON-тег | Go-тип |
|-----|------|----------|--------|
| `ScanShippingBufferRequest` | `BufferBinID` | `buffer_bin_id` | `string` |
| `ScanShippingBufferResponse` | `BufferBinID` | `buffer_bin_id` | `string` |
| | `ProductsPlaced` | `products_placed` | `int` |
| | `OrdersAssembled` | `orders_assembled` | `int` |

Внутренние working-типы (не по HTTP): `AllocatedProduct`, `Task`.

**Интерфейс `assemblyRepository`** (выборочная выдержка — **не полный интерфейс**; `service.go:20-41`, всего 20 методов). Здесь опущены реальные методы `GetOrderLinesByOrderID`, `GetAllocateProductsForSKU`, `GetSKUByID`, `GetBinSectionByID`, `GetPendingTaskByProductForUpdate`, `GetProductByIDForUpdate`, `GetShippingBufferBinByID` — для мок-конструкции сверяйтесь с исходником:

```go
WithTx(ctx context.Context, fn func(assemblyRepository) error) error
GetOrdersByDestinationForUpdate(ctx, destinationID uuid.UUID) ([]domain.Order, error)
UpdateProductAllocated(ctx, productID, orderID uuid.UUID) error
BatchInsertAssemblyTasks(ctx context.Context, tasks []Task) error  // batch на Allocate, status=PENDING
UpdateOrderStatus(ctx context.Context, orderID uuid.UUID, status string) error
GetTasks(ctx, destinationID, operatorID uuid.UUID, status string) ([]TaskItem, error)
CheckChainStatus(ctx, productIDs []uuid.UUID, aggregateType string) error  // делегат ledger.CheckChainStatus
MarkTaskDone(ctx, eventID, operatorID uuid.UUID) error    // WHERE status='PENDING'
SetProductAssembled(ctx, productID uuid.UUID) error
InsertPickOutboxEvent — удалён; в `assembly/pick` outbox не пишется
CountAssembledByOperator(ctx, operatorID uuid.UUID) (int, error)  // COUNT(DISTINCT product_id)
ListOperatorBasket(ctx, operatorID, destinationID uuid.UUID) ([]BasketItem, error)  // read-only корзина для черновика
LockOperatorBasket(ctx, operatorID, destinationID uuid.UUID) ([]BasketItem, error)  // FOR UPDATE в submit
MoveProductsToShippingBuffer(ctx, productIDs []uuid.UUID, bufferBinID uuid.UUID) error
StampTaskUnload(ctx, actionID uuid.UUID, productIDs []uuid.UUID, operatorID, destinationID uuid.UUID, occurredAt time.Time) error  // action_id + unloaded_at
LockOperation / LockSigner / WriteActions  // подписанная операция и outbox
```

Sentinel-ошибки (`errors.go`): `ErrDestinationNotFound`, `ErrInsufficientStock`, `ErrSKUNotFound`, `ErrOrderNotNew`, `ErrNoTaskForProduct`, `ErrProductNotAllocated`, `ErrInvalidInput`, `ErrBinNotShippingBuffer`, `ErrCartEmpty`, `ErrDestinationMismatch`, `ErrDraftStale`.

**Методы Service:**

| Сигнатура | Поведение |
|-----------|-----------|
| `Allocate(ctx, destinationID) (*AllocateResponse, error)` | Транзакция: `NEW`-заказы destination `FOR UPDATE`; per-order `FOR UPDATE SKIP LOCKED` FIFO по `STORED`; нехватка → заказ в `InsufficientOrders`; иначе products→`ALLOCATED`, batch-insert `assembly_tasks` (`PENDING`), order→`ALLOCATED`. Частичная аллокация — НЕ ошибка. **Outbox не пишется.** |
| `GetTasks(ctx, destinationID, operatorID, status) (*TaskResponse, error)` | Read-only. `operatorID=Nil` → без фильтра по оператору. Default `status=PENDING`. |
| `Pick(ctx, operatorID, productID) (*PickResponse, error)` | Pre-check `CheckChainStatus(AggregatePutaway)` (вне транзакции). Транзакция: lock task+product, product должен быть `ALLOCATED`, task→`DONE` (+`operator_id`), product→`ASSEMBLED`. **Outbox НЕ пишется** (#14). После commit — `CountAssembledByOperator` для `cart_size`. |
| `DraftUnloadToShippingBuffer(ctx, operatorID, bufferBinID) (*signedops.Operation, rpID string, error)` | Читает корзину (`ListOperatorBasket`), режет на части ≤ 2000, пишет `warehouse_actions` (`TypePick`). Никаких изменений товаров. |
| `SubmitUnloadToShippingBuffer(ctx, operatorID, operationID, assertions) (*ScanShippingBufferResponse, error)` | Проверяет все assertion, затем транзакция: lock корзины и операции (состав должен совпасть с подписанным — иначе `DRAFT_STALE`), `MoveProductsToShippingBuffer` → `READY_TO_SHIP`, `StampTaskUnload` (по частям), `UpdateOrdersToAssembled`, `WriteActions` (outbox). |

**Эндпоинты:**

| Маршрут | Метод | Гейт | Outbox |
|---------|-------|------|--------|
| `/assembly/allocate` | `POST` | **`RequireAdminOrOperator`** | — |
| `/assembly/tasks` | `GET` | `RequireOperator` | — (read-only). Query: `destination_id` (req UUID), `operator_id` (opt), `status` (opt) |
| `/assembly/pick` | `POST` | `RequireOperator` | — |
| `/assembly/scan-shipping-buffer/draft` | `POST` | `RequireOperator` | — (пишет `warehouse_actions`) |
| `/assembly/scan-shipping-buffer/submit` | `POST` | `RequireOperator` | **`signed_action` / `wms.action.v1`**, 1 строка на часть |

> [!NOTE] В `GetTasks` `operatorID` из JWT извлекается и **сразу игнорируется** (`_ = operatorID`); фильтрация — только по query-параметру `operator_id`, и она нестрогая: `operator_id IS NULL OR operator_id = $3`. Подбор с полки в цепь не пишет — Pick-действие создаётся только при выгрузке корзины в буфер.

### 5.4 `shipping` — отгрузка (этап 5)

**Назначение:** скан shipping-buffer, регистрация водителя/рейса, физическая отгрузка (products→`SHIPPED`, аудит, outbox, продвижение заказов, departure рейса при опустошении буфера). Bedrock-flow: [flows/shipping-flow.md](../flows/shipping-flow.md).

**Request-DTO (verbatim, `types.go`, неэкспортируемые HTTP-структуры):**

| Структура | Поле | JSON-тег | Go-тип |
|-----------|------|----------|--------|
| `scanBufferRequest` | `BufferBinID` | `buffer_bin_id` | `string` |
| `scanDriverRequest` | `DispatchCode` | `dispatch_code` | `string` |
| `shipHTTPRequest` | `BufferBinID` | `buffer_bin_id` | `string` |
| | `DispatchID` | `dispatch_id` | `string` |
| | `ProductIDs` | `product_ids` | `[]string` (опц.; пусто = bulk; max 200) |

Сервисный `ShipRequest` (экспортируемый, не по HTTP — собирается в хендлере): `BufferBinID uuid.UUID`, `DispatchID uuid.UUID`, `OperatorID uuid.UUID`, `ProductIDs []uuid.UUID`.

**Response-DTO (verbatim, экспортируемые):**

`ScanBufferResponse`: `BufferBin buffer_bin BufferBinResponse`, `Products products []ScanBufferProductResponse`, `Count count int`.
`BufferBinResponse`: `ID id uuid.UUID`, `Code code string`, `Destination destination DestinationResponse`.
`DestinationResponse`: `ID id uuid.UUID`, `Code code string`, `Name name string`.
`ScanBufferProductResponse`: `ProductID product_id uuid.UUID`, `QRCode qr_code string`, `SKUName sku_name string`, `OrderExternalNo order_external_no *string` (nullable).

`ScanDriverResponse`:

| Поле | JSON-тег | Go-тип |
|------|----------|--------|
| `DispatchID` | `dispatch_id` | `uuid.UUID` |
| `DispatchCode` | `dispatch_code` | `string` |
| `VehicleNumber` | `vehicle_number` | `string` |
| `DriverName` | `driver_name` | `string` |
| `DriverPhone` | `driver_phone` | `*string` (nullable) |
| `Destination` | `destination` | `DestinationResponse` |
| `Status` | `status` | `domain.OutboundDispatchStatus` |
| `ArrivedAt` | `arrived_at` | `*time.Time` (nullable) |

`ShipResponse`:

| Поле | JSON-тег | Go-тип |
|------|----------|--------|
| `ProductsShipped` | `products_shipped` | `int` |
| `OutboxEventsCreated` | `outbox_events_created` | `int` |
| `OrdersCompleted` | `orders_completed` | `int` |
| `OrdersPartiallyShipped` | `orders_partially_shipped` | `int` |
| `DispatchDeparted` | `dispatch_departed` | `bool` |
| `BufferRemaining` | `buffer_remaining` | `int` |
| `OperationID` | `operation_id` | `string` |

**Интерфейс `shippingRepository`** (`service.go`) — сигнатуры:

```go
WithTx(ctx context.Context, fn func(shippingRepository) error) error
GetBinWithDestinationByID(ctx, binID uuid.UUID) (*BufferBinRecord, error)
GetBufferBinForUpdate(ctx, binID uuid.UUID) (*BufferBinRecord, error)            // FOR UPDATE OF b
ListReadyToShipProductsByBin(ctx, binID uuid.UUID) ([]ReadyToShipProduct, error)
GetDispatchByCode(ctx, code string) (*DispatchRecord, error)
GetDispatchForUpdate(ctx, dispatchID uuid.UUID) (*DispatchRecord, error)         // FOR UPDATE OF d
UpdateDispatchToAtGate(ctx, dispatchCode string) (*DispatchRecord, error)        // WHERE status='SCHEDULED'
ListProductsForShip / LockProductsForShip(ctx, binID uuid.UUID, productIDs []uuid.UUID) ([]ProductForShip, error)
UnconfirmedProducts(ctx, productIDs []uuid.UUID, aggregate string) ([]uuid.UUID, error)  // гейт PICKING_NOT_CONFIRMED
BatchUpdateProductsShipped(ctx, productIDs []uuid.UUID) error
BatchInsertShippings(ctx, events []shippingEvent, dispatchID, operatorID uuid.UUID, occurredAt time.Time, actionID uuid.UUID) error
UpdateOrdersShippedConditional(ctx, orderIDs []uuid.UUID) (completed, partial int, err error)  // two-statement FOR UPDATE
CountReadyToShipProductsInBuffer(ctx, binID uuid.UUID) (int, error)              // по всей зоне destination, выведенной из bin
UpdateDispatchDeparted(ctx, dispatchID uuid.UUID) error                         // WHERE status='AT_GATE'
LockOperation / LockSigner / WriteActions                                        // подписанная операция и outbox
```

Sentinel-ошибки (`errors.go`): `ErrInvalidInput`, `ErrBinNotFound`, `ErrBinNotShippingBuffer`, `ErrDispatchNotFound`, `ErrDispatchAlreadyDeparted`, `ErrDispatchCancelled`, `ErrDestinationMismatch`, `ErrDispatchNotAtGate`, `ErrBufferEmpty`, `ErrProductNotInBuffer`, `ErrDraftStale`, `ErrPickingNotConfirmed` (несёт `PickingNotConfirmedError`, число товаров уходит в `message`).

**Методы Service:**

| Сигнатура | Поведение |
|-----------|-----------|
| `ScanBuffer(ctx, operatorID, bufferBinID) (*ScanBufferResponse, error)` | Read-only: bin `SHIPPING_BUFFER` + destination; список `READY_TO_SHIP`. |
| `ScanDriver(ctx, operatorID, dispatchCode) (*ScanDriverResponse, error)` | Условный `UPDATE ... status='AT_GATE' WHERE status='SCHEDULED'`; при 0 строк — SELECT-фолбэк: `DEPARTED`→`ErrDispatchAlreadyDeparted`, `CANCELLED`→`ErrDispatchCancelled`, `AT_GATE`→идемпотентный успех. |
| `DraftShip(ctx, operatorID, bufferBinID, dispatchID, productIDs) (*signedops.Operation, rpID string, error)` | Bin+dispatch+destination gates, bulk/spot выбор товаров, гейт `UnconfirmedProducts(AggregatePicking)` → `PICKING_NOT_CONFIRMED`; режет на части ≤ 2000 и пишет `warehouse_actions` (`TypeShip`). |
| `SubmitShip(ctx, operatorID, operationID, assertions) (*ShipResponse, error)` | Проверка assertion + повторный гейт сборки; одна транзакция: lock bin/dispatch (`AT_GATE`, destination match) → lock товаров (состав = подписанный, иначе `DRAFT_STALE`) → `SHIPPED` → `wms_ops.shippings` (с `action_id` части) → заказы → `CountReadyToShipProductsInBuffer`; если 0 → `UpdateDispatchDeparted` → `WriteActions` (outbox по частям). |

**Эндпоинты** (все `POST`, JWT + `RequireOperator`):

| Маршрут | Outbox |
|---------|--------|
| `/shipping/scan-buffer` | — (read-only) |
| `/shipping/scan-driver` | — (пишет `outbound_dispatches.status='AT_GATE'` условно) |
| `/shipping/ship/draft` | — (пишет `warehouse_actions`). Лимит `product_ids` = 200 |
| `/shipping/ship/submit` | **`signed_action` / `wms.action.v1`**, 1 строка на часть |

> [!WARNING] Особенности shipping:
> - `CountReadyToShipProductsInBuffer` считает по **всей зоне destination** (всем `SHIPPING_BUFFER`-ячейкам), не по одной ячейке — `dispatch_departed=true` только когда вся зона пуста.
> - `UpdateOrdersShippedConditional` использует двухстейтментный `SELECT ... FOR UPDATE` + `UPDATE` (избежать READ COMMITTED-аномалии при конкурентных ship).
> - Гейт на `submit` — **обратный**: `UnconfirmedProducts(AggregatePicking)` не даёт отгрузить товар, чьё Pick-действие не `COMMITTED` в цепи (`409 PICKING_NOT_CONFIRMED`). Проверяется дважды — на `draft` и на `submit`.
> - `aggregate_type` в SQL для строки outbox теперь всегда `'signed_action'`; этапы различаются колонкой `warehouse_actions.action_type`, а не `aggregate_type`.

### 5.5 `dispatches` — CRUD исходящих рейсов

**Назначение:** управление плановыми рейсами (`OutboundDispatch` — машина+водитель на запланированное время). **Без блокчейна/outbox** — чистый CRUD над `wms_inventory.outbound_dispatches`. Коды рейсов генерируются посуточно под advisory-lock.

**DTO (verbatim).** Request `NewDispatchQuery` (`handler.go`):

| Поле | JSON-тег | Go-тип | Обязательно / валидация |
|------|----------|--------|-------------------------|
| `DestinationID` | `destination_id` | `uuid.UUID` | валидируется в репозитории через `INSERT … SELECT … FROM destinations WHERE destination_id=$1 RETURNING *` (нет совпадения → `pgx.ErrNoRows` → `ErrDestinationNotFound`, 404); не отдельный SELECT и не FK-проверка; zero-UUID не проверяется в хендлере |
| `VehicleNumber` | `vehicle_number` | `string` | непустая |
| `DriverName` | `driver_name` | `string` | непустая |
| `DriverPhone` | `driver_phone,omitempty` | `*string` | опц. |
| `ScheduledAt` | `scheduled_at` | `time.Time` | не более 1 мин в прошлом |

`DispatchFilter` (из query-параметров, не JSON): `Status *domain.OutboundDispatchStatus`, `DestinationID uuid.UUID`, `WarehouseID int`. Response — `domain.OutboundDispatch` (см. § 4.1; обёрнут ключом `{"dispatch": ...}` в POST и GET-by-id).

**Интерфейс `dispatchesRepository`** (`service.go`):

```go
type dispatchesRepository interface {
    WithTx(ctx context.Context, fn func(dispatchesRepository) error) error
    GetActualDispatchCode(ctx context.Context) (int, error)        // под pg_advisory_xact_lock(0x44535043)
    CreateDispatchCode(ctx context.Context) (string, error)        // формат DSP-YYYY-MMDD-NNN
    CreateNewDispatch(ctx context.Context, disp *NewDispatchQuery, dispCode string) (domain.OutboundDispatch, error)
    GetDispatchByID(ctx context.Context, dispID uuid.UUID) (domain.OutboundDispatch, error)
    GetDispatchesByFilter(ctx context.Context, filter DispatchFilter) ([]domain.OutboundDispatch, error)
}
```

Sentinel-ошибки (`errors.go`): `ErrDestinationNotFound`, `ErrDispatchNotFound`. Локальная `ApiError{Code,Message}` — отдельный тип, не `httputil.APIError`.

**Методы Service:** `GetDispatches(ctx, filter)`, `GetDispatchByID(ctx, dispID)`, `CreateNewDispatch(ctx, query)` (транзакция: `CreateDispatchCode` под `pg_advisory_xact_lock(0x44535043)` → insert; `warehouse_id` берётся из `destinations`, не из запроса).

**Эндпоинты** (JWT + `RequireOperator`; **trailing slash обязателен** — нет `StrictSlash`):

| Маршрут | Назначение |
|---------|-----------|
| `GET /dispatches/` | список с опц. фильтрами `status`/`destination_id`/`warehouse_id` |
| `POST /dispatches/` | создание рейса (код `DSP-YYYY-MMDD-NNN`) |
| `GET /dispatches/{dispatch_id}` | по UUID |

> [!WARNING] `dispatches` отличается от остальных модулей:
> - Внутренние ошибки → **HTTP 503** (а не 500).
> - `respondWithError` имеет **инвертированный порядок аргументов** → для 404 в поле `message` попадает строка кода (`"DESTINATION_NOT_FOUND"`), а `code` = `"NOT_FOUND"`.
> - `POST` возвращает **200, не 201**; `data` обёрнут лишним ключом `{"dispatch": ...}` (в отличие от `GET /dispatches/`, где массив лежит прямо в `data`).
> - `GET /dispatches/` при пустом результате может вернуть `"data": null` (nil-слайс), не `[]`.
> - Своя структура `ApiError` (не `httputil.APIError`).

---

### 5.6 `destinations` — справочник магазинов-получателей

**Назначение:** read-only справочник магазинов для UI сборки (выбор `destination_id` под `/assembly/allocate` и `/assembly/tasks`). **Без блокчейна/outbox и без транзакций** — единственный `SELECT` над `wms_inventory.destinations`. Появился потому, что магазины сидятся со случайными UUID, а эндпоинта со списком не было — UI не мог выбрать получателя.

**DTO (verbatim, `handler.go`, неэкспортируемые wire-типы).** Обрезанный вид: без `created_at`/`updated_at`.

| DTO | Поле | JSON-тег | Go-тип |
|-----|------|----------|--------|
| `destinationItem` | `DestinationID` | `destination_id` | `string` |
| | `Code` | `code` | `string` |
| | `Name` | `name` | `string` |
| | `Address` | `address` | `*string` (nullable → JSON `null`, не опускается) |
| | `WarehouseID` | `warehouse_id` | `int64` |
| `destinationListResponse` | `Destinations` | `destinations` | `[]destinationItem` (всегда не-`nil`: `{"destinations": []}`) |

**Интерфейс `destinationsRepository`** (`service.go`, 1 метод):

```go
type destinationsRepository interface {
    GetDestinations(ctx context.Context, warehouseID int64) ([]domain.Destination, error)
}
```

> Репозиторий **read-only**: держит только узкий `dbTX` (`Exec`/`Query`/`QueryRow`), **без** `*pgxpool.Pool` и `WithTx` — транзакций здесь нет (в отличие от пишущих модулей). SQL: `SELECT … FROM wms_inventory.destinations [WHERE warehouse_id=$1] ORDER BY code ASC LIMIT 1000` (`warehouseID <= 0` → без фильтра).

**Эндпоинт:**

| Маршрут | Метод | Гейт | Outbox |
|---------|-------|------|--------|
| `/destinations` | `GET` | `RequireOperator` | — (read-only). Query: `warehouse_id` опц. (`int64`); нечисло или `<= 0` → `400 INVALID_REQUEST` |

> [!NOTE] Хендлер регистрируется и на `""`, и на `"/"` под `PathPrefix("/destinations").Subrouter()` — `gorilla/mux` не редиректит между `/destinations` и `/destinations/`, поэтому работают оба пути. Пустой результат отдаётся как `{"destinations": []}` (никогда `null`).

### 5.7 `signedaction` + `signedops` — подписанное действие как единица записи

**Назначение:** общая машинерия подписанных складских действий (ADR-0001). `signedaction` — чистая библиотека без БД: `Assertion` и `Verify` (WebAuthn P-256: `type`, challenge, origin, `rpIdHash`, флаги UP/UV, нормализация `s` к нижней половине), `Challenge(...)`, `ReceiveDetailsHash`/`PutAwayDetailsHash`/`PickDetailsHash`/`ShipDetailsHash`, `Envelope` (конверт §9), `ConfirmationState` (`SENT`/`APPLIED`/`REJECTED`/`FAILED`).

`signedops` держит операцию (набор частей-действий) и её персистентность:

```go
const MaxItemsPerAction = 2000        // ADR-0001 §10

type Part struct { ActionID uuid.UUID; ItemIDs []uuid.UUID; Salt, DetailsHash, Challenge [32]byte }
type Operation struct { OperationID uuid.UUID; ActionType uint8; CreatedBy uuid.UUID;
    SignerCredentialID []byte; Details json.RawMessage; Parts []Part; Submitted bool }

func NewOperation(actionType uint8, createdBy uuid.UUID, signer *domain.Passkey, details []byte,
    items []uuid.UUID, detailsHashFor func(salt [32]byte, partItems []uuid.UUID) [32]byte) (*Operation, error)
func Envelopes(op *Operation, signer *domain.Passkey, assertions map[uuid.UUID]signedaction.Assertion,
    rpID string, origins []string) ([][]byte, error)
```

`NewOperation` сортирует товары по `itemId` и режет их на части по `MaxItemsPerAction`; у каждой части свой `actionId`, соль и challenge.

| Слой | Что внутри |
|------|-------------|
| `Repository` | `InsertOperation`, `Operation`, `LockOperation` (все части операции `FOR UPDATE` + `created_by`), `LockSigner` (`passkey_credentials FOR SHARE` + запрет на отозванный ключ), `WriteActions` (по строке outbox на часть + `submitted_at`), `OperationStatus` |
| HTTP | `NewDraftView` (`DraftView`: `operation_id`, `action_type`, `signer_key_id`, `rp_id`, `allow_credentials`, `user_verification`, `timeout`, `parts[{action_id, item_ids, details_hash, challenge}]`), `SubmitRequest` (`operation_id` + `assertions[{action_id, credential_id, authenticator_data, client_data_json, signature}]`), `WriteError` (sentinel → HTTP) |

| Маршрут | Метод | Гейт | Назначение |
|---------|-------|------|-----------|
| `/signed-actions/operations/{operationID}` | `GET` | `RequireAdminOrOperator` | Статус частей операции по данным `onchain_events`: `state` + `reject_reason`. До submit → `409 OPERATION_NOT_SUBMITTED`. |

Sentinels (`errors.go`): `ErrOperationNotFound` → 404; `ErrOperationSubmitted`/`ErrOperationNotSubmitted`/`ErrSignerNotActive` → 409; `ErrInvalidAssertion`/`ErrInvalidRequest` → 400.

### 5.8 `passkeys` — passkey и ключевые действия

**Назначение:** регистрация WebAuthn-ключей пользователей и управление ими в реестре `ConsortiumRegistry` подписанными ключевыми действиями (ADR-0001 §1, §7).

| Слой | Что внутри |
|------|-------------|
| `service.go` | `BeginRegistration`/`FinishRegistration` (attestation → `passkey_credentials`, `status=PENDING`), `MyPasskey`, `Organization`, `AdminKeyExport`, `DraftKeyRegister`/`DraftKeyRevoke`, `SubmitKeyAction`, `RejectKey` |
| `status.go` | `syncStatus` — статус `PENDING`/`ACTIVE` ключа обновляется из цепи при чтении (`GetRegistryKey`): активен — `ACTIVE`, иначе `REVOKED`. Ошибки RPC только логируются |
| `actions_repository.go` | `InsertKeyAction`, `KeyAction`, `SubmitKeyAction` (одна транзакция: lock действия и целевого ключа → проверка состояния → INSERT outbox (`aggregate_type=signed_action`) → `submitted_at`), `RejectPendingPasskey`, `LatestConfirmations` |
| `envelope.go` | Конверт ключевого действия (`subjectKey`, без `itemIds` и `detailsHash` — их контракт считает сам) |

Ключевое действие атомарно на одну строку `key_actions` (без частей). `SubmitKeyAction` сериализуется с складскими действиями через блокировку строки ключа (ADR-0001 §7): складское действие берёт её `FOR SHARE`, отзыв — `FOR UPDATE`.

**Эндпоинты** (JWT + гейт):

| Маршрут | Метод | Гейт | Назначение |
|---------|-------|------|-----------|
| `/passkeys/registration/begin` | `POST` | `RequireAdminOrOperator` | Опции WebAuthn-регистрации для текущего пользователя |
| `/passkeys/registration/finish` | `POST` | `RequireAdminOrOperator` | Attestation → `passkey_credentials` в `PENDING`, `201` |
| `/passkeys/me` | `GET` | `RequireAdminOrOperator` | Свой passkey (с синхронизацией статуса) |
| `/passkeys/organization` | `GET` | **`RequireAdmin`** | Ключи сотрудников организации + подтверждения в цепи |
| `/passkeys/organization/admin-key` | `GET` | **`RequireAdmin`** | Публичный ключ админа для `registerAdminKey` |
| `/passkeys/organization/keys/reject` | `POST` | **`RequireAdmin`** | `PENDING`-регистрацию → `REJECTED` (`{target_key_id}`) |
| `/passkeys/actions/key-register/draft` | `POST` | **`RequireAdmin`** | Черновик `KeyRegister` (5) для `PENDING`-ключа оператора своей организации |
| `/passkeys/actions/key-revoke/draft` | `POST` | **`RequireAdmin`** | Черновик `KeyRevoke` (6) для `ACTIVE`-ключа оператора |
| `/passkeys/actions/{actionID}/submit` | `POST` | **`RequireAdmin`** | Подпись админа → outbox + `submitted_at` |

### 5.9 `audit` — сверка БД ↔ цепь

**Назначение:** доказать, что записи БД и действия в цепи сходятся (ADR-0001 §4, §8). Пересчитывает `detailsHash` из доменных таблиц (а не из копии деталей) и сверяет его с цепью; ловит подмену ячейки/рейса и несовпадение состава товаров.

| Слой | Что внутри |
|------|-------------|
| `details_repository.go` | `Action`, `ProductID`, `ActionsForProduct`, `Details` — выборка подписанных действий товара с солью и пересчёт деталей из `wms_ops`/`wms_inventory` |
| `details.go` | Сборка деталей по типу действия (Receive/PutAway/Pick/Ship) |
| `chain.go`, `chain_decode.go`, `abi.go` | RPC-клиент и декодирование `ItemMoved`/`ActionApplied` (встроенные ABI-файлы `SignedMappingWMS.abi.json`, `ConsortiumRegistry.abi.json`) |
| `audit.go`, `audit_compare.go` | Сверка одного действия/товара: `Check` (`OK`/`MISMATCH`/`SKIPPED`), `Status` (`APPLIED`/`REJECTED`/`PENDING`/`MISSING_ON_CHAIN`/`MISSING_IN_DB`), `Finding` (`DETAILS_HASH_MISMATCH`, `ITEMS_MISMATCH`, `SIGNER_MISMATCH`, `BAD_SIGNATURE`, `TYPE_MISMATCH`, …) |
| `http.go` | `GET /audit/products/{key}` (UUID или QR), гейт `RequireAuditorOrAdmin`; отдаёт **только данные БД и соль**, сверку с цепью делает клиент (страница `/audit`) или CLI |

CLI: `wms/cmd/audit` — `audit [--json] product <uuid|qr>`, `action <uuid>`, `all`; окружение `RPC_URL`, `CONTRACT_ADDR`, `REGISTRY_ADDR`, `DB_*`.

---

## 6. Как добавить новый эндпоинт / модуль

### 6.1 Новый эндпоинт в существующем модуле

Берём за образец, например, `putaway`. Слои `handler → service → repository`:

1. **DTO** в `handler.go` (или `types.go`): request/response-структуры с json-тегами. Если есть UUID — держите строкой, парсите в хендлере.
2. **Метод Repository** в `repository.go`: весь SQL. Мутации внутри транзакции — через `WithTx` (он подменит `r.q` на `pgx.Tx`). Outbox-INSERT — только в той же транзакции, что и бизнес-мутация. Добавьте сигнатуру метода в интерфейс `<module>Repository` в `service.go`.
3. **Метод Service** в `service.go`: бизнес-логика, FSM-переходы, при необходимости `CheckChainStatus(...)` **до** транзакции. Открывайте транзакцию через `repo.WithTx(ctx, func(tx <module>Repository) error { ... })`.
4. **Хендлер** в `handler.go`: `decodeJSON` (с `DisallowUnknownFields`) → RBAC-гейт (`httputil.RequireOperator`/`RequireAdminOrOperator`, обязательный `return` при `ok==false`) → парсинг UUID → вызов сервиса → `mapServiceError` → `WriteJSON`/локальный `writeJSON`.
5. **Регистрация** в `(h *Handler) RegisterRoutes(router *mux.Router)`: `router.HandleFunc("/path", h.Method).Methods(http.MethodPost)`. Точный путь (без trailing slash, если не следуете паттерну `dispatches`).
6. **Маппинг ошибок:** добавьте sentinel в `errors.go` модуля и ветку в `mapServiceError` (sentinel → HTTP-статус + `code`). Сообщения — на русском.
7. **Тесты:** unit на сервис с моком `<module>Repository` (паттерн `mock<Module>Repo` в `*_test.go`). См. [testing требования в CONVENTIONS.md](../CONVENTIONS.md).

### 6.2 Новый модуль

1. Создайте `wms/internal/<module>/` с `handler.go`, `service.go`, `repository.go`, `types.go`, `errors.go` (по образцу `putaway`/`shipping`).
2. В `service.go` объявите **неэкспортируемый** `<module>Repository` интерфейс (минимум `WithTx` + нужные методы) и `NewService(repo <module>Repository) *Service`.
3. В `repository.go` реализуйте `*Repository` поверх `*pgxpool.Pool`; `NewRepository(db) *Repository`; `WithTx` по образцу (begin → `fn(&Repository{db:r.db, q:tx})` → commit/rollback).
4. Если этап пишет в блокчейн — это **двухшаговый подписанный путь** (ADR-0001): `draft`-метод сервиса собирает `signedops.Operation` (`NewOperation` + `ops.InsertOperation`) и отдаёт `signedops.NewDraftView`; `submit`-метод проверяет `signedops.Envelopes` и внутри `repo.WithTx` зовёт `LockOperation`/`LockSigner`/`WriteActions` (`aggregate_type = signed_action`, `event_type = wms.action.v1`, 1 строка на часть). Событий на товар/этап больше не пишем. При необходимости — `CheckChainStatus`/`UnconfirmedProducts` гейт на соседний этап.
5. В `cmd/wms/main.go`: соберите `repo → service → handler`, смонтируйте саброутер:
   ```go
   modRouter := r.PathPrefix("/<module>").Subrouter()
   modRouter.Use(auth.Middleware([]byte(cfg.JWTSecret)))
   modHandler.RegisterRoutes(modRouter)
   ```
6. Если добавляете новый доменный статус/модель — кладите в `internal/domain` как typed-enum (предпочтительно) + согласуйте с DB-миграцией ([db/Database_ru_v2.md](../db/Database_ru_v2.md)).
7. Оформите MR по [MR_GUIDE.md](../MR_GUIDE.md) и [CONVENTIONS.md](../CONVENTIONS.md).

---

## 7. Сводка известных нюансов (gotchas) для приёмки команды

- **`http.ListenAndServe` без graceful shutdown** — `main.go` не использует `platform/httpserver`; SIGTERM рвёт in-flight запросы.
- **Сбой Kafka на старте → `return` из `main()`** (не `log.Fatalf`) — процесс молча исчезает, причина только в логах.
- **Auth-ответы — plain-text, бизнес-ответы — JSON-конверт.** Клиенты должны обрабатывать обе формы.
- **`CloseCargoplaceWithOutbox` нельзя оборачивать в `WithTx`** (своя `db.Begin`).
- **INSERT в `public.outbox_events` есть только в двух файлах:** `signedops/repository.go` и `passkeys/actions_repository.go` — оба пишут `aggregate_type='signed_action'`, `event_type='wms.action.v1'`, 1 строка на часть-действие.
- **Гейты цепочки асимметричны:** putaway блокирует `FAILED` приёмки, pick — `FAILED` раскладки, ship — отсутствие `COMMITTED` сборки (`PICKING_NOT_CONFIRMED`); на приёмке гейта нет (товар только создаётся).
- **`dispatches` — белая ворона** по формату ошибок (503, инвертированные аргументы, 200 вместо 201, обязательный trailing slash, `data:null`).
- **`User.PasswordHash` без `json:"-"`** — не маршалить `domain.User` напрямую в ответ.
- **Роль `CUSTOMER` без эндпоинтов** — всегда 403 на складе.
- **Источник истины по API** — `api/openapi.yaml`; источник истины по маршрутам — `RegisterRoutes` в `wms/internal/<module>/handler.go`.
