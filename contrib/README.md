# contrib/

Contributed assets for operating repo-guardian in production.

These files are reference starting points, not normative
configuration. Adjust thresholds, panel layouts, and selectors
to match your environment.

## Contents

| Path | Purpose |
|------|---------|
| `prometheus/alerts.yaml` | Alerting rules for availability, error rate, GitHub rate limiting, latency, webhooks, and strict-mode scope misconfigurations. |
| `grafana/repo-guardian-dashboard.json` | Grafana dashboard with overview, repo checks, compliance, webhook, custom-properties, error/rate-limit panels, plus per-org activity panels. |

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
| `prs_created_total` | CounterVec | `org` | Pull requests created by repo-guardian for missing files. |
| `prs_updated_total` | CounterVec | `org` | Existing PRs updated with new files. |

Example:

```promql
# PR creation rate per org
sum by (org) (rate(repo_guardian_prs_created_total[5m]))

# Top 10 rules by missing-file detections
topk(10, sum by (rule_name) (rate(repo_guardian_files_missing_total[1h])))
```

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
| `github_rate_remaining` | Gauge | — | Remaining GitHub API quota. |
| `github_rate_limit_waits_total` | CounterVec | `reason` | Total rate-limit waits by reason. |
| `github_rate_limit_wait_seconds` | Histogram | — | Duration of rate-limit waits. |

### Custom properties

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `properties_checked_total` | Counter | — | Repositories where custom properties were evaluated. |
| `properties_prs_created_total` | Counter | — | PRs created for custom properties. |
| `properties_set_total` | Counter | — | Repositories where properties were set via API. |
| `properties_already_correct_total` | Counter | — | Repositories where properties already matched. |

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

## Importing the dashboard

```bash
# Grafana CLI
grafana-cli admin import-dashboard contrib/grafana/repo-guardian-dashboard.json

# Or via API
curl -X POST -H "Content-Type: application/json" \
  -d @contrib/grafana/repo-guardian-dashboard.json \
  https://grafana.example.com/api/dashboards/db
```

The dashboard expects a Prometheus datasource named via the
`DS_PROMETHEUS` template variable; pick yours during import.
The dashboard defines an `org` template variable populated from
`label_values(repo_guardian_repos_checked_total, org)` so panels
in the "Per-org Activity" section can be filtered.

## Applying the alerts

If using the Prometheus Operator, wrap `prometheus/alerts.yaml`
in a `PrometheusRule` resource. See the file's preamble for an
example. Otherwise, drop the `groups:` content into your existing
`rule_files`.
