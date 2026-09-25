---
id: IMPL-0027
title: "v2 read-only API, business UI, and status page"
status: Draft
author: Donald Gifford
created: 2026-09-25
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0027: v2 read-only API, business UI, and status page

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-09-25

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Execution order](#execution-order)
- [Pre-implementation audit (2026-09-25)](#pre-implementation-audit-2026-09-25)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Contract and the api role skeleton](#phase-1-contract-and-the-api-role-skeleton)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Authentication and authorization](#phase-2-authentication-and-authorization)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Read endpoints](#phase-3-read-endpoints)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: Status page](#phase-4-status-page)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: UI repository and the Bun BFF](#phase-5-ui-repository-and-the-bun-bff)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: UI views](#phase-6-ui-views)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
  - [Phase 7: Chart — api, ui, ingress, read-only role](#phase-7-chart--api-ui-ingress-read-only-role)
    - [Tasks](#tasks-6)
    - [Success Criteria](#success-criteria-6)
  - [Phase 8: End-to-end, docs, homelab](#phase-8-end-to-end-docs-homelab)
    - [Tasks](#tasks-7)
    - [Success Criteria](#success-criteria-7)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Build DESIGN-0027:

- the `repo-guardian api` role: a read-only, OIDC-authenticated,
  org-scoped JSON API over the DESIGN-0025 tables, generated from a
  hand-written OpenAPI 3.1 contract;
- a public status page endpoint;
- the business UI in a new `repo-guardian-ui` repository: a Bun BFF
  (Hono, `openid-client`, an encrypted cookie session with no
  datastore) serving a React front end;
- deployment of both from the repo-guardian chart behind one ingress
  host.

The UI replaces the business dashboards (E1/E2) that DESIGN-0026
deletes from Grafana.

**Implements:** DESIGN-0027 (from INV-0009, INV-0018)

## Scope

### In Scope

- `api/openapi.yaml`, oapi-codegen generation into `internal/api/gen`,
  and the `generate-api` / `lint-api` drift gate.
- `internal/api`: server, middleware chain, handlers, status cache,
  authn (go-oidc), authz (group→org), and the read-only pool.
- `store.Reader` API methods as sqlc queries (`api_*.sql`), sharing the
  compliance query with `report` (IMPL-0025 Phase 7).
- The `repo-guardian-ui` repository: BFF, views, tests, image build and
  signed publish, and a Renovate hook back into this chart.
- Chart `api.*` / `ui.*` values, templates, guards, the single-host
  Ingress, the read-only role per Postgres mode, and the authz ConfigMap.
- Docs (`docs/usage/api.md`, `docs/usage/ui.md`, a Keycloak homelab
  walkthrough) and the mkdocs OpenAPI rendering.

### Out of Scope

- Any write endpoint or action from the UI. The API is read-only by
  design.
- Notifications and alerting on findings (a later design).
- The findings schema (IMPL-0025) and the control plane (IMPL-0026).
  `api` only reads what they write.
- Multi-tenant or per-org UI theming.

## Execution order

- Phases 1–4 need IMPL-0025 Phase 2 (tables and `Reader` exist) and
  IMPL-0026 Phase 1 (`v2` branch). They can run in parallel with
  IMPL-0026 Phases 2–6.
- Phase 3's compliance-math parity needs IMPL-0025 Phase 7 (report on
  findings).
- Phases 5–6 live in the new repository and need only the spec from
  Phase 1, with seeded API responses for development.
- Phase 7 needs IMPL-0026 Phase 8 (chart 2.0.0 topology).
- Whether the UI ships in `v2.0.0-rc.1` is IMPL-0026 OQ10.

## Pre-implementation audit (2026-09-25)

Code at `main` @ `5d9c440`:

- **None of the Go dependencies are present**: no `oapi-codegen`,
  `coreos/go-oidc`, `kin-openapi` or `go-jose` in go.mod. The only
  mention of oapi-codegen is a URL comment in `security.yml:33`.
- **The HTTP plumbing to reuse:**
  - `observability.Handler` (otelhttp, route-labelled);
  - uninstrumented `handleHealthz` (`main.go:698`) and `handleReadyz`
    (`:706`, which checks nothing today; IMPL-0026 5.3 adds real
    checks, and `api` reuses them);
  - `startServer` (`:549`), `newMetricsServer` (`:538`);
  - `ReadHeaderTimeout: 10s` on the main server.
- **The report path is the compliance-math reference:**
  - `internal/report/model.go:86-94` `CompliantPercent` returns
    `(0, false)` on an empty denominator and floors to one decimal;
  - IMPL-0025 Phase 7 moves it onto findings and a shared sqlc query;
  - `/rules`, `/orgs` and the snapshot writer must call that query,
    not re-derive it.
- **The live PR linker** (`cmd/repo-guardian/report.go:122`
  `newPRLinker`) needs a GitHub client. The API has none, so PR links
  come from finding evidence (IMPL-0025 3.2 `PREvidence`).
- **There is no `docs/usage/api.md`.** `docs/usage/` holds only
  `getting-started.md` and `policy-reference.md`.
- **Chart:**
  - no Ingress template exists today (removed by IMPL-0024; ingress is
    operator-owned);
  - DESIGN-0027 reintroduces an **optional** chart Ingress for the UI
    host only. `docs/operations/ingress.md` must say this is the one
    chart-rendered ingress and that the webhook path is still
    operator-owned;
  - `store-postgres.yaml` (baked) has no init-SQL mechanism for extra
    roles;
  - `store-cnpg-cluster.yaml` has no `spec.managed.roles`;
  - both are added in Phase 7.
- **`repo-guardian-ui` does not exist** on GitHub (verified).
- **Existing baked installs** initialized their Postgres volume long
  ago, so first-boot init SQL will never run for them. The read-only
  role needs another path for those (OQ6).

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all
its tasks are checked off and its success criteria are met. Go work
lands on `v2` in this repository; Phases 5–6 land in
`repo-guardian-ui`.

---

### Phase 1: Contract and the api role skeleton

#### Tasks

- [ ] 1.1 `api/openapi.yaml`, OpenAPI 3.1:
  - `info.version` tracks the binary version;
  - components: `Problem` (RFC 9457), `Cursor` (opaque string), and
    enums for status, reason, remediation, park reason and component
    state, each documented as open;
  - paths `/api/v1/me`, `/api/v1/summary`, `/api/v1/status` and
    `/api/v1/openapi.yaml`;
  - security scheme `bearerAuth` (JWT);
  - `/status` and `/openapi.yaml` are public (`security: []`).

  Use only constructs oapi-codegen v2.8 documents for 3.1: `type:
  [T, "null"]`, `oneOf` enums.
- [ ] 1.2 Pin oapi-codegen ≥ v2.8.0 as a Go tool dependency
  (`go get -tool`). Add `api/oapi-codegen.yaml` with `std-http-server`,
  `strict-server`, `models` and `embedded-spec` (served at
  `/openapi.yaml`), output to `internal/api/gen/api.gen.go`. If a
  construct fails, add `api/overlay.yaml` (DESIGN-0027 § Contract)
  rather than editing the spec.
- [ ] 1.3 Make targets:
  - `generate-api`;
  - `lint-api`: regenerate into `build/api`, `diff -r`, and a
    `vacuum`/`redocly`-style lint of the spec (OQ9).

  CI: an `api` paths-filter output (`api/**`, `internal/api/**`) gating
  a `lint-api` job.
- [ ] 1.4 `internal/api/server.go`:
  - `New(cfg, reader, statusSource, verifier, authz, logger)` returns
    an `http.Handler` built on `ServeMux` and the generated
    `HandlerWithOptions`;
  - middleware outermost first: `observability.Handler`, request ID
    plus access log, recover, authn, authz, handler;
  - generated strict handlers receive a typed context carrying the
    `Principal`.
- [ ] 1.5 Read-only pool, `internal/store/postgres/ropool.go`:
  - `NewReadOnlyPool(ctx, dsn)` with max 8 connections and
    `AfterConnect` running `SET default_transaction_read_only = on`
    and `SET statement_timeout = '5s'`;
  - a `V2Store` Reader constructor over it;
  - every handler query runs inside `pgx.BeginTxFunc` with
    `AccessMode: pgx.ReadOnly`.
- [ ] 1.6 Role wiring. Fill IMPL-0026's `runAPI` stub:
  - `API_LISTEN_ADDR` (default `:8080`), `METRICS_ADDR`;
  - `STORE_RO_DSN` is required in `split`; `all` may fall back to
    `STORE_DSN` with the same session settings (DESIGN-0027 OQ11);
  - readiness = pool ping plus `RequireSchema`;
  - the `all` topology mounts the API under the same process on its
    own listener.
- [ ] 1.7 First handlers: `/me` (stubbed principal until Phase 2),
  `/summary` (a real query) and `/openapi.yaml`.
- [ ] 1.8 Contract test harness, `internal/api/contract_test.go`: wraps
  every handler test's response in kin-openapi's
  `openapi3filter.ValidateResponse` against the embedded spec. A
  helper makes it impossible to write a handler test that skips
  validation.

#### Success Criteria

- `repo-guardian api` starts against a migrated, seeded database and
  serves `/api/v1/summary` whose response validates against the spec.
- `make lint-api` fails after a hand edit to `api.gen.go`. Prove it
  once, then revert.
- An `INSERT` attempted through the read-only pool fails with
  `cannot execute INSERT in a read-only transaction` (integration test).

---

### Phase 2: Authentication and authorization

#### Tasks

- [ ] 2.1 Add `github.com/coreos/go-oidc/v3`. `internal/api/authn.go`:
  - provider discovery from `OIDC_ISSUER` at startup, retried with
    backoff (readiness stays false until it succeeds);
  - `Verifier` with `ClientID = OIDC_AUDIENCE`, 60s skew, and
    `SupportedSigningAlgs` restricted to RS256/ES256;
  - extract `sub`, the `OIDC_NAME_CLAIM` and `OIDC_GROUPS_CLAIM`
    claims;
  - reject ID tokens: require `aud` to equal the API audience, and
    reject when `typ` is present and is not `at+jwt`/`JWT`.

  401 carries `WWW-Authenticate: Bearer error="invalid_token"` and
  increments `api_auth_failures_total{reason}` (missing, malformed,
  expired, audience, issuer, signature).
- [ ] 2.2 `API_AUTH_ENABLED=false`: a fixed `Principal{Orgs: ["*"]}`
  with a loud startup `slog.Warn`. The chart side of the guard is in
  Phase 7.
- [ ] 2.3 `internal/api/authz.go`:
  - load `API_AUTHZ_CONFIG` (YAML: `groups: {group: [orgs|"*"]}`,
    `defaultOrgs`, and `clients: {azp: [groups]}` per OQ4);
  - resolve a `Principal`'s visible orgs as a lowercased set, or the
    all flag;
  - read once at startup, with the chart checksum annotation rolling
    pods on change (OQ8).
- [ ] 2.4 Scope enforcement in SQL:
  - every `Reader` API method takes `Scope{All bool; Orgs []string}`;
  - queries use `($1::bool OR lower(org) = ANY($2))`;
  - a lint-style test lists every `api_*.sql` query and fails if one
    lacks the scope predicate (a grep over the sqlc sources for the
    `@scope_all` / `@scope_orgs` named parameters).
- [ ] 2.5 Visibility semantics:
  - a no-visible-org principal gets 403 everywhere except `/me`;
  - a resource in an invisible org gets 404, via the same "not found"
    path as a missing ID, so the two are indistinguishable.
- [ ] 2.6 Test issuer, `internal/api/oidctest` (OQ3): an httptest
  server serving discovery and JWKS with a generated key, plus
  `Mint(claims, opts)` to produce valid, expired, wrong-audience,
  wrong-issuer, bad-signature, ID-token-shaped and no-groups tokens.
- [ ] 2.7 Tests:
  - each rejection → 401 plus the counter reason;
  - no groups → 403 on `/summary` and 200 on `/me`;
  - `*` sees everything;
  - an `azp` mapping grants a machine client;
  - with two seeded orgs, `/summary` for `team-web` aggregates only its
    orgs.

#### Success Criteria

- Every token-rejection case yields 401 with the right counter label.
- The scope-predicate lint test fails when a query without the
  predicate is added. Prove it once, then revert.
- Two-org authz tests pass for `/summary` and `/me`. Phase 3 extends
  them to every endpoint.

---

### Phase 3: Read endpoints

#### Tasks

- [ ] 3.1 Extend the spec with the remaining endpoints from
  DESIGN-0027 § Endpoints:
  - `/rules`, `/rules/{kind}/{name}`, `/orgs`, `/orgs/{org}`,
    `/findings`, `/repositories`, `/repositories/{id}`,
    `/repositories/{id}/events`, `/repositories/{id}/checks`,
    `/compliance/history`, `/policy`, `/installations`;
  - keyset pagination `?cursor&limit` (default 50, max 200) on every
    list;
  - filter parameters as in the design;
  - evidence as a `oneOf` keyed by `reason`, with `evidence_version`,
    and documented as open for unknown versions.
- [ ] 3.2 sqlc queries, `internal/store/postgres/queries/api_*.sql`,
  one file per resource. All are keyset-paged on `(sort_key, id)`, with
  the scope predicate. Use the **shared compliance query** from
  IMPL-0025 7.1 for `/rules`, `/orgs` and `/summary`.
- [ ] 3.3 Cursor encoding, `internal/api/cursor.go` (OQ5): base64url
  JSON `{k: sortKey, id, f: filterHash}`. A cursor replayed with
  different filters returns 400.
- [ ] 3.4 `pr_stale` is computed in SQL:
  `remediation = 'pr_open' AND (evidence->'pr'->>'created_at')::timestamptz < now() - $stale`,
  with `PR_STALE_AFTER` (default 720h) overridable by `?stale_after=`,
  bounded 1h–8760h.
- [ ] 3.5 `/repositories/{id}/events`: merge `finding_events` and
  `repository_events` in SQL (`UNION ALL … ORDER BY occurred_at DESC,
  id DESC`) with keyset pagination over the merged stream.
- [ ] 3.6 `/policy`: the current version (the newest `policy_versions`
  row with `rollout_completed_at` or the latest `first_seen_at`),
  rollout state (active repositories at/behind the version), and
  `summary` passed through with a typed schema.
- [ ] 3.7 `migrated_from_v1` findings: the API returns the reason as-is.
  The UI renders it (Phase 6). The contract documents it.
- [ ] 3.8 Tests per endpoint:
  - contract validation;
  - two-org authz (visible, invisible → 404, `*`);
  - pagination stability (insert rows between page fetches and assert
    no duplicates or skips);
  - filter combinations (table-driven).
- [ ] 3.9 Compliance parity test: one seeded database through
  `report.Build`, `/rules`, `/orgs` and `InsertComplianceSnapshot`
  yields identical percentages, including the "unmeasured → null"
  case.
- [ ] 3.10 Performance check: a seeded fixture at the INV-0018 target
  (20k repositories, 10 rules, 200k findings, ~1M events) on
  testcontainers. `/summary`, `/rules`, `/orgs` and a filtered
  `/findings` page each run under 100ms at p99 over 100 requests. The
  test is tagged `perf` and not in default CI; results are recorded
  here.

#### Success Criteria

- Every DESIGN-0027 endpoint exists, validates against the spec, and
  passes two-org authz and pagination tests.
- The compliance parity test passes across report, API and snapshot.
- The perf fixture's p99 numbers are recorded, and any endpoint over
  100ms has the design's first remedy (a 30s cache alongside the status
  refresh) applied.

---

### Phase 4: Status page

#### Tasks

- [ ] 4.1 `internal/api/status.go`:
  - a `StatusCache` refreshed every 30s by a goroutine, which is the
    only reader of the underlying queries;
  - the handler serves the cached snapshot and never queries;
  - on the first request before the first refresh, it returns
    `state: "unknown"` rather than blocking.
- [ ] 4.2 Component rules, table-driven with DESIGN-0027's thresholds:
  checks, webhooks (informational), discovery, snapshots, GitHub API
  budget, and work backlog. Intervals (`CHECK_INTERVAL`,
  `DISCOVERY_INTERVAL`, `COMPLIANCE_SNAPSHOT_INTERVAL`) come from the
  same env vars the worker reads, and the chart passes them to `api`
  too.
- [ ] 4.3 Optional Temporal probe: if `TEMPORAL_ADDRESS` is set, a
  read-only client calls `DescribeTaskQueue` (enhanced mode) for
  backlog age and poller count. Failure marks the backlog component
  `unknown`, never the whole page down.
- [ ] 4.4 Fleet compliance uses the shared query over **all** orgs.
  Because the endpoint is public and aggregate-only, it is
  intentionally not scoped.
- [ ] 4.5 Privacy guard test: walk the `/status` response schema and
  assert that the only string fields are enums, RFC 3339 times and
  `detail`. Assert `detail` values come from a fixed template set,
  rendered only with durations (the test enumerates the templates).
- [ ] 4.6 `api_status_refresh_seconds` histogram; a refresh error is
  logged and counted, and the previous snapshot is kept.
- [ ] 4.7 `STATUS_PUBLIC=false` requires auth on `/status` (the same
  middleware), for operators who do not want it public.

#### Success Criteria

- `/api/v1/status` serves with zero database queries per request
  (asserted with an otelpgx or query-counting pool wrapper in tests).
- Every component threshold row has a passing table case.
- The privacy guard test fails if a free-text field is added to the
  schema. Prove it once, then revert.

---

### Phase 5: UI repository and the Bun BFF

Work in `github.com/donaldgifford/repo-guardian-ui`.

#### Tasks

- [ ] 5.1 Create the repository:
  - license, README, CODEOWNERS and Renovate, which repo-guardian's own
    policy will then check;
  - branch protection mirroring this repository's;
  - `mise.toml` pinning Bun and Node (for Playwright).
- [ ] 5.2 Layout:
  - `server/` (Hono BFF);
  - `web/` (React + Vite);
  - `spec/openapi.yaml` (vendored, pinned; OQ2);
  - `web/src/api/schema.d.ts`, generated by `openapi-typescript`;
  - a `bun run gen:api` script and a CI drift gate.
- [ ] 5.3 BFF config, `server/config.ts`:
  - env parse and validate: `OIDC_ISSUER`, `OIDC_CLIENT_ID`,
    `OIDC_CLIENT_SECRET`, `OIDC_SCOPES`, `API_UPSTREAM`,
    `UI_SESSION_KEYS` (comma list, ≥ 32 bytes each), `UI_SESSION_TTL`,
    `PUBLIC_URL`;
  - fail fast on missing values;
  - `/ui/config` exposes only display-safe values.
- [ ] 5.4 Sessions, `server/session.ts`:
  - `jose` `EncryptJWT` with `dir` and A256GCM; the key list comes
    first-key-encrypts, any-key-decrypts;
  - cookie `__Host-rg_session` (HttpOnly, Secure, `SameSite=Lax`,
    `Path=/`, no Domain);
  - chunking into `__Host-rg_session.0..n` above 3,800 bytes;
  - an absolute expiry of `UI_SESSION_TTL` stored inside the payload.
- [ ] 5.5 OIDC, `server/auth.ts`, with `openid-client` v6:
  - discovery;
  - `/auth/login` with PKCE, `state` and `nonce` in a short-lived
    encrypted `__Host-rg_auth` cookie;
  - `/auth/callback`, which validates `state`, exchanges the code,
    stores the tokens and redirects to the saved path. The path must
    be a same-origin relative path, which prevents an open redirect;
  - `POST /auth/logout` checks `Origin`, revokes the refresh token when
    the issuer supports RFC 7009, clears the cookies, and redirects to
    `end_session_endpoint`.
- [ ] 5.6 API proxy, `server/proxy.ts`:
  - `GET`/`HEAD` `/api/*` only (other methods → 405);
  - the upstream is always `API_UPSTREAM`;
  - strip the inbound `Cookie` and hop-by-hop headers;
  - `Authorization` comes from the caller's own `Bearer` if present
    (passed through untouched), else from the session;
  - refresh the access token when it expires within 60s, and re-set
    the cookie;
  - `/api/v1/status` and `/api/v1/openapi.yaml` go without a token;
  - `X-Request-ID` is propagated.
- [ ] 5.7 Static serving:
  - Vite build output served by Hono's `serveStatic`;
  - hashed assets cached long, `index.html` with `no-cache`;
  - SPA fallback only for non-`/api` and non-`/auth` paths;
  - unauthenticated requests for app paths redirect to login, while
    `/status` and its assets are public;
  - security headers: CSP (`default-src 'self'; connect-src 'self';
    frame-ancestors 'none'; script-src 'self'`), `X-Content-Type-Options`
    and `Referrer-Policy`.
- [ ] 5.8 `/healthz` (liveness) and `/readyz` (issuer discovery loaded
  and `API_UPSTREAM` `/healthz` reachable, cached for 10s).
- [ ] 5.9 `bun test` suite, against a mock issuer (OQ7) and a stub
  upstream, covering every BFF bullet in DESIGN-0027 § Testing Strategy:
  - state mismatch;
  - cookie attributes;
  - chunking;
  - key rotation;
  - refresh-before-expiry;
  - expired session → login;
  - proxy method and header hygiene;
  - single upstream;
  - bearer passthrough;
  - open-redirect rejection.
- [ ] 5.10 Container image:
  - multi-stage: `oven/bun` build, then Bun's distroless runtime;
  - non-root and read-only root filesystem compatible;
  - one process, no nginx.

  Publish to `ghcr.io/donaldgifford/repo-guardian-ui` from tags, signed
  with cosign keyless and with SLSA L3 provenance, mirroring
  repo-guardian's `ghcr.yml`. Apply the release-pipeline lessons from
  CLAUDE.md: extract the digest in the expression layer, and verify the
  first tag-triggered run creates jobs (OQ1).

#### Success Criteria

- A tagged `v0.1.0` of `repo-guardian-ui` publishes a signed image
  with provenance.
- Locally, the BFF completes login against the homelab IdP, holds the
  session only in the HttpOnly cookie, and proxies `/api/v1/me` to a
  running `repo-guardian api`.
- The `bun test` suite is green in CI.

---

### Phase 6: UI views

#### Tasks

- [ ] 6.1 Front-end foundations:
  - React 19, TypeScript strict, Vite, Tailwind, shadcn/ui (charts via
    Recharts);
  - TanStack Router (file routes) and TanStack Query;
  - an `openapi-fetch` client against same-origin `/api`;
  - an error boundary that renders RFC 9457 problems.
- [ ] 6.2 Fleet (`/`) and Repository (`/repos/:id`) first, per
  DESIGN-0027's sketches:
  - Fleet: tiles, compliance trend, PR age buckets, worst rules, and
    parked by reason;
  - Repository: the findings table with evidence renderers per reason,
    PR links built from structured fields, the timeline and recent
    checks.
- [ ] 6.3 Then Rules, Orgs, Findings (filters in the URL search params,
  and client-side CSV export of the fetched pages), History and Policy.
  The `/status` view is public and has no auth-only components.
- [ ] 6.4 Evidence rendering rules:
  - text only: no `dangerouslySetInnerHTML` (an ESLint rule forbids
    it) and no markdown;
  - unknown `evidence_version` → the reason code only;
  - `migrated_from_v1` → "last checked by v1; details on next check";
  - external links with `rel="noopener noreferrer"`.
- [ ] 6.5 An org filter across views, persisted in the URL. `/me`
  drives the "ask for access" page for principals with no visible orgs.
- [ ] 6.6 Unit tests for view models (formatting, buckets, percent
  rounding matching the API's one-decimal floor).

#### Success Criteria

- Every view in DESIGN-0027 § Views renders against a seeded API.
- The ESLint rule blocks `dangerouslySetInnerHTML` (proven by a
  failing lint on a probe commit).
- Navigating Fleet → Rule → Repository and applying filters works
  against the homelab API.

---

### Phase 7: Chart — api, ui, ingress, read-only role

In this repository, on `v2`, after IMPL-0026 Phase 8.

#### Tasks

- [ ] 7.1 Values per DESIGN-0027 § Chart (`api.*`, `ui.*`), plus:
  - `api.authz` rendered into a ConfigMap mounted at
    `API_AUTHZ_CONFIG`, with a checksum annotation;
  - `api.prStaleAfter`;
  - `api.status.public`;
  - `ui.image.tag` pinned.
- [ ] 7.2 Templates, each with `namespace: {{ .Release.Namespace }}`:
  - `deployment-api.yaml`, `service-api.yaml` (ClusterIP),
    `pdb-api.yaml`;
  - `deployment-ui.yaml`, `service-ui.yaml`, `pdb-ui.yaml`;
  - `ingress-ui.yaml`, with the whole host routed to `ui`;
  - `configmap-authz.yaml`.

  In `all` topology the API runs in the `all` pod; `ui` still deploys
  separately, pointing `API_UPSTREAM` at the `all` Service's API port.
- [ ] 7.3 Guards (`_helpers.tpl` plus schema), with negative tests in
  `values_guard_test.yaml`:
  - `ui.enabled` requires `api.enabled` and `api.auth.enabled`;
  - `ui.ingress.enabled` requires `api.auth.enabled`;
  - `api.auth.enabled=true` requires `api.auth.issuer`;
  - `ui.enabled` requires `ui.existingSecret`;
  - `split` + `api.enabled` requires an RO DSN source.
- [ ] 7.4 Read-only role per Postgres mode:
  - **baked:** init SQL creates `repoguardian_ro` for new volumes, plus
    the OQ6 path for existing volumes. The RO DSN Secret is generated
    alongside the app DSN.
  - **CNPG:** `spec.managed.roles` with `repoguardian_ro` (login,
    password from a generated Secret), and the api connects via the
    `-ro` Service.
  - **external:** `api.roDsn.existingSecret` is required, and the docs
    give the `CREATE ROLE` and `GRANT` statements.

  IMPL-0025's migration grants `SELECT` whenever the role exists.
- [ ] 7.5 Renovate in this repository: a custom manager or regex rule
  that bumps `ui.image.tag` in `values.yaml` from GHCR tags of
  `repo-guardian-ui`. Bumps land as chart patches with `dont-release`.
- [ ] 7.6 `docs/operations/ingress.md`: the UI host is the one
  chart-rendered Ingress, and the webhook path stays operator-owned
  (IMPL-0024).
- [ ] 7.7 helm-unittest, `api_ui_test.yaml`: renders per topology;
  secret scoping (the UI gets only its own Secret, and the API gets no
  App key or webhook secret); the guards; the authz checksum; the
  Ingress backend.

#### Success Criteria

- `make helm-test` is green, with every guard's negative case failing
  render with its message.
- `helm template` with `api.enabled`, `ui.enabled` and
  `ui.ingress.enabled` renders one Ingress, whose only backend is the
  `ui` Service.

---

### Phase 8: End-to-end, docs, homelab

#### Tasks

- [ ] 8.1 Playwright suite in `repo-guardian-ui`, run against the mock
  issuer and a `repo-guardian api` container on a seeded Postgres
  (docker compose in CI). It covers:
  - login;
  - fleet → rule → repository;
  - filters;
  - the public status page without login;
  - an HTML-bearing evidence string rendered as text;
  - `document.cookie` showing no session or token.
- [ ] 8.2 Homelab IdP setup (OQ10). Register:
  - the UI as a confidential client (redirect
    `https://<host>/auth/callback`);
  - the API audience `repo-guardian-api`;
  - a groups claim mapper;
  - two test groups mapped to different orgs.
- [ ] 8.3 Homelab deploy on the IMPL-0026 cutover instance:
  - enable `api`, `ui` and `ui.ingress`;
  - verify two users in different groups see different orgs;
  - verify a machine client with client credentials reads `/findings`
    through the UI host;
  - confirm the status page is reachable anonymously.
- [ ] 8.4 Docs:
  - `docs/usage/api.md`: authentication (Keycloak example), authz,
    status page, pagination, errors, compatibility policy, and a link
    to the rendered spec;
  - `docs/usage/ui.md`: views, sessions and their revocation trade-off,
    key rotation;
  - an mkdocs OpenAPI plugin (DESIGN-0027 OQ10; plugin choice OQ9)
    rendering `api/openapi.yaml`;
  - the chart README (`README.md.gotmpl`), then `make helm-docs`.
- [ ] 8.5 Release: attach `api/openapi.yaml` to GitHub releases (the
  goreleaser `extra_files`), and pin the UI's vendored spec to the rc
  tag.
- [ ] 8.6 Update docz statuses for DESIGN-0027 and this IMPL, and
  CLAUDE.md on `v2` (the api role, spec-first workflow, the scope
  predicate rule, the text-only evidence rule).

#### Success Criteria

- The Playwright suite is green in `repo-guardian-ui` CI.
- The homelab serves the UI and API behind one host with working
  login, correct per-group org visibility, and an anonymous status
  page.
- Docs are published, and the spec renders in mkdocs.

---

## File Changes

| File | Action | Description |
| ---- | ------ | ----------- |
| `api/openapi.yaml`, `api/oapi-codegen.yaml`, `api/overlay.yaml` (if needed) | Create | contract and generator config |
| `internal/api/gen/api.gen.go` | Create (generated) | strict server and models |
| `internal/api/{server,authn,authz,cursor,status,handlers_*}.go` | Create | api role |
| `internal/api/oidctest/` | Create | in-process test issuer |
| `internal/store/postgres/ropool.go`, `queries/api_*.sql` | Create | read-only pool and API queries |
| `internal/store/v2.go` | Modify | `Reader` API methods with `Scope` |
| `cmd/repo-guardian/*` | Modify | `runAPI` |
| `Makefile`, `.github/workflows/ci.yml`, `go.mod`, `.goreleaser.yaml` | Modify | generate/lint-api, deps, spec release asset |
| `charts/repo-guardian/templates/{deployment,service,pdb}-{api,ui}.yaml`, `ingress-ui.yaml`, `configmap-authz.yaml` | Create | chart |
| `charts/repo-guardian/{values.yaml,values.schema.json,templates/_helpers.tpl,templates/store-*.yaml,tests/*}` | Modify | values, guards, RO role |
| `renovate.json` | Modify | `ui.image.tag` bump rule |
| `docs/usage/{api,ui}.md`, `docs/operations/ingress.md`, `mkdocs.yml` | Create / Modify | docs |
| `repo-guardian-ui/**` (new repository) | Create | BFF, views, tests, image |

## Testing Plan

- [ ] Contract validation on every handler test (1.8).
- [ ] Authn rejection matrix and counter (2.7).
- [ ] Authz: two-org tests on every endpoint, 404 for invisible orgs,
  and the scope-predicate lint (2.4, 3.8).
- [ ] Read-only enforcement through the pool (Phase 1 criteria).
- [ ] Pagination stability under concurrent writes (3.8).
- [ ] Compliance parity across report, API and snapshot (3.9).
- [ ] Status: thresholds, zero queries per request, privacy schema
  guard (Phase 4).
- [ ] Perf fixture at 20k repositories (3.10, recorded).
- [ ] BFF `bun test` matrix (5.9).
- [ ] Playwright e2e (8.1).
- [ ] Chart guards and topology renders (7.7).

## Dependencies

- IMPL-0025 Phase 2 (tables, `Reader`), Phase 3 (evidence types) and
  Phase 7 (shared compliance query).
- IMPL-0026 Phase 1 (`v2` branch), Phase 5 (role dispatch and
  readiness) and Phase 8 (chart 2.0.0).
- New Go modules: `oapi-codegen` (tool), `coreos/go-oidc/v3`,
  `getkin/kin-openapi` (tests), `golang.org/x/oauth2` (transitive).
- The new GitHub repository `repo-guardian-ui`, with GHCR package
  visibility flipped to public after the first push (CLAUDE.md GHCR
  note).
- A homelab OIDC provider (OQ10).

## Open Questions

1. **UI image publishing pipeline.**
   - (a) Mirror repo-guardian's `ghcr.yml` shape: bake, cosign keyless,
     SLSA L3 generator by tag, digest extracted in the expression
     layer, triggered by a tag push. It has the same supply-chain
     posture as the main image, and the lessons are already paid for.
   - (b) A plain `docker/build-push-action` with cosign signing only
     (no SLSA) until v2 GA.
   - other:

2. **How the UI pins the spec.**
   - (a) Vendor `spec/openapi.yaml` from a repo-guardian **tag** (an rc
     or release asset) with a `bun run spec:update <tag>` script.
     Renovate bumps it via a custom regex manager on the pinned tag.
   - (b) A git submodule of repo-guardian at a commit.
   - (c) Fetch from `v2` HEAD during development, and pin only at
     release.
   - other:

3. **Go test issuer.**
   - (a) A hand-written `internal/api/oidctest` (httptest discovery and
     JWKS, generated key, `Mint` with per-case knobs). It gives full
     control over the malformed cases the matrix needs.
   - (b) `github.com/oauth2-proxy/mockoidc`, which is less code but
     harder to make it mint wrong-audience or bad-signature tokens.
   - other:

4. **Machine-client authorization.**
   - (a) Ship `api.authz.clients: {<azp>: [groups]}` in v2.0, so
     client-credentials tokens without a groups claim can be mapped
     (DESIGN-0027 mentions it).
   - (b) Require the IdP to put groups on client tokens; no `azp`
     mapping.
   - other:

5. **Cursor integrity.**
   - (a) Unsigned base64url JSON with a filter hash. Tampering can only
     reposition a page inside the caller's own visible scope, because
     authz is enforced in SQL on every page.
   - (b) HMAC-signed cursors (needs a key in chart values).
   - other:

6. **Read-only role for existing baked-Postgres installs**, whose
   volumes were initialized before this role existed.
   - (a) A chart `post-install,post-upgrade` hook Job in baked mode only
     that uses the baked admin Secret to
     `CREATE ROLE repoguardian_ro … IF NOT EXISTS`, and then re-runs the
     grant block. Migrations still never create roles (DESIGN-0025
     OQ12); the chart owns the role, as that decision intended.
   - (b) Document a manual `CREATE ROLE` step for existing baked
     installs; new installs get it from init SQL.
   - (c) In baked mode, run `api` with the app DSN plus the read-only
     session settings (as the `all` fallback does).
   - other:

7. **Mock issuer for BFF tests and Playwright.**
   - (a) The `oauth2-mock-server` npm package in `bun test` and
     Playwright CI, plus one real Keycloak run in the homelab (8.3).
   - (b) A Keycloak container in CI e2e (realistic, but slow and
     heavy).
   - other:

8. **Authz config changes.**
   - (a) Read at startup; the chart's checksum annotation rolls the
     `api` pods on change.
   - (b) Hot-reload on file change (fsnotify on the mounted ConfigMap).
   - other:

9. **Spec lint and mkdocs rendering tools.**
   - (a) `vacuum` for spec linting (a Go binary via mise) and
     `mkdocs-swagger-ui-tag` for rendering.
   - (b) Redocly CLI for linting (Node) and `neoteroi-mkdocs` (OAD) for
     rendering.
   - other:

10. **Homelab IdP for the auth work.**
    - (a) Deploy Keycloak in the homelab (the design's documented
      example, and it doubles as the docs walkthrough).
    - (b) Use an IdP already running in the homelab (name it in
      "other").
    - other:

## References

- DESIGN-0027 — read-only API, business UI, status page
- DESIGN-0025 / IMPL-0025 — tables, evidence, shared compliance query
- DESIGN-0026 / IMPL-0026 — roles, chart 2.0.0, cutover
- INV-0009 — status API and UI options (auth hard gate)
- INV-0018 — v2 (Obs 8, Obs 9)
- IMPL-0024 — operator-owned ingress
- [oapi-codegen v2.8.0](https://github.com/oapi-codegen/oapi-codegen/releases/tag/v2.8.0) ·
  [go-oidc](https://github.com/coreos/go-oidc) ·
  [kin-openapi](https://github.com/getkin/kin-openapi) ·
  [openid-client](https://github.com/panva/openid-client) ·
  [jose](https://github.com/panva/jose) · [Hono](https://hono.dev) ·
  [openapi-typescript / openapi-fetch](https://openapi-ts.dev) ·
  [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) ·
  [RFC 7009](https://www.rfc-editor.org/rfc/rfc7009)
