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
| `make docker-build-ui` | build `ghcr.io/donaldgifford/repo-guardian-ui:dev` |

For local development, run `bun run dev:server` and `bun run dev:web`.
Vite proxies `/api`, `/auth` and `/ui` to the BFF on `:3000`.

TypeScript is pinned to 5.x: `openapi-typescript` needs the compiler
API that TypeScript 7's native compiler does not ship.

## Configuration

| Variable | Default | |
| --- | --- | --- |
| `OIDC_ISSUER` | required | https, except a loopback issuer in development |
| `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | required | a confidential client |
| `OIDC_SCOPES` | `openid profile email offline_access` | must include `openid` |
| `API_UPSTREAM` | required | the `api` Service; the only host `/api` ever reaches |
| `PUBLIC_URL` | required | the browser-facing origin, used for the redirect URI |
| `UI_SESSION_KEYS` | required | comma-separated, at least 32 bytes each; the first seals, all open |
| `UI_SESSION_TTL` | `8h` | the absolute session length; refresh never extends it |
| `PORT` | `3000` | |

The process fails at startup, listing every problem, when any of these
is missing or malformed.

## Routes

| Route | |
| --- | --- |
| `/auth/login`, `/auth/callback`, `POST /auth/logout` | OIDC code flow with PKCE; logout revokes and ends the issuer session |
| `/api/*` | GET and HEAD only, to `API_UPSTREAM`. A caller's own `Authorization: Bearer` passes through; otherwise the session's token, refreshed within 60s of expiry. `/api/v1/status` and `/api/v1/openapi.yaml` go without a token |
| `/healthz` | the process is up |
| `/readyz` | issuer discovery has loaded and the upstream's `/healthz` answers (cached 10s) |
| `/assets/*` | hashed build output, cached for a year |
| anything else | the SPA shell (`no-cache`); a login redirect without a session, except `/status` |

Every response carries `Content-Security-Policy: default-src 'self';
connect-src 'self'; frame-ancestors 'none'; script-src 'self'`,
`X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`.
