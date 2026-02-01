# WMS_Blockchain DB (PostgreSQL 17) — SQL-миграции

Этот каталог содержит версионированные SQL-миграции для **PostgreSQL 17**, используемые для создания структуры базы данных `wms_blockchain_db` с нуля (схемы `wms_inventory`, `wms_ops`, а также таблицы, связанные с событиями/пользователями).

---

## 0) Предварительные требования

- Установлен Docker и доступна команда `docker compose`

## 1) Запуск PostgreSQL 17

Выполните в текущем каталоге:

```bash
docker compose up -d
```

Проверка версии:

```bash
docker exec -it postgres_db psql -U root -d wms_blockchain_db -c "SELECT version();"
```


## 2) Применение миграций

Параметры подключения по умолчанию соответствуют `docker-compose.yml`:

- `DB_HOST` (по умолчанию `localhost`)
- `DB_PORT` (по умолчанию `5432`)
- `DB_USER` (по умолчанию `root`)
- `DB_PASSWORD` (по умолчанию `root`)
- `DB_NAME` (по умолчанию `wms_blockchain_db`)
- `DB_CONTAINER` (по умолчанию `postgres_db`)

Выполните:

```bash
chmod +x scripts/migrate.sh scripts/rollback.sh
./scripts/migrate.sh
```


Проверка примененных версий:

```bash
docker exec -it postgres_db psql -U root -d wms_blockchain_db -c "SELECT * FROM public.schema_migrations ORDER BY version;"
```

---

## 3) Как устроена "версификация" миграций

- Каталог миграций: `migrations/`
- Соглашение об именовании файлов:
  - `0001_xxx.up.sql`: миграция вперед (создание/изменение структуры)
  - `0001_xxx.down.sql`: откат миграции
- Скрипт выполняет файлы `*.up.sql` в порядке сортировки по именам
- `public.schema_migrations` — таблица учета миграций:
  - `migrate.sh` использует ее, чтобы определить, применена ли версия, и избежать повторного выполнения
  - В конце каждого `*.up.sql` нужно добавить: `INSERT INTO public.schema_migrations(version) VALUES (<version>);`

## 4) Стратегия rollback

`scripts/rollback.sh` выполняет **разрушающий откат** (выполняет соответствующий `*.down.sql`, удаляет созданные в проекте схемы/таблицы/типы).

```bash
./scripts/rollback.sh            # откатить последнюю миграцию (по умолчанию)
./scripts/rollback.sh --steps 2  # откатить последние 2 миграции
./scripts/rollback.sh --to 1     # откатить до версии 1 (оставить 1)
./scripts/rollback.sh --all      # откатить все (до 0)
```

---

## 5) Быстрая самопроверка

Проверка "нескольких миграций + откат" в чистой среде:

```bash
docker compose up -d
chmod +x scripts/migrate.sh scripts/rollback.sh

./scripts/rollback.sh --all
./scripts/migrate.sh

docker exec -it postgres_db psql -U root -d wms_blockchain_db -c "SELECT * FROM public.schema_migrations ORDER BY version;"
```

Затем проверить откат:

```bash
./scripts/rollback.sh --steps 2
./scripts/migrate.sh
./scripts/rollback.sh --to 1
./scripts/rollback.sh --all
```

---
