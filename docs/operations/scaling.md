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

- **Memory mode is single-replica.** `store.backend=memory` and `queue.backend=memory` deliver every ticker fire to every replica's worker pool — N replicas means N times the work and N times the GitHub API consumption. The chart will run, but you'll see duplicate PRs. Always pair memory backends with `replicaCount: 1`.
- **Scheduler backend mismatches.** `scheduler.backend=valkey` requires `queue.backend=valkey` because they share the redis client. The chart will surface a clear startup error if you misconfigure.
- **Pod restarts cost a `JOB_ACK_TIMEOUT + REAPER_INTERVAL` window.** A worker that crashes mid-`engine.CheckRepo` leaves the job in-flight; the reaper requeues it after the timeout. Lower `JOB_ACK_TIMEOUT` for faster recovery, higher for tolerance to legitimately slow checks.
