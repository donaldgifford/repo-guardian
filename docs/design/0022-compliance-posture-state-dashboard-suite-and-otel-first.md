---
id: DESIGN-0022
title: "Compliance posture state, dashboard suite, and OTEL-first observability"
status: Draft
author: Donald Gifford
created: 2026-08-02
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0022: Compliance posture state, dashboard suite, and OTEL-first observability

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-08-02

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [Per-rule posture state (Finding C, shape C1)](#per-rule-posture-state-finding-c-shape-c1)
  - [Leader-scoped posture exporter (Finding D, shape D1)](#leader-scoped-posture-exporter-finding-d-shape-d1)
  - [The org↔installation join metric (Finding E2)](#the-orginstallation-join-metric-finding-e2)
  - [OTEL metrics-first instrumentation (Finding G)](#otel-metrics-first-instrumentation-finding-g)
  - [Compliance snapshots and the per-org report (Finding C rider)](#compliance-snapshots-and-the-per-org-report-finding-c-rider)
  - [Config-generated monitoring (Finding H, shape H1)](#config-generated-monitoring-finding-h-shape-h1)
  - [Dashboard suite (Finding E)](#dashboard-suite-finding-e)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Observability](#observability)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Per-rule posture state](#phase-1-per-rule-posture-state)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Leader-scoped posture exporter and join metrics](#phase-2-leader-scoped-posture-exporter-and-join-metrics)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: OTEL metrics-first](#phase-3-otel-metrics-first)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: Compliance snapshots and the report CLI](#phase-4-compliance-snapshots-and-the-report-cli)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: Monitoring generator foundation](#phase-5-monitoring-generator-foundation)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: Dashboard suite content](#phase-6-dashboard-suite-content)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
  - [Phase 7: Cleanup, deprecations, docs, chart](#phase-7-cleanup-deprecations-docs-chart)
    - [Tasks](#tasks-6)
    - [Success Criteria](#success-criteria-6)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Risks](#risks)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

This design implements the full
[INV-0013](../investigation/0013-state-vs-event-metrics-dashboard-suite-and-system-observability.md)
recommendation as one effort. INV-0013 proposed three follow-ups
(posture-state persistence, a dashboard suite with a config-driven
generator, and metrics-first OTEL adoption); they are designed together
because they share every load-bearing surface: the posture exporter
feeds the KPI dashboard, the Finding I taxonomy decides every panel's
source, the generator and the report CLI load the same policy config,
and OTEL cancels the hand-rolled collectors the system dashboard would
otherwise need.

Three things ship. **Persisted per-rule check outcomes** — the facts
the engine computes today and throws away — in a new `rule_state`
table, projected to Prometheus by a leader-scoped exporter so
"how many repos are missing CODEOWNERS *right now*" finally has an
honest answer. **OTEL instrumentation at the four transport
boundaries** (inbound HTTP, GitHub client, Valkey, Postgres), exported
through the existing `/metrics` endpoint via the OTel prometheus
bridge — zero new infrastructure. **A four-dashboard suite generated
from `guardian.hcl`** by a new `repo-guardian monitoring generate`
subcommand, so a configured-but-silent org renders as a no-data
panel instead of a blind spot, plus a `repo-guardian report` sibling
that turns the same posture tables into per-org owner reports.

## Goals and Non-Goals

### Goals

- Posture questions ("how many repos fail rule X, per org, now") are
  answerable from a gauge and from SQL — not inferred from event
  counters that a restart zeroes and a fixed repo never decrements.
- Exactly one replica runs posture queries, at one cadence,
  independent of scrape rate and replica count (the 1+N fan-out from
  INV-0013 Finding D never materialises).
- Compliance history survives Prometheus retention
  (`compliance_snapshot`), and a per-org report with repo identity and
  missing-since durations can be generated from it.
- USE/RED coverage for the service's own hops — inbound HTTP
  (including the currently-uncounted HMAC 401s), GitHub client,
  Valkey, Postgres — with no collector, no Tempo, no new cluster
  infra.
- Dashboards and alerts are generated from the operator's actual
  config: a row exists for every org that *should* report, panels
  exist only for mechanisms in use, and the generator doubles as a CI
  config check.
- One source of truth for dashboards: the static `contrib/` tier is
  the generator's output for a default config, not a hand-maintained
  fork.

### Non-Goals

- **Tracing infrastructure.** No collector, no Tempo, no OTLP
  exporter configuration. The instrumentation added here emits spans
  to a no-op TracerProvider; turning them on is a later, purely
  additive design (INV-0013 effort 3's second half, deliberately
  decoupled).
- **`repo` as a metric label.** Never, at any cardinality. Repo
  identity lives in Postgres (this design) and in Loki fields
  (Finding E4), not in Prometheus series.
- **Changing DESIGN-0021's queue observability.** The delayed-requeue
  metrics are complementary and land there; this design is written
  against the post-0021 metric surface (budget series and the
  rate-limit wait pair treated as gone).
- **Loki as a posture source.** Rejected as primary in Finding C4 —
  weekly sweep cadence forces 7d+ log windows. Loki-derived metrics
  stay scoped to log-shaped event signals in E4.
- **Real-time posture.** The exporter reflects the last completed
  check per repo and refreshes on its own cadence; sub-minute
  freshness is not a requirement.
- **Grafana Enterprise reporting.** The report path is a CLI
  rendering markdown; scheduled PDF delivery is Enterprise-licensed
  and out of scope.
- **Per-org report *delivery*** beyond writing files (Open
  Question 6 picks the shape; automation of sending is follow-up).

## Background

INV-0013 established, verified against code:

- **Eight metrics are posture masquerading as events** (Finding B).
  `files_missing_total` and friends count check-events; a repo fixed
  yesterday still counts in `increase(...[7d])`, a repo checked twice
  counts twice, and a restart just makes the wrongness visible. The
  one in-tree metric doing posture correctly is `open_prs_by_rule` —
  a sweep-snapshot gauge.
- **Postgres cannot serve posture today** (Finding C). The worker
  write-back persists `{status, error, policy_version}` only;
  `findActionableRules`' per-rule outcomes are computed, used, and
  discarded. The downstream report requirement (repo identity +
  missing-since durations) settles the storage shape as a per-rule
  table (C1), not a JSONB blob (C2).
- **The 1+N scrape fan-out dissolves with leader-scoped export**
  (Finding D1). The Valkey scheduler's SETNX election already
  designates one replica; a leader-gated `Schedule` handler queries
  at its own cadence and `Set()`s gauges. No Valkey query cache
  needed unless a second consumer appears (D3).
- **The system-level gaps are enumerable** (Finding F) and — after
  Finding G — mostly *cancelled* rather than built: `otelhttp` (both
  directions), `redisotel`, and `otelpgx` replace the planned
  hand-rolled GitHub RoundTripper histogram, webhook duration
  middleware, and go-redis collector. The OTel prometheus bridge
  exports through the existing endpoint, so metrics-first OTEL costs
  zero new infra.
- **Config-generated monitoring is an established pattern**
  (Finding H: mixins, Sloth/Pyrra, beats `setup --dashboards`), with
  an unusually strong fit: `label_values(...)` template variables can
  only show orgs that have emitted data, and "configured but silent"
  was exactly the enterprise-migration failure mode. The
  grafana-foundation-sdk provides official, typed Go builders.
- **Finding I's taxonomy** (business / service / infra) gives each
  dashboard a closed source list: E1 KPI = business, E2 detailed =
  business + service sliced by org, E3 = service + infra, E4 = Loki
  evidence.

**Relationship to DESIGN-0021.** The delayed-requeue design removes
the seven dead budget series and (per its INV-0013-driven amendment)
retires `github_rate_limit_waits_total` / `_wait_seconds` in favour of
its own `queue_delayed_*` metrics. Nothing in this design references
any of those series; the E3 dashboard consumes 0021's queue metrics as
planned service-tier signals. The two designs share one physical
surface — the installation transport — where 0021's throttle transport
must sit *inside* this design's `otelhttp` client wrapper (ordering
specified in Phase 3).

## Detailed Design

### Per-rule posture state (Finding C, shape C1)

A new `rule_state` table records the outcome of every rule evaluation,
written in the same worker write-back path that already persists
`repo_state` — the actionable set is in memory at exactly that point.

One row per `(installation, owner, repo, rule)`. The row carries
`actionable` (is the rule currently failing for this repo),
`actionable_since` (set on the false→true transition, cleared on
true→false — this is the "missing since 2026-06-14" the report needs),
`rule_kind` (`file` | `setting` | `branch_protection`), and
`policy_version`. Setting rules and branch-protection rules are
included from day one — BP currently has *no* mismatch metric at all
(Finding B), so this closes that hole rather than porting it.

The catalog-parseability fact (Finding B's `catalog_parse_failed_total`
posture reading) is a per-repo, not per-rule, fact: a nullable
`catalog_parse_ok` column on `repo_state` (null = no catalog rule
evaluated / unknown), set from the reconciler outcome.

Write-back semantics are unchanged in spirit: **best-effort, never
propagated**. A `rule_state` write failure logs Warn, counts
`store_writeback_total{outcome="error"}`, and does not fail the job —
the queue job remains the source of truth for "did we do the work"
(IMPL-0015 Phase 0 contract). The upsert is one batched statement per
job, executed in a single transaction that also reconciles rows for
rules no longer in the evaluated set (Open Question 3).

Posture is then one indexed query:

```sql
SELECT owner AS org, rule_name,
       count(*) FILTER (WHERE actionable) AS actionable,
       count(*)                            AS tracked
FROM rule_state
GROUP BY 1, 2;
```

### Leader-scoped posture exporter (Finding D, shape D1)

A new leader-gated schedule handler, `posture-export`, registered via
the existing `Scheduler.Schedule(ctx, name, interval, handler)`
surface — the same SETNX election that already serialises
`stale-sweep`. Every `POSTURE_EXPORT_INTERVAL` (default 60s) the
leader runs the posture query and populates two GaugeVecs:

- `repo_guardian_repos_actionable{rule_name, org}`
- `repo_guardian_repos_tracked{org}`

The handler follows the `ResetOpenPRsByRule` precedent: full GaugeVec
`Reset()` at the top of each tick, then `Set()` — so an org or rule
that disappears from the fleet stops emitting stale series instead of
freezing at its last value. Non-leader replicas never serve the
series; dashboards aggregate with `max by (...)`, exactly as
`scheduler_is_leader` panels already do. Compliance ratio is computed
in PromQL from the two gauges (Open Question 2).

The GitHub-owned posture reading from Finding B — org schema gaps —
gets its projection at the source that already knows: the
custom-properties schema-preflight cache (30-minute TTL, IMPL-0017)
sets `repo_guardian_property_schema_missing{org, property}` to 0/1 on
each refresh. No DB involvement; the cache refresh *is* the check.

### The org↔installation join metric (Finding E2)

`repo_guardian_installation_info{installation_id, org} 1` — a
constant-value info gauge set when an installation client is
constructed and during discovery. It exists solely so Grafana can
`group_left` the `installation_id`-keyed series (discovery,
rate-limit) into per-org dashboard rows. One line of instrumentation,
unblocks the E2 layout.

### OTEL metrics-first instrumentation (Finding G)

Four boundaries, four off-the-shelf instrumentation packages, one
exporter:

| Boundary | Package | Replaces (planned, never built) |
|---|---|---|
| Inbound HTTP (webhook server) | `otelhttp` server middleware | hand-rolled `promhttp.InstrumentHandler*` duration middleware |
| GitHub client | `otelhttp` client transport | hand-rolled RoundTripper RED histogram |
| Valkey (queue + scheduler clients) | `redisotel` metrics | `redisprometheus.NewCollector` |
| Postgres (pgxpool) | `otelpgx` | possibly the `pgxpool.Stat()` collector — verified in task 3.5, one source only |

The OTel SDK's `go.opentelemetry.io/otel/exporters/prometheus` bridge
registers into the same default registry `promhttp` already serves
(main.go:413), so every semconv series appears on the existing
`/metrics` endpoint with no collector and no config change for the
scrape stack. The TracerProvider is the no-op provider — spans are
structurally emitted but go nowhere until the later tracing design
turns them on.

What this closes, from the Finding G unmask list: HMAC 401s (counted
nowhere today — `webhook_rejected_total` lives only in the allowlist
middleware) become ordinary `http.server.request.duration{status}`
samples; GitHub status truth (429 vs 403 vs 5xx) appears on the client
histogram; Valkey command latency (including the reaper Lua and leader
SETNX) is measured for the first time; pool-acquire wait separates
from query time.

Instrumentation scope decision: the **webhook server only** for
inbound. The metrics/health server would mostly instrument Prometheus
scraping itself — noise, not signal.

Transport ordering with DESIGN-0021 (both designs touch the
installation transport):

```text
otelhttp client transport        ← this design, outermost
  └─ rate-limit transport        ← DESIGN-0021 (ThrottledError source)
       └─ ghinstallation token transport
```

Outermost `otelhttp` means a 0021 deferral shows up as an errored
client span/measurement — which is the residual signal that replaces
the retiring `github_rate_limit_wait_*` pair.

**Dedup rule (Finding G):** domain metrics stay authoritative for
domain questions (`store_query_seconds{op}` knows `stale_repos`;
semconv sees only SQL verbs). Every dashboard panel picks exactly one
source; no panel may mix a hand-rolled and a semconv series for the
same signal.

### Compliance snapshots and the per-org report (Finding C rider)

A second leader-gated schedule handler, `compliance-snapshot`
(default cadence 24h), runs
`INSERT INTO compliance_snapshot ... SELECT` from the posture query —
giving quarter-over-quarter history independent of Prometheus
retention. Volume is trivial: orgs × rules rows per day (~120/day at
target scale); no retention machinery needed initially.

`repo-guardian report` is a CLI sibling of the generator: loads the
same `guardian.hcl`, queries `rule_state` + `compliance_snapshot`, and
renders one report per org — compliance percentage per rule, trend
versus the previous snapshot, and the table a metric can never hold:
*which* repos, failing *which* rules, since *when*. Open-PR links are
fetched live from GitHub at generation time (low volume, installation
client). Output shape is Open Question 6.

### Config-generated monitoring (Finding H, shape H1)

`repo-guardian monitoring generate --config guardian.hcl --out
./monitoring/ --format json|k8s`.

The generator `policy.Load()`s the operator's config — which makes
every invocation a free config validation (the strict-templates
precedent, extended) — and derives the generation model: the org list
from `scope`, the enabled rules and their kinds, which reconcilers are
attached, which mechanisms (custom properties, absent rules, gates)
are in use. From that model it emits:

- the four dashboards (Finding E), with one E2 row *per configured
  org* — a silent org renders as a no-data row, a signal instead of a
  blind spot;
- a PrometheusRule manifest containing only alerts whose mechanism is
  configured (no `PropertySchemaMissing` without a `custom_properties`
  reconciler), each authored against the INV-0012 C/E window rules;
- panels only for enabled rule kinds (no forbidden-files panels
  without absent-mode rules).

Dashboards are authored once, in Go, against the
grafana-foundation-sdk builders — there is **no hand-maintained
dashboard JSON anywhere**. The static `contrib/` tier is the
generator's output for a default (empty-scope) config, regenerated in
CI with a fail-on-diff gate — the same drift convention `helm-docs`
and `docz update` already use in this repo. The SDK's coupling to
Grafana schema versions is handled by pinning and a render test
(Grafana ≥ 10 required; acceptable — the homelab runs
kube-prometheus-stack).

Format `k8s` wraps the dashboard JSON for in-cluster consumption
(Open Question 7 picks ConfigMap-sidecar vs grafana-operator CRs) and
emits the PrometheusRule as a CR. Format `json` emits plain files for
manual import.

Where the generator lives (main binary vs a separate `cmd/`) is Open
Question 5.

### Dashboard suite (Finding E)

Four dashboards, sources fixed by the Finding I taxonomy:

- **E1 — KPI / status** (business tier only): compliance % per rule
  and org (the two Phase 2 gauges), `open_prs_by_rule` ages,
  convergence rate (`prs_closed_total{reason="satisfied"}`), error
  budget, rate-limit headroom, queue depth, leader status. The
  compliance panels consume *real posture*, never
  `increase(files_missing_total)`.
- **E2 — detailed, per-org repeated rows** (business + service sliced
  by org): a fleet section of aggregates (`sum without (org)`), then a
  generated row per configured org. `installation_id`-keyed series
  join in via `installation_info` `group_left`. Queue / store /
  scheduler metrics are deliberately absent here — they are
  fleet-scoped and belong to E3.
- **E3 — system / health** (service + infra): go runtime + process,
  OTEL semconv HTTP server/client, `redisotel`, pgx pool + query
  latency, queue USE (including DESIGN-0021's `queue_delayed_depth` /
  `queue_wait_seconds` once landed), the three orphan histograms from
  Finding A (`scheduler_sweep_batch_size`,
  `store_writeback_duration_seconds`, `discovery_duration_seconds`)
  finally get panels, posture-exporter health.
- **E4 — Loki** (evidence tier): error rate by component, sweep
  summaries, write-back failures, webhook rejections, top erroring
  repos (repo identity as a log field, where it belongs). The
  catalog-parse log line gets test-locked before any panel depends on
  it; Loki-ruler recording-rule examples ship in `contrib/` for the
  log-shaped signals.

## API / Interface Changes

```go
// internal/checker — CheckRepo returns per-rule outcomes.
// Error paths return (nil, err); the worker then writes status-only,
// exactly as today.
type RuleOutcome struct {
    RuleName   string
    Kind       string // "file" | "setting" | "branch_protection"
    Actionable bool
}

type CheckResult struct {
    Outcomes       []RuleOutcome
    CatalogParseOK *bool // nil = no catalog rule evaluated
}

func (e *Engine) CheckRepo(ctx context.Context, client ghclient.Client,
    owner, repo string) (*CheckResult, error)

// internal/store — Store gains posture methods (final set is an impl
// detail; the contract is batched-single-tx upsert + the two query
// shapes the exporter and report need).
type RuleState struct {
    InstallationID  int64
    Owner, Repo     string
    RuleName, Kind  string
    Actionable      bool
    ActionableSince *time.Time
    PolicyVersion   string
}

type Store interface {
    // ...existing five methods...
    UpsertRuleStates(ctx context.Context, states []RuleState) error
    ActionableCounts(ctx context.Context) ([]RuleCount, error)
    ActionableRepos(ctx context.Context, org string) ([]RuleState, error)
    InsertComplianceSnapshot(ctx context.Context, at time.Time) error
}
```

New config (env → chart values → `values.schema.json`, per
convention): `POSTURE_EXPORT_INTERVAL` (default `60s`),
`COMPLIANCE_SNAPSHOT_INTERVAL` (default `24h`). OTEL is on by default
with no new knobs; the standard `OTEL_SDK_DISABLED=true` escape hatch
is honoured.

New CLI surface: `repo-guardian monitoring generate` and
`repo-guardian report` subcommands (placement per Open Question 5).

Mock/fake fallout: `make mocks` regenerates the Store mock; the
test-local `recordingQueue` / `fakeStore` recorders and the
checker-package fakes absorb the `CheckRepo` signature change
(embedded `mocks.MockClient` is unaffected — this is an engine-side
change, not a `github.Client` change).

## Data Model

Migration `0002` (up + down), additive only:

```sql
CREATE TABLE rule_state (
    installation_id  BIGINT      NOT NULL,
    owner            TEXT        NOT NULL,
    repo             TEXT        NOT NULL,
    rule_name        TEXT        NOT NULL,
    rule_kind        TEXT        NOT NULL,
    actionable       BOOLEAN     NOT NULL,
    actionable_since TIMESTAMPTZ,
    policy_version   TEXT        NOT NULL DEFAULT '',
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (installation_id, owner, repo, rule_name)
);

-- posture query support: count actionable per (owner, rule)
CREATE INDEX idx_rule_state_actionable
    ON rule_state (owner, rule_name) WHERE actionable;

CREATE TABLE compliance_snapshot (
    org              TEXT        NOT NULL,
    rule_name        TEXT        NOT NULL,
    actionable_count INT         NOT NULL,
    tracked_count    INT         NOT NULL,
    snapshot_at      TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (org, rule_name, snapshot_at)
);

ALTER TABLE repo_state ADD COLUMN catalog_parse_ok BOOLEAN;
```

The upsert preserves `actionable_since` across still-actionable
checks and manages the transition edges in SQL:

```sql
INSERT INTO rule_state (...) VALUES (...)
ON CONFLICT (installation_id, owner, repo, rule_name) DO UPDATE SET
    actionable = EXCLUDED.actionable,
    actionable_since = CASE
        WHEN NOT rule_state.actionable AND EXCLUDED.actionable THEN now()
        WHEN NOT EXCLUDED.actionable                            THEN NULL
        ELSE rule_state.actionable_since
    END,
    rule_kind      = EXCLUDED.rule_kind,
    policy_version = EXCLUDED.policy_version,
    updated_at     = now();
```

Row volume at target scale: 5k repos × ~6 rules = ~30k rows, upserted
one batch per check. Dead-installation cleanup follows the existing
`repo_state` operational recipe (ent-setup.md) — same key prefix, one
more `DELETE ... WHERE installation_id =` at cutover.

## Observability

New metrics (all on the existing `/metrics` endpoint):

| Metric | Type | Labels | Answers |
|---|---|---|---|
| `repo_guardian_repos_actionable` | Gauge | `rule_name`, `org` | posture: repos currently failing each rule |
| `repo_guardian_repos_tracked` | Gauge | `org` | denominator for compliance % |
| `repo_guardian_installation_info` | Gauge (=1) | `installation_id`, `org` | Grafana `group_left` join |
| `repo_guardian_property_schema_missing` | Gauge (0/1) | `org`, `property` | schema gap, as posture |
| `repo_guardian_posture_export_total` | Counter | `outcome` | is the exporter running |
| `repo_guardian_posture_export_duration_seconds` | Histogram | — | exporter query cost |
| semconv HTTP server/client, redis, pgx series | (bridge) | per semconv | Finding F's USE/RED gaps |

Removed (Phase 7, per Open Question 4): the four legacy unlabeled
`properties_*` counters, superseded by real posture.

One new starter alert: `RepoGuardianPostureExportStalled` — no
successful `posture_export_total{outcome="success"}` increase over
several export intervals while a leader exists; the KPI dashboard is
lying if this fires. Authored under the INV-0012 C/E window rules like
everything else, and emitted by the generator.

## Implementation Phases

Each phase is independently mergeable, in dependency order:
2 requires 1; 4 requires 1; 6 requires 5 (and consumes 2–4's series);
7 is last. Run `make lint` and `make fmt` after each task; commit per
numbered task.

### Phase 1: Per-rule posture state

#### Tasks

- [ ] 1.1 Migration `0002`: `rule_state` + partial index,
      `compliance_snapshot`, `repo_state.catalog_parse_ok` (up and
      down).
- [ ] 1.2 Change `Engine.CheckRepo` to return `(*CheckResult, error)`
      with per-rule outcomes for file, setting, and branch-protection
      rules (BP gains an actionable verdict — its first posture
      signal) and the catalog-parse fact.
- [ ] 1.3 `Store.UpsertRuleStates`: single-transaction batched upsert
      with the `actionable_since` transition CASE, plus reconciliation
      of rows absent from the evaluated set (per Open Question 3
      resolution).
- [ ] 1.4 Worker write-back: persist the `CheckResult` in the existing
      `writeBack` path — best-effort, `store_writeback_total` outcome
      accounting, never propagated.
- [ ] 1.5 `make mocks`; update test-local `fakeStore` recorders and
      checker-test plumbing for the new `CheckRepo` signature.
- [ ] 1.6 Tests: `actionable_since` set on false→true, preserved on
      true→true, cleared on true→false; batched upsert race
      (concurrent-goroutine pattern from
      `TestPostgresStore_UpsertIfMissing_ConcurrentRace`); rule-state
      write failure does not fail the job.

#### Success Criteria

- After a sweep, the posture SQL returns counts matching the engine's
  in-memory actionable sets for every rule kind.
- A repo that flips from failing to satisfied has its row updated with
  `actionable=false, actionable_since=NULL` on the next check.
- Store write failures never change job outcomes.
- `make ci` passes; migration applies and rolls back cleanly.

### Phase 2: Leader-scoped posture exporter and join metrics

#### Tasks

- [ ] 2.1 `installation_info{installation_id, org} 1` set at
      installation-client construction and during discovery.
- [ ] 2.2 `posture-export` schedule handler: leader-gated via the
      existing `Scheduler.Schedule`; full `Reset()` + `Set()` of
      `repos_actionable` and `repos_tracked` each tick
      (`ResetOpenPRsByRule` precedent).
- [ ] 2.3 `property_schema_missing{org, property}` 0/1 gauge set at
      each schema-preflight cache refresh.
- [ ] 2.4 Exporter health: `posture_export_total{outcome}` +
      `posture_export_duration_seconds`.
- [ ] 2.5 Config: `POSTURE_EXPORT_INTERVAL` env +
      `values.yaml` / `values.schema.json` + helm-unittest cases.
- [ ] 2.6 Tests: only the leader emits (two schedulers, one series
      source); a removed org/rule stops emitting after the next tick;
      gauge values equal SQL truth.
- [ ] 2.7 `contrib/README.md` rows for all Phase 2 metrics.

#### Success Criteria

- With N replicas, exactly one serves the posture series;
  `max by (rule_name, org)` is correct and stable across leader
  failover.
- No stale series survive an org or rule leaving the fleet.
- `make ci` and helm-unittest pass.

### Phase 3: OTEL metrics-first

#### Tasks

- [ ] 3.1 OTel SDK + `exporters/prometheus` bridge registered into
      the default registry; no-op TracerProvider; honour
      `OTEL_SDK_DISABLED`.
- [ ] 3.2 `otelhttp` server middleware on the webhook server mux
      (only) — HMAC 401s and every other status become measured.
- [ ] 3.3 `otelhttp` client transport wrapping the installation
      transport, **outermost** (above the rate-limit transport —
      DESIGN-0021 ordering contract).
- [ ] 3.4 `redisotel` metrics on both Valkey clients (queue,
      scheduler).
- [ ] 3.5 `otelpgx` on the pgxpool; verify whether its pool-stats
      option covers `pgxpool.Stat()` — if yes use it, if no add the
      small Stat collector. Exactly one pool-stats source ships.
- [ ] 3.6 Cardinality audit of the bridge output (no `url.path` on
      server metrics; bounded label sets) + document the
      one-source-per-panel dedup rule in
      `docs/operations/scaling.md`.
- [ ] 3.7 Tests: `/metrics` contains the semconv series alongside the
      existing `repo_guardian_*` series in one registry; a webhook
      request with a bad HMAC increments the server duration
      histogram with a 401 label.

#### Success Criteria

- Inbound HTTP, GitHub client, Valkey, and Postgres all have RED/USE
  series on the existing endpoint with zero new cluster infra.
- The Finding F hand-rolled collector list is fully cancelled or
  consciously superseded (task 3.5's one-source decision recorded).
- `make ci` passes.

### Phase 4: Compliance snapshots and the report CLI

#### Tasks

- [ ] 4.1 `compliance-snapshot` schedule handler (leader-gated,
      `COMPLIANCE_SNAPSHOT_INTERVAL`, default 24h):
      `INSERT ... SELECT` from the posture query.
- [ ] 4.2 `repo-guardian report`: per-org output with compliance %
      per rule, trend vs previous snapshot, and the
      repo/rule/missing-since table; open-PR links fetched live at
      generation time.
- [ ] 4.3 Golden-file tests for report rendering; snapshot-trend test
      across two synthetic snapshots.
- [ ] 4.4 Operator doc: generating and distributing reports; snapshot
      cadence and (non-)retention rationale.

#### Success Criteria

- A generated report for a seeded org matches DB truth exactly
  (identity, since-dates, percentages).
- Snapshot rows accumulate at the configured cadence on the leader
  only.
- `make ci` passes.

### Phase 5: Monitoring generator foundation

#### Tasks

- [ ] 5.1 `monitoring generate` subcommand skeleton: `policy.Load` →
      generation model (orgs, rules, kinds, reconcilers, mechanisms);
      `--out`, `--format json|k8s`.
- [ ] 5.2 grafana-foundation-sdk dependency (pinned) + the shared
      panel-library package; render test against the pinned Grafana
      schema version.
- [ ] 5.3 PrometheusRule generation: existing starter alerts
      re-authored as generator output, mechanism-scoped, windows per
      INV-0012 findings C/E; includes `PostureExportStalled` and the
      DESIGN-0021 alerts once those metrics exist.
- [ ] 5.4 CI drift gate: regenerate the static tier from the default
      config, fail on diff (helm-docs convention).
- [ ] 5.5 Document generator-as-validation (a failing
      `policy.Load` fails generation — CI config check for free).

#### Success Criteria

- `monitoring generate` against `examples/guardian-enterprise.hcl`
  emits dashboards containing a row for every configured org
  (including orgs with no data) and an alert manifest containing only
  configured-mechanism alerts.
- Hand-editing a generated artifact turns CI red.
- `make ci` passes.

### Phase 6: Dashboard suite content

#### Tasks

- [ ] 6.1 E1 KPI dashboard — business-tier sources only (Finding I);
      compliance panels read the Phase 2 gauges.
- [ ] 6.2 E2 detailed dashboard — fleet aggregate section + generated
      per-org rows; `installation_info` `group_left` joins for
      `installation_id`-keyed series.
- [ ] 6.3 E3 system dashboard — service + infra tiers: OTEL semconv
      series, queue USE (incl. DESIGN-0021 series when present), pgx
      pool, go runtime, the three orphan histograms, posture-exporter
      health.
- [ ] 6.4 E4 Loki dashboard + Loki-ruler recording-rule examples in
      `contrib/`.
- [ ] 6.5 Test-lock the catalog-parse log line ("catalog-info parse
      failed; skipping reconcile to avoid clearing properties" —
      message + keys) before E4 references it.
- [ ] 6.6 Commit the generated static tier to `contrib/grafana/`;
      delete the legacy 61-panel dashboard with a pointer in
      `contrib/README.md`.
- [ ] 6.7 Rewrite `contrib/README.md` around the four-dashboard suite
      and the generated/static two-tier model.

#### Success Criteria

- All four dashboards render against a live stack with no
  empty-by-construction panels and no references to dead or retiring
  series (budget pair, rate-limit wait pair).
- Every panel's source respects the Finding I taxonomy and the
  one-source-per-panel rule.
- `make ci` passes; drift gate green on the committed static tier.

### Phase 7: Cleanup, deprecations, docs, chart

#### Tasks

- [ ] 7.1 Remove the four legacy `properties_*` counters (per Open
      Question 4 resolution) with migration notes in
      `contrib/README.md`.
- [ ] 7.2 `docs/operations/scaling.md`: posture architecture, exporter
      leader semantics, OTEL series catalog, dedup rule.
- [ ] 7.3 Chart: version + appVersion bump, new values documented via
      `README.md.gotmpl` (never the rendered README), prometheusrule
      parity with the generator output.
- [ ] 7.4 CLAUDE.md: posture-state contract (write-back best-effort,
      leader-scoped export, taxonomy pointer), transport-ordering
      contract shared with DESIGN-0021.
- [ ] 7.5 Flip INV-0013 to Concluded and this design per its
      lifecycle; `docz update design inv`; mkdocs stays at the
      14-warning baseline.

#### Success Criteria

- No dangling references to removed counters in code, chart, contrib,
  or docs.
- Chart renders and installs with the new values; helm-unittest
  passes.
- `make ci` passes.

## Testing Strategy

- **Unit** — upsert transition semantics (the `actionable_since`
  CASE), exporter reset-then-set behaviour, generation-model
  derivation from policy configs, report rendering (golden files).
  Table-driven throughout.
- **Integration (`integration` build tag, real Postgres)** — migration
  0002 up/down, batched upsert under concurrency, posture query
  correctness at realistic row counts, snapshot insert. Follows the
  existing store integration-test convention.
- **Leader semantics** — exporter emission is leader-gated; asserted
  with the two-scheduler pattern from the valkey scheduler
  integration tests.
- **Non-vacuity** — per standing practice, every behavioural test is
  verified by neutralising the fix and confirming failure. Task 2.6's
  stale-series test especially: it passes trivially if the exporter
  never `Reset()`s and the test never removes an org.
- **Mock fidelity** — `UpsertRuleStates` then `ActionableCounts` is a
  list-then-act shape; any fake must reflect prior writes (CLAUDE.md
  rule), or exporter tests are vacuous.
- **Render tests** — generator output parses as valid dashboard JSON
  for the pinned Grafana schema; a fixture config with zero
  mechanisms produces zero mechanism panels/alerts.

## Migration / Rollout Plan

1. **Phases 1–2 ship as one minor** (schema + exporter). Migration
   0002 is additive; old binaries ignore the new tables entirely, so
   rollback is binary-only.
2. **Phase 3 ships as a minor.** Purely additive series on the
   existing endpoint; scrape configs unchanged. Verify scrape size
   growth is acceptable (semconv adds ~a few hundred series per
   replica).
3. **Phases 4–6 ship in any order after their dependencies**; all
   additive. Dashboards land in `contrib/` and the GitOps repo at the
   operator's pace.
4. **Phase 7 ships last as a minor with a prominent note** — it
   removes the four legacy `properties_*` counters (the only
   scrape-visible removal in this design; anything scraping them gets
   the `contrib/README.md` migration recipe).
5. Coordination with DESIGN-0021: no ordering requirement except
   E3/alert content — panels referencing 0021's queue series are
   generated only when those metrics exist (the generator's
   mechanism-scoping handles this naturally).

## Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| `rule_state` write amplification slows checks | low | one batched statement per job in the existing write-back; best-effort so it can never fail a job; p99 tracked by `store_writeback_duration_seconds` (which finally gets a panel) |
| Posture staleness misread as truth | medium | dashboards label posture panels "as of last check per repo"; `updated_at` supports an explicit staleness panel; exporter-stalled alert |
| Exporter leader flaps → gauge gaps | low | same SETNX machinery as sweeps, already proven; `max by` across replicas; export health metrics |
| foundation-sdk / Grafana schema drift | medium | pin the SDK, render test in CI, regenerate on upgrade — the drift gate catches silent divergence |
| Semconv series bloat scrape size | low-medium | task 3.6 cardinality audit; bridge views can drop attributes if needed |
| Two-tier dashboards fork anyway | low | there is no hand-written JSON — static tier is generator output, enforced by the CI diff gate |
| Report GitHub calls at generation time hit limits | low | volume is per-open-PR per org, ad-hoc cadence; uses the standard installation client with existing accounting |

## Open Questions

1. **Posture storage shape.**
   (a) C1 — the `rule_state` per-rule table as specified. The report
   requirement (repo identity + missing-since) already settled this
   in INV-0013 Finding C; JSONB cannot express `actionable_since`
   per rule without contortions, and "show me the repos" was needed
   twice during the enterprise migration.
   (b) C2 — `actionable_rules JSONB` on `repo_state`: cheaper
   migration, no second table, but per-rule timestamps and indexed
   posture queries get materially worse.
   (c) C3 only — sweep-snapshot gauges, no schema change: posture
   lags a sweep, no identity, no report. Kills Phase 4.
   other:

2. **Compliance ratio computation.**
   (a) Export the two raw gauges (`repos_actionable`,
   `repos_tracked`) and compute the ratio in PromQL
   (`1 - actionable/tracked`) — raw series compose into any panel,
   recording rules can pre-bake the ratio, and the exporter stays
   dumb.
   (b) Export a precomputed `compliance_ratio{org, rule_name}` gauge
   as well — convenient, but a derived series that can silently
   disagree with its inputs.
   (c) Compute in Grafana via the Postgres datasource — no gauges at
   all for compliance; couples the KPI dashboard to DB credentials
   in Grafana.
   other:

3. **`rule_state` reconciliation when rules leave the policy.**
   (a) Reconcile in the write-back transaction: after the batched
   upsert, delete this repo's rows whose `rule_name` is not in the
   evaluated set. Exact, incremental, no background job, and the
   write-back already holds the full evaluated set. A policy that
   drops a rule converges repo-by-repo as checks occur.
   (b) A background GC pass comparing `rule_state` against the loaded
   policy — converges faster after a policy change, but adds a job
   and a second writer.
   (c) Query-time filtering only (`WHERE rule_name IN (...)`) — rows
   never deleted; table grows with every renamed rule forever.
   other:

4. **Legacy `properties_*` counters (4 unlabeled Counters).**
   (a) Remove in Phase 7, same minor that completes the suite —
   they are posture-shaped (Finding B), unlabeled (pre-IMPL-0009),
   and fully superseded by `repos_actionable` + the property-sync
   event counters; `contrib/README.md` carries the migration note.
   (b) Deprecate for one release (log a warning in docs, keep
   emitting), remove the next minor — gentler, but nothing external
   is known to consume them and it drags the cleanup across two
   releases.
   (c) Keep indefinitely — cost is small but they contradict the
   taxonomy and invite wrong panels.
   other:

5. **Generator and report binary placement.**
   (a) Subcommands on the main `repo-guardian` binary — one artifact,
   one version, the policy loader is already linked, and operators
   run it from the same image in CI. Measure the foundation-sdk size
   delta on the ~19.5MB distroless image; if it is egregious, fall
   back to (b) before merging.
   (b) A separate `cmd/repo-guardian-monitoring` binary sharing
   `internal/policy` — keeps the server image lean, but two
   artifacts to version and release.
   other:

6. **Report output shape.**
   (a) Markdown files per org written to `--out` — simplest, renders
   everywhere (GitHub, Slack paste, Backstage TechDocs), delivery
   stays a human/automation choice outside the binary.
   (b) Open a GitHub issue per org via the App — delivery built in
   and org owners are already on GitHub, but it writes to repos we
   otherwise only remediate, and needs an issue-target convention.
   (c) HTML + SMTP email — the classic compliance-report shape, but
   brings mail configuration and templating for marginal gain at
   current scale.
   other:

7. **`--format k8s` dashboard artifact shape.**
   (a) ConfigMaps with the `grafana_dashboard: "1"` sidecar label —
   the kube-prometheus-stack convention already running in the
   homelab; no new operator, kustomize-native.
   (b) grafana-operator `GrafanaDashboard` CRs — cleaner ownership
   model, but requires deploying grafana-operator.
   (c) Plain JSON only; the operator wraps it themselves — smallest
   generator surface, pushes boilerplate onto every consumer.
   other:

## References

- [INV-0013](../investigation/0013-state-vs-event-metrics-dashboard-suite-and-system-observability.md)
  — findings A–I; this design implements its full recommendation
- [INV-0012](../investigation/0012-inert-budgettracker-and-untrustworthy-alert-pack.md)
  — alert-window rules (findings C/E) applied to all generated alerts
- [DESIGN-0021](0021-delayed-requeue-job-contract-and-rate-limit-consolidation.md)
  — post-0021 metric surface this design is written against; shared
  installation-transport ordering contract (Phase 3 here, Phase 3
  there)
- [DESIGN-0012](0012-persistent-reconcile-state-and-multi-replica-coordination.md)
  — the Store/Scheduler surfaces extended here
- [IMPL-0013](../impl/0013-reconcile-open-prs-when-file-rules-become-satisfied.md)
  — `open_prs_by_rule` snapshot-gauge precedent (`ResetOpenPRsByRule`)
- [IMPL-0015](../impl/0015-stale-sweep-cutover-and-repository-discovery.md)
  — write-back best-effort contract this design extends
- [IMPL-0017](../impl/0017-configurable-annotation-sourced-custom-properties.md)
  — the schema-preflight cache that feeds `property_schema_missing`
- grafana-foundation-sdk: <https://github.com/grafana/grafana-foundation-sdk>
- OTel Go prometheus bridge:
  <https://pkg.go.dev/go.opentelemetry.io/otel/exporters/prometheus>
- `otelhttp`:
  <https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp>
- `redisotel`: <https://github.com/redis/go-redis/tree/master/extra/redisotel>
- `otelpgx`: <https://github.com/exaring/otelpgx>
- kube-prometheus-stack dashboard sidecar convention:
  <https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack>
