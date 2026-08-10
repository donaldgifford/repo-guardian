---
id: IMPL-0023
title: "Compliance posture state, dashboard suite, and OTEL-first observability"
status: Draft
author: Donald Gifford
created: 2026-08-02
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0023: Compliance posture state, dashboard suite, and OTEL-first observability

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-08-02

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Sequencing and Release Shape](#sequencing-and-release-shape)
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
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Implement
[DESIGN-0022](../design/0022-compliance-posture-state-dashboard-suite-and-otel-first.md)
(all open questions resolved 2026-08-02): persist per-rule check
outcomes in a new `rule_state` table and project them to Prometheus
via a leader-scoped exporter; instrument the four transport boundaries
with OTEL exported through the existing `/metrics` endpoint; add
compliance snapshots and a `repo-guardian report` CLI; and build the
four-dashboard suite through a `repo-guardian monitoring generate`
subcommand driven by `guardian.hcl`, with the static `contrib/` tier
as the generator's default-config output.

**Implements:** DESIGN-0022 (resolved: 1a, 2a, 3a, 4a, 5a, 6a, 7b)

## Scope

### In Scope

- Migration 0002 (`rule_state`, `compliance_snapshot`,
  `repo_state.catalog_parse_ok`), `Engine.CheckRepo` returning
  `*CheckResult`, `Store.UpsertRuleStates` + posture queries, worker
  write-back extension.
- `posture-export` and `compliance-snapshot` leader-gated schedule
  handlers; `installation_info` and `property_schema_missing` gauges.
- OTEL SDK + prometheus bridge; `otelhttp` (webhook server + GitHub
  client transport), `redisotel`, `otelpgx`.
- `repo-guardian monitoring generate` and `repo-guardian report`
  subcommands on the main binary (OQ5 → a);
  grafana-foundation-sdk panel library; E1–E4 dashboards;
  grafana-operator CR output (OQ7 → b); CI drift gate.
- Phase 7 removal of the four legacy `properties_*` counters
  (OQ4 → a).

### Out of Scope

- Tracing infrastructure (collector, Tempo, OTLP config) — spans go
  to the no-op TracerProvider; later additive design.
- `repo` as a metric label, Loki as a posture source, Grafana
  Enterprise reporting, report *delivery* automation — all design
  non-goals.
- DESIGN-0021's queue mechanics (IMPL-0022) — only the shared
  transport-ordering contract touches both.

## Sequencing and Release Shape

- Phase order is dependency order: 2 and 4 require 1; 6 requires 5
  and consumes 2–4's series; 7 is last. Phase 3 is independent and
  may run in parallel with 1–2.
- **Phases 1+2 ship as one binary minor** (schema + exporter;
  migration 0003 is additive, rollback is binary-only). **Phase 3
  ships as a minor.** Phases 4–6 are additive and ship at their own
  pace. **Phase 7 is a minor with a prominent note** — the only
  scrape-visible removal (the four legacy counters). PR labeling
  across phases is Open Question 5.
- Coordination with IMPL-0022: whichever Phase 3 lands second adds
  the transport-ordering test (`otelhttp` outermost, rate-limit
  transport inside, `ghinstallation` innermost). Generated
  panels/alerts referencing IMPL-0022's queue metrics appear only
  once those metrics exist — the generator's mechanism-scoping
  handles the gap naturally.

Run `make fmt` + `make lint` after each task; commit per numbered
task with conventional commits.

---

## Implementation Phases

### Phase 1: Per-rule posture state

The engine computes per-rule outcomes in `findActionableRules` and
discards them; this phase persists them in the same write-back that
already stores `{status, error, policy_version}`.

#### Tasks

- [x] 1.1 Migration `0003` (up + down) in
      `internal/store/postgres/migrations/`: `rule_state` with the
      partial index on `(owner, rule_name) WHERE actionable`,
      `compliance_snapshot`, and
      `ALTER TABLE repo_state ADD COLUMN catalog_parse_ok BOOLEAN`.
      **Divergence:** written as `0002`, renumbered to `0003` when main
      was merged — INV-0015 shipped its own `0002_repo_active` while
      this branch was parked. Different filenames, so git merged both
      silently and golang-migrate would have refused the source at
      startup. The two are independent; `MigrateUpDownUp` covers the
      full 0001-0003 chain.
- [x] 1.2 Change `Engine.CheckRepo` (engine.go:97) to return
      `(*CheckResult, error)`: `Outcomes []RuleOutcome{RuleName,
      Kind, Actionable}` covering file, setting, and
      branch-protection rules (BP gains its first actionable verdict
      — it has no mismatch signal today) plus
      `CatalogParseOK *bool` from the reconciler outcome. Error paths
      return `(nil, err)`.
- [x] 1.3 `Store.UpsertRuleStates(ctx, states)` on the interface +
      postgres impl: single-transaction batched upsert with the
      `actionable_since` transition CASE (set on false→true, cleared
      on true→false, preserved on true→true), then delete-not-in
      reconciliation of this repo's rows absent from the evaluated
      set (OQ3 → a).
- [x] 1.4 Worker `writeBack` extension: persist the `CheckResult`
      (rule rows + `catalog_parse_ok`) best-effort in the same call
      path — failures log Warn + count
      `store_writeback_total{outcome="error"}`, never fail the job
      (IMPL-0015 Phase 0 contract).
- [x] 1.5 `make mocks` for the Store; update the test-local
      `fakeStore` recorders and every `CheckRepo` call site
      (worker, checker tests) for the new signature.
- [x] 1.6 Tests: `actionable_since` transition semantics
      (table-driven over the four edges); delete-not-in removes a
      renamed rule's row on the next check; concurrent batched
      upsert race (16-goroutine pattern from
      `TestPostgresStore_UpsertIfMissing_ConcurrentRace`); rule-state
      write failure does not change the job outcome.

#### Success Criteria

- After a sweep, `SELECT owner, rule_name, count(*) FILTER (WHERE
  actionable) FROM rule_state GROUP BY 1,2` matches the engine's
  in-memory actionable sets for every rule kind.
- A repo flipping failing → satisfied gets
  `actionable=false, actionable_since=NULL` on its next check.
- Store failures never block jobs; migration applies and rolls back
  cleanly.
- `make ci` passes (integration tests under the `integration` tag
  against real Postgres).

---

### Phase 2: Leader-scoped posture exporter and join metrics

#### Tasks

- [x] 2.1 `repo_guardian_installation_info{installation_id, org} 1`
      set at installation-client construction
      (`CreateInstallationClient`) and during discovery.
      **Divergence:** emitted at the worker's `CreateInstallationClient`
      *call site*, not inside the method. The method takes only an
      installation ID; resolving the account login there costs an
      `Apps.GetInstallation` call per job, while the caller already
      holds `j.Owner`. Both emissions precede the call they accompany so
      a failing installation still carries its org label — which is when
      the `installation_id`-keyed panels are actually being read.
- [x] 2.2 `posture-export` handler registered via
      `Scheduler.Schedule` (same SETNX election as `stale-sweep`),
      interval `POSTURE_EXPORT_INTERVAL` (default 60s): full
      GaugeVec `Reset()` then `Set()` of
      `repos_actionable{rule_name, org}` and `repos_tracked{org}`
      (the `ResetOpenPRsByRule` precedent — stale series die on the
      next tick). Compliance ratio stays in PromQL (OQ2 → a).
      **Decided (from the INV-0015 merge): filter on
      `repo_state.active`, and export the excluded population as its own
      series.** Parked repos leave both the numerator and the
      denominator, so a compliance ratio is only ever computed over
      repos we can actually measure. The alternative — letting an
      access-denied repo hold its last verdict — makes the ratio quietly
      wrong in a way nothing corrects, because parking is exactly the
      state in which a repo is never re-checked. Chosen for usability
      over brevity: the filter is more verbose at the query, but it
      makes "N repos unmeasurable" a first-class number instead of an
      invisible distortion, and `active = false` is then reusable as the
      general "not in the fleet right now" predicate for the report and
      any later exporter.

      Implementation notes for this task:

      - The filter needs a join. `active` lives on `repo_state`;
        `rule_state` has no such column. The aggregate becomes
        `rule_state JOIN repo_state USING (installation_id, owner, repo)
        WHERE repo_state.active`, which is a join on the `repo_state`
        primary key but does change the shape from the single-table
        aggregate `idx_rule_state_actionable` was built for. Check the
        plan at fleet size before assuming the partial index still
        carries it; if it does not, that is an index change, not a
        reason to revisit the decision.
      - Archived and fork parks already clear their `rule_state` rows
        (`Pool.park` passes an empty result), so for them the filter is
        belt-and-braces. It is load-bearing for `access_denied`, which
        deliberately keeps its rows.
      - The unmeasurable series is per-org and should distinguish *why*.
        `repo_state` has no `park_reason` column, so the discriminator
        is `last_check_status`: `'error'` is access-denied, `'skipped'`
        is archived/fork (see docs/operations/migrations.md). Suggested
        shape `repos_unmeasurable{org, reason}`, reset-and-set on the
        same tick as the posture gauges so stale series die together.
      - Cross-check the total against
        `repo_guardian_repos_parked_total{reason}`, which already counts
        park *events*; the new gauge is the standing population, and the
        two disagreeing is a real signal (parks that never un-parked, or
        un-parks nobody counted).

      **Shipped.** `Store.Posture(ctx) (*Posture, error)` returns all
      three aggregates from ONE read-only transaction rather than the
      sketch's separate `ActionableCounts`: the exporter divides
      Actionable by Tracked, and `UpsertRuleStates` rewrites a repo's
      whole rule set atomically, so two independent reads could straddle
      a write-back and publish a compliance ratio above 100%.
      `internal/checker/posture.go` holds the handler, wired in
      `main.go` at `checker.DefaultPostureExportInterval` until task 2.5
      makes it configurable. Reason labels come from a closed set
      (`store.Reason*`) mapped in SQL, with unmapped `last_error` values
      collapsing to `unknown` — free text reaching a Prometheus label
      would be a cardinality incident, not a wrong number.

      **Divergence:** the read happens BEFORE `ResetPosture()`, not
      after. Resetting first blanks every series for the duration of the
      query, so a scrape landing in that window reports zero repos
      tracked — indistinguishable from a fleet that vanished, and on a
      60s tick against a slow query that is not a rare interleaving. A
      failed read now leaves the previous values standing, which is the
      right failure mode for a gauge.

      **Follow-up for the dashboard phase (6.1/6.2), not a defect
      here:** `repos_tracked{org}` counts distinct repos with any
      posture, so `repos_actionable{rule} / repos_tracked{org}`
      understates non-compliance for a rule scoped to a subset of the
      org. A rule applying to 10 of 100 repos with 5 failing reads as 5%
      rather than 50%. The per-rule denominator is available from the
      same aggregate if the panels need it; decide there, where the
      ratio is actually written.
- [x] 2.3 `property_schema_missing{org, property}` 0/1 gauge set at
      each schema-preflight cache refresh in
      `internal/reconciler/custom_properties.go` (the cache already
      knows; no DB involvement). Written only on the success branch of
      `fetchOrgSchema`; a failed fetch leaves the series at its last
      known value rather than clearing it. Zeros are written
      explicitly so a gap an operator fixes actually drops out.
- [ ] 2.4 Exporter health: `posture_export_total{outcome}` +
      `posture_export_duration_seconds`.
- [ ] 2.5 Config `POSTURE_EXPORT_INTERVAL` +
      `values.yaml` / `values.schema.json` + helm-unittest cases.
- [ ] 2.6 Tests: only the leader emits (two schedulers, one series
      source); a removed org/rule stops emitting after the next tick
      (non-vacuity: skip the `Reset()`, watch it fail); gauge values
      equal SQL truth.
- [ ] 2.7 `contrib/README.md` rows for all Phase 2 metrics.

#### Success Criteria

- With N replicas exactly one serves the posture series;
  `max by (rule_name, org)` is stable across leader failover.
- No stale series survive an org or rule leaving the fleet.
- `make ci` and helm-unittest pass.

---

### Phase 3: OTEL metrics-first

Four boundaries, one exporter, zero new infra. May run in parallel
with Phases 1–2.

#### Tasks

- [ ] 3.1 Dependencies + SDK setup in `cmd/repo-guardian/main.go`:
      MeterProvider with the
      `go.opentelemetry.io/otel/exporters/prometheus` bridge
      registered into the default registry `promhttp` already serves;
      no-op TracerProvider; honour `OTEL_SDK_DISABLED`.
- [ ] 3.2 `otelhttp` server middleware on the webhook server mux only
      (not the metrics/health server) — HMAC 401s and every other
      status become measured for the first time.
- [ ] 3.3 `otelhttp` client transport wrapping the installation
      transport, **outermost** (above the rate-limit transport —
      the IMPL-0022 ordering contract; whichever lands second adds
      the ordering test).
- [ ] 3.4 `redisotel` metrics on both Valkey clients (queue,
      scheduler) — reaper Lua and leader SETNX command latency
      included.
- [ ] 3.5 `otelpgx` on the pgxpool; verify whether its pool-stats
      option covers `pgxpool.Stat()` — use it if yes, add the small
      Stat collector if no. Exactly one pool-stats source ships;
      record the verification outcome in the PR.
- [ ] 3.6 Cardinality audit of the bridge output (no `url.path` on
      server metrics, bounded label sets; drop attributes via views
      if needed) + document the one-source-per-panel dedup rule in
      `docs/operations/scaling.md`.
- [ ] 3.7 Tests: `/metrics` serves semconv series alongside
      `repo_guardian_*` in one registry; a bad-HMAC webhook request
      increments the server duration histogram with a 401 label.

#### Success Criteria

- Inbound HTTP, GitHub client, Valkey, and Postgres all have RED/USE
  series on the existing endpoint with no collector deployed.
- The Finding F hand-rolled collector list is cancelled or
  consciously superseded (3.5's one-source decision recorded).
- `make ci` passes.

---

### Phase 4: Compliance snapshots and the report CLI

#### Tasks

- [ ] 4.1 `compliance-snapshot` leader-gated handler
      (`COMPLIANCE_SNAPSHOT_INTERVAL`, default 24h):
      `INSERT ... SELECT` from the posture query via
      `Store.InsertComplianceSnapshot`.
- [ ] 4.2 `repo-guardian report` subcommand (CLI dispatch per Open
      Question 1): loads `guardian.hcl`, queries `rule_state` +
      `compliance_snapshot`, renders one markdown file per org to
      `--out` (OQ6 → a) — compliance % per rule, trend vs previous
      snapshot, and the repo/rule/missing-since table; open-PR links
      fetched live via the installation client.
- [ ] 4.3 Golden-file tests for report rendering; trend test across
      two synthetic snapshots; empty-org and zero-snapshot edge
      cases.
- [ ] 4.4 Operator doc: generating and distributing reports; snapshot
      cadence and no-retention rationale (~120 rows/day at target
      scale).

#### Success Criteria

- A generated report for a seeded org matches DB truth exactly
  (identity, since-dates, percentages).
- Snapshot rows accumulate at the configured cadence on the leader
  only.
- `make ci` passes.

---

### Phase 5: Monitoring generator foundation

#### Tasks

- [ ] 5.1 `repo-guardian monitoring generate` subcommand skeleton
      (CLI dispatch per Open Question 1): `policy.Load` → generation
      model (orgs from `scope`, enabled rules and kinds, attached
      reconcilers, mechanisms in use); flags `--config`, `--out`,
      `--format json|k8s`.
- [ ] 5.2 grafana-foundation-sdk dependency pinned to the Grafana-13
      cohort (OQ3 — Grafana ≥ 13 is the supported floor) + the
      panel-library package in `internal/monitoring/` (OQ2); render
      test against the 13 schema; measure the binary size delta
      (design OQ5's escape hatch to a separate `cmd/` if egregious).
- [ ] 5.3 PrometheusRule generation: existing starter alerts
      re-authored as generator output, mechanism-scoped (no
      `PropertySchemaMissing` without a `custom_properties`
      reconciler), windows per INV-0012 findings C/E; includes
      `RepoGuardianPostureExportStalled` and — once the metrics
      exist — the IMPL-0022 queue alerts. **New since the merge:**
      `RepoGuardianRepoAccessDenied` (INV-0015) joins the starter set
      to re-author, and it carries a load-bearing label selector —
      `repos_parked_total{reason="access_denied"}`, because the same
      metric also counts routine archived/fork parks. Any generator
      that emits the alert without the selector pages on every normal
      onboarding sweep.
- [ ] 5.4 `--format k8s`: grafana-operator `GrafanaDashboard` CRs
      (design OQ7 → b) + a `PrometheusRule` CR; `--format json` emits
      plain files. Datasources are concrete UIDs defaulting to
      `prometheus` and `loki`, overridable via `--prometheus-uid` /
      `--loki-uid` (OQ4); the static tier carries the defaults.
- [ ] 5.5 CI drift gate: regenerate the static tier from the default
      config in `ci.yml`, fail on diff (the helm-docs convention);
      wire into the `changes` paths-filter matrix. **Reuse what
      landed:** `make lint-alerts` now has a `lint-alerts-chart` half
      that renders the chart, extracts `spec.groups` with `yq`, and
      runs `promtool check rules` — with an explicit guard against the
      vacuous pass, since promtool exits 0 on "0 rules found".
      Generated PrometheusRules should go through the same shape rather
      than a new one, and the `alerts` paths-filter already covers the
      Makefile. Note helm-unittest cannot substitute: it passes 105/105
      against syntactically invalid PromQL.
- [ ] 5.6 Document generator-as-validation: a failing `policy.Load`
      fails generation, so running it in CI is a free config check
      (strict-templates precedent).

#### Success Criteria

- `monitoring generate` against `examples/guardian-enterprise.hcl`
  emits dashboards with a row for every configured org (including
  silent ones) and an alert manifest containing only
  configured-mechanism alerts.
- Hand-editing a generated artifact turns CI red.
- `make ci` passes.

---

### Phase 6: Dashboard suite content

All dashboards authored in the Phase 5 panel library — no
hand-written dashboard JSON anywhere.

#### Tasks

- [ ] 6.1 E1 KPI dashboard — business-tier sources only (INV-0013
      Finding I): compliance % from the Phase 2 gauges,
      `open_prs_by_rule` ages, convergence rate, error budget,
      rate-limit headroom, queue depth, leader status.
- [ ] 6.2 E2 detailed dashboard — fleet aggregate section
      (`sum without (org)`) + per-org rows generated per configured
      org; `installation_info` `group_left` joins for
      `installation_id`-keyed series; queue/store/scheduler metrics
      deliberately absent (they are E3's). **New since the merge:** a
      parked-repo panel from `repos_parked_total{org, reason}` —
      `sum by (reason)` over 24h. It belongs on E2 rather than E3
      because it is fleet composition, not system health, and it is the
      denominator context for the posture numbers: a fleet whose
      tracked count drops needs the archived/fork series next to it to
      distinguish "repos left the fleet" from "the exporter broke".
- [ ] 6.3 E3 system dashboard — service + infra tiers: OTEL semconv
      HTTP server/client, redisotel, pgx pool + `store_query_seconds`,
      queue USE (including IMPL-0022's series when present), go
      runtime/process, the three orphan histograms
      (`scheduler_sweep_batch_size`,
      `store_writeback_duration_seconds`,
      `discovery_duration_seconds`), posture-exporter health.
- [ ] 6.4 E4 Loki dashboard (error rate by component, sweep
      summaries, write-back failures, webhook rejections, top
      erroring repos as log fields) + Loki-ruler recording-rule
      examples in `contrib/`.
- [ ] 6.5 Test-lock the catalog-parse log line ("catalog-info parse
      failed; skipping reconcile to avoid clearing properties" —
      exact message + keys, the same contract style as
      `TestAPIMode_FiltersUndefinedMappedProperty`) before E4
      references it.
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
- Drift gate green on the committed static tier; `make ci` passes.

---

### Phase 7: Cleanup, deprecations, docs, chart

#### Tasks

- [ ] 7.1 Remove the four legacy counters
      (`properties_checked_total`, `properties_prs_created_total`,
      `properties_set_total`, `properties_already_correct_total`;
      OQ4 → a) with migration notes in `contrib/README.md`.
- [ ] 7.2 `docs/operations/scaling.md`: posture architecture,
      exporter leader semantics, OTEL series catalog, dedup rule.
- [ ] 7.3 Chart: version + appVersion bump, new values documented via
      `README.md.gotmpl` (never the rendered README), prometheusrule
      parity with the generator output.
- [ ] 7.4 CLAUDE.md: posture-state contract (write-back best-effort,
      leader-scoped export, taxonomy pointer) and the
      transport-ordering contract shared with IMPL-0022. Also the
      nil-vs-empty `*CheckResult` rule and its interaction with
      INV-0015 parking — `Pool.park` passes an empty result for
      archived/fork and nil for access-denied, and getting that
      backwards is silently wrong in both directions. CLAUDE.md already
      carries the parking entry; this extends it rather than adding a
      second one.
- [ ] 7.5 Flip INV-0013 to Concluded and DESIGN-0022 to Implemented;
      `docz update design inv impl`; mkdocs stays at the 14-warning
      baseline.

#### Success Criteria

- No dangling references to removed counters in code, chart,
  contrib, or docs.
- Chart renders and installs with the new values; helm-unittest
  passes.
- `make ci` passes.

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/store/postgres/migrations/0002_*.sql` | Create | `rule_state`, `compliance_snapshot`, `catalog_parse_ok` column |
| `internal/store/store.go` + `postgres/postgres.go` | Modify | `RuleState`, `UpsertRuleStates`, posture queries, snapshot insert |
| `internal/checker/engine.go` | Modify | `CheckRepo` returns `*CheckResult` |
| `internal/worker/worker.go` | Modify | write-back persists `CheckResult` |
| `internal/checker/` (new file) | Create | `posture-export` + `compliance-snapshot` schedule handlers |
| `internal/reconciler/custom_properties.go` | Modify | `property_schema_missing` gauge at cache refresh |
| `internal/metrics/metrics.go` | Modify | P2 gauges + export health; P7 removes 4 legacy counters |
| `cmd/repo-guardian/main.go` | Modify | subcommand dispatch, OTEL SDK setup, handler wiring |
| `internal/github/client.go` / transport wiring | Modify | `installation_info` gauge, otelhttp client transport |
| `internal/monitoring/` (or per OQ2) | Create | generation model + foundation-sdk panel library |
| `internal/report/` | Create | report rendering |
| `internal/config/config.go` | Modify | `POSTURE_EXPORT_INTERVAL`, `COMPLIANCE_SNAPSHOT_INTERVAL` |
| `charts/repo-guardian/` values + schema + tests | Modify | new intervals; P7 bump |
| `contrib/grafana/` | Replace | four generated dashboards replace the 61-panel legacy |
| `contrib/prometheus/alerts.yaml` | Modify | generator-authored parity |
| `.github/workflows/ci.yml` | Modify | drift-gate job in the paths-filter matrix |

## Testing Plan

- [x] Migration 0002 up/down integration test (real Postgres,
      `integration` tag).
- [x] `actionable_since` transition table-driven tests + delete-not-in
      + 16-goroutine upsert race.
- [ ] Exporter leader-gating and stale-series reset tests (non-vacuity:
      skip the `Reset()`, confirm failure, restore).
- [ ] Mock fidelity: `UpsertRuleStates` → `ActionableCounts` is
      list-then-act — fakes must reflect prior writes (CLAUDE.md
      rule) or exporter tests are vacuous.
- [ ] `/metrics` bridge coexistence test + bad-HMAC 401 measurement
      test.
- [ ] Report golden files; generator render tests against the pinned
      Grafana schema; zero-mechanism fixture emits zero mechanism
      panels/alerts.
- [ ] Catalog-parse log-line contract test (message + keys).
- [ ] helm-unittest for new chart values; CI drift gate on the static
      tier.
- [ ] Each behavioural test neutralise-verify-restore per standing
      practice.

## Dependencies

- DESIGN-0022 (all OQs resolved 2026-08-02) — this plan tracks it
  1:1.
- IMPL-0022 (DESIGN-0021): no ordering requirement except the shared
  transport-ordering test and E3/alert content for its queue metrics
  (generator scoping absorbs the gap).
- grafana-foundation-sdk pinned to the Grafana-13 cohort; Grafana
  ≥ 13 is the supported floor for the generated suite (OQ3);
  grafana-operator present in the cluster for `--format k8s` output
  (design OQ7 → b).

## Open Questions

1. **CLI subcommand dispatch.**
   **Resolved 2026-08-02 → (a).**
   (a) Stdlib `flag.FlagSet` per subcommand with an `os.Args[1]`
   switch in `main()` — `run()` keeps today's flat flags for the
   server path (zero behaviour change for existing deployments), and
   `monitoring` / `report` each get their own FlagSet. Two
   subcommands don't justify a framework dependency, and the repo's
   12-factor/minimal-dep posture holds.
   (b) `spf13/cobra` — nicer help/completion and room to grow, at the
   cost of a dependency tree and restructuring `main()` for a binary
   that is 95% a server.
   (c) A separate tiny `cmd/` binary per tool instead of subcommands
   — contradicts the resolved design OQ5 (a) unless the size delta
   forces it.
   other:

2. **Panel-library package location.**
   **Resolved 2026-08-02 → (a).**
   (a) `internal/monitoring/` — importable by the generator
   subcommand, the render tests, and any future operator/sidecar;
   keeps `cmd/` thin per house convention (`main.go` is already
   coverage-ignored, so testable logic should not live there).
   (b) Package under `cmd/repo-guardian/` — co-located with the only
   caller, but untestable-by-convention and unusable if OQ5's
   escape hatch to a separate binary ever fires.
   other:

3. **Grafana schema/version pin for the foundation SDK.**
   **Resolved 2026-08-02 → other: Grafana 13+ only.** Pin the SDK's
   Grafana-13 cohort; Grafana ≥ 13 is the supported floor for the
   generated suite (record it in the panel-library doc comment,
   `contrib/README.md`, and the generator's `--help`). Render tests
   run against the 13 schema. The renovate packageRule against
   transitive cohort jumps from option (a) still applies — cohort
   bumps are deliberate, render-tested upgrades.
   (a) Pin the SDK cohort matching the deployed homelab Grafana major
   (verify the running version at task 5.2 time; currently the
   kube-prometheus-stack default, Grafana 11.x) and record it in the
   panel library's doc comment; bump deliberately with a render test,
   never transitively via renovate (add a packageRule if renovate
   proposes cohort jumps).
   (b) Always track the latest SDK cohort — newest panel features,
   but generated JSON may outrun the deployed Grafana and fail
   import silently.
   other:

4. **Datasource references in generated dashboards.**
   **Resolved 2026-08-02 → other: concrete datasource UIDs,
   defaulting to `prometheus` and `loki`.** Template-variable
   datasources were rejected from operator experience (the
   bind-at-import prompt is the annoyance, recently re-confirmed).
   The generator emits real UIDs, defaulting to `prometheus` and
   `loki`, overridable via `--prometheus-uid` / `--loki-uid` flags —
   zero-flag runs work on conventional kube-prometheus-stack /
   grafana-operator setups, and the committed static tier carries the
   defaults.
   (a) Template-variable datasources (`${DS_PROMETHEUS}`,
   `${DS_LOKI}`) declared per dashboard — artifacts stay portable
   across clusters and the operator binds them at import/CR-apply
   time; the contrib static tier works anywhere.
   (b) Hardcode the datasource UIDs via generator flags
   (`--prometheus-uid`, `--loki-uid`) — zero-click imports on the
   known cluster, but every regeneration is cluster-specific and the
   static tier stops being portable.
   (c) Both: variables by default, optional flags to burn in UIDs —
   more surface, only worth it if (a) proves annoying in ArgoCD.
   other:

5. **Release labeling across the per-phase PRs.**
   **Resolved 2026-08-02 → (a).**
   (a) `dont-release` on the Phase 1 PR, `minor` on the Phase 2 PR
   (cutting the schema+exporter release), `minor` on Phase 3,
   `dont-release` on Phases 4–6 PRs where they are dashboard/contrib
   heavy with `minor` on whichever PR carries the snapshot/report
   binary changes, `minor` on Phase 7 (the counter removal). Matches
   the design's rollout checkpoints; chart version bumps stay manual
   per house rule.
   (b) `minor` on every phase PR — more releases, each independently
   shippable, but ships a schema with no exporter and subcommands
   with no dashboards.
   other:

## References

- [DESIGN-0022](../design/0022-compliance-posture-state-dashboard-suite-and-otel-first.md)
  — the design; all OQs resolved 2026-08-02
- [INV-0013](../investigation/0013-state-vs-event-metrics-dashboard-suite-and-system-observability.md)
  — findings A–I; the Finding I taxonomy fixes every panel's source
- [INV-0012](../investigation/0012-inert-budgettracker-and-untrustworthy-alert-pack.md)
  — alert-window rules (findings C/E) applied to generated alerts
- [IMPL-0022](0022-delayed-requeue-job-contract-and-rate-limit-consolidation.md)
  — shared transport-ordering contract; queue metrics consumed by E3
- [IMPL-0015](0015-stale-sweep-cutover-and-repository-discovery.md)
  — the write-back best-effort contract Phase 1 extends
- [IMPL-0013](0013-reconcile-open-prs-when-file-rules-become-satisfied.md)
  — `ResetOpenPRsByRule` snapshot-gauge precedent for the exporter
- grafana-foundation-sdk: <https://github.com/grafana/grafana-foundation-sdk>
- grafana-operator: <https://github.com/grafana/grafana-operator>
- OTel Go prometheus bridge:
  <https://pkg.go.dev/go.opentelemetry.io/otel/exporters/prometheus>
