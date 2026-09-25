# repo-guardian UI

The business UI (DESIGN-0027 § The UI): a Bun process that is both a
backend-for-frontend (Hono) and the static server for a React SPA.
Tokens stay on the server; the browser holds only an encrypted session
cookie.

```text
ui/
  server/   Hono BFF: config, sessions, OIDC, /api proxy, static serving
  web/      React + Vite SPA; web/src/api/schema.gen.ts is generated
  scripts/  check-api.ts, the generated-types drift check
```

## Commands

| Make target | Does |
| --- | --- |
| `make ui-install` | `bun install --frozen-lockfile` |
| `make generate-ui-api` | regenerate `web/src/api/schema.gen.ts` from `api/openapi.yaml` |
| `make lint-ui` | typecheck, and fail if the generated types are stale |
| `make test-ui` | `bun test` |

For local development, run `bun run dev:server` and `bun run dev:web`.
Vite proxies `/api`, `/auth` and `/ui` to the BFF on `:3000`.

TypeScript is pinned to 5.x: `openapi-typescript` needs the compiler
API that TypeScript 7's native compiler does not ship.
