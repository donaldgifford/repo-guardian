# Scaling repo-guardian

The operator's sizing guide for repo-guardian v2. It covers what each
role costs, how the evaluator and the remediator scale on their queues,
how to size their Postgres pools, and why the GitHub rate limit, not
CPU, is the constraint at enterprise scale. The roles and values are
introduced in [Turning on repo-guardian v2](v2-onboarding.md);
[Running repo-guardian on AWS](aws.md) covers the infrastructure.

> [!NOTE]
> v2 has no job queue, stale sweep or leader election.
> `staleSweep.*` and `queue.valkey.*` were removed in v2, as were
> `config.workerCount` and `store.postgres.maxConns`; all fail render
> (see [removed chart values](v2-migration.md#removed-chart-values) and
> [controls chart values](v2-migration.md#controls-chart-values)).
> Each repository's evaluation is a Temporal workflow on its own timer,
> and Temporal hands the work to whichever worker pod polls next.

## The roles

| Role | Scales on | State |
| --- | --- | --- |
| `ingest` | webhook rate; it validates the HMAC and starts a workflow | stateless, no database, no App key |
| `evaluator` | the `repo-guardian-eval` queue's backlog | one Postgres pool per pod |
| `remediator` | the `repo-guardian-remediate` queue's backlog | one Postgres pool per pod, one connection per running remediation |
| `api` | read traffic from the UI and API clients | an 8-connection read-only pool per pod |

Any number of replicas of any role can run at once. There is no leader:
Temporal dispatches each workflow and activity task to one poller, and
the Schedules (`controls-discovery`, `controls-snapshot`,
`remediation-sweep`, `controls-discovery-remediate`) are server-side.

## Sizing knobs

| Value | Env var | Default | When to tune |
| --- | --- | --- | --- |
| `ingest.replicas` | | `2` | Rarely. Ingest does no I/O beyond starting a workflow. |
| `evaluator.replicas` | | `1` | Fixed replica count. Ignored when `evaluator.keda.enabled`. |
| `evaluator.concurrency` | `EVALUATOR_CONCURRENCY` | `10` | Concurrent activities per evaluator pod. Raise only while the GitHub budget has room (see below). |
| `evaluator.dbPoolSize` | `EVALUATOR_DB_POOL_SIZE` | `0` (pgxpool's default) | Set it to about `evaluator.concurrency` when `store_pool_wait_seconds{role="evaluator"}` shows waits. |
| `evaluator.resources` | | `{}` (falls back to `resources`) | Per-pod requests and limits. |
| `remediator.replicas` | | `1` | Fixed replica count. Ignored when `remediator.keda.enabled`. |
| `remediator.concurrency` | `REMEDIATOR_CONCURRENCY` | `10` | Concurrent remediation activities per pod. |
| `remediator.dbPoolSize` | `REMEDIATOR_DB_POOL_SIZE` | `0` (concurrency + 4) | Must be at least `remediator.concurrency`, or the remediator refuses to start. |
| `remediator.resources` | | `{}` (falls back to `resources`) | Per-pod requests and limits. |
| `api.replicas` | | `2` | Read traffic. Each replica holds up to 8 database connections. |
| `evalInterval` | `EVAL_INTERVAL` | `24h` | Each repository's evaluation cadence. The main lever on steady-state API cost. |
| `policyRolloutWindow` | `POLICY_ROLLOUT_WINDOW` | `24h` | How long a policy change is spread over. Shorter = a bigger burst on every policy change. |
| `discovery.interval` | `DISCOVERY_INTERVAL` | `1h` | Discovery is the safety net behind the webhooks; it costs one repository listing per installation per run. |
| `remediator.sweepBatch` | `REMEDIATION_SWEEP_BATCH` | `500` | Due remediations one hourly sweep starts. |
| `remediator.sweepSpacing` | `REMEDIATION_SWEEP_SPACING` | `30s` | Gap between remediations the sweep starts on one installation; keeps a first enablement under GitHub's secondary rate limit. |
| (via `extraEnv`) | `RATE_LIMIT_THRESHOLD` | `0.10` | Fraction of each installation's limit held back as a reserve. |

## Autoscaling with KEDA

`templates/worker-scaledobject.yaml` renders one ScaledObject per
controls role, each on its own queue's backlog, so the evaluator and the
remediator scale independently. It needs the KEDA CRDs,
`topology: split` (in `all` one pod runs every role, so there is
nothing per-role to scale), and that role's App id.

| Value | Default | Meaning |
| --- | --- | --- |
| `keda.trigger` | `prometheus` | `prometheus` reads the Temporal server's `approximate_backlog_count`; `temporal` asks the frontend directly and is refused with `temporal.auth.oidc` |
| `keda.prometheus.serverAddress` | `""` | the Prometheus that scrapes the Temporal server; required with `prometheus` once a role enables KEDA |
| `keda.prometheus.authenticationRef` | `""` | an operator-owned TriggerAuthentication for that Prometheus |
| `keda.prometheus.namespaceLabel` | `exported_namespace` | the label holding the Temporal namespace; `namespace` with `honorLabels: true` |
| `<role>.keda.enabled` | `false` | render the role's ScaledObject; KEDA then owns its replica count |
| `<role>.keda.minReplicas` | `1` evaluator, `0` remediator | floor; the remediator may scale to zero, since promotion does not need a running remediator |
| `<role>.keda.maxReplicas` | `4` | ceiling |
| `<role>.keda.targetQueueSize` | `"50"` | backlog per replica before KEDA scales out |
| `<role>.keda.fallbackReplicas` | `null` (the role's `replicas`) | replicas held when the trigger fails three polls in a row |
| `<role>.keda.query` | `""` | override the default query |

The default query, for the evaluator in the Temporal namespace
`repo-guardian`:

```promql
sum(max by (partition, task_type, task_priority, worker_build_id) (
  approximate_backlog_count{exported_namespace="repo_guardian", taskqueue="repo_guardian_eval"}
))
```

Temporal sanitises label values, so `-` becomes `_` in both the
namespace and the queue (`repo_guardian_remediate` for the
remediator). The `max by` is required: there is no aggregate series, and
each partition, task type, priority and build id is its own.

**Size `maxReplicas` to the GitHub budget, not the backlog.**
Evaluations are rate-limit bound. Once the installations' budgets are
spent, an extra pod only parks more timers. A policy rollout makes a
large backlog by design and drains it across `policyRolloutWindow`.

## Postgres pool sizing

Each controls pod opens one pool with its own role's DSN
(`STORE_DSN_EVALUATOR` or `STORE_DSN_REMEDIATOR`, falling back to
`STORE_DSN`).

- **Evaluator.** `evaluator.dbPoolSize: 0` keeps pgxpool's default,
  the larger of 4 and the pod's CPU count. With `concurrency: 10` on a
  small pod that can be fewer connections than activities. Watch
  `store_pool_wait_seconds{role="evaluator"}` and set `dbPoolSize` to
  about `concurrency` if it shows waits.
- **Remediator.** Every running remediation holds one connection for
  its row lock for the life of the run, and its journal writes take a
  second, short one. So the pool must be at least the concurrency:
  `remediator.dbPoolSize: 0` means `concurrency + 4`, and a smaller
  explicit value fails at startup with
  `REMEDIATOR_DB_POOL_SIZE (N) is smaller than REMEDIATOR_CONCURRENCY (M): every running remediation holds a connection`.
- **API.** A fixed 8-connection read-only pool per replica.

### Connection math

The ceiling the database (or RDS Proxy) must absorb:

```text
  evaluator pods  × evaluator pool
+ remediator pods × remediator pool
+ api replicas    × 8
+ 1               (the migrate Job, during a release)
```

Use KEDA's `maxReplicas` for the pod counts, not the current ones. For
example, 4 evaluators × 12 + 2 remediators × 14 + 2 api × 8 + 1 = 93
connections. Behind PgBouncer (`store.postgres.cnpg.pooler`) or RDS
Proxy, size the pooler's client limit to that and its server pool to
what Postgres's `max_connections` leaves after admin headroom.

## The GitHub rate budget

At more than a handful of organisations the binding constraint is
GitHub's per-installation rate limit. A GitHub App installation gets
5,000 requests an hour, more with repository and user count up to
12,500, and 15,000 on GitHub Enterprise Cloud organisations. Each
installation's limit is its own; there is no aggregate.

repo-guardian keeps one budget workflow per App per installation,
`installation/eval/<installation id>` and
`installation/remediate/<installation id>`, so the Evaluation App and
the Remediation App never spend each other's budget. Before every
evaluation or remediation the activity asks that workflow for a lease:

- The cost of a lease is a moving average of what recent runs actually
  spent, seeded at 20 calls.
- A lease is granted when `remaining − reserved − reserve ≥ cost`. The
  reserve is `limit × RATE_LIMIT_THRESHOLD` (10% by default), **halved**
  for webhook-triggered and human-requested work (priority 1 and 2), so
  when the budget is scarce the checks someone is waiting on go first.
- Before the first report of a window, the budget is unknown and the
  lease is granted optimistically.
- A refused lease returns a time: the installation's reset plus up to
  60 seconds of spread. The evaluation waits on a durable Temporal
  timer and retries under the same check key. It holds no worker slot
  while it waits, and nothing is recorded.
- A suspended installation is held, rechecked hourly, until it is
  unsuspended.

### The math

Steady-state cost per installation per hour is roughly:

```text
(repositories in the installation ÷ evalInterval in hours) × calls per evaluation
```

Calls per evaluation depend on the controls assigned; the budget
workflow learns it per installation, and
`http_client_request_duration_seconds_count` shows it. As an example
only, at 20 calls per evaluation and `evalInterval: 24h`, a 1,200-repo
installation spends about 1,000 calls an hour, under 7% of a 15,000/hour
GitHub Enterprise Cloud budget. Pushes and pull-request events add
targeted evaluations on top. A policy rollout re-evaluates every
managed repository across `policyRolloutWindow`, so a 24h window costs
about one extra day of steady state.

### Signals

| Metric | Meaning | Healthy |
| --- | --- | --- |
| `repo_guardian_rate_limit_remaining{installation_id}` | GitHub's remaining budget, set from the response headers by every evaluation | stays above the reserve; `RepoGuardianRateLimitNearExhaustion` fires below 200 |
| `repo_guardian_budget_acquire_total{result}` | lease requests: `granted`, `optimistic`, `wait` | `wait` is occasional; a steady `wait` rate means the fleet asks for more than the budget gives |
| `repo_guardian_evaluation_freshness_seconds{org}` | age of the previous result when a control is re-evaluated | close to `evalInterval`; `RepoGuardianEvaluationFreshness` watches it |
| `temporal_activity_schedule_to_start_latency_seconds` | how long activities wait for a worker slot | `RepoGuardianTemporalActivityBacklog` fires at a p99 over a minute |

### When the budget is the bottleneck

If evaluations fall behind `evalInterval` while `budget_acquire_total`
shows steady `wait`s, infrastructure does not help: more replicas or
more concurrency only wait for the same reset. The levers are:

1. **Lengthen `evalInterval`.** 24h to 48h halves steady-state cost.
   Webhooks still evaluate a repository when a push touches a path a
   control owns or reads.
2. **Assign fewer controls.** Each assigned control adds its reads to
   every evaluation of the repositories it applies to.
3. **Lengthen `policyRolloutWindow`** if the burst after a policy change
   is what runs the budget out.
4. **Ask GitHub for a higher limit,** possible on some Enterprise
   contracts.

Separate App registrations per organisation would give each its own
budget; that was investigated and deferred
([INV-0006](../investigation/0006-per-org-github-app-credentials.md)).

<a id="delayed-requeue-impl-0022"></a>

> [!NOTE]
> The 1.x delayed-requeue queue was removed in v2. A throttled
> evaluation or remediation now waits on the budget workflow's reset
> time as a Temporal timer, as described above.

## Remediation throughput

Remediation is limited by GitHub's **secondary** rate limit on
content-creating requests (about 500 an hour), not only the primary
one. A pull request costs about four (branch, commit, pull request,
labels). The hourly `remediation-sweep` starts up to
`remediator.sweepBatch` due remediations, `remediator.sweepSpacing`
apart per installation: at the default 30s, about 120 per installation
per hour. The rest stay due for the next sweep. Raise `sweepSpacing` if
other tools share the Remediation App's limit; do not lower it on a
first enablement, when every non-compliant repository becomes due at
once.

| Metric | Meaning |
| --- | --- |
| `repo_guardian_remediation_runs_total{org,control,result}` | remediation runs and lifecycle closes |
| `repo_guardian_remediation_prs_open{org,control,age_bucket}` | open remediation pull requests (exported by the api; query with `max by`) |
| `repo_guardian_remediation_holds{org,reason,age_bucket}` | results held from remediation (exported by the api; query with `max by`) |
| `repo_guardian_remediation_blocked_total{org,reason}` | remediations blocked; `RepoGuardianRemediationBlocked` |
| `repo_guardian_remediation_reservations_expired_total{org}` | pull-request reservations freed by maintenance |
| `repo_guardian_remediation_cleanup_latency_seconds{org}` | withdrawal or compliance to the closing write |

## Repository parking

A repository whose evaluation can never succeed, or that no control can
apply to, is *parked*: its `repositories` row gets `active = false` and
a `park_reason`, and its evaluation workflow ends. The row and its
history are kept. Parking is why fleet cost tracks managed repositories
rather than every repository the App can see.

| `park_reason` | Trigger | Results |
| --- | --- | --- |
| `archived`, `fork` | the repository is archived or a fork | cleared: nothing applies |
| `removed` | the repository was deleted or removed from the installation | cleared |
| `installation_removed` | the Evaluation App was uninstalled | cleared |
| `access_denied` | the App could not read it | kept: nothing was learned |

Only discovery un-parks, because only discovery sees the installation's
real repository set. A push to a parked repository leaves it parked.
Un-parking therefore lags by up to one `discovery.interval`.

To see what is parked and why:

```sql
SELECT org, name, park_reason, parked_at, last_error
FROM repositories
WHERE NOT active
ORDER BY org, name;
```

## Compliance posture

<a id="compliance-posture-impl-0023-phases-12"></a>

Compliance is **state** read back from Postgres, not counters
incremented at check time. A counter cannot answer "how many
repositories fail CODEOWNERS right now": it only grows, counts a
repository checked twice twice, and resets on restart.

```text
evaluator records a result
  └─> control_results  (Postgres, one row per repository × control)
        └─> PostureExporter.Export  (every api replica, every 60s)
              └─> controls_status{org,control,bucket} / controls_coverage{org,control}
                  remediation_prs_open{org,control,age_bucket} / remediation_holds{org,reason,age_bucket}
                    └─> the E1 and E2 dashboards
```

| Metric | Meaning |
| --- | --- |
| `repo_guardian_controls_status{org,control,bucket}` | repositories per control in each bucket: `compliant`, `non_compliant`, `pending`, `stale`, `unmeasurable`, `not_applicable`, `unknown` |
| `repo_guardian_controls_coverage{org,control}` | repositories the control is actively assigned to |
| `repo_guardian_remediation_prs_open{org,control,age_bucket}` | open remediation pull requests by age |
| `repo_guardian_remediation_holds{org,reason,age_bucket}` | held results by hold reason and age |

The exporter runs in the api role, so the posture gauges exist only
where an api runs: `api.enabled` in `split`, always in `all`.
`posture.snapshotInterval` (`COMPLIANCE_SNAPSHOT_INTERVAL`, `24h`) is a
different thing: the cadence of the `compliance_history` rows that
`repo-guardian report --since` reads, kept permanently.

### Every api replica publishes

Every api replica reads the same rows and publishes the same gauges. Two
consequences bite if you do not know them:

1. **Aggregate with `max by (...)`, never `sum`.** `sum` over replicas
   multiplies the fleet by the replica count. A replica that stopped
   exporting keeps serving its last values until it restarts, and `max`
   is still correct then. The generated dashboards do this, and
   `TestSuite_PostureQueriesDedupeAcrossReplicas` requires every posture
   series to sit directly inside a `max by`. Hand-written panels are on
   their own.

2. **`sum(max by (org, control) (...))` for a fleet total, in that
   order.** The inner aggregation collapses replicas; the outer adds
   orgs and controls. Reversed, it reports the largest org rather than
   the fleet, and the number looks plausible.

```promql
# Non-compliant repositories across the fleet.
sum(max by (org, control, bucket) (repo_guardian_controls_status{bucket="non_compliant"}))

# Results held on a conflict for more than a week.
sum(max by (org, reason, age_bucket) (
  repo_guardian_remediation_holds{reason="conflict", age_bucket=~"7-30d|30d\\+"}
))
```

The export reads the whole aggregate **before** resetting the gauges.
Reset-then-read would blank every series for the length of the query,
and a scrape in that window reads as a vanished fleet. On a read
failure the previous values stay, and the api logs
`controls posture export failed; keeping the previous values`.

### Compliance is "no data" when nothing is measured

The dashboards, the API and `repo-guardian report` refuse to divide by
zero and refuse to call the result 100%. The dashboard compliance query
filters its denominator (`compliant + non_compliant`) to `> 0` and adds
`and on (...) (controls_coverage > 0)`, so a control with nothing
measured or nothing assigned renders "no data". The API and report
return a null percentage for the same case (`store.CompliantPercent`).

<a id="custom-property-schema-preflight-impl-0017-phase-3"></a>

> [!NOTE]
> The 1.x custom-property schema preflight, its log line and its
> counter were removed in v2. The `custom_properties` control reads the
> organisation's property schema itself: a property the schema does not
> define is withheld from the write and reported in the control's
> result, with "define the property in the org's custom property
> schema" as the fix. See
> [`custom_properties`](../usage/controls-reference.md#custom_properties).

## OpenTelemetry series

Off-the-shelf OTel instrumentation on the transport boundaries exports
through the Prometheus bridge into the **same registry `/metrics`
already serves**. There is no collector and no second scrape target.
`OTEL_SDK_DISABLED=true` turns it all off.

| Boundary | Package | Series |
|---|---|---|
| Inbound HTTP (ingest's webhook routes, the api) | `otelhttp` | `http_server_request_duration_seconds`, `http_server_request_body_size_bytes`, `http_server_response_body_size_bytes` |
| GitHub client (REST and GraphQL) | `otelhttp` | `http_client_request_duration_seconds`, `http_client_request_body_size_bytes` |
| Postgres | `otelpgx` | `db_client_operation_duration_seconds`, `db_client_operation_errors_total`, `pgxpool_*` |
| Temporal SDK | `go.temporal.io/sdk/contrib/opentelemetry` | `temporal_*` (activity and workflow-task latency, slots, pollers) |

Things worth knowing:

- **Pool stats are `pgxpool_*`,** not the semconv
  `db_client_connection_*` family. `repo_guardian_store_pool_wait_seconds{role}`
  is the domain metric for pool waits per controls role.
- **Only the application routes are instrumented inbound.** `/healthz`,
  `/readyz` and `/metrics` are not.
- **The client histogram under-counts GitHub calls under rate-limit
  pressure.** go-github's own pre-check short-circuits above the
  transport once it has seen `remaining=0`, so those calls are never
  measured. Use `budget_acquire_total{result="wait"}` for throttle
  volume.
- **Every panel picks one source.** `repo_guardian_store_query_seconds{operation}`
  knows which API read is slow; the semconv database metrics know
  whether the time went to the pool or the query. Neither answers the
  other's question, so no panel mixes them.
- **Cardinality is closed deliberately.** `server.address` and
  `server.port` are pinned (left to default they derive from the
  client-controlled `Host` header), and scope labels are dropped, so a
  dependency bump does not reset every series.
