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
| `make lint-ui` | typecheck, lint, and fail if the generated types are stale or the build inlines a data: URL or a Google Fonts URL (`check:dist`) |
| `make test-ui` | `bun test` |
| `make docker-build-ui` | build `ghcr.io/donaldgifford/repo-guardian-ui:dev` |

For local development, run `bun run dev:server` and `bun run dev:web`.
Vite proxies `/api`, `/auth` and `/ui` to the BFF on `:3000`.

The Playwright suite is `make test-ui-e2e` (needs Docker and Go). The
axe audit (`e2e/a11y.spec.ts`) runs in it. The screenshot spec
(`e2e/visual.spec.ts`) only runs through `make test-ui-e2e-visual`,
which renders in the pinned `mcr.microsoft.com/playwright` image;
`make test-ui-e2e-update-snapshots` regenerates
`e2e/__screenshots__/`.

## Design

- **The mockup is the reference.** `design/repo-guardian-mockup.html`
  (DESIGN-0036) is the source of every panel, label and breakpoint.
  Change it in the same PR as the UI, so it never drifts.
- **Tokens.** One dark theme, declared once in `web/src/index.css`
  `@theme`: surfaces, `foreground`/`muted-foreground`, `accent`, and the
  status tones `ok`, `warn`, `bad`, `info`. Components read tokens and
  carry no colour of their own. Badges tint their tone at 18% and pull
  the text 65% toward white, which keeps every tone at WCAG AA.
- **Primitives.** `web/src/components/ui/` holds shadcn-style
  primitives on `radix-ui`. They know nothing of findings or rules;
  domain components (`badges`, `timeline`, `filters`, `charts`) build
  on them, and views compose those.
- **Fonts** are bundled from `@fontsource` (IBM Plex Sans, JetBrains
  Mono), never fetched from a CDN, because the CSP allows only
  `'self'`. `check:dist` fails the build when Vite inlines a `data:`
  URL or references Google Fonts; `vite.config.ts` sets
  `assetsInlineLimit: 0` for that reason.
- **`UI_DOCS_URL`** (chart `ui.docsUrl`) adds the sidebar Docs group.
  The BFF serves it to the SPA as `docs_url` on `/ui/config`.
- **Component tests** run under happy-dom: import `web/testing/dom.ts`
  first, before anything that loads radix.

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
| `GITHUB_HOST` | `github.com` | where PR and repository links point |
| `UI_DOCS_URL` | unset | https URL of the operator's documentation; sets the sidebar Docs group |
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
`X-Content-Type-Options: nosniff` and `Referrer-Policy: same-origin`.
