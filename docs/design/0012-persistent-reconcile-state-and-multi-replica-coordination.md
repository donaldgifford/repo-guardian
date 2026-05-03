---
id: DESIGN-0012
title: "Persistent reconcile state and multi-replica coordination"
status: Draft
author: Donald Gifford
created: 2026-05-02
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0012: Persistent reconcile state and multi-replica coordination

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-05-02

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

repo-guardian today keeps all reconcile state in memory. On pod
restart, the scheduler enqueues every repo from scratch; mid-sweep
restarts lose progress; and a multi-replica deployment would have
every pod racing on the same repo set. At ~1000 repos × ~5 API calls
per check, a cold sweep already approaches the hourly GitHub
rate-limit ceiling for a single installation. This design proposes
persisting `last_checked_at` per repo and adding multi-replica
coordination, in two phases scaled to operational need rather than
shipping a full distributed system on day one.

## Goals and Non-Goals

### Goals

- Restart-safe reconciliation: a pod restart should not redo work
  that completed within a configurable freshness window.
- Bounded API consumption per sweep: each tick should fit within
  the installation's hourly rate-limit budget, with the rest deferred
  to the next tick rather than burned and 429'd.
- Multi-replica coordination *when needed*: 2–5 pods should not
  reconcile the same repo simultaneously; sweep enqueueing should
  happen on exactly one pod at a time.
- Webhook-driven reconciles must remain stateless and parallel:
  every pod should be able to handle webhook traffic without
  consulting a leader.
- Phased rollout: ship V1 (single-replica + state) before V2
  (multi-replica + coordination); each phase must be independently
  useful and reversible.

### Non-Goals

- Cross-cluster coordination (single k8s cluster only).
- Distributed work queue with at-least-once semantics across all
  pods. The current in-memory queue + idempotent reconciles are
  sufficient if scoped appropriately.
- Backfilling historical `last_checked_at` from prior runs. New
  state starts empty; the first post-deploy sweep will look like
  a cold start, after which steady state takes over.
- HA-grade zero-downtime sweeping. Single-leader sweep with a
  brief gap during leader transitions is acceptable.
- Replacing the GitHub PR / branch as the source of truth for
  *what* needs to be reconciled. State tracks *when* we last
  looked, not *what* the desired state is.

## Background

Current architecture (relevant parts):

- `internal/scheduler/` ticks every `RECONCILE_INTERVAL` (default
  weekly), enumerates installations, and enqueues every repo.
- `internal/checker/queue.go` is a buffered channel + N worker
  goroutines, all in-process.
- `internal/checker/engine_policy.go` runs file rules + reconcilers
  per repo. After PR #69 (INV-0003), each check is idempotent at
  the GitHub API level.
- No persistence anywhere in the binary. PVCs / secrets are
  mounted but only for config and webhook secrets.

Failure modes the current design exhibits:

1. **Cold-start API burn.** At T=0 (pod boot) the scheduler runs
   immediately. At ~5 API calls/repo × 1000 repos = ~5000 calls,
   we approach the 5000/hr rate-limit ceiling on the first sweep
   of every restart.
2. **Restart amnesia.** Restart at T=20m loses the in-memory queue.
   The next pod restarts the sweep from scratch — repos checked
   in the prior 20 minutes get re-checked.
3. **Multi-replica race.** Running `replicas: 2` would have both
   pods enumerate the same repo set, double the API consumption,
   and double-create / contend on the same `repo-guardian/add-missing-files`
   branch (the engine fix from INV-0003 makes this safe but wasteful).

## Detailed Design

The design has two phases. V1 is the minimum viable state layer.
V2 layers coordination on top of V1 and is only activated when
the operator decides to scale beyond `replicas: 1`.

### Phase V1: Persistent freshness state (single-replica)

Add a small embedded state store recording, per repo:

- `installation_id`, `owner`, `repo` (composite key)
- `last_checked_at` (timestamp)
- `last_check_status` (`success` / `error` / `skipped`)
- `last_error` (nullable, last error string for debuggability)

Sweep behavior changes to:

1. On scheduler tick (or boot), query the store for repos where
   `last_checked_at IS NULL OR last_checked_at < NOW() - freshness_window`.
2. Sort by `last_checked_at ASC NULLS FIRST` so the longest-stale
   repos go first.
3. Enqueue at most `rate_budget = (remaining_rate_limit / avg_cost_per_repo)`
   repos this tick; the rest carry to the next tick.
4. After each job completes (success or error), write back
   `last_checked_at = NOW()`, `last_check_status`, `last_error`.

Webhooks bypass the freshness check entirely — an event always
triggers a reconcile, and the post-job write updates the
`last_checked_at` for free.

### Phase V2: Multi-replica coordination

Add three things on top of V1:

1. **k8s Lease-based leader election** for the sweeper role only.
   The leader runs the freshness query and enqueues jobs; followers
   only handle webhook events. Implementation: standard
   `k8s.io/client-go/tools/leaderelection` with a configurable
   lease duration (default 30s).
2. **Persistent job queue** replacing the in-memory channel. Workers
   poll the store with `SELECT ... FOR UPDATE SKIP LOCKED LIMIT 1`
   semantics, atomically claim jobs, execute, mark complete or
   failed-with-retry. This unlocks horizontal scaling of workers
   independent of leader role.
3. **Backend swap from SQLite to Postgres.** SQLite cannot serve
   multi-process `SKIP LOCKED` cleanly; this is the natural
   inflection point.

V2 is gated behind a config flag (e.g., `STORE_BACKEND=postgres`).
Operators on V1 keep SQLite + leader-irrelevant single-replica
behavior. Switching backends is one flag flip + a one-time data
migration script.

### Storage backend options (decision deferred to Open Questions)

| Option | V1 fit | V2 fit | Pros | Cons |
|--------|--------|--------|------|------|
| **SQLite (file on PVC)** | Strong | Weak | No new infra; embedded driver; single-pod simple | `SKIP LOCKED` is awkward; PVC pins pod to node; can't share across replicas |
| **Postgres (existing instance)** | Strong | Strong | Real concurrency; SKIP LOCKED; already in homelab | New ops dependency; migrations |
| **Redis** | Weak | Medium | Fast; SETNX for locks | In-memory durability concerns; another dep; weaker query semantics |
| **GitHub custom properties** | Weak | Weak | Zero new infra | Reads cost API calls — defeats the goal |
| **k8s ConfigMap/Secret** | Weak | Weak | No new infra | k8s API rate-limited; not write-heavy-friendly |

The pragmatic recommendation is SQLite for V1 (lowest blast radius)
with a clean abstraction so V2's swap to Postgres is a backend
implementation, not a redesign. Alternative: skip SQLite and go
straight to Postgres if the operator already runs one, accepting
the day-one ops cost in exchange for not migrating later.

### Coordination options (V2 only)

| Option | Pros | Cons |
|--------|------|------|
| **k8s Lease (leader election)** | Idiomatic; no new infra; well-tested | Single-leader bottleneck on sweep enqueue (acceptable) |
| **Postgres advisory locks** | Same DB as state; atomic | Postgres-only |
| **Redis SETNX with TTL** | Well-known pattern | Lock-TTL semantics are subtle; another dep |
| **Consistent hashing of repo → pod** | No central lock; horizontal scale | Pod-discovery complexity; rebalancing churn; harder to debug |

Recommendation: **k8s Lease for the sweeper role + Postgres SKIP
LOCKED for worker-side job claiming.** They serve different needs
— the lease prevents duplicate enqueueing, SKIP LOCKED prevents
duplicate execution.

## API / Interface Changes

New environment variables:

- `STORE_BACKEND` — `sqlite` (default) or `postgres`. Drives which
  driver is loaded.
- `STORE_DSN` — for SQLite: filesystem path (default
  `/var/lib/repo-guardian/state.db`). For Postgres: standard
  connection string.
- `RECONCILE_FRESHNESS` — duration (default `168h` = 7 days).
  Repos checked within this window are skipped at sweep time.
- `RATE_LIMIT_RESERVE` — fraction of the hourly limit to leave as
  headroom for webhook-driven work (default `0.2` = 20% reserved).
- `LEADER_ELECTION_ENABLED` — bool (default `false`). When true,
  uses k8s Lease for sweeper role.
- `LEADER_ELECTION_NAMESPACE` / `LEADER_ELECTION_NAME` — lease
  identity, defaults derived from pod env.

Helm chart additions:

- New PVC template (V1) gated on `persistence.enabled` (default
  `true` for SQLite backend).
- New `serviceAccount` permission to write Lease objects (V2 only,
  gated on `leaderElection.enabled`).
- New values keys: `persistence.size`, `persistence.storageClass`,
  `persistence.accessMode`.

No public API surface changes (HTTP endpoints unchanged). Internal
Go interface additions:

```go
type Store interface {
    GetRepoState(ctx, installationID int64, owner, repo string) (*RepoState, error)
    UpdateRepoState(ctx, *RepoState) error
    StaleRepos(ctx, freshness time.Duration, limit int) ([]RepoState, error)
}
```

A second interface for V2's job queue (`Enqueue`, `Claim`, `Complete`)
is introduced only when V2 ships.

## Data Model

Single table, both backends:

```sql
CREATE TABLE repo_state (
    installation_id BIGINT NOT NULL,
    owner TEXT NOT NULL,
    repo TEXT NOT NULL,
    last_checked_at TIMESTAMP WITH TIME ZONE,
    last_check_status TEXT NOT NULL DEFAULT 'pending',
    last_error TEXT,
    PRIMARY KEY (installation_id, owner, repo)
);

CREATE INDEX idx_repo_state_freshness
    ON repo_state(last_checked_at NULLS FIRST);
```

V2 adds:

```sql
CREATE TABLE reconcile_jobs (
    id BIGSERIAL PRIMARY KEY,
    installation_id BIGINT NOT NULL,
    owner TEXT NOT NULL,
    repo TEXT NOT NULL,
    trigger TEXT NOT NULL,        -- 'scheduler' | 'webhook' | 'push'
    enqueued_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    claimed_by TEXT,              -- pod identity
    claimed_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    status TEXT NOT NULL DEFAULT 'pending',
    error TEXT
);

CREATE INDEX idx_reconcile_jobs_pending
    ON reconcile_jobs(enqueued_at)
    WHERE claimed_at IS NULL;
```

Migration from V1 → V2: data preserved (schema is a superset).

## Testing Strategy

- **Unit tests** for the `Store` interface against both backends
  (SQLite via in-memory mode, Postgres via `testcontainers-go`).
- **Integration test** with a fake clock proving the freshness
  gate skips recently-checked repos and re-enqueues stale ones.
- **Concurrency test** (V2) spawning N goroutines all calling
  `Claim` against the same Postgres instance, asserting no job
  is claimed twice.
- **Restart-safety test** simulating a mid-sweep crash, asserting
  in-flight jobs are reclaimable on the next process boot.
- **Helm-unittest** for the new PVC template and the serviceAccount
  RBAC additions.
- **Homelab smoke test** before declaring V1 complete: deploy with
  `freshness=1h`, restart the pod 30m later, observe that no
  re-checks fire.

## Migration / Rollout Plan

1. **V1 ships with SQLite** behind a feature flag
   (`STORE_BACKEND=memory` keeps current behavior). Default is
   `sqlite` once V1 is validated in homelab.
2. **Helm chart bumps minor version** when V1 lands (new PVC,
   schema changes). Operators on the legacy in-memory mode can
   stay there during the transition.
3. **V1 retrospective** captures rate-limit measurements before
   designing V2.
4. **V2 ships with Postgres-or-SQLite split**. SQLite remains
   supported for single-replica deployments. Postgres is opt-in.
5. **Deprecation timeline**: in-memory `STORE_BACKEND=memory`
   removed two minor releases after V1 lands.

Rollback at any phase is a flag flip + helm rollback. State data
is non-load-bearing — losing it just causes one extra sweep cycle.

## Open Questions

1. **SQLite or skip-to-Postgres for V1?** SQLite is simpler but
   creates a future migration. Postgres is heavier but matches
   the homelab's existing infrastructure. The right answer depends
   on whether multi-replica is months away or never.
2. **Where does the freshness window come from per rule?** A repo
   might have a fast-moving rule (e.g., dependabot updates) and a
   slow-moving rule (e.g., CODEOWNERS). One global window may be
   too coarse.
3. **Rate-limit-aware scheduling vs. simple freshness gate.** Is
   the freshness window enough to fit within rate-limit budgets,
   or do we also need explicit budget arithmetic? Probably the
   former is sufficient at 1000-repo scale; the latter matters
   at 10000+.
4. **Sweep batch size.** Should we cap repos-per-tick? If yes,
   what's the default? Probably yes, with a default of N=200 or
   `min(rate_budget, 200)`.
5. **Exposing state via metrics.** Prometheus counters for
   `repos_skipped_freshness_total{reason}`,
   `sweep_batch_size`, `state_store_query_duration_seconds`?
6. **Operator override: force re-check.** Add a webhook endpoint
   or admin API that resets `last_checked_at` for a repo or
   installation? Useful for "the rule changed, recheck everyone."
7. **Job queue retention (V2).** How long do completed jobs stick
   around for audit? Cron job to delete completed > 30 days?
8. **Helm chart impact.** Does V1's PVC requirement break the
   "deploy with `helm install` and nothing else" promise from
   DESIGN-0005? Mitigation: PVC is opt-in (`persistence.enabled=false`
   keeps the in-memory store with documented restart cost).

## References

- IMPL-0010 (chart 0.3.3 published the binary this design extends)
- INV-0003 (engine bug whose fix made same-repo reconciles
  idempotent — prerequisite for safe multi-replica execution)
- DESIGN-0005 (Helm chart, the deployment surface this design
  modifies)
- GitHub REST API rate limits:
  https://docs.github.com/en/rest/overview/rate-limits-for-the-rest-api
- Kubernetes Lease API:
  https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#lease-v1-coordination-k8s-io
- Postgres SKIP LOCKED:
  https://www.postgresql.org/docs/current/sql-select.html#SQL-FOR-UPDATE-SHARE
