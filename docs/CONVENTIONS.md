# Конвенции проекта

Единые правила для всей команды. Хуки проверяют автоматически — коммит/пуш не пройдёт если не по формату.

## Содержание

- [Git: именование веток](#git-именование-веток)
- [Git: сообщения коммитов](#git-сообщения-коммитов)
- [Go: стиль кода](#go-стиль-кода)
- [Go: структура модуля](#go-структура-модуля)
- [SQL: миграции](#sql-миграции)
- [Docker](#docker)

---

## Git: именование веток

**Формат:** `<тип>/<описание-через-дефис>`

| Тип | Когда использовать | Пример |
|-----|-------------------|--------|
| `feature/` | Новая функциональность | `feature/receiving-endpoint` |
| `fix/` | Исправление бага | `fix/kafka-connection-timeout` |
| `hotfix/` | Критический фикс в прод | `hotfix/db-migration-rollback` |
| `docs/` | Только документация | `docs/api-reference` |
| `refactor/` | Рефакторинг без изменения поведения | `refactor/extract-ledger-client` |
| `chore/` | Конфиги, CI, зависимости | `chore/update-golangci-lint` |
| `test/` | Только тесты | `test/receiving-service-unit` |

**Правила:**
- Только строчные латинские буквы, цифры, дефис, точка, подчёркивание
- Без пробелов, без кириллицы в имени ветки
- `main` и `develop` — защищённые ветки, прямой пуш запрещён

**Неправильно:**
```
receivingEndpoint          # нет типа
feature/Receiving_Endpoint # заглавные буквы
my-branch                  # нет типа
```

---

## Git: сообщения коммитов

Используем [Conventional Commits](https://www.conventionalcommits.org/ru/v1.0.0/).

**Формат:** `<тип>(<скоуп>): <описание>`

### Типы

| Тип | Значение | Пример |
|-----|----------|--------|
| `feat` | Новая функциональность | `feat(wms): добавить эндпоинт приёмки товаров` |
| `fix` | Исправление бага | `fix(ledger-adapter): исправить таймаут подключения к ноде` |
| `docs` | Документация | `docs: обновить README` |
| `style` | Форматирование (без изменения логики) | `style(wms): отформатировать handler.go` |
| `refactor` | Рефакторинг (без изменения поведения) | `refactor(wms): вынести валидацию в отдельный метод` |
| `test` | Тесты | `test(wms): добавить unit-тесты для receiving service` |
| `chore` | Рутина: зависимости, конфиги | `chore: обновить go-ethereum до v1.16.8` |
| `ci` | CI/CD конфиги | `ci: добавить stage деплоя` |
| `perf` | Улучшение производительности | `perf(wms): кешировать запросы к ledger-adapter` |
| `build` | Система сборки, Docker | `build: обновить базовый образ до golang:1.24` |
| `revert` | Откат предыдущего коммита | `revert: откатить feat(wms): добавить кеш` |

### Скоупы (опционально, но рекомендуется)

| Скоуп | Что затрагивает |
|-------|----------------|
| `wms` | WMS-сервис |
| `ledger-adapter` | Ledger Adapter |
| `deploy` | Docker, docker-compose, инфра |
| `ci` | GitLab CI пайплайны |
| `db` | Миграции, схема БД |

### Правила

1. Описание начинается со строчной буквы
2. Без точки в конце
3. Минимум 3 символа в описании
4. Описание отвечает на вопрос "что делает этот коммит?"
5. Пишем на русском или английском — но единообразно в рамках одного MR
6. Один коммит = одно логическое изменение

### Breaking Changes

Если коммит ломает обратную совместимость, добавьте `!` после скоупа:

```
feat(wms)!: изменить формат ответа /health
```

### Примеры хороших и плохих коммитов

```bash
# ✅ Хорошо
feat(wms): добавить handler для приёмки товаров
fix(ledger-adapter): обработать ошибку при недоступности ноды
docs: описать API эндпоинты в README
test(wms): покрыть receiving service unit-тестами
refactor(wms): вынести подключение к БД в platform/postgres

# ❌ Плохо
fix: fix                     # неинформативно
обновил код                  # нет типа, нет контекста
FEAT: НОВАЯ ФИЧА             # заглавные буквы, нет скоупа
feat(wms): .                 # описание < 3 символов
```

> **Ссылка:** [Conventional Commits спецификация](https://www.conventionalcommits.org/ru/v1.0.0/)

---

## Go: стиль кода

### Автоматически проверяется

- **gofmt** — форматирование (пробелы, отступы)
- **goimports** — сортировка импортов
- **golangci-lint** — статический анализ (~15 линтеров, конфиг в `.golangci.yml`)

### Что проверять на ревью

1. **Именование:**
   - Переменные и функции — `camelCase`
   - Экспортируемые — `PascalCase`
   - Акронимы целиком: `userID`, не `userId`; `httpClient`, не `HTTPClient` (кроме начала: `HTTPClient` ок)

2. **Ошибки:**
   - Всегда обрабатывать: `if err != nil { return ..., fmt.Errorf("context: %w", err) }`
   - Никогда `_ = someFunc()` если функция возвращает error
   - Оборачивать с контекстом: `fmt.Errorf("create order: %w", err)`, не просто `return err`

3. **Структура файлов:**
   - Один тип + его методы = один файл (обычно)
   - Не больше 300 строк на файл — сигнал к разделению

4. **Зависимости:**
   - `internal/` пакеты не импортируют друг друга горизонтально
   - Зависимости идут сверху вниз: handler → service → repository

> **Ссылки:**
> - [Effective Go](https://go.dev/doc/effective_go)
> - [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md)
> - [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)

---

## Go: структура модуля

Каждый бизнес-модуль (receiving, assembly, putaway, shipping) следует одному шаблону:

```
internal/<module>/
├── handler.go      # HTTP: парсинг запроса → вызов service → ответ
├── service.go      # Бизнес-логика: валидация, оркестрация
└── repository.go   # SQL-запросы через pgxpool
```

### handler.go

```go
func (h *Handler) CreateShipment(w http.ResponseWriter, r *http.Request) {
    // 1. Парсинг запроса
    var req CreateShipmentRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "invalid request body", http.StatusBadRequest)
        return
    }

    // 2. Вызов service
    shipment, err := h.svc.Create(r.Context(), req)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    // 3. Ответ
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(shipment)
}
```

### service.go

```go
func (s *Service) Create(ctx context.Context, req CreateShipmentRequest) (*domain.Shipment, error) {
    // 1. Валидация
    if req.OrderID == 0 {
        return nil, fmt.Errorf("order_id is required")
    }

    // 2. Бизнес-логика
    shipment := &domain.Shipment{
        OrderID:   req.OrderID,
        Status:    "created",
        CreatedAt: time.Now(),
    }

    // 3. Сохранение
    if err := s.repo.Save(ctx, shipment); err != nil {
        return nil, fmt.Errorf("save shipment: %w", err)
    }

    return shipment, nil
}
```

### repository.go

```go
func (r *Repository) Save(ctx context.Context, s *domain.Shipment) error {
    _, err := r.db.Exec(ctx,
        `INSERT INTO wms_ops.shippings (order_id, status, created_at)
         VALUES ($1, $2, $3)`,
        s.OrderID, s.Status, s.CreatedAt,
    )
    if err != nil {
        return fmt.Errorf("insert shipping: %w", err)
    }
    return nil
}
```

---

## SQL: миграции

Миграции лежат в `wms/migrations/`. Формат имени:

```
NNNN_<описание>_up.sql    # применение
NNNN_<описание>_down.sql  # откат
```

**Правила:**
- Каждая миграция имеет пару up/down
- Down-миграция должна полностью откатывать up
- Не изменять уже применённые миграции — только новые
- Тестировать down-миграцию перед коммитом

---

## Docker

- `Dockerfile` (с заглавной `D`) — стандартное имя
- Multi-stage build: builder + runtime
- Runtime образ минимальный (alpine)
- Не хранить секреты в Dockerfile
- `.env` файлы — через `docker-compose.yaml` environment с фоллбеками

> **Ссылка:** [Dockerfile best practices](https://docs.docker.com/build/building/best-practices/)
