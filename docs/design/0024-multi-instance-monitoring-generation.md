---
id: DESIGN-0024
title: "Multi-instance monitoring generation"
status: Draft
author: Donald Gifford
created: 2026-09-23
---

<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN-0024: Multi-instance monitoring generation

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

`repo-guardian monitoring generate` produces output that is only correct
when exactly one repo-guardian reports to a given Prometheus, Loki and
Grafana. INV-0017 Observation 10 showed that assumption is already false
in the homelab (`repo-guardian` and `repo-guardian-dev`), and that it
fails silently in four ways. This design adds a single notion of
**instance identity** to the generator and threads it through every
artifact it emits, so N repo-guardians — different environments, orgs,
or policies — can share one monitoring stack without colliding or
merging.

With no instance given, output is byte-identical to today's. The
committed tier and its drift gate do not move.

## Goals and Non-Goals

### Goals

1. **No collisions.** Two instances' dashboards, CRs and rules can be
   applied to the same Grafana / Prometheus / cluster and coexist.
2. **No merging.** Every PromQL and LogQL query in an instance's
   artifacts sees only that instance's series and streams. This is a
   correctness fix, not cosmetics: it is what stops `max by (org)` from
   silently reporting the larger of two instances watching the same org.
3. **No masking.** `RepoGuardianNoRepoChecks` and every other
   aggregate-to-scalar alert evaluates per instance, so one dead instance
   among ten fires.
4. **Distinguishable alerts.** An alert that fires in Alertmanager says
   which instance it came from, even when its expression aggregated every
   label away.
5. **Per-policy correctness is preserved.** Each instance is generated
   from its own `guardian.hcl`, so mechanism gating stays exact.
6. **One identity, both tiers.** The metric matcher and the Loki stream
   selector derive from the same flags (INV-0017 Observation 8's
   closing note).

### Non-Goals

- **One shared dashboard set with an `instance` template variable.**
  Mechanism gating is per-policy: ten instances with ten policies need
  ten different panel sets, and a shared dashboard can only be right for
  a homogeneous fleet. Revisit as an opt-in if homogeneous fleets turn
  out to be common (OQ5).
- **Teaching the binary its own identity.** No const label is added to
  the app's metrics. The scrape already stamps identity; see OQ2.
- **The Loki rules tier.** INV-0017 question 3 is designed separately,
  but it inherits the instance selector from this design for free.
- **Pinning datasource UIDs.** Cluster-side, per INV-0017 Observation 2.

## Background

INV-0017 Observation 10 in full; the short version:

| Failure | Cause |
| --- | --- |
| Dashboards flap between instances forever | Dashboard UIDs are constants (`repo-guardian-kpi`, …) and a Grafana UID is global to the Grafana, not the namespace |
| CRs collide in one namespace | `GrafanaDashboard` `metadata.name` is `Dashboard.Slug`, also a constant |
| A dead instance is invisible | `sum(increase(repo_guardian_repos_checked_total[2h])) == 0` aggregates across every scraped instance |
| Two instances' posture merges | `max by (org)` dedupes replicas — correct within a deployment, wrong across two that watch the same org |

Two facts from the code audit shape the design:

1. **The identity already exists on every series.** The chart ships a
   plain `ServiceMonitor` (`charts/repo-guardian/templates/servicemonitor.yaml`,
   port `metrics`, no relabelling), so Prometheus Operator stamps
   `namespace`, `service`, `job`, `pod` and `instance` on everything.
   Nothing needs to change in the app or the chart to make instances
   distinguishable — the generator just never filters on it.
2. **Every query is a hand-written string literal.** Roughly 70 PromQL
   expressions across E1–E3 and the alert catalogue (E4's LogQL already
   routes through one function, below), e.g.
   `` `sum(max by (org, rule) (repo_guardian_open_prs_by_rule{age_bucket="30d+"}))` ``.
   There is no PromQL AST anywhere and no PromQL parser dependency. How
   a matcher gets into 86 literals without missing one is the main
   design problem.

On the Loki side, INV-0017 confirmed `{app="repo-guardian"}` matches
four streams — `{namespace} × {container}` over
`{repo-guardian, repo-guardian-dev} × {repo-guardian, valkey}` — and
that the chart's container is always `{{ .Chart.Name }}`, i.e.
`repo-guardian`.

## Detailed Design

### Instance identity

An instance is a **(label, value)** pair naming which scraped series and
shipped streams belong to it, plus a short **name** used to build
identifiers:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--instance` | empty | Instance name. Empty = single-instance mode, output unchanged. |
| `--instance-label` | `namespace` | The scrape label that disambiguates instances. |
| `--instance-value` | `--instance` | The label's value, when it differs from the name. |

The default of `namespace` fits the common layout (one release per
namespace, as in the homelab) and is present on every series via the
ServiceMonitor and on every stream via the log shipper. Two releases in
one namespace need `--instance-label service` or `job` instead, which is
why it is a flag rather than a constant (OQ2).

The name and the value are separate because they have different
constraints: the value must match whatever the scrape produced, while
the name must fit inside a Grafana UID (below).

### Identifiers

With `--instance dev`:

| Identifier | Today | With `--instance dev` |
| --- | --- | --- |
| Dashboard UID and slug | `repo-guardian-kpi` | `repo-guardian-dev-kpi` |
| `GrafanaDashboard` `metadata.name` | `repo-guardian-kpi` | `repo-guardian-dev-kpi` |
| Dashboard title | `repo-guardian — KPI` | `repo-guardian — KPI (dev)` |
| Grafana folder | `repo-guardian` | `repo-guardian` (shared) — OQ4 |
| `PrometheusRule` `metadata.name` | `repo-guardian` (`--name`) | `repo-guardian-dev` |
| Output paths | `dashboards/repo-guardian-kpi.json` | `dashboards/repo-guardian-dev-kpi.json` |

The suffix goes in the middle (`repo-guardian-<instance>-kpi`) so an
instance's four dashboards sort together.

**Grafana caps dashboard UIDs at 40 characters.** The longest slug
suffix is `-system` (7) and the prefix `repo-guardian-` is 14, so the
instance name is limited to **19 characters** of RFC 1123 label syntax.
`ValidateSuite` already refuses rather than sanitises for the reason in
its doc comment; the instance name gets the same treatment — reject a
name that would produce a >40-char UID at flag-parse time, with the
arithmetic in the error.

`--name` keeps working as an explicit override of the `PrometheusRule`
name; when only `--instance` is given, the default becomes
`repo-guardian-<instance>`.

### Matcher injection (PromQL)

Every metric selector in every query gains `<label>="<value>"`. Because
the matcher filters to one instance *before* any aggregation, it fixes
the merging and masking failures without touching a single `sum`, `max
by` or `by (...)` clause: `max by (org)` over one instance's series is
exactly the replica dedupe it was written to be, and `sum(...) == 0`
over one instance fires when that instance is dead.

The mechanism is a **selector helper**, not a parser:

```go
// Metric renders a repo_guardian_* selector with the instance matcher
// and any extra matchers, e.g.
//   m.Metric("open_prs_by_rule", `age_bucket="30d+"`)
//   -> repo_guardian_open_prs_by_rule{namespace="dev",age_bucket="30d+"}
func (s Selectors) Metric(name string, matchers ...string) string
```

The literals are rewritten once to build their selectors through it
(via `fmt.Sprintf` or small string concatenation — no text/template).
With no instance the helper emits exactly today's text, which the
unchanged committed tier proves byte-for-byte.

Why not a PromQL parser (walk `VectorSelector`s, append a matcher,
re-print): `prometheus/prometheus` is the only real parser, it drags the
whole Prometheus module into the main binary, and the
dependency-isolation argument DESIGN-0022 OQ5 made for the Grafana SDK
applies with more force. The parser's one real advantage — it cannot
miss a selector — is recovered by a test instead (see Testing Strategy).

The selector helper lives in `internal/monitoring` (the model package)
so both `alert` and `dashboard` can use it without importing each other.

### Loki selector

`Datasources.Stream()` is already the single choke point for every E4
query. It changes from "wrap `LogStream` in braces" to composing:

1. the base selector — `--loki-selector`, default `app="repo-guardian"`;
2. the instance matcher — `<label>="<value>"`, when `--instance` is set;
3. `container="repo-guardian"` — always, when `--loki-selector` was
   not given explicitly.

Item 3 fixes the Valkey half of INV-0017 Observation 8 for every
install, not just multi-instance ones. It is safe to hardcode because
the chart names the container `{{ .Chart.Name }}`. It changes the
committed tier's E4 output, which is the one intended exception to
"single-instance output is unchanged" (OQ3).

An explicit `--loki-selector` is used verbatim plus the instance matcher
— the operator has told us their labels; we do not second-guess them
with a `container` matcher their shipper may not produce.

The `--loki-selector` syntax check INV-0017 OQ6 resolved on runs over
the composed result.

### Alert identity

Aggregating expressions drop every label, so two instances' copies of
`RepoGuardianNoRepoChecks` are indistinguishable in Alertmanager — same
name, same (empty) label set, deduplicated into one notification. Every
rule in an instance's `PrometheusRule` therefore gains a static label:

```yaml
labels:
  severity: warning
  repo_guardian_instance: dev
```

Static rule labels survive aggregation, route in Alertmanager like any
other, and cost nothing. Omitted entirely in single-instance mode so the
committed tier does not move.

### The model

`monitoring.Model` gains an `Instance` value (name, label, value),
populated from the flags in `cmd/repo-guardian/monitoring.go` alongside
`Datasources`. `alert.Generate`, `dashboard.Suite` and `emit.Generate`
already take the model; nothing new is threaded through their
signatures.

## API / Interface Changes

- **CLI:** three new flags on `monitoring generate` — `--instance`,
  `--instance-label`, `--instance-value`. All optional; defaults preserve
  current behaviour.
- **`--name` default** becomes `repo-guardian-<instance>` when
  `--instance` is set. Unchanged otherwise.
- **`Datasources.Stream()`** composes instead of wrapping (above).
- **Internal:** `monitoring.Instance` type and `Selectors` helper;
  `Dashboard.Slug` and the `New(uid, …)` calls take the instance-aware
  slug; `alert.Spec` rendering stamps the instance label.
- **No app, metric or chart changes.**

## Data Model

No persistent data. The only new "schema" is the naming convention
`repo-guardian-<instance>-<dashboard>`, bounded at 40 characters, and the
alert label `repo_guardian_instance`.

## Testing Strategy

1. **Single-instance output is unchanged.** `make lint-monitoring` is the
   test: the committed tier is generated with no `--instance` and must
   diff clean, except for the deliberate E4 `container=` change (OQ3),
   which lands as a reviewed regeneration.
2. **Every selector carries the matcher.** Generate the full suite *and*
   the full alert catalogue (not the mechanism-gated kept set — use a
   model with every mechanism on) with `--instance x`, then assert that
   every `repo_guardian_[a-z0-9_]+` occurrence in every expression is
   immediately followed by a `{…}` containing `namespace="x"`. All app
   metrics share the `repo_guardian_` prefix, which is what makes a
   regex adequate for those. **E3 is the trap:** it also charts four
   families the app does not prefix — `http_server_*` / `http_client_*`
   (otelhttp), `db_client_connections_*` (otelpgx) and `pgxpool_*` — so
   the test must match those by name too, and the helper needs a
   variant that takes a full metric name. This is the parser's "cannot miss one" guarantee, as a test.
   **Verify it is non-vacuous** by deliberately bypassing the helper in
   one literal and confirming the test fails.
3. **Identifiers.** UIDs, slugs, CR names and `PrometheusRule` name carry
   the instance; `ValidateSuite` passes; a 20-character instance name is
   rejected at flag parse with the 40-character arithmetic in the
   message.
4. **Two instances coexist.** Generate `--instance a` and `--instance b`
   into one directory tree and assert no path, UID or `metadata.name`
   collides.
5. **Alert labels.** Every rule carries `repo_guardian_instance` in
   instance mode and none in single-instance mode.
6. **promtool.** The instance-mode `PrometheusRule` passes
   `promtool check rules` — a matcher spliced into the wrong place is a
   parse error, and this catches it where the regex would not.
7. **Loki composition.** Default / explicit `--loki-selector` × with /
   without `--instance`, four cases, exact strings.

## Migration / Rollout Plan

- **Existing single-instance users:** no action. Output is unchanged
  apart from E4 gaining `container="repo-guardian"`, which only removes
  Valkey noise.
- **Homelab (two instances today):** regenerate each with its own
  `--config` and `--instance` (`--instance prod` / `--instance dev`, with
  `--instance-value repo-guardian` / `repo-guardian-dev` since the
  namespaces are named after the release). Apply both. Delete the old
  un-suffixed `GrafanaDashboard` CRs — otherwise the old
  `repo-guardian-kpi` keeps flapping between whichever instance last
  wrote it.
- **Docs:** `docs/operations/monitoring-generation.md` gains a
  "Running more than one repo-guardian" section with the two-instance
  recipe; `contrib/generated/README.md` notes the reference tier is
  single-instance.
- **Chart NOTES.txt (optional follow-up):** print the exact
  `monitoring generate` flags for this release — it knows its namespace
  and name.
- Release: minor (new flags), no chart change.

## Open Questions

- **OQ1 — Selector helper or PromQL parser?** (a)=selector helper plus
  the regex-coverage test, keeping `prometheus/prometheus` out of the
  main binary; (b)=parse-and-rewrite with `promql/parser`, which cannot
  miss a selector but costs the dependency; (c)=parser only in the test,
  helper in production — the strongest guarantee, with the dependency
  confined to test builds; other:

- **OQ2 — Default instance label?** (a)=`namespace`, present on every
  series and stream today and right for one-release-per-namespace;
  (b)=`service`, which also distinguishes two releases in one namespace
  but is absent from Loki streams in the homelab shipper config, so the
  two tiers would need different labels; (c)=`job`; other:

- **OQ3 — Does `container="repo-guardian"` go into the default E4
  selector for single-instance users too?** (a)=yes — it only removes
  Valkey's non-JSON lines, and the committed tier is regenerated in the
  same PR; (b)=only in instance mode, keeping single-instance output
  strictly byte-identical; other:

- **OQ4 — Shared or per-instance Grafana folder?** (a)=shared
  `repo-guardian` folder, with the instance in each title so they sort
  and read together; (b)=`repo-guardian-<instance>` per instance, which
  keeps folders small but splits a fleet view across N folders; other:

- **OQ5 — Is a shared-dashboard `instance` variable mode ever wanted?**
  (a)=no, not in this design — revisit only if homogeneous fleets
  appear; (b)=yes, as an opt-in `--instance-variable` for fleets that
  commit to a single policy; other:

## References

- INV-0017 — Observations 8 (Loki four streams) and 10 (multi-instance
  failures), OQ6/OQ8/OQ9
- DESIGN-0022 — dashboard suite, the `max by` posture contract, OQ5
  (dependency isolation)
- IMPL-0023 — Phase 5 (`monitoring generate`), Phase 6 (panel library)
- `internal/monitoring/dashboard/suite.go` — `Dashboard.Slug`,
  `ValidateSuite`
- `internal/monitoring/dashboard/dashboard.go` — `Datasources.Stream()`
- `internal/monitoring/emit/k8s.go`, `emit.go` — CR and rule naming
- `charts/repo-guardian/templates/servicemonitor.yaml`
</content>
</invoke>
