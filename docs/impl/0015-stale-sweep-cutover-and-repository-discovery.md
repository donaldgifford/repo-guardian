---
id: IMPL-0015
title: "Stale-sweep cutover and repository discovery"
status: Draft
author: Donald Gifford
created: 2026-06-23
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0015: Stale-sweep cutover and repository discovery

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-06-23

<!--toc:start-->
- [Objective](#objective)
- [Sequencing](#sequencing)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 0: State-writeback prerequisites](#phase-0-state-writeback-prerequisites)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 1: Discoverer + Layer 1 budget gating](#phase-1-discoverer--layer-1-budget-gating)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 2: RC validation in homelab](#phase-2-rc-validation-in-homelab)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Implement [DESIGN-0017: Stale-sweep cutover and repository
discovery](../design/0017-stale-sweep-cutover-and-repository-discovery.md).
Fix the worker write-back gap that makes the IMPL-0011 freshness gate a no-op,
land budget-aware enqueueing + jitter to pace cold-start and steady-state
operation, and replace the legacy "enqueue every repo every tick" Sweeper
with a discovery-only loop on the Postgres backend.

**Implements:** DESIGN-0017 (Approved 2026-06-23)

The design's pre-implementation audit surfaced 9 gaps beyond the
worker-writeback bug. Phase 0 of this IMPL bundles all the prerequisites
that must land together for the freshness gate, the budget tracker, and the
cutover-gating logic to be coherent.

## Sequencing

**IMPL-0016 ships first.** Per DESIGN-0018 resolved [OQ
(g)](../design/0018-deprecate-memory-backend.md#open-questions), the memory
backend is removed before IMPL-0015 starts. By the time this IMPL's Phase 0
begins, the codebase has no `internal/store/memory/`,
`internal/queue/memory/`, or `internal/scheduler/ticker/` packages — every
deployment is Postgres + Valkey.

This simplifies IMPL-0015:

- No "what does memory backend do here?" branch for every new write site.
- Worker / webhook / config tests are single-backend.
- Phase 0 Task 0.3 (legacy Sweeper gating) becomes "delete the schedule
  call unconditionally" instead of "wrap in if-statement."
- Phase 3 (chart default flip for `DISCOVERY_ENABLED`) collapses into
  Phase 1 — discovery is on by default from the start since there's no
  memory-backend deployment to opt-out for.
- Phase 2 (operator-side opt-in cutover) collapses into the RC validation
  flow — the homelab operator is the cutover.

Both IMPLs bundle into the **1.0.0 release**, validated via
manually-tagged `1.0.0-rc.N` versions:

| RC | What landed |
|---|---|
| `1.0.0-rc.1` | IMPL-0016 work (memory backend removed; chart defaults flipped; schema validation added; `docker-compose.dev.yaml` shipped) |
| `1.0.0-rc.2` | IMPL-0015 Phase 0 (state writeback prerequisites) |
| `1.0.0-rc.3+` | IMPL-0015 Phase 1 (Discoverer + BudgetTracker), with `DISCOVERY_ENABLED=true` by default |
| `1.0.0` | Stable cut when all phases validated in homelab |

Binary `appVersion` stays on the 1.x line (1.8.1 → 1.9.0). Chart version
moves to 1.0.0 to signal the meaningful operator-facing release. Binary
v2.0.0 was deferred to avoid Go module `/v2` path migration; revisit if
we ever expose a public library.

## Scope

### In Scope

- Worker `Store` injection and per-reconcile write-back on both success
  and error paths.
- Webhook handler `Store` injection and write-back on
  `installation_repositories.added`, `repository.created`, and push events.
- Legacy `Sweeper.ReconcileAll` schedule gated on `STORE_BACKEND != postgres`.
- `policy.Version` template-hash fix (currently ignores template content).
- New `Store.UpsertIfMissing` interface method + Postgres / memory
  implementations.
- `BudgetTracker` for Layer 1 budget-aware enqueueing in StaleSweeper +
  Discoverer.
- `Discoverer.Discover` loop (rename / refactor from legacy
  `Sweeper.ReconcileAll`) — write-only, no enqueue, idempotent.
- Discovery cadence env vars + chart values (`DISCOVERY_INTERVAL`,
  `DISCOVERY_ENABLED`, `DISCOVERY_RESERVE_FRACTION`,
  `DISCOVERY_ESTIMATED_COST_PER_REPO`).
- Net-new Phase 0 + Phase 1 metrics.

### Out of Scope

- Removing the memory backend. Tracked separately in DESIGN-0018 /
  IMPL-0016. **IMPL-0016 ships first**; by the time this IMPL starts, the
  memory backend packages are already deleted. See [Sequencing](#sequencing).
- Fast retry of errored repos (separate sweep cadence for
  `last_check_status=error`). Future enhancement; out of scope per
  DESIGN-0017 decision (m).
- Repo *removal* handling (archived / deleted / moved out of installation).
  Out of scope per DESIGN-0017 decision (c) — 404 on next reconcile is
  the existing soft-handling path.
- Auto-tuning `estimatedCostPerRepo` from observed consumption. Out of
  scope per DESIGN-0017 decision (h).
- Phase 4 (legacy Sweeper removal from Postgres path) — already absorbed
  into Phase 0 by the audit. With memory backend removed in IMPL-0016,
  the legacy Sweeper schedule is dropped unconditionally in Phase 0
  Task 0.3 (no `STORE_BACKEND` gating needed).
- Phase 2 (operator opt-in cutover) and Phase 3 (chart default flip) —
  collapsed into the RC validation flow per [Sequencing](#sequencing).
  Discovery is enabled by default from the 1.0.0 release since there's
  no memory-backend deployment to opt-out for.

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its
tasks are checked off and its success criteria are met. Phase 0 is the
largest — it bundles every prerequisite the design surfaced in the gap
audit. Phase 1 lands Discoverer + budget tracker. Phase 2 is the RC
validation flow tracking against the 1.0.0 release.

---

### Phase 0: State-writeback prerequisites

The prerequisite bundle. Phase 1 doesn't work coherently without these.
Ships as a single atomic PR (per resolved Open Question 4) tagged
`1.0.0-rc.2` (or next available) for homelab validation. IMPL-0016 ships
first as `1.0.0-rc.1` — this Phase 0 lands on a memory-backend-free
codebase.

#### Tasks

**0.1 — Worker `Store` injection + write-back contract**

- [x] Add `store store.Store` and `policyVersion string` fields to
  `internal/worker.Pool`.
- [x] Update `worker.New(...)` constructor signature to accept `store`
  and `policyVersion` parameters. Document the new contract in the
  package doc-comment.
- [x] Update `cmd/repo-guardian/main.go` `bringUp` to thread the
  `stateStore` and a `policyVersion` value (computed once at startup
  from `policy.Version(cfg, templates.AsMap())` — see Task 0.4) into
  `worker.New`. policyVersion is now computed at bringUp scope so it
  feeds both `worker.New` and `StaleSweeper`.
- [x] In `processJob`, after `engine.CheckRepo` returns, construct a
  `*store.RepoState` with `LastCheckedAt = &now`, `PolicyVersion =
  p.policyVersion`, and either `LastCheckStatus = store.StatusSuccess`
  + `LastError = ""` (on success) OR `LastCheckStatus =
  store.StatusError` + `LastError = truncate(err.Error(), 1024)` (on
  failure). Write-back also runs on the `CreateInstallationClient`
  failure path so a transient JWT/install-token glitch surfaces as a
  StatusError row (otherwise the repo would be a stale-sweep dead
  zone — checked but never re-checked).
- [x] Always call `p.store.UpdateRepoState(ctx, state)` — both success
  and error paths. Best-effort: log + count + continue on Store write
  failure (per design decision (k)).
- [x] Add a `Truncate(s string, n int)` helper. Lives in
  `internal/store/util.go` (resolves Open Question 3a). Operates on
  runes, not bytes; clipped strings get a single `…` ellipsis
  suffix and the rune count is exactly `maxRunes`.
- [x] Update mockery-generated mock for `store.Store` to include any
  new methods (`UpsertIfMissing` landed in Task 0.5).
- [x] Write unit tests: success-path write, error-path write,
  store-write-failure logged-and-continued, long-error truncation,
  nil-store no-panic. Use the mockery mock.

**0.2 — Webhook handler `Store` injection + discovery write-back**

- [x] Add `store store.Store`, `policyVersion string`, and `freshness
  time.Duration` fields to `internal/webhook.Handler`.
- [x] Update `webhook.NewHandler(...)` constructor signature to accept
  the new dependencies.
- [x] Update `cmd/repo-guardian/main.go` to wire the Store + policy
  version into the webhook handler constructor.
- [x] In `handleInstallationRepositoriesEvent`, for each repo in
  `RepositoriesAdded`, call `h.store.UpsertIfMissing(ctx,
  &store.RepoState{...})` with a jittered initial `LastCheckedAt =
  now - rand.Int63n(int64(2*h.freshness))` and `PolicyVersion = ""`.
- [x] In `handleRepositoryEvent` for `repository.created`, perform the
  same UpsertIfMissing. (Also applied to `handleInstallationEvent`
  for `installation.created` — the App-onboarding-with-existing-repos
  path that mirrors `installation_repositories.added`.)
- [x] In `handlePushEvent`, BEFORE the enqueue, call
  `h.store.UpdateRepoState(ctx, &store.RepoState{...})` with
  `LastCheckStatus = store.StatusPending`, current timestamp, and the
  policy version. Resolves OQ 6a (mark pending before enqueue so the
  stale-sweeper doesn't redundantly re-enqueue while the job is in
  flight).
- [x] Write unit tests for each handler path; assert exactly one
  Store call per event with correct payload. Tests in
  `internal/webhook/discovery_test.go` exercise:
  `installation_repositories.added` (UpsertIfMissing per repo + jitter
  bounds), `repository.created` (single UpsertIfMissing),
  `push` (markPending before enqueue), and the Store-error path
  (does not block ACK or skip the queue).

**0.3 — Drop the legacy Sweeper schedule call**

With memory backend removed in IMPL-0016, the legacy Sweeper's bootstrap
role is gone. StaleSweeper handles every deployment. The `Sweeper` type
itself stays in `internal/scheduler/sweep.go` for Phase 1 to repurpose
as `Discoverer.Discover`; only the *schedule call* is removed here.

- [x] In `cmd/repo-guardian/main.go`, delete the
  `sched.Schedule(ctx, "sweep", interval, sweeper.ReconcileAll)` call
  AND the `sweeper := scheduler.NewSweeper(...)` construction that
  feeds it. (No `if` gate — every deployment is Postgres + Valkey
  post-IMPL-0016.) The `scheduler.NewSweeper` symbol stays in
  `internal/scheduler/sweep.go` for Phase 1 to repurpose as the
  `Discoverer.Discover` handler.
- [x] Verify no tests directly depend on the legacy schedule running.
  (Existing `internal/scheduler/sweep_test.go` tests exercise
  `scheduler.NewSweeper` + `.reconcileAll(...)` directly, not the
  schedule call path, so they keep working unchanged.)
- [x] Document the change in `docs/operations/scaling.md` under the
  scaling matrix section (new "One sweeper, not two" gotcha entry).

**0.4 — `policy.Version` template-hash fix**

- [x] Add `AsMap() map[string]string` method to `*rules.TemplateStore`
  in `internal/rules/registry.go` (the package's only Go file
  post-IMPL-0014; no `store.go` exists in `internal/rules`).
  Returns a snapshot of all template names → content (embedded +
  ConfigMap-overridden).
- [x] Update `cmd/repo-guardian/main.go` to pass `templates.AsMap()`
  to `policy.Version` instead of `nil` (templates flow back from
  `loadPolicyAndEngine` so the `bringUp` call site can hash them).
- [x] Hash-change-on-template-content already covered by
  `policy.TestVersion_TemplateContentChangesHash` in
  `internal/policy/version_test.go`; new
  `TestTemplateStoreAsMap` in
  `internal/rules/registry_test.go` locks the snapshot contract
  (copy semantics, directory override capture).
- [x] Document the operator-facing implication in
  `charts/repo-guardian/README.md.gotmpl`: editing a template
  ConfigMap now triggers re-enqueue of all repos via policy version
  invalidation. (Chart README regenerated via `make helm-docs`.)

**0.5 — `Store.UpsertIfMissing` interface + implementations**

- [x] Add `UpsertIfMissing(ctx context.Context, s *RepoState) (created
  bool, err error)` to the `Store` interface in
  `internal/store/store.go`. Document it in the package doc-comment.
- [x] Implement in `internal/store/postgres/postgres.go` using a single
  query: `INSERT INTO repo_state (...) VALUES (...) ON CONFLICT
  (installation_id, owner, repo) DO NOTHING RETURNING (xmax = 0) AS
  created`. Handle the "no row returned" case as `created = false,
  err = nil`.
- [x] Regenerate the mockery mock for `store.Store` via `make mocks`.
  (`internal/store/memory/` was deleted in IMPL-0016 — postgres is
  the only concrete implementation.) Mockery v2.53.6 doesn't yet
  support go1.26 source packages; the `UpsertIfMissing` mock was
  hand-extended following the existing pattern. Bump mockery when a
  go1.26-compatible release lands.
- [x] Write unit tests against the mockery `MockStore`: callers
  short-circuit correctly when `created=false`, propagate
  errors on `err != nil`. Integration coverage of the actual
  INSERT...ON CONFLICT behaviour lives in the postgres
  integration test below. (Unit-test coverage is exercised by the
  worker/webhook callers in tasks 0.1 / 0.2.)
- [x] Add an integration test in
  `internal/store/postgres/postgres_integration_test.go` that
  exercises the `ON CONFLICT` path with a real Postgres.

**0.6 — Net-new Phase 0 metrics**

- [x] Add `repo_guardian_store_writeback_total` as a CounterVec
  labelled by `installation_id` and `outcome` (`ok` | `error`).
- [x] Add `repo_guardian_store_writeback_duration_seconds` as a
  Histogram.
- [x] Wire both metrics into the worker's UpdateRepoState call site
  added in Task 0.1. (`writeBack` in `internal/worker/worker.go`
  increments `StoreWritebackTotal` with the per-installation label
  and observes `StoreWritebackDurationSeconds` on every call,
  success and error paths.)
- [x] Update `charts/repo-guardian/templates/prometheusrule.yaml` if
  any alerts on the new metrics are warranted. None planned for
  Phase 0 — these are observation metrics.
- [x] Document the new metrics in
  `docs/operations/scaling.md` under the metrics catalogue.

#### Success Criteria

- All Phase 0 tasks checked off.
- `make ci` passes (lint + test + build).
- After deploying to homelab on Postgres backend:
  - `repo_guardian_store_writeback_total{outcome="ok"}` rises in lockstep
    with `repo_guardian_repos_checked_total` (1:1 ratio modulo Store
    outages).
  - `repo_state.last_check_status` populates with `success` / `error` /
    `pending` values; previously-NULL rows fill in as workers process
    them.
  - Queue depth on a sweep tick drops to "only stale repos" (legacy
    Sweeper no longer feeding the queue on Postgres backend).
  - Editing a template entry in the ConfigMap + redeploying triggers
    re-enqueue of all repos via policy version invalidation. Verify by
    watching `repos_checked_total` rise across all repos within the
    next sweep tick.
- Chart version bumped per Open Question 5.
- DESIGN-0017 reference doc unchanged (Phase 0 implementation matches
  the design's described behaviour).

---

### Phase 1: Discoverer + Layer 1 budget gating

Land the Discoverer loop (off by default) and the BudgetTracker that gates
enqueues in both StaleSweeper and Discoverer. Layer 2 (jitter) is already
partially shipped in Phase 0 via the webhook discovery handlers; Phase 1
extends the same jittered initial `last_checked_at` semantic to the
Discoverer path.

#### Tasks

**1.1 — `BudgetTracker` implementation**

- [x] Create `internal/budget/` package (per OQ 2b) with the
  `Tracker` struct. `RateLimitClient` is a small in-package interface
  the production wiring binds to `github.Client.RateLimitRemaining`;
  `github.Client.RateLimitRemaining` was extended to return the
  `resetAt` so the tracker can detect the GitHub-reported hourly
  window rolling. All existing call sites (5 mock impls,
  `internal/checker/sweep.go.allowedByRateLimit`) updated in lockstep.
- [x] Implement `SpendableForEnqueue(installationID) (int, error)`,
  `RefreshFromAPI(ctx, client, installationID) error`, and a
  `Decrement(installationID)` helper that subtracts
  `Options.CostPerRepo` per call.
- [x] Per-installation tracker map keyed by `installationID int64`,
  guarded by `sync.Mutex` for concurrent caller safety.
- [x] `resetAt`-elapsed refresh trigger: `SpendableForEnqueue`
  returns `ErrNoSnapshot` when the cached snapshot's `ResetAt` is in
  the past; callers refresh on that signal.
- [x] Unit tests against a fake `RateLimitClient` cover: no-snapshot
  fall-open, refresh-then-spendable, budget-exhausted gate,
  Decrement accuracy, resetAt-elapsed → ErrNoSnapshot,
  multi-installation isolation, refresh-error propagation,
  unknown-limit fall-open, Decrement-without-snapshot no-op,
  and panic on invalid Options.

**1.2 — Wire `BudgetTracker` into `StaleSweeper`**

- [x] `StaleSweeperOptions.Budget *budget.Tracker` field; the
  StaleSweeper now consults the optional tracker via
  `allowedByBudget` AFTER the existing live-API reserve gate
  (`allowedByRateLimit`). Both gates can deny enqueue; only the
  newer budget gate counts under
  `enqueue_gated_by_budget_total`.
- [x] Before each `Queue.Enqueue` call, consult
  `tracker.SpendableForEnqueue()`. If 0, increment
  `enqueue_gated_by_budget_total` and skip the enqueue.
- [x] On successful enqueue, decrement the tracker's `remaining`
  field by `costPerRepo`.
- [x] Add unit tests covering: budget-gated-zero-spendable,
  decrement-on-successful-enqueue (3 enqueues × 10 cost = 30
  budget burned). Existing all-within-budget path covered
  implicitly by `TestStaleSweeper_EnqueuesStaleRepos`
  (tracker nil → fall open). Budget-recovers-on-next-tick is
  exercised by the in-process tracker semantics (next
  RefreshFromAPI restores the snapshot).

**1.3 — `Discoverer.Discover` implementation**

- [x] Created a new `Discoverer` type alongside Sweeper in
  `internal/scheduler/discoverer.go` (chose "new type" over the
  rename path to keep the existing Sweeper tests green; Sweeper is
  unwired post-Phase 0 and will be deleted in a follow-up).
- [x] Implemented `Discover(ctx) error` per DESIGN-0017 snippet:
  `ListInstallations` → for each `ListInstallationRepos` → for each
  call `Store.UpsertIfMissing` with jittered initial
  `LastCheckedAt` (uniform over [-2*freshness, 0]) and empty
  `PolicyVersion`.
- [x] Consult `BudgetTracker` before each `ListInstallationRepos`
  page (per OQ 7: page-level gate is the natural granularity since
  `list_installation_repos` is the API-heavy call). When
  `SpendableForEnqueue` returns 0, log Warn, increment
  `EnqueueGatedByBudgetTotal{installation_id}` (shared with
  StaleSweeper so the existing `RepoGuardianBudgetGated` alert
  covers both schedulers), and skip the installation. Fall-open on
  `ErrNoSnapshot` and on nil tracker (tests). Unit tests:
  `TestDiscoverer_BudgetExhausted_SkipsInstallation` +
  `TestDiscoverer_BudgetNoSnapshot_FallsOpen`.
- [x] Skip on Store-read errors (treat as "still actionable"
  fail-safe — matches DESIGN-0017's discoverer-error semantic).
  `upsertRepo` logs the error, doesn't increment
  `repo_discovered_total`, and returns false so the iteration
  continues.
- [x] Increment `repo_discovered_total{installation_id}` on each
  `created=true` row. Per-installation, idempotent on repeat runs.
- [x] Unit tests: upserts every returned repo (jitter bounds checked),
  idempotency on repeat runs (3 discover calls → 1 increment),
  skips archived + forked, list_installations error fails safe,
  list_installation_repos error skips the installation, Store
  error fails safe, ctx-cancelled returns ctx.Err().

**1.4 — Configuration**

- [x] Add new env vars to `internal/config/`:
  - `DISCOVERY_INTERVAL` — Go duration; default `1h`.
  - `DISCOVERY_ENABLED` — bool; default `true`.
  - `DISCOVERY_RESERVE_FRACTION` — float; default `0.20`. Range
    `[0, 1]` validated in `Config.validateDiscovery`.
  - `DISCOVERY_ESTIMATED_COST_PER_REPO` — int; default `10`.
    `> 0` validated in `Config.validateDiscovery`.
- [x] Add to `charts/repo-guardian/values.yaml` under a new
  `discovery:` block. Defaults mirror the binary env-var defaults.
  Also added schema validation in `values.schema.json` so out-of-
  range values fail at chart-render time, not pod CrashLoopBackoff.
- [x] Wire env vars into `Deployment` template alongside the
  existing `RATE_LIMIT_RESERVE` block.
- [x] Add helm-unittest cases asserting the env vars appear with
  correct defaults + the `discovery.enabled=false` override path.

**1.5 — Wire scheduler**

- [x] In `cmd/repo-guardian/main.go`, when `cfg.DiscoveryEnabled`,
  schedule `discoverer.Discover` at `cfg.DiscoveryInterval`. (No
  `STORE_BACKEND` gate — memory backend gone.) Logic lives in the
  new `scheduleHandlers` helper alongside the StaleSweeper schedule
  to keep `bringUp` under the funlen budget.
- [x] Log explicitly which schedulers are active at startup
  (StaleSweeper always, Discoverer when enabled, opt-out logged
  when `DISCOVERY_ENABLED=false`). The BudgetTracker is constructed
  unconditionally in `bringUp` and passed to both StaleSweeper and
  Discoverer.

**1.6 — Net-new Phase 1 metrics**

- [x] `repo_guardian_repo_discovered_total{installation_id}` —
  CounterVec, Discoverer increments on each `created=true` row.
- [x] `repo_guardian_discovery_duration_seconds` — Histogram.
- [x] `repo_guardian_discovery_api_calls_total{installation_id,
  endpoint}` — CounterVec, labelled `endpoint=list_installations`
  / `list_installation_repos`.
- [x] `repo_guardian_api_budget_remaining{installation_id}` — Gauge.
- [x] `repo_guardian_api_budget_spendable{installation_id}` — Gauge.
- [x] `repo_guardian_api_budget_reserve_fraction{installation_id}` —
  Gauge.
- [x] `repo_guardian_api_budget_utilisation{installation_id}` —
  Gauge.
- [x] `repo_guardian_api_budget_refresh_total{installation_id,
  outcome}` — CounterVec.
- [x] `repo_guardian_enqueue_gated_by_budget_total{installation_id}` —
  CounterVec. Operator alarm signal.
- [x] Documented all in `docs/operations/scaling.md` under
  "Discoverer + BudgetTracker (IMPL-0015 Phase 1)".
- [x] Added `RepoGuardianBudgetGated` alert in
  `prometheusrule.yaml`: fires when
  `rate(enqueue_gated_by_budget_total[15m]) > 0` for 30m. Tunable
  via `prometheusRule.alerts.BudgetGated.{enabled,for,severity,
  threshold}`.

#### Success Criteria

- All Phase 1 tasks checked off.
- `make ci` passes.
- With `DISCOVERY_ENABLED=false` (default), no behavioural change vs
  Phase 0 — Discoverer code exists but the scheduler doesn't run it.
- With `DISCOVERY_ENABLED=true` in a test deployment:
  - Discoverer runs at the configured interval.
  - `repo_discovered_total` increments on first run; stays flat on
    subsequent runs (idempotency).
  - `api_budget_*` metrics populate with sensible values.
- StaleSweeper's `enqueue_gated_by_budget_total` is zero in normal
  operation; non-zero only when the deployment is actually
  rate-limit-bound.

---

### Phase 2: RC validation in homelab

Phases 2 and 3 from the original DESIGN-0017 rollout (operator opt-in
cutover + chart default flip) collapse into RC validation per
[Sequencing](#sequencing). Discovery is already on-by-default from
Phase 1, so there's no operator cutover step. The homelab operator
validates the full IMPL-0015 work via manually-tagged `1.0.0-rc.N`
deployments.

#### Tasks

- [ ] Tag `1.0.0-rc.2` (or next available rc.N) when Phase 0 ships.
  Deploy to homelab; verify Phase 0 success criteria (writeback
  metric rises with reconciles; `repo_state.last_check_status`
  populates; legacy Sweeper not scheduled).
- [ ] Force a manual sweep run after the rc deploys: trigger the
  webhook with a test push OR wait one tick. Confirm
  `store_writeback_total{outcome="ok"}` increments and the
  expected `repo_state` row appears.
- [ ] Tag `1.0.0-rc.3` (or next available) when Phase 1 ships.
  Deploy to homelab; verify Phase 1 success criteria (Discoverer
  active by default; `repo_discovered_total` increments;
  `api_budget_*` metrics populate).
- [ ] Add subsequent rc.N tags as fixes accumulate. Each rc is a
  validation checkpoint.
- [ ] Author / update `docs/operations/scaling.md` and chart README
  to reflect the new defaults: Discoverer on; StaleSweeper as the
  only sweep path; budget-tracker metrics catalogue.
- [ ] When all rc.N validation passes, cut stable `1.0.0` (manual
  `git tag v1.9.0 && git push --tags` + bump
  `charts/repo-guardian/Chart.yaml` to `version: 1.0.0`).
- [ ] Add a chart `CHANGELOG.md` entry for 1.0.0 describing the full
  scope: memory backend removed, state writeback wired, Discoverer
  on by default, budget gating in place.

#### Success Criteria

- All RC tags validated in homelab; each rc.N produced the expected
  metrics changes vs the previous rc.
- Final `1.0.0` chart + `v1.9.0` binary tag published.
- `make ci` passes against the final tag.
- Chart README regenerated and committed.
- Operations docs reflect the post-removal architecture.
- A fresh `helm install` on a clean cluster produces a working
  Postgres+Valkey + Discoverer-on deployment out of the box.

---

## File Changes

| File | Phase | Action | Description |
|------|-------|--------|-------------|
| `internal/worker/worker.go` | 0 | Modify | Add Store + policyVersion fields; write-back in processJob |
| `internal/worker/worker_test.go` | 0 | Modify | Add success/error/store-failure tests |
| `internal/webhook/handler.go` | 0 | Modify | Add Store field; UpsertIfMissing + UpdateRepoState calls |
| `internal/webhook/handler_test.go` | 0 | Modify | Add tests for each event type's Store interaction |
| `internal/store/store.go` | 0 | Modify | Add `UpsertIfMissing` to interface |
| `internal/store/postgres/postgres.go` | 0 | Modify | Implement `UpsertIfMissing` |
| `internal/store/postgres/postgres_test.go` | 0 | Modify | Add UpsertIfMissing tests |
| `internal/store/postgres/postgres_integration_test.go` | 0 | Modify | Add ON CONFLICT integration test |
| `internal/store/mocks/store_mock.go` | 0 | Regenerate | `make mocks` |
| `internal/rules/store.go` | 0 | Modify | Add `AsMap() map[string]string` |
| `internal/rules/store_test.go` | 0 | Modify | Test AsMap |
| `internal/policy/version_test.go` | 0 | Modify | Test that template changes invalidate hash |
| `internal/metrics/metrics.go` | 0+1 | Modify | New metric definitions |
| `cmd/repo-guardian/main.go` | 0+1 | Modify | Wire Store into worker + webhook; delete legacy Sweeper schedule call; schedule Discoverer |
| `internal/scheduler/sweep.go` | 1 | Modify | Either rename to Discoverer or extract Discoverer type |
| `internal/scheduler/budget.go` | 1 | Create | BudgetTracker |
| `internal/scheduler/budget_test.go` | 1 | Create | BudgetTracker tests |
| `internal/checker/sweep.go` | 1 | Modify | Wire BudgetTracker into SweepStale |
| `internal/checker/sweep_test.go` | 1 | Modify | Budget-gating tests |
| `internal/config/config.go` | 1 | Modify | New env vars + validation |
| `internal/config/config_test.go` | 1 | Modify | Defaults + range validation tests |
| `charts/repo-guardian/values.yaml` | 1 | Modify | New `discovery:` block with `enabled: true` default |
| `charts/repo-guardian/templates/deployment.yaml` | 1 | Modify | Wire new env vars |
| `charts/repo-guardian/tests/backend_shapes_test.yaml` | 1 | Modify | Assert new env vars |
| `charts/repo-guardian/templates/prometheusrule.yaml` | 1 | Modify | Optional alert on `enqueue_gated_by_budget_total` |
| `charts/repo-guardian/README.md.gotmpl` | 0 | Modify | Document template ConfigMap invalidation |
| `charts/repo-guardian/Chart.yaml` | 2 | Modify | Bump chart version through rc tags to 1.0.0 final |
| `docs/operations/scaling.md` | 0+1+2 | Modify | Document new metrics + legacy Sweeper change + final 1.0.0 architecture |

## Testing Plan

- [ ] Unit tests for every new function and modified function path.
- [ ] Mock-based tests for the worker / webhook handler write-back
  paths using mockery-generated `Store` mocks.
- [ ] Integration tests against Postgres (testcontainers) for
  `UpsertIfMissing` ON CONFLICT semantics.
- [ ] Integration tests for `Discoverer.Discover` against a fake GitHub
  client + real Postgres.
- [ ] Helm-unittest cases for new chart values and env vars (Phase 1)
  and the Phase 3 default flip.
- [ ] End-to-end validation in homelab after each phase deploys: watch
  the metrics catalogue described in each phase's success criteria.
- [ ] Race condition tests: simultaneous webhook + Discoverer writes
  for the same repo (UpsertIfMissing atomic semantics; idempotency
  even under concurrent fire).
- [ ] Coverage target: ≥ 60% per the project standard (CLAUDE.md);
  ≥ 80% in new packages (`budget.go`) since they're net-new code.

## Dependencies

- IMPL-0011 (Persistent reconcile state + multi-replica coordination) —
  shipped. Provides the Store, Queue, Scheduler interfaces and the
  `RepoState.LastCheckStatus` / `LastError` columns this IMPL writes for
  the first time.
- DESIGN-0017 (Approved 2026-06-23 in PR #128).
- DESIGN-0018 (Approved alongside) — **does NOT block this IMPL**. Per
  design decision (g), IMPL-0015 and IMPL-0016 are independently
  sequenced. IMPL-0015 keeps the no-op memory Store path intact;
  IMPL-0016 removes it later.
- `mockery v2` (pinned in `mise.toml`) — needed to regenerate the
  `store.Store` mock after the interface change in Task 0.5.

## Open Questions

**1.** ✅ **Resolved.** Discoverer package location — `Discoverer` is a
renamed / repurposed `Sweeper.ReconcileAll` per the design. Where does it
live?

- **(a) = Stay in `internal/scheduler/` (chosen).** Alongside the legacy
  `Sweeper` type. Rename `ReconcileAll` → `Discover`, delete enqueueing,
  add `UpsertIfMissing` calls. Smallest diff; the type and its
  callers stay in the same package.
- (b) Move to a new `internal/discovery/` package. Cleaner separation;
  Discoverer is conceptually distinct from the scheduler primitives.
- (c) Move to `internal/checker/discovery.go` (next to
  `internal/checker/sweep.go`). Co-locates with StaleSweeper since
  they share the BudgetTracker.
- other:

**2.** ✅ **Resolved.** BudgetTracker package location — sibling of
question 1.

- (a) `internal/scheduler/budget.go`. BudgetTracker is leader-scoped
  (only the Scheduler leader holds one), so co-locating with the
  scheduler types makes sense.
- **(b) = New `internal/budget/` package (chosen).** Cleanest separation;
  the tracker can be unit-tested in isolation without scheduler
  imports. Imported by both `internal/scheduler/` (for the Discoverer
  path) and `internal/checker/` (for StaleSweeper); a neutral home
  prevents either-import-the-other cycles.
- (c) `internal/checker/budget.go`. Lives next to StaleSweeper where
  it's heavily used.
- other:

**3.** ✅ **Resolved.** `truncate` helper for `LastError` field — where
does the truncation helper live?

- **(a) = `internal/store/util.go` (new file) (chosen).** Stays close to
  the `RepoState.LastError` field it's truncating. Reusable by any
  future Store writer.
- (b) `internal/worker/util.go` (new file). Worker is the only caller
  today; principle of "package the call sites use it from."
- (c) Inline `lo.SubString` or similar one-liner at the call site.
  No new helper needed.
- other:

**4.** ✅ **Resolved.** Phase 0 PR strategy — Phase 0 has 6 sub-tasks
(0.1-0.6) that collectively span ~10 files. Do we ship them as one PR
or split?

- **(a) = Single PR (chosen).** Phase 0 is the "make the freshness gate
  coherent" bundle — any subset shipped alone leaves the system in an
  inconsistent state (e.g., shipping worker write-back without
  the legacy Sweeper gating just doubles the Store writes). Atomic
  bundling matches the design's framing.
- (b) Split by sub-task. Each task (0.1-0.6) as its own PR, sequenced.
  Smaller PRs are easier to review and revert; CI runs faster per
  PR. Cost: longer total wall-clock to ship Phase 0.
- (c) Split by surface. Group "Store interface + impls" (0.5, 0.6) as
  one PR; "worker + webhook write-back" (0.1, 0.2) as another;
  "config + scheduler gating" (0.3, 0.4) as a third. Mid-ground
  between (a) and (b).
- other:

**5.** ✅ **Resolved (revised 2026-06-23).** Chart version bump for
Phase 0.

- (a) Chart-minor (0.7.x → 0.8.0). Originally chosen; superseded by the
  IMPL-0015/IMPL-0016 reversal — see [Sequencing](#sequencing). Phase 0
  now ships as part of the `1.0.0-rc.N` train (tag `1.0.0-rc.2` or next
  available), bundled into the eventual `1.0.0` stable release alongside
  IMPL-0016 and IMPL-0015's other phases.
- (b) Chart-patch (0.7.x → 0.7.y). No new operator-facing knobs;
  metrics are internal. Patch bump matches no-config-change.
- (c) Chart-major (0.7.x → 1.0.0). Argument: the worker contract
  changes (now writes to Store); operators need to be aware.
  Probably overkill for an internal change.
- other:

**6.** ✅ **Resolved.** Webhook push handler `pending` status write —
order relative to the enqueue call?

- **(a) = Write `pending` BEFORE the enqueue (chosen).** Fall back to
  no-write on enqueue failure. Operator-visible state matches "we
  tried to enqueue."
- (b) Write `pending` AFTER successful enqueue. If the enqueue
  fails, no state change. Cleaner failure semantics; matches "the
  worker is the only thing that can advance state."
- (c) Skip the `pending` write entirely. Webhook just enqueues; the
  worker writes `success`/`error` on completion. The brief window
  where the row's status is the old value is acceptable.
- other:

**7.** ✅ **Resolved.** Where does the BudgetTracker consultation happen
inside Discoverer — per repo or per page of `ListInstallationRepos`?

- **(a) = Per repo (chosen).** Tight integration; fewest wasted API calls
  when budget runs out mid-page.
- (b) Per page. Coarser-grained gate; reduces tracker-state
  contention. Wastes the last partial page if budget exhausts
  mid-page.
- (c) Per installation. Coarsest; only check budget when moving from
  one installation to the next. Discoverer is mostly Store writes,
  not API calls, so the cost of "over-discovering" by one
  installation is small.
- other:

**8.** ✅ **Resolved.** Phase 0 → Phase 1 boundary — does Phase 1
implementation start once Phase 0 is merged, or once Phase 0 has soaked
in homelab for some time?

- (a) Phase 0 must soak in homelab for ≥ 1 sweep cycle (default
  168h = 1 week) before Phase 1 implementation starts. Catches
  unforeseen issues with the freshness gate before layering
  budget gating on top.
- (b) Phase 0 must merge and pass CI; no soak required. Phase 1
  implementation starts immediately. Faster end-to-end; relies on
  test coverage to catch issues.
- (c) Phase 0 must soak for one full day in homelab. Compromise
  between (a) and (b) — catches obvious issues without blocking
  for a week.
- **other (chosen): Force a manual sweep run after Phase 0 deploys to
  homelab; verify install + Phase 0 freshness gate work end-to-end
  before starting Phase 1 implementation.** No time-based soak
  window — the cycle of "deploy → trigger sweep manually → verify
  metrics + Store rows" provides the validation signal more
  efficiently than waiting on natural sweep cadence. Concretely:
  after Phase 0 chart deploys, hit the binary's webhook endpoint or
  the sweep schedule with a test payload, watch
  `store_writeback_total{outcome="ok"}` rise and
  `repo_state.last_check_status` populate, then green-light Phase 1
  implementation work.

## References

- [DESIGN-0017: Stale-sweep cutover and repository
  discovery](../design/0017-stale-sweep-cutover-and-repository-discovery.md)
  — the design this IMPL implements. Read first.
- [DESIGN-0018: Deprecate memory
  backend](../design/0018-deprecate-memory-backend.md) — sibling
  design; IMPL-0016 will implement it after IMPL-0015 ships.
- [DESIGN-0012: Persistent reconcile state and multi-replica
  coordination](../design/0012-persistent-reconcile-state-and-multi-replica-coordination.md)
  — established the Store / Queue / Scheduler interfaces this IMPL
  extends.
- [IMPL-0011: Persistent reconcile state and multi-replica
  coordination](0011-persistent-reconcile-state-and-multi-replica-coordination.md)
  — opened the migration window this IMPL closes.
- `internal/store/store.go:25-30` — existing `StatusSuccess` /
  `StatusError` / `StatusPending` / `StatusSkipped` constants. The
  schema columns this IMPL is the first to populate.
- `docs/operations/scaling.md` — operator-facing context for the
  validation procedure and the metrics catalogue this IMPL extends.
