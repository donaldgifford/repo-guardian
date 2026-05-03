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
rate-limit ceiling for a single installation. This design introduces
durable persistence for state and queue, with **interface boundaries**
between the engine and any concrete backend so that the storage,
queue, and scheduler implementations can be swapped without engine
changes. Day-one defaults: **Postgres** for state (CNPG in homelab,
RDS Postgres elsewhere) and **embedded NATS JetStream** for the
durable work queue + leader-elected scheduler.

## Goals and Non-Goals

### Goals

- **Durability over pragmatism.** Surviving a pod crash mid-sweep
  must not redo completed work. State and in-flight jobs survive
  restarts.
- **Interface-first architecture.** `Store`, `Queue`, and `Scheduler`
  are Go interfaces with multiple implementations available from
  day one. The engine depends only on the interfaces; concrete
  backends are selected at startup.
- **No-dep mode is a first-class supported configuration.** Because
  every interface has an in-memory implementation, an operator can
  run repo-guardian with zero external dependencies (no Postgres,
  no embedded NATS clustering) by selecting the `memory` backends.
  Restart-amnesia and single-replica are accepted tradeoffs in this
  mode; it is not a "test only" path that gets deprecated.
- **Multi-replica safe by default.** No mode where running
  `replicas: 2` is unsafe. Coordination primitives are part of
  the V1 design, not bolted on later.
- **No extra deployable infra for the queue.** NATS runs embedded
  in the binary (clustered across pods via JetStream). Postgres is
  external — but most operators already run one (CNPG in homelab,
  RDS in cloud).
- **Bounded API consumption per sweep.** Each scheduler tick fits
  within the installation's hourly rate-limit budget; remaining
  repos carry to the next tick.
- **Webhook-driven reconciles remain parallel** across all replicas
  — they enqueue to NATS like any other source, and any worker can
  claim them.

### Non-Goals

- Cross-cluster coordination. Single k8s cluster only.
- Replacing GitHub PR / branch state as the source of truth for
  *what* needs reconciling. State tracks *when we last looked*,
  not *desired configuration*.
- Backfilling historical `last_checked_at`. New state starts empty;
  the first post-deploy sweep is a cold start, then steady state.
- Sub-second leader failover during sweep transitions. JetStream's
  consumer-takeover semantics (a few seconds gap when a consumer
  pod dies) are acceptable.
- A standalone NATS deployment as a fallback. We pick embedded and
  commit to it; if embedded NATS doesn't fit the operator's needs,
  the swappable `Queue` interface allows replacing it (e.g., with
  a Postgres-backed queue) without touching engine code.

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

Why NATS embedded:

- **Embedded server.** `github.com/nats-io/nats-server/v2/server`
  starts a full NATS server in-process. JetStream provides durable
  streams, work queues with at-least-once semantics, and KV store
  primitives.
- **Cluster-of-pods topology.** NATS clusters via raft for JetStream
  metadata. Each pod runs an embedded server; they discover each
  other via a headless k8s Service. Stream replicas live on a quorum
  of pods, so any single pod can crash without queue data loss.
- **Already used in similar Go projects** (nats.io/nats-server
  embedded in operators, control planes). Production-tested embedded
  pattern.
- **Single binary deployable** stays a property of repo-guardian.
  The chart adds a PVC for JetStream storage and a headless Service
  for NATS clustering; no separate StatefulSet for a queue broker.

Why Postgres for state (and not NATS KV or embedded SQLite):

- The state queries we care about are relational with order-by:
  "give me the N most-stale repos with `last_checked_at < ?`".
  This is awkward in NATS KV and trivial in SQL.
- CNPG in homelab and RDS in production are both well-trodden;
  no new database-class infra to introduce.
- Connection pooling, migrations, observability are all solved
  problems for Postgres clients in Go (`pgx`, `golang-migrate`).

## Detailed Design

### Architectural shape

```
            ┌─────────────────────────────────────┐
            │            Engine                   │
            │  (file rules, reconcilers,          │
            │   GitHub client, idempotent ops)    │
            └────────┬────────┬────────┬──────────┘
                     │        │        │
                     ▼        ▼        ▼
                  Store    Queue   Scheduler   ← Go interfaces
                     │        │        │
                     ▼        ▼        ▼
               Postgres   NATS      NATS
              (CNPG/RDS) (Stream) (Periodic
                         + WorkQueue       + Leader-elected
                          consumer)         consumer)
```

The engine never imports `pgx` or `nats-server`. It receives
`Store`, `Queue`, and `Scheduler` instances at construction time
and uses only their interface methods. Concrete implementations
live in `internal/store/postgres`, `internal/queue/nats`,
`internal/scheduler/nats`. Test code uses `internal/store/memory`
etc.

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
}

type Store interface {
    GetRepoState(ctx context.Context, installationID int64, owner, repo string) (*RepoState, error)
    UpdateRepoState(ctx context.Context, s *RepoState) error
    StaleRepos(ctx context.Context, freshness time.Duration, limit int) ([]RepoState, error)
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

- `queue/nats` — embedded JetStream Stream + WorkQueue consumer.
  Stream config: `MaxAge=24h`, `Retention=WorkQueue`, replicas=3.
  Consumer is durable, `MaxAckPending=N` (configurable, default 5).
  Cluster-aware; multi-replica safe.
- `queue/memory` — in-process buffered channel. Supported for tests
  and no-dep single-replica deployments. Restart loses any
  in-flight queue contents; webhook events arriving during a
  restart window are dropped at the HTTP layer.
- *Future option:* `queue/postgres` — `SELECT ... FOR UPDATE SKIP
  LOCKED` if some operator rejects embedded NATS.

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

- `scheduler/nats` — uses a JetStream stream with a single durable
  consumer per scheduled task. A heartbeat goroutine on the
  JetStream meta-leader writes a "tick" message at the configured
  interval; the durable consumer with `MaxAckPending=1` ensures
  only one pod processes it. Leader failover is built in: if the
  consuming pod dies, JetStream redelivers to the next available
  consumer.
- `scheduler/ticker` — `time.Ticker` for tests and no-dep
  single-replica deployments. Each pod's ticker fires
  independently, so this implementation must not be paired with
  multi-replica without coordination.

### Sweep flow

1. `Scheduler` fires the sweep tick (NATS-coordinated, single pod).
2. Sweep handler queries `Store.StaleRepos(freshness, batch_size)`.
3. For each stale repo, calls `Queue.Enqueue(Job{...})`.
4. Worker pods (all replicas) `Subscribe` to the queue, claim jobs,
   run the engine on the repo.
5. On success or error, worker calls `Store.UpdateRepoState(...)`.
6. Webhook handler bypasses the freshness check; it always
   `Queue.Enqueue`s a job. Same worker pool consumes it.

### Why not Postgres for the queue too

Considered. Tradeoff matrix:

| Concern | Postgres queue (`SKIP LOCKED`) | NATS JetStream embedded |
|---------|-------------------------------|------------------------|
| Operational simplicity | One DB to run | Embedded; no extra deploy |
| Latency | Polling cost (50-200ms/poll) | Pub/sub; sub-ms delivery |
| Throughput at 10k+ repos | Adequate; needs tuning | Designed for this |
| Survives DB outage | No | Yes — queue is independent |
| Familiarity | Higher | Lower (Go ecosystem) |
| Lock-in | Lower | Higher (Go-only embedded story) |

Embedded NATS wins on isolation (queue available even if Postgres
is briefly down), latency, and throughput. The interface boundary
preserves the option to swap.

## API / Interface Changes

New environment variables:

- `STORE_BACKEND` — `postgres` (default) or `memory`.
- `STORE_DSN` — Postgres connection string. Required when
  `STORE_BACKEND=postgres`.
- `QUEUE_BACKEND` — `nats` (default) or `memory`.
- `QUEUE_NATS_STORAGE` — `file` (default, persistent) or `memory`.
- `QUEUE_NATS_REPLICAS` — int (default `3`). Applied to JetStream
  stream config; clamped to `<=` cluster size at runtime.
- `SCHEDULER_BACKEND` — `nats` (default) or `ticker`.
- `RECONCILE_FRESHNESS` — duration (default `168h`).
- `RATE_LIMIT_RESERVE` — fraction (default `0.2`).
- `NATS_CLUSTER_NAME` — string (default `repo-guardian`).
- `NATS_CLUSTER_PEERS` — comma-separated peer URLs; populated
  automatically from the headless Service in the chart.
- `POD_NAME` / `POD_NAMESPACE` — k8s downward API; used for NATS
  identity and observability.

Helm chart additions:

- StatefulSet **replaces** Deployment when persistence is enabled,
  because each pod needs a stable identity for the embedded NATS
  cluster and a per-pod PVC for JetStream storage. Deployment
  remains an option for the in-memory backends (test/dev).
- Headless Service for NATS cluster discovery.
- PVC template per replica via `volumeClaimTemplates` (sizes default
  to `1Gi` for queue storage; tunable).
- New values keys:
  ```yaml
  store:
    backend: postgres
    dsn: ""           # required; usually from secret
  queue:
    backend: nats
    storage: file
    persistence:
      size: 1Gi
      storageClass: ""
  scheduler:
    backend: nats
  postgresql:
    # opt-in CNPG cluster sub-chart for homelab convenience
    enabled: false
  ```
- ServiceAccount RBAC: `events.k8s.io` for emitting events,
  `coordination.k8s.io` removed (NATS handles leader election,
  not k8s Lease).

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
    PRIMARY KEY (installation_id, owner, repo)
);

CREATE INDEX idx_repo_state_freshness
    ON repo_state(last_checked_at NULLS FIRST);
```

JetStream stream and consumer configs (declarative, applied at
startup):

```go
streamCfg := &nats.StreamConfig{
    Name:      "REPO_GUARDIAN_JOBS",
    Subjects:  []string{"jobs.>"},
    Retention: nats.WorkQueuePolicy,
    Storage:   nats.FileStorage,
    Replicas:  3,
    MaxAge:    24 * time.Hour,
}
consumerCfg := &nats.ConsumerConfig{
    Durable:        "workers",
    AckPolicy:      nats.AckExplicitPolicy,
    MaxAckPending:  cfg.WorkerConcurrency,
    AckWait:        5 * time.Minute,
}
```

A second stream `REPO_GUARDIAN_TICKS` carries the periodic sweep
trigger with replicas=3 and a single durable consumer named
`sweep-leader`.

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
  - Postgres via `testcontainers-go` running CNPG-flavored Postgres
    16 (matches homelab).
  - NATS via `nats-server` embedded directly in the test binary —
    same code path as production.
- **Multi-replica concurrency test**: spin up three embedded NATS
  servers in one process forming a cluster, publish 100 jobs,
  assert each is consumed exactly once across all consumers.
- **Restart-safety test**: kill an embedded NATS server mid-job,
  bring it back, assert no job is lost or double-consumed (verifies
  JetStream's at-least-once semantics + engine idempotency).
- **Contract tests** that run the same suite against every backend
  pair (`memory` and `postgres` for `Store`; `memory` and `nats`
  for `Queue`; `ticker` and `nats` for `Scheduler`). Catches
  divergence between the in-memory and durable implementations
  before it bites in production.
- **Helm-unittest** for the StatefulSet, headless Service, PVC
  templates, and conditional in-memory Deployment.
- **Homelab smoke test**: deploy with `replicas: 3`, kill one pod,
  observe sweep continues; restart all three, observe no duplicate
  reconciles.

## Migration / Rollout Plan

This design intentionally does not stage a "single-replica first"
phase, since the interface-first architecture lets us ship the
durable backends from day one without doubling the code paths.

1. **Land the interfaces** in a separate PR with the in-memory
   implementations only. Switch existing engine code to depend on
   the interfaces. CI green, no behavior change. Today's binary
   still works exactly as it does now.
2. **Land the Postgres `Store` implementation** with the freshness
   gate plumbed through the sweep handler. Behind
   `STORE_BACKEND=postgres`.
3. **Land the embedded-NATS `Queue` and `Scheduler` implementations.**
   Behind `QUEUE_BACKEND=nats` and `SCHEDULER_BACKEND=nats`.
4. **Helm chart MAJOR bump** to 0.4.0. Adds StatefulSet template,
   headless Service, PVC `volumeClaimTemplates`, optional CNPG
   sub-chart. Both deployment shapes coexist in the chart:
   - `store.backend=memory` + `queue.backend=memory` +
     `scheduler.backend=ticker` → renders as Deployment, no PVCs,
     no extra services. The "try it" path.
   - `store.backend=postgres` + `queue.backend=nats` +
     `scheduler.backend=nats` → renders as StatefulSet with PVCs
     and headless Service. The "run it for real" path.
5. **Homelab smoke test** for one full sweep cycle on the durable
   backends.
6. **Pick a chart default.** Probably `memory` for first-touch
   ergonomics, with chart docs explicitly recommending
   `postgres + nats` for any production-ish deployment.
   Decision deferred to Open Questions.

The `memory` backends remain supported indefinitely as a
no-dep mode. They are not a transitional path that gets
deprecated.

Rollback at any step is a values flip back to the prior backend +
helm rollback. Switching from `memory` to `postgres + nats` is
not a data migration — `memory` has no persistent state to carry
forward.

## Open Questions

1. **NATS embedded cluster bootstrap in k8s.** Headless Service
   + downward API for peer discovery is the canonical pattern,
   but exact wiring (init container? readiness probe gating?)
   needs prototyping. Prior art: NATS' own Helm chart.
2. **JetStream replica count vs. replica count.** What happens
   when chart `replicas=1` (dev clusters) but `streamReplicas=3`?
   We clamp at runtime, but the documentation needs to call this
   out so operators don't get confused.
3. **CNPG sub-chart vs. external Postgres.** Should the chart
   ship with an opt-in CNPG cluster (pulling in the CNPG operator
   as a dependency), or always require the operator to provide
   a DSN? Probably the latter, with CNPG examples in chart docs.
4. **Scheduler interface granularity.** Is one global sweep
   sufficient, or do we want per-installation schedules with
   different intervals?
5. **Per-rule freshness windows.** A repo might have a fast-moving
   rule (dependabot) and a slow-moving one (CODEOWNERS). One
   global window per repo may be too coarse — should freshness
   be tracked per `(repo, rule)`?
6. **Webhook delivery semantics.** Webhook handler currently runs
   the engine inline; under the new design it should `Queue.Enqueue`
   and respond 202. What's the SLA for webhook ACK time?
7. **Operator force-recheck.** Admin endpoint that resets
   `last_checked_at` for a repo, installation, or globally?
   Useful for "the rule changed, recheck everyone."
8. **JetStream stream sizing.** What `MaxBytes` /
   `MaxMsgsPerSubject` do we set? Worst case is one job per repo
   per scheduler tick × N ticks of backlog if workers fall behind.
9. **Observability.** Prometheus metrics for queue depth,
   consumer lag, scheduler tick liveness, store query latency,
   and rate-limit headroom. What are the SLO targets?
10. **NATS auth.** Embedded cluster-internal traffic via mTLS or
    simple token? Probably mTLS via cert-manager in homelab,
    something different in cloud.
11. **Chart default backend.** `memory` (first-touch friendly,
    matches current behavior) or `postgres + nats` (durable but
    requires the operator to provide a Postgres DSN before the
    pod will boot)? Argument for `memory`: matches current chart
    behavior; upgrade-in-place won't break anyone. Argument for
    `postgres + nats`: avoids a footgun where someone deploys
    with the chart default and silently loses data on restart.

## References

- IMPL-0010 (chart 0.3.3 published the binary this design extends)
- INV-0003 (engine bug whose fix made same-repo reconciles
  idempotent — prerequisite for safe multi-replica execution)
- DESIGN-0005 (Helm chart, the deployment surface this design
  modifies)
- GitHub REST API rate limits:
  https://docs.github.com/en/rest/overview/rate-limits-for-the-rest-api
- NATS embedded server:
  https://docs.nats.io/running-a-nats-service/clients
- NATS JetStream:
  https://docs.nats.io/nats-concepts/jetstream
- CloudNativePG:
  https://cloudnative-pg.io/documentation/current/
