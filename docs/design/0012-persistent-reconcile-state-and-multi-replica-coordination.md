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
  - [Architectural shape](#architectural-shape)
  - [Store interface](#store-interface)
  - [Queue interface](#queue-interface)
  - [Scheduler interface](#scheduler-interface)
  - [Sweep flow](#sweep-flow)
  - [Why not Postgres for the queue too](#why-not-postgres-for-the-queue-too)
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
rate-limit ceiling for a single installation. This design introduces
durable persistence for state and queue, with **interface boundaries**
between the engine and any concrete backend so that the storage,
queue, and scheduler implementations can be swapped without engine
changes. Day-one defaults: **Postgres** for state and **Valkey** for
queue + scheduler-lock, both shipped baked into the Helm chart for
the simplest install path. Operators can override either to use
external infra (RDS, ElastiCache, CNPG cluster) or the in-memory
mode for try-it-out / no-dep deployments.

## Goals and Non-Goals

### Goals

- **Durability over pragmatism.** Surviving a pod crash mid-sweep
  must not redo completed work. State and in-flight jobs survive
  restarts when the chart is configured with the durable backends.
- **Interface-first architecture.** `Store`, `Queue`, and `Scheduler`
  are Go interfaces with multiple implementations available from
  day one. The engine depends only on the interfaces; concrete
  backends are selected at startup. Same principle as the `Provider`
  interface from INV-0004.
- **`helm install` just works.** Default chart config bakes Postgres
  and Valkey into the install — no external dependencies required
  for a standalone production-ish deployment. Both are gated on
  values flags so operators can disable either to point at their own
  infra.
- **Bring-your-own infra for every backend.** Each baked dependency
  has at least one external alternative:
  - Postgres: `external` (DSN), `cnpg` (chart renders a CNPG
    `Cluster` CR if the operator is installed), or `baked`
    (chart-managed Deployment + PVC).
  - Valkey: `external` (DSN — works with AWS ElastiCache,
    Cloud Memorystore, etc.) or `baked` (chart-managed Deployment
    + PVC).
- **No-dep mode is a first-class supported configuration.** Because
  every interface has an in-memory implementation, an operator can
  run repo-guardian with zero external dependencies (no Postgres,
  no Valkey) by selecting the `memory` backends. Restart-amnesia
  and single-replica are accepted tradeoffs in this mode; it is
  not a "test only" path that gets deprecated.
- **Multi-replica safe by default.** No mode where running
  `replicas: 2` is unsafe (with the durable backends). Coordination
  primitives are part of the V1 design, not bolted on later.
- **Bounded API consumption per sweep.** Each scheduler tick fits
  within the installation's hourly rate-limit budget; remaining
  repos carry to the next tick.
- **Webhook-driven reconciles remain parallel** across all replicas
  — they enqueue like any other job source, and any worker can
  claim them.

### Non-Goals

- Cross-cluster coordination. Single k8s cluster only.
- Replacing GitHub PR / branch state as the source of truth for
  *what* needs reconciling. State tracks *when we last looked*,
  not *desired configuration*.
- Backfilling historical `last_checked_at`. New state starts empty;
  the first post-deploy sweep is a cold start, then steady state.
- Sub-second leader failover during sweep transitions. Valkey's
  lock-TTL semantics give a few-seconds gap on leader pod death;
  acceptable.
- A standalone NATS deployment or embedded NATS cluster. NATS is
  reserved as a future `Queue` / `Scheduler` implementation if the
  Valkey-based defaults don't fit a future need.
- Bitnami images or charts. Valkey ships as a self-contained
  resource in our chart using the official `valkey/valkey` image.

## Background

Current architecture (relevant parts):

- `internal/scheduler/` ticks every `RECONCILE_INTERVAL` (default
  weekly), enumerates installations, and enqueues every repo.
- `internal/checker/queue.go` is a buffered channel + N worker
  goroutines, all in-process.
- `internal/checker/engine_policy.go` runs file rules + reconcilers
  per repo. After PR #69 (INV-0003), each check is idempotent at
  the GitHub API level.
- No persistence anywhere in the binary.

Failure modes the current design exhibits:

1. **Cold-start API burn.** At T=0 (pod boot) the scheduler runs
   immediately. ~5 API calls/repo × 1000 repos = ~5000 calls
   approaches the 5000/hr installation rate-limit ceiling on every
   restart.
2. **Restart amnesia.** Restart at T=20m loses the in-memory queue.
   The next pod redoes work that was just completed.
3. **Multi-replica race.** Running `replicas: 2` would have both
   pods enumerate the same repo set, double API consumption, and
   contend on the same `repo-guardian/add-missing-files` branch.
   The INV-0003 fix makes contention safe but wasteful.

Why Valkey (not Redis, not embedded NATS):

- **Valkey is the Linux Foundation BSD fork of Redis,** kept open
  after Redis's BSL/SSPL relicense. API-compatible with Redis at
  the wire protocol level, so the Go client (`github.com/redis/go-redis/v9`)
  works unchanged against either.
- **Operationally simple.** A single Valkey instance is plenty for
  our scale (1000s of repos, < 100 jobs/min steady state, ~kHz
  burst). Familiar SETNX-based locks for leader election; LPUSH /
  BRPOP atomic queue semantics. No clustering complexity unless
  the operator opts in.
- **Durability is sufficient for the queue.** With idempotent
  reconciles (post-INV-0003), losing in-flight queue contents on a
  Valkey restart just means the next sweep tick re-enqueues. AOF
  persistence on the baked Valkey gives at-most-1-second loss
  window.
- **No NATS embedded clustering.** Considered in earlier drafts of
  this design; rejected because the StatefulSet + headless Service
  + per-pod PVC + raft-quorum bootstrap was disproportionate
  complexity for a queue at our scale. The interface boundary
  preserves the option to swap to NATS later if Valkey doesn't fit.
- **Not Bitnami.** Valkey ships from the project's own
  `valkey/valkey` image; no Bitnami image fork or chart dependency.

Why Postgres for state (and not Valkey or embedded SQLite):

- The state queries we care about are relational with order-by:
  "give me the N most-stale repos with `last_checked_at < ?`".
  Trivial in SQL, awkward in Valkey's KV-or-sorted-set model.
- CNPG in homelab and RDS in production are both well-trodden;
  no new database-class infra to introduce. Plus the chart can
  bake a quickstart Postgres for ops who don't already have one.
- Connection pooling, migrations, observability are all solved
  problems for Postgres clients in Go (`pgx`, `golang-migrate`).

## Detailed Design

### Architectural shape

```
            ┌─────────────────────────────────────┐
            │            Engine                   │
            │  (file rules, reconcilers,          │
            │   Provider, idempotent ops)         │
            └────────┬────────┬────────┬──────────┘
                     │        │        │
                     ▼        ▼        ▼
                  Store    Queue   Scheduler   ← Go interfaces
                     │        │        │
                     ▼        ▼        ▼
               Postgres   Valkey   Valkey-lock
                                   (or k8s Lease)
```

The engine never imports `pgx`, `redis`, or any concrete client.
It receives `Store`, `Queue`, and `Scheduler` instances at
construction time and uses only their interface methods. Concrete
implementations live in `internal/store/postgres`,
`internal/queue/valkey`, `internal/scheduler/valkey`. Test code
uses `internal/store/memory` etc.

This pairs with the `Provider` interface from INV-0004
(`internal/scm.Provider`, GitHub-only V1) — same interface-first
principle applied to the SCM backend.

### `Store` interface

Persistent per-repo state.

```go
package store

type RepoState struct {
    InstallationID  int64
    Owner           string
    Repo            string
    LastCheckedAt   *time.Time
    LastCheckStatus string  // "success" | "error" | "skipped" | "pending"
    LastError       string
    PolicyVersion   string  // hash of active policy at last write
}

type Store interface {
    GetRepoState(ctx context.Context, installationID int64, owner, repo string) (*RepoState, error)
    UpdateRepoState(ctx context.Context, s *RepoState) error
    // StaleRepos returns rows whose last_checked_at is older than
    // freshness OR whose policy_version differs from currentPolicyVersion.
    // The latter handles the new-rule-added scenario without per-rule
    // freshness tracking — see Open Question #2.
    StaleRepos(ctx context.Context, freshness time.Duration, currentPolicyVersion string, limit int) ([]RepoState, error)
    Close() error
}
```

Implementations:

- `store/postgres` — durable, multi-replica safe. Uses `pgx/v5` +
  `golang-migrate`.
- `store/memory` — in-process map. Supported for both tests and
  no-dep single-replica deployments. Restart loses all state;
  operator accepts that tradeoff.

### `Queue` interface

Durable work queue. At-least-once delivery; consumer-side dedupe
is handled by the engine's existing idempotent reconcile path
(per INV-0003).

```go
package queue

type Job struct {
    ID              string
    InstallationID  int64
    Owner           string
    Repo            string
    Trigger         string  // "scheduler" | "webhook" | "push"
    EnqueuedAt      time.Time
}

type Queue interface {
    Enqueue(ctx context.Context, j Job) error
    Subscribe(ctx context.Context, handler func(context.Context, Job) error) error
    Close() error
}
```

The `Subscribe` handler is invoked once per claimed job; the
implementation acks on `nil` return and nacks-with-retry on error.
Workers are goroutines inside the binary; the queue manages the
claim/ack lifecycle.

Implementations:

- `queue/valkey` — Valkey list-as-queue with `BRPOP` for blocking
  consumer waits and `LPUSH` for enqueue. Worker consumes with a
  per-job ack pattern using a separate "in-flight" sorted set keyed
  by claim timestamp; orphaned in-flight jobs (claimed pod died)
  are reaped after a configurable timeout (default 5 minutes).
  Multi-replica safe: every worker pod connects to the same Valkey,
  `BRPOP` is atomic, no two workers see the same job.
- `queue/memory` — in-process buffered channel. Supported for tests
  and no-dep single-replica deployments. Restart loses any
  in-flight queue contents; webhook events arriving during a
  restart window are dropped at the HTTP layer.
- *Future option:* `queue/nats` — embedded NATS JetStream if Valkey
  doesn't meet a future need (e.g., higher throughput, cross-cluster
  replication). The interface stays the same.
- *Future option:* `queue/postgres` — `SELECT ... FOR UPDATE SKIP
  LOCKED` if a deployment wants to consolidate on Postgres only.

### `Scheduler` interface

Periodic ticks that fire on exactly one pod cluster-wide.

```go
package scheduler

type Scheduler interface {
    Schedule(ctx context.Context, name string, interval time.Duration, handler func(context.Context) error) error
    Stop() error
}
```

Implementations:

- `scheduler/valkey` — every pod runs a `time.Ticker` at the
  configured interval; on each fire, the pod attempts to acquire a
  Valkey lock via `SET key value NX EX <ttl>`. If acquired, the
  pod runs the handler; if not, the pod skips this tick (some
  other pod is leading). The lock TTL is set slightly longer than
  the handler's expected runtime so leader hand-off is automatic
  on pod death. No standing leader role — each tick is an
  independent acquisition. Same Valkey instance as the queue, so
  no extra dependency.
- `scheduler/ticker` — `time.Ticker` for tests and no-dep
  single-replica deployments. Each pod's ticker fires
  independently, so this implementation must not be paired with
  multi-replica without external coordination.
- *Future option:* `scheduler/k8s-lease` — k8s `Lease` API for
  leader election if some operator wants to avoid Valkey for
  scheduling alone. Not necessary if Valkey is already deployed
  for the queue.
- *Future option:* `scheduler/nats` — JetStream-based tick
  delivery if NATS replaces Valkey at the queue layer.

### Sweep flow

1. `Scheduler` fires the sweep tick (Valkey-lock-coordinated; only
   the lock-holder pod executes).
2. Sweep handler queries
   `Store.StaleRepos(freshness, currentPolicyVersion, batch_size)`.
3. For each stale repo, calls `Queue.Enqueue(Job{...})`.
4. Worker pods (all replicas) `Subscribe` to the queue, claim jobs
   via Valkey `BRPOP` + in-flight set, run the engine on the repo.
5. On success or error, worker calls `Store.UpdateRepoState(...)`
   and removes the job from the in-flight set.
6. Webhook handler bypasses the freshness check; it always
   `Queue.Enqueue`s a job. Same worker pool consumes it.

### Backend modes and chart deployment shapes

Each interface has multiple implementations selected via chart
values. The chart renders different resources depending on the
combination:

| Mode | `Store` | `Queue` | `Scheduler` | Chart resources rendered |
|---|---|---|---|---|
| **No-dep** | `memory` | `memory` | `ticker` | Deployment only (single replica required) |
| **Baked** *(default)* | `baked-postgres` | `baked-valkey` | `valkey-lock` | Deployment + Postgres Deployment + Valkey Deployment + PVCs + Services |
| **CNPG + baked Valkey** | `cnpg` | `baked-valkey` | `valkey-lock` | Deployment + CNPG `Cluster` CR + Valkey Deployment + Valkey PVC + Services |
| **External Postgres + ElastiCache** | `external` | `external` | `valkey-lock` | Deployment only (operator manages external infra) |
| **External Postgres + baked Valkey** | `external` | `baked-valkey` | `valkey-lock` | Deployment + Valkey Deployment + Valkey PVC + Service |

CNPG mode requires the CNPG operator pre-installed cluster-wide;
the chart renders a `Cluster` CR (and optional `Pooler` CR) but
does NOT take a Helm sub-chart dependency on the operator and does
NOT install a pre-flight helm hook. If the CNPG CRDs are missing,
the CR application fails at apply time and surfaces in helm/Argo
the way any other missing CRD would. CNPG provisions Postgres and
creates a `<cluster>-app` Secret with `host`/`username`/`password`/
`dbname` keys; the repo-guardian Deployment consumes those keys
via `secretKeyRef` — no chart-rendered DB credentials Secret in
this mode. This pattern mirrors the maintainer's
server-price-tracker chart.

### Why not Postgres for the queue too

Considered. Tradeoff matrix:

| Concern | Postgres queue (`SKIP LOCKED`) | Valkey queue |
|---------|-------------------------------|--------------|
| Operational simplicity | One DB to run | One Valkey to run |
| Latency | Polling cost (50-200ms/poll) | Atomic `BRPOP`; sub-ms delivery |
| Throughput at 10k+ repos | Adequate with tuning | Plenty without tuning |
| Survives DB outage | No (queue is in same DB) | Yes — queue is independent |
| Familiarity | Higher | Higher (Redis API is well-known) |
| Operator burden | Same as state's | Adds one chart-managed Deployment |

Valkey wins on isolation (queue available even if Postgres is
briefly down) and latency. The interface boundary preserves the
option to consolidate to Postgres-only later if the operator
burden of running Valkey turns out to be unwelcome — that's a
config flip, not an engine change.

### Observability

10 new Prometheus metrics on top of the existing 21. All
per-repo / per-installation metrics carry an `org` label,
consistent with IMPL-0009. Pod-identity labels are kept off
everything except `scheduler_is_leader` to avoid cardinality
blowup.

| Metric | Type | Labels | Source |
|---|---|---|---|
| `repo_guardian_queue_depth` | Gauge | `queue` (`jobs` / `in-flight`) | Periodic `LLEN`/`ZCARD` poll |
| `repo_guardian_queue_enqueued_total` | Counter | `trigger` (`webhook` / `sweep`) | At `Queue.Enqueue` |
| `repo_guardian_queue_claimed_total` | Counter | — | At `BRPOP` success |
| `repo_guardian_queue_acked_total` | Counter | `outcome` (`success` / `error`) | At `Queue.Ack` |
| `repo_guardian_queue_reaped_total` | Counter | — | When in-flight ZSET entries time out |
| `repo_guardian_scheduler_is_leader` | Gauge | `pod` | 1 on lock-holder pod, 0 elsewhere |
| `repo_guardian_scheduler_sweep_batch_size` | Histogram | — | At sweep handler exit |
| `repo_guardian_store_query_seconds` | Histogram | `op` (`stale_repos` / `update_repo_state` / ...) | Wrap pgx calls |
| `repo_guardian_rate_limit_remaining` | Gauge | `installation_id` | Read from GitHub response headers |
| `repo_guardian_rate_limit_reserve_blocked_total` | Counter | `installation_id` | Increment when sweep skips an enqueue due to reserve |

**SLO targets are deliberately deferred.** We don't yet have
prod data on queue arrival rate at typical org sizes, how often
the sweep tick lands on an empty stale-set, pgx pool contention
under N-replica fan-in, or realistic rate-limit budget
consumption. Ship the metrics, run for a release cycle, and
draft a follow-up DESIGN with measured baselines and committed
SLO targets at that point. Until then, treat the chart-bundled
alerts (below) as starter shapes, not contractual SLOs.

The chart surfaces a `serviceMonitor` block (matching the
existing chart pattern) and a sibling `prometheusRule` block,
both `enabled: false` by default. When `prometheusRule.enabled:
true`, the chart renders a `PrometheusRule` with these starter
alerts:

- `RepoGuardianNoLeader` — `max(repo_guardian_scheduler_is_leader) == 0` for 5m → leader election broken
- `RepoGuardianQueueBacklog` — `repo_guardian_queue_depth{queue="jobs"} > 1000` for 15m → workers can't keep up
- `RepoGuardianReapingTooMuch` — `rate(repo_guardian_queue_reaped_total[5m]) > 0.1` → workers crashing or DSN flapping
- `RepoGuardianRateLimitNearExhausted` — `repo_guardian_rate_limit_remaining < 100` per installation
- `RepoGuardianStoreLatencyHigh` — `histogram_quantile(0.99, store_query_seconds) > 0.5s` for 10m

`for:` durations and thresholds are values-overridable so
operators can tune for their fleet size.

## API / Interface Changes

New environment variables on the binary:

- `STORE_BACKEND` — `postgres` (default) or `memory`.
- `STORE_DSN` — Postgres connection string. Required when
  `STORE_BACKEND=postgres`. Read from a Secret in the chart.
- `QUEUE_BACKEND` — `valkey` (default) or `memory`.
- `QUEUE_VALKEY_DSN` — Valkey connection string
  (`redis://host:6379/0`). Required when `QUEUE_BACKEND=valkey`.
  Same protocol works with external Valkey, AWS ElastiCache,
  Cloud Memorystore.
- `SCHEDULER_BACKEND` — `valkey` (default) or `ticker`.
- `RECONCILE_FRESHNESS` — duration (default `168h`).
- `RATE_LIMIT_RESERVE` — fraction (default `0.2`).
- `WORKER_CONCURRENCY` — int (default `5`). Number of in-process
  worker goroutines per replica.
- `JOB_ACK_TIMEOUT` — duration (default `5m`). In-flight set
  reaper threshold.
- `REAPER_INTERVAL` — duration (default `60s`). How often each
  pod attempts the `lock:reaper` and runs the in-flight
  reclamation pass.
- `STORE_POSTGRES_MAX_CONNS` — int (default `5`). pgx pool max
  per replica; tune up only if you've moved past the
  recommended PgBouncer pattern at high replica counts.
- `STORE_SWEEP_BATCH_SIZE` — int (default `200`). Hard cap on
  repos enqueued per scheduler tick. Independent of the
  per-installation rate-limit reserve check, which gates each
  individual enqueue.

Helm chart values surface (new):

```yaml
# Store: persistent state for reconcile freshness
store:
  backend: postgres        # postgres | memory
  postgres:
    mode: baked            # baked | cnpg | external
    maxConns: 5            # STORE_POSTGRES_MAX_CONNS — pgx pool
                           # max per replica
    # baked: chart renders Postgres Deployment + PVC + Service.
    # cnpg: chart renders a CNPG Cluster CR (requires the CNPG
    #   operator pre-installed in the cluster).
    # external: operator provides the DSN; chart renders nothing.
    dsn: ""                # external mode: full DSN; usually from
                           # an existingSecret reference
    existingSecret: ""     # name of Secret holding STORE_DSN key
    baked:
      image: "postgres:16"
      persistence:
        size: 5Gi
        storageClass: ""
      # Same auto-generated-password pattern as Valkey. If
      # store.postgres.existingSecret is unset, the chart creates
      # <release>-postgres with a random password on first install
      # and reuses it on upgrades.
    cnpg:
      # Chart renders a CNPG `Cluster` CR (and optional `Pooler` CR).
      # The CNPG operator must be pre-installed cluster-wide; the
      # chart does NOT depend on the CNPG operator chart and does
      # NOT run a helm hook to verify the CRDs. If the operator is
      # missing, the CR fails to reconcile and Argo/helm surfaces
      # the error at apply time. This mirrors the pattern used in
      # the maintainer's server-price-tracker chart.
      #
      # On install, CNPG provisions Postgres and creates an app
      # Secret named `<cluster>-app` containing host/username/
      # password/dbname keys. The repo-guardian Deployment consumes
      # those keys via `secretKeyRef` — the chart does NOT render
      # its own Postgres credentials Secret in this mode.
      instances: 1
      imageName: ghcr.io/cloudnative-pg/postgresql:17.2
      bootstrap:
        database: repo_guardian
        owner: repo_guardian
      storage:
        size: 5Gi
        storageClass: ""
      managed:
        services:
          # Single-instance does not need read-only or replica
          # services. Drop them to keep the namespace tidy.
          disabledDefaultServices: ["ro", "r"]
      monitoring:
        enablePodMonitor: false
      postgresql:
        parameters:
          max_connections: "200"
      resources: {}
      # Optional PgBouncer in front of the cluster — only useful
      # at high replica counts (>5 worker pods).
      pooler:
        enabled: false
        type: rw
        instances: 1
        pgbouncer:
          poolMode: transaction
          defaultPoolSize: 25
          maxClientConnections: 100

# Queue: durable work queue + scheduler-lock backend
queue:
  backend: valkey          # valkey | memory
  valkey:
    mode: baked            # baked | external
    # baked: chart renders Valkey Deployment + PVC + Service.
    # external: operator provides the DSN (works with AWS
    #   ElastiCache, Cloud Memorystore, or any other Valkey/Redis-
    #   compatible service).
    dsn: ""
    existingSecret: ""
    baked:
      image: "valkey/valkey:8"
      persistence:
        size: 1Gi
        storageClass: ""
      # AUTH is on by default. If existingSecret is unset, the
      # chart auto-generates a random password Secret named
      # <release>-valkey on first install and reuses it on upgrades
      # (helm `lookup` + `randAlphaNum` pattern).

# Scheduler
scheduler:
  backend: valkey          # valkey | ticker
  # `valkey` reuses queue.valkey.dsn for leader-election locks.
  # `ticker` is single-replica only.
  sweep:
    batchSize: 200         # STORE_SWEEP_BATCH_SIZE — hard cap
                           # on repos enqueued per tick
  reaper:
    interval: 60s          # REAPER_INTERVAL — how often each pod
                           # attempts lock:reaper

# Observability — both off by default; opt in per cluster.
serviceMonitor:
  enabled: false
  interval: 30s
  path: /metrics
  labels: {}

prometheusRule:
  enabled: false
  # Per-alert overrides. Operators can tune `for:` durations and
  # threshold expressions without forking the chart.
  alerts:
    noLeader:
      for: 5m
    queueBacklog:
      threshold: 1000
      for: 15m
    reapingTooMuch:
      rate: 0.1
    rateLimitNearExhausted:
      threshold: 100
    storeLatencyHigh:
      quantile: 0.99
      threshold: 0.5
      for: 10m
```

ServiceAccount RBAC stays minimal — no leader-election Lease
permissions needed when `scheduler.backend=valkey`. If a future
`scheduler/k8s-lease` implementation lands, we add
`coordination.k8s.io/leases` permissions then.

Chart Deployment shape: stays a Deployment (not StatefulSet). No
per-pod PVCs needed — repo-guardian pods are stateless; only the
baked Postgres and baked Valkey have PVCs, and those are single-
replica deployments with their own PVCs.

Internal Go interface additions are listed under Detailed Design.

## Data Model

Postgres schema, owned by `internal/store/postgres/migrations`:

```sql
CREATE TABLE repo_state (
    installation_id BIGINT NOT NULL,
    owner TEXT NOT NULL,
    repo TEXT NOT NULL,
    last_checked_at TIMESTAMP WITH TIME ZONE,
    last_check_status TEXT NOT NULL DEFAULT 'pending',
    last_error TEXT,
    policy_version TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (installation_id, owner, repo)
);

CREATE INDEX idx_repo_state_freshness
    ON repo_state(last_checked_at NULLS FIRST);
CREATE INDEX idx_repo_state_policy_version
    ON repo_state(policy_version);
```

Valkey key layout (no schema; documented as a contract):

```
repo-guardian:queue:jobs        # LIST — pending jobs (LPUSH / BRPOP)
repo-guardian:queue:in-flight   # ZSET — claimed jobs, scored by claim ts
repo-guardian:lock:sweep        # KEY  — sweep scheduler lock (SET NX EX)
repo-guardian:lock:reaper       # KEY  — in-flight reaper lock
```

Job payload is a JSON-encoded `Job` struct (see Queue interface).
TTLs:

- `lock:sweep`, `lock:reaper`: 30s (longer than handler runtime).
- `queue:in-flight` entries: reaped at `JOB_ACK_TIMEOUT` (default 5m)
  — older entries are popped back onto `queue:jobs` for retry.

No CNPG-specific schema differences; CNPG-managed Postgres is just
a different deployment of the same Postgres image, so the
`golang-migrate`-managed schema works against either.

## Testing Strategy

Two distinct test-double patterns:

- **In-memory implementations** (`store/memory`, `queue/memory`,
  `scheduler/ticker`) are full functional implementations of the
  interface — they hold state, enforce contracts, and let
  state-based tests assert on outcomes ("given these stale repos
  in the store, the sweep enqueues these jobs"). They are part of
  the production binary, not test-only fixtures.
- **Generated mocks** via `mockery v2` (already pinned in
  `mise.toml`, currently unused) for interaction-verification
  tests ("the engine called `Store.UpdateRepoState` with
  `status=success`"). A `.mockery.yaml` config will declare the
  three interfaces and emit mocks under
  `internal/<pkg>/mocks/<Interface>.go`. Regeneration via
  `make mocks` (new target) or implicitly in `make ci`. This
  catches interface-shape drift at compile time when an interface
  changes — no manual mock edits required.

  The existing `github.Client` hand-written mocks in
  `internal/checker/engine_test.go` and `internal/reconciler/*_test.go`
  predate this decision. They can stay as-is; migrating them to
  mockery is a follow-up cleanup, not a prerequisite.

Test layers:

- **Unit tests** at the interface boundary. Engine tests use
  in-memory backends or hand-written mocks; do not require
  Postgres or NATS at all.
- **Backend integration tests** under an `_integration` build tag:
  - Postgres via `testcontainers-go` running `postgres:16` (same
    image as the baked deployment).
  - Valkey via `testcontainers-go` running `valkey/valkey:8`.
- **Multi-replica concurrency test**: spin up N worker goroutines
  in one process all pointed at the same testcontainer Valkey,
  enqueue 100 jobs, assert each is claimed exactly once across all
  workers (atomic `BRPOP` is the load-bearing primitive).
- **Restart-safety test**: kill the testcontainer Valkey mid-job,
  bring it back, assert no job is lost (in-flight set's reaper
  re-queues), and engine idempotency makes any double-claim safe.
- **Contract tests** that run the same suite against every backend
  pair (`memory` and `postgres` for `Store`; `memory` and `valkey`
  for `Queue`; `ticker` and `valkey` for `Scheduler`). Catches
  divergence between the in-memory and durable implementations
  before it bites in production.
- **Helm-unittest** for the chart's deployment-shape matrix:
  - `memory + memory + ticker` → only the repo-guardian Deployment
  - `baked + baked` → repo-guardian + Postgres Deployment + Valkey
    Deployment + PVCs + Services
  - `cnpg + baked` → repo-guardian + CNPG Cluster CR + Valkey
    Deployment + PVC + Service
  - `external + external` → only the repo-guardian Deployment with
    DSN env vars sourced from a Secret
- **Homelab smoke test**: deploy `baked + baked` with
  `replicas: 3`, kill one pod, observe sweep continues; restart
  all three, observe no duplicate reconciles.

## Migration / Rollout Plan

Interface-first means we ship in small increments without doubling
code paths.

1. **Land the interfaces** with `memory` implementations only
   (`store/memory`, `queue/memory`, `scheduler/ticker`). Switch
   existing engine code to depend on the interfaces. CI green,
   no behavior change. Mockery-generated mocks land alongside.
2. **Land the Postgres `Store` implementation** behind
   `STORE_BACKEND=postgres`. Includes `golang-migrate` setup and
   the freshness gate plumbed through the sweep handler.
3. **Land the Valkey `Queue` + `Scheduler` implementations** behind
   `QUEUE_BACKEND=valkey` and `SCHEDULER_BACKEND=valkey`. Adds the
   in-flight set reaper as a goroutine on every worker pod.
4. **Helm chart MINOR bump.** Adds the four deployment shapes
   (no-dep / baked / cnpg + baked / external). The `baked` mode
   becomes the chart default since it's the simplest "helm install
   just works" path. Existing operators on the legacy in-memory
   chart see no behavior change unless they opt into a new mode.
5. **Homelab smoke test**: deploy `baked + baked` with
   `replicas: 3`, validate one full sweep + Valkey-coordinated
   leader election + in-flight reaper.
6. **Decommission decision.** The `memory` backends stay
   supported indefinitely as a no-dep mode. `valkey-lock`
   scheduler stays the default. No deprecation timeline for any
   backend.

Rollback at any step is a values flip + helm rollback. Switching
from `memory` to `baked` (or vice versa) is not a data migration
— `memory` has no persistent state to carry forward, and `baked`
state is only `last_checked_at` which is non-load-bearing.

Switching between Postgres modes (`baked` ↔ `cnpg` ↔ `external`)
*is* a data migration if you want to preserve `last_checked_at`,
but the cost of losing it is one extra sweep cycle. Operators
who care about the migration can `pg_dump` / `pg_restore`; we
don't ship tooling for it in V1.

## Open Questions

1. **Scheduler interface granularity.** **Resolved.** One
   global sweep is sufficient for V1. The `Scheduler` interface
   admits per-installation schedules in a future iteration
   (just another implementation slot — `scheduler/per-install`),
   but adding it now would couple the interface to a concept we
   don't have a use case for yet. Until an operator surfaces a
   real "this installation needs hourly while others are weekly"
   need, one `RECONCILE_FRESHNESS` window applied uniformly
   keeps the scheduler trivial.
2. **Per-rule freshness windows.** **Resolved.** Track
   freshness per `(installation_id, owner, repo)` only — not
   per `(repo, rule)`. The new-rule-added scenario is handled
   by hashing the active policy into a `policy_version` column
   on `repo_state`: when the operator updates `guardian.hcl`
   the binary's loader recomputes the hash, sweep treats every
   row whose `policy_version` differs from current as stale,
   and the next tick re-enqueues. This avoids the cardinality
   blowup of per-rule rows (tens of rules × tens of thousands
   of repos) and the schema churn of tracking which rule each
   row corresponds to. Schema impact: add `policy_version TEXT`
   to `repo_state`, default empty, populated on first write.
3. **Webhook delivery semantics.** **Resolved.** Single
   shared `queue:jobs` LIST — no separate "high priority"
   queue for webhook-driven jobs. Reconcile triggers are
   limited (scheduler tick, new-repo install events, push to
   default branch touching a watched file) rather than firing
   on every PR/comment, so queue contention from webhook
   floods is unlikely. Webhook ACK SLA: 202 within 2s
   end-to-end (HMAC validation + IP allowlist + `Queue.Enqueue`
   + return). The Valkey enqueue itself is sub-millisecond;
   the budget is for the upstream middleware. If the queue
   ever becomes the contention point we promote to a two-list
   pattern (`queue:jobs:high` / `queue:jobs:low` with a
   priority-aware `BLPOP` over both keys), but not before
   metrics show it's needed.
4. **Operator force-recheck.** **Resolved.** No HTTP admin
   endpoint in V1. The chart owner also owns the state DB
   (baked, CNPG, or external) and can run a one-liner —
   `UPDATE repo_state SET last_checked_at = NULL WHERE
   owner = '...';` — to force a recheck of any subset on the
   next sweep tick. A future admin web UI is out of scope for
   this design; if it lands, it adds an HTTP surface that
   wraps the same SQL.
5. **CNPG operator as a dependency.** **Resolved.** No helm
   hook, no chart-level pre-flight check, no Helm sub-chart
   dependency on the CNPG operator chart — Argo and Helm hooks
   are operationally awkward together, and the CNPG operator is
   a cluster-scoped prereq that organizations install once and
   reuse across many apps. Pattern follows the maintainer's
   server-price-tracker chart: `store.postgres.mode = cnpg`
   gates rendering of the `Cluster` (and optional `Pooler`) CR;
   if the CRDs are absent the CR application fails at apply
   time and surfaces in Argo/helm normally. Documentation in
   the chart README and a clear binary-side error message when
   `STORE_POSTGRES_MODE=cnpg` is set but the DSN can't be
   resolved cover the discoverability gap. The chart consumes
   the CNPG-generated `<cluster>-app` Secret via `secretKeyRef`
   — no chart-rendered Postgres credentials Secret in cnpg
   mode.
6. **Observability.** **Resolved** for the metric set and the
   chart-bundled alert shapes — see the new "Observability"
   subsection under Detailed Design (10 new metrics on top of
   the existing 21, `serviceMonitor` + `prometheusRule` chart
   blocks both off by default, 5 starter alerts when
   `prometheusRule.enabled=true`). **SLO targets are
   deliberately deferred** to a follow-up DESIGN once we have a
   release cycle of prod data on queue arrival rate, sweep
   batch sizes, pgx pool contention, and rate-limit budget
   consumption. Until then, the chart-shipped alert thresholds
   are starter shapes, not contractual SLOs, and operators can
   tune `for:` durations and thresholds via values.
7. **In-flight reaper ownership.** **Resolved.** Every worker
   pod runs a reaper goroutine; pods race on a separate
   `repo-guardian:lock:reaper` Valkey key (already in the
   schema). This decouples reaper cadence from sweep cadence
   and keeps orphan reclamation resilient to scheduler-leader
   churn — if the leader pod dies mid-sweep, the reaper still
   runs from another pod, just with a 30s lock-TTL gap.
   Defaults: reaper interval 60s, `lock:reaper` TTL 30s (longer
   than expected reap runtime). Cost is one extra goroutine per
   pod and one extra Valkey key — negligible.
8. **Postgres connection pool sizing.** **Resolved.** Default
   pgx pool max = 5 per replica, values-tunable via
   `STORE_POSTGRES_MAX_CONNS` (env) / `store.postgres.maxConns`
   (helm). Math at default: 5 replicas → 25 conns, 10 → 50;
   baked Postgres `max_connections=100` and CNPG `Cluster.spec.
   postgresql.parameters.max_connections=200` (already in the
   values block) both leave comfortable headroom. For replica
   counts >15, the already-exposed CNPG `Pooler` (PgBouncer) is
   the recommended path rather than bumping `max_conns`.
9. **Sweep batch size.** **Resolved.** Hard cap of 200 repos
   enqueued per scheduler tick, values-tunable via
   `STORE_SWEEP_BATCH_SIZE` (env) / `scheduler.sweep.batchSize`
   (helm). Rate-limit reserve is a *separate* per-repo enqueue
   gate: at enqueue time, check the per-installation rate-limit
   remaining; if under reserve, skip the enqueue and bump
   `repo_guardian_rate_limit_reserve_blocked_total{installation_id}`
   (the metric is already in the Q6 set). Decoupling keeps the
   intent of each layer clear — the batch cap protects Postgres
   and Valkey throughput; the reserve protects GitHub.

## References

- IMPL-0010 (chart 0.3.3 published the binary this design extends)
- INV-0003 (engine bug whose fix made same-repo reconciles
  idempotent — prerequisite for safe multi-replica execution)
- INV-0004 (Provider interface refactor — same interface-first
  principle, applied to the SCM layer)
- DESIGN-0005 (Helm chart, the deployment surface this design
  modifies)
- GitHub REST API rate limits:
  https://docs.github.com/en/rest/overview/rate-limits-for-the-rest-api
- Valkey: https://valkey.io
- CloudNativePG: https://cloudnative-pg.io/documentation/current/
- server-price-tracker chart (CNPG `Cluster` + `Pooler` template
  pattern adopted here):
  https://github.com/donaldgifford/server-price-tracker/tree/main/charts/server-price-tracker
- AWS ElastiCache for Redis (Valkey-compatible):
  https://docs.aws.amazon.com/AmazonElastiCache/latest/red-ug/
