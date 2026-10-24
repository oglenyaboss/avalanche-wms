# Терминал склада (frontend)

Next.js (App Router) + TypeScript + Tailwind + shadcn/ui. Приложение работает только в браузере: сервер Next отдаёт оболочку и проксирует `/api/*` в бэкенд.

## Запуск

```bash
npm install
npm run dev          # http://localhost:3000, API на моках
```

Бэкенд не нужен: запросы к `/api/*` перехватывают моки (MSW, папка `mocks/`). Пользователи моков — логин и пароль совпадают:

| Логин | Роль |
|-------|------|
| `operator` | OPERATOR |
| `admin` | ADMIN |
| `auditor` | AUDITOR |
| `customer` | CUSTOMER |

С настоящим бэкендом: создать `.env.local` по образцу `.env.example` с `NEXT_PUBLIC_API_MOCKS=off` и адресом WMS в `WMS_API_URL`.

## Команды

| Команда | Что делает |
|---------|-----------|
| `npm run dev` | dev-сервер |
| `npm run build` | production-сборка |
| `npm run lint` | ESLint |
| `npm test` | unit-тесты (Vitest) |
| `npm run test:e2e` | e2e (Playwright, десктоп и планшет; сам собирает и поднимает приложение на порту 3100) |

## Где что лежит

| Путь | Что |
|------|-----|
| `src/app/(terminal)/` | экраны терминала; layout пускает только с сессией |
| `src/app/login/` | вход |
| `src/components/app-shell/navigation.ts` | разделы меню и роли, которым они доступны; по этому списку проверяется доступ к экрану |
| `src/lib/auth/` | сессия (`localStorage`, ключ `wms-auth`), вход, `authFetch` |
| `src/components/ui/` | компоненты shadcn/ui |
| `mocks/` | моки API |
| `e2e/` | Playwright-тесты |

`authFetch` добавляет `Authorization: Bearer …`, на `401` один раз обновляет токен через `/auth/refresh` и повторяет запрос. Если обновить не удалось, сессия сбрасывается и терминал уходит на вход.

## Сканер

Экран подключает поле скана — `ScanField` из `src/components/scan/scan-field.tsx`: передаёт `label` и `onScan(value, source)`. Поле само регистрируется в реестре (`src/lib/scan/scan-registry.ts`) и становится активным при монтировании и при фокусе; реестр на приложение один, живёт в `ScanProvider` (`src/lib/scan/scan-context.tsx`).

Код попадает в активное поле одним путём из трёх источников: сканер-клавиатура (Enter в поле — `keyboard`), эмулятор сканера (`emulator`) и камера (`camera`). После скана поле очищается и держит фокус — следующий скан тоже попадёт в него; пока `onScan` не завершился, поле помечено `aria-busy` и игнорирует новые сканы.

Эмулятор — кнопка в шапке, появляется только с моками (`NEXT_PUBLIC_API_MOCKS=off` — его нет). Панель показывает активное поле и умеет сканировать в него код из каталога (`mocks/scan-catalog.ts`, временный) или введённый вручную. Проверить сканер можно на экране `/scanner-check` — ссылка внизу панели эмулятора, экран доступен всем ролям и не входит в меню.

## API

Типы генерируются из `docs/api/openapi.yaml` командой `npm run gen:api` в `src/lib/api/schema.d.ts`. Руками этот файл не правят и wire-типы не пишут: нужный тип берётся как `Schema<"PutawayScanBufferResponse">`.

```ts
import { api, unwrap } from "@/lib/api/client";
import { ApiError } from "@/lib/api/errors";

try {
  const buffer = await unwrap(api.POST("/putaway/scan-buffer", { body: { buffer_bin_id } }));
} catch (error) {
  if (error instanceof ApiError && error.code === "BUFFER_BIN_NOT_FOUND") { /* … */ }
}
```

`unwrap` снимает конверт `{success, data, error}` и отдаёт `data`. Ответы без конверта (`/auth/*`, `/health`) отдаёт как есть. Любая ошибка приходит как `ApiError { status, code, message }`:

| Ответ | `code` | `message` |
|-------|--------|-----------|
| JSON-конверт ошибки | `error.code` | `error.message` с бэкенда |
| `unauthorized` (текст) | `UNAUTHORIZED` | «Сессия истекла, войдите заново» |
| `forbidden` (текст) | `FORBIDDEN` | «Недостаточно прав для этого действия» |
| `invalid request body` (текст) | `INVALID_REQUEST` | «Некорректный запрос» |
| `user already exists` (текст) | `USER_ALREADY_EXISTS` | «Пользователь уже существует» |
| `internal server error` (текст) | `INTERNAL_ERROR` | «Внутренняя ошибка сервера» |
| другое тело | `HTTP_<статус>` | «Сервер ответил ошибкой <статус>» |
| нет ответа | `NETWORK_ERROR`, `status: 0` | «Нет связи с сервером» |

`api` ходит через `authFetch`, `publicApi` — без токена (вход).

Разметка: от 1024 px меню слева, на планшете — за кнопкой «Открыть меню».

## Новый экран

1. Страница в `src/app/(terminal)/<путь>/page.tsx`.
2. Пункт в `src/components/app-shell/navigation.ts` с ролями, которые пускает бэкенд (`docs/api/api-contract.md`, раздел RBAC).
