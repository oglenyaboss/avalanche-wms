# Frontend

React + TypeScript + Vite frontend for the WMS application.

## Local run

1. Start the backend from the repository root:
   ```bash
   make up
   ```
2. Install dependencies in `frontend/`:
   ```bash
   npm install
   ```
3. Start the dev server:
   ```bash
   npm run dev
   ```

The default API base URL is `VITE_API_BASE_URL=/api`. Vite proxies `/api/*`
requests to `http://localhost:8081` and strips the `/api` prefix, so
`/api/auth/login` becomes `/auth/login` on the WMS backend without extra CORS
setup.

For a local environment created by `make up`, `deploy/db-init.sh` seeds the
default admin credentials `admin/admin`.

## Scripts

```bash
npm run dev
npm run build
npm run lint
npm run preview
```

## Structure

- `src/app` - app bootstrap, providers, router
- `src/pages` - route-level pages
- `src/widgets` - page sections and layout
- `src/features` - user scenarios such as auth
- `src/entities` - domain state and contracts
- `src/shared` - UI primitives, config, API client, utilities

## Package manager

This project uses **npm** and `package-lock.json`.
