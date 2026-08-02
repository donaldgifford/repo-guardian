---
id: INV-0013
title: "State-vs-event metrics, dashboard suite, and system observability"
status: Open
author: Donald Gifford
created: 2026-08-01
---
<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0013: State-vs-event metrics, dashboard suite, and system observability

**Status:** Open
**Author:** Donald Gifford
**Date:** 2026-08-01

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [A. Metric inventory — one-by-one classification](#a-metric-inventory--one-by-one-classification)
  - [B. Eight metrics are posture masquerading as events](#b-eight-metrics-are-posture-masquerading-as-events)
  - [C. Postgres cannot serve posture today — the write-back is status-only](#c-postgres-cannot-serve-posture-today--the-write-back-is-status-only)
  - [D. The 1+N query fear is solvable three ways — and one is already built](#d-the-1n-query-fear-is-solvable-three-ways--and-one-is-already-built)
  - [E. Dashboard suite architecture](#e-dashboard-suite-architecture)
  - [F. System / USE / RED coverage gaps](#f-system--use--red-coverage-gaps)
  - [G. OTEL is the cross-hop answer, but RED does not have to wait for it](#g-otel-is-the-cross-hop-answer-but-red-does-not-have-to-wait-for-it)
  - [H. Config-generated dashboards and alerts — established pattern, strong fit](#h-config-generated-dashboards-and-alerts--established-pattern-strong-fit)
  - [I. Signal taxonomy — business / service / infra](#i-signal-taxonomy--business--service--infra)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!--toc:end-->

## Question

Three questions, one per problem observed while refreshing `contrib/`:

1. Which of repo-guardian's 52 metrics represent **current posture**
   (how many repos are missing CODEOWNERS *right now*) rather than
   **events** (a check observed a missing file), and therefore read
   wrong after a pod restart zeroes the counter?
2. Can the Postgres store serve posture directly — exposed as gauges —
   without every replica hammering the same query on every scrape, and
   does that require a Valkey query cache?
3. What dashboard architecture follows: KPI, per-org detailed, system
   health, and Loki — and where are the USE/RED gaps (go runtime, DB
   pool, Valkey, GitHub client, inbound HTTP), including whether RED
   requires adopting OTEL?

## Hypothesis

- A handful of `*_total` counters are being read as posture and cannot
  provide it even in principle — a repo fixed yesterday still counts in
  last week's increments, and a restart zeroes the "current" reading.
- The store cannot answer posture queries today because the worker
  write-back persists only `{status, error, policy_version}` — no
  per-rule outcomes — so a DB-backed gauge needs a schema extension,
  not just an exporter.
- The 1+N scrape-fan-out fear is real for a naive custom Collector but
  dissolves with any of: leader-scoped export (already built), a
  ticker-driven gauge (query cadence decoupled from scrape cadence), or
  a Valkey-cached query layer.
- RED for the service's own hops can be done metrics-only; only
  cross-hop tracing (ALB → gateway → service → PG/Valkey/GitHub)
  requires OTEL.

## Context

**Triggered by:** the contrib/ refresh on this branch (commit
`e6f141a`), the enterprise migration operational work (INV-0012 branch,
PR #171), and the operator observation that "missing catalog-info /
CODEOWNERS counts clear on restart."

Adjacent to [INV-0012](0012-inert-budgettracker-and-untrustworthy-alert-pack.md)
but distinct: INV-0012 established that the BudgetTracker metrics are
dead and the alert packs were unvalidated; this investigation is about
the *semantics* of the live metrics, the dashboard suite that should
consume them, and the system-level observability that doesn't exist yet.

## Approach

1. Classify all 52 metrics in `internal/metrics/metrics.go` by
   constructor type and by semantic class (event, posture, snapshot
   gauge, live gauge, latency, dead).
2. Read the store schema (`internal/store/postgres/migrations/`) and
   the worker write-back (`internal/worker/worker.go.writeBack`) to
   establish what posture data the DB actually holds.
3. Grep the instrumentation surfaces: metrics registry wiring
   (`main.go`), GitHub client, webhook handler, pgxpool, go-redis.
4. Verify the structured-log surface for the Loki dashboard (handler
   format, locked log contracts).
5. Enumerate dashboard mechanics (Grafana repeated rows, template
   variables, Loki ruler) against what the metric label sets support.

## Environment

| Component | Version / Value |
|-----------|----------------|
| repo-guardian | appVersion 1.10.1, chart 1.0.0-rc.9 |
| Metrics registry | `promauto` on default registry, `promhttp.Handler()` (main.go:413) |
| Valkey client | `github.com/redis/go-redis/v9` v9.19.0 |
| Store | pgx/v5 + pgxpool, `repo_state` table (migration 0001) |
| Logging | `slog.NewJSONHandler` → stdout (main.go:520) |
| promtool | 3.12.0 (mise) |

## Findings

### A. Metric inventory — one-by-one classification

52 metrics: 40 CounterVec/Counter, 6 Gauge/GaugeVec (live), 6
Histogram. Classified by what the number *means*, not what type it is:

**Event counters — correct as-is.** Occurrences of an action; `rate()`
and `increase()` are the only honest reads; restart-zeroing is handled
by PromQL counter-reset detection and is a non-issue for these.

| Metric | Notes |
|--------|-------|
| `repos_checked_total{trigger,org}` | work throughput |
| `prs_created_total{org}`, `prs_updated_total{org}`, `prs_closed_total{org,reason}` | PR lifecycle events |
| `pr_orphan_left_total{org}` | cleanup failure events (retried next sweep) |
| `settings_remediated_total`, `branch_protection_remediated_total` | remediation actions |
| `settings_checked_total`, `branch_protection_checked_total` | evaluation throughput |
| `rule_gate_closed_total{rule_name,org,reason}` | gate evaluations (diagnostic) |
| `ignored_total{scope,org}`, `out_of_scope_total{level,org}` | skip events (diagnostic) |
| `webhook_received_total{event_type}`, `webhook_rejected_total{reason}` | ingress events |
| `errors_total{operation,org}` | error events |
| `github_rate_limit_waits_total{reason}` | throttle events |
| `custom_property_cleared_total{org}` | clear actions |
| `queue_enqueued_total`, `queue_claimed_total`, `queue_acked_total{outcome}`, `queue_reaped_total` | queue lifecycle |
| `rate_limit_reserve_blocked_total{installation_id}` | gate events |
| `repo_discovered_total`, `discovery_api_calls_total` | discovery events |
| `store_writeback_total{installation_id,outcome}` | write-back events |

**Live gauges — correct as-is.** Sampled truth at scrape time:
`queue_depth{queue}`, `rate_limit_remaining{installation_id}`,
`github_rate_remaining`, `scheduler_is_leader{name}`.

**Snapshot gauge — the existing correct pattern for posture.**
`open_prs_by_rule{org,rule,age_bucket}`: reset at sweep start by
`metrics.ResetOpenPRsByRule` and repopulated as the sweep joins the
open-PR set against actionable rules. It answers "how many PRs are
open right now, by age" — surviving restarts *logically* (the next
sweep rebuilds it) with one blind window between restart and first
sweep. This is the in-tree precedent Finding B's metrics should follow
or improve on.

**Latency histograms — correct, three have no dashboard/alert
consumer.** `check_duration_seconds`, `github_rate_limit_wait_seconds`,
`store_query_seconds{op,outcome}` are consumed;
`scheduler_sweep_batch_size`, `store_writeback_duration_seconds`,
`discovery_duration_seconds` are exposed and documented but appear on
no dashboard panel or alert (write-back latency has a documented p99
target of 50ms and nothing watching it).

**Dead — INV-0012.** All six `api_budget_*` plus
`enqueue_gated_by_budget_total`: no producer calls `RefreshFromAPI` in
prod. Excluded from all dashboard planning here; DESIGN-0021 replaces
them.

**Legacy unlabeled.** `properties_checked_total`,
`properties_prs_created_total`, `properties_set_total`,
`properties_already_correct_total` — plain Counters with no `org`
label, predating the per-org labeling convention (IMPL-0009 §metrics).
`properties_already_correct_total` is also posture-shaped (Finding B).
Candidates for deprecation or relabeling when Finding B's design lands.

### B. Eight metrics are posture masquerading as events

The operator complaint — "counts of missing catalog-info or CODEOWNERS
clear on restart" — is not a restart bug. It is a semantic mismatch:

| Metric | What it actually counts | What operators read it as |
|--------|------------------------|---------------------------|
| `files_missing_total{rule_name,org}` | check-events that observed a missing file | repos currently missing the file |
| `files_forbidden_present_total{rule_name,org}` | check-events that observed a forbidden file | repos currently carrying it |
| `settings_mismatched_total{rule_name,org}` | evaluations that found drift | repos currently drifted |
| `custom_property_missing_schema_total{org,property}` | skipped sync attempts | properties currently undefined |
| `catalog_parse_failed_total{org}` | skipped reconciles | repos with currently-broken catalog files |
| `pr_open_with_empty_actionable_total{org}` | drift observations | PRs currently drifted |
| `properties_already_correct_total` | no-op syncs | fleet compliance level |
| `open_prs_by_rule` | *(already a snapshot gauge — the model)* | — |

The failure mode is worse than restart-zeroing: **a counter cannot
represent posture even between restarts.** A repo that gained
CODEOWNERS yesterday still contributes to `increase(...[7d])` today; a
repo checked twice counts twice; a repo the sweep hasn't reached counts
zero. Any KPI panel titled "repos missing CODEOWNERS" built on these
counters is wrong on all three axes, and the restart just makes it
obviously wrong instead of subtly wrong.

The counters are still valuable *as events* (detection rate, activity,
alerting on `increase()`); the fix is not to remove them but to add a
posture representation alongside — which leads to Finding C.

**Source assignment — the decision run over each posture metric.** The
operational test is three questions: (1) is the number only meaningful
"as of now"? (2) is the system of record *our check outcomes* (vs
GitHub's state, vs the process itself)? (3) do consumers need identity
or since-when durations? Both yes on 1+2 → Postgres-backed; system of
record GitHub → snapshot/TTL gauge projection; otherwise pure Prom.

| Posture reading | System of record | Source verdict |
|-----------------|------------------|----------------|
| repos missing a file (`files_missing_total`) | our check outcomes | **PG** — `rule_state` rows → `repos_actionable{rule_name,org}` gauge |
| repos with forbidden file (`files_forbidden_present_total`) | our check outcomes | **PG** — same table, absent-mode rows |
| repos with setting drift (`settings_mismatched_total`) | our check outcomes | **PG** — `rule_state` must include setting rules |
| branch-protection drift | our check outcomes | **PG** — closes a hole: no `*_mismatched` metric even exists for BP today (only checked/remediated) |
| repos with unparseable catalog (`catalog_parse_failed_total`) | our check outcomes | **PG** — parse-ok as a per-repo fact |
| fleet compliance % (`properties_already_correct_total`) | our check outcomes | **PG** — derived; deprecate the counter |
| open drifted PRs (`pr_open_with_empty_actionable_total` posture reading) | **GitHub** | snapshot gauge — `open_prs_by_rule` pattern is already correct; no PG mirror (the report can hit GitHub at generation time for PR links) |
| org schema missing properties (`custom_property_missing_schema_total` posture reading) | **GitHub** | TTL gauge set at preflight-cache refresh (`property_schema_missing{org,property}` 0/1) — the cache already knows; PG adds nothing |

Summarized: **Postgres backs exactly the facts the engine computes and
currently throws away — per-rule check outcomes.** GitHub-owned
posture gets projections (sweep snapshot or preflight-TTL gauges).
Process behavior — every rate, latency, and saturation signal — stays
pure Prometheus and never touches the DB.

### C. Postgres cannot serve posture today — the write-back is status-only

The full extent of persisted state (migration 0001):

```sql
CREATE TABLE repo_state (
    installation_id   BIGINT  NOT NULL,
    owner             TEXT    NOT NULL,
    repo              TEXT    NOT NULL,
    last_checked_at   TIMESTAMP WITH TIME ZONE,
    last_check_status TEXT    NOT NULL DEFAULT 'pending',
    last_error        TEXT,
    policy_version    TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (installation_id, owner, repo)
);
```

`worker.writeBack` persists `{status, truncated error, policy_version,
timestamp}` — the engine's per-rule findings (`findActionableRules`'s
actionable set) are computed, used to open/refresh the PR, and
discarded. "SELECT count of repos missing CODEOWNERS" has no table to
run against. So the user-suggested "provide it by a DB query exposed as
a metric" requires a schema extension first. Two shapes worth carrying
into a design:

- **C1 — per-rule state table.** `rule_state(installation_id, owner,
  repo, rule_name, actionable bool, check_mode, updated_at)`, upserted
  in the same write-back path (one batched statement per job; the
  actionable set is already in memory at that point). Posture is then
  `SELECT rule_name, count(*) FILTER (WHERE actionable) GROUP BY 1` —
  and it also unlocks queries metrics can't express at all ("list the
  repos", "compliance % per org per rule", history via audit trigger).
- **C2 — JSONB column on `repo_state`.** `actionable_rules JSONB` —
  cheaper migration, no second table, worse queryability (needs
  `jsonb_array_elements` for per-rule counts) and no per-rule
  `updated_at`. Fine if posture-by-rule-per-org is the only consumer;
  C1 if the "show me the repos" operator workflow matters (it did,
  twice, during this week's enterprise migration debugging).

Interim zero-schema option: **C3 — extend the sweep-snapshot pattern**
(`open_prs_by_rule` style) with
`repos_actionable{rule_name,org}` gauges repopulated per sweep from the
engine's in-memory result. No migration, but posture lags a full sweep
interval, goes blind between restart and first sweep, and only exists
on the leader replica. Honest enough for a KPI dashboard's "as of last
sweep" panel; not honest enough for point-in-time compliance reporting.

Considered and rejected as primary: **C4 — Loki last-state
projection.** Emit a structured per-repo per-rule outcome log line
(does not exist today) and reconstruct current state in LogQL
(`last_over_time`-style, `repo` as a parsed field). It works as an
event-sourced approximation, but every posture query must scan a
window longer than the sweep interval — which is *weekly* — across
5k repos, with retention pinned ≥ the window forever, versus one
indexed `count(*) GROUP BY` on a table the workers already write in
the same code path. Loki-derived metrics stay correct for log-shaped
event signals (Finding E4); they are a fallback for posture, not a
design.

**The downstream report requirement settles the C1-vs-C2 choice.** The
stated goal is a compliance dashboard *and* a generated per-org report
sent to org owners. A report needs repo *names* (identity — never
representable in metrics, marginal in Loki) and *durations* ("missing
since 2026-06-14" is what makes an owner act), which means per-rule
rows with timestamps: C1, not C2's JSONB blob. Two riders fall out:
a nightly `compliance_snapshot(org, rule_name, actionable_count, ts)`
insert gives quarter-over-quarter history independent of Prometheus
retention, and the report generator itself is a
`repo-guardian report` sibling of Finding H's `monitoring generate`
subcommand (loads the same config, queries the same tables, renders
per-org output; Grafana's native reporting is an Enterprise-license
feature, so the CLI shape is the OSS path). The compliance dashboard
is then a mixed-datasource dashboard: Prom for the projected count
gauges and trends, the Grafana Postgres datasource for the "which
repos" table panels.

### D. The 1+N query fear is solvable three ways — and one is already built

The stated concern: with N replicas scraped every 30s, a naive
DB-backed gauge runs the same posture query N times per 30s. Three
resolutions, cheapest first:

- **D1 — leader-scoped exporter (already built).** The Valkey
  scheduler's SETNX leader election (`repo-guardian:lock:<name>`)
  already designates exactly one replica for sweeps. A posture exporter
  registered as a leader-gated `Schedule` handler (query every 60s,
  `Set()` the gauges) runs the query on **one** replica at **one**
  cadence, independent of scrape rate and replica count. Non-leader
  replicas simply don't serve the series; dashboards aggregate with
  `max by (...)`, exactly as `scheduler_is_leader` panels already do.
  No cache layer, no new infra.
- **D2 — ticker-driven gauge instead of custom Collector.** Even
  without leader-gating, the 1+N problem is an artifact of implementing
  posture as a `prometheus.Collector` that queries *at scrape time*. A
  plain ticker that queries every 60s and `Set()`s decouples query
  cadence from scrape cadence: N replicas = N queries/minute total, not
  N per scrape. Acceptable for small N; D1 makes it 1.
- **D3 — Valkey as a query cache.** `GET posture:<hash>` → on miss,
  `SET ... EX 60` + query. Solves the same problem as D1 with more
  moving parts (serialization, TTL vs sweep-freshness coherence,
  stampede control — go-redis v9 is in-tree, and `singleflight` is
  already a house pattern from the schema-preflight cache). Worth it
  only if something *other than metrics export* needs the same cached
  query results (e.g., a future status API endpoint or the KPI
  dashboard querying Postgres directly via Grafana's PG datasource).
  As a pure metrics answer, D1 strictly dominates.

Verdict to carry into the design: **D1 for export; D3 only if a second
consumer for posture queries appears.** The query itself (C1 shape,
count-group-by over ≤ `20 orgs × 5k repos × ~6 rules` rows) is
index-friendly and sub-second; the fan-out was the only real concern.

### E. Dashboard suite architecture

Split the current single 61-panel dashboard into four, in `contrib/grafana/`:

- **E1 — KPI / status.** Single-screen posture: compliance % per rule
  (needs C), open-PR ages (`open_prs_by_rule` — exists), convergence
  rate (`prs_closed_total{reason="satisfied"}`), error budget
  (`errors_total` / `repos_checked_total`), rate-limit headroom
  (`rate_limit_remaining` min by installation), queue depth, leader
  status. Everything except the compliance % is buildable today; the
  compliance panels should ship in "as of last sweep" form (C3) or wait
  for C1 — *not* be faked with `increase(files_missing_total)`.
- **E2 — detailed, per-org repeated rows.** Grafana multi-value
  variable `org` from `label_values(repo_guardian_repos_checked_total,
  org)`; a top "fleet" section of aggregate panels
  (`sum without (org)`), then one **repeated row** per selected `$org`
  (row → Repeat for → `org`) containing the per-org slices: checks by
  trigger, missing/forbidden by rule, PR lifecycle, gate closures,
  property sync, errors by operation. Every engine-side metric already
  carries `org`, so this needs zero binary changes. Two scoping gaps to
  note on the dashboard: queue/store/scheduler metrics are
  fleet-scoped by design (they belong in E3, not in org rows), and
  discovery/rate-limit metrics are keyed by `installation_id` with **no
  org↔installation join series** — a one-line info metric
  `repo_guardian_installation_info{installation_id, org} 1` set at
  client-construction time would let Grafana `group_left` those into
  org rows. Cheap, high-leverage, should ride along with any Finding C
  implementation.
- **E3 — system / health.** Consumes what Finding F enumerates: go
  runtime + process (already exposed), DB pool, Valkey client, GitHub
  client RED, inbound webhook RED, queue USE (depth = saturation,
  enqueue/ack = utilisation, reaped = errors), store latency (exists),
  the three orphan histograms from Finding A.
- **E4 — Loki logging dashboard.** Grounded in what the binary
  actually emits: `slog.NewJSONHandler` to stdout, so LogQL is
  `| json | level="ERROR"` etc. Two log lines are load-bearing
  contracts already: `"custom properties missing from org schema"`
  (keys `org`, `missing_properties` — locked by
  `TestAPIMode_FiltersUndefinedMappedProperty`) and `"catalog-info
  parse failed; skipping reconcile to avoid clearing properties"` (not
  test-locked — should be, if a dashboard starts depending on it).
  Panels: error-level rate by component, sweep summaries
  (`gated_rate_limit` / `gated_budget` fields), write-back failures,
  webhook rejections by reason, top erroring repos. **Loki-derived
  metrics belong in the Loki ruler, not the app**: recording rules like
  `sum by (org) (count_over_time({app="repo-guardian"} | json |
  msg="catalog-info parse failed..." [1h]))` give log-sourced series
  without new instrumentation — appropriate for signals that are
  naturally log-shaped (per-repo failure identity) where a metric label
  would explode cardinality (`repo` must never become a metric label at
  5k repos; it is fine as a log field).

### F. System / USE / RED coverage gaps

What exists vs. what's missing, per dependency:

| Surface | Exists today | Missing |
|---------|--------------|---------|
| Go runtime / process | `go_*`, `process_*` (promauto default registry — verified exposed) | any dashboard or alert consuming them (GC pause, goroutines, RSS, FD count) |
| Postgres | `store_query_seconds{op,outcome}` (R+E of RED) | pool USE: `pgxpool.Stat()` (AcquireCount, AcquiredConns, IdleConns, EmptyAcquireCount = pool-exhaustion signal) is never collected; a ~30-line custom Collector |
| Valkey | queue-level counters | client-level: go-redis pool stats via `redisprometheus.NewCollector` (hits/misses/timeouts/idle) — one constructor call per client |
| GitHub API | `github_rate_remaining`, wait counters/histogram, `errors_total` | RED per request: no duration histogram, no status-code dimension, no endpoint-class dimension. A `RoundTripper` wrapper on the installation transport — `github_request_duration_seconds{method, endpoint_class, code}` with a small fixed endpoint-class set (contents, pulls, rulesets, properties, meta) to cap cardinality |
| Inbound HTTP (webhook) | received/rejected/enqueued counters (R+E) | duration histogram (D of RED) + response-code labels; `promhttp.InstrumentHandler*` middleware is stdlib-adjacent and fits the existing mux |
| Queue | depth gauge, lifecycle counters | nothing structural — USE is derivable; E3 just needs the panels |

None of these require OTEL; all are client_golang idioms on code paths
that already exist.

### G. OTEL is the cross-hop answer, but RED does not have to wait for it

The full-path ask (ALB → Cilium gateway → service → Valkey/PG/GitHub)
is a **tracing** problem: only trace propagation stitches hops owned by
different systems. That means OTEL SDK + `otelhttp` (inbound),
`otelpgx`, `redisotel`, a transport wrapper for go-github, W3C
`traceparent` propagation (ALB and Cilium both forward it), and an
in-cluster collector (Alloy / otel-collector) — plus a tracing backend
(Tempo, since the stack is already Grafana-flavored). That is real
scope: a dependency footprint, a config surface, and cluster infra.

The pragmatic split: **Finding F's metrics-only RED first** (no new
infra, answers "which hop is slow/erroring" at the aggregate level),
OTEL tracing as a separate opt-in effort when per-request "why was
*this* webhook slow" matters. Adopting `otelhttp` middleware early on
the webhook path is cheap and forward-compatible (it emits both traces
and metrics), but wiring the full chain should not gate the dashboards.

**What OTEL instrumentation would unmask — signals current metrics
hide (verified against the code):**

1. `check_duration_seconds` conflates engine compute, GitHub API
   latency, and the DESIGN-0002 transport's rate-limit sleeps (up to
   an hour *inside* `RoundTrip`). INV-0012 found the sleep by code
   reading; a client-span would have shown it as an hour-long span on
   day one. `otelhttp` client instrumentation on the installation
   transport decomposes all three.
2. `errors_total{operation}` collapses status truth: GitHub 429 vs
   403 vs 5xx vs network error are indistinguishable, and go-github
   retries are invisible. Client `http.client.request.duration
   {status_code}` + span events carry both.
3. **HMAC 401 rejections are counted nowhere.** `webhook_rejected_total`
   increments only in the allowlist middleware (`allowlist.go`); a
   signature-validation failure returns 401 with no metric. Server
   middleware (`otelhttp` or `promhttp.InstrumentHandler*`) emits
   duration + status for *every* request — 401 volume, 404 scans,
   panic-500s — closing the gap wholesale rather than per-reason.
4. Enqueue latency inside the webhook path: a Valkey stall makes the
   202 slow and nothing measures it — `redisotel` (or a go-redis
   hook) times every command including the reaper Lua and leader
   SETNX, none of which are measured today.
5. `store_query_seconds` starts its timer before pool acquire
   (`observeQuery` wraps the pool call), so pool exhaustion
   masquerades as slow queries with no discriminator. `otelpgx`
   separates acquire from query spans; the `pgxpool.Stat()` collector
   (Finding F) is the aggregate USE view of the same thing.
6. End-to-end "webhook received → PR opened" latency is a pure trace
   concept: DESIGN-0021's `queue_wait_seconds` measures one hop;
   traces stitch webhook → enqueue → claim → check → GitHub writes.

Dedup rule for the design: OTEL semconv metrics overlap hand-rolled
ones (`db.client.operation.duration` vs `store_query_seconds`). Keep
the domain metrics, adopt OTEL at the transport layers, and pick one
source per dashboard panel — never both.

**What OTEL actually lets us remove.** Less than intuition suggests,
because nearly every hand-rolled metric is *domain*-level (org, rule,
trigger, reason labels OTEL cannot know). The honest ledger:

- *Removable existing metrics (2):* `github_rate_limit_waits_total`
  and `github_rate_limit_wait_seconds` — but their real executioner is
  DESIGN-0021, which stops the transport from sleeping at all; OTEL
  client histograms then carry the residual signal (slow GitHub calls
  by status). Nothing else existing goes away: `store_query_seconds`
  keeps its domain `op` labels (semconv sees SQL verbs, not
  `stale_repos`), `check_duration_seconds` is job-level not
  transport-level, `webhook_*` reasons are security semantics.
- *Planned work cancelled (3–4 items from Finding F):* the
  hand-rolled GitHub RoundTripper RED histogram → `otelhttp` client
  transport; the webhook duration middleware → `otelhttp` server; the
  `redisprometheus` collector → `redisotel` metrics; possibly the
  `pgxpool.Stat()` collector if `otelpgx`'s pool-stats option covers
  it (verify at design time — one or the other, not both).
- *Removed by other efforts, not OTEL:* the 7 dead budget series
  (DESIGN-0021 Phase 6) and the 4 legacy `properties_*` counters
  (posture design). Postgres loses nothing to OTEL — posture facts
  are business state, not telemetry.

**"OTEL now" without the infra bill:** the OTel Go SDK can export its
metrics through the *existing* Prometheus endpoint (the
`go.opentelemetry.io/otel/exporters/prometheus` bridge registers into
the same registry `promhttp` serves). So the instrumentation set —
`otelhttp` both directions, `redisotel`, `otelpgx` — can ship
metrics-first with zero new cluster infrastructure; the collector +
Tempo backend becomes a later, purely additive step that turns the
already-emitted spans on.

### I. Signal taxonomy — business / service / infra

Classification of every signal (live, planned, retiring) by tier, so
each dashboard has one obvious source list: **E1 KPI = business**,
**E2 detailed = business + service sliced by org**, **E3 = service +
infra**, **E4 Loki = evidence across all tiers**.

**Business — fleet compliance and value delivered (org-facing):**

| Signal | Source | Covers |
|--------|--------|--------|
| `repos_actionable{rule_name,org}` *(planned)* | PG → leader gauge | posture: repos failing each rule, per org |
| compliance % per org/rule *(planned)* | PG → leader gauge + PG datasource | the KPI headline |
| `compliance_snapshot` history *(planned)* | PG table | quarter-over-quarter reporting |
| "which repos" tables + per-org report | PG datasource / `report` CLI | identity + missing-since |
| `open_prs_by_rule{org,rule,age_bucket}` | binary (sweep snapshot) | open remediation PRs by age |
| `files_missing_total`, `files_forbidden_present_total` | binary counter | detection activity per rule/org |
| `settings_mismatched_total` | binary counter | setting-drift detections |
| `prs_created_total`, `prs_updated_total`, `prs_closed_total{reason}` | binary counter | remediation delivered |
| `settings_remediated_total`, `branch_protection_remediated_total` | binary counter | remediation delivered |
| `custom_property_cleared_total` | binary counter | property sync actions |
| `custom_property_missing_schema_total` | binary counter (+ planned TTL gauge) | org schema governance gap |
| `catalog_parse_failed_total` | binary counter (+ Loki for repo identity) | broken catalog files, per org |
| `properties_*` (4 legacy) | binary counter | *deprecate with posture design* |

**Service — repo-guardian's own operation:**

| Signal | Source | Covers |
|--------|--------|--------|
| `repos_checked_total{trigger,org}` | binary counter | throughput by trigger |
| `check_duration_seconds` | binary histogram | job latency (decomposed by OTEL spans later) |
| `errors_total{operation,org}` | binary counter | operation failures |
| `queue_depth`, `queue_{enqueued,claimed,acked,reaped}_total` | binary | queue USE |
| `queue_wait_seconds`, `queue_delayed_*`, `attempts_exhausted` *(DESIGN-0021)* | binary | queue latency + deferral behavior |
| `scheduler_is_leader`, `scheduler_sweep_batch_size` | binary | leader + sweep sizing |
| `store_writeback_total`, `store_writeback_duration_seconds` | binary | state write-back health |
| `repo_discovered_total`, `discovery_duration_seconds`, `discovery_api_calls_total` | binary | discovery behavior |
| `rate_limit_reserve_blocked_total` | binary counter | self-throttling decisions |
| `ignored_total`, `out_of_scope_total`, `rule_gate_closed_total` | binary counter | policy-engine gating |
| `pr_open_with_empty_actionable_total`, `pr_orphan_left_total` | binary counter | convergence-path faults |
| `webhook_received_total{event_type}`, `webhook_rejected_total{reason}` | binary counter | ingress domain semantics |
| `http.server.request.duration{route,status}` *(planned OTEL)* | OTEL → prom bridge | inbound RED incl. the uncounted 401s |

**Infra — dependencies and runtime:**

| Signal | Source | Covers |
|--------|--------|--------|
| `store_query_seconds{op,outcome}` | binary histogram | Postgres latency/errors (R+E) |
| pgx pool stats *(planned — `pgxpool.Stat()` or otelpgx, pick one)* | collector / OTEL | Postgres pool USE |
| `db.client.operation.duration` + acquire spans *(planned OTEL)* | OTEL | query-vs-acquire decomposition |
| Valkey command latency + pool *(planned — `redisotel`)* | OTEL | queue/scheduler dependency health |
| `http.client.request.duration{host,status}` *(planned OTEL)* | OTEL | GitHub API RED, status truth, retry visibility |
| `rate_limit_remaining{installation_id}`, `github_rate_remaining` | binary gauge | GitHub quota headroom |
| `github_rate_limit_waits_total`, `github_rate_limit_wait_seconds` | binary | *retiring with DESIGN-0021* |
| `api_budget_*` (7 series) | binary | *dead — removed by DESIGN-0021 Phase 6* |
| `go_*`, `process_*` | default registry | runtime USE (GC, goroutines, RSS, FDs) |
| k8s/node/ALB/gateway signals | cluster stacks | outside the binary; join via traces (Finding G) |

### H. Config-generated dashboards and alerts — established pattern, strong fit

The follow-up question: instead of static dashboards (or purely
label-discovered template variables), should repo-guardian *generate*
its monitoring artifacts from `guardian.hcl` — per-org rows for exactly
the configured orgs, panels only for enabled rules, alerts only for
mechanisms actually in use?

**Prior art — this is a named, established pattern, not a novelty:**

- **Monitoring mixins** (kubernetes-mixin, node-mixin, kube-prometheus)
  — the granddaddy: jsonnet packages that take a config object and
  emit dashboards JSON + PrometheusRule YAML via `mixtool`. "Read
  config, generate dashboards + alerts" has been the prometheus-
  ecosystem convention for years.
- **Sloth / Pyrra** — generate PrometheusRule CRs from an SLO spec:
  the exact "validate a config and spit out a prom alerts CR" shape.
- **Elastic beats** `setup --dashboards` — the shipping binary knows
  its own enabled modules and pushes matching dashboards; precedent
  for the CLI-subcommand shape.
- **Grafana Foundation SDK** (`grafana/grafana-foundation-sdk`) — the
  current official, strongly-typed Go builder library: chained
  builders → `Build()` → standard dashboard JSON, or wrapped in
  `apiVersion/kind/metadata` for Grafana's Kubernetes-compatible API /
  grafana-operator `GrafanaDashboard` CRs. Grafana ≥ 10. (The older
  community lineage — grabana, DARK — is superseded by this.)

**Why the fit is unusually strong here.** The E2 template-variable
approach (`label_values(...)`) can only show orgs that have *emitted
data*. An org that is configured in `scope.orgs` but silent is
invisible — and "configured but silent" is precisely the failure mode
of this week's enterprise migration (dead installation IDs, orgs
producing nothing). Config-generated dashboards invert the direction:
a row exists for every org that *should* report, so absence renders as
a no-data panel — a signal instead of a blind spot. The same inversion
applies to rules and alerts: no `custom_properties` reconciler
configured → no property-sync panels and no `PropertySchemaMissing`
alert; no absent-mode rules → no forbidden-files panels; no empty
graphs, no dead alerts. The generator also subsumes config
*validation*: it must `policy.Load()` the same HCL the server loads,
so `repo-guardian monitoring generate` in CI is a free config check
(the strict-templates precedent, extended).

**Where generation runs — three shapes:**

- **H1 — CLI subcommand (recommended).** `repo-guardian monitoring
  generate --config guardian.hcl --out ./monitoring/ --format
  json|k8s` emitting dashboard JSON (or GrafanaDashboard CRs) + a
  PrometheusRule CR. Artifacts are committed to the GitOps repo and
  consumed by kustomize/ArgoCD like any manifest; a CI check re-runs
  the generator and fails on diff (the `helm-docs`/`docz update` drift
  convention this repo already uses). The binary already links the
  policy loader; the foundation SDK is a codegen'd type dependency —
  if its weight on the server binary matters, the subcommand can live
  in a separate `cmd/` binary sharing `internal/policy`.
- **H2 — kustomize KRM/exec plugin** wrapping H1 — generation at
  render time, no committed artifacts. Works, but exec plugins need
  `--enable-alpha-plugins` everywhere including ArgoCD's repo-server,
  which is operationally clunky; only worth it if config churn makes
  committed artifacts annoying.
- **H3 — operator/sidecar** applying grafana-operator CRs at runtime
  from the live ConfigMap. Most automatic, most infra, hardest to
  review; not justified at current config-change frequency.

**Trade-offs to carry into the design:** SDK output is coupled to
Grafana schema versions (pin and test-render); generated and static
artifacts must not fork silently — the static `contrib/` suite remains
the zero-config fallback tier, the generated suite is the config-aware
tier, and both should share panel definitions (generate the static one
from an empty/default config so there is exactly one source of truth);
`repo` must still never become a metric label — generation does not
change cardinality rules, it only scopes which series get panels.

## Conclusion

**Answer:** Confirmed on all three fronts.

1. Eight metrics are posture-shaped counters (Finding B); they cannot
   answer "how many now" regardless of restarts, and the restart just
   surfaces it. `open_prs_by_rule` is the one metric already doing
   posture correctly.
2. The DB cannot serve posture today — per-rule outcomes are discarded
   after each check (Finding C). A Valkey query cache is **not** the
   missing piece; the missing piece is persisted per-rule state, and
   the leader election already solves the 1+N export fan-out (Finding
   D) without a cache.
3. The dashboard split is four dashboards (Finding E); the system-level
   gaps are enumerable and metrics-only (Finding F); OTEL is required
   only for cross-hop tracing and should not gate anything else
   (Finding G).
4. Generating dashboards and alert CRs from `guardian.hcl` is an
   established ecosystem pattern (mixins, Sloth/Pyrra, beats setup)
   with an unusually strong fit here: it makes configured-but-silent
   orgs visible — the exact blind spot of this week's enterprise
   migration — and doubles as CI config validation (Finding H).

## Recommendation

Three follow-up efforts, in dependency order:

1. **DESIGN: compliance-posture state and exporters.** Per-rule state
   persistence (C1 vs C2 decision), leader-scoped posture exporter
   (D1), the `installation_info` join metric (E2), and deprecation
   path for the four legacy `properties_*` counters. This is the only
   binary-changing prerequisite for the KPI dashboard's headline
   panels.
2. **Dashboard suite DESIGN.** E2 detailed per-org, E3 system/health
   (with the three pgxpool/go-redis/GitHub RoundTripper collectors
   from Finding F as a small accompanying binary change if accepted),
   E4 Loki — plus E1 KPI in "as of last sweep" degraded mode until
   effort 1 lands. Test-lock the catalog-parse log line before E4
   depends on it. The design's central decision is Finding H: static
   contrib dashboards vs a `repo-guardian monitoring generate` CLI
   subcommand (H1, foundation SDK) emitting the config-aware suite —
   recommended shape is H1 with the static contrib tier generated
   from a default config so there is one source of truth.
3. **OTEL adoption, split in two.** Metrics-first now: `otelhttp`
   (both directions), `redisotel`, `otelpgx` exporting through the
   existing Prometheus endpoint via the OTel prometheus bridge — zero
   new infra, closes the Finding G unmask list, and cancels 3–4 items
   of Finding F's planned hand-rolled work. Tracing infra (collector +
   Tempo) as a later, purely additive DESIGN; explicitly decoupled so
   it cannot stall efforts 1–2. Dashboard source lists follow the
   Finding I taxonomy (E1=business, E2=business+service by org,
   E3=service+infra).

Sequencing note: DESIGN-0021 (delayed requeue) already replaces the
dead budget metrics with measured queue-delay observability; effort 1
should be written against the post-0021 metric surface to avoid
designing panels for series that are about to be deleted.

## References

- [INV-0012](0012-inert-budgettracker-and-untrustworthy-alert-pack.md) — dead budget metrics, alert-validation gaps
- [DESIGN-0021](../design/0021-delayed-requeue-job-contract-and-rate-limit-consolidation.md) — replaces the budget mechanism; new queue-delay metrics
- [IMPL-0009](../impl/0009-per-org-rule-scoping-and-observability.md) — per-org metric labeling convention
- [IMPL-0013](../impl/0013-reconcile-open-prs-when-file-rules-become-satisfied.md) — `open_prs_by_rule` snapshot-gauge precedent
- `contrib/README.md` — refreshed metric catalog (commit `e6f141a`)
- `internal/metrics/metrics.go`, `internal/worker/worker.go`, `internal/store/postgres/migrations/0001_init.up.sql` — primary sources
- Grafana repeated rows: <https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/create-dashboard/#configure-repeating-rows>
- Loki ruler recording rules: <https://grafana.com/docs/loki/latest/alert/#recording-rules>
- Grafana Foundation SDK: <https://github.com/grafana/grafana-foundation-sdk> — official Go builders → dashboard JSON / k8s-resource output
- Foundation SDK CI/CD provisioning: <https://grafana.com/docs/grafana/latest/as-code/observability-as-code/foundation-sdk/dashboard-automation/>
- Monitoring mixins (config → dashboards + alerts precedent): <https://github.com/kubernetes-monitoring/kubernetes-mixin>
- Sloth (SLO spec → PrometheusRule generation precedent): <https://github.com/slok/sloth>
