# contrib/

Assets for operating repo-guardian in production.

## The two tiers

Dashboards and alerts are **generated from your `guardian.hcl`**, not
hand-maintained. That is the point of IMPL-0023: a panel charting a
rule you do not run renders empty forever, and an empty panel is
indistinguishable from a compliant fleet. Generating from the config
means a panel cannot outlive the rule it charts, and an alert whose
mechanism is switched off is never emitted at all.

```bash
repo-guardian monitoring generate --config guardian.hcl --out ./monitoring
```

Two tiers live here:

| Tier | Path | What it is |
|------|------|------------|
| **Generated** | `generated/` | Committed output of `make monitoring-generate` against the **built-in defaults**. A worked example, and what CI diffs against to prove the generator still runs. Do not edit — the drift gate will fail and your edit will be overwritten. |
| **Hand-maintained** | `loki/rules.yaml` | Loki ruler recording and alerting examples. Not generated, because what is worth recording depends on your fleet size and your Prometheus's cardinality budget. |

`generated/` is generated from the built-in defaults, so it shows the
19 alerts and four dashboards that a default policy engages. Six more
alerts exist and are emitted only when the policy engages the mechanism
they watch:

| Alert | Emitted when the policy |
|-------|-------------------------|
| `RepoGuardianCatalogParseFailures` | runs the `custom_properties` reconciler in `api` mode |
| `RepoGuardianPropertySchemaMissing` | runs the `custom_properties` reconciler in `api` mode |
| `RepoGuardianPropertiesPRBurst` | runs `custom_properties` in `github-action` mode |
| `RepoGuardianSettingRemediationChurn` | declares `rule "setting"` blocks |
| `RepoGuardianBranchProtectionChurn` | declares `rule "branch_protection"` blocks |
| `RepoGuardianRuleNeverApplies` | declares a top-level `scope { orgs }` block |

Generate against your own config to get the ones that apply to you.
That gating is the fix for INV-0012 finding A: an alert watching a
series with no producer never fires, and never fires looks exactly like
never fails.

## The dashboards

Four, each answering a different question, and deliberately not
combinable. A business gauge next to a service counter cannot be read:
when the picture looks wrong there is no way to tell which half is
lying (DESIGN-0022 Finding I).

| Dashboard | Answers | Reads |
|-----------|---------|-------|
| `repo-guardian-kpi` (E1) | Is the fleet compliant, and which rule is failing? | Prometheus, business tier |
| `repo-guardian-detail` (E2) | Which organisation? | Prometheus, business tier |
| `repo-guardian-system` (E3) | Is the service itself healthy? | Prometheus, service + infra tiers |
| `repo-guardian-logs` (E4) | Which repository, and why? | Loki |

E4 exists because that last question cannot be answered by a metric: a
repository label on a 20,000-repo fleet is a cardinality bomb
(Finding G), so the per-repository answer lives in the logs. Its
panels match on specific log lines, and
`TestLogLines_AreStillEmittedByTheBinary` fails the build if any of
those lines stops being emitted.

**E4's stream selector is the one thing you will probably have to
change.** Stream labels are minted by your log shipper, not by
repo-guardian, so the `{app="repo-guardian"}` default is a convention:

```bash
repo-guardian monitoring generate --config guardian.hcl \
  --loki-selector 'job="platform/repo-guardian"'
```

A blank E4 is far more likely to be a selector mismatch than a silent
fleet. The first panel's description prints the selector in use.

## Where the old files went

| Was | Now |
|-----|-----|
| `grafana/repo-guardian-dashboard.json` | Replaced by the four generated dashboards. The 61-panel original mixed business and service tiers on one screen, and its panels drifted from the rules that fed them — both problems the generator removes structurally. |
| `prometheus/alerts.yaml` | Replaced by `generated/alerts/rules.yaml`. A second hand-maintained copy of the alert set is precisely the drift the generator exists to remove. |

One alert did not survive the move: `RepoGuardianRateLimitLow` watched
the unlabelled `github_rate_remaining` gauge. The generated tier alerts
on `min(repo_guardian_rate_limit_remaining) < 200` instead, which
carries `installation_id` and so names the installation that is out of
budget rather than reporting that some installation is.

## Exposed Metrics

repo-guardian exposes these metrics on the dedicated metrics
listener (default `:9090/metrics`). All metric names are prefixed
with `repo_guardian_`.

### Repository checks

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `repos_checked_total` | CounterVec | `trigger`, `org` | Total repositories processed, broken out by trigger source (`webhook`, `scheduler`, `push`) and GitHub org. |
| `check_duration_seconds` | Histogram | — | Time to check a single repository, default Prometheus buckets. |

Example:

```promql
# Per-org check rate
sum by (org) (rate(repo_guardian_repos_checked_total[5m]))

# p99 check duration
histogram_quantile(0.99,
  sum(rate(repo_guardian_check_duration_seconds_bucket[5m])) by (le))
```

### File rules

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `files_missing_total` | CounterVec | `rule_name`, `org` | Number of times a file rule found a missing file requiring action. |
| `files_forbidden_present_total` | CounterVec | `rule_name`, `org` | Forbidden files detected present by an `absent`-mode rule (IMPL-0019). The absent-rule analogue of `files_missing_total`. |
| `rule_gate_closed_total` | CounterVec | `rule_name`, `org`, `reason` | File-rule evaluations skipped because a `when { rule_satisfied }` gate was closed (IMPL-0019). `reason=not_satisfied` is normal operation; `reason=error` means the referee rule's evaluation failed and the gate failed closed. |
| `prs_created_total` | CounterVec | `org` | Pull requests created by repo-guardian for missing files. |
| `prs_updated_total` | CounterVec | `org` | Existing PRs updated with new files. |

Example:

```promql
# PR creation rate per org
sum by (org) (rate(repo_guardian_prs_created_total[5m]))

# Top 10 rules by missing-file detections
topk(10, sum by (rule_name) (rate(repo_guardian_files_missing_total[1h])))

# Forbidden files still present, by absent-mode rule (IMPL-0019)
sum by (rule_name, org) (rate(repo_guardian_files_forbidden_present_total[1h]))

# Gate fail-closed errors — referee rule evaluation failing (should be ~zero)
sum by (rule_name, org) (rate(repo_guardian_rule_gate_closed_total{reason="error"}[1h]))
```

### PR drift and convergence (IMPL-0013 Phase 1)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `pr_open_with_empty_actionable_total` | CounterVec | `org` | Reconcile passes where an open repo-guardian PR exists but no rule is actionable — the INV-0005 drift surface. A non-zero rate after IMPL-0013 Phase 3 lands indicates convergence is not working. |
| `open_prs_by_rule` | GaugeVec | `org`, `rule`, `age_bucket` | Snapshot of currently-open repo-guardian PRs, attributed to each rule referenced by the PR and bucketed by age (`<1d`, `1-7d`, `7-30d`, `30d+`). Reset to zero at the start of each sweep so {org, rule} combinations that drop to zero stop reporting. |

Example:

```promql
# Drift rate per org (fires the RepoGuardianPRDrift alert)
sum by (org) (rate(repo_guardian_pr_open_with_empty_actionable_total[1h]))

# Stuck PRs older than 30 days, by org and rule
sum by (org, rule) (repo_guardian_open_prs_by_rule{age_bucket="30d+"})

# Per-rule fleet-wide stuck-PR breakdown (find rules with the worst convergence)
topk(10, sum by (rule) (repo_guardian_open_prs_by_rule))

# Aging distribution for a single rule
sum by (age_bucket) (repo_guardian_open_prs_by_rule{rule="codeowners"})
```

The `open_prs_by_rule` gauge is a per-sweep snapshot; expect a brief
zero window at sweep start before workers re-populate it. Average
the gauge or use `max_over_time` if your scrape interval is shorter
than your sweep interval.

### Setting rules

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `settings_checked_total` | CounterVec | `rule_name`, `org` | Setting rule evaluations. |
| `settings_mismatched_total` | CounterVec | `rule_name`, `org` | Setting rules that found a mismatch with the expected value. |
| `settings_remediated_total` | CounterVec | `rule_name`, `org` | Setting rules that were remediated via the API. |

### Branch protection

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `branch_protection_checked_total` | CounterVec | `rule_name`, `org` | Branch protection rule evaluations. |
| `branch_protection_remediated_total` | CounterVec | `rule_name`, `org` | Branch protection rules that were remediated via the rulesets API. |

### Scope and ignore

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `ignored_total` | CounterVec | `scope`, `org` | Repos or rules skipped by ignore lists. `scope=global` for top-level ignore matches; `scope=rule` for per-rule ignore matches. |
| `out_of_scope_total` | CounterVec | `level`, `org` | Rule evaluations skipped by strict-mode scope. `level=policy` increments by N (enabled-rule count) when the top-level scope rejects the repo. `level=rule` increments by 1 per rule when its scope rejects the repo. Always 0 in legacy mode. |

Example:

```promql
# Detect a rule that never applies (likely typo in scope.orgs)
sum by (org) (rate(repo_guardian_out_of_scope_total{level="rule"}[1h]))
  > 0 unless
sum by (org) (rate(repo_guardian_files_missing_total[1h])) > 0
```

### Webhooks

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `webhook_received_total` | CounterVec | `event_type` | Webhook events received from GitHub. |
| `webhook_rejected_total` | CounterVec | `reason` | Webhooks rejected by the IP allowlist. |

### Errors

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `errors_total` | CounterVec | `operation`, `org` | Errors encountered, broken out by operation (`create_install_client`, `check_repo`, etc.). |

### GitHub API

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `github_rate_remaining` | Gauge | — | Remaining GitHub API quota (app-scoped client). |

The `github_rate_limit_waits_total` / `_wait_seconds` pair was removed in IMPL-0022: the transport no longer sleeps on rate-limit pressure, it defers the whole job. See `queue_delayed_total` / `queue_delay_seconds` under §Delayed requeue.

### Custom properties

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `properties_checked_total` | Counter | — | Repositories where custom properties were evaluated. |
| `properties_prs_created_total` | Counter | — | PRs created for custom properties. |
| `properties_set_total` | Counter | — | Repositories where properties were set via API. |
| `properties_already_correct_total` | Counter | — | Repositories where properties already matched. |

### Custom-property sync (DESIGN-0019 / IMPL-0017 / IMPL-0020)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `custom_property_cleared_total` | CounterVec | `org` | Managed properties cleared (JSON `null`) because their source annotation was removed from `catalog-info.yaml` — the DESIGN-0019 full-state-sync contract. A sustained rate here with no catalog edits suggests a fight with an org-schema `default_value` (clears re-inherit the default and re-trigger drift). |
| `custom_property_missing_schema_total` | CounterVec | `org`, `property` | Sync attempts for a managed property the org's custom-property schema does not define. The preflight skips these (rest of the payload still syncs) and warns once per org per 30-minute schema-cache window. |
| `catalog_parse_failed_total` | CounterVec | `org` | Custom-properties reconciles skipped because `catalog-info.yaml` failed to parse (IMPL-0020 A1 — malformed files are never overwritten with defaults). |

Example:

```promql
# Which properties are missing from which org's schema
sum by (org, property) (increase(repo_guardian_custom_property_missing_schema_total[24h]))

# Repos drifting because their catalog-info.yaml is broken
sum by (org) (increase(repo_guardian_catalog_parse_failed_total[24h]))
```

### Multi-replica / scheduler / store / queue (IMPL-0011)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `queue_depth` | Gauge | `queue` | Current queue depth (jobs waiting to be claimed). |
| `queue_enqueued_total` | CounterVec | `queue` | Jobs enqueued. |
| `queue_claimed_total` | CounterVec | `queue` | Jobs claimed by a worker. |
| `queue_acked_total` | CounterVec | `queue`, `outcome` | Jobs ack'd after processing. `outcome={success,error,deferred}` (`deferred` = parked into the delayed set, IMPL-0022). |
| `queue_reaped_total` | CounterVec | `queue` | Jobs requeued by the in-flight reaper after ack-window expiry. |
| `scheduler_is_leader` | Gauge | `name`, `pod` | 1 on the replica holding the leader lock, 0 elsewhere. One series per schedule per pod (`stale-sweep`, `discovery`, `posture-export`) — sum across pods for a given `name` should be exactly 1. |
| `scheduler_sweep_batch_size` | Histogram | — | Distribution of `StaleRepos` batch sizes per sweep tick. |
| `store_query_seconds` | Histogram | `op`, `outcome` | Persistent store query latency. `op` enumerates `GetRepoState`, `UpdateRepoState`, `StaleRepos`, `UpsertIfMissing`, etc. |
| `rate_limit_remaining` | Gauge | `installation_id` | Per-installation GitHub rate-limit budget, sampled once per installation per sweep. Observability only — the sweep no longer gates on it (IMPL-0022 Phase 6); throttled work defers instead. |

Example:

```promql
# Queue depth alarm signal
max(repo_guardian_queue_depth{queue="jobs"})

# Worker outcome breakdown
sum by (outcome) (rate(repo_guardian_queue_acked_total[5m]))

# Leader stability — should be exactly one replica per schedule
sum by (name) (repo_guardian_scheduler_is_leader)

# Per-op store latency p99
histogram_quantile(0.99,
  sum by (op, le) (rate(repo_guardian_store_query_seconds_bucket[5m])))
```

### Delayed requeue (IMPL-0022)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `queue_delayed_depth` | Gauge | — | Jobs parked in the delayed set awaiting promotion. Published by every pod's reaper tick. Sustained > 100 drives `RepoGuardianQueueBackpressure`. |
| `queue_delayed_total` | CounterVec | `reason`, `installation_id` | Deferral events (whole jobs parked with a due-time, e.g. `reason=rate_limit`). The single source for throttle counting — replaced `github_rate_limit_waits_total`. |
| `queue_delay_seconds` | Histogram | `reason` | Deferral horizon (due-time minus now) at defer time. Buckets 1s–4h. |
| `queue_wait_seconds` | Histogram | `installation_id` | Enqueue-to-claim latency, including parked time — the DESIGN-0015 partition go/no-go datum. Expect top-bucket skew during fleet onboarding / policy bumps. |
| `queue_attempts_exhausted_total` | CounterVec | `installation_id` | Jobs dropped at `MAX_JOB_ATTEMPTS` with a terminal `repo_state` error. Drives `RepoGuardianJobsExhausted`. |

Example:

```promql
# Who is being throttled, and how hard
sum by (installation_id) (increase(repo_guardian_queue_delayed_total{reason="rate_limit"}[1h]))

# Per-installation p99 queue wait — tenant divergence is the partition signal
histogram_quantile(0.99,
  sum by (installation_id, le) (rate(repo_guardian_queue_wait_seconds_bucket[6h])))
```

### PR convergence (IMPL-0013 Phase 3)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `prs_closed_total` | CounterVec | `org`, `reason` | Repo-guardian PRs auto-closed by the convergence path. `reason=satisfied` when all file rules are satisfied on `main`. |
| `pr_orphan_left_total` | CounterVec | `org` | Orphan files (rules whose file is on the reconcile branch but no longer in actionable) that could not be cleaned up via DeleteFile — usually a transient API glitch. Next sweep retries. |

Example:

```promql
# Convergence rate per org (PRs successfully auto-closed)
sum by (org) (rate(repo_guardian_prs_closed_total{reason="satisfied"}[1h]))

# Orphan-cleanup failures (next sweep retries)
sum by (org) (rate(repo_guardian_pr_orphan_left_total[1h]))
```

### Worker write-back (IMPL-0015 Phase 0)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `store_writeback_total` | CounterVec | `installation_id`, `outcome` | Worker persisted job outcome to the state store. Rises 1:1 with `repos_checked_total` modulo errors. `outcome={ok,error}`. |
| `store_writeback_duration_seconds` | Histogram | — | Latency of `UpdateRepoState` from the worker pool. Target p99 < 50ms. |

Example:

```promql
# Write-back error rate (should be ~zero in steady state)
sum(rate(repo_guardian_store_writeback_total{outcome="error"}[5m]))
  / sum(rate(repo_guardian_store_writeback_total[5m]))

# Write-back p99 (alarm if > 50ms sustained)
histogram_quantile(0.99,
  sum(rate(repo_guardian_store_writeback_duration_seconds_bucket[5m])) by (le))
```

### Discoverer (IMPL-0015 Phase 1)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `repo_discovered_total` | CounterVec | `installation_id` | New repos surfaced by the Discoverer per installation. Bumps on first discovery; idempotent thereafter. |
| `discovery_duration_seconds` | Histogram | — | Wall-clock per `Discoverer.Discover` invocation. |
| `discovery_api_calls_total` | CounterVec | `installation_id`, `endpoint` | GitHub API calls the Discoverer made. `endpoint={list_installations,list_installation_repos}`. |

Example:

```promql
# Cumulative discovered repos per installation
sum by (installation_id) (repo_guardian_repo_discovered_total)

# Discovery cost per tick
sum by (endpoint) (rate(repo_guardian_discovery_api_calls_total[1h]))
```

### Compliance posture (IMPL-0023 Phase 2)

The posture gauges answer "how many repositories are failing right
now", which the pre-existing counters cannot: a counter only grows, so
a repository fixed yesterday still shows in its rate (INV-0013 Finding
B). They are projected from the `rule_state` table by a leader-scoped
`posture-export` handler on `POSTURE_EXPORT_INTERVAL` (default 60s),
which resets and re-sets every series each tick so a rule or org that
leaves the fleet stops being reported instead of freezing at its last
value.

**Aggregate with `max by (...)`, never `sum`.** Only the leader
publishes, but during a failover the outgoing and incoming leaders can
briefly both hold series, and non-leaders retain whatever they last
published before losing the lock. `max` is correct in both states;
`sum` double-counts through every leader change.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `repos_actionable` | GaugeVec | `rule_name`, `org` | Repositories currently failing each rule. Covers file, setting, and branch-protection rules. Absent series means no repo fails that rule — not that the rule is unevaluated. |
| `repos_tracked` | GaugeVec | `org` | Active repositories with any posture — the compliance denominator. Excludes parked repos. |
| `repos_unmeasurable` | GaugeVec | `org`, `reason` | Standing population of parked repositories, excluded from both numerator and denominator. `reason={access_denied,archived,fork,unknown}`. |
| `repos_parked_total` | CounterVec | `org`, `installation_id`, `reason` | Park *events* (INV-0015), as opposed to the standing population above. |
| `posture_export_total` | CounterVec | `outcome` | Export ticks. `outcome={ok,error}`. The liveness signal for every gauge in this section. |
| `posture_export_duration_seconds` | Histogram | — | Wall-clock per export, buckets to 60s. Observed on the failure path too, so a store read that times out is measured rather than silently dropped. |
| `installation_info` | GaugeVec | `installation_id`, `org` | Constant 1. A join label only — it carries no measurement, it exists so `installation_id`-keyed series can be grouped by org. |
| `property_schema_missing` | GaugeVec | `org`, `property` | 1 when an org's custom-property schema does not define a managed property, 0 when it does. Written at each schema-cache refresh; a failed fetch leaves the last known value rather than clearing it. |

Two things to know before building panels on these.

**`posture_export_total{outcome="ok"}` is the only heartbeat.** The
gauges keep serving their last successful values indefinitely, so a
leader whose store reads all fail looks exactly like a fleet that is
stable. Alert on the absence of `ok` increments, not on the gauges.

**The per-rule ratio understates non-compliance for a scoped rule.**
`repos_tracked{org}` counts every repo with posture, while
`repos_actionable{rule_name}` counts only repos the rule applies to. A
rule scoped to 10 of 100 repos with 5 failing reads as 5% rather than
50%. Use the org-wide denominator for fleet health and an absolute
count when a single scoped rule is the subject.

There is deliberately no "repos failing at least one rule" series, and
it cannot be derived from the ones here: the per-rule counts overlap by
an unknown amount, so summing them over-counts and taking the max
under-counts. If a panel needs that number it needs a new aggregate.

Example:

```promql
# Per-rule non-compliance within an org. Read with the scoping caveat
# above — the denominator is every measurable repo, not the subset the
# rule applies to.
max by (rule_name, org) (repo_guardian_repos_actionable)
  / on (org) group_left max by (org) (repo_guardian_repos_tracked)

# Worst rules fleet-wide
topk(10, sum by (rule_name) (max by (rule_name, org) (repo_guardian_repos_actionable)))

# How much of the fleet we cannot see, and why
sum by (reason) (max by (org, reason) (repo_guardian_repos_unmeasurable))

# Park events vs standing parks — these disagreeing is a real signal
# (parks that never un-parked, or un-parks nobody counted).
sum by (reason) (increase(repo_guardian_repos_parked_total[24h]))

# Exporter stalled: gauges are still being served but nothing updates
# them. This is the alert condition, not any gauge going flat.
absent(rate(repo_guardian_posture_export_total{outcome="ok"}[10m]) > 0)

# Attach an org to any installation_id-keyed series
sum by (org) (
  rate(repo_guardian_queue_delayed_total[1h])
    * on (installation_id) group_left(org) repo_guardian_installation_info)
```

### BudgetTracker (IMPL-0015 Phase 1)

> **Inert in production (INV-0012).** Nothing outside tests calls
> `Tracker.RefreshFromAPI`, so no snapshot is ever cached: every gate
> falls open, all six metrics below stay unpublished (or zero), and
> `enqueue_gated_by_budget_total` can never increment. Do not build
> paging alerts on these — an empty budget dashboard means "not
> wired", not "healthy". DESIGN-0021 replaces this mechanism with
> measured queue-delay metrics; the tables below document the intended
> semantics until then.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `api_budget_remaining` | Gauge | `installation_id` | Cached rate-limit budget remaining after local `Decrement`. Tighter than `rate_limit_remaining`. |
| `api_budget_spendable` | Gauge | `installation_id` | Additional enqueues the tracker will allow without breaching reserve. |
| `api_budget_reserve_fraction` | Gauge | `installation_id` | Operator-configured reserve floor (chart `discovery.reserveFraction`). |
| `api_budget_utilisation` | Gauge | `installation_id` | `1 - (remaining / limit)`. |
| `api_budget_refresh_total` | CounterVec | `installation_id`, `outcome` | BudgetTracker refresh attempts. `outcome={ok,error}`. |
| `enqueue_gated_by_budget_total` | CounterVec | `installation_id` | Enqueues blocked by the BudgetTracker reserve gate. Shared between StaleSweeper and Discoverer. |

Example:

```promql
# Operator alarm: deployment is rate-limit-bound
sum(rate(repo_guardian_enqueue_gated_by_budget_total[15m])) > 0

# Spendable budget approaching zero (gate about to close)
min by (installation_id) (repo_guardian_api_budget_spendable)

# Refresh failures (gate falls open without a cached snapshot)
sum by (installation_id) (
  rate(repo_guardian_api_budget_refresh_total{outcome="error"}[15m]))
```

### OpenTelemetry semconv series (IMPL-0023 Phase 3)

Four transport boundaries — inbound HTTP, the GitHub client, Valkey and
Postgres — are instrumented with off-the-shelf OTel libraries and
exported through a Prometheus bridge into this same endpoint. Those
series are **not** `repo_guardian_`-prefixed: they are `http_server_*`,
`http_client_*`, `db_client_*`, `redis_*` and `pgxpool_*`.

The catalog, the cardinality decisions, and the one-source-per-panel
dedup rule live in `docs/operations/scaling.md` §OpenTelemetry series
rather than being repeated here, so there is one copy to keep true.

Two things to know before writing a query against them:

- **Every panel picks one source.** Domain metrics stay authoritative
  for domain questions — `store_query_seconds{op}` knows `stale_repos`
  from `upsert_if_missing`, while the semconv database metrics see only
  SQL verbs. Semconv is authoritative for transport questions the
  domain metrics cannot see, like pool-acquire wait versus execution
  time. No panel mixes the two for the same signal.
- **`http_client_request_duration_seconds_count` is not "GitHub API
  calls attempted."** go-github's own client-side pre-check
  short-circuits above the transport once its header cache sees
  `remaining=0`, so those calls are never measured — the under-count
  happens exactly when the system is rate-limited. Use
  `queue_delayed_total{reason="rate_limit"}` for throttle volume.

## Common Queries

### Per-org activity

```promql
# Total work per org (last 24h)
sum by (org) (increase(repo_guardian_repos_checked_total[24h]))

# Compliance gap per org (rules detecting missing files)
sum by (org, rule_name) (rate(repo_guardian_files_missing_total[5m]))

# Out-of-scope volume by level
sum by (level) (rate(repo_guardian_out_of_scope_total[5m]))
```

### Error budget

```promql
# Error rate as a fraction of total checks (global)
sum(rate(repo_guardian_errors_total[15m]))
  /
clamp_min(sum(rate(repo_guardian_repos_checked_total[15m])), 1)

# Per-org error rate
sum by (org) (rate(repo_guardian_errors_total[15m]))
  /
clamp_min(sum by (org) (rate(repo_guardian_repos_checked_total[15m])), 1)
```

## Migration from pre-DESIGN-0010

Several metrics gained an `org` label in DESIGN-0010 (IMPL-0009).
Two were promoted from `Counter` to `CounterVec`:

| Metric | Before | After |
|--------|--------|-------|
| `prs_created_total` | `Counter` | `CounterVec[org]` |
| `prs_updated_total` | `Counter` | `CounterVec[org]` |
| `repos_checked_total` | `CounterVec[trigger]` | `CounterVec[trigger, org]` |
| `files_missing_total` | `CounterVec[rule_name]` | `CounterVec[rule_name, org]` |
| `settings_*_total` | `CounterVec[rule_name]` | `CounterVec[rule_name, org]` |
| `branch_protection_*_total` | `CounterVec[rule_name]` | `CounterVec[rule_name, org]` |
| `ignored_total` | `CounterVec[scope]` | `CounterVec[scope, org]` |
| `errors_total` | `CounterVec[operation]` | `CounterVec[operation, org]` |

To preserve the prior scalar query semantics for promoted metrics
that used to be unlabeled counters:

```promql
# Before
rate(repo_guardian_prs_created_total[5m])

# After (preserves scalar)
sum(rate(repo_guardian_prs_created_total[5m]))
```

To preserve the prior aggregation for relabeled counters:

```promql
# Before
sum by (rule_name) (rate(repo_guardian_files_missing_total[5m]))

# After (still works — Prometheus drops the new org label)
sum by (rule_name) (rate(repo_guardian_files_missing_total[5m]))

# Or explicitly aggregate away org
sum without (org) (rate(repo_guardian_files_missing_total[5m]))
```

## Importing the dashboards

```bash
# By hand
curl -X POST -H "Content-Type: application/json" \
  -d @contrib/generated/dashboards/repo-guardian-kpi.json \
  https://grafana.example.com/api/dashboards/db
```

The generated dashboards carry **concrete datasource UIDs**, not a
`${DS_PROMETHEUS}` input placeholder. A dashboard with an input prompts
on every import, which makes it un-provisionable — grafana-operator
applies a CR and nobody is there to answer the prompt. Point them at
your datasources at generation time instead:

```bash
repo-guardian monitoring generate --config guardian.hcl \
  --prometheus-uid my-prom --loki-uid my-loki
```

For grafana-operator, generate `GrafanaDashboard` CRs directly. The
instance selector is required — it is how the operator knows which
Grafana to file the dashboards into:

```bash
repo-guardian monitoring generate --config guardian.hcl \
  --format k8s --namespace monitoring \
  --instance-selector dashboards=grafana
```

Per-org rows on E2 are **declared from your config** when it has a
top-level `scope { orgs = [...] }` block, and discovered from a
template variable when it does not. Declared is better: a declared row
that renders empty says "this org has stopped reporting", where a
discovered row simply disappears and the dashboard looks exactly as
healthy as before. Use `--org` to declare rows for a config that has no
scope block.

## Applying the alerts

`generated/alerts/rules.yaml` is a plain `groups:` document — drop it
into your `rule_files`, or wrap it in a `PrometheusRule`. If you deploy
the Helm chart, it already ships an equivalent `PrometheusRule`; do not
apply both.

For the Loki examples, see the header of `loki/rules.yaml` — they need
a ruler with remote-write configured, and their expressions are LogQL
rather than PromQL.
