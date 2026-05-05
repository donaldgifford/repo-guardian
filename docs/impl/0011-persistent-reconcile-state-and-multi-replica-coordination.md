---
id: IMPL-0011
title: "Persistent reconcile state and multi-replica coordination"
status: Draft
author: Donald Gifford
created: 2026-05-03
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0011: Persistent reconcile state and multi-replica coordination

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-05-03

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Interfaces and in-memory implementations + mockery](#phase-1-interfaces-and-in-memory-implementations--mockery)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Postgres Store implementation](#phase-2-postgres-store-implementation)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Valkey Queue implementation + in-flight reaper](#phase-3-valkey-queue-implementation--in-flight-reaper)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: Valkey Scheduler implementation](#phase-4-valkey-scheduler-implementation)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: Webhook integration + observability metrics](#phase-5-webhook-integration--observability-metrics)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: Helm chart deployment shapes](#phase-6-helm-chart-deployment-shapes)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
  - [Phase 7: Multi-replica validation + homelab smoke](#phase-7-multi-replica-validation--homelab-smoke)
    - [Tasks](#tasks-6)
    - [Success Criteria](#success-criteria-6)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Implement the interface-first persistence layer described in DESIGN-0012:
durable `Store` (Postgres) for repo reconcile state, durable `Queue` (Valkey
list + in-flight ZSET) for work items, and `Scheduler` (Valkey lock) for
multi-replica leader election. All three back interfaces with both a
durable and an in-memory implementation so the binary continues to support
a no-dep single-replica deployment alongside the new multi-replica path.
Helm chart gains four deployment shapes (no-dep / baked / cnpg+baked /
external) with `baked` as the new default. Add 10 new Prometheus metrics
and an opt-in `PrometheusRule`.

**Implements:** DESIGN-0012

## Scope

### In Scope

- New Go packages: `internal/store/`, `internal/queue/`, `internal/scheduler/`
  with `memory` + concrete (`postgres` / `valkey`) implementations.
- Mockery v2 config and generated mocks for `Store`, `Queue`, `Scheduler`.
- pgx/v5 + golang-migrate setup, with embedded SQL migrations, applied at
  binary startup before any Store traffic.
- go-redis/v9 client for both Valkey queue and lock.
- New env vars: `STORE_BACKEND`, `STORE_DSN`, `QUEUE_BACKEND`,
  `QUEUE_VALKEY_DSN`, `SCHEDULER_BACKEND`, `RECONCILE_FRESHNESS`,
  `RATE_LIMIT_RESERVE`, `WORKER_CONCURRENCY`, `JOB_ACK_TIMEOUT`,
  `REAPER_INTERVAL`, `STORE_POSTGRES_MAX_CONNS`, `STORE_SWEEP_BATCH_SIZE`.
- Engine integration: replace the in-process channel queue and scheduler
  with the new interfaces. Sweep handler queries `Store.StaleRepos` and
  enqueues via `Queue.Enqueue`.
- `policy_version` hash plumbed through the policy loader, store schema,
  and sweep query.
- Webhook handler returns 202 and enqueues; no inline engine call.
- Helm chart: render Postgres / Valkey / CNPG resources gated by mode
  flags; auto-generated Secrets with `existingSecret` overrides; optional
  `ServiceMonitor` and `PrometheusRule`.
- Helm-unittest coverage for the deployment-shape matrix.
- 10 new Prometheus metrics from DESIGN-0012 §Observability.
- Homelab smoke validation at `replicas: 3`.

### Out of Scope

- HTTP admin force-recheck endpoint (Q4: deferred — operators use psql).
- Per-installation schedules (Q1: deferred — single global sweep only).
- Per-rule freshness windows (Q2: handled via `policy_version` instead).
- High-priority webhook queue split (Q3: single shared queue; promote
  later if metrics show contention).
- NATS / `queue/nats` / `scheduler/nats` implementations (interface-ready,
  not implemented in V1).
- `scheduler/k8s-lease` implementation (interface-ready, not built).
- SLO target commitment (deferred to a follow-up DESIGN once we have a
  release cycle of prod data).
- Migration tooling between Postgres modes (`baked` ↔ `cnpg` ↔ `external`)
  — operators use `pg_dump` / `pg_restore` if they care about preserving
  `last_checked_at`.
- Migrating existing hand-written `github.Client` mocks to mockery
  (cleanup follow-up, not a prerequisite).

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its
tasks are checked off and its success criteria are met. Commit at the end
of every numbered task with a conventional commit message.

---

### Phase 1: Interfaces and in-memory implementations + mockery

Establish the interface boundaries with full in-memory implementations.
The engine starts depending on the interfaces but observable behavior
does not change yet — the in-memory queue and ticker are functionally
equivalent to today's `internal/checker/queue.go` + `internal/scheduler/`.

#### Tasks

- [ ] Add `internal/store/store.go` with `RepoState` struct (incl.
  `PolicyVersion`) and `Store` interface as defined in DESIGN-0012.
- [ ] Add `internal/store/memory/memory.go` — map-backed implementation,
  thread-safe via `sync.RWMutex`, full contract coverage incl.
  `StaleRepos(freshness, currentPolicyVersion, limit)`.
- [ ] Add `internal/queue/queue.go` with `Job` struct and `Queue`
  interface (`Enqueue`, `Subscribe`, `Close`).
- [ ] Add `internal/queue/memory/memory.go` — buffered channel
  implementation; `Subscribe` is a goroutine consuming from the channel.
- [ ] Add `internal/scheduler/scheduler.go` with `Scheduler` interface
  (`Schedule(name, interval, handler)`, `Stop`).
- [ ] Add `internal/scheduler/ticker/ticker.go` — `time.Ticker`-based
  implementation that fires the handler on every tick (no leader-election;
  single-replica only).
- [ ] Wire the new interfaces into `cmd/repo-guardian/main.go` —
  construct `memory` implementations behind config flags, pass instances
  to engine constructor.
- [ ] Move `internal/checker/queue.go` to `internal/worker/worker.go`
  (Open Q5 resolution). The in-process workers now consume from
  `Queue.Subscribe` instead of the legacy buffered channel. Worker
  pool count comes from `WORKER_CONCURRENCY`.
- [ ] Refactor `internal/scheduler/`: keep the existing weekly tick logic
  but move it behind `Scheduler.Schedule(name="sweep", interval=...)`.
- [ ] Update `internal/config/` to read `STORE_BACKEND`, `QUEUE_BACKEND`,
  `SCHEDULER_BACKEND` (all default to their `memory`/`ticker` flavors in
  this phase). New types live alongside existing config struct.
- [ ] Add `policyVersion()` helper in `internal/policy/`: SHA-256 of
  canonical-JSON-marshaled `PolicyConfig` (sorted keys), followed by
  `(name, content)` pairs from `TemplateStore` in sorted order. Hash
  must change when policy OR template content OR env-var overrides
  change. Wire it through to the Engine. (Open Q4 resolution.)
- [ ] Set up `.mockery.yaml` v2 config covering `Store`, `Queue`,
  `Scheduler`. Output to `internal/<pkg>/mocks/`.
- [ ] Add `make mocks` target. Reference it from `make ci`.
- [ ] Generate initial mocks; commit them to the repo.
- [ ] Add unit tests for `memory` implementations: store
  upsert/freshness/policy-version, queue enqueue/consume order, ticker
  firing.

#### Success Criteria

- `go build ./...` succeeds.
- `make test` green.
- `make lint` and `make fmt` green.
- `make mocks` regenerates without diff after a clean run.
- Existing engine integration tests pass with no behavior change.
- Coverage on new packages ≥ 80%.

---

### Phase 2: Postgres `Store` implementation

Add the durable Postgres-backed `Store`. Migrations applied at binary
startup. Freshness gate plumbed through the sweep handler.

#### Tasks

- [ ] Add `pgx/v5` and `golang-migrate/migrate/v4` to `go.mod`.
- [ ] Create `internal/store/postgres/migrations/` with `embed.FS` and
  `0001_init.up.sql` / `0001_init.down.sql` containing the `repo_state`
  schema from DESIGN-0012 §Data Model.
- [ ] Add `internal/store/postgres/postgres.go` implementing `Store`
  against a `*pgxpool.Pool`. All queries parameterized; `StaleRepos`
  expressed as `WHERE last_checked_at IS NULL OR last_checked_at < $1
  OR policy_version <> $2 ORDER BY last_checked_at NULLS FIRST LIMIT $3`.
- [ ] Add `internal/store/postgres/migrate.go` — runs `migrate.Up()` at
  startup, fails the binary if migrations fail. Uses pgx-backed driver.
- [ ] Read `STORE_DSN` and `STORE_POSTGRES_MAX_CONNS` from config; build
  `pgxpool.Config` with the connection cap.
- [ ] Wire `STORE_BACKEND=postgres` selection into `main.go`. Default
  value of `STORE_BACKEND` flips to `postgres` in this phase only when
  `STORE_DSN` is set; otherwise stays `memory` (chart drives the choice
  via env vars; binary alone is forgiving).
- [ ] Add `Store.Close()` semantics: drain pool with timeout, log on
  shutdown.
- [ ] Add `repo_guardian_store_query_seconds` histogram (deferred wiring
  to Phase 5; just register here).
- [ ] Tag the integration tests `_integration` and add testcontainers-go
  dependency. New file
  `internal/store/postgres/postgres_integration_test.go` exercises:
  upsert + read-back, stale query honoring freshness, stale query
  honoring policy-version mismatch, idempotent migration apply.
- [ ] Add `make test-integration` target that runs `go test -tags=integration ./...`.

#### Success Criteria

- `STORE_BACKEND=postgres STORE_DSN=postgres://... ./repo-guardian`
  comes up clean against an empty database, applies migrations, and the
  smoke path (manual webhook → enqueue → engine → store update) writes
  a row.
- Integration tests green against `testcontainers-go` Postgres 16.
- Contract test parity: same test suite passes against `memory` and
  `postgres` Store implementations.
- `make ci` green.

---

### Phase 3: Valkey `Queue` implementation + in-flight reaper

Add the durable Valkey queue and the per-pod reaper goroutine. Engine
worker goroutines consume from `queue/valkey`.

#### Tasks

- [ ] Add `github.com/redis/go-redis/v9` to `go.mod`.
- [ ] Add `internal/queue/valkey/valkey.go` implementing `Queue`:
  - `Enqueue` does `LPUSH repo-guardian:queue:jobs <json-job>`.
  - `Subscribe` runs N consumer goroutines, each in a loop:
    blocking `BRPOP queue:jobs` to wait for a job, then a tiny Lua
    script (`EVAL`/`EVALSHA`) atomically `LPUSH`-back-and-`ZADD
    in-flight` (`BRPOP` itself can't compose with Lua). On handler
    success, `ZREM in-flight`. On handler error or timeout, leave
    in-flight for the reaper. Document the script in the package
    doc comment. (Open Q7 resolution.)
  - Job ID is a deterministic hash of `(installation_id, owner, repo)`
    so dedupe is observable in metrics. (Engine reconcile is
    idempotent regardless.)
- [ ] Add `internal/queue/valkey/reaper.go` — goroutine that:
  1. Every `REAPER_INTERVAL` (default 60s) attempts `SET
     repo-guardian:lock:reaper <pod-id> NX EX 30`.
  2. If acquired, runs `ZRANGEBYSCORE in-flight 0 (now - JOB_ACK_TIMEOUT)`,
     re-LPUSHes each entry to `queue:jobs`, then `ZREM`s from in-flight.
  3. Releases the lock by waiting for TTL (no early `DEL` to avoid
     stomping on a re-acquired lock during clock drift).
- [ ] Use a Lua script for the BRPOP→ZADD claim transition to keep
  claim atomic. (Single round-trip to Valkey.)
- [ ] Read `QUEUE_VALKEY_DSN`, `JOB_ACK_TIMEOUT`, `REAPER_INTERVAL` from
  config.
- [ ] Wire `QUEUE_BACKEND=valkey` selection into `main.go`.
- [ ] AUTH parsing: `redis://:password@host:port/db` is the canonical
  DSN form; client honors AUTH automatically. Fail fast at startup if
  Valkey ping fails.
- [ ] Register Prometheus metrics (deferred wiring to Phase 5):
  `repo_guardian_queue_depth`, `_enqueued_total`, `_claimed_total`,
  `_acked_total`, `_reaped_total`.
- [ ] Add integration tests under `_integration` against
  `testcontainers-go` Valkey 8: enqueue/consume FIFO order, in-flight
  reaper requeues stuck entries, multiple workers see no double-claim
  (race test with N goroutines).
- [ ] Add a contract test sweep that runs the same suite against
  `memory` and `valkey` queue implementations.

#### Success Criteria

- Multi-worker race test passes — 1000 enqueues, 10 workers, every job
  claimed exactly once.
- Reaper test passes: kill a worker mid-job, confirm reaper requeues
  within `JOB_ACK_TIMEOUT + REAPER_INTERVAL` window, second worker
  picks it up.
- Memory-backend contract tests stay green; Valkey contract tests pass.
- `make test-integration` green.
- `make ci` green.

---

### Phase 4: Valkey `Scheduler` implementation

Add the leader-election scheduler. Each pod's ticker attempts a Valkey
SET-NX-EX lock; only the holder runs the handler.

#### Tasks

- [ ] Add `internal/scheduler/valkey/valkey.go` implementing `Scheduler`:
  - `Schedule(name, interval, handler)` starts a goroutine running
    `time.NewTicker(interval)`.
  - On each tick, attempt `SET repo-guardian:lock:<name> <pod-id> NX EX
    <ttl>` where `ttl` is the larger of (handler runtime estimate,
    interval × 0.5). Default 30s.
  - If SET succeeded, run handler; if not, skip this tick.
  - Lock is not extended mid-handler in V1; if the handler runs longer
    than `ttl`, two pods could overlap on the next tick. Document this
    and keep `ttl` generously larger than realistic handler runtime
    (sweep handler should be < 5s for 200-repo batch).
- [ ] Pod ID derivation: prefer `POD_NAME` env (downward API), fall
  back to a startup-time random `xid` if absent. Surfaced as the
  `pod` label on `repo_guardian_scheduler_is_leader`.
- [ ] Read `SCHEDULER_BACKEND` from config; wire `valkey` selection
  into `main.go` and use the same `*redis.Client` instance as the
  queue.
- [ ] Register `repo_guardian_scheduler_is_leader` gauge (deferred
  wiring to Phase 5).
- [ ] Integration test: spin up two scheduler instances pointed at the
  same testcontainer Valkey, run a tick, assert exactly one handler
  invocation.
- [ ] Contract test sweep: same suite passes against `ticker` and
  `valkey` scheduler implementations (acknowledging that `ticker` is
  N-runs-per-tick under multi-instance — tested only in single-instance
  mode).

#### Success Criteria

- Two-pod leader-election test green: with both pods running, one tick
  fires the handler exactly once.
- Pod death test: kill the leader pod mid-handler, observe next tick
  goes to the surviving pod after `ttl` expiry.
- `make ci` green.

---

### Phase 5: Webhook integration + observability metrics

Webhook handler enqueues instead of running engine inline. All 10 new
metrics wired up.

#### Tasks

- [ ] Refactor `internal/webhook/handler.go`: on a valid event, build a
  `Job` and call `Queue.Enqueue`; respond 202 with the queued Job ID.
  Engine call removed from the handler hot path.
- [ ] Add metric `repo_guardian_queue_enqueued_total{trigger}` with
  values `"webhook"`, `"sweep"`, `"push"`.
- [ ] Wire `repo_guardian_queue_depth{queue}` — periodic `LLEN
  queue:jobs` and `ZCARD queue:in-flight` poll, every 15s, exported via
  the registered gauge.
- [ ] Wire `repo_guardian_queue_claimed_total` — increment on `BRPOP`
  success in `queue/valkey`.
- [ ] Wire `repo_guardian_queue_acked_total{outcome}` —
  `outcome=success|error` on each handler return.
- [ ] Wire `repo_guardian_queue_reaped_total` — increment per entry the
  reaper requeues.
- [ ] Wire `repo_guardian_scheduler_is_leader{pod}` — set to 1 in the
  tick when the lock is acquired, 0 otherwise (with a one-tick decay).
- [ ] Wire `repo_guardian_scheduler_sweep_batch_size` histogram — observe
  the count of jobs enqueued per sweep call.
- [ ] Wire `repo_guardian_store_query_seconds{op}` — middleware around
  every pgx call, op label = method name (`stale_repos`,
  `update_repo_state`, `get_repo_state`).
- [ ] Wire `repo_guardian_rate_limit_remaining{installation_id}` via
  a `RoundTripper` wrapper at
  `internal/github/ratelimit_transport.go` that captures
  `X-RateLimit-Remaining` from every response and writes the gauge.
  Composed with the existing ghinstallation transport at client
  construction. (Open Q10 resolution.)
- [ ] Wire `repo_guardian_rate_limit_reserve_blocked_total{installation_id}`
  — increment when the sweep enqueue path skips a repo because
  `remaining < (limit × RATE_LIMIT_RESERVE)`.
- [ ] Update `internal/checker/sweep.go` (new): replaces the existing
  scheduler enumeration logic. Calls
  `Store.StaleRepos(RECONCILE_FRESHNESS, policyVersion, batchSize)`,
  iterates results, applies the rate-limit reserve gate per
  installation, and `Queue.Enqueue`s the survivors.
- [ ] Webhook ACK SLA test: mock Queue, send a webhook, assert response
  written within 2s end-to-end (use a tight test timeout).
- [ ] Implement worker shutdown semantics on SIGTERM: stop claiming
  new jobs → wait up to `(grace - 10s)` for in-flight handlers to
  complete and ack normally → for any remaining in-flight jobs,
  `LPUSH queue:jobs` + `ZREM queue:in-flight` (nack-and-requeue) →
  close redis client → exit. SIGKILL fallback handled by the reaper.
  (Open Q11 resolution.)
- [ ] Test: send SIGTERM to a worker mid-engine-call, assert the job
  is requeued (not left dangling for the reaper) within the grace
  window.

#### Success Criteria

- Webhook returns 202 within 2s on the happy path.
- All 10 metrics visible at `/metrics` after a smoke run.
- Integration test: end-to-end sweep enqueues N jobs, workers consume,
  store rows reflect updated `last_checked_at`, metrics reflect the
  same N.
- `make ci` green.

---

### Phase 6: Helm chart deployment shapes

Render the four deployment shapes from DESIGN-0012 §Backend modes via
chart values. Add `serviceMonitor` and `prometheusRule` opt-in surfaces.

#### Tasks

- [ ] Add `charts/repo-guardian/templates/store-postgres.yaml` (Deployment
  + PVC + Service) gated by `store.backend=postgres` AND
  `store.postgres.mode=baked`.
- [ ] Add `charts/repo-guardian/templates/store-postgres-secret.yaml` —
  auto-generated password Secret using the `lookup` + `randAlphaNum`
  pattern; skipped if `store.postgres.existingSecret` is set.
- [ ] Add `charts/repo-guardian/templates/store-cnpg-cluster.yaml` —
  `Cluster` CR gated by `store.postgres.mode=cnpg`. Mirrors
  server-price-tracker pattern (instances, imageName, bootstrap,
  storage, managed.services, monitoring, postgresql.parameters,
  resources).
- [ ] Add `charts/repo-guardian/templates/store-cnpg-pooler.yaml` — `Pooler`
  CR gated by `store.postgres.mode=cnpg AND store.postgres.cnpg.pooler.enabled`.
- [ ] Add `charts/repo-guardian/templates/queue-valkey.yaml` (Deployment
  + PVC + Service) gated by `queue.backend=valkey` AND
  `queue.valkey.mode=baked`.
- [ ] Add `charts/repo-guardian/templates/queue-valkey-secret.yaml` —
  auto-generated AUTH Secret; skipped if `queue.valkey.existingSecret`
  is set. AUTH on by default.
- [ ] Update `charts/repo-guardian/templates/deployment.yaml`:
  - `STORE_DSN` from `store.postgres.existingSecret` ref OR the
    chart-rendered Postgres Secret OR the CNPG `<cluster>-app` Secret
    (`secretKeyRef` pointing at the keys CNPG creates).
  - `QUEUE_VALKEY_DSN` from `queue.valkey.existingSecret` OR
    chart-rendered Valkey Secret.
  - Inject `STORE_BACKEND`, `QUEUE_BACKEND`, `SCHEDULER_BACKEND`,
    `STORE_POSTGRES_MAX_CONNS`, `STORE_SWEEP_BATCH_SIZE`,
    `REAPER_INTERVAL`, `JOB_ACK_TIMEOUT`, `WORKER_CONCURRENCY`.
  - `POD_NAME` from the downward API for scheduler pod-ID.
- [ ] Update `charts/repo-guardian/values.yaml` with the full schema
  from DESIGN-0012 §API/Interface Changes (store / queue / scheduler /
  serviceMonitor / prometheusRule blocks). Pin
  `store.postgres.baked.image` to a specific minor (e.g.,
  `postgres:16.4`) and `queue.valkey.baked.image` to a specific minor
  (e.g., `valkey/valkey:8.0`). (Open Q2/Q3 resolutions.)
- [ ] Set `terminationGracePeriodSeconds: 60` on the Deployment to
  give workers time to nack-and-requeue in-flight jobs before
  SIGKILL. (Open Q11 resolution.)
- [ ] Add `charts/repo-guardian/templates/servicemonitor.yaml` gated
  by `serviceMonitor.enabled`.
- [ ] Add `charts/repo-guardian/templates/prometheusrule.yaml` gated
  by `prometheusRule.enabled`. Render the 5 starter alerts with
  values-overridable `for:` / threshold expressions.
- [ ] Add helm-unittest cases under `charts/repo-guardian/tests/` for
  each deployment shape:
  - `memory + memory + ticker` → exactly one Deployment, no PVCs, no
    Postgres/Valkey resources.
  - `baked + baked` → repo-guardian + Postgres + Valkey + 2 PVCs +
    matching Services.
  - `cnpg + baked` → repo-guardian + CNPG `Cluster` CR + Valkey +
    Valkey PVC + Service. No Postgres Deployment, no Postgres Secret.
  - `external + external` → repo-guardian only; DSN env vars sourced
    from the operator-provided existingSecret.
- [ ] Stamp `namespace: {{ .Release.Namespace }}` in every new
  template's metadata (kustomize+ArgoCD requirement, see PR #67
  post-mortem).
- [ ] Bump chart `version` from `0.4.x` (post-IMPL-0012) to `0.5.0`
  (MINOR — new shapes, no breaking changes for the old in-memory
  mode operator who explicitly sets `store.backend=memory`).
  IMPL-0011 ships AFTER IMPL-0012 per the revised order. (Open Q8
  resolution.)
- [ ] Bump chart `appVersion` to match the binary release that ships
  this work.
- [ ] Add the `0.5.0` release-notes entry to
  `charts/repo-guardian/CHANGELOG.md` calling out the default flip
  to `baked` and the legacy opt-in:

  > **Behavior change**: chart 0.5.0 defaults to baked Postgres +
  > Valkey (`store.backend=postgres`, `queue.backend=valkey`). To
  > preserve the previous in-memory single-replica behavior, set
  > `store.backend=memory`, `queue.backend=memory`,
  > `scheduler.backend=ticker` in your values.

  (Open Q9 resolution.)
- [ ] Run `helm template ... | kubectl apply --dry-run=client` for
  each of the four shapes, confirm clean output and no unparseable
  YAML.
- [ ] Update `charts/repo-guardian/README.md` with a "Choosing a
  deployment shape" section linking the four modes to operator
  scenarios.

#### Success Criteria

- All 4 helm-unittest shape cases green.
- `ct lint` and `ct install` (against a kind cluster) green for
  `baked + baked`.
- Manual `helm template` for each shape produces a kubectl-applyable
  document set.
- Chart README documents the four shapes and the CNPG operator prereq.

---

### Phase 7: Multi-replica validation + homelab smoke

End-to-end validation that the durable backends actually deliver on the
multi-replica promise.

#### Tasks

- [ ] Multi-replica concurrency test (in-process, testcontainers
  Postgres + Valkey): N=10 worker goroutines, enqueue 1000 jobs,
  assert every job processed exactly once and `repo_state` reflects
  all 1000.
- [ ] Restart-safety test (in-process): kill the testcontainer Valkey
  mid-sweep, bring it back, assert reaper requeues in-flight, no jobs
  permanently lost, engine idempotency makes any double-claim safe.
- [ ] Homelab deploy: `helm upgrade --install repo-guardian
  --version 0.5.0 -n repo-guardian` against the homelab cluster with
  `replicas: 3`, `store.postgres.mode=cnpg`,
  `queue.valkey.mode=baked`. Validate via `kubectl logs` that all 3
  pods are workers and exactly one is leader at a time.
- [ ] Homelab leader-failover test: `kubectl delete pod
  <leader-pod>`; observe in metrics/logs that another pod becomes
  leader within `lock:sweep` TTL (30s).
- [ ] Confirm `donaldgifford/logpush` and
  `donaldgifford/repo-guardian-test-repo` reconciles still produce
  PRs as before, with `last_checked_at` written to Postgres after
  each.
- [ ] Production-grade documentation:
  - `docs/operations/scaling.md` — how to size `WORKER_CONCURRENCY`
    and `STORE_POSTGRES_MAX_CONNS` against expected repo count.
  - `docs/operations/migrations.md` — how to operate the Postgres
    schema; running migrations out-of-band; backup expectations.
  - Update `CLAUDE.md` Architecture section.
  - Update MEMORY.md with any post-mortem learnings.

#### Success Criteria

- 3-replica homelab deploy passes for one full sweep cycle without
  duplicate reconciles.
- Leader-failover within 30s of a pod kill.
- All metrics visible in Prometheus and the 5 starter alerts in a
  healthy state.
- `cosign verify` and SLSA provenance pass on the chart 0.5.0
  artifact.

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `go.mod` / `go.sum` | Modify | Add pgx/v5, golang-migrate, go-redis/v9, testcontainers-go |
| `.mockery.yaml` | Create | Declare Store/Queue/Scheduler interfaces |
| `Makefile` | Modify | Add `mocks`, `test-integration` targets |
| `cmd/repo-guardian/main.go` | Modify | Construct Store/Queue/Scheduler from env, pass to engine |
| `internal/store/store.go` | Create | RepoState struct + Store interface |
| `internal/store/memory/memory.go` | Create | In-memory map-backed Store |
| `internal/store/postgres/postgres.go` | Create | pgx-backed Store |
| `internal/store/postgres/migrate.go` | Create | golang-migrate runner |
| `internal/store/postgres/migrations/0001_init.{up,down}.sql` | Create | Schema |
| `internal/store/mocks/Store.go` | Create | Mockery-generated |
| `internal/queue/queue.go` | Create | Job struct + Queue interface |
| `internal/queue/memory/memory.go` | Create | Channel-backed Queue |
| `internal/queue/valkey/valkey.go` | Create | Valkey LIST + ZSET Queue |
| `internal/queue/valkey/reaper.go` | Create | In-flight reaper goroutine |
| `internal/queue/mocks/Queue.go` | Create | Mockery-generated |
| `internal/scheduler/scheduler.go` | Create | Scheduler interface |
| `internal/scheduler/ticker/ticker.go` | Create | time.Ticker-based Scheduler |
| `internal/scheduler/valkey/valkey.go` | Create | Valkey-lock Scheduler |
| `internal/scheduler/mocks/Scheduler.go` | Create | Mockery-generated |
| `internal/policy/version.go` | Create | `policyVersion()` SHA-256 helper |
| `internal/checker/sweep.go` | Create | Sweep handler using Store + Queue |
| `internal/checker/queue.go` | Modify | Workers consume from `Queue.Subscribe` |
| `internal/checker/engine_policy.go` | Modify | Call `Store.UpdateRepoState` after each check |
| `internal/scheduler/scheduler.go` (legacy) | Delete | Replaced by interface + impls |
| `internal/webhook/handler.go` | Modify | Enqueue + 202; remove inline engine call |
| `internal/config/config.go` | Modify | New env vars |
| `internal/metrics/metrics.go` | Modify | Register 10 new metrics |
| `charts/repo-guardian/Chart.yaml` | Modify | Bump to 0.5.0 |
| `charts/repo-guardian/values.yaml` | Modify | Add store/queue/scheduler/serviceMonitor/prometheusRule blocks |
| `charts/repo-guardian/templates/deployment.yaml` | Modify | Wire env vars + DSN secrets |
| `charts/repo-guardian/templates/store-postgres*.yaml` | Create | Baked Postgres + Secret |
| `charts/repo-guardian/templates/store-cnpg-cluster.yaml` | Create | CNPG `Cluster` CR |
| `charts/repo-guardian/templates/store-cnpg-pooler.yaml` | Create | CNPG `Pooler` CR |
| `charts/repo-guardian/templates/queue-valkey*.yaml` | Create | Baked Valkey + Secret |
| `charts/repo-guardian/templates/servicemonitor.yaml` | Create | Opt-in ServiceMonitor |
| `charts/repo-guardian/templates/prometheusrule.yaml` | Create | Opt-in starter alerts |
| `charts/repo-guardian/tests/*.yaml` | Create | helm-unittest cases for the 4 shapes |
| `charts/repo-guardian/README.md` | Modify | Document the 4 shapes |
| `docs/operations/scaling.md` | Create | Operator sizing guide |
| `docs/operations/migrations.md` | Create | Schema operations guide |
| `CLAUDE.md` | Modify | Architecture / patterns updates |

## Testing Plan

- [ ] Unit tests for all in-memory implementations (Phase 1).
- [ ] Unit tests for `policyVersion()` (Phase 1).
- [ ] Integration tests under `_integration` build tag for Postgres
  Store (Phase 2).
- [ ] Integration tests under `_integration` build tag for Valkey
  Queue + reaper (Phase 3).
- [ ] Integration tests for Valkey Scheduler leader election (Phase 4).
- [ ] Webhook ACK SLA test (Phase 5).
- [ ] End-to-end sweep test with metrics assertions (Phase 5).
- [ ] Contract test suite that runs against every backend pair
  (memory ↔ postgres for Store; memory ↔ valkey for Queue;
  ticker ↔ valkey for Scheduler) — establishes parity.
- [ ] Multi-replica concurrency test: N goroutines, 1000 jobs, no
  duplicates (Phase 7).
- [ ] Restart-safety test: kill Valkey, observe reaper recovery
  (Phase 7).
- [ ] Helm-unittest cases for each of the 4 deployment shapes
  (Phase 6).
- [ ] Homelab deploy + 3-replica leader failover (Phase 7).

## Dependencies

- DESIGN-0012 (this IMPL implements it).
- INV-0003 idempotent `CreateOrUpdateFile` already shipped in chart
  0.3.3 / appVersion 1.4.1 — prerequisite for safe multi-replica
  execution.
- INV-0004 `Provider` interface refactor — same interface-first
  principle, but lands on its own track. Not a blocker, but the two
  refactors should be coordinated to avoid touching the same engine
  code paths in conflicting ways.
- mockery v2 (already pinned in `mise.toml`).
- testcontainers-go (new dependency).
- pgx/v5 (new dependency).
- golang-migrate/migrate/v4 (new dependency).
- go-redis/v9 (new dependency).
- CNPG operator (cluster prereq for `store.postgres.mode=cnpg`; not a
  Helm sub-chart dependency).

## Open Questions

All resolved. Captured here for the audit trail.

1. **golang-migrate apply strategy.** **Resolved.** Embedded
   migrations applied in the binary at startup before any Store
   traffic. golang-migrate's `pg_advisory_lock` on
   `schema_migrations` serializes N-replica boot races cleanly —
   only one applies, others see "no change" and proceed. No helm
   pre-install Job (Argo+hooks awkward; rollback is harder when
   the migrate Job's lifecycle is decoupled from the app). CI
   already runs migrations against testcontainer Postgres, so
   broken migrations don't reach prod.
2. **Postgres image pinning.** **Resolved.** Pin to a specific
   minor (`postgres:16.4` style — exact tag chosen at write time
   based on what's current). Values-overridable via
   `store.postgres.baked.image`. Reasoning: floating major tag
   makes chart upgrades non-reproducible across patch releases;
   digest pinning is overkill churn for a quickstart image
   that's not the prod-scale path anyway.
3. **Valkey image pinning.** **Resolved.** Pin to a specific
   minor (`valkey/valkey:8.0` style). Same reasoning as Q2;
   override via `queue.valkey.baked.image`.
4. **`policy_version` hash input.** **Resolved.** Hash
   `(PolicyConfig + TemplateStore content)`. The hash MUST
   change when any rule the engine evaluates changes — including
   template content for `contains` and `exact` rules where a
   template change makes existing files fail assertions.
   Hash-only-PolicyConfig misses template edits; hashing raw HCL
   bytes breaks under multi-file HCL config. Implementation:
   SHA-256 of canonical-JSON-marshaled `PolicyConfig` followed
   by `(name, content)` pairs from the TemplateStore in sorted
   order. Env-var overrides also flow through because they're
   applied to `PolicyConfig` before the hash.
5. **Worker package location.** **Resolved.** Rename
   `internal/checker/queue.go` to `internal/worker/worker.go`
   as part of Phase 1. Workers were colocated in `checker`
   only because they were in-process consumers of an in-process
   channel — once the queue is its own interface with multiple
   backends, workers want their own home. Doing the rename in
   Phase 1 avoids a churnful "moved 3 files, no behavior change"
   follow-up PR.
6. **Mockery output convention.** **Resolved.** Option (a) —
   `internal/<pkg>/mocks/<Interface>.go`. Mockery v2 default;
   keeps test-double imports obvious at the call site
   (`mocks.Store`); doesn't pollute the parent package's
   compilation.
7. **Queue claim atomicity.** **Resolved.** Lua script for the
   BRPOP→ZADD claim. Pattern: blocking `BRPOP` from
   `queue:jobs`, then a tiny `EVAL` script does `LPUSH` back +
   `ZADD in-flight` atomically (`BRPOP` doesn't compose with
   Lua's no-blocking-ops rule). Sub-millisecond atomicity
   window. `EVALSHA` cached after first call. Document the
   script in `internal/queue/valkey/`.
8. **Chart version bump.** **Resolved.** `0.4.x`
   (post-IMPL-0012) → `0.5.0` (MINOR). Pre-1.0 chart, SemVer
   pre-1.0 allows breaking changes in MINOR, and the legacy
   `memory + memory + ticker` shape is preserved via opt-in
   values. 1.0.0 stays reserved for the chart's API stability
   moment (Provider refactor + multi-replica battle-tested + ops
   surface stable). **Note**: IMPL-0011 ships AFTER IMPL-0012 per
   the revised release order (smaller cycle time + lower blast
   radius for IMPL-0012 made it the right thing to ship first).
9. **Backward compat for existing in-memory deployments.**
   **Resolved.** Flip the chart default to `baked` (option a).
   Document the legacy opt-in clearly in
   `charts/repo-guardian/CHANGELOG.md`:

   > **Behavior change**: chart 0.5.0 defaults to baked
   > Postgres + Valkey (`store.backend=postgres`,
   > `queue.backend=valkey`). To preserve the previous
   > in-memory single-replica behavior, set
   > `store.backend=memory`, `queue.backend=memory`,
   > `scheduler.backend=ticker` in your values.

   Belt + suspenders: the chart README's "Choosing a deployment
   shape" section (Phase 6 task) lists no-dep mode as the first
   row, so the legacy path is right there for anyone who needs
   it.
10. **Per-installation rate-limit header capture.** **Resolved.**
    Wrap the ghinstallation transport with a metrics
    `RoundTripper` that captures `X-RateLimit-Remaining` from
    every response and writes
    `repo_guardian_rate_limit_remaining{installation_id}`. Lives
    at `internal/github/ratelimit_transport.go` and composes
    with the existing ghinstallation transport. Single capture
    point covers every call site without per-call discipline.
11. **Worker shutdown semantics on SIGTERM.** **Resolved.**
    Nack-and-requeue at SIGTERM so siblings pick up without
    waiting for `JOB_ACK_TIMEOUT` (5min). Sequence: stop
    claiming new jobs → for each in-flight engine call, wait up
    to `(terminationGracePeriodSeconds - 10s)` for completion
    and ack on success → for any remaining in-flight jobs,
    `LPUSH queue:jobs` + `ZREM queue:in-flight` → close redis
    client → exit. Set chart
    `terminationGracePeriodSeconds: 60`. SIGKILL fallback
    handled by the reaper. Engine idempotency makes any
    eventual double-execution safe.

## References

- DESIGN-0012 — the design this implements.
- INV-0003 — idempotent `CreateOrUpdateFile` (prerequisite).
- INV-0004 — Provider interface refactor (same interface-first
  pattern; tracked separately).
- IMPL-0010 — chart 0.3.3 publish workflow (baseline this work
  extends).
- pgx/v5: <https://github.com/jackc/pgx>
- golang-migrate: <https://github.com/golang-migrate/migrate>
- go-redis/v9: <https://github.com/redis/go-redis>
- testcontainers-go: <https://golang.testcontainers.org/>
- mockery v2: <https://vektra.github.io/mockery/>
- CloudNativePG: <https://cloudnative-pg.io/documentation/current/>
- Valkey: <https://valkey.io>
- server-price-tracker chart (CNPG pattern):
  <https://github.com/donaldgifford/server-price-tracker/tree/main/charts/server-price-tracker>
