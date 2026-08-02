---
id: DESIGN-0015
title: "Per-installation Valkey queue partitioning"
status: Draft
author: Donald Gifford
created: 2026-06-22
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0015: Per-installation Valkey queue partitioning

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-06-22

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Relationship to DESIGN-0021 (re-baselined 2026-07-29)](#relationship-to-design-0021-re-baselined-2026-07-29)
- [Detailed Design](#detailed-design)
  - [Key layout](#key-layout)
  - [Worker scheduling strategies](#worker-scheduling-strategies)
  - [Per-installation concurrency cap](#per-installation-concurrency-cap)
  - [Reaper changes](#reaper-changes)
  - [Installation lifecycle](#installation-lifecycle)
  - [Compatibility with cluster mode](#compatibility-with-cluster-mode)
- [API / Interface Changes](#api--interface-changes)
  - [internal/queue/Queue interface](#internalqueuequeue-interface)
  - [internal/queue/valkey/](#internalqueuevalkey)
  - [internal/worker/](#internalworker)
  - [internal/metrics/](#internalmetrics)
  - [Chart values](#chart-values)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
  - [Unit tests (internal/queue/valkey/)](#unit-tests-internalqueuevalkey)
  - [Behaviour tests (inline in valkeyintegrationtest.go)](#behaviour-tests-inline-in-valkeyintegrationtestgo)
  - [Integration tests (integrationtest.go)](#integration-tests-integrationtestgo)
  - [Chaos tests](#chaos-tests)
- [Migration / Rollout Plan](#migration--rollout-plan)
  - [Phase 1 — Feature flag (default off)](#phase-1--feature-flag-default-off)
  - [Phase 2 — Drain-and-cutover](#phase-2--drain-and-cutover)
  - [Phase 3 — Promote to default](#phase-3--promote-to-default)
  - [Rollback](#rollback)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

Split the single global Valkey queue (`repo-guardian:queue:jobs`) into per-installation
queues so that one noisy installation cannot starve work for the rest. Workers schedule
across the per-installation queues with bounded per-installation concurrency,
producing strict fairness and unlocking per-installation queue-depth metrics.

This is **partitioning** at the application layer — different conceptually from Valkey
cluster-mode sharding. See `docs/operations/aws.md` for the distinction.

## Goals and Non-Goals

### Goals

- Bound noisy-installation impact: a single installation cannot consume more than its
  fair share of worker slots.
- Per-installation observability: `queue_depth{installation_id}` and
  `queue_dispatched_total{installation_id}` exposed as Prometheus metric labels.
- Backward-compatible rollout: existing single-queue deployments continue to work;
  partitioning is opt-in via a chart value.
- Survives in both single-node Valkey and Valkey cluster-mode-disabled topologies
  (the recommended ElastiCache shape per `docs/operations/aws.md`).

### Non-Goals

- Valkey cluster-mode (sharding) support. Tracked separately — orthogonal axis. This
  design DOES leave the door open by keeping all per-installation keys for one
  installation in the same hash slot via tags, so a future cluster-mode rollout
  doesn't require redesign.
- Per-installation priority weights or QoS tiers. Future enhancement; the initial
  scheduler is round-robin with a uniform cap.
- Cross-installation work stealing. Strategy A (BRPOP-many) is naturally
  work-conserving; explicit work-stealing is out of scope for v1.

## Background

repo-guardian's queue today is a LIST + two ZSETs (the delayed set arrives with
DESIGN-0021):

```text
repo-guardian:queue:jobs        LIST   (FIFO of pending jobs; LPUSH / BRPOP)
repo-guardian:queue:in-flight   ZSET   (member = job JSON, score = claim nanos)
repo-guardian:queue:delayed     ZSET   (member = job JSON, score = due nanos — DESIGN-0021)
repo-guardian:lock:reaper       STRING (reaper leader lock, SETNX)
```

`Queue.Enqueue(job)` LPUSHes onto `jobs`. `Subscribe` consumers `BRPOP` a job, then
`ZADD` it into `in-flight` to claim it; a nil handler return ZREMs the entry (ack).
The reaper — one pod at a time, leader-elected via `lock:reaper` — ticks every
`REAPER_INTERVAL` (default 1m) and atomically requeues in-flight entries older than
`JOB_ACK_TIMEOUT` (default 5m) via a Lua script; under DESIGN-0021 the same tick also
promotes due entries from `delayed` back to `jobs`.

At fleet scale (3000 repos across 25 GitHub orgs — see `docs/operations/aws.md`), one
large org can have 500+ actionable repos per sweep while smaller orgs have only a
handful. The single-FIFO queue means the noisy org's burst always enters before the
smaller orgs' webhook-triggered work, regardless of webhook arrival order. Workers
drain the noisy org's burst before getting to smaller orgs.

The existing `staleSweep.batchSize` and `config.workerCount` knobs give *eventual*
fairness — every org's work clears within a sweep cycle — but not strict round-robin.
For operators with per-org SLAs or sensitive webhook-driven workflows, eventual
fairness is insufficient.

## Relationship to DESIGN-0021 (re-baselined 2026-07-29)

[DESIGN-0021](0021-delayed-requeue-job-contract-and-rate-limit-consolidation.md)
was drafted after this design and changes both the facts underneath it and part of
its motivation. This design now sequences **after** 0021, and its go/no-go becomes
data-driven rather than speculative.

**What 0021 removes from the motivation.** The worst starvation mode in the original
framing was a *rate-limited* noisy org: workers pull its jobs and block in the
transport's pre-emptive sleep, so one org's exhausted budget parks the entire worker
pool. 0021 eliminates that mode outright. Because installation clients are cached
(`client.go.getInstallClient` — one `rateLimitTransport` per installation, shared
across jobs), once one job observes exhaustion every subsequent job for that
installation gets `ThrottledError` from cached state with **zero API calls**, defers
to the delayed set, and frees its worker slot in milliseconds. The residual
motivation for this design is narrower: plain FIFO burst starvation by a large org
doing *real, unthrottled* work.

**The go/no-go datum.** 0021 Phase 5 adds `queue_wait_seconds{installation_id}`
(enqueue→dispatch latency, measurable today because `Job` carries both
`InstallationID` and `EnqueuedAt`). That histogram directly measures the starvation
this design exists to fix, without partitioning anything. Proceed with this design
only if soak data shows small installations' wait times degrading under large
installations' bursts; otherwise it stays Draft.

**Mechanical deltas this design must absorb from 0021:**

- **A third per-installation key.** Each installation gets
  `{install-N}:delayed` alongside `jobs` and `inflight`, sharing the installation's
  hash tag — 0021's `deferScript` is a multi-key Lua across in-flight + delayed, so
  the pair must co-locate in cluster mode exactly like the requeue pair.
- **Scripts are already partition-ready.** `deferScript` / `promoteScript` /
  `requeueScript` take their keys as `KEYS[]` parameters, so partitioning changes
  only Go-side key selection (the `installKeys` helper), not the Lua.
- **Promotion joins the reaper's per-installation iteration.** The reaper loop that
  scans each installation's inflight ZSET also promotes each installation's delayed
  ZSET. Same O(installations)-per-tick cost analysis; still negligible.
- **`EnqueueAfter` routes like `Enqueue`.** Key selection by `job.InstallationID`
  applies to both producer methods.
- **`Attempts`/`AvailableAt`** travel inside the job JSON and are key-layout
  agnostic — no interaction.

**New capability unlocked (future extension, not v1).** Once queues are
per-installation, a deferral for installation N can pause N's *entire queue*: record
`paused_until = ThrottledError.ResetAt` and drop N from the BRPOP-many key list until
it passes. Today each of N's queued jobs must individually claim → defer; with
partitioning one deferral parks all of them for free. This subsumes Strategy C's
"rate-limit headroom" weighting with a measured signal instead of a modelled one,
and is the strongest 0021-era argument *for* this design if the wait-time data ever
justifies it.

## Detailed Design

### Key layout

Each known installation gets its own LIST + ZSET. A registry SET tracks known
installation IDs so workers and the reaper can iterate.

```text
repo-guardian:queue:{install-12345}:jobs       LIST
repo-guardian:queue:{install-12345}:inflight   ZSET
repo-guardian:queue:{install-12345}:delayed    ZSET  (DESIGN-0021 delayed set)
repo-guardian:queue:{install-67890}:jobs       LIST
repo-guardian:queue:{install-67890}:inflight   ZSET
repo-guardian:queue:{install-67890}:delayed    ZSET
repo-guardian:queue:installations              SET  (member: stringified install ID)
```

Note the `{install-N}` hash-tag braces. Valkey treats text inside `{ }` as the hash
slot key in cluster mode. Tagging all three keys for installation N together
guarantees they land on the same shard — required for the multi-key Lua scripts that
operate atomically across pairs (`requeueScript`: inflight→jobs; `deferScript`:
inflight→delayed; `promoteScript`: delayed→jobs). The installations-registry SET has
no tag because no multi-key operation needs it alongside the per-installation keys.

`Enqueue(job)` becomes:

```redis
LPUSH repo-guardian:queue:{install-<job.InstallationID>}:jobs <job-json>
SADD  repo-guardian:queue:installations <job.InstallationID>
```

Both ops; SADD is idempotent so no harm if the installation already exists in the
registry.

### Worker scheduling strategies

Three workable shapes considered. Recommended initial implementation is **A with a
per-installation soft cap**; B and C are future extensions if A proves insufficient.

| Strategy | How it works | Pros | Cons |
|---|---|---|---|
| **A. BRPOP across all keys** | Worker reads `SMEMBERS installations`, issues `BRPOP key1 key2 … keyN <timeout>` — Valkey returns from whichever queue has work first | Trivial code; natural fairness for ready work; no extra coordination state | No strict cap on per-installation parallelism — a noisy org can still claim every worker slot |
| **B. Round-robin with per-installation concurrency cap** | Maintain `repo-guardian:queue:{install-N}:in_flight_count` counter; worker iterates installations in round-robin order, skips any at cap | Strict fairness; bounds noisy-installation parallelism | More state; atomic check-and-incr Lua script required |
| **C. Weighted random by depth + rate-limit headroom** | Worker picks an installation at random, weighted by `(queue_depth × rate_limit_remaining)` | Adapts dynamically to rate limits; statistically fair | Most state; depth + headroom snapshots; harder to reason about |

**Recommendation: A with a soft cap.** Combines BRPOP-many's simplicity with an
explicit per-installation in-flight ceiling. The cap is enforced application-side:
when a worker successfully BRPOPs from installation N's queue, it increments a local
counter for N; subsequent BRPOPs exclude N from the key list once N is at cap. The
counter decrements on job completion. No Valkey-side coordination needed.

```go
// Sketch
func (w *Worker) loop(ctx context.Context) {
    for ctx.Err() == nil {
        installs := w.snapshotInstallations()              // local cache, refreshed periodically
        eligible := w.filterAtCap(installs)                // exclude installations at perInstallationCap
        if len(eligible) == 0 { time.Sleep(idleBackoff); continue }
        keys := installToKeys(eligible)
        result, err := w.redis.BRPop(ctx, w.brpopTimeout, keys...).Result()
        // ... dispatch
    }
}
```

### Per-installation concurrency cap

Default formula:

```text
perInstallationCap = max(2, ceil(workerCount / max(installations_count, 1)))
```

Examples (workerCount=15 per replica, 3 replicas → 45 concurrent workers total):

- 1 installation:  cap = 45 (effectively uncapped — only one tenant)
- 5 installations: cap = 9   (no one tenant exceeds 9 of 45)
- 25 installations: cap = 2  (noisy org limited to 2 of 45)

Operator override: `queue.partitioning.perInstallationCap` in values.yaml.

### Reaper changes

The current reaper scans two ZSETs on every tick: `in-flight` (requeue expired
claims) and, post-DESIGN-0021, `delayed` (promote due jobs). With partitioning, it
scans both per installation:

```go
installs, err := r.redis.SMembers(ctx, "repo-guardian:queue:installations").Result()
for _, install := range installs {
    jobs, inflight, delayed := installKeys(install)
    // requeueScript: expired inflight entries → jobs (existing)
    // promoteScript: due delayed entries → jobs (DESIGN-0021)
}
```

Per-tick cost grows O(installations). At 25 installations × 2 script calls = 50
Valkey ops per tick. With the default 1-minute `REAPER_INTERVAL`, that's <1 op/sec.
Negligible.

### Installation lifecycle

**Discovery:** installations are added to the registry on first Enqueue (idempotent
SADD). The `Discoverer` (shipped in IMPL-0015 Phase 1, replacing the legacy
`Sweeper` this draft originally referenced) populates `repo_state`, and the
`StaleSweeper`'s enqueues populate the registry from there.

**Removal:** uninstalling a GitHub App should remove the installation from the
registry. Handled via the existing `installation.deleted` webhook event — webhook
handler issues `SREM` and deletes the per-installation LIST + ZSET keys.

**Stale registry entries:** if a worker crashes mid-removal, an installation may
linger in the registry with no LIST. BRPOP on a non-existent key blocks waiting for
LPUSH; this is fine — no jobs ever arrive, the worker times out and moves on. A
janitor goroutine could periodically `EXISTS` each registry entry and SREM stale
ones, but it's not on the critical path.

### Compatibility with cluster mode

The hash-tag layout (`{install-N}`) ensures cluster-mode safety even though this
design doesn't target cluster mode in v1. If a future operator deploys against
ElastiCache cluster-mode-enabled, each installation's three keys still co-locate on
one shard — required for every multi-key Lua transition (`requeueScript`,
`deferScript`, `promoteScript`) and for multi-key `BRPOP` within that installation.
Note the *global* keys of the pre-partitioned layout are cluster-mode-unsafe for the
same scripts; partitioning is what makes cluster mode possible at all, which is why
the tags are in the v1 layout despite v1 not targeting cluster mode.

Also requires the `cmd/repo-guardian/main.go` wiring to use `redis.NewUniversalClient`
(or detect cluster topology) instead of `redis.NewClient`. Out of scope for this
DESIGN — captured as a follow-up.

## API / Interface Changes

### `internal/queue/Queue` interface

Unchanged externally — this design adds no methods to the post-DESIGN-0021 surface:

```go
type Queue interface {
    Enqueue(ctx context.Context, j Job) error
    Subscribe(ctx context.Context, handler func(context.Context, Job) error) error
    Close() error
    EnqueueAfter(ctx context.Context, j Job, at time.Time) error // DESIGN-0021
}
```

The `Job` struct already carries `InstallationID`. The implementation selects keys
from this field — for `Enqueue`, `EnqueueAfter`, claim, defer, and promote alike.

### `internal/queue/valkey/`

- Lua scripts need **no rewrite** — `requeueScript`, `deferScript`, and
  `promoteScript` take their keys as `KEYS[]` parameters. Only the Go-side key
  selection changes.
- The key-naming helper (`installKeys(installationID) (jobs, inflight, delayed
  string)`) extends the single key-construction point DESIGN-0021 Phase 2
  establishes.
- BRPOP-multi loop in `Subscribe` polls all installations registered in
  `repo-guardian:queue:installations`.

### `internal/worker/`

- Per-worker local state: `inFlightByInstall map[int64]int`.
- Configurable `PerInstallationCap` (default per formula above).
- Filter eligible installations before BRPOP.

### `internal/metrics/`

- `repo_guardian_queue_depth` becomes `repo_guardian_queue_depth{installation_id}` —
  GaugeVec rather than Gauge.
- `repo_guardian_queue_dispatched_total` gains the same label.
- `repo_guardian_queue_partitions` (new) — count of registered installations.
- Cardinality budget: 25 installations × 4 series = 100 series. Negligible.

### Chart values

```yaml
queue:
  valkey:
    partitioning:
      enabled: false               # opt-in during rollout
      perInstallationCap: ""       # empty → formula default; integer to override
```

## Data Model

No schema changes (queue is Valkey, not Postgres). Key namespace evolves as
described in [Key layout](#key-layout). Existing single-queue deployments retain the
`repo-guardian:queue:jobs`, `repo-guardian:queue:in-flight`, and
`repo-guardian:queue:delayed` keys until the
[migration](#migration--rollout-plan) drains them.

## Testing Strategy

### Unit tests (`internal/queue/valkey/`)

- Key-name selector correctness for various `installationID` values.
- Idempotent SADD on repeated Enqueue.
- BRPOP-multi returns from any non-empty installation queue.

### Behaviour tests (inline in `valkey_integration_test.go`)

The multi-backend contract suite this draft originally targeted is gone —
post-IMPL-0016 there is one Queue backend and contract assertions live inline under
the `integration` build tag (see `internal/queue/valkey/valkey_integration_test.go`).
Add fairness assertions there:

- **Strict fairness under burst:** enqueue 100 jobs for install-A, 1 job for
  install-B; with `perInstallationCap=2` and 5 workers, install-B's job dispatches
  within 1 second of install-A's burst arrival.
- **No starvation under uniform load:** enqueue 50 jobs for each of 5 installations;
  every installation's first job dispatches before any installation's tenth.
- **Cap enforcement:** with `perInstallationCap=3`, no worker dispatches more than 3
  concurrent jobs for a single installation.

### Integration tests (`_integration_test.go`)

- Run the existing in-flight + reaper integration tests with the new key layout.
- Add ElastiCache test target (`cache.t4g.micro`) running alongside the existing
  testcontainer Valkey target.

### Chaos tests

- Worker crash mid-job: verify reaper requeues from the correct per-installation
  inflight ZSET.
- Installation uninstall during active processing: verify `installation.deleted`
  webhook drains the queue cleanly.

## Migration / Rollout Plan

### Phase 1 — Feature flag (default off)

- Land partitioning behind `queue.valkey.partitioning.enabled=false`.
- Existing deployments unaffected.
- Operators opt in by enabling the flag + bumping the chart.

### Phase 2 — Drain-and-cutover

- When `enabled=true`, binary on startup:
  1. Drains residual jobs from the legacy `repo-guardian:queue:jobs` LIST by
     re-enqueueing them through the new partitioning logic (jobs already carry
     `InstallationID`; they route to per-installation keys).
  2. Deletes the legacy keys when drained.
  3. Resumes normal operation against the per-installation key space.

- The drain is idempotent — repeated restarts during the migration window do not
  duplicate work.

### Phase 3 — Promote to default

After ≥ 1 release cycle of opt-in production usage:

- Flip the chart default to `enabled=true`.
- Document the migration recipe in `docs/operations/`.
- Mark the legacy single-key code path deprecated; remove in a future release.

### Rollback

If a bug surfaces in partitioning, operators set `enabled=false` and restart. The
binary drains residual partitioned jobs back into the legacy LIST on startup (mirror
of the Phase 2 drain).

## Open Questions

**(a)** Per-installation cap formula.

- **(a) = `max(2, ceil(workerCount / installations_count))` (recommended).** Scales
  inversely with fleet breadth; gives single-tenant deployments effectively-uncapped
  behaviour and multi-tenant deployments strict fairness.
- (b) Static cap (`perInstallationCap=N` fixed by operator). Simpler, but requires
  retuning whenever installations are added or workerCount changes.
- (c) Dynamic cap derived from per-installation rate-limit headroom. More fair
  under unequal rate-limit budgets (e.g., GHEC orgs at 12.5k/hr vs non-enterprise at
  5k/hr); much more state.
- other:

**(b)** Worker scheduling strategy at GA.

- **(a) = Strategy A (BRPOP-many) with soft cap (recommended).** Simplest implementation;
  good enough for the vast majority of fleets.
- (b) Strategy B (round-robin with hard cap). Reach for it if BRPOP-many's
  "exclude-at-cap" application-side check proves insufficient (e.g., under sustained
  noisy-org bursts where the local cap counter races against worker startup).
- (c) Strategy C (weighted random by depth + headroom). Future enhancement only.
- other:

**(c)** Installation registry consistency mechanism.

- **(a) = Idempotent SADD on Enqueue, SREM on `installation.deleted` webhook, no
  periodic janitor (recommended).** Simplest; stale entries cause BRPOP timeouts
  but no correctness issue.
- (b) Periodic janitor SCAN/EXISTS pass. Defensive but more code. Easy to add later
  if stale entries accumulate.
- other:

**(d)** Per-installation metric cardinality cap.

- **(a) = No cap (recommended).** Realistic fleets are < 100 installations; 4 series
  × 100 = 400 series per metric, well under Prometheus practical limits.
- (b) Cardinality cap (e.g., top-N by depth). Defensive but adds aggregation
  complexity. Premature optimisation.
- other:

**(e)** Drain-on-cutover behaviour during Phase 2.

- **(a) = Auto-drain on startup, idempotent (recommended).** Operators flip the flag
  and restart; the binary handles the migration transparently.
- (b) Operator-driven drain via a CLI subcommand (e.g.,
  `repo-guardian queue migrate-partitioned`). More explicit, less convenient.
- other:

## References

- `docs/operations/aws.md` § Partitioning, sharding, and the noisy-org problem —
  explains the distinction and the user-visible motivation
- `docs/operations/scaling.md` § Scaling for GitHub Enterprise (multi-org,
  multi-thousand-repo) — the fleet shape this design targets
- [DESIGN-0012: Persistent reconcile state and multi-replica coordination](0012-persistent-reconcile-state-and-multi-replica-coordination.md)
  — established the Queue + Scheduler interfaces this design extends
- [IMPL-0011: Persistent reconcile state and multi-replica coordination](../impl/0011-persistent-reconcile-state-and-multi-replica-coordination.md)
  — implementation of DESIGN-0012; baseline for this design's deltas
- [DESIGN-0017: Stale-sweep cutover and repository discovery](0017-stale-sweep-cutover-and-repository-discovery.md)
  — shipped via IMPL-0015; its Discoverer populates the repo_state this design's
  registry hangs off
- [DESIGN-0021: Delayed-requeue job contract and rate-limit consolidation](0021-delayed-requeue-job-contract-and-rate-limit-consolidation.md)
  — sequences before this design; supplies the delayed set, the key-naming
  centralisation, and the `queue_wait_seconds{installation_id}` go/no-go datum (see
  [Relationship to DESIGN-0021](#relationship-to-design-0021-re-baselined-2026-07-29))
- [INV-0012](../investigation/0012-inert-budgettracker-and-untrustworthy-alert-pack.md)
  — the investigation that produced DESIGN-0021; finding I (lease-outliving sleeps)
  is the starvation mode 0021 removes from this design's motivation
