---
id: DESIGN-0016
title: "Separate Postgres read and write endpoints"
status: Draft
author: Donald Gifford
created: 2026-06-22
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0016: Separate Postgres read and write endpoints

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
  - [Two-pool wiring](#two-pool-wiring)
  - [Read/write query classification](#readwrite-query-classification)
  - [Replica-lag tolerance](#replica-lag-tolerance)
  - [Reader-unavailable fallback](#reader-unavailable-fallback)
  - [Chart values surface](#chart-values-surface)
- [API / Interface Changes](#api--interface-changes)
  - [internal/config/](#internalconfig)
  - [internal/store/](#internalstore)
  - [internal/store/postgres/store.go](#internalstorepostgresstorego)
  - [internal/metrics/](#internalmetrics)
  - [Chart templates](#chart-templates)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
  - [Unit tests](#unit-tests)
  - [Integration tests (integrationtest.go)](#integration-tests-integrationtestgo)
  - [Chart tests (helm-unittest)](#chart-tests-helm-unittest)
- [Migration / Rollout Plan](#migration--rollout-plan)
  - [Phase 1 — Implementation (default off)](#phase-1--implementation-default-off)
  - [Phase 2 — CNPG operator adoption](#phase-2--cnpg-operator-adoption)
  - [Phase 3 — AWS RDS / Aurora adoption](#phase-3--aws-rds--aurora-adoption)
  - [Phase 4 — Promote as default for multi-instance CNPG](#phase-4--promote-as-default-for-multi-instance-cnpg)
  - [Rollback](#rollback)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

Wire repo-guardian's `internal/store/postgres/` to maintain two pgxpool connection
pools — a writer pool for state mutations and a reader pool for stale-sweep queries
— so chart deployments can route read-heavy traffic at Postgres reader endpoints
(CNPG `<cluster>-ro` service, Aurora reader endpoint, RDS read replicas).

Single-endpoint deployments remain supported via fallback: if only one DSN is
provided, both pools point at it. The change is opt-in and backward-compatible.

## Goals and Non-Goals

### Goals

- Route `Store.StaleRepos` (the per-sweep read-heavy hotpath) at a reader endpoint
  when one is configured.
- Support both CNPG and Aurora reader-endpoint shapes from the same chart.
- Preserve backward compatibility: existing single-DSN deployments behave identically.
- Graceful degradation if the reader endpoint is unavailable — fall back to the
  writer pool, log + count the fallback so operators can alert on it.

### Non-Goals

- Read-after-write consistency handling for repo-guardian's own writes. The state
  table is updated once per reconcile (after worker finishes); the next sweep tick
  is the natural read point and replica lag (~20ms on Aurora) is well under any
  sweep interval. We do not need synchronous-replica or commit-wait semantics.
- Connection-aware routing inside a single transaction. We don't do multi-statement
  transactions that mix reads and writes; if we ever do, they go to the writer
  pool unconditionally.
- Per-query dynamic routing decisions based on load. Static classification per
  Store method.
- Direct integration with AWS RDS Proxy's reader endpoint feature beyond DSN
  configuration (operator points the read DSN at the Proxy's reader endpoint).

## Background

`internal/store/postgres/` today holds a single `*pgxpool.Pool` constructed from one
DSN (`STORE_DSN` env var). Every query — reads (`StaleRepos`, `Get`, etc.) and writes
(`Upsert`, `Touch`) — uses the same pool against the same endpoint.

This is the right default for:

- Baked Postgres mode (single instance, one endpoint)
- CNPG with `instances=1` (no replicas)
- Operators who don't care about read offload

It leaves performance on the table for:

- CNPG `instances > 1`: CNPG provisions a `<cluster>-rw` service routing only to the
  primary, plus a `<cluster>-ro` service load-balanced across hot-standby replicas.
  Replicas sit idle today.
- Aurora Postgres: cluster has separate writer + reader endpoints. Reader endpoint
  load-balances across reader instances. Today repo-guardian's binary uses only the
  writer.
- RDS provisioned with read replicas: same shape as Aurora at smaller scale.

The hotpath that benefits most is `Store.StaleRepos(freshness, policyVersion,
batchSize)` — runs once per sweep tick across the (potentially large) state table.
At 3000 repos × 25 orgs scale (`docs/operations/aws.md`), this is by far the
heaviest query. Other reads (`Get` for individual repos during worker dispatch) are
small but frequent.

## Detailed Design

### Two-pool wiring

The Postgres store grows two pools:

```go
type Store struct {
    rw     *pgxpool.Pool   // writer; required
    ro     *pgxpool.Pool   // reader; may equal rw if no separate read DSN
    metrics *Metrics
}
```

Construction:

- If `STORE_DSN_RO` is set: parse, build pool, assign to `ro`.
- If unset: `ro = rw` (alias the single pool). All queries against `ro` go through
  the writer endpoint.

The alias case has zero performance impact and zero new code paths beyond the
constructor.

### Read/write query classification

Static method classification — encoded in the Store implementation, not
operator-tunable:

| Method | Pool | Rationale |
|---|---|---|
| `StaleRepos(ctx, freshness, policyVersion, batchSize)` | `ro` | Per-sweep tick read-heavy hotpath |
| `Get(ctx, owner, repo)` | `ro` | Per-worker dispatch read; high frequency, no write semantics |
| `Upsert(ctx, state)` | `rw` | Updates `last_checked_at` and policy version |
| `Touch(ctx, owner, repo)` | `rw` | Updates `last_checked_at` only |
| `Close()` | both | Closes rw, and ro if not aliased |

Methods on Store that take a `*pgx.Tx` (none today; future-proofing) always go
through `rw`. Mixed-mode transactions are not supported — they'd require
two-phase-commit semantics we don't need.

### Replica-lag tolerance

Aurora and Postgres streaming replication have non-zero lag — typically <20ms under
healthy conditions, up to seconds under load. Implications:

- **Write-then-read within the same sweep:** does not happen. The worker writes the
  state row at the END of `engine.CheckRepo`; the read in `StaleRepos` happens at
  the START of the NEXT sweep tick (minutes-to-hours later). Replica lag is
  irrelevant at our sweep cadence.
- **Discovery-then-read:** the legacy Sweeper (or DESIGN-0017's replacement) writes
  newly-discovered repo rows; the StaleSweeper reads them. If the discovery write
  hasn't replicated by the time StaleSweeper reads, the repo simply gets enqueued
  on the next tick. No correctness impact — eventual consistency is sufficient.
- **Webhook-triggered reconcile:** the webhook handler enqueues a job; the worker's
  `Get(owner, repo)` reads from the replica. If the previous reconcile's `Upsert`
  hasn't replicated, the worker sees stale state and may do redundant work (e.g.,
  re-evaluate rules that already passed). The reconcile is idempotent — no harm
  done — but it costs an extra GitHub API call cycle. Acceptable.

**Recommended `replica-lag-tolerance` setting:** none. The Store doesn't measure lag
or wait for it. Documentation flags the eventual-consistency model explicitly.

### Reader-unavailable fallback

If the reader pool's `Acquire` fails (network partition, all reader replicas down,
Proxy reader pool exhausted), the Store falls back to the writer pool for that
query. Behaviour:

- Log at WARN with `op=fallback_to_rw method=<method> error=<err>` once per
  fallback. Throttled via existing `slog` rate-limit or a `sync.Once`-protected
  warning per replica boot.
- Increment counter: `repo_guardian_store_fallback_total{from="ro", to="rw"}`.
- Proceed with the query against `rw`. The query's correctness is identical; only
  the routing changes.

Alerting:

- Alert if `rate(repo_guardian_store_fallback_total[5m]) > 0.1`. Indicates the
  reader endpoint is unhealthy.

### Chart values surface

Building on the existing `store.postgres` block:

```yaml
store:
  backend: postgres
  postgres:
    mode: external                # or cnpg, baked
    existingSecret: ""            # backward-compat single-DSN deployments
    existingSecretKey: STORE_DSN

    # NEW: optional read-endpoint configuration
    readEndpoint:
      enabled: false              # opt-in
      existingSecret: ""          # operator-provided; falls back to existingSecret if empty
      existingSecretKey: STORE_DSN_RO

    # CNPG mode auto-wires both endpoints
    cnpg:
      cluster:
        instances: 3              # primary + 2 standbys
      pooler:
        # rw pooler unchanged
      readPooler:                 # NEW
        enabled: false
        # PgBouncer config for the read-only pooler
```

For CNPG mode with `readEndpoint.enabled=true`, the chart auto-wires the read DSN to
the CNPG `<cluster>-ro` service (or `<cluster>-r` if `readPooler.enabled=true`).
Operator doesn't need to construct the read DSN manually.

For external mode (RDS / Aurora), operator provides the read DSN via the same
ExternalSecret pattern as the writer DSN.

## API / Interface Changes

### `internal/config/`

Two new env vars:

- `STORE_DSN_RO` — optional read-only Postgres DSN. Unset → aliased to `STORE_DSN`.
- `STORE_DSN_RO_MAX_CONNS` — optional pool size cap for the read pool. Defaults to
  the same `STORE_POSTGRES_MAX_CONNS` value used for the writer.

### `internal/store/`

`Store` interface unchanged. The `pgx` implementation grows the second pool
internally; callers don't observe the change.

### `internal/store/postgres/store.go`

- Constructor accepts an optional `ReadDSN string` (empty → alias rw).
- `ro` and `rw` fields on the Store struct.
- Each method picks its pool per the [classification table](#readwrite-query-classification).

### `internal/metrics/`

- `repo_guardian_store_query_seconds` gains an `endpoint="rw"|"ro"` label.
- `repo_guardian_store_fallback_total{from, to}` (new CounterVec) — counts
  reader→writer fallbacks.
- `repo_guardian_store_pool_size{endpoint}` (existing → adds label).

### Chart templates

- `_helpers.tpl`: new function `repo-guardian.storeReadSecretName` mirroring
  `repo-guardian.storeSecretName`. Picks between the chart-rendered CNPG-RO secret,
  the operator-provided existingSecret, or aliases to the writer secret.
- `deployment.yaml`: conditional env block for `STORE_DSN_RO` when
  `readEndpoint.enabled=true`.
- `store-cnpg-pooler.yaml`: extended to support the `ro` Pooler type alongside the
  existing `rw`.

## Data Model

No schema changes. The `repo_state` table and its indexes are read and written
identically; only the connection routing changes.

## Testing Strategy

### Unit tests

- Constructor with `ReadDSN=""` aliases `ro` to `rw`. Both fields are equal.
- Constructor with non-empty `ReadDSN` builds two distinct pools.
- Each Store method's pool selection (verify via test-only pool injection).

### Integration tests (`_integration_test.go`)

- Spin two Postgres containers (writer + reader). Configure replication is too
  heavy for the test loop; instead, run a `pg_dump | psql` after each write to
  simulate replication for the read-after-write tests.
- Verify reader-pool failure (kill the reader container) triggers fallback to
  writer with metric increment.
- Verify CNPG-style aliasing (single DSN, both pools point at it) gives identical
  semantics to the pre-change Store.

### Chart tests (`helm-unittest`)

- `readEndpoint.enabled=false` (default) — deployment has no `STORE_DSN_RO` env var.
- `readEndpoint.enabled=true` with CNPG mode — `STORE_DSN_RO` references the CNPG
  `<cluster>-ro` service secret.
- `readEndpoint.enabled=true` with external mode + `existingSecret` provided —
  `STORE_DSN_RO` references the operator's secret.
- `readEndpoint.enabled=true` with external mode + empty `existingSecret` — falls
  back to the writer's secret (graceful single-DSN behaviour).

## Migration / Rollout Plan

### Phase 1 — Implementation (default off)

- Land the two-pool wiring; default `readEndpoint.enabled=false`.
- Existing deployments unaffected — pools alias to a single endpoint.

### Phase 2 — CNPG operator adoption

- Operators with existing CNPG clusters (homelab, etc.) set
  `cnpg.cluster.instances=3` and `readEndpoint.enabled=true`.
- Chart auto-wires the read DSN to `<cluster>-ro`.
- Validate via metrics: `store_query_seconds{endpoint="ro"}` non-zero,
  `store_fallback_total` near zero.

### Phase 3 — AWS RDS / Aurora adoption

- Operators on RDS Proxy point a second Proxy at their cluster's reader endpoint.
- Update the External Secrets resource to expose `STORE_DSN_RO` as a second key.
- Set `readEndpoint.enabled=true` and reference the secret.

### Phase 4 — Promote as default for multi-instance CNPG

After ≥ 1 release cycle of stable Phase 2 + 3 production usage:

- When `cnpg.cluster.instances > 1`, default `readEndpoint.enabled=true` in the
  chart. Single-instance and baked modes still default to `enabled=false`.

### Rollback

If a bug surfaces, set `readEndpoint.enabled=false` and restart. The Store
constructor sees no read DSN, aliases `ro=rw`, and behaviour reverts to
single-endpoint.

## Open Questions

**(a)** What happens when the read pool is configured but ALL StaleSweeper queries
fall back to writer for an extended period?

- **(a) = Continue serving (recommended).** Service stays up; metrics surface the
  problem; operators investigate the reader. This matches the "fallback should be
  invisible to job correctness" principle.
- (b) Drain workers / pause sweep until reader recovers. Defensive but degrades the
  primary user-facing capability for a non-correctness issue. Rejected.
- other:

**(b)** Reader-pool size formula.

- **(a) = Same as writer (recommended).** Operators tune one knob (`maxConns`)
  applied to both pools. Simple mental model.
- (b) Independent knob (`readMaxConns`). More precision but adds a config surface
  that most operators won't tune.
- (c) Auto-derive (`readMaxConns = ceil(writer * 0.5)` since reads are usually
  smaller). Magic number that obscures.
- other:

**(c)** Per-`Get` routing — should the per-worker dispatch read use `ro` or `rw`?

- **(a) = `ro` (recommended).** Frequent and read-heavy. Stale-read on a recently
  reconciled repo just costs one extra reconcile cycle; harmless.
- (b) `rw`. Avoids the stale-read scenario at the cost of doubling writer load.
- other:

**(d)** Should the chart auto-enable `readEndpoint.enabled=true` for CNPG
multi-instance from day one, or wait for Phase 4?

- **(a) = Wait for Phase 4 (recommended).** Phase 1-3 are explicit operator opt-ins;
  Phase 4 promotes once we have production confidence. Safer default-change
  cadence.
- (b) Auto-enable in Phase 1. Lower friction for new deployments at the cost of
  earlier exposure to potential bugs. The CNPG-side wiring is straightforward, so
  the risk is low — but precedent in this repo is conservative default changes.
- other:

**(e)** RDS Proxy support — does the design need to know about Proxy-specific
behaviours (connection pinning, transaction multiplexing, IAM session limits) or
can we treat the Proxy endpoint as an opaque Postgres endpoint?

- **(a) = Opaque (recommended).** RDS Proxy speaks Postgres protocol; the binary
  shouldn't be Proxy-aware. Document Proxy behaviours as operator-side concerns in
  `docs/operations/aws.md`, not in the binary.
- (b) Proxy-aware. Premature; we have no evidence the Proxy needs special handling
  for our query shape.
- other:

## References

- `docs/operations/aws.md` § Postgres on AWS — operator-facing context for the
  Aurora / RDS / RDS-Proxy combinations
- `docs/operations/aws.md` § Gaps and workarounds — flagged the single-DSN
  limitation that this design resolves
- [DESIGN-0012: Persistent reconcile state and multi-replica coordination](0012-persistent-reconcile-state-and-multi-replica-coordination.md)
  — established the Store interface this design extends
- [IMPL-0011: Persistent reconcile state and multi-replica coordination](../impl/0011-persistent-reconcile-state-and-multi-replica-coordination.md)
  — baseline implementation
- [DESIGN-0015: Per-installation Valkey queue partitioning](0015-per-installation-valkey-queue-partitioning.md)
  — sibling design; both close out multi-replica scaling correctness
- [DESIGN-0017: Stale-sweep cutover and repository discovery](0017-stale-sweep-cutover-and-repository-discovery.md)
  — sibling design; affects the discovery-then-read pattern this design's
  Phase 4 promotion depends on
- [CNPG Pooler documentation](https://cloudnative-pg.io/documentation/current/connection_pooling/)
  — `rw` and `ro` Pooler types
- [Aurora Postgres reader endpoint](https://docs.aws.amazon.com/AmazonRDS/latest/AuroraUserGuide/Aurora.Overview.Endpoints.html)
  — endpoint topology background
