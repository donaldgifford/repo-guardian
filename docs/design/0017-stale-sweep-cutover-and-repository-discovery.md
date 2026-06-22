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
  - [Cold-start enqueue pacing](#cold-start-enqueue-pacing)
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

- Eliminate redundant enqueueing on the Postgres backend. After cutover, each
  sweep tick enqueues only stale repos, not every repo.
- Cold-start a deployment against an existing 3000-repo fleet without burning the
  GitHub API rate-limit budget in the first hour.
- Keep discovery of new repos working without depending exclusively on webhooks
  (operators may have webhook delivery gaps, mis-configured installations, or
  brand-new installations with no historical webhooks).
- Preserve existing memory-backend behaviour: memory mode has no Store and
  continues to use the legacy Sweeper unchanged.
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

### Cold-start enqueue pacing

When a fresh deployment encounters an existing fleet, the Store is empty and
every repo qualifies as stale. The current StaleSweeper would issue
`StaleRepos(batchSize=200, …)` → enqueue 200 → repeat next tick → enqueue 200 →
… eventually catching up.

That pacing is correct in shape but breaks down because the **discovery step**
(populating the Store) happens via the legacy Sweeper, which doesn't pace —
it dumps all 3000 repos in one tick.

The fix: split discovery from enqueueing.

```
Discovery (slow, controlled):
    ListInstallations() → ListInstallationRepos() → for each repo not in Store: INSERT row with
    last_checked_at = NOW() - rand(0, freshness*2)

StaleSweeper (existing):
    StaleRepos(freshness, …) returns repos whose last_checked_at is older than freshness.
    The jittered initial last_checked_at staggers the cold-start enqueue across multiple
    sweep ticks.
```

The randomised initial `last_checked_at` means cold start enqueues a fraction of
the fleet per tick, naturally pacing against rate-limit budgets. Example with
freshness=24h, 3000 repos, sweep_interval=1h:

| Sweep tick | Repos with last_checked_at > 24h old | Enqueued (approx.) |
|---|---|---|
| 1 | ~125 (those in [24h, 26h] jitter band) | 125 |
| 2 | next 125 | 125 |
| … | … | … |
| 24 | last 125 | 125 |
| 25 | repos reconciled in tick 1 are now stale → cycle repeats | 125 |

Steady-state is reached in 24 sweep ticks (24 hours at 1h cadence). Worst-case
API consumption is ~125 repos × 10 calls = 1250 API calls per tick, well within
even a single installation's 5000/hr budget.

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

Two new env vars:

- `DISCOVERY_INTERVAL` — Go duration string, default `1h`.
- `DISCOVERY_ENABLED` — bool, default `true` when `STORE_BACKEND=postgres`,
  `false` otherwise.

### `internal/store/`

`Store` interface unchanged. The discovery path uses existing `Get` and `Upsert`
methods.

### `internal/webhook/`

Extend the `installation_repositories.added` and `repository.created` handlers
to write Store rows when the Postgres backend is active. Same idempotent
`Get`+conditional `Upsert` pattern.

### `internal/metrics/`

- `repo_guardian_repo_discovered_total{installation_id}` — counts newly
  discovered repos.
- `repo_guardian_discovery_duration_seconds` — histogram of Discover loop
  wall-clock time.
- `repo_guardian_discovery_api_calls_total{installation_id, endpoint}` —
  counts the ListInstallations / ListInstallationRepos / Get API calls each
  Discover tick makes.

### Chart values

```yaml
discovery:
  # NEW. Only effective when store.backend=postgres.
  enabled: true
  interval: "1h"
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

### Phase 1 — Land Discoverer alongside legacy Sweeper

- Implement `Discoverer.Discover` and new env vars; ship at default
  `DISCOVERY_ENABLED=false` for both backends.
- Existing deployments unaffected.

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
