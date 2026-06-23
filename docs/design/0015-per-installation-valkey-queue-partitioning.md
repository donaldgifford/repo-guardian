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
  - [Contract tests (contract_test.go extension)](#contract-tests-contracttestgo-extension)
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
- Cross-installation work stealing. Strategy A (BLPOP-many) is naturally
  work-conserving; explicit work-stealing is out of scope for v1.
- Memory-backend partitioning. Memory mode is single-replica and the noisy-org
  problem doesn't arise there.

## Background

repo-guardian's queue today is a single LIST + ZSET pair:

```
repo-guardian:queue:jobs        LIST  (FIFO of pending jobs)
repo-guardian:queue:inflight    ZSET  (jobID → dispatch_ts; reaper input)
```

`Queue.Enqueue(job)` LPUSHes onto `jobs`. `Subscribe`-driven workers BRPOPLPUSH from
`jobs` into `inflight`, process, then ZREM the in-flight entry. The reaper goroutine
scans `inflight` periodically and requeues jobs whose dispatch_ts is older than
`JOB_ACK_TIMEOUT`.

At fleet scale (3000 repos across 25 GitHub orgs — see `docs/operations/aws.md`), one
large org can have 500+ actionable repos per sweep while smaller orgs have only a
handful. The single-FIFO queue means the noisy org's burst always enters before the
smaller orgs' webhook-triggered work, regardless of webhook arrival order. Workers
drain the noisy org's burst before getting to smaller orgs.

The existing `staleSweep.batchSize` and `config.workerCount` knobs give *eventual*
fairness — every org's work clears within a sweep cycle — but not strict round-robin.
For operators with per-org SLAs or sensitive webhook-driven workflows, eventual
fairness is insufficient.

## Detailed Design

### Key layout

Each known installation gets its own LIST + ZSET. A registry SET tracks known
installation IDs so workers and the reaper can iterate.

```
repo-guardian:queue:{install-12345}:jobs       LIST
repo-guardian:queue:{install-12345}:inflight   ZSET
repo-guardian:queue:{install-67890}:jobs       LIST
repo-guardian:queue:{install-67890}:inflight   ZSET
repo-guardian:queue:installations              SET  (member: stringified install ID)
```

Note the `{install-N}` hash-tag braces. Valkey treats text inside `{ }` as the hash
slot key in cluster mode. Tagging the LIST and ZSET for installation N together
guarantees they land on the same shard — required for multi-key Lua scripts that
operate atomically across the pair. The installations-registry SET has no tag because
no multi-key operation needs it alongside the per-installation keys.

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
| **A. BLPOP across all keys** | Worker reads `SMEMBERS installations`, issues `BLPOP key1 key2 … keyN <timeout>` — Valkey returns from whichever queue has work first | Trivial code; natural fairness for ready work; no extra coordination state | No strict cap on per-installation parallelism — a noisy org can still claim every worker slot |
| **B. Round-robin with per-installation concurrency cap** | Maintain `repo-guardian:queue:{install-N}:in_flight_count` counter; worker iterates installations in round-robin order, skips any at cap | Strict fairness; bounds noisy-installation parallelism | More state; atomic check-and-incr Lua script required |
| **C. Weighted random by depth + rate-limit headroom** | Worker picks an installation at random, weighted by `(queue_depth × rate_limit_remaining)` | Adapts dynamically to rate limits; statistically fair | Most state; depth + headroom snapshots; harder to reason about |

**Recommendation: A with a soft cap.** Combines BLPOP-many's simplicity with an
explicit per-installation in-flight ceiling. The cap is enforced application-side:
when a worker successfully BLPOPs from installation N's queue, it increments a local
counter for N; subsequent BLPOPs exclude N from the key list once N is at cap. The
counter decrements on job completion. No Valkey-side coordination needed.

```go
// Sketch
func (w *Worker) loop(ctx context.Context) {
    for ctx.Err() == nil {
        installs := w.snapshotInstallations()              // local cache, refreshed periodically
        eligible := w.filterAtCap(installs)                // exclude installations at perInstallationCap
        if len(eligible) == 0 { time.Sleep(idleBackoff); continue }
        keys := installToKeys(eligible)
        result, err := w.redis.BLPop(ctx, w.blpopTimeout, keys...).Result()
        // ... dispatch
    }
}
```

### Per-installation concurrency cap

Default formula:

```
perInstallationCap = max(2, ceil(workerCount / max(installations_count, 1)))
```

Examples (workerCount=15 per replica, 3 replicas → 45 concurrent workers total):

- 1 installation:  cap = 45 (effectively uncapped — only one tenant)
- 5 installations: cap = 9   (no one tenant exceeds 9 of 45)
- 25 installations: cap = 2  (noisy org limited to 2 of 45)

Operator override: `queue.partitioning.perInstallationCap` in values.yaml.

### Reaper changes

The current reaper scans one ZSET (`inflight`) on every tick. With partitioning, it
scans all per-installation inflight ZSETs:

```go
installs, err := r.redis.SMembers(ctx, "repo-guardian:queue:installations").Result()
for _, install := range installs {
    expired, err := r.redis.ZRangeByScore(ctx, fmt.Sprintf("repo-guardian:queue:{install-%s}:inflight", install), ...)
    // ... requeue expired
}
```

Per-tick cost grows O(installations). At 25 installations × 1 ZRANGEBYSCORE call = 25
Valkey ops per tick. With the default 30-second reaper interval, that's <1 op/sec.
Negligible.

### Installation lifecycle

**Discovery:** installations are added to the registry on first Enqueue (idempotent
SADD). The legacy `Sweeper` (and the proposed replacement per DESIGN-0017) handles
populating the registry initially.

**Removal:** uninstalling a GitHub App should remove the installation from the
registry. Handled via the existing `installation.deleted` webhook event — webhook
handler issues `SREM` and deletes the per-installation LIST + ZSET keys.

**Stale registry entries:** if a worker crashes mid-removal, an installation may
linger in the registry with no LIST. BLPOP on a non-existent key blocks waiting for
LPUSH; this is fine — no jobs ever arrive, the worker times out and moves on. A
janitor goroutine could periodically `EXISTS` each registry entry and SREM stale
ones, but it's not on the critical path.

### Compatibility with cluster mode

The hash-tag layout (`{install-N}`) ensures cluster-mode safety even though this
design doesn't target cluster mode in v1. If a future operator deploys against
ElastiCache cluster-mode-enabled, the per-installation keys still co-locate on one
shard (required for the multi-key BLPOP across multiple keys in a single ZRANGE+ZREM
Lua script).

Also requires the `cmd/repo-guardian/main.go` wiring to use `redis.NewUniversalClient`
(or detect cluster topology) instead of `redis.NewClient`. Out of scope for this
DESIGN — captured as a follow-up.

## API / Interface Changes

### `internal/queue/Queue` interface

Unchanged externally:

```go
type Queue interface {
    Enqueue(ctx context.Context, job Job) error
    Subscribe(ctx context.Context, handler Handler) error
    Close() error
}
```

The `Job` struct already carries `InstallationID`. The implementation selects the key
based on this field.

### `internal/queue/valkey/`

- LUA scripts rewritten to operate on per-installation keys.
- New private method `installKeys(installationID int64) (jobsKey, inflightKey string)`
  centralises key naming.
- BLPOP-multi loop in `Subscribe` polls all installations registered in
  `repo-guardian:queue:installations`.

### `internal/worker/`

- Per-worker local state: `inFlightByInstall map[int64]int`.
- Configurable `PerInstallationCap` (default per formula above).
- Filter eligible installations before BLPOP.

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
`repo-guardian:queue:jobs` and `repo-guardian:queue:inflight` keys until the
[migration](#migration--rollout-plan) drains them.

## Testing Strategy

### Unit tests (`internal/queue/valkey/`)

- Key-name selector correctness for various `installationID` values.
- Idempotent SADD on repeated Enqueue.
- BLPOP-multi returns from any non-empty installation queue.

### Contract tests (`contract_test.go` extension)

Add fairness assertions to the existing Queue contract suite:

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

- **(a) = Strategy A (BLPOP-many) with soft cap (recommended).** Simplest implementation;
  good enough for the vast majority of fleets.
- (b) Strategy B (round-robin with hard cap). Reach for it if BLPOP-many's
  "exclude-at-cap" application-side check proves insufficient (e.g., under sustained
  noisy-org bursts where the local cap counter races against worker startup).
- (c) Strategy C (weighted random by depth + headroom). Future enhancement only.
- other:

**(c)** Installation registry consistency mechanism.

- **(a) = Idempotent SADD on Enqueue, SREM on `installation.deleted` webhook, no
  periodic janitor (recommended).** Simplest; stale entries cause BLPOP timeouts
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
  — sibling design; partitioning + discovery cutover together close out the
  multi-replica scaling story
