# Scaling repo-guardian

This is the operator-facing sizing guide for the IMPL-0011 multi-replica
deployment shapes. The single-replica `memory + memory + ticker` mode
needs none of this — keep `replicaCount: 1` and run.

## Sizing knobs

| Knob | Default | When to tune |
|------|---------|--------------|
| `replicaCount` | `1` | Bump when one replica is saturated handling webhooks (target: <50% CPU on the webhook handler container). |
| `config.workerCount` | `5` | Number of concurrent reconcile workers per replica. Each worker holds one GitHub installation client. Increase when queue depth stays > 100 for sustained periods. |
| `store.postgres.maxConns` | `16` | Postgres pool cap per replica. Should be ≥ `workerCount + 4` (workers + sweep + scheduler + reaper); below that the pool starves. |
| `staleSweep.batchSize` | `200` | Max repos returned per `StaleRepos` query. Higher batches = bigger spike per sweep tick. |
| `staleSweep.freshness` | `24h` | Max age of `last_checked_at` before re-queue. Lower = more frequent reconciles, higher = less GitHub API consumption. |
| `staleSweep.rateLimitReserve` | `0.1` | Fraction of installation's GitHub rate-limit reserved against sweep. Raise if you keep tripping reserve_blocked alerts. |
| `queue.valkey.jobAckTimeout` | `5m` | Max time a job may stay in-flight before reaper requeues. Should be > worst-case `engine.CheckRepo` runtime. |
| `queue.valkey.reaperInterval` | `60s` | Cadence between reaper iterations. Lower = faster recovery on worker death; higher = less Valkey load. |

## Scaling for repo count

Rough rule of thumb: each replica reconciles ~`workerCount × (3600 / avg_check_duration_seconds)` repos per hour. If the average check is 2s and `workerCount=5`, one replica handles ~9000 reconciles per hour.

For a 200-repo organisation reconciling daily, one replica with the defaults is comfortably oversized. You only need horizontal scale when:

- The `repo_guardian_queue_depth{queue="jobs"}` Prometheus metric stays above ~100 for sustained windows.
- `repo_guardian_check_duration_seconds` p99 climbs past your tick interval × workerCount.
- Webhook ACK latency starts breaching the 2s SLA (visible as `repo_guardian_errors_total{operation="enqueue"}`).

Scale up: bump `replicaCount` first, `workerCount` second. Splitting work across replicas reduces the blast radius of a single GitHub installation token going stale.

## Scaling for GitHub Enterprise (multi-org, multi-thousand-repo)

The single-org rule of thumb above breaks down once you cross ~10 orgs or ~1000 repos. The binding constraint stops being CPU or queue depth and starts being **GitHub's per-installation API rate limit**. Sizing for this scale is a different exercise.

### What changes at this scale

| Concern | Single-org / small | GHEC multi-org / large |
|---|---|---|
| Bottleneck | Worker throughput | Per-installation rate-limit budget |
| Sweep duration | Seconds to a few minutes | Tens of minutes, intentionally paced |
| Database load | Negligible either way | Still negligible (state table is tiny) |
| Connection pooling | Direct Postgres connection fine | Need PgBouncer between binary and Postgres |
| Replica count | 1 is correct | 2-3 for HA + webhook ingress |

### The rate-limit math

GitHub App installations get **5000 requests/hour** on non-enterprise organisations and **12500 requests/hour** on GitHub Enterprise Cloud organisations. At 25 GHEC orgs × 12500 = 312500 req/hr theoretical aggregate, but sweeps run all installations in parallel and the limit is per-install, not aggregate.

| Math | Value |
|---|---|
| Repos in scope | 3000 |
| Average API calls per repo check | ~10 (4 file checks + branch/PR ops if actionable) |
| Full sweep API cost | 30000 calls |
| Per-install per sweep | 30000 / 25 = 1200 calls per installation |
| Fraction of 12500/hr GHEC budget | ~10% |
| Fraction of 5000/hr non-GHEC budget | ~24% |

A full cold-start sweep is the worst case. Steady-state is much lighter because the `staleSweep.freshness` gate skips repos checked within the freshness window — most sweep ticks enqueue a small fraction of the inventory.

### Recommended chart values for 3000 repos × 25 GHEC orgs

```yaml
replicaCount: 3                      # HA + webhook ingress distribution

config:
  workerCount: 15                    # 15 × 3 = 45 concurrent reconciles
  scheduleInterval: "12h"            # freshness gate carries the day; sweep can run often

staleSweep:
  freshness: "24h"
  batchSize: 500                     # higher = fewer Store round-trips per tick
  rateLimitReserve: 0.15             # 15% reserve for webhook-triggered work

store:
  backend: postgres
  postgres:
    mode: cnpg
    maxConns: 10                     # per-replica → 30 total client connections to pooler
    cnpg:
      cluster:
        instances: 3                 # primary + 2 standbys for failover
      pooler:
        enabled: true
        pgbouncer:
          poolMode: transaction
          defaultPoolSize: 30        # 30 server connections from pooler to Postgres
          maxClientConnections: 100  # 30→100 multiplexing ratio

queue:
  backend: valkey
  valkey:
    mode: baked
    jobAckTimeout: "5m"
    reaperInterval: "30s"

scheduler:
  backend: valkey                    # MANDATORY for multi-replica
```

### Connection-pool math

With the values above:

```
replicas × maxConns       = 3 × 10  = 30 client connections into PgBouncer
PgBouncer defaultPoolSize = 30           server connections to Postgres
Postgres max_connections  = 100          (CNPG default; absorbs pool + admin headroom)
```

PgBouncer in `transaction` mode multiplexes 30 long-lived client conns onto 30 short-lived server conns. The 100 `max_client_conn` headroom is for transient connection storms during pod restarts; with steady-state 30 clients it's never approached.

If you bump replica count further, the formula is `replicas × maxConns ≤ defaultPoolSize`. Either lower `maxConns` per replica or raise `defaultPoolSize` and Postgres `max_connections` in lockstep.

### Cold-start validation

Before turning on multi-replica for a fleet this size, run a full cold-start sweep on a single replica and watch:

| Metric | Healthy | Action if breached |
|---|---|---|
| `repo_guardian_rate_limit_remaining{installation_id="..."}` | Stays above `(1 - rateLimitReserve) × limit` for every install | Raise `staleSweep.rateLimitReserve` (e.g. 0.20) |
| `repo_guardian_rate_limit_reserve_blocked_total` | Increments only briefly during cold start, plateaus after | Lower `staleSweep.batchSize` to spread work over more ticks |
| `repo_guardian_queue_depth` | Drains within a sweep cycle | Raise `config.workerCount` per replica |
| `repo_guardian_store_query_seconds` p99 | < 100ms | Bump CNPG `instances`, add SSD storage class |

Only once those metrics behave on a single replica should you raise `replicaCount`. Multi-replica adds coordination overhead (Valkey leader election, work distribution) — it doesn't fix a misconfigured single replica.

### When the API rate limit is genuinely the bottleneck

If after tuning you still see `reserve_blocked_total` climbing on a steady-state sweep, the only real levers are:

1. **Increase `staleSweep.freshness`** — re-check repos less often. 24h → 48h halves steady-state load.
2. **Disable rules you don't actually need.** Each rule = ~1 API call per repo. Trimming `dependabot` (if you use Renovate exclusively) saves 3000 calls per sweep.
3. **Go to per-org App credentials.** Each App has its own rate-limit budget. 25 separate Apps = 25 × budget. This is the INV-0006 path — currently Deferred; consider promoting if rate limit is the actual scaling wall.
4. **Request a rate-limit increase from GitHub.** Possible on Enterprise contracts, less so otherwise.

Infrastructure scaling does not help once the API rate limit is binding. More replicas, more workers, more Postgres — all of them sit idle waiting for the rate-limit window to refresh.

## Sizing for Postgres

Each Store query is a single round-trip; tracked via `repo_guardian_store_query_seconds`. Watch the p95 — anything above 100ms means the database is the bottleneck and you should look at:

- Storage class (use SSD for the baked StatefulSet).
- `maxConns` (should be ≥ `workerCount + 4`).
- Index health (`pg_stat_user_indexes` on `repo_state`).
- CNPG instance count if running in `mode=cnpg` (default 1; bump to 3 for HA).

## Sizing for Valkey

Valkey is small. The queue + in-flight ZSET + reaper lock fit in single-digit MB even for tens of thousands of jobs. The `queue.valkey.baked` 1Gi PVC is conservative; you'll never fill it.

The bottleneck is BRPOP throughput, not memory. Run `redis-cli -a $VALKEY_PASSWORD INFO commandstats | grep brpop` to inspect; usually p99 is < 1ms.

## Multi-replica gotchas

- **Backends are Postgres + Valkey only (IMPL-0016).** The in-memory store/queue and the in-process ticker scheduler were removed in chart 1.0. There's no single-replica fallback — every deployment needs Postgres + Valkey backing services. The chart's baked modes (`store.postgres.mode=baked`, `queue.valkey.mode=baked`) cover trivial deployments without external infra.
- **Pod restarts cost a `JOB_ACK_TIMEOUT + REAPER_INTERVAL` window.** A worker that crashes mid-`engine.CheckRepo` leaves the job in-flight; the reaper requeues it after the timeout. Lower `JOB_ACK_TIMEOUT` for faster recovery, higher for tolerance to legitimately slow checks.
