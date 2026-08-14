---
id: IMPL-0023
title: "Compliance posture state, dashboard suite, and OTEL-first observability"
status: In Progress
author: Donald Gifford
created: 2026-08-02
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0023: Compliance posture state, dashboard suite, and OTEL-first observability

**Status:** In Progress
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
- [x] 2.4 Exporter health: `posture_export_total{outcome}` +
      `posture_export_duration_seconds`. The counter is the liveness
      signal for every posture gauge, which have no other heartbeat: a
      leader whose store reads all fail keeps serving its last
      successful values indefinitely, so "the fleet is stable" and
      "nothing has updated in six hours" look identical from a
      dashboard. `RepoGuardianPostureExportStalled` (task 5.3) must
      therefore watch the absence of `outcome="ok"` increments, not the
      gauges. Duration is observed on the failure path too — a store
      read that times out is the most useful sample the histogram can
      hold, and skipping it leaves the p99 looking healthy exactly when
      it is not. Buckets run to 60s because that is the tick interval;
      past it the exporter is permanently behind.
- [x] 2.5 Config `POSTURE_EXPORT_INTERVAL` +
      `values.yaml` / `values.schema.json` + helm-unittest cases.
      Chart key is `posture.exportInterval`. The default lives in
      `internal/config` rather than `internal/checker`, so config stays
      a leaf package and the exporter reads its interval from `Config`
      like every other scheduled handler. Both the default AND the
      override are asserted in helm-unittest: the binary defaults the
      var on its own, so a chart that silently stopped emitting it
      would keep working and the drift would only surface when an
      operator set the value and nothing happened.
- [x] 2.6 Tests: only the leader emits (two schedulers, one series
      source); a removed org/rule stops emitting after the next tick
      (non-vacuity: skip the `Reset()`, watch it fail); gauge values
      equal SQL truth. Unit tests in `posture_test.go`; the two the
      unit tests structurally cannot cover live in
      `posture_integration_test.go` — leader gating against real
      Valkey, and gauges against real SQL (with a fake store the
      exporter publishes what it is handed *by construction*, so only
      Postgres can say whether the numbers are right). Both verified by
      neutralising the thing under test.

      **Gotcha worth remembering:** `GaugeVec.WithLabelValues`
      INSTANTIATES the child at zero as a side effect, so reading an
      absent series to prove it is absent creates it. A
      `CollectAndCount` taken after such a read measures the test's own
      footprint, not the exporter's output — this cost a failing
      assertion here. Count series BEFORE asserting individual values.
- [x] 2.7 `contrib/README.md` rows for all Phase 2 metrics, as a
      §Compliance posture section: the three posture gauges, the two
      health signals, `installation_info`, and `property_schema_missing`.

      Three operator-facing contracts are recorded there because they
      are not inferable from the metric names: aggregate with
      `max by (...)` and never `sum` (during failover two pods can
      briefly both hold series, and a demoted pod keeps whatever it last
      published); `posture_export_total{outcome="ok"}` is the only
      heartbeat the gauges have; and the per-rule ratio understates a
      scoped rule (the 2.2 follow-up, now written down where the queries
      are). Also stated explicitly that **"repos failing at least one
      rule" is not derivable** from these series — the per-rule counts
      overlap by an unknown amount, so summing over-counts and taking
      the max under-counts. A draft example claiming to compute it was
      caught and removed; if a panel needs that number in Phase 6 it
      needs a new aggregate, not a clever query.

      Two accuracy defects in the same catalog fixed in passing:
      `scheduler_is_leader` has carried a `pod` label since IMPL-0011
      and the table listed only `name`, and `repos_parked_total`
      (INV-0015) had no row at all — it belongs beside
      `repos_unmeasurable` as the event counterpart to that standing
      population, which is the cross-check 2.2 called for. Every PromQL
      example validated through `promtool check rules`.

      Still stale and deliberately left: the §BudgetTracker table
      documents six metrics deleted in IMPL-0022 Phase 6. Removing it is
      task 7.1's success criterion ("no dangling references to removed
      counters in ... contrib").

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

- [x] 3.1 Dependencies + SDK setup: MeterProvider with the
      `go.opentelemetry.io/otel/exporters/prometheus` bridge registered
      into the default registry `promhttp` already serves; no-op
      TracerProvider; honour `OTEL_SDK_DISABLED`.

      **Divergence: the bootstrap is `internal/observability`, not
      `cmd/repo-guardian/main.go`.** main.go is coverage-ignored
      (CLAUDE.md), so putting it there would have made task 3.7's
      coexistence test unable to reach the code it is meant to prove.
      main.go keeps only the call and the shutdown defer.

      **`OTEL_SDK_DISABLED` is not implemented by the Go SDK.** The
      design called it "the standard escape hatch", which is true of the
      specification and of other language SDKs, but opentelemetry-go
      does not read it — grepping every vendored `go.opentelemetry.io`
      module for the string matches nothing. An operator setting it and
      assuming it took effect would have been wrong, so this package
      implements it: no-op providers, exporter never registered. An
      unparseable value fails startup rather than defaulting, because
      the two ways to guess are not symmetric — guessing enabled ignores
      someone turning telemetry off, guessing disabled silently blinds a
      deployment.

      **Instrumentation wraps unconditionally; only the provider
      changes.** The alternative was an `if enabled` branch at four call
      sites in four packages, where the cost of getting one wrong is a
      boundary that is silently unmeasured forever. `Shutdown` is a
      no-op rather than nil in the disabled state so main.go needs no
      branch either.

      **Ordering constraint, load-bearing:** the bootstrap runs before
      `newGitHubClient`/`newQueue`/`newStore`. otelhttp, redisotel and
      otelpgx each capture `otel.GetMeterProvider()` at construction
      time, so installing the provider after them leaves those call
      sites permanently attached to the no-op default — with no error
      and no missing-series alert to notice it by.

      Bridge options: only `WithoutTargetInfo()`. `target_info` restates
      per-pod-static resource attributes that Prometheus already covers
      with job/instance from the scrape config.

      **Handed to 3.6 with evidence:** the bridge stamps THREE scope
      labels on every semconv series — `otel_scope_name`,
      `otel_scope_version`, `otel_scope_schema_url` — and the latter two
      are empty strings. Checked whether `WithoutScopeInfo()` is safe by
      enumerating the metric names of all four instrumentations:
      redisotel emits `db.client.connections.*` / `redis.dial`, otelpgx
      emits `db.client.operation.*` / `pgxpool.*`, and the two otelhttp
      directions differ by `http.server.*` vs `http.client.*`. Nothing
      collides, so the scope labels are not acting as a discriminator
      and dropping them is safe — but it is 3.6's call to make with the
      rest of the cardinality audit, not a decision to slip in here.

      Also noted, not fixed: `.goreleaser.yml` sets no ldflags and there
      is no version variable anywhere in the binary, so `service.version`
      is left unset rather than given a placeholder. Stamping it is a
      release-pipeline change, out of scope for this phase.

      **Two defects found by a post-3.3 architecture review and fixed in
      place:**

      1. *The resource merge was a latent startup crash.*
         `resource.Merge` refuses two resources with different schema
         URLs, and `resource.Default()` carries whichever schema the SDK
         release was built against — 1.43.0 today, which matched the
         imported semconv package by coincidence, not by construction.
         The next SDK bump that moves `Default()` forward would have
         made `New` return "conflicting Schema URL" and `main.go` abort:
         a crash-looping pod after a routine renovate PR, with the cause
         being two version numbers in different modules. Reproduced
         directly (merging against a 1.99.0 schema errors), then fixed
         with `resource.NewSchemaless`, which merges with anything. Cost
         is a schema URL on two attributes stable since semconv 1.0.
      2. *`OTEL_SDK_DISABLED` parsing failed startup, against spec.* The
         original reasoning here — "the two ways to guess are not
         symmetric" — was simply wrong: the spec's fallback is the
         DEFAULT, and the default is enabled, so warning and continuing
         leaves the deployment fully observable rather than blind. There
         was never a blinding risk to trade an outage for. Now warns and
         stays enabled, per the OTel env-var specification.

      A third review finding hardened the coexistence test rather than
      the code: the bridge registers as an **unchecked collector** (its
      `Describe` is a deliberate no-op), so Prometheus does no
      descriptor-collision check at registration — a clashing metric
      name produces no panic and no error there. It surfaces at scrape
      time, where `promhttp.HandlerOpts{}` defaults to
      `HTTPErrorOnError` and one bad series **500s the entire endpoint**,
      taking all 56 `repo_guardian_*` metrics with it. The test now
      asserts `reg.Gather()` returns no error, because a substring check
      alone would have been reading an error page.
- [x] 3.2 `otelhttp` server middleware on the webhook **route** only —
      HMAC 401s and every other status become measured for the first
      time. Verified end-to-end against the real `webhook.NewHandler`
      rather than a stub: a stub would only prove otelhttp works, which
      is upstream's test.

      **Divergence: the route, not the mux.** `healthz`/`readyz` live on
      the same server as the webhook (only `/metrics` is on the other
      one), so wrapping the mux would have instrumented kubelet probes —
      constant traffic answering no question anyone asks, burying the
      webhook signal it sits beside. Wrapping at the route registration
      also puts the middleware OUTSIDE the IP allowlist, so 403s are
      measured alongside 401s.

      **`otelhttp.WithRouteTag` no longer exists** (removed by v0.70.0).
      `http.route` now comes from `http.Request.Pattern`, which net/http
      populates when a Go 1.22-pattern ServeMux routes the request — so
      the tests must route through a real mux or `http.route` is silently
      empty and they measure something production never does. The
      attribute is the pattern's PATH only: registering
      `POST /webhooks/github` yields `http_route="/webhooks/github"`.

      **Cardinality finding, handed to 3.6 with a reproduction:** the
      emitted attribute set is method / route / status / protocol /
      scheme / **server.address** / **server.port**, and the last two
      come from the request Host header — which is client-controlled on
      an endpoint anyone who finds the hostname can POST to. `url.path`
      is correctly absent, so the route keying is right, but boundedness
      is NOT yet a property of the whole set and no test here claims it
      is. Closing it needs a view dropping those two attributes, which
      is 3.6's shape of work, not a wrapper change.
- [x] 3.3 `otelhttp` client transport wrapping the installation
      transport, **outermost** (above the rate-limit transport — the
      IMPL-0022 ordering contract). OTEL landed second, so the ordering
      test is here: `internal/github/transport_order_test.go`.

      All three constructors (`NewClient*`, `CreateInstallationClient`,
      `NewClientForBaseURL`) funnel through one `instrumentedClient`
      helper so the ordering cannot drift between the App client, the
      installation client, and the base-URL client tests drive.

      **The test discriminates on sample COUNT, not on attributes.** The
      rate-limit transport does not send once remaining is under the
      reserve — it returns `*ThrottledError` in place of a response. With
      otelhttp on top that refusal is still an attempt and lands in the
      client histogram; underneath, the request never reaches otelhttp
      and the deferral is not measured at all. So two requests are
      issued, one sent and one refused, and both must be measured.
      Verified by actually inverting the chain: the count drops to 1 and
      the test names the inversion in its failure message.

      Consequence worth stating plainly, since it is the reason the
      order matters: with otelhttp underneath, a throttled installation
      produces NO client metrics, so a rate-limited period looks
      identical to an idle one — the exact failure the IMPL-0022
      deferral work exists to make visible.

      The fixture uses remaining=20 (under the reserve) rather than
      remaining=0 deliberately: zero trips go-github's internal
      pre-check, which short-circuits above our transport AND above
      otelhttp, exercising a different path than the one under test.
- [x] 3.4 `redisotel` metrics on the Valkey client — reaper Lua and
      leader SETNX command latency included.

      **Correction to the task text: there is only ONE Valkey client.**
      `newScheduler` takes the queue's `*redis.Client` (main.go, an
      IMPL-0011 Phase 4 decision), so queue and scheduler share it and
      one `InstrumentValkey` call covers both. Nothing was skipped; the
      plural in the plan was wrong about the code.

      Metrics only, no tracing: spans go nowhere in this build and
      redisotel's tracing carries per-command overhead that buys nothing
      until a tracing backend exists.

      **No background goroutine.** redisotel spawns one only when given
      a `WithCloseChan`, whose sole job is unregistering the metric
      callbacks. This client lives for the whole process and its
      callbacks die with the meter provider at shutdown, so passing a
      channel would create a goroutine per client whose only purpose is
      to tidy up immediately before exit. Omitted deliberately, which is
      also what keeps the "no fire-and-forget goroutines" rule satisfied
      here.

      Instrumentation failure is logged, not fatal — telemetry must
      never stop the queue coming up.

      The unit test needs no server, which is the interesting part: the
      pool instruments are asynchronous observables read from the
      client's own stats at collection time, so a client that cannot
      reach Valkey still reports its pool — exactly the state an
      operator most wants a number for. Command latency needs a live
      server and is left to the queue integration tests.
- [x] 3.5 `otelpgx` on the pgxpool.

      **Open question resolved: otelpgx covers `pgxpool.Stat()`. No
      hand-rolled Stat collector ships.** `otelpgx.RecordStats` takes an
      interface (`Stat() *pgxpool.Stat` + `Config() *pgxpool.Config`)
      that `*pgxpool.Pool` already satisfies, and the emitted series
      carry real values read from the live pool — verified by scraping
      `pgxpool_max_connections` and getting the configured cap rather
      than a zero. Exactly one pool-stats source, as required.

      Two separate wirings, both needed:

      - `otelpgx.NewTracer()` on `ConnConfig` gives per-query
        `db.client.operation.duration` and `.errors`. Worth checking
        before wiring, since spans go nowhere here: the tracer records
        METRICS as well as spans, so it is not dead weight under a no-op
        TracerProvider. It must be set BEFORE the pool is built — it is
        a config field read at connection time, so a pool constructed
        first would never consult it and query metrics would be silently
        absent with no error anywhere. Pinned by a test.
      - `otelpgx.RecordStats(pool)` for pool statistics, registered as
        an asynchronous callback rather than a goroutine.

      **Naming surprise worth recording for Phase 6:** the pool series
      are `pgxpool_*` (`pgxpool_max_connections`,
      `pgxpool_acquire_duration_nanoseconds_total`, …), NOT the
      `db.client.connection.*` semconv family. Dashboard panels written
      against the semconv names would match nothing. Found by asserting
      the semconv name and watching the test fail.

      Complementary to `store_query_seconds{op}`, not redundant with it,
      and the DESIGN-0022 dedup rule decides which is authoritative:
      the domain metric knows the difference between `StaleRepos` and
      `UpsertIfMissing` where semconv sees only SQL verbs, so it stays
      authoritative for *which* operation is slow; semconv separates
      pool-acquire wait from execution time, so it is authoritative for
      *why*.

      Instrumentation failure is logged, not fatal — an unmeasured store
      beats a pod that will not start because its telemetry did not
      register.
- [x] 3.6 Cardinality audit of the bridge output + the
      one-source-per-panel dedup rule in `docs/operations/scaling.md`
      (with the full OTEL series catalog).

      **No views were needed.** The task text anticipated dropping
      attributes via SDK views; both real problems turned out to have
      option-level fixes, which are cheaper and closer to the cause.

      Audit outcome, three findings:

      1. **`server.address` / `server.port` were remotely triggerable.**
         They derive from the request `Host` header, and
         `/webhooks/github` is reachable from the internet, so a caller
         sending a distinct spoofed Host per request would mint a series
         per value across three histograms — in the same registry that
         serves every `repo_guardian_*` metric. Fixed with
         `otelhttp.WithServerName`, which pins the address and
         suppresses the port. Regression test sends three spoofed Hosts
         and asserts none reaches the metrics; without the fix all three
         do.
      2. **Scope labels churn on every dependency bump.**
         `otel_scope_version` is the *instrumentation library's*
         version, so each renovate bump of otelhttp/redisotel/otelpgx
         changes a label value on every series that library emits — old
         series stale, new series from zero, `rate()` sees a counter
         reset. Phase 6 bakes PromQL into generated dashboards behind a
         fail-on-diff gate, so this is recurring breakage. Dropped with
         `WithoutScopeInfo`, safe because the four instrumentations emit
         disjoint metric names (verified in task 3.1) so the name
         already identifies the producer.
      3. **The name-translation strategy was inherited, and the exporter
         documents its default as subject to change.** A flip would
         strip `_total`/`_seconds` from every series at once, breaking
         every generated panel, every alert in `prometheusrule.yaml`,
         and the repo's own naming convention in one dependency bump.
         Now pinned explicitly to `UnderscoreEscapingWithSuffixes`.

      `url.path` was already absent — server metrics key on `http.route`
      from the ServeMux pattern — so the original concern in the task
      text did not materialise.

      **Documented caveat that no fix can address:** go-github v68's own
      client-side pre-check short-circuits inside `BareDo`, above the
      `http.Client` entirely, once its header cache has seen
      `remaining=0`. Those calls reach no transport at any wrapping
      position, so `http_client_request_duration_seconds_count`
      under-counts attempted GitHub calls exactly when the system is
      rate-limited. scaling.md says so and points at
      `queue_delayed_total{reason="rate_limit"}` as the intended source
      for throttle volume.
- [x] 3.7 Tests: `/metrics` serves semconv series alongside
      `repo_guardian_*` in one registry
      (`TestNew_BridgeAndDomainMetricsShareOneEndpoint`); a bad-HMAC
      webhook request increments the server duration histogram with a
      401 label (`TestHandler_MeasuresRejectedWebhooks`, driven through
      the real `webhook.NewHandler` — a stub would only prove otelhttp
      works, which is upstream's test).

      Two strengthenings beyond the task text:

      - **The coexistence test asserts `Gather()` is clean**, not just
        that both families appear. The bridge is an unchecked collector
        (`Describe` is a deliberate no-op), so a name collision produces
        no panic and no error at registration; it surfaces at scrape
        time, where `promhttp.HandlerOpts{}` defaults to
        `HTTPErrorOnError` and one bad series returns 500 for the ENTIRE
        endpoint. A substring assertion alone would have been reading an
        error page and passing.
      - **A separate test exercises the DEFAULT registry**, which is the
        path main.go actually takes (nil `Registerer`). Every other test
        injects a private registry — correct for isolation, but it left
        the nil branch and the real coexistence setting untested. The
        domain metrics in that test are not synthesised: `internal/metrics`
        registers all of them at init, so it is the genuine article
        sharing a registry with the bridge. It lives alone in its own
        file with a comment saying why, since default-registry
        registration is process-global and one-shot.

#### Success Criteria

- Inbound HTTP, GitHub client, Valkey, and Postgres all have RED/USE
  series on the existing endpoint with no collector deployed.
- The Finding F hand-rolled collector list is cancelled or
  consciously superseded (3.5's one-source decision recorded).
- `make ci` passes.

**Phase 3 outcome (all criteria met).** All four boundaries export
through the bridge into the registry `/metrics` already serves — no
collector, no second scrape target, no scrape-config change. Every item
on INV-0013 Finding F's cancelled list is superseded rather than
quietly dropped: the hand-rolled GitHub RoundTripper histogram by the
otelhttp client transport (3.3), the webhook duration middleware by the
otelhttp server handler (3.2), the `redisprometheus` collector by
`redisotel` (3.4), and the `pgxpool.Stat()` collector by
`otelpgx.RecordStats` — the "one or the other, not both" question the
investigation left open, answered in 3.5 by confirming otelpgx reads
the pool directly. `make ci`, the integration suite, and `go mod tidy
-diff` are all clean.

---

### Phase 4: Compliance snapshots and the report CLI

#### Tasks

- [x] 4.1 `compliance-snapshot` leader-gated handler
      (`COMPLIANCE_SNAPSHOT_INTERVAL`, default 24h):
      `INSERT ... SELECT` via `Store.InsertComplianceSnapshot`.

      **Divergence, and it fixes a known gap: the denominator is
      PER-RULE, not the posture query's per-org `Tracked`.**
      `rule_state` holds a row for every rule actually evaluated against
      a repository, satisfied ones included, so `count(*)` grouped by
      (owner, rule) is "repos this rule applies to" and the `FILTER` is
      "repos it applies to and fails on". That is the honest ratio for a
      scoped rule, and it closes the follow-up recorded under task 2.2 —
      a rule applying to 10 of 100 repos with 5 failures reads as 50%
      here where the org-wide denominator says 5%. The posture gauges
      cannot close that without a new series; the snapshot table already
      has a per-rule row to hold it. The integration test asserts a
      2-of-4 rule and fails with `tracked:4` if the denominator reverts.

      Written as one `INSERT ... SELECT` rather than reading counts into
      Go and writing them back: the whole snapshot is then one statement
      against one MVCC view, so a worker write-back cannot land midway
      and date a mixture of two states as a single moment.

      The caller supplies the timestamp, so every row of a run shares an
      instant exactly. The report groups by `snapshot_at` to compare a
      run against the previous one — a database-side `now()` per row
      would scatter one run across microsecond-apart instants and force
      every trend query to bucket by time instead of grouping by a
      value. It also lets the tests seed a history without sleeping.

      `ON CONFLICT DO NOTHING` makes a retry after a partial failure
      safe. The handler itself does not retry: a missed snapshot leaves
      a visible, harmless gap in a daily series, whereas a retry loop
      against a database that is already struggling is neither.

      Leader-gating matters more here than for the other two handlers.
      Running posture-export everywhere merely duplicates effort;
      running this everywhere would corrupt the history, since each
      replica inserts its own rows at its own timestamp and a
      quarter-over-quarter query would then count one state N times or
      once depending on whether the clocks happened to agree.
- [x] 4.2 `repo-guardian report` subcommand (CLI dispatch per Open
      Question 1): loads `guardian.hcl`, queries `rule_state` +
      `compliance_snapshot`, renders one markdown file per org to
      `--out` (OQ6 → a) — compliance % per rule, trend vs previous
      snapshot, and the repo/rule/missing-since table; open-PR links
      fetched live via the installation client.

      **It fixed a live bug on the way in.** `dispatch` deliberately
      does NOT `flag.Parse()` before the subcommand switch, because
      `flag.CommandLine` stops at the first non-flag argument. Parsing
      first would swallow the subcommand name and leave its flags in an
      unread tail — which is not hypothetical: before this switch
      existed, `repo-guardian report --out ./x` silently started the
      HTTP server, because nothing inspected `flag.Args()`. Anything
      that is not a known subcommand still falls through to the server,
      so `repo-guardian` and `repo-guardian --strict-templates` behave
      exactly as before. Consequence of that same rule: `--help` and
      `-h` start with a dash and so never reach the switch, so `run()`
      prepends the subcommand banner to `flag.CommandLine.Usage` —
      otherwise the report subcommand would be invisible to the one
      thing a user actually types.

      **Divergence — PR links are opt-in behind `--with-pr-links`, and
      the report package never imports `internal/github`.** Enrichment
      goes through a `report.PRLinker` interface the CLI satisfies with
      a `GitHubLinker`, so the golden tests in 4.3 need no client, no
      credentials and no network. Live fetching is a per-org
      installation lookup plus a list call, which is the one part of
      this command that can be slow, rate-limited or 403 — making it a
      flag means the common case (`report --out ./x`) is pure database
      work. A failing installation is memoised so one broken org costs
      one API error, not one per repo.

      **Divergence — `runReport` calls neither `config.Load()` nor
      `pgstore.Migrate`.** `config.Load()` demands a webhook secret and
      a Valkey DSN, neither of which a read-only report needs, and
      failing a report because the queue is unconfigured would be
      absurd; the DSN comes from `--dsn` or `STORE_DSN` directly.
      Skipping `Migrate` is the more important half: a CLI run by an
      operator from a newer binary would otherwise migrate the schema
      out from under a running older server.

      `filename()` REJECTS an org name containing a path separator
      rather than sanitizing it. Sanitizing is the tempting choice and
      the wrong one — it can collide two distinct orgs onto one file
      and silently overwrite one report with the other. GitHub org
      names cannot contain a separator, so the rejection path is
      unreachable in practice and exists to stay that way.

      `github.PullRequest` grew an `HTMLURL` field, populated at both
      literal sites. The field did not exist, and synthesizing
      `github.com/owner/repo/pull/N` would hardcode the provider into
      a struct that DESIGN-0017's GitLab backend is meant to reuse.
- [x] 4.3 Golden-file tests for report rendering; trend test across
      two synthetic snapshots; empty-org and zero-snapshot edge
      cases.

      Ten golden fixtures under `internal/report/testdata/`,
      regenerated with `go test ./internal/report -update`. The flag's
      doc comment says to READ the diff after regenerating: a golden
      file's whole value is that an unintended change to wording or
      column layout shows up for review, and regenerating without
      reading turns the suite into a slow way of asserting that the
      code equals itself.

      **Beyond the listed scope, because the listed cases would have
      missed them:** a rule present only in the previous snapshot
      (`retired-rule`) must NOT appear — it stopped being evaluated, so
      a percentage for it describes a measurement nobody took today —
      and a rule present only in Current renders "new", never 0. Also
      pinned: the two link shapes (a nil linker omits the PR column
      entirely, since a column of dashes is indistinguishable from "no
      repository has an open PR"), the per-repository link-call budget
      including the failure path, per-rule comparison dates, pipe/newline
      escaping in cells, and the 0640/0750 output modes.

      **A vacuous fixture was caught and fixed here, exactly the
      IMPL-0013 P4 failure mode.** The case named "floors rather than
      rounds" originally used 999-of-1000 — which is 99.9% under BOTH
      rules, so it pinned nothing despite its name. Verified by
      swapping the floor for `math.Round` and watching it pass. Now
      1999-of-2000 (99.95%), which floors to 99.9% and rounds to
      100.0%; under the same swap it fails, along with `second_org`
      (66.6% vs 66.7%) and the `repeating_decimal` unit case. The
      per-repository link cache was neutralised the same way and takes
      three tests plus one golden with it.

      Reading the generated fixtures also found a live wording defect:
      the "no previous snapshot exists" note rendered even for an org
      with no rules evaluated at all, where trends are not the reason
      the table is empty. The template now gates that note on `.Rules`.
- [x] 4.4 Operator doc: generating and distributing reports; snapshot
      cadence and no-retention rationale (~120 rows/day at target
      scale).

      `docs/operations/compliance-reports.md`, wired into the mkdocs
      nav; strict build still aborts at the documented 14-warning
      baseline, none of them from this file.

      **Writing it found and fixed a live defect.** `initLogger` writes
      to stdout — correct and load-bearing for the server, wrong for a
      subcommand whose stdout carries a pipeable path list, since a
      JSON log record interleaved into it makes `report | xargs` treat
      a log line as a filename. `report.go`'s comment already claimed
      "the logger writes to stderr", so the code contradicted itself.
      Split into `initLogger` (server, stdout — unchanged) and
      `initLoggerTo(w, level)`, with `runReport` taking stderr.
      `TestInitLogger_UsesStdout` swaps the real `os.Stdout` rather
      than trusting the argument, because a "CLIs log to stderr"
      refactor would otherwise silently empty every operator's log
      pipeline.

      A drafted claim that an em dash in "Failing since" means "already
      failing when rule_state first recorded it" was **wrong** and was
      removed: `upsertRuleStateQuery` stamps `now()` on every
      false→true edge including the first insert, so a NULL against
      `actionable = true` cannot come from this binary at all. The doc
      now says so.

      Known gap recorded rather than papered over:
      `COMPLIANCE_SNAPSHOT_INTERVAL` has no first-class chart value
      yet, so the doc gives the `extraEnv` workaround and points at
      task 7.3, which owns the chart bump.

#### Success Criteria

- [x] A generated report for a seeded org matches DB truth exactly
      (identity, since-dates, percentages).

      `TestReport_MatchesDatabaseTruth` in
      `internal/store/postgres/report_render_integration_test.go`.
      **This test exists because neither existing half proved it:** the
      golden tests pin the format against hand-built view models and
      the store integration tests pin the queries against a real
      database, so each supplies the other's input and a seam defect —
      a renderer reading the wrong field, a query whose column order
      drifted — passes both. Seeds 4 repos (3 failing) plus a snapshot
      at 4 failing, then asserts the rendered markdown: `3 | 4 | 25.0%`,
      `1 fewer since <date>`, every failing repo named with the date
      Postgres stamped, the compliant one absent, and no PR column.
      `TestReport_ParkedReposAreExcludedFromTheNumbers` pins that a
      deactivated repo leaves the denominator rather than joining the
      compliant count — the flattering direction is the one that gets
      believed.
- [x] Snapshot rows accumulate at the configured cadence on the leader
      only.

      Cadence from `COMPLIANCE_SNAPSHOT_INTERVAL` (default 24h);
      leader-gating from `Scheduler.Schedule("compliance-snapshot",
      ...)`, which is the same SETNX election as `stale-sweep` and
      `posture-export`. Accumulation and idempotency are covered by
      `TestPostgresStore_InsertComplianceSnapshot_ExcludesParkedAndIsIdempotent`.
- [x] `make ci` passes.

---

### Phase 5: Monitoring generator foundation

#### Tasks

- [x] 5.1 `repo-guardian monitoring generate` subcommand skeleton
      (CLI dispatch per Open Question 1): `policy.Load` → generation
      model (orgs from `scope`, enabled rules and kinds, attached
      reconcilers, mechanisms in use); flags `--config`, `--out`,
      `--format json|k8s`.

      **Package split on the SDK line, so OQ5's escape hatch stays a
      package move.** `internal/monitoring` (model + derivation) imports
      `internal/policy` and the standard library only. Task 5.2's
      grafana-foundation-sdk goes in a `dashboard/` sub-package and 5.3's
      alert specs in `alert/`, so if the binary-size delta is egregious
      the cheap relocation is `dashboard/` alone — leaving
      `monitoring generate --format k8s` still emitting the
      PrometheusRule from the main binary, which is a genuinely useful
      degraded mode. It must NOT import `internal/checker` or
      `internal/metrics` either: the engine drags the reconcilers and
      56 `promauto` registrations into a read-only CLI (same reasoning
      already recorded on `runReport`).

      **The scope predicates moved to `internal/policy`
      (`applies.go`).** `policyScopeAllows` / `ruleScopeAllows` /
      `strictMode` were unexported helpers in `internal/checker`, which
      was fine while the engine was their only caller. A dashboard row
      is a claim about which rules the engine evaluates for an org, so a
      second copy would not fail loudly — it would render a plausible,
      wrong row. `internal/checker/scope.go` now aliases them. Same
      shape as `policy.ExtractWatchedPaths`.

      **Mechanism = sole producer of a series, and nothing else.**
      `label_sync`, `workflow_sync`, the `branch_protection` reconciler
      and content assertions are all real configurable features that
      instrument NOTHING today, so they are deliberately absent from
      the enum. Adding them for symmetry would invite a panel that is
      empty by construction — the same shape as the BudgetTracker alert
      that watched a counter with no producer for months (INV-0012 A).
      Two subtleties the plan did not mention: `custom_properties`'
      schema preflight runs in BOTH api and github-action modes, so
      `PropertySchemaMissing` gates on the reconciler, not the mode
      (gating on mode is the easy thing to get backwards and silently
      drops the alert for every api-mode deployment); and `dry_run` is
      an INVERTED mechanism that suppresses `prs_created_total`, so a
      generator that only knows how to add alerts will page a dry-run
      deployment forever.

      **Fixed a trap in `policy.Load` at the CLI boundary.** Load
      treats a missing file as "use built-in defaults" and returns a
      nil error — correct for the server, wrong here in two ways.
      `monitoring generate --config guardain.hcl` (typo) would emit the
      DEFAULT artifacts with exit 0, and that same path skips
      `Validate`, `validateStrictScope` and `compilePolicyTemplates`,
      so task 5.6's generator-as-validation property would evaporate on
      exactly the invocation that most needs it. `requireConfigExists`
      stats an explicitly-given path first; an EMPTY path stays legal
      and still means defaults, which is how the static tier is built.

      **Two gaps in DESIGN-0022 answered, both flagged for the design
      to absorb.** (1) *Legacy mode has no org list anywhere*, so "one
      row per configured org" and the silent-org signal are
      structurally impossible there — a silent org is one you never
      named, and legacy mode names none. The generator warns and falls
      back to rows discovered from series, with a repeatable `--org`
      flag as the escape hatch so a legacy operator can get the signal
      without adding a `scope` block to every rule. (2) *Scope orgs can
      be globs* (`path.Match`), so `orgs = ["myent-*"]` is legal and a
      literal row for it is undefined. `Org.Pattern` marks them and the
      CLI warns that a silent org among them is invisible.

      **`Rule.Orgs` nil vs empty is load-bearing.** nil means "every
      org" (legacy); empty means "no org", which a strict-mode rule
      scoped to nothing produces and is a real reportable condition.
      Collapsing them turns a misconfigured rule into one that looks
      universal. Pinned by neutralisation.

      **`Derive` refuses cross-kind duplicate rule names.** Uniqueness
      is validated within each kind but not across them, and every
      posture series is keyed on `rule_name` with NO kind label
      (`repos_actionable{rule_name, org}`) — so a `rule "file" "x"` and
      a `rule "setting" "x"` would merge into one number that is the
      sum of two unrelated things. That also means "panels only for
      enabled rule kinds" is not expressible as a label selector;
      `Model.RuleNames(kinds...)` materialises the names, and its
      `false` return is the instruction to omit the panel entirely
      rather than emit a matcher that matches everything.

      Determinism is enforced at construction, not at emit, because the
      5.5 drift gate diffs bytes and a gate that flaps on nothing
      trains everyone to regenerate without reading the diff.
      `Source.EnvInfluence` records which of the seven env vars that
      can change the derived model behind the config file's back were
      actually set — `CUSTOM_PROPERTIES_MODE` alone adds a whole
      reconciler and two alerts.
- [x] 5.2 grafana-foundation-sdk dependency pinned to the Grafana-13
      cohort (OQ3 — Grafana ≥ 13 is the supported floor) + the
      panel-library package in `internal/monitoring/` (OQ2); render
      test against the 13 schema; measure the binary size delta
      (design OQ5's escape hatch to a separate `cmd/` if egregious).

      **Divergence 1 — "the Grafana-13 cohort" does not exist.** The
      SDK's per-Grafana cohort branches (`v10.1.x+cog-v0.0.x` …
      `v11.6.x+cog-v0.0.x`) stop at 11.6; there is no v12 or v13
      branch. That scheme was superseded by a single consolidated
      module whose README states it is "best suited for Grafana >= 12,
      but will work with Grafana >= 10". Pinned to **`v0.0.18`**, the
      current release of that line. OQ3's "Grafana ≥ 13 floor" survives
      intact — it was always a decision about which Grafana the
      generated dashboards target, not about an SDK branch, and a
      floor of 13 sits above the SDK's stated suitability.

      **Divergence 2 — authored against dashboard schema v1, and the
      SDK marks that builder superseded.** `dashboardv2` exists, but it
      is not a drop-in: every panel package in the SDK (`timeseries`,
      `stat`, `table`, `gauge`, …) declares
      `var _ cog.Builder[dashboard.Panel]` — they build the **v1**
      Panel type. Authoring against v2 would mean abandoning the panel
      builders and hand-writing v2 element structs, which is precisely
      the hand-maintained dashboard authoring this package exists to
      remove. The consumers agree: grafana-operator's
      `GrafanaDashboard` takes classic dashboard JSON, and the static
      `contrib/` tier is imported by hand. Revisit when the panel
      packages emit v2 builders, not before; the deprecation is
      forward-looking and v1 renders fine on the ≥ 13 floor. Recorded
      as an `//nolint:staticcheck` with that reasoning at the single
      call site.

      **Binary-size delta, measured (darwin/arm64, three panel types
      reachable):**

      | build | before | after | delta |
      |---|---:|---:|---:|
      | unstripped | 43,379,586 | 44,581,298 | +1,201,712 (+2.8%) |
      | stripped (`-s -w`, goreleaser's flags) | 29,223,458 | 30,132,962 | +909,504 (+3.1%) |

      **Not egregious — OQ5's escape hatch stays closed.** Under 1 MiB
      on the artifact that actually ships. Measuring this needed a
      reachable call: a blank import measured +0 bytes because the
      linker eliminates a package whose symbols nobody uses, which is a
      trap worth naming — the first measurement said "free" and was
      meaningless. Phase 6 will add more panel types, but the packages
      are thin generated wrappers over one shared core, so most of the
      cost is already paid.

      **The escape hatch is nonetheless kept cheap.** The SDK enters
      only `internal/monitoring/dashboard`; the model package and 5.3's
      alert package stay import-clean of it, so relocating is a
      directory move that leaves `monitoring generate --format k8s`
      still emitting the PrometheusRule from the main binary.

      Panels set `noValue: "no data"` rather than rendering an empty
      series as 0 — a zero is a measurement and an absent series is the
      absence of one, and conflating them is how a dashboard reports a
      healthy fleet while the exporter is dead. Same reasoning as the
      report's `n/a` for an unmeasured rule. The org template variable
      is driven by `repos_tracked` (a gauge present for every known
      org, including compliant ones) rather than by any event counter,
      which would silently drop exactly the orgs with nothing wrong.
- [x] 5.3 PrometheusRule generation: existing starter alerts
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

      Landed as `internal/monitoring/alert/` — `alert.go` (the
      catalogue as data, `Generate`) and `render.go` (YAML projection
      to groups and to a `monitoring.coreos.com/v1` PrometheusRule).
      25 alerts across four groups. The catalogue is data rather than
      a template so "which alerts did we skip and why" is answerable
      — `Generate` returns `([]Spec, []Skip)` and every omission
      carries a reason.

      Divergences and decisions worth recording:

      1. **Two INV-0012 finding-E windows widened**, not one:
         `WebhookRejectionsHigh` 15m → 30m and `StoreQueryErrors`
         5m → 10m. Finding E says to judge window-versus-`for` per
         metric cadence rather than blanket-rewriting, and that is
         right, but `window >= for` is the conservative direction:
         it can only smooth an alert, never stop a correct one from
         firing. It is enforced uniformly by
         `TestCatalogue_WindowIsAtLeastFor`.
      2. **`Spec.Window` is a declared field**, not parsed out of the
         expression, so the invariant above is checkable without a
         PromQL parser. That makes it a hand-maintained copy of
         something already in `Expr` — exactly the shape that drifts —
         so `TestCatalogue_DeclaredWindowMatchesTheExpression` diffs
         the declaration against every literal `[...]` in the
         expression.
      3. **`NoSchedulerLeader` retargeted** from `name="sweep"` to
         `name="stale-sweep"`. The legacy full-enumeration `sweep`
         schedule was removed in IMPL-0015 Phase 0, so the contrib
         alert has been watching a series with no producer since —
         a second instance of INV-0012 finding A, found by
         re-authoring.
      4. **`PRDrift` is gated on `auto_close_pr`.** The subtle one:
         with auto-close OFF an open PR whose rules are all satisfied
         is the *designed* behaviour, so the alert would fire
         permanently on a correctly-configured deployment.
      5. **`PropertySchemaMissing` gates on the reconciler, not its
         mode** — the schema preflight runs in both `api` and
         `github-action` mode. Gating on mode silently drops the
         alert for every api-mode deployment; the mis-gating is
         probe-verified to fail the test.
      6. **`Excludes` exists because `dry_run` is inverted.** It does
         not enable a series, it suppresses `prs_created_total`, so
         every PR-shaped alert is empty by construction on a dry-run
         deployment. A generator that only knows how to *add* alerts
         gets this backwards.
      7. **`RepoAccessDenied` is deliberately unconditional** — an
         installation can lose read access at any time regardless of
         configuration — and its `{reason="access_denied"}` selector
         is pinned by its own test.
      8. **`RenderPrometheusRule` hand-builds the manifest** rather
         than importing prometheus-operator's API types, which would
         drag the whole `k8s.io/api` + apimachinery tree in for four
         fields. The namespace is stamped explicitly per the
         chart-template convention in CLAUDE.md.
      9. **`TestCatalogue_PromtoolAcceptsEveryExpression`** shells out
         to `promtool check rules` (skips when absent; mise supplies
         it). Nothing else in Go-land distinguishes valid PromQL from
         invalid: a missing paren compiles, renders, round-trips
         through YAML and passes every other test in the package. It
         renders `Catalogue()` rather than `Generate()` so
         dry-run-suppressed alerts are checked too, and guards the
         empty-render vacuous pass the same way `lint-alerts-chart`
         does.

      Every guarantee was probe-verified by neutralization: broken
      PromQL, the reverted finding-E window, the dropped
      `access_denied` selector, the mis-gated preflight, the dropped
      dry-run exclusion, the mis-gated `PRDrift`, Go-format durations,
      and the unstamped namespace each fail their test.
- [x] 5.4 `--format k8s`: grafana-operator `GrafanaDashboard` CRs
      (design OQ7 → b) + a `PrometheusRule` CR; `--format json` emits
      plain files. Datasources are concrete UIDs defaulting to
      `prometheus` and `loki`, overridable via `--prometheus-uid` /
      `--loki-uid` (OQ4); the static tier carries the defaults.

      Landed as `internal/monitoring/emit/` (`emit.go`, `k8s.go`,
      `write.go`) plus `dashboard.Suite` / `dashboard.ValidateSuite`.
      Output layout is `dashboards/<slug>.{json,yaml}` +
      `alerts/{rules,prometheusrule}.yaml`.

      Design notes and divergences:

      1. **`emit` is a third package, not a file in either half.**
         `monitoring` may not import the Grafana SDK and `alert` may
         not import `dashboard` — that separation is what makes
         DESIGN-0022 OQ5's escape hatch a directory move rather than
         an untangling — so something has to be allowed to see both.
         `emit` is the only package that does.
      2. **Pure `Generate`, thin `Write`**, the same split
         `internal/report` uses. The drift gate compares bytes, so
         determinism and ordering are testable without a temp
         directory.
      3. **`dashboard.Suite(model, ds) []Dashboard` is the whole
         Phase 6 seam.** Adding a dashboard is a line in `Suite`; the
         emitter never learns how many there are. It returns nothing
         until Phase 6 authors E1–E4, which is exactly why task 5.5's
         gate must assert a non-zero artifact count rather than
         trusting that a green run wrote something.
      4. **The slug is both a filename stem and a CR
         `metadata.name`,** so `ValidateSuite` constrains it to the
         narrower grammar (RFC 1123, ≤253 chars, no duplicates) and
         *refuses* rather than sanitizes — any sanitizing rule can map
         two slugs onto one name and the second dashboard would
         silently overwrite the first. Same call
         `report.filename` makes for org names.
      5. **`--format k8s` refuses to render a dashboard with no
         `--instance-selector`.** The CRD does not require the field
         in the sense of erroring without it: the operator simply has
         no Grafana to file the dashboard into, so the CR sits
         unreconciled forever with nothing in `kubectl get` to say so.
         Refusing beats emitting something inert.
      6. **`spec.datasources` is deliberately never emitted.** It
         remaps `${DS_X}` import placeholders, and the panel library
         bakes concrete UIDs precisely so no placeholder exists.
         Adding it "for completeness" would reintroduce the
         prompt-on-import problem the concrete-UID decision (OQ4)
         exists to avoid. Verified against the published CRD:
         `grafana.integreatly.org/v1beta1`, `instanceSelector`
         required, `json` a string, `resyncPeriod` defaulting to
         `10m0s`, `allowCrossNamespaceImport` defaulting to false —
         so all three optional fields are omitted unless asked for,
         and a test pins that.
      7. **The Catalogue-versus-Generate trap.** `alertArtifact` must
         consume `alert.Generate`'s kept set; `alert.Catalogue()`
         returns everything and task 5.3's promtool test renders it
         deliberately. Feeding `Catalogue()` into the emitted manifest
         would ship `PropertySchemaMissing` to a deployment with no
         `custom_properties` reconciler — the INV-0012 finding-A shape
         this generator exists to prevent. Probe-verified.
      8. **`--format` is validated in the flag parser as well as in
         `emit.Generate`,** so a typo reports the typo rather than
         whatever the config file happens to complain about first.
      9. Artifacts are written `0640` under `0750`, matching
         `internal/report`: a generated dashboard names every org and
         rule in the fleet. This governs `--out` only — the static
         tier committed to `contrib/` is public and git does not carry
         mode bits across a checkout.
- [x] 5.5 CI drift gate: regenerate the static tier from the default
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

      Landed as `make monitoring-generate` / `make lint-monitoring` +
      a `monitoring-drift` job in `ci.yml` gated on a new `monitoring`
      paths filter (`contrib/generated/**`, `internal/monitoring/**`,
      `internal/policy/**`, `cmd/repo-guardian/monitoring.go`,
      `Makefile`) OR the `go` filter OR `workflows`.

      Divergences and decisions:

      1. **The static tier lives at `contrib/generated/`, not
         `contrib/grafana/`** as task 6.6 says. One `generate`
         invocation writes both artifact kinds under one root, so the
         literal path would have put `alerts/rules.yaml` inside
         `grafana/`. A separate root also makes the gate precise: it
         can never be confused by an edit to the hand-written recipes
         in `contrib/prometheus/` and `contrib/grafana/`, which is
         what 6.6's "delete the legacy dashboard" step will be moving
         around. 6.6's wording is updated to match.
      2. **The gate generates into `build/` and uses `diff -r`, not
         `git diff --exit-code` over the working tree.** `git diff` is
         blind to untracked files, so a gate built on it would stay
         green for exactly the change that ADDS a dashboard — the
         first thing Phase 6 does. `diff -r` reports files present on
         only one side, so a dashboard deleted from the suite but left
         committed fails too. Both directions are probe-verified,
         along with the modified-file case.
      3. **`monitoring-generate` clears `dashboards/` and `alerts/`
         first.** Regenerating in place leaves a deleted dashboard's
         file behind, and the gate would then call a stale tier
         current.
      4. **`--config ''` is passed explicitly**, overriding the flag's
         `$GUARDIAN_CONFIG` default. Without it the tier is generated
         from whatever policy the developer happens to have exported,
         and the gate then fails for everyone else.
      5. **The anti-vacuous guard is a `yq` rule count**, the same
         shape `lint-alerts-chart` carries. An emitter that wrote
         nothing produces no diff against a tier that is also empty,
         and "no diff" is what this target treats as success.
      6. **`make ci` deliberately does not include it**, matching
         `lint-alerts`: both need tools (`yq`, `promtool`) beyond the
         Go toolchain and both run as their own CI job.
      7. The generator logs to stderr on every run, including an
         expected `Warn` about undeclarable orgs under the built-in
         policy. The job's verdict is the file diff; a future edit
         must not "fix" a red run by silencing the logger.
- [x] 5.6 Document generator-as-validation: a failing `policy.Load`
      fails generation, so running it in CI is a free config check
      (strict-templates precedent).

      Landed as `docs/operations/monitoring-generation.md` (wired into
      `mkdocs.yml`), which covers the whole subcommand: quick start,
      the flag table, why `--instance-selector` is mandatory, why
      datasource UIDs are concrete rather than `${DS_}` inputs, what
      mechanism scoping does and does not emit, the silent-org
      warnings, the static tier and its drift gate, and
      troubleshooting.

      The generator-as-validation section is verified rather than
      asserted — every claim in it was run against the binary: an
      unknown `guardian {}` attribute, a strict-mode rule with no
      `scope {}`, and an unclosed PR-template action each fail
      generation with a located error. The section also records the
      caveat that motivated the `--config` existence check:
      `policy.Load` treats a missing file as "use built-in defaults"
      and returns nil, so without the guard a typo'd path would emit
      the DEFAULT artifacts with exit 0 and skip the validation
      entirely — on exactly the invocation that most needs it.

#### Success Criteria

- [~] `monitoring generate` against `examples/guardian-enterprise.hcl`
  emits dashboards with a row for every configured org (including
  silent ones) and an alert manifest containing only
  configured-mechanism alerts.

  **Alert half met, dashboard half deferred to Phase 6** — the panel
  library, the `dashboard.Suite` seam and the k8s CR wrapping all ship
  here, but the four dashboards are Phase 6 content, so `Suite`
  returns nothing yet. Verified for the alert half: the enterprise
  example (strict scope, 3 orgs, 12 mechanisms) emits 23 alerts
  against the built-in policy's 19. The four extra are exactly the
  ones its mechanisms unlock, and the scoping is visibly discriminating
  rather than permissive — `BranchProtectionChurn` stays out because
  the example configures branch-protection *rules* without
  *remediation*, and `PropertiesPRBurst` stays out because its
  `custom_properties` reconciler runs in api mode.
- [x] Hand-editing a generated artifact turns CI red. Probe-verified in
  all three directions: modified file, stale extra file, missing file.
- [x] `make ci` passes.

---

### Phase 6: Dashboard suite content

All dashboards authored in the Phase 5 panel library — no
hand-written dashboard JSON anywhere.

#### Tasks

- [x] 6.1 E1 KPI dashboard — business-tier sources only (INV-0013
      Finding I): compliance % from the Phase 2 gauges,
      `open_prs_by_rule` ages, convergence rate, error budget,
      rate-limit headroom, queue depth, leader status.

      Landed as `internal/monitoring/dashboard/e1.go`, wired into
      `Suite`. Three rows: Fleet compliance, Convergence, Service
      health. 13 panels.

      Notes and one defect found:

      1. **Two-stage posture aggregation, and the order matters.**
         Every posture query is `sum(max by (<the gauge's own labels>)
         (...))`. The inner `max` collapses replicas — the gauges are
         published by the leader only, and a demoted replica keeps
         serving its last values until its process restarts, so `sum`
         on the inside multiplies the fleet by the number of replicas
         that ever held the lock. The outer `sum` then adds orgs.
         `max` on the outside would report the largest org rather than
         the fleet. Pinned by
         `TestSuite_PostureQueriesDedupeAcrossReplicas` and
         `TestSuite_LeaderGaugesAreNotSummed`.
      2. **Compliance reads "no data", never 100%, when nothing is
         tracked.** The `and on() (tracked > 0)` clause drops the
         series entirely, matching `report.CompliantPercent`'s
         `(0, false)`. The two must agree or the dashboard and the
         report describe the same fleet differently.
      3. **DEFECT FOUND — `open_prs_by_rule` is not a state gauge on
         multi-replica.** `recordOpenPRsByRule` (`drift.go:38`) `Inc()`s
         it from whichever replica performs a check, but
         `metrics.ResetOpenPRsByRule` is called only from
         `StaleSweeper.SweepStale` (`sweep.go:110`), which runs on the
         leader. A non-leader's copy therefore accumulates without
         ever being reset: it counts "times we observed an open PR",
         not "how many are open now" — the exact Finding B confusion
         this design exists to remove, in a metric the design lists as
         an E1 source. The panel ships with the caveat in its
         description (which lands in the dashboard JSON where an
         operator reads it) rather than being quietly dropped or
         quietly trusted. **Recommended fix, out of scope here:**
         derive it in `PostureExporter.Export` from the store like the
         other posture gauges, and delete the check-time `Inc`. That
         also fixes `RepoGuardianStaleOpenPRs`, which currently
         thresholds an inflated number.
      4. **`Builder` is now a type alias** for the SDK's
         `DashboardBuilder`, so dashboard files can name the type
         without each carrying the schema-v1 deprecation suppression;
         the alias takes it once.
      5. **`TestSuite_EveryPanelQueryParses`** wraps every panel's
         expression as an alert rule and runs `promtool check rules`
         over the lot. Nothing else can tell valid PromQL from
         invalid: a dashboard with a broken query renders perfectly
         and shows nothing, which is indistinguishable from a
         compliant fleet. Grafana variables are substituted first.
         `TestSuite_EveryPanelIsDescribed` refuses an undescribed
         panel — the guesses that matter here (rate or state? does it
         include parked repos?) are the ones INV-0013 found people
         getting wrong.
- [x] 6.2 E2 detailed dashboard — fleet aggregate section
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

      Landed as `internal/monitoring/dashboard/e2.go`. A Fleet row plus
      one row per declarable org.

      Notes:

      1. **Only NON-pattern orgs get a declared row.** A row titled
         `acme-labs-*` would query `{org="acme-labs-*"}` — an exact
         match against a name no repository has — so it would render
         permanently empty and look exactly like an org that went
         silent, which is the one thing declared rows exist to make
         visible. Patterns fall through to the discovered-row path.
      2. **The `$org` template variable is declared only when no org
         is declarable.** A variable nothing references invites an
         operator to switch it and wonder why no panel moved. Both
         directions are pinned and probe-verified.
      3. **`sum without (org)` from the plan is written as
         `max by (org)` / `sum by (rule_name)` instead.** `without` on
         a leader-published gauge would sum across replicas — the
         same trap E1 documents. The intent (fleet aggregate) is
         preserved; the aggregation is the one the exporter's
         contract allows.
      4. **The `installation_info` `group_left(org)` join** carries the
         org label onto `rate_limit_remaining`, which is otherwise
         keyed only by `installation_id` and unreadable per org.
      5. **Queue, store and scheduler series are asserted ABSENT** by
         `TestE2_OmitsFleetScopedInfrastructure` rather than merely
         left out. Repeated under a per-org heading they would show
         the same number in every row and invite someone to read it as
         that org's share.
      6. The promtool panel-parse test now runs over both a legacy and
         a strict model, so the `$org` branch and the literal-org
         branch are both parsed.
- [x] 6.3 E3 system dashboard — service + infra tiers: OTEL semconv
      HTTP server/client, redisotel, pgx pool + `store_query_seconds`,
      queue USE (including IMPL-0022's series when present), go
      runtime/process, the three orphan histograms
      (`scheduler_sweep_batch_size`,
      `store_writeback_duration_seconds`,
      `discovery_duration_seconds`), posture-exporter health.

      Landed as `internal/monitoring/dashboard/e3.go`. Four rows —
      HTTP, Queue, Store, Runtime and sweeps — 22 panels. All three
      orphan histograms now have one.

      The finding worth carrying forward: **the OTEL exposition names
      are not the semconv names, and building a panel from the spec
      produces an empty one.** Every foreign series charted here was
      read off a live scrape of the bridge, not inferred:

      - otelpgx publishes the pgx-NATIVE `pgxpool_*` family (13
        series), NOT `db.client.connection.*`, and keys it on
        `db_client_connection_pool_name` with
        `db_system_name="postgresql"`.
      - redisotel publishes `db_client_connections_*` — plural
        "connections", the older semconv shape — keyed on `pool_name`
        with `db_system="redis"`. Note "redis": the library reports
        the protocol, not the server, and repo-guardian runs Valkey.
        A panel matching `db_system="valkey"` would be empty forever.
      - otelhttp emits `http_server_request_duration_seconds` and
        `http_client_request_duration_seconds`.

      `TestE3_ChartsOnlyVerifiedForeignSeries` holds every non-
      `repo_guardian_` name to an allowlist whose doc comment says
      where each came from, so a later tidy-up toward the spec fails
      the build rather than going quiet. It needed a small PromQL
      identifier scanner that distinguishes series names from label
      names (labels appear inside `{...}` AND inside the parenthesised
      list after `by`/`on`/`group_left`).

      `TestE3_CarriesNoComplianceSeries` asserts the Finding I tier
      split from the other side: no business gauge appears on E3.
- [x] 6.4 E4 Loki dashboard (error rate by component, sweep
      summaries, write-back failures, webhook rejections, top
      erroring repos as log fields) + Loki-ruler recording-rule
      examples in `contrib/`.

      `internal/monitoring/dashboard/e4.go` — 13 panels in five rows
      (Errors, Repository faults, Write-back and job loss, Webhook,
      Sweeps), wired into `Suite` as the fourth dashboard. Every
      matcher is a named constant, and every message was read off the
      emitting source rather than remembered.

      **Divergence 1 — "error rate by component" became "by level and
      by message".** There is no `component` field in repo-guardian's
      log records; the structured keys are `level`, `msg`, `owner`,
      `repo`, `reconciler`, `rule`. Grouping by a field that does not
      exist yields one unlabelled series, which is worse than useless
      on an incident dashboard. Grouping by `msg` gets the intended
      shape — one line per repeating fault — and is bounded because
      messages are static strings with the variable parts in fields.

      **Divergence 2 — "top erroring repos" is per-fault, not global.**
      A single `topk by (owner, repo)` over all ERROR lines reads as a
      leaderboard with no action attached to it. The three panels that
      do group by repository are each tied to one fault an operator can
      act on: an unparseable catalog-info, a parked repository, and a
      job dropped at the attempt cap.

      **Divergence 3 — the stream selector is a new flag.** Stream
      labels are minted by the log shipper, so `{app="repo-guardian"}`
      is a convention this repository cannot verify. `Datasources`
      grew a `LogStream` field (default `app="repo-guardian"`) and the
      CLI a `--loki-selector`. It is the one input most likely to be
      wrong on a fresh import, so the first panel's description prints
      the selector in use, and `TestE4_EveryQueryStartsFromTheStream
      Selector` fails if any panel hard-codes the default instead of
      taking the configured one.

      **The `promtool` gate needed a datasource split.**
      `TestSuite_EveryPanelQueryParses` fed every panel expression to
      `promtool check rules`, which parses PromQL; E4's LogQL would
      have failed it. `panelTarget` now carries the panel's datasource
      TYPE and panel type, the three PromQL assertions run through a
      `promTargets` filter, and `suiteTargets` additionally fails any
      panel that declares no datasource at all (such a panel inherits
      whatever the dashboard default is at import time).

      **No offline LogQL parser exists here** — `logcli`/`lokitool`
      are not in `mise.toml` and vendoring Loki's parser to check a
      dozen strings is not a trade worth making. Three structural
      tests stand in for it:
      `TestE4_GraphPanelsUseMetricQueries` (a log-selector expression
      on a graph plots nothing and a metric expression on a logs panel
      lists nothing — neither errors),
      `TestE4_ChartsNoPrometheusSeries`, and
      `TestE4_IsTheOnlyLokiDashboard`.

      **`TestLogLines_AreStillEmittedByTheBinary` is the one that
      matters.** It walks `internal/` and fails if any of the eleven
      matched log lines is no longer emitted anywhere. It lives in
      package `dashboard` (not `dashboard_test`) so it reads the same
      constants the panels do — a duplicated list could drift and
      pass. First version was vacuous: `internal/monitoring/dashboard`
      is itself under `internal/`, so every literal matched its own
      const declaration; the walk now skips the declaring tree. Probe-
      verified by renaming the attempt-cap message in
      `internal/worker/worker.go`.

      `contrib/loki/rules.yaml` carries the recording-rule examples,
      hand-maintained and outside the drift-gated `contrib/generated/`
      tree. The cardinality argument is stated in the header: these
      series are bounded by how many repositories are currently BROKEN,
      not by how many exist, which is why the per-repository answer the
      app refuses to mint (Finding G) is safe to record here. One
      correctness fix during authoring: the alerting rules originally
      referenced the recorded series by name, which the Loki ruler
      cannot do — it parses `expr` as LogQL, and the recorded names
      only exist after remote-write lands them in Prometheus. They are
      now full LogQL, with a note that alerting in Prometheus over the
      recorded series is the alternative and that doing both
      double-fires.

      Also corrected the `dashboard` package doc, which claimed every
      panel takes "exactly one datasource and one query" — `TimeSeries`
      has been variadic since 6.1 and E1/E3 use multi-query panels. The
      rule is one datasource and one TIER per panel; the datasource
      half is enforced by construction, the tier half by tests.
- [x] 6.5 Test-lock the catalog-parse log line ("catalog-info parse
      failed; skipping reconcile to avoid clearing properties" —
      exact message + keys, the same contract style as
      `TestAPIMode_FiltersUndefinedMappedProperty`) before E4
      references it.

      Landed as `internal/reconciler/log_contract_test.go`, done
      BEFORE E4 as the task requires. Three tests:

      1. `TestCatalogParseFailure_LogContract` — exact message, WARN
         level, and the `reconciler` / `mode` / `err` keys.
      2. `TestNotComponent_LogContract` — the sibling line, at INFO,
         plus an assertion that a valid non-Component entity does NOT
         also emit the parse-failure line. Without that second half a
         LogQL query would count healthy repositories as broken ones.
      3. `TestCatalogParseFailure_CarriesCallerFields` — `owner`,
         `repo` and `rule` reach the record even though this package
         does not set them (they come from the engine's per-repo
         logger and the rule loop). They are what an E4 panel groups
         by, so the pass-through is part of the same contract.

      Why it is a contract at all: the counter says HOW MANY
      repositories have a broken catalog, and only the log says WHICH
      and WHY. A LogQL matcher that stops matching returns no rows,
      which renders identically to "no repository has a broken
      catalog" — the same silent-failure shape as every other gap this
      design closes. All four assertions are probe-verified by
      rewording the message, dropping the `mode` key, and flipping the
      non-Component level.
- [x] 6.6 Commit the generated static tier to `contrib/generated/`
      (see task 5.5 divergence 1 — one `generate` run writes both
      dashboards and alerts, so they share a root rather than landing
      in `contrib/grafana/`); delete the legacy 61-panel dashboard
      with a pointer in `contrib/README.md`. The alert half of the
      tier and its drift gate are already in place; this task adds the
      dashboards to it via `dashboard.Suite`.

      The four dashboards landed in the tier incrementally as 6.1-6.4
      wired each into `dashboard.Suite`; `make lint-monitoring` has
      gated them since. What this task adds is the deletions.

      **Divergence — `contrib/prometheus/alerts.yaml` is deleted, not
      modified.** The file-change table says "Modify | generator-
      authored parity". A second hand-maintained copy of the alert set
      IS the drift the generator exists to remove, and parity has no
      meaning here: the checked-in tier is generated from built-in
      defaults, so it is a proper subset of the catalogue by design.
      Six alerts appear only when the policy engages their mechanism,
      which is the INV-0012-finding-A fix working, not a gap. The
      README now names those six and their gates instead of shipping a
      file that fires none of them.

      Audited before deleting rather than assumed: 24 of the legacy
      file's 25 alerts exist in `alert.Catalogue()`. The one that does
      not is `RepoGuardianRateLimitLow`, which watches the unlabelled
      `github_rate_remaining` gauge — a real, fed series (set by the
      transport in `internal/github/ratelimit.go`), so this is NOT a
      third finding-A instance; I checked. The catalogue's
      `RepoGuardianRateLimitNearExhaustion` supersedes it on the
      per-installation `rate_limit_remaining`, which names the
      installation that is out of budget rather than reporting that
      some installation is. Recorded in the README's "where the old
      files went" table so the loss is deliberate and visible.

      Consequent retargeting: `make lint-alerts-contrib` became
      `lint-alerts-generated` and now parses
      `contrib/generated/alerts/rules.yaml`, and the CI `alerts`
      paths-filter entry moved from `contrib/prometheus/**` to
      `contrib/generated/alerts/**`. Keeping the target is worth it
      even though the Go tests promtool the catalogue: this parses the
      COMMITTED artifact, so it catches a hand-edit to a file whose
      header says not to hand-edit it. Also fixed the stale pointer in
      `docs/ADDING_RULES.md`, which told rule authors their rule would
      appear in two named panels of a dashboard that no longer exists.
- [x] 6.7 Rewrite `contrib/README.md` around the four-dashboard suite
      and the generated/static two-tier model.

      New front matter: the two tiers (generated vs hand-maintained)
      and why generation is the point at all; the six mechanism-gated
      alerts and what engages each; the four dashboards as a table of
      questions rather than of contents; E4's stream selector as the
      thing most likely to need changing. Rewrote "Importing" around
      concrete datasource UIDs and the grafana-operator `--format k8s`
      path, and "Applying the alerts" around the generated file plus
      the do-not-apply-both warning for chart users.

      The metric reference tables (lines ~200 onward) are untouched:
      they are still accurate and still the only place the label sets
      are written down. Task 7.1 edits them when the four legacy
      counters go.

#### Success Criteria

- All four dashboards render against a live stack with no
  empty-by-construction panels and no references to dead or retiring
  series (budget pair, rate-limit wait pair).
- Every panel's source respects the Finding I taxonomy and the
  one-source-per-panel rule.
- Drift gate green on the committed static tier; `make ci` passes.

**Met, with one half operator-side.** 56 panels across four
dashboards (12 KPI, 8 detail, 23 system, 13 logs, against the built-in
policy). Every one has at least one query and an explicit datasource,
both asserted by `suiteTargets` rather than eyeballed — a panel with no
datasource inherits whatever the dashboard default happens to be at
import time, which is how a Prometheus panel ends up silently pointed
at Loki.

Dead-series scan is clean: no `api_budget_*`, no
`enqueue_gated_by_budget_total`, no `rate_limit_wait*` anywhere in the
generated tier. The only rate-limit series charted is
`repo_guardian_rate_limit_remaining`.

The taxonomy is enforced from both sides rather than asserted once —
`TestE3_CarriesNoComplianceSeries`, `TestE2_OmitsFleetScopedInfra
structure`, `TestE4_ChartsNoPrometheusSeries`,
`TestE4_IsTheOnlyLokiDashboard`. The one-source rule was restated
during 6.4: it is one datasource and one TIER per panel, not one
query — `TimeSeries` has been variadic since 6.1 and the package doc
had not caught up.

`make ci` passes and the drift gate is green.

**Not verifiable here: "render against a live stack."** There is no
Grafana, Prometheus or Loki in this environment. What is verified is
that every dashboard builds through the SDK, marshals to JSON, carries
concrete datasource UIDs, and that every PromQL expression parses under
`promtool`. The LogQL half has no offline parser available at all (see
6.4), so E4's expressions are structurally checked only. Live import
remains an operator-side smoke step, the same shape as the homelab
smoke IMPL-0019 and IMPL-0020 left open.

---

### Phase 7: Cleanup, deprecations, docs, chart

#### Tasks

- [x] 7.1 Remove the four legacy counters
      (`properties_checked_total`, `properties_prs_created_total`,
      `properties_set_total`, `properties_already_correct_total`;
      OQ4 → a) with migration notes in `contrib/README.md`.

      Three of the four are straightforward removals — they predated
      per-org labelling and two were posture wearing a counter's
      clothes, which is Finding B exactly.

      **Divergence — `properties_prs_created_total` folds into
      `prs_created_total{org}` rather than simply disappearing.** The
      design says "superseded by real posture", which is true of the
      other three and NOT true of this one: PR creation is an event,
      not a state, and it had a live consumer in
      `RepoGuardianPropertiesPRBurst`. Deleting it outright would have
      left reconciler-opened PRs counted nowhere and orphaned that
      alert onto a series with no producer — the very INV-0012 finding
      A shape this phase is supposed to be eliminating.

      Folding is a fix, not a compromise. Reconciler PRs were
      invisible to every per-org PR panel and to
      `RepoGuardianPRBurst` for as long as they had a counter of their
      own, and nothing about them justifies a separate series: they
      are pull requests repo-guardian opened against someone's
      repository. **Operator-visible consequence:** on a
      `github-action`-mode deployment `prs_created_total` steps up
      after this upgrade. Documented in `contrib/README.md` as a
      behaviour change rather than a rename.

      `RepoGuardianPropertiesPRBurst` is therefore deleted: with the
      counters folded it was `RepoGuardianPRBurst` under another name,
      same 50-per-hour threshold. Coverage was checked rather than
      assumed — `PRBurst` requires `MechanismFileRules`, and a
      `custom_properties` reconciler is only ever attached to a file
      rule, so any policy that could have fired the old alert engages
      the new one.

      `MechanismCustomPropertiesGHA` survives with no alert to gate.
      It is a true fact about the policy — which of two very different
      write paths is live — it is reported in the generation log, and
      deriving it is free. Its doc comment now says all of that
      instead of naming a metric that no longer exists.

      New test `TestGHAMode_PRCountsAgainstTheOrgCounter` pins the
      fold (probe-verified by neutralising the increment). The
      mechanism-scoping table lost its PropertiesPRBurst rows and
      gained an assertion that github-action mode does not *suppress*
      the api-mode alerts either — the preflight runs in both modes,
      and gating it on the mode is the mistake that would silently
      drop both alerts for every api-mode deployment.

      Docs: root `README.md` metric table (four rows deleted),
      `contrib/README.md` (a removed → use-instead table, the
      behaviour-change note, and the mechanism-gated alert list down
      from six to five).
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
- [x] Exporter leader-gating and stale-series reset tests (non-vacuity:
      skip the `Reset()`, confirm failure, restore). Stale-series in
      `posture_test.go`, leader-gating in `posture_integration_test.go`
      (two real schedulers against one Valkey — a fake scheduler would
      only be testing itself).
- [x] Mock fidelity: `UpsertRuleStates` → `Posture` (the aggregate
      shipped as one read, not the sketch's `ActionableCounts`) is
      list-then-act — fakes must reflect prior writes (CLAUDE.md rule)
      or exporter tests are vacuous.

      **Divergence: satisfied with real Postgres instead of a stateful
      fake.** The CLAUDE.md rule exists because a fake that forgets
      prior writes makes the read side assert nothing. Here the failure
      is worse than that rule anticipates: the exporter's whole job is
      to publish what the store returns, so against ANY fake — stateful
      or not — the gauges match by construction and the assertion is
      circular. Teaching the fake to re-derive the aggregate would just
      mean reimplementing the SQL in Go and testing that the two
      implementations agree, which is the wrong bug to catch. So the
      write→read chain is pinned end-to-end against real Postgres in
      two places (`TestPostureExport_GaugesEqualSQLTruth`, and the
      Phase 1 worker write-back test), and the fakes in
      `posture_test.go` are kept deliberately dumb — they serve a
      scripted aggregate to test reset/ordering/error semantics, which
      is all they can honestly test.
- [x] `/metrics` bridge coexistence test + bad-HMAC 401 measurement
      test.

      Coexistence: `TestNew_BridgeAndDomainMetricsShareOneEndpoint`
      plus `TestNew_DefaultRegistryIsTheProductionPath` — the latter
      exists because the bridge is an UNCHECKED collector (its
      `Describe` is a no-op), so a name collision raises nothing at
      registration and instead 500s the entire `/metrics` endpoint at
      scrape time. A test against a fresh registry would never see it.

      401: `TestHandler_MeasuresRejectedWebhooks` drives the real
      webhook handler with a bad signature and asserts the 401 lands
      in `http_server_request_duration_seconds` with
      `http_response_status_code="401"`. A stub handler would only
      prove otelhttp works, which is upstream's test.
- [x] Report golden files.

      Ten fixtures in `internal/report/testdata/`, plus
      `TestReport_MatchesDatabaseTruth` closing the seam between the
      goldens (format, hand-built input) and the store integration
      tests (queries, real database) — each half supplies the other's
      input, so a seam defect passes both.
- [ ] Generator render tests against the pinned Grafana schema;
      zero-mechanism fixture emits zero mechanism panels/alerts.
      (Phase 5.)
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
