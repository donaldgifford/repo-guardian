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

### Write-back observability (IMPL-0015 Phase 0)

Worker write-backs are tracked separately from generic Store queries:

| Metric | Meaning | Healthy signal |
|---|---|---|
| `repo_guardian_store_writeback_total{outcome="ok"}` | Worker finished a job and persisted its outcome. Rises 1:1 with `repo_guardian_repos_checked_total` modulo write-back errors. | Lock-step with `repos_checked_total`. |
| `repo_guardian_store_writeback_total{outcome="error"}` | Worker finished a job but `UpdateRepoState` failed. The work was real (PR opened/updated, files committed) but persistent state did not converge; the stale-sweeper will re-enqueue. | Zero in steady state; transient spikes during DB rolls. |
| `repo_guardian_store_writeback_duration_seconds` | Latency of `UpdateRepoState` from the worker pool. | p99 < 50ms. Bumps in this metric — but flat `store_query_seconds` — point at write-contention or a missing index, not a generic DB problem. |

## Discoverer + BudgetTracker (IMPL-0015 Phase 1)

The Discoverer runs on the leader pod at `DISCOVERY_INTERVAL` (default 1h), enumerates installations + repos via the GitHub API, and persists discovery rows via `Store.UpsertIfMissing`. Newly-discovered rows enter `repo_state` with `LastCheckStatus=pending` and a jittered `LastCheckedAt` so the stale-sweeper picks them up on its next tick without synchronizing every repo's due-time.

The `BudgetTracker` (`internal/budget/`) is a per-installation, leader-scoped cache of the GitHub rate-limit window. Both the StaleSweeper and Discoverer share a single tracker via `bringUp` wire-up so the cost-per-repo accounting reflects total enqueue pressure.

### Discovery metrics

| Metric | Meaning | Healthy signal |
|---|---|---|
| `repo_guardian_repo_discovered_total{installation_id}` | New repos surfaced by the Discoverer per installation. | Bumps on first discovery; flat thereafter (idempotent UpsertIfMissing). |
| `repo_guardian_discovery_duration_seconds` | Wall-clock per `Discoverer.Discover` invocation. | Scales with installations × repos; p99 should fit comfortably inside `DISCOVERY_INTERVAL`. |
| `repo_guardian_discovery_api_calls_total{installation_id, endpoint}` | GitHub API calls the Discoverer made. | Steady rate; `endpoint="list_installation_repos"` is the dominant contributor. |

### Budget metrics

| Metric | Meaning | Healthy signal |
|---|---|---|
| `repo_guardian_api_budget_remaining{installation_id}` | Cached rate-limit budget remaining after local Decrement. Tighter than `rate_limit_remaining` because it includes pending-but-not-yet-completed Enqueues. | Tracks `rate_limit_remaining` but never exceeds it. |
| `repo_guardian_api_budget_spendable{installation_id}` | Additional enqueues the tracker will allow without breaching reserve. | Positive in steady state; dropping to zero is the gate-closing signal. |
| `repo_guardian_api_budget_reserve_fraction{installation_id}` | Operator-configured reserve floor (chart value `discovery.reserveFraction`). | Constant; useful to confirm chart values landed without grepping logs. |
| `repo_guardian_api_budget_utilisation{installation_id}` | `1 - (remaining / limit)`. | Steady; rising values approaching `reserve_fraction` signal the deployment is becoming rate-limit-bound. |
| `repo_guardian_api_budget_refresh_total{installation_id, outcome="error"}` | Failed tracker refresh attempts. | Zero in steady state. Non-zero means the gate is falling open (no snapshot to check against) and the deployment may exceed budget. |
| `repo_guardian_enqueue_gated_by_budget_total{installation_id}` | Enqueues blocked by the BudgetTracker reserve gate. | Zero in normal operation. Non-zero means the deployment is rate-limit-bound — only fixes are `staleSweep.freshness↑`, rules↓, or per-org App credentials (INV-0006). |

### Tuning

- `discovery.reserveFraction` (default 0.20) — operators with smooth load can lower this for higher utilisation; bursty workloads should raise it. Cannot exceed 1.0.
- `discovery.estimatedCostPerRepo` (default 10) — calibrate against `repo_guardian_repos_checked_total` ÷ `rate_limit_remaining` drop per sweep. Bump higher if you see budget exhaustion despite the tracker reporting positive `api_budget_spendable`.
- `discovery.interval` (default 1h) — primary discovery channel is webhooks (`installation_repositories.added` / `repository.created`); the Discoverer is the safety net for missed deliveries. Operators confident in webhook fidelity can stretch this to 6h+.

## Custom-property schema preflight (IMPL-0017 Phase 3)

The `custom_properties` reconciler (API mode) checks every managed property
name — `{Owner, Component}` ∪ the operator's `annotation_properties` targets
— against the org's actual custom-property schema before PATCHing. A
property the schema doesn't define is dropped from the payload (the rest
still syncs in the same PATCH), warned about, and counted. The schema lookup
is cached per org for 30 minutes and de-duplicated across concurrent
worker-pool calls for the same org, so a fleet sweep costs at most one
`GetOrgPropertySchema` API call per org per window regardless of how many
repos in that org are processed concurrently.

### Metric

| Metric | Meaning | Healthy signal |
|---|---|---|
| `repo_guardian_custom_property_missing_schema_total{org, property}` | Sync attempts for a managed property absent from the org's custom-property schema. | Zero in steady state. Non-zero means either the org schema is missing a property definition the policy expects, or `annotation_properties` maps to a stale/renamed property. |

Cardinality is bounded by the operator's `annotation_properties` config
(single digits per org), so this is safe to alert on directly without a
`sum()` — see below.

### Loki matching contract

When the schema fetch succeeds but one or more managed properties are
undefined, the reconciler emits exactly this log line (locked by
`TestAPIMode_FiltersUndefinedMappedProperty` in
`internal/reconciler/custom_properties_test.go` — a Go test asserts the
literal text and keys so this contract can't drift silently):

```
level=WARN msg="custom properties missing from org schema" org=<org> repo=<repo> missing_properties=[<Name1> <Name2> ...]
```

- **Message text** (match on this, not just the level): `custom properties missing from org schema`
- **Structured keys:** `org` (string), `repo` (string), `missing_properties` (list of property names present in the sync attempt but absent from the org schema)

Sample LogQL alerting rule (Loki ruler), grouped by org so each org's
missing-schema streak pages independently:

```yaml
groups:
  - name: repo-guardian-schema-preflight
    rules:
      - alert: RepoGuardianPropertySchemaMissingLogs
        expr: |
          sum by (org) (
            count_over_time({app="repo-guardian"} |= "custom properties missing from org schema" [15m])
          ) > 0
        for: 30m
        labels:
          severity: warning
        annotations:
          summary: "repo-guardian: org {{ $labels.org }} has custom properties missing from its schema"
```

The fail-open path (schema fetch itself errors — 403, 5xx, timeout) is a
separate, distinct log line and is intentionally rarer (once per org per
30-minute TTL window, not once per repo):

```
level=WARN msg="fetching org custom property schema failed; sending unfiltered properties" org=<org> error="<err>"
```

If the App is missing the org-level **Custom properties: read** permission,
every affected org will show exactly this line, once per TTL window, with
the unfiltered payload still syncing (no properties silently dropped due to
a permissions gap — see `docs/usage/policy-reference.md` §Custom Property
Schema Preflight).

The Prometheus-side starter alert for the metric (`RepoGuardianPropertySchemaMissing`)
lives in `charts/repo-guardian/templates/prometheusrule.yaml`; both signals
key off the same `org` value, so a Loki log line and a Prometheus alert for
the same incident always agree on which org to investigate.

## Sizing for Valkey

Valkey is small. The queue + in-flight ZSET + reaper lock fit in single-digit MB even for tens of thousands of jobs. The `queue.valkey.baked` 1Gi PVC is conservative; you'll never fill it.

The bottleneck is BRPOP throughput, not memory. Run `redis-cli -a $VALKEY_PASSWORD INFO commandstats | grep brpop` to inspect; usually p99 is < 1ms.

## Multi-replica gotchas

- **One sweeper, not two (IMPL-0015 Phase 0).** The legacy `sweep` schedule (full-fleet enumeration via the GitHub API) was deleted in `1.0.0-rc.2`. Only the `stale-sweep` schedule runs — it queries the persistent `repo_state` table for rows whose `last_checked_at` is older than `freshness` (or whose `policy_version` differs from the current one) and enqueues those. Behavioural consequence: a brand-new install with no `repo_state` rows produces an empty first sweep. Newly-discovered repos enter `repo_state` via webhook handlers (`installation_repositories.added`, `repository.created`) calling `UpsertIfMissing` with a jittered `LastCheckedAt`; the next sweep tick picks them up once the jitter window elapses.
- **Backends are Postgres + Valkey only (IMPL-0016).** The in-memory store/queue and the in-process ticker scheduler were removed in chart 1.0. There's no single-replica fallback — every deployment needs Postgres + Valkey backing services. The chart's baked modes (`store.postgres.mode=baked`, `queue.valkey.mode=baked`) cover trivial deployments without external infra.
- **Pod restarts cost a `JOB_ACK_TIMEOUT + REAPER_INTERVAL` window.** A worker that crashes mid-`engine.CheckRepo` leaves the job in-flight; the reaper requeues it after the timeout. Lower `JOB_ACK_TIMEOUT` for faster recovery, higher for tolerance to legitimately slow checks.
