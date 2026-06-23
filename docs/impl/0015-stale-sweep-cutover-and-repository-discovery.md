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
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 0: State-writeback prerequisites](#phase-0-state-writeback-prerequisites)
  - [Phase 1: Discoverer + Layer 1 budget gating](#phase-1-discoverer--layer-1-budget-gating)
  - [Phase 2: Opt-in cutover (operator-side)](#phase-2-opt-in-cutover-operator-side)
  - [Phase 3: Chart default flip](#phase-3-chart-default-flip)
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
  IMPL-0016. This IMPL preserves the memory backend's existing semantics
  via a no-op Store path.
- Fast retry of errored repos (separate sweep cadence for
  `last_check_status=error`). Future enhancement; out of scope per
  DESIGN-0017 decision (m).
- Repo *removal* handling (archived / deleted / moved out of installation).
  Out of scope per DESIGN-0017 decision (c) — 404 on next reconcile is
  the existing soft-handling path.
- Auto-tuning `estimatedCostPerRepo` from observed consumption. Out of
  scope per DESIGN-0017 decision (h).
- Phase 4 (legacy Sweeper removal from Postgres path) — already absorbed
  into Phase 0 by the audit. Memory backend retains the legacy Sweeper
  schedule until DESIGN-0018 ships.

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its
tasks are checked off and its success criteria are met. Phase 0 is the
largest by far — it bundles every prerequisite the design surfaced in the
gap audit. Phases 2 and 3 are smaller / operator-side.

---

### Phase 0: State-writeback prerequisites

The prerequisite bundle. None of Phases 1-3 work coherently without these.
Phase 0 lands as one or more PRs (see Open Question 4 for PR strategy) and
must ship and soak in production before Phase 1 begins implementation.

#### Tasks

**0.1 — Worker `Store` injection + write-back contract**

- [ ] Add `store store.Store` and `policyVersion string` fields to
  `internal/worker.Pool`.
- [ ] Update `worker.New(...)` constructor signature to accept `store`
  and `policyVersion` parameters. Document the new contract in the
  package doc-comment.
- [ ] Update `cmd/repo-guardian/main.go` `bringUp` to thread the
  `stateStore` and a `policyVersion` value (computed once at startup
  from `policy.Version(cfg, templates.AsMap())` — see Task 0.4) into
  `worker.New`.
- [ ] In `processJob`, after `engine.CheckRepo` returns, construct a
  `*store.RepoState` with `LastCheckedAt = &now`, `PolicyVersion =
  p.policyVersion`, and either `LastCheckStatus = store.StatusSuccess`
  + `LastError = ""` (on success) OR `LastCheckStatus =
  store.StatusError` + `LastError = truncate(err.Error(), 1024)` (on
  failure).
- [ ] Always call `p.store.UpdateRepoState(ctx, state)` — both success
  and error paths. Best-effort: log + count + continue on Store write
  failure (per design decision (k)).
- [ ] Add a `truncate(s string, n int)` helper. See Open Question 3 for
  package location.
- [ ] Provide a no-op `Store` implementation for memory backend
  deployments — likely already covered by `internal/store/memory/` if
  its `UpdateRepoState` is a trivial map write. Confirm with audit.
- [ ] Update mockery-generated mock for `store.Store` to include any
  new methods (`UpsertIfMissing` lands in Task 0.5).
- [ ] Write unit tests: success-path write, error-path write,
  Store-write-failure logged-and-continued. Use the mockery mock.

**0.2 — Webhook handler `Store` injection + discovery write-back**

- [ ] Add `store store.Store`, `policyVersion string`, and `freshness
  time.Duration` fields to `internal/webhook.Handler`.
- [ ] Update `webhook.NewHandler(...)` constructor signature to accept
  the new dependencies.
- [ ] Update `cmd/repo-guardian/main.go` to wire the Store + policy
  version into the webhook handler constructor.
- [ ] In `handleInstallationRepositoriesEvent`, for each repo in
  `RepositoriesAdded`, call `h.store.UpsertIfMissing(ctx,
  &store.RepoState{...})` with a jittered initial `LastCheckedAt =
  now - rand.Int63n(int64(2*h.freshness))` and `PolicyVersion = ""`.
- [ ] In `handleRepositoryEvent` for `repository.created`, perform the
  same UpsertIfMissing.
- [ ] In `handlePushEvent`, after the existing enqueue logic, call
  `h.store.UpdateRepoState(ctx, &store.RepoState{...})` with
  `LastCheckStatus = store.StatusPending`, current timestamp, and the
  policy version. See Open Question 6 for ordering relative to the
  enqueue call.
- [ ] Write unit tests for each handler path; assert exactly one
  Store call per event with correct payload.

**0.3 — Legacy Sweeper gating on `STORE_BACKEND`**

- [ ] In `cmd/repo-guardian/main.go`, wrap the existing
  `sched.Schedule(ctx, "sweep", interval, sweeper.ReconcileAll)` call
  in `if cfg.StoreBackend != config.StoreBackendPostgres`.
- [ ] Add a `slog.Info` log message at the gate explaining the
  rationale ("Postgres backend uses StaleSweeper, not legacy
  per-tick enumeration").
- [ ] Verify no tests directly depend on the legacy schedule
  unconditionally running.
- [ ] Document the change in `docs/operations/scaling.md` under the
  Postgres-backend deployment section.

**0.4 — `policy.Version` template-hash fix**

- [ ] Add `AsMap() map[string]string` method to `*rules.TemplateStore`
  in `internal/rules/store.go`. Returns a snapshot of all template
  names → content (embedded + ConfigMap-overridden).
- [ ] Update `cmd/repo-guardian/main.go:177` (or wherever
  `policy.Version` is called at startup) to pass
  `templates.AsMap()` instead of `nil`.
- [ ] Write a unit test that asserts the version hash changes when a
  template entry's content changes (use a non-default
  `TemplateStore` fixture).
- [ ] Document the operator-facing implication in
  `charts/repo-guardian/README.md.gotmpl`: editing a template
  ConfigMap now triggers re-enqueue of all repos via policy version
  invalidation.

**0.5 — `Store.UpsertIfMissing` interface + implementations**

- [ ] Add `UpsertIfMissing(ctx context.Context, s *RepoState) (created
  bool, err error)` to the `Store` interface in
  `internal/store/store.go`. Document it in the package doc-comment.
- [ ] Implement in `internal/store/postgres/postgres.go` using a single
  query: `INSERT INTO repo_state (...) VALUES (...) ON CONFLICT
  (installation_id, owner, repo) DO NOTHING RETURNING (xmax = 0) AS
  created`. Handle the "no row returned" case as `created = false,
  err = nil`.
- [ ] Implement in `internal/store/memory/memory.go` with a map
  presence check under the existing mutex.
- [ ] Regenerate the mockery mock for `store.Store` via `make mocks`.
- [ ] Write unit tests for both implementations: existing row
  preserved (returns `created=false`), missing row inserted
  (returns `created=true`), idempotent on repeated calls.
- [ ] Add an integration test in
  `internal/store/postgres/postgres_integration_test.go` that
  exercises the `ON CONFLICT` path with a real Postgres.

**0.6 — Net-new Phase 0 metrics**

- [ ] Add `repo_guardian_store_writeback_total` as a CounterVec
  labelled by `installation_id` and `outcome` (`ok` | `error`).
- [ ] Add `repo_guardian_store_writeback_duration_seconds` as a
  Histogram.
- [ ] Wire both metrics into the worker's UpdateRepoState call site
  added in Task 0.1.
- [ ] Update `charts/repo-guardian/templates/prometheusrule.yaml` if
  any alerts on the new metrics are warranted. None planned for
  Phase 0 — these are observation metrics.
- [ ] Document the new metrics in
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

- [ ] Create `internal/scheduler/budget.go` (or `internal/budget/` — see
  Open Question 2) with the `BudgetTracker` struct per DESIGN-0017
  snippet.
- [ ] Implement `SpendableForEnqueue() int`,
  `RefreshFromAPI(client) error`, and a `Decrement(cost int)` helper.
- [ ] Add per-installation tracker map (keyed by `installationID
  int64`) to the leader (Scheduler).
- [ ] Add `resetAt`-elapsed refresh trigger so the tracker
  auto-refreshes when the GitHub-reported hourly window rolls.
- [ ] Write unit tests with a fake `RateLimitClient`. Cover:
  budget-exhausted gate, reset-elapsed refresh, decrement
  accuracy, multi-installation isolation.

**1.2 — Wire `BudgetTracker` into `StaleSweeper`**

- [ ] In `internal/checker/sweep.go.SweepStale`, build a per-tick
  `budgets map[int64]int` from the candidate set's installations.
- [ ] Before each `Queue.Enqueue` call, consult
  `tracker.SpendableForEnqueue()`. If 0, increment
  `enqueue_gated_by_budget_total` and skip the enqueue.
- [ ] On successful enqueue, decrement the tracker's `remaining`
  field by `costPerRepo`.
- [ ] Add unit tests covering: all-within-budget, budget-exhausted-mid-tick,
  budget-recovers-on-next-tick.

**1.3 — `Discoverer.Discover` implementation**

- [ ] Either repurpose `internal/scheduler.Sweeper` (rename
  `ReconcileAll` → `Discover` and remove enqueueing) or create a
  new `Discoverer` type. See Open Question 1 for package location.
- [ ] Implement `Discover(ctx) error` per DESIGN-0017 snippet:
  `ListInstallations` → for each `ListInstallationRepos` → for each
  call `Store.UpsertIfMissing` with jittered initial
  `LastCheckedAt` and empty `PolicyVersion`.
- [ ] Consult `BudgetTracker` before each `UpsertIfMissing` (or
  before each `ListInstallationRepos` page — see Open Question 7).
- [ ] Skip on Store-read errors (treat as "still actionable"
  fail-safe — matches DESIGN-0017's discoverer-error semantic).
- [ ] Increment `repo_discovered_total{installation_id}` on each
  `created=true` row.
- [ ] Write unit tests: empty installations, idempotency on repeat
  runs, jitter range bounds, error propagation.

**1.4 — Configuration**

- [ ] Add new env vars to `internal/config/`:
  - `DISCOVERY_INTERVAL` — Go duration; default `1h`.
  - `DISCOVERY_ENABLED` — bool; default `false` for both backends
    (Phase 2 is opt-in; Phase 3 flips the default for Postgres).
  - `DISCOVERY_RESERVE_FRACTION` — float; default `0.20`. Validate
    range `[0.0, 1.0]`.
  - `DISCOVERY_ESTIMATED_COST_PER_REPO` — int; default `10`.
    Validate `> 0`.
- [ ] Add to `charts/repo-guardian/values.yaml` under a new
  `discovery:` block. Mirror env vars exactly.
- [ ] Wire env vars into `Deployment` template via the existing
  reserved-env-vars helper.
- [ ] Add helm-unittest cases asserting the env vars appear with
  correct defaults.

**1.5 — Wire scheduler**

- [ ] In `cmd/repo-guardian/main.go`, when
  `cfg.DiscoveryEnabled && cfg.StoreBackend ==
  config.StoreBackendPostgres`, schedule
  `discoverer.Discover` at `cfg.DiscoveryInterval`.
- [ ] Log explicitly which schedulers are active at startup
  (legacy Sweeper on memory, StaleSweeper on Postgres,
  Discoverer when enabled).

**1.6 — Net-new Phase 1 metrics**

- [ ] `repo_guardian_repo_discovered_total{installation_id}` —
  CounterVec, Discoverer increments.
- [ ] `repo_guardian_discovery_duration_seconds` — Histogram.
- [ ] `repo_guardian_discovery_api_calls_total{installation_id,
  endpoint}` — CounterVec, labelled `endpoint=list_installations`
  / `list_installation_repos`.
- [ ] `repo_guardian_api_budget_remaining{installation_id}` —
  Gauge.
- [ ] `repo_guardian_api_budget_spendable{installation_id}` — Gauge.
- [ ] `repo_guardian_api_budget_reserve_fraction{installation_id}` —
  Gauge.
- [ ] `repo_guardian_api_budget_utilisation{installation_id}` —
  Gauge.
- [ ] `repo_guardian_api_budget_refresh_total{installation_id,
  outcome}` — CounterVec.
- [ ] `repo_guardian_enqueue_gated_by_budget_total{installation_id}` —
  Counter. Operator alarm signal.
- [ ] Document all in `docs/operations/scaling.md`.
- [ ] Consider an alert in `prometheusrule.yaml` on sustained
  `enqueue_gated_by_budget_total` rate > 0 — operators want to know
  when they're rate-limit-bound.

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

### Phase 2: Opt-in cutover (operator-side)

After Phase 1 ships to production and soaks for ≥ 1 release cycle,
operators flip `DISCOVERY_ENABLED=true` per installation. This phase is
primarily an operator runbook and validation procedure; the code is
unchanged from Phase 1.

#### Tasks

- [ ] Author `docs/operations/discoverer-cutover.md` runbook covering:
  - Pre-flight checks (Phase 0 metrics healthy, Phase 1 deployed)
  - How to flip `DISCOVERY_ENABLED=true` via chart values
  - Expected metric changes (queue depth, `repo_discovered_total`,
    `api_budget_utilisation`)
  - Rollback procedure (set back to `false`, restart)
- [ ] Validate the runbook in homelab by performing the cutover.
- [ ] Add a chart NOTES.txt section that points operators at the
  runbook when `DISCOVERY_ENABLED=false` is detected on a Postgres
  backend deployment.
- [ ] Update `docs/operations/scaling.md` to reference the runbook.

#### Success Criteria

- Runbook reviewed and validated in homelab.
- Operator performs the cutover; queue depth on subsequent sweep
  ticks reflects only-stale-repos (no legacy enumeration).
- `repo_discovered_total` increments on new-repo events; no
  drift between webhook-driven and discovery-loop-driven discoveries.
- No regressions in any Phase 0 metric.

---

### Phase 3: Chart default flip

After ≥ 1 release cycle of Phase 2 operator validation in homelab and any
public testers, flip the chart default for `DISCOVERY_ENABLED` to `true`
when `store.backend=postgres`.

#### Tasks

- [ ] Change `charts/repo-guardian/values.yaml` default for
  `discovery.enabled` from `false` to `true` (still gated by
  Postgres backend selection in the deployment template).
- [ ] Update `charts/repo-guardian/README.md.gotmpl` to document the
  new default.
- [ ] Bump chart `version` and `appVersion` (chart-major if
  considered a behavioural change to defaults, else chart-minor).
- [ ] Regenerate chart README via `make helm-docs`.
- [ ] Add a chart `CHANGELOG.md` entry describing the default flip
  and any opt-out instructions.
- [ ] Update helm-unittest cases to reflect the new default.
- [ ] Update `docs/operations/scaling.md` to remove "opt-in" framing.

#### Success Criteria

- All Phase 3 tasks checked off.
- `make ci` passes; helm-unittest passes against the new default.
- Chart README regenerated and committed.
- A fresh chart install on Postgres backend has Discoverer active
  out-of-the-box.
- Memory backend deployments unaffected (no Discoverer
  scheduling; legacy Sweeper continues).

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
| `internal/store/memory/memory.go` | 0 | Modify | Implement `UpsertIfMissing` |
| `internal/store/memory/memory_test.go` | 0 | Modify | Add UpsertIfMissing tests |
| `internal/store/mocks/Store.go` | 0 | Regenerate | `make mocks` |
| `internal/rules/store.go` | 0 | Modify | Add `AsMap() map[string]string` |
| `internal/rules/store_test.go` | 0 | Modify | Test AsMap |
| `internal/policy/version_test.go` | 0 | Modify | Test that template changes invalidate hash |
| `internal/metrics/metrics.go` | 0+1 | Modify | New metric definitions |
| `cmd/repo-guardian/main.go` | 0+1 | Modify | Wire Store into worker + webhook; gate legacy Sweeper; schedule Discoverer |
| `internal/scheduler/sweep.go` | 1 | Modify | Either rename to Discoverer or extract Discoverer type |
| `internal/scheduler/budget.go` | 1 | Create | BudgetTracker |
| `internal/scheduler/budget_test.go` | 1 | Create | BudgetTracker tests |
| `internal/checker/sweep.go` | 1 | Modify | Wire BudgetTracker into SweepStale |
| `internal/checker/sweep_test.go` | 1 | Modify | Budget-gating tests |
| `internal/config/config.go` | 1 | Modify | New env vars + validation |
| `internal/config/config_test.go` | 1 | Modify | Defaults + range validation tests |
| `charts/repo-guardian/values.yaml` | 1+3 | Modify | New `discovery:` block; Phase 3 flips defaults |
| `charts/repo-guardian/templates/deployment.yaml` | 1 | Modify | Wire new env vars |
| `charts/repo-guardian/tests/backend_shapes_test.yaml` | 1 | Modify | Assert new env vars |
| `charts/repo-guardian/templates/prometheusrule.yaml` | 1 | Modify | Optional alert on `enqueue_gated_by_budget_total` |
| `charts/repo-guardian/README.md.gotmpl` | 0+3 | Modify | Document template ConfigMap invalidation; Phase 3 documents new default |
| `docs/operations/scaling.md` | 0+1 | Modify | Document new metrics + legacy Sweeper change |
| `docs/operations/discoverer-cutover.md` | 2 | Create | Operator runbook for opt-in cutover |

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

**5.** ✅ **Resolved.** Chart version bump for Phase 0.

- **(a) = Chart-minor (0.7.x → 0.8.0) (chosen).** Phase 0 changes chart
  values surface (new metric definitions, no new operator-tunable
  knobs but the deployment env has new metrics endpoints). Minor bump
  signals "additive change."
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
