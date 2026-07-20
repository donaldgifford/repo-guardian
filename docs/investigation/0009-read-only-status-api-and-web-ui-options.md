---
id: INV-0009
title: "Read-only status API and web UI options"
status: Open
author: Donald Gifford
created: 2026-07-19
---
<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0009: Read-only status API and web UI options

**Status:** Open
**Author:** Donald Gifford
**Date:** 2026-07-19

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Findings](#findings)
  - [Observation 1 — current HTTP surface already runs two listeners](#observation-1--current-http-surface-already-runs-two-listeners)
  - [Observation 2 — the persisted data is thin but sufficient for a status page v1](#observation-2--the-persisted-data-is-thin-but-sufficient-for-a-status-page-v1)
  - [Observation 3 — the Store interface needs read-only list/aggregate methods](#observation-3--the-store-interface-needs-read-only-listaggregate-methods)
  - [Observation 4 — read-only Postgres routing is already half-built in the chart](#observation-4--read-only-postgres-routing-is-already-half-built-in-the-chart)
  - [Observation 5 — a second container image would double the release machinery](#observation-5--a-second-container-image-would-double-the-release-machinery)
- [Options](#options)
  - [API placement](#api-placement)
  - [Web UI placement](#web-ui-placement)
  - [Authentication](#authentication)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Question

Should repo-guardian grow an external, read-only status API (fleet/per-repo
reconcile state) so a web UI can be built on top of it without connecting to
Postgres directly — and if so, where should the API and the UI live: inside
this repo/binary by default, as a sibling deployment, or as separate services?

## Hypothesis

A versioned read-only API belongs in this repo (it is a thin projection of the
`repo_state` table and must evolve in lockstep with the store schema), but it
should run as its own deployment — not inside the reconciler process — using a
read-only database path. The web UI is a different toolchain (Bun/React/TS)
with its own release cadence and belongs in a separate repo consuming the
published API contract.

## Context

Operating the homelab deployment, the only ways to answer "what is
repo-guardian doing right now?" are Prometheus metrics (aggregates), pod logs,
or psql against the store. A status web page needs structured per-repo data
(last checked, status, last error, policy drift) without handing the UI — or
its users — database credentials. Because the access pattern is read/status
only, there is an opportunity to route it through read-only Postgres endpoints
(CNPG replicas, or a `SELECT`-only role) so the API can never contend with or
corrupt the write path.

**Triggered by:** operator experience post-IMPL-0015/0016 rollout; related:
DESIGN-0012 (persistent store), DESIGN-0017/0018.

## Approach

1. Inventory the current HTTP surface in `cmd/repo-guardian/main.go`.
2. Inventory what data is actually persisted (store schema + interface) vs
   what a status UI would want.
3. Check what the chart already provides for read-only Postgres routing
   (baked / cnpg / external modes).
4. Enumerate placement options for API and UI, with auth implications
   (Keycloak/Okta OIDC, not GitHub).

## Findings

### Observation 1 — current HTTP surface already runs two listeners

`main.go` runs two `http.Server`s: the main server (`POST /webhooks/github`
plus health, `main.go:398-408`) and the metrics server (`GET /metrics`,
`main.go:411-419`). The dual-listener pattern exists precisely so surfaces
with different exposure levels (internet-facing webhook vs cluster-internal
metrics) never share a port. A status API is a third exposure class
(authenticated humans/UI) and must not piggyback on either existing listener —
the webhook port sits behind the IP-allowlist/HMAC funnel path and the metrics
port is scrape-only.

### Observation 2 — the persisted data is thin but sufficient for a status page v1

The entire persistent surface today is one table, `repo_state`
(`internal/store/postgres/migrations/0001_init.up.sql`):

| Column | Status-page use |
|---|---|
| `installation_id, owner, repo` | identity / grouping by org |
| `last_checked_at` | freshness, "stale since" |
| `last_check_status` | success / error / skipped / pending rollup |
| `last_error` | per-repo error detail (already truncated to 1024 runes) |
| `policy_version` | drift vs current policy hash |

That supports a credible v1: fleet summary (counts by status/org), a sortable
repo list, per-repo detail with last error, and policy-drift flagging.
What it does **not** support: per-rule results, open-PR links, reconciler
outcomes — those live only in Prometheus aggregates and GitHub itself. Any
richer UI needs new persisted columns/tables (e.g. a `check_result` detail
table), which is follow-up store schema work, not an API-design blocker.

### Observation 3 — the Store interface needs read-only list/aggregate methods

`store.Store` (`internal/store/store.go:68-74`) exposes only point lookups and
the sweep query (`GetRepoState`, `StaleRepos`). An API needs paginated
list-with-filters and count-by-status aggregates. Natural shape: a separate
`store.Reader` interface (list, count, get) implemented by the postgres
package against its own `pgxpool` — which is exactly the seam where a
read-only DSN plugs in. The reconciler keeps `Store`; the API compiles against
`Reader` only, so the API binary path cannot write even by accident.

### Observation 4 — read-only Postgres routing is already half-built in the chart

- **cnpg mode:** CNPG automatically exposes a `<cluster>-ro` Service backed by
  replicas; the chart already templates the cluster and pooler
  (`store-cnpg-cluster.yaml`, `store-cnpg-pooler.yaml`). Wiring an RO DSN is a
  values/env plumbing task.
- **baked mode:** single Postgres pod — no replica to route to, but a
  dedicated `SELECT`-only role still enforces least privilege on the same
  instance.
- **external mode:** operator supplies a second `existingSecret` DSN pointing
  at their replica/pooler.

So the internal routing this investigation anticipates ("read-only pg
endpoints that route accordingly") is: optional `STORE_RO_DSN` env (falls back
to `STORE_DSN` when unset) + a `repoguardian_ro` role created by a migration,
with the chart wiring per-mode defaults.

### Observation 5 — a second container image would double the release machinery

The release pipeline (bake → dual-registry push → cosign → SLSA L3 provenance,
per the #143–#146 post-mortem) is per-image. A separate API image means a
second bake target, second SLSA invocation per registry, second signing path —
significant CI surface for zero benefit, since the API shares ~all Go
dependencies. Shipping the API as a **subcommand of the existing binary**
(`repo-guardian api`) reuses the image, the pipeline, and the distroless
runtime unchanged; the chart just adds a second Deployment with different
args.

## Options

### API placement

| Option | Shape | Pros | Cons |
|---|---|---|---|
| **A** | Third listener inside the reconciler process | zero new deploys; trivial wiring | couples API availability/load to reconciler lifecycle; every replica serves API; can't scale or restart independently; blurs exposure classes |
| **B** | Same repo + same image, `api` subcommand, own Deployment (chart `api.enabled`) | shares store code + migrations; independent scale/lifecycle; RO DSN natural; no new release machinery (Obs. 5) | one more Deployment to operate |
| **C** | Separate repo/service | full independence | duplicates schema knowledge; drift risk between store migrations and API queries; second Go release pipeline |
| **D** | Auto-API over Postgres (PostgREST/Hasura) | no code | exposes schema verbatim; no domain shaping; auth bolt-on; third-party component to run and patch |
| **E** | No API — Grafana over Prometheus | nothing to build | aggregates only, no per-repo detail/error text; not a product status page |

**Assessment:** B. A is tempting but wrong exposure class (Obs. 1); C/D trade
a small amount of code for permanent drift risk; E doesn't meet the need.

### Web UI placement

| Option | Shape | Pros | Cons |
|---|---|---|---|
| **A** | Separate repo (`repo-guardian-ui`), Bun/React/TS, consumes generated client from the OpenAPI spec published here | clean toolchain split (Go CI stays lean); independent release cadence; the OpenAPI spec is the only coupling point | cross-repo coordination on contract changes |
| **B** | `web/` directory in this repo | one repo | Bun/node toolchain lands in Go CI (paths-filter mitigates but complicates); release/versioning entanglement |
| **C** | SPA embedded in the Go binary via `embed.FS` | single deployable | couples UI release to binary release; bloats the ~19.5 MB distroless image; still needs the API anyway |

**Assessment:** A. The contract-first split also forces the API to stay
honest (versioned, documented) from day one.

### Authentication

Requirement: human access to UI and API via IdP (Keycloak or Okta), **not**
GitHub.

- **UI:** OIDC Authorization Code + PKCE against the IdP; the SPA holds
  short-lived access tokens.
- **API:** stateless bearer-JWT validation middleware — verify issuer,
  audience, and signature via the IdP's JWKS endpoint (standard `go-oidc` /
  `lestrrat-go/jwx` territory). No sessions, no user store in repo-guardian.
  Machine access (dashboards, scripts) uses client-credentials tokens from
  the same IdP.
- **Exposure gating:** `api.auth` disabled ⇒ the chart must not render an
  Ingress for the API (cluster-internal only). Auth configured ⇒ Ingress
  allowed. This mirrors the webhook's two-layer posture and keeps "accidental
  unauthenticated internet API" impossible by construction.
- The webhook listener, IP allowlist, HMAC, and funnel path are untouched by
  all of this — the API is a new Service/port, never a new route on the
  webhook server.

## Conclusion

**Answer:** Yes — build the read-only status API in this repo, but not as a
default-on part of the reconciler process. Ship it as an `api` subcommand of
the existing binary/image running as its own chart-gated Deployment
(`api.enabled=false` initially), reading through a `store.Reader` interface
backed by an optional read-only DSN and a `SELECT`-only Postgres role. The web
UI is a separate repo consuming the published OpenAPI contract, with
Keycloak/Okta OIDC on both UI (code + PKCE) and API (bearer JWT).

## Recommendation

Phase the work as three separable efforts (each its own DESIGN/IMPL):

1. **Phase A — API v1 (this repo).** OpenAPI 3.1 spec committed under
   `api/openapi.yaml`; `oapi-codegen` server stubs; endpoints:
   `GET /api/v1/summary` (counts by status/org, current policy version,
   stale count), `GET /api/v1/repos` (paginated, filter by org/status/drift),
   `GET /api/v1/repos/{owner}/{repo}`, `GET /healthz`. New `store.Reader`
   interface + postgres impl with own pool, statement timeout, `STORE_RO_DSN`
   fallback to `STORE_DSN`. Migration adds `repoguardian_ro` role. Chart:
   `api.enabled`, second Deployment (same image, `args: ["api"]`), Service,
   optional Ingress gated on auth config. OIDC JWT middleware included from
   the start (configurable issuer/audience/JWKS URL), toggleable off for
   cluster-internal use.
2. **Phase B — UI (new repo).** Bun + React + TS scaffold; OIDC login (code +
   PKCE); TS client generated from the OpenAPI spec; status pages over the
   Phase A endpoints. No repo-guardian changes beyond publishing the spec
   (e.g. as a release artifact or committed file the UI repo vendors).
3. **Phase C — richer data (optional, later).** Persist per-rule check
   results / PR references if the UI needs more than repo-level status;
   extends the store schema and the API additively
   (`/api/v1/repos/{o}/{r}/checks`).

Do **not** default-enable the API in the chart until Phase A has shipped and
the auth posture is validated in the homelab.

## Open Questions

1. API placement?
   - (a) same repo + same image, `api` subcommand, own Deployment behind
     `api.enabled=false` (recommended)
   - (b) third listener inside the reconciler process — fewest moving parts,
     accepts coupled lifecycle
   - (c) separate repo/service
   - (d) PostgREST/Hasura over the store
   - (e) skip the API; Grafana dashboards over Prometheus
   - other:
2. Read-only DB path?
   - (a) optional `STORE_RO_DSN` (falls back to `STORE_DSN`) + dedicated
     `SELECT`-only `repoguardian_ro` role; chart wires CNPG `-ro` Service in
     cnpg mode (recommended)
   - (b) reuse `STORE_DSN` as-is; rely on the `Reader` interface alone for
     read-only-ness
   - other:
3. API auth for v1?
   - (a) OIDC bearer-JWT middleware built in Phase A, toggleable off for
     cluster-internal; Ingress rendering hard-gated on auth being configured
     (recommended)
   - (b) no auth in v1, NetworkPolicy/cluster-internal only; add OIDC in a
     follow-up
   - (c) always require auth, even in-cluster
   - other:
4. Contract format?
   - (a) OpenAPI 3.1 spec in-repo, `oapi-codegen` server stubs, generated TS
     client for the UI (recommended)
   - (b) hand-rolled REST + markdown docs
   - (c) GraphQL
   - other:
5. UI placement?
   - (a) separate repo consuming the published OpenAPI contract (recommended)
   - (b) `web/` directory in this repo behind CI paths-filters
   - (c) SPA embedded in the Go binary via `embed.FS`
   - other:
6. IdP assumption for design purposes?
   - (a) design against generic OIDC (issuer/audience/JWKS config), validate
     with Keycloak in the homelab first (recommended)
   - (b) target Okta specifically
   - other:

## References

- `cmd/repo-guardian/main.go` — dual-listener pattern (`newMainServer` :398,
  `newMetricsServer` :411)
- `internal/store/store.go` — current `Store` interface (:68–74)
- `internal/store/postgres/migrations/0001_init.up.sql` — `repo_state` schema
- `charts/repo-guardian/templates/store-cnpg-cluster.yaml`,
  `store-cnpg-pooler.yaml` — CNPG shapes (RO service comes with CNPG)
- DESIGN-0012 — persistent store rationale
- Release-pipeline post-mortem (PRs #143–#146) — why a second image is costly
- INV-0008 — concurrent investigation (custom-properties config surface)
