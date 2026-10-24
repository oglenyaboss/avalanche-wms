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

Разметка: от 1024 px меню слева, на планшете — за кнопкой «Открыть меню».

## Новый экран

1. Страница в `src/app/(terminal)/<путь>/page.tsx`.
2. Пункт в `src/components/app-shell/navigation.ts` с ролями, которые пускает бэкенд (`docs/api/api-contract.md`, раздел RBAC).
