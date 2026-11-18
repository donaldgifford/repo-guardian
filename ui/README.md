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
| `make ui-install` | `bun install --frozen-lockfile`, with the registry token (see below) |
| `make generate-ui-api` | regenerate `web/src/api/schema.gen.ts` from `api/openapi.yaml` |
| `make lint-ui` | typecheck and ESLint (the markup and radix bans), and fail if the generated types or changelog are stale (`check:api`, `check:changelog`), the newest changelog entry is not the chart `appVersion`, `web/src` names a removed colour token (`check:tokens`), or the build inlines a data: URL or a Google Fonts URL (`check:dist`) |
| `make test-ui` | `bun test` over `server`, `web` and `scripts` |
| `make test-ui-e2e` | the Playwright suite, both projects, with the axe audit (needs Docker and Go) |
| `make test-ui-e2e-visual` | compare the screenshots in the pinned Playwright image |
| `make test-ui-e2e-update-snapshots` | regenerate `e2e/__screenshots__/` in the pinned image |
| `make docker-build-ui` | build `ghcr.io/donaldgifford/repo-guardian-ui:dev`, with the registry token as a build secret |

`CHANGELOG.yaml` is the status page's Changelog card: newest first,
tags from `new | improved | fixed | breaking` plus the areas
`fleet | findings | policy | ui | api | chart`. `bun run gen:changelog`
writes `web/src/generated/changelog.json` (committed) and
`bun run check:changelog` fails on drift, a tag outside the set, dates
that ascend, a repeated version, or a newest entry that is not the chart
`appVersion` — so every chart bump adds an entry.

For local development, run `bun run dev:server` and `bun run dev:web`.
Vite proxies `/api`, `/auth` and `/ui` to the BFF on `:3000`.

The Playwright suite is `make test-ui-e2e` (needs Docker and Go). The
axe audit (`e2e/a11y.spec.ts`) runs in it. `e2e/stack.ts` seeds two
databases in one Postgres container: `e2e/seed.sql` (both Apps, acme's
remediations) behind the api role and BFF on `:3100`, and the same seed
with `e2e/seed-no-remediate.sql` applied (no Remediation App) behind a
second pair on `:3101`, which the `no-remediation` Playwright project
walks to assert every gated panel reads "not enabled". The screenshot
spec (`e2e/visual.spec.ts`: status, Findings, Orgs, Remediations) only
runs through `make test-ui-e2e-visual`, which renders in the pinned
`mcr.microsoft.com/playwright` image; `make test-ui-e2e-update-snapshots`
regenerates `e2e/__screenshots__/`.

## Registry token

`@donaldgifford/design-system` is published to GitHub Packages
(`npm.pkg.github.com`), which asks for a token even to read a public
package. `ui/.npmrc` maps the `@donaldgifford` scope there and holds only
the reference `${NPM_TOKEN}`; the value is never stored in the
repository or an image.

| Where | Where the token comes from |
| --- | --- |
| Your machine | `NPM_TOKEN` if set, else `gh auth token` (the Make targets do this). The token needs `read:packages`: `gh auth refresh -s read:packages` adds it. |
| CI (`ci.yml`) | `secrets.GITHUB_TOKEN`, with `packages: read` on the `ui` and `ui-e2e` jobs |
| Image build | a build secret: `docker-bake.hcl` mounts `NPM_TOKEN` from the caller's environment into the build stage's `bun install` only. The package is a devDependency, so the production stage installs without it. `ghcr.yml` passes `secrets.GITHUB_TOKEN`. |
| Dependabot | the `github-packages-npm` registry in `.github/dependabot.yml`, reading the Dependabot secret `GH_PACKAGES_READ_TOKEN` |

Calling `bun install` directly, outside Make, needs
`NPM_TOKEN=$(gh auth token) bun install`. A cached install that produced
a `dist/` directory and no `src/` under
`node_modules/@donaldgifford/design-system` is stale: delete the cache
entry and install again.

## Design

- **The design system is the component library.** Every component,
  block and the shell (sidebar, top bar, drawers, palette) come from
  `@donaldgifford/design-system` (DESIGN-0039), built on Base UI; its
  [Storybook](https://donaldgifford.github.io/design-system/) is the
  catalog. A component that is generic belongs there: open an issue or
  pull request on the package rather than patching around it here.
  What stays in `web/src/components/` knows this app (rules, findings,
  posture, the router, the API client) and says why in a
  `// Local, not in the design system:` comment.
- **The mockup is the reference for pages.** `design/repo-guardian-mockup.html`
  (DESIGN-0036, frame and tokens updated by DESIGN-0039) shows how the
  package's parts compose into each page. Change it in the same PR as
  the UI, so it never drifts.
- **Tokens** are the package's: `web/src/index.css` is one import of
  its `globals.css` and declares nothing of its own. Use `card`, `muted`, `success`,
  `warning`, `destructive`, `info`; `check:tokens` fails on the old
  `panel`, `panel-2`, `ok`, `warn`, `bad`, which Tailwind would
  otherwise drop silently. Badges are the package's `Badge` with a
  `variant` chosen by meaning (`toneFor` in `lib/format.ts`).
- **Breakpoints.** The sidebar becomes a sheet below 768px (Tailwind
  `md`). Collapsed, it is an icon rail kept in `localStorage` under
  `rg.sidebar`; `⌘B` toggles it and `⌘K` opens the search palette.
- **Fonts** (IBM Plex Sans, JetBrains Mono) arrive bundled through the
  package's `@fontsource` dependencies, never fetched from a CDN,
  because the CSP allows only `'self'`. `check:dist` fails the build
  when Vite inlines a `data:` URL or references Google Fonts;
  `vite.config.ts` sets `assetsInlineLimit: 0` for that reason.
- **No radix.** ESLint bans `radix-ui` and `@radix-ui/*` under `web/`,
  so the package's overlays are the only ones.
- **`UI_DOCS_URL`** (chart `ui.docsUrl`) adds the sidebar Docs group.
  The BFF serves it to the SPA as `docs_url` on `/ui/config`.
- **Component tests** run under happy-dom: import `web/testing/dom.ts`
  first, before anything that loads an overlay. It and
  `web/testing/preload.ts` load the package's Base UI and cmdk modules
  under a document, because both decide at module load whether one
  exists.

TypeScript is pinned to 5.x: `openapi-typescript` needs the compiler
API that TypeScript 7's native compiler does not ship.

## Configuration

| Variable | Default | |
| --- | --- | --- |
| `OIDC_ISSUER` | required | https, except a loopback issuer in development |
| `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | required | a confidential client |
| `OIDC_SCOPES` | `openid profile email offline_access` | must include `openid` |
| `OIDC_TOKEN_AUTH_METHOD` | `client_secret_post` | or `client_secret_basic`: how the client secret reaches the token and revocation endpoints |
| `OIDC_AUTHORIZATION_PARAMS` | unset | a JSON object of string values added to the authorize request, e.g. `{"audience":"api://repo-guardian"}`; may not set `redirect_uri`, `scope`, `state`, `nonce`, `code_challenge`, `code_challenge_method`, `response_type` or `client_id` |
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
