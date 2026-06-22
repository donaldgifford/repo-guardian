---
id: DESIGN-0017
title: "Stale-sweep cutover and repository discovery"
status: Draft
author: Donald Gifford
created: 2026-06-22
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0017: Stale-sweep cutover and repository discovery

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-06-22

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [Per-reconcile Store update — the missing half](#per-reconcile-store-update--the-missing-half)
  - [Pacing — two layered mechanisms](#pacing--two-layered-mechanisms)
  - [Layer 1 — Budget-aware enqueueing (primary)](#layer-1--budget-aware-enqueueing-primary)
  - [Layer 2 — Jittered initial last_checked_at (secondary)](#layer-2--jittered-initial-last_checked_at-secondary)
  - [Cross-replica coordination](#cross-replica-coordination)
  - [Discovery-only Sweeper mode](#discovery-only-sweeper-mode)
  - [Webhook-driven incremental discovery](#webhook-driven-incremental-discovery)
  - [Periodic reconciliation discovery](#periodic-reconciliation-discovery)
  - [Cutover behaviour](#cutover-behaviour)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

Cut over fully from the legacy `Sweeper.ReconcileAll` (full
`ListInstallations()` → `ListInstallationRepos()` → enqueue-every-repo loop) to
the IMPL-0011 `StaleSweeper.SweepStale` (Store-backed, only-stale-repos query) on
the Postgres backend. Today both schedulers run at the same interval, so the
StaleSweeper's selectivity is wasted — the legacy Sweeper has already enqueued
every repo by the time StaleSweeper runs.

This DESIGN replaces the legacy Sweeper's "enqueue everything" responsibility with
a smaller "discover-only" mode that runs at a slower cadence, populates Store rows
for newly-observed repos, but does NOT enqueue them. StaleSweeper picks them up on
its next tick. The same path also paces the cold-start enqueue burst when a fresh
deployment encounters an existing fleet of repos.

## Goals and Non-Goals

### Goals

- **Fix the freshness gate.** Today's workers don't write `last_checked_at` back
  to Store after reconciling, so StaleSweeper re-enqueues every aged repo on
  every tick forever (steady-state thrash). Wire `Store.UpdateRepoState` into
  the worker post-`engine.CheckRepo` path. This is the gating fix for the
  whole design.
- Eliminate redundant enqueueing on the Postgres backend. After cutover, each
  sweep tick enqueues only stale repos, not every repo.
- Cold-start a deployment against an existing 3000-repo fleet without burning the
  GitHub API rate-limit budget in the first hour.
- Keep discovery of new repos working without depending exclusively on webhooks
  (operators may have webhook delivery gaps, mis-configured installations, or
  brand-new installations with no historical webhooks).
- Preserve existing memory-backend behaviour: memory mode gets a no-op Store
  implementation; the legacy Sweeper continues to enqueue per tick.
- Bounded blast radius from misconfigured discovery: if discovery breaks, in-flight
  reconciliation continues; only new-repo onboarding is affected.

### Non-Goals

- Eliminating the legacy Sweeper entirely. Memory backend deployments need it.
  Postgres backend after cutover doesn't.
- Webhook-only discovery. Webhooks are best-effort and can be missed; we keep a
  periodic discovery path as a safety net.
- Real-time repo discovery (sub-second). Discovery runs at a configurable cadence
  (default: hourly). New repos can wait minutes for onboarding without harm.
- Cross-installation deduplication beyond the existing per-Store-row uniqueness.

## Background

`cmd/repo-guardian/main.go` today schedules two handlers at the same interval:

```go
sched.Schedule(ctx, "sweep", interval, sweeper.ReconcileAll)             // legacy: enumerates EVERYTHING
if storeBackend == "postgres" {
    sched.Schedule(ctx, "stale-sweep", interval, staleSweeper.SweepStale) // filtered: stale only
}
```

The legacy `Sweeper.ReconcileAll` (in `internal/scheduler/sweep.go`) does:

1. `ListInstallations()` — one API call
2. For each installation, `ListInstallationRepos()` — one API call per page
3. For each repo, `Queue.Enqueue` — one Valkey op
4. Done

The `StaleSweeper.SweepStale` (in `internal/checker/sweep.go`) does:

1. `Store.StaleRepos(freshness, policyVersion, batchSize)` — one Postgres query
2. For each stale repo, rate-limit check + `Queue.Enqueue` — one Valkey op each
3. Done

Both run at `policyCfg.Guardian.ParsedScheduleInterval` (default 168h, configurable
to anything via `SCHEDULE_INTERVAL`). At tick time, the legacy Sweeper dumps every
known repo into the queue; StaleSweeper then does its filtered work — but by then
the queue already has every repo. Workers proceed to reconcile each repo
regardless of `last_checked_at`, because the engine itself has no freshness gate
(only the queue-feeding StaleSweeper does).

The intent in the IMPL-0011 comment header:

> Bootstrap (populating the store with rows for newly-installed repos) is the
> legacy Sweeper's job. The two coexist during the IMPL-0011 migration window.

The migration window never closed. The legacy Sweeper still enqueues, not just
bootstraps.

### Real symptoms

- **Cold start with empty Store:** every repo enqueued on first tick → instant
  rate-limit pressure as workers race to reconcile 3000 repos × ~10 API calls
  each = 30k API calls within the first sweep window.
- **Pod restart with warm Store:** every repo re-enqueued unconditionally, even
  though Store would have correctly skipped them. Worker pool churns through
  repos doing redundant work (some short-circuited by the engine's own internal
  checks, some not).
- **Steady-state:** legacy Sweeper enqueues N repos every tick; StaleSweeper adds
  the (small) stale subset. Worker pool processes 2x the work it needs to.

## Detailed Design

### Per-reconcile Store update — the missing half

The IMPL-0011 architecture treats `repo_state.last_checked_at` as the source of
truth for "when was this repo last reconciled." StaleSweeper queries against
that column. But **the workers don't currently update it**:

- `internal/worker/worker.go` doesn't import `internal/store/`.
- `engine.CheckRepo` doesn't accept a `Store` parameter.
- `Store.UpdateRepoState` is defined but is called by zero non-test code paths.

Consequences:

- **Webhook-triggered reconciles don't refresh the timestamp.** A push event
  arrives, the worker reconciles the repo, completes successfully — and the
  Store still says the repo was last checked at whatever `last_checked_at`
  Discoverer wrote on first observation. The next StaleSweeper tick treats the
  repo as still stale and enqueues it again.
- **Steady-state thrashing.** Once a repo's jittered `last_checked_at` ages
  past `freshness`, it stays stale forever — every sweep tick re-enqueues it
  because no completion path moves the timestamp forward. The freshness gate
  is effectively a no-op after one cycle.
- **Policy-version invalidation doesn't roll forward.** When the operator
  changes the policy and `policy.Version()` returns a new hash, StaleSweeper
  is supposed to re-enqueue every repo (matching the `policy_version != $new`
  predicate). But after that re-enqueue, the Store row's `policy_version`
  field never updates because workers don't write to the Store. The "every
  tick" thrash persists across all 3000 repos until manual intervention.

This is a correctness bug, not an optimisation. The DESIGN-0017 cutover
**cannot be completed** until the worker writes back. Closing this gap is
the highest-priority piece of this design.

**Worker write-back contract:**

After every successful `engine.CheckRepo`, the worker calls
`Store.UpdateRepoState`:

```go
// In internal/worker/worker.go processJob, after engine.CheckRepo returns nil
err := p.store.UpdateRepoState(ctx, &store.RepoState{
    InstallationID: j.InstallationID,
    Owner:          j.Owner,
    Repo:           j.Repo,
    LastCheckedAt:  ptr(time.Now()),
    PolicyVersion:  p.policyVersion,
})
if err != nil {
    log.Warn("UpdateRepoState failed; StaleSweeper will re-enqueue",
        "owner", j.Owner, "repo", j.Repo, "error", err)
    metrics.StoreWriteBackErrorsTotal.WithLabelValues(j.Owner).Inc()
    // Do NOT return the error — the reconcile succeeded; the Store write
    // is best-effort. Worst case: this repo gets re-checked next sweep.
}
```

**Behaviour on reconcile failure:**

If `engine.CheckRepo` returns an error, the worker does NOT call
`UpdateRepoState`. Leaving the old timestamp means StaleSweeper will pick the
repo up again at the next eligible window — which is the correct retry
semantic. Updating `last_checked_at` on failure would suppress retries.

**Trigger field:**

The job's `Trigger` field (`scheduler`, `webhook`, `push`) is preserved on
the Store row as a metric label only, not as state. The Store doesn't
distinguish "which trigger most recently reconciled this repo" because the
freshness gate doesn't care — a successful reconcile is a successful
reconcile.

**Why this resolves the user-visible bug:**

After this design lands:

- Webhook push → worker reconciles → Store updated → next StaleSweeper
  correctly skips the repo for the next `freshness` window.
- Sweep tick reconciles → Store updated → next sweep tick skips the repo
  similarly.
- Failed reconcile → Store NOT updated → next sweep retries → bounded retry
  loop driven by `freshness` and rate-limit reserve, with no thrashing.
- Policy version change → all repos re-enqueue → workers reconcile and write
  the new version → freshness gate works normally for the next cycle.

### Pacing — two layered mechanisms

Two distinct concerns sit underneath "don't burn the API budget":

| Concern | Timescale | Mechanism |
|---|---|---|
| Avoid enqueueing more work per tick than the installation's current rate-limit budget can absorb | Within a single sweep tick | **Budget-aware enqueueing** (Layer 1) — reads `RateLimitRemaining`, deducts an estimated cost per repo, gates further enqueues when budget would dip below a configured reserve |
| Avoid having every repo go stale on the same tick (post-cold-start synchronisation) | Across multiple sweep ticks | **Jittered initial `last_checked_at`** (Layer 2) — spreads first-observation timestamps so stale candidates trickle in over time instead of arriving in lockstep |

Layer 1 is the primary throughput control. Layer 2 is the smoothing-over-time
mechanism that avoids synchronisation effects. Both are cheap; both are easy to
observe via Prometheus metrics; they layer cleanly.

### Layer 1 — Budget-aware enqueueing (primary)

Each installation has a `BudgetTracker`:

```go
type BudgetTracker struct {
    installationID int64
    limit          int           // hourly cap (5000 non-enterprise; 12500 GHEC)
    remaining      int           // live counter, decremented after each Enqueue
    resetAt        time.Time     // GitHub-reported reset time
    reserve        float64       // fraction to NOT touch (default 0.20)
    costPerRepo    int           // estimated API calls per reconcile (default 10)
}

func (bt *BudgetTracker) SpendableForEnqueue() int {
    if time.Now().After(bt.resetAt) {
        bt.RefreshFromAPI()  // pulls fresh remaining + reset from GitHub
    }
    reserveCount := int(float64(bt.limit) * bt.reserve)
    spendable := bt.remaining - reserveCount
    if spendable <= 0 { return 0 }
    return spendable / bt.costPerRepo
}
```

The Scheduler leader (StaleSweeper + Discoverer) consults the tracker before
every Enqueue decision:

```go
// In StaleSweeper.SweepStale
candidates, _ := s.store.StaleRepos(ctx, freshness, policyVersion, batchSize)
budgets := map[int64]int{}                                  // available slots per installation
for _, c := range candidates {
    if budgets[c.InstallationID] == 0 {
        budgets[c.InstallationID] = s.trackers[c.InstallationID].SpendableForEnqueue()
    }
    if budgets[c.InstallationID] <= 0 {
        metrics.EnqueueGatedByBudget.WithLabelValues(strconv.FormatInt(c.InstallationID, 10)).Inc()
        continue
    }
    s.queue.Enqueue(ctx, jobFromRepo(c))
    budgets[c.InstallationID]--
    s.trackers[c.InstallationID].remaining -= s.trackers[c.InstallationID].costPerRepo
}
```

**Key properties:**

- **Aggressive within bounds.** The leader spends all available budget down to
  the reserve floor every tick. No artificial "go slow at the start" behaviour
  beyond what the budget itself enforces.
- **Reactive to actual state.** The tracker refreshes from
  `Client.RateLimitRemaining` on tick start (or when `resetAt` elapses), so
  webhook-triggered consumption that happened between ticks is observed.
- **Costed in advance.** `costPerRepo` is an estimate; actual consumption is
  observed on the next refresh. Estimation error self-corrects within one tick.
- **Single integer per installation.** Trivial to surface as a metric and to
  reason about.

**Default reserve fraction: 0.20.** Holds back 20% of the installation's hourly
budget for webhook-triggered work and unforeseen consumption. Tunable via:

| Knob | Default | Meaning |
|---|---|---|
| `discovery.reserveFraction` | `0.20` | Fraction of `limit` kept untouched |
| `discovery.estimatedCostPerRepo` | `10` | API calls assumed per reconcile (override after observing real consumption via metrics) |

**Worked example — 25 GHEC orgs × 3000 repos at cold start:**

| Stage | Per-install budget | Discovery enqueues per tick | Wall-clock to drain |
|---|---|---|---|
| Tick 1 | 12500 remaining, reserve 2500 → 10000 spendable / 10 = 1000 repos | min(1000, repos_for_install) | — |
| Across 25 installs uniformly | 1000 per install × 25 = 25000 total | — | — |
| 3000 stale candidates / 25 = 120 per install | All 3000 enqueued in tick 1 (budget allows) | — | 1 tick (≤1 hour) |

At the GHEC budget level, the rate limit isn't actually the binding constraint
for a steady-state 3000-repo fleet — the deployment can absorb the cold-start
spike comfortably. The budget mechanism is most valuable for non-enterprise
installations (5000/hr cap, where 3000 repos × 10 calls = 30000 calls would
exceed the budget by 6x without pacing).

### Layer 2 — Jittered initial `last_checked_at` (secondary)

Even with budget gating in place, two failure modes remain:

1. **Synchronisation collapse.** Without jitter, all 3000 repos have
   `last_checked_at = NOW()` at discovery time. 24 hours later, all 3000
   become stale on the same tick. Layer 1 gates enqueueing to fit within
   budget — but every subsequent tick the SAME 3000 repos are stale until the
   leader works through them. The cycle then repeats in lockstep.
2. **Worker-pool burst peaks.** When budget is generous (GHEC), Layer 1
   allows 1000-repo enqueues per tick. The worker pool then peaks at the same
   moment every tick across installations. Spread out by jitter, the peaks
   smear and the worker pool stays uniformly busy.

The jitter writes a randomised initial `last_checked_at`:

```go
initialLastChecked := time.Now().Add(-time.Duration(rand.Int63n(int64(2*s.freshness))))
```

This places the timestamp uniformly in `[now - 2*freshness, now]`. Over time,
repos cycle through stale and freshly-reconciled states at staggered cadences.
The result is smooth steady-state load instead of tick-synchronised bursts.

**Worked example with both layers — 25 orgs × 3000 repos, GHEC, freshness 24h,
sweep_interval 1h:**

| Sweep tick | Repos qualifying as stale | Budget allows | Enqueued |
|---|---|---|---|
| 1 (cold start) | ~750 (those with jitter in [24h, 48h] band) | 25000 | 750 |
| 2 | ~125 (next jitter band slice + tick-1 repos still in flight) | 24900 | 125 |
| 3 | ~125 | 24900 | 125 |
| … | … | … | … |
| 48 | ~125 | 24900 | 125 |
| 49 | tick-1 repos rolling back into stale | 24900 | ~125 |
| Steady state | uniform ~125/tick | budget always plentiful | always ≤ budget |

Without Layer 2 (no jitter): tick 1 sees 3000 stale; Layer 1 budget enqueues
1000 (still over the 750 jittered case but under the 3000 unbounded case);
ticks 2-3 also see 3000 stale (the worker pool hasn't drained them yet);
eventual steady state oscillates around 3000-stale-per-day spikes.

Jitter alone (Layer 2 without Layer 1) doesn't help with non-enterprise rate
limits where even 750 repos × 10 calls = 7500 calls exceeds the 5000/hr budget.

Together, the two layers form a leaky bucket: jitter limits the **inflow rate**
to the stale set; budget limits the **drain rate** from stale to queue.

### Cross-replica coordination

StaleSweeper and Discoverer are scheduled via `scheduler.Scheduler.Schedule(...)`.
With the Valkey-backed scheduler (`scheduler.backend=valkey`), SETNX leader
election ensures only one replica runs the scheduled handler per tick. This
means budget tracking is naturally single-leader — no cross-replica
coordination needed for the enqueue decision.

Workers across all replicas drain the queue and make API calls; each call
implicitly decrements the GitHub-side rate-limit counter. The Scheduler
leader's `BudgetTracker.RefreshFromAPI` on the next tick observes the actual
consumption (the GitHub API returns the live `X-RateLimit-Remaining` header).

Multi-replica scenario where the leader changes mid-tick (rare — the SETNX
lease lasts longer than a tick): the new leader rebuilds its `BudgetTracker`
state from a fresh API call on its first tick. The brief gap between leader
loss and re-acquisition means one tick may be missed, not over-spent. Safe by
default.

### Discovery-only Sweeper mode

Rename and repurpose `Sweeper.ReconcileAll` into `Sweeper.Discover`. Behaviour:

```go
func (s *Sweeper) Discover(ctx context.Context) error {
    installs, _ := s.client.ListInstallations(ctx)
    for _, inst := range installs {
        repos, _ := s.client.ListInstallationRepos(ctx, inst.ID)
        for _, repo := range repos {
            existing, err := s.store.Get(ctx, repo.Owner, repo.Name)
            if err != nil { /* log + count */ continue }
            if existing != nil { continue } // already known
            initialLastChecked := time.Now().Add(-time.Duration(rand.Int63n(int64(2*s.freshness))))
            s.store.Upsert(ctx, store.State{
                Owner:           repo.Owner,
                Repo:            repo.Name,
                InstallationID:  inst.ID,
                LastCheckedAt:   initialLastChecked,
                PolicyVersion:   "", // forces StaleSweeper to pick it up
            })
            metrics.RepoDiscovered.WithLabelValues(strconv.FormatInt(inst.ID, 10)).Inc()
        }
    }
    return nil
}
```

Key properties:

- **No Queue.Enqueue.** The Discover loop only writes Store rows.
- **Idempotent.** Repeated runs do not duplicate rows (`Get`+conditional `Upsert`).
- **Jittered `last_checked_at`.** Spreads the cold-start enqueue burst.
- **Empty PolicyVersion.** Forces StaleSweeper to treat the row as stale on its
  next tick, picking up the repo for first reconciliation.

### Webhook-driven incremental discovery

The webhook handler already processes `installation_repositories.added` events
(see `internal/webhook/`). Extend it to write Store rows the same way Discover
does:

```go
// In webhook handler for installation_repositories.added
for _, repo := range payload.RepositoriesAdded {
    s.store.Upsert(ctx, store.State{
        Owner:          repo.Owner.Login,
        Repo:           repo.Name,
        InstallationID: payload.Installation.ID,
        LastCheckedAt:  time.Now().Add(-time.Duration(rand.Int63n(int64(2*s.freshness)))),
        PolicyVersion:  "",
    })
}
```

Same idempotency and jitter as Discover. Webhook-driven discovery is the
primary path during steady-state operation; the periodic Discover is the safety
net.

### Periodic reconciliation discovery

Cadence: configurable, default 1h. Independent of `SCHEDULE_INTERVAL` (which now
governs only StaleSweeper).

| Env var | Default | Meaning |
|---|---|---|
| `DISCOVERY_INTERVAL` | `1h` | Discovery sweep cadence |
| `DISCOVERY_ENABLED` | `true` (Postgres backend only) | Toggle for operators who want webhook-only discovery |

The 1h default balances:

- Detecting webhook delivery gaps within an hour
- Spreading the API cost of `ListInstallations` + `ListInstallationRepos` across
  many ticks (3000 repos / 100 per page = 30 API calls per discovery tick, well
  within budget)

### Cutover behaviour

`cmd/repo-guardian/main.go` wiring after this design:

```go
// Memory backend: legacy Sweeper unchanged (still enqueues).
if storeBackend == "memory" {
    sched.Schedule(ctx, "sweep", interval, legacySweeper.ReconcileAll)
}

// Postgres backend: split discovery from enqueueing.
if storeBackend == "postgres" {
    if cfg.DiscoveryEnabled {
        sched.Schedule(ctx, "discovery", cfg.DiscoveryInterval, discoverer.Discover)
    }
    sched.Schedule(ctx, "stale-sweep", cfg.ScheduleInterval, staleSweeper.SweepStale)
}
```

The legacy Sweeper is no longer scheduled when Postgres backend is in use. The
new `Discoverer` (renamed and slimmed from the legacy Sweeper) handles only
discovery.

## API / Interface Changes

### `internal/scheduler/sweep.go` (existing legacy Sweeper)

- Memory-backend path: unchanged.
- Postgres-backend path: NEW type `Discoverer` with method `Discover(ctx)`.
  Re-uses the existing `client.ListInstallations` / `ListInstallationRepos` logic
  but writes Store rows instead of enqueueing.
- The legacy `Sweeper.ReconcileAll` stays for memory-backend deployments.
- Both types live in `internal/scheduler/`; sharing utility functions where
  appropriate.

### `internal/config/`

New env vars:

- `DISCOVERY_INTERVAL` — Go duration string, default `1h`.
- `DISCOVERY_ENABLED` — bool, default `true` when `STORE_BACKEND=postgres`,
  `false` otherwise.
- `DISCOVERY_RESERVE_FRACTION` — float, default `0.20`. Fraction of each
  installation's hourly rate-limit budget kept untouched by Layer 1 budget
  gating.
- `DISCOVERY_ESTIMATED_COST_PER_REPO` — int, default `10`. Estimated API calls
  per reconcile, used by `BudgetTracker.SpendableForEnqueue`.

### `internal/store/`

`Store` interface unchanged. The discovery path and the per-reconcile
write-back path both use existing `GetRepoState` / `UpdateRepoState` methods.

### `internal/worker/`

The Pool gains a `Store` dependency:

```go
type Pool struct {
    queue          queue.Queue
    engine         *checker.Engine
    ghClient       ghclient.Client
    store          store.Store          // NEW
    policyVersion  string               // NEW — populated from policy.Version() at startup
    logger         *slog.Logger
    workers        int
    // ...
}

func New(q queue.Queue, engine *checker.Engine, ghClient ghclient.Client,
         st store.Store, policyVersion string,
         workers int, logger *slog.Logger) *Pool {
    return &Pool{
        queue:         q,
        engine:        engine,
        ghClient:      ghClient,
        store:         st,
        policyVersion: policyVersion,
        workers:       workers,
        logger:        logger,
    }
}
```

`processJob` writes `last_checked_at` back to Store on successful reconcile;
skips the write on reconcile error (preserves retry semantic — see
[Per-reconcile Store update](#per-reconcile-store-update--the-missing-half)).

For memory-backend deployments (where there is no Store), the constructor
accepts a no-op Store implementation that returns immediately from
`UpdateRepoState`. No special-casing in `processJob`.

### `internal/webhook/`

Extend the `installation_repositories.added` and `repository.created` handlers
to write Store rows when the Postgres backend is active. Same idempotent
`Get`+conditional `Upsert` pattern.

### `internal/metrics/`

Worker write-back (Per-reconcile Store update):

- `repo_guardian_store_writeback_total{installation_id, outcome="ok"|"error"}`
  — counts UpdateRepoState calls from the worker after each reconcile.
  `outcome="error"` means the reconcile succeeded but the Store write
  failed; alarm signal if sustained.
- `repo_guardian_store_writeback_duration_seconds` — histogram of the
  UpdateRepoState call latency.

Discovery loop:

- `repo_guardian_repo_discovered_total{installation_id}` — counts newly
  discovered repos.
- `repo_guardian_discovery_duration_seconds` — histogram of Discover loop
  wall-clock time.
- `repo_guardian_discovery_api_calls_total{installation_id, endpoint}` —
  counts the ListInstallations / ListInstallationRepos / Get API calls each
  Discover tick makes.

Budget tracker (Layer 1):

- `repo_guardian_api_budget_remaining{installation_id}` — Gauge mirroring the
  latest `RateLimitRemaining` value the leader observed.
- `repo_guardian_api_budget_spendable{installation_id}` — Gauge of
  `(remaining - reserve) / costPerRepo` — i.e., how many repos the leader can
  enqueue right now.
- `repo_guardian_api_budget_reserve_fraction{installation_id}` — Gauge of the
  configured reserve fraction (constant per installation under normal config;
  exposed for dashboarding).
- `repo_guardian_enqueue_gated_by_budget_total{installation_id}` — Counter
  incremented each time a stale candidate is skipped because the budget
  tracker returned 0 spendable slots. Operator alarm signal — sustained
  non-zero means the deployment is rate-limit-bound.
- `repo_guardian_api_budget_refresh_total{installation_id, outcome}` — Counter
  with `outcome="ok"` for successful refreshes and `outcome="error"` for
  failed refreshes (network, auth, etc.).
- `repo_guardian_api_budget_utilisation{installation_id}` — Gauge of
  `1 - (remaining / limit)`. Useful for dashboards showing how aggressively the
  deployment is using the available budget.

### Chart values

```yaml
discovery:
  # NEW. Only effective when store.backend=postgres.
  enabled: true
  interval: "1h"
  # Layer 1 — budget-aware enqueueing
  reserveFraction: 0.20            # 20% of per-install limit kept untouched
  estimatedCostPerRepo: 10         # tune from `api_budget_utilisation` metrics
```

The values are no-ops when `store.backend=memory` (existing legacy Sweeper path
is unaffected).

## Data Model

No schema changes. The `repo_state` table is extended in usage:

- New rows can now have `policy_version = ''` (empty string) to signal
  "discovered but not yet reconciled." The existing StaleSweeper query
  (`policy_version != $currentVersion`) already handles this correctly — empty
  string matches the inequality and the repo gets enqueued.
- The `last_checked_at` column is no longer monotonically derived from
  reconciliation time; on discovery, it can be set to a past timestamp via the
  jitter logic. This is harmless — the column's semantic is "when StaleSweeper
  should next consider this repo," not "wall-clock time of last successful
  reconcile."

## Testing Strategy

### Unit tests

- `Discoverer.Discover` idempotency: repeated runs do not duplicate rows.
- Jitter range: `last_checked_at` always within `(now - 2*freshness, now)`.
- Empty installation list: no Store writes, no panics.
- Store write failure: error counted, loop continues to next repo.

### Integration tests (`_integration_test.go`)

- Cold-start simulation: start with empty Store, run Discover, verify N rows
  inserted with jittered timestamps spanning the expected band.
- Existing rows preserved: rerun Discover after manual `Upsert` with a
  non-default `policy_version`; verify the manual row is untouched.
- Webhook-driven discovery: replay `installation_repositories.added` payload,
  verify the new repo lands in Store with empty `policy_version`.

### Cutover validation

- Spin a Postgres-backed deployment, populate Store with 3000 fake rows whose
  `last_checked_at` is uniformly random in `[now - 24h, now]`.
- Start the binary with legacy Sweeper still scheduled (pre-cutover state).
  Observe: queue depth spikes to 3000 on first tick.
- Disable legacy Sweeper, enable Discoverer (post-cutover state). Restart.
  Observe: queue depth on first tick is only the stale subset (~125 with
  freshness=24h, sweep_interval=1h).

### Chart tests (`helm-unittest`)

- `store.backend=memory`: deployment env has no `DISCOVERY_*` vars (or has them
  but with `DISCOVERY_ENABLED=false`).
- `store.backend=postgres` default: `DISCOVERY_ENABLED=true`, `DISCOVERY_INTERVAL=1h`.
- `store.backend=postgres` with `discovery.enabled=false`: env reflects opt-out.

## Migration / Rollout Plan

### Phase 0 — Worker write-back (prerequisite)

Land the per-reconcile Store update first. This is the gating step — none of
the subsequent phases work correctly without it because the freshness gate
in StaleSweeper depends on workers writing back.

- Wire `Store` into `internal/worker/Pool`; thread `policyVersion` through
  from `cmd/repo-guardian/main.go`.
- `processJob` calls `UpdateRepoState` after every successful `engine.CheckRepo`.
- Failed reconciles do NOT write — preserves retry semantics.
- Memory-backend deployments get a no-op Store implementation; no behavioural
  change.
- Land with budget/jitter Layers 1+2 simultaneously to avoid a window where
  the freshness gate is half-fixed. They're additive in scope but
  inseparable in effect.
- Validate via the new `store_writeback_total{outcome="ok"}` counter — should
  rise in lockstep with `repos_checked_total`.

### Phase 1 — Land Discoverer alongside legacy Sweeper

After Phase 0 is stable in production:

- Implement `Discoverer.Discover` and new env vars; ship at default
  `DISCOVERY_ENABLED=false` for both backends.
- Existing deployments unaffected; legacy Sweeper continues to bootstrap.

### Phase 2 — Opt-in cutover

- Operators with Postgres backend set `DISCOVERY_ENABLED=true`.
- `cmd/repo-guardian/main.go` reads the flag: when true, schedules Discoverer
  AND disables the legacy Sweeper schedule.
- Operator-visible behaviour: queue depth drops sharply because the legacy
  Sweeper no longer enqueues unconditionally.
- Validate via metrics: `repo_guardian_queue_depth` per sweep tick matches
  `StaleRepos` result size; `repo_discovered_total` increments on new repos.

### Phase 3 — Default-flip for Postgres backend

After ≥ 1 release cycle of Phase 2 operator validation:

- Chart default flips to `discovery.enabled=true` when `store.backend=postgres`.
- Memory backend remains on the legacy Sweeper path.

### Phase 4 — Remove legacy Sweeper from Postgres path

Once Phase 3 has been the default for ≥ 1 release cycle:

- `cmd/repo-guardian/main.go` no longer schedules `legacySweeper.ReconcileAll`
  when `STORE_BACKEND=postgres`, regardless of `DISCOVERY_ENABLED` (the flag
  itself remains a kill-switch for Discoverer, but the legacy path is removed
  from the Postgres-backend code path).
- The legacy Sweeper type stays in the codebase for memory-backend deployments.

### Rollback

If Discoverer misbehaves, set `DISCOVERY_ENABLED=false` and restart. Until
Phase 4, the legacy Sweeper schedule re-engages and the deployment behaves
identically to pre-cutover. After Phase 4, the rollback path is via a chart
revert (the legacy Sweeper isn't scheduled for Postgres regardless of the flag).

## Open Questions

**(a)** Jitter range for the initial `last_checked_at`.

- **(a) = `random in [now - 2*freshness, now]` (recommended).** Spreads the
  cold-start enqueue across `2 * freshness / sweep_interval` ticks. With
  freshness=24h, sweep=1h → ~48 ticks.
- (b) `random in [now - freshness, now]`. Spreads across fewer ticks, faster
  steady-state but higher per-tick burst.
- (c) Uniform `now - freshness/2`. No jitter; all repos go stale at the same
  tick. Bad. Rejected.
- other:

**(b)** Discovery cadence default.

- **(a) = `1h` (recommended).** Detects webhook delivery gaps within an hour;
  API cost is negligible at typical fleet sizes.
- (b) `15m`. More responsive to new-repo events that miss the webhook; 4x the
  API cost.
- (c) `24h`. Lower API cost but lets webhook gaps linger for a day.
- other:

**(c)** Should Discoverer also handle repo *removal* (a repo being archived,
deleted, or moved out of the installation)?

- **(a) = Out of scope for v1 (recommended).** Repo removal is a soft problem —
  a stale Store row for an inaccessible repo just hits a 404 on next reconcile
  and gets handled by error paths. Tracking removal cleanly would require
  another comparison pass and isn't blocking.
- (b) Add a "missing repos" pass after each Discover loop. Insert into a
  removal-pending queue; webhook `repository.deleted` cleans up.
- other:

**(d)** Webhook handler write-on-discovery vs Discover loop write — what if
both fire at once?

- **(a) = Idempotent `Get`+conditional `Upsert` (recommended).** Both paths
  check for an existing row before inserting; race is harmless. Same row
  inserted twice with the same key just stays as one row.
- (b) Distributed lock per (owner, repo) during discovery. Overkill for the
  blast radius.
- other:

**(e)** Memory-backend behaviour after this design.

- **(a) = Unchanged — legacy Sweeper continues to enqueue (recommended).** Memory
  backend has no Store; there's no place to write discovery rows. The legacy
  Sweeper's existing semantics are correct for single-replica memory mode.
- (b) Extend Discoverer to support a memory-only mode. Adds complexity for a
  use case that's already correct.
- other:

**(f)** Single-binary architectural question: should Discoverer be a separate
process (e.g., a CronJob) or remain in-pod as a goroutine?

- **(a) = In-pod goroutine (recommended).** Matches the existing Scheduler.Schedule
  pattern. No new resource/deployment shape; existing leader-election covers
  the multi-replica case.
- (b) Separate CronJob. Better isolation but adds a deployment surface and
  needs its own creds + image + chart resources.
- other:

**(g)** Layer 1 reserve-fraction default.

- **(a) = `0.20` (recommended).** Holds back 20% of each installation's hourly
  budget for webhook-triggered work and unforeseen consumption. Comfortable
  margin for typical fleets.
- (b) `0.10`. More aggressive use of available budget; smaller margin for
  webhook bursts.
- (c) `0.30`. More conservative; leaves more budget for webhook surges at the
  cost of slower steady-state catch-up after long downtime.
- (d) Dynamic — derive from observed webhook QPS per installation. Adds state
  but adapts to actual operator usage patterns. Future enhancement.
- other:

**(h)** Layer 1 estimated cost per repo.

- **(a) = `10` API calls per repo (recommended).** Matches the empirical
  per-reconcile cost in `docs/operations/scaling.md` (4 file checks + branch
  / PR ops if actionable + rate-limit probe). Configurable via
  `DISCOVERY_ESTIMATED_COST_PER_REPO` so operators can re-calibrate from
  observed `api_budget_utilisation` metrics.
- (b) Auto-tune from observed consumption. Track actual cost per reconcile in
  Postgres; periodically refresh `costPerRepo` from the rolling average.
  Cleaner adaptive behaviour but adds Store schema and statistics complexity.
- (c) Hard-code with no operator knob. Simpler but worse for operators whose
  reconcilers do more work (e.g., aggressive label sync against thousands of
  labels).
- other:

**(i)** Layer 1 refresh cadence — when does the BudgetTracker call
`Client.RateLimitRemaining`?

- **(a) = Per sweep tick at minimum, plus on `resetAt` elapsed (recommended).**
  The leader refreshes its trackers once per tick before enqueueing.
  Cheap (one API call per installation per tick) and gives accurate budget
  visibility. The `resetAt` elapse triggers a refresh outside the tick
  cadence to pick up the hourly budget refill cleanly.
- (b) Refresh only on `resetAt` elapsed. Cheaper but stale between refreshes;
  webhook consumption isn't observed until the hour rolls.
- (c) Refresh after every Enqueue. Most accurate but pathological — N calls
  per tick. Rejected.
- other:

**(j)** Layer 1 vs Layer 2 — should the chart allow disabling jitter (Layer 2)
once Layer 1 is in place?

- **(a) = Keep both always on (recommended).** They address different failure
  modes (within-tick burst vs cross-tick synchronisation); the cost of
  jitter is negligible (one `rand.Int63n` per discovered repo). Removing
  one creates a sharp edge in deployment behaviour without operational
  upside.
- (b) Make Layer 2 opt-out via `discovery.jitterEnabled: false`. Lets
  operators experiment in test environments.
- other:

**(k)** Worker write-back failure semantics — what should happen when
`engine.CheckRepo` succeeds but `Store.UpdateRepoState` fails?

- **(a) = Log + count + continue (recommended).** The reconcile succeeded
  externally (PR created, file written, comment posted). The Store write is
  best-effort. If it fails, next StaleSweeper tick re-enqueues; the
  reconcile is idempotent so the duplicate work is wasted but not
  incorrect. Alarm via `store_writeback_total{outcome="error"}` rate.
- (b) Return the write-back error from `processJob`. The queue marks the job
  failed; reaper requeues it after `JOB_ACK_TIMEOUT`. Cleaner retry but
  causes a duplicate reconcile (the external work already happened on the
  first attempt).
- (c) Retry the write inline with backoff. Complicates the worker hot path
  for an unlikely failure mode.
- other:

**(l)** Write-back on partial reconcile success — `engine.CheckRepo` is a
single returned-error operation today, but internally it composes multiple
sub-steps (file checks, PR creation, reconciler runs). Should partial
internal failures (e.g., custom_properties reconciler errored) still trigger
write-back?

- **(a) = Yes — write-back if `engine.CheckRepo` returns nil; the engine
  decides what counts as success (recommended).** Keeps the worker simple
  and treats the engine as a black box. The engine's existing error
  semantics already handle partial failures (returning nil for "good
  enough" success or err for "retry").
- (b) Add a granular write-back interface where the engine returns a
  per-step status. Worker writes individual step timestamps. Adds complex
  state to Store rows. Premature.
- other:

## References

- `docs/operations/aws.md` § Cold-start validation — operator-facing context for
  the metrics-watching validation procedure
- `docs/operations/scaling.md` § Scaling for GitHub Enterprise — fleet-size
  context this design optimises for
- `internal/checker/sweep.go` header comment — explicit "the two coexist during
  the IMPL-0011 migration window" intent that this design closes out
- [DESIGN-0012: Persistent reconcile state and multi-replica coordination](0012-persistent-reconcile-state-and-multi-replica-coordination.md)
  — established the StaleSweeper pattern
- [IMPL-0011: Persistent reconcile state and multi-replica coordination](../impl/0011-persistent-reconcile-state-and-multi-replica-coordination.md)
  — opened the migration window this design closes
- [DESIGN-0015: Per-installation Valkey queue partitioning](0015-per-installation-valkey-queue-partitioning.md)
  — sibling design; fairness within the queue
- [DESIGN-0016: Separate Postgres read and write endpoints](0016-separate-postgres-read-and-write-endpoints.md)
  — sibling design; affects the Get + Upsert path Discoverer uses
