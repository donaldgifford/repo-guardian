---
id: IMPL-0032
title: "Controls UI: findings, org tree, fleet panels, changelog and rc.11 fixes"
status: Draft
author: Donald Gifford
created: 2026-10-18
---

<!-- markdownlint-disable-file MD024 MD025 MD041 -->

# IMPL-0032: Controls UI: findings, org tree, fleet panels, changelog and rc.11 fixes

<!--toc:start-->
<!-- markdownlint-capture -->
<!-- markdownlint-disable MD013 -->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Pre-implementation audit](#pre-implementation-audit)
- [How the phases are ordered](#how-the-phases-are-ordered)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: rc.11 fixes and UI foundations](#phase-1-rc11-fixes-and-ui-foundations)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: API additions](#phase-2-api-additions)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Persist next due](#phase-3-persist-next-due)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: Findings page and control detail](#phase-4-findings-page-and-control-detail)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: Orgs tree](#phase-5-orgs-tree)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: Fleet panels](#phase-6-fleet-panels)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
  - [Phase 7: Status page changelog](#phase-7-status-page-changelog)
    - [Tasks](#tasks-6)
    - [Success Criteria](#success-criteria-6)
  - [Phase 8: e2e, accessibility, screenshots and docs](#phase-8-e2e-accessibility-screenshots-and-docs)
    - [Tasks](#tasks-7)
    - [Success Criteria](#success-criteria-7)
  - [Phase 9: Remediation data for the gated panels](#phase-9-remediation-data-for-the-gated-panels)
    - [Tasks](#tasks-8)
    - [Success Criteria](#success-criteria-8)
  - [Phase 10: rc image and homelab review](#phase-10-rc-image-and-homelab-review)
    - [Tasks](#tasks-9)
    - [Success Criteria](#success-criteria-9)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
  - [OQ1: The shape of aggregated history points](#oq1-the-shape-of-aggregated-history-points)
  - [OQ2: What /results filters on: status or bucket](#oq2-what-results-filters-on-status-or-bucket)
  - [OQ3: When /results computes total](#oq3-when-results-computes-total)
  - [OQ4: How next due is persisted](#oq4-how-next-due-is-persisted)
  - [OQ5: Where CHANGELOG.yaml lives](#oq5-where-changelogyaml-lives)
  - [OQ6: How the changelog reaches the Vite bundle](#oq6-how-the-changelog-reaches-the-vite-bundle)
  - [OQ7: The header org scope](#oq7-the-header-org-scope)
  - [OQ8: The org tree component](#oq8-the-org-tree-component)
  - [OQ9: Default range](#oq9-default-range)
  - [OQ10: The remediation data tasks and the one-PR rule](#oq10-the-remediation-data-tasks-and-the-one-pr-rule)
  - [OQ11: INV-0023 F7.1 and F7.2](#oq11-inv-0023-f71-and-f72)
  - [OQ12: The control_results index](#oq12-the-control_results-index)
  - [OQ13: Testing the "not enabled" empty state](#oq13-testing-the-not-enabled-empty-state)
  - [OQ14: Order against IMPL-0030](#oq14-order-against-impl-0030)
- [References](#references)
<!-- markdownlint-restore -->
<!--toc:end-->

<!--docz:objective:start-->
## Objective

Bring back, on the controls model, the UI that IMPL-0029 removed with
the findings model, and fix what rc.11 showed was rough:

- a Findings page over a new `GET /results`, filterable by control, rule, org and decision, with a deep-linked drawer and Re-evaluate;
- an expandable orgs tree whose children are repositories;
- the richer fleet panels (KPI strip with deltas, finding age, worst controls, parked by reason, compliance by control with sparklines);
- CSV export on every table;
- a curated changelog on the status page;
- remediation-gated panels that show an empty state until IMPL-0030;
- the INV-0023 repository-page fixes (rule tone, name crumb, next due, remediable wording) and the stale status screenshot.

The picture of the result is `ui/design/repo-guardian-mockup.html`,
accepted with DESIGN-0037. Every task that says "as the mockup draws
it" means that file's route. Where a page has to differ, the task
changes the mockup in the same commit; the plan never builds past it.

**Implements:** DESIGN-0037 (all sections; OQ1 = keep both words,
OQ2–OQ10 = (a)) and INV-0023 F1, F2, F3, F4 and F7.3. F5 and F6 are
already done (PR #221, `eda8692`).

<!--docz:objective:end-->

<!--docz:scope:start-->
## Scope

<!--docz:in-scope:start-->
### In Scope

- **API, all additive except OQ1:** `GET /results`, `Repository.compliance`, `Summary.parked_by_reason`, `Summary.finding_age`, `Summary.controls_failing`, `aggregate=` on `/compliance/history`, each with its sqlc query in `queries_controls/api_*.sql`, `api.Reader` method, handler, spec, regenerated server and client, and test cases.
- **Spec description fixes:** rule statuses on `Event.from_status`/`to_status`, and `suspended` in `ParkReason`.
- **Next due:** persist `repositories.next_due_at` from `EvaluationWorkflow`, version-gated, with a recaptured replay history (INV-0023 F3).
- **UI:**
  - the `rule` and `tag` tone rows and `RuleStatusBadge`;
  - a view-supplied breadcrumb label;
  - restored `lib/csv.ts`, range tabs on `?days=`, the header org scope and the ⌘K palette;
  - the Findings, Orgs tree and Fleet pages and the control-detail cards;
  - the changelog card and its `lint-ui` gate.
- **e2e and docs:** seed growth, specs, axe, the status screenshot (`docs/usage/img/ui/status.png` too), `docs/usage/ui.md`, `docs/usage/api.md`, CLAUDE.md.
- **CI:** the `ui` paths filter gains the chart's `Chart.yaml` and the changelog, so the version gate runs on a release-bump PR.

<!--docz:in-scope:end-->

<!--docz:out-of-scope:start-->
### Out of Scope

- **Remediation itself and its rows** (IMPL-0030). The gated panels ship here with empty states; their data is Phase 9 (OQ10).
- **INV-0023 F7.1** (`STORE_DSN_<ROLE>` unread) **and F7.2** (`checks` never pruned) by default (OQ11).
- **The compliance drivers map and per-repository metrics** (DESIGN-0037 non-goals).
- **Any write path beyond the existing re-evaluate POST.**
- **CSP, session or BFF proxy changes.** `/results` is a GET and rides the existing `/api/*` proxy.
- **New tables.** One index migration may land, decided by OQ12.

<!--docz:out-of-scope:end-->
<!--docz:scope:end-->

## Pre-implementation audit

An audit against the code (2026-10-18) checked DESIGN-0037's
assumptions. What it changed in this plan:

| Finding | Effect on the plan |
|---------|--------------------|
| `HistoryPoint` requires `org`, `control_id`, `decision`; a fleet or control aggregate has no org or control | OQ1; task 2.1 |
| `control_results` has no `decision` (it is `control_assignments.decision`); rule results are the `rule_results` table | 2.2 joins both |
| `Summary.compliance.non_compliant` counts the posture *bucket* (stale, parked and suspended rows leave it), raw `control_results.status` does not | OQ2; 2.2 reuses the bucket `CASE` from `compliance.sql` |
| No paged endpoint returns a `total`; `store.Page[T]` has none | OQ3; 2.2 |
| `RecordEvaluation` runs before `scheduleNext` computes `NextDue` (a SideEffect jitter); reordering breaks `evaluation_first_run.json` replay | OQ4; Phase 3 |
| The UI image builds with context `ui/`, so a root `CHANGELOG.yaml` is not in it; the build is Vite 8, which has no YAML loader | OQ5, OQ6; Phase 7 |
| The CI `ui` filter is `ui/**` + `api/openapi.yaml`, so a chart-only bump PR never runs `lint-ui` | 7.4 |
| `GroupGrid` shares one denominator (tracked) for percent and shading, nests children in an inner table and is unused | OQ8; Phase 5 |
| `RangeTabs`, `OrgScope`, `CommandPalette`, `csv.ts`, `range.ts` were deleted in `7ad928f`; `RangeTabs` bound `?range=` strings, and `csv.ts` carried findings-era helpers | 1.5–1.8 restore and rework them |
| `BucketBadge`/`DecisionBadge` keep their own tone maps outside `toneFor` | 1.1 folds them in |
| `TestAPIPerf` prints no plans and seeds no `rule_results` or snapshots | 2.7; OQ12 |
| `ParkReason` description omits `suspended`; `Event` statuses document control statuses only | 2.8 |
| The e2e seed installs the remediate App on acme | OQ13 |
| `docs/usage/img/ui/status.png` is a hand copy of the e2e snapshot | 8.4 copies it after re-rendering |

## How the phases are ordered

Phase 1 fixes what rc.11 got wrong and rebuilds the shared pieces the
later pages compose from (tones, crumbs, CSV, range tabs, org scope,
palette). Phase 2 is the Go side: every API addition with its tests,
so the page phases only consume. Phase 3 is the one workflow change,
isolated because it needs a version gate and a recaptured history.
Phases 4 to 6 are the pages in the design's order: Findings first
(it needs the most new API), then the Orgs tree, then the Fleet panels.
Phase 7 is the changelog, which touches the build, `lint-ui` and CI.
Phase 8 is the e2e seed, axe, screenshots and docs. Phase 9 fills the
remediation-gated panels and waits on IMPL-0030 (OQ10). Phase 10 is
human-run.

Each code phase ends with `make lint`, `make test` and, where UI
changed, `make lint-ui`, `make test-ui` and `make test-ui-e2e` green,
with a commit per numbered task. The plan lands as one PR into `v2`
with the `dont-release` label (Rule 6, one PR per IMPL).

## Implementation Phases

<!--docz:phase:start-->
### Phase 1: rc.11 fixes and UI foundations

The INV-0023 fixes and the shared pieces later phases compose from.
Nothing here needs new API.

<!--docz:tasks:start-->
#### Tasks

- [ ] 1.1 **Tone table (INV-0023 F1).** In `ui/web/src/lib/format.ts`, add
  `rule` (`pass` ok, `fail` bad, `error` bad, `unknown` warn,
  `not_applicable` mute), `bucket`, `decision` and `tag` (`new` ok,
  `improved` info, `fixed` warn, `breaking` bad, areas outline) to
  `ToneField`. Move `BucketBadge`'s `bucketTones` and `DecisionBadge`
  in `components/posture.tsx` onto `toneFor`, and add
  `RuleStatusBadge`. Extend `format.test.ts` with every rule value,
  including a failing rule rendering `bad`.
- [ ] 1.2 **Repository page rule badges.** The repository page's `ruleColumns`
  (`views/repositories.tsx`) and the Events tab use `RuleStatusBadge`
  when `rule_id` is set and `StatusBadge` otherwise.
- [ ] 1.3 **Breadcrumb label (INV-0023 F2).** Add a breadcrumb label
  context in `components/page/Breadcrumbs.tsx` that a view sets, and
  have the repository view set `org/name` from `useRepository`. The
  crumb falls back to the param while loading, or reads `…` for
  numeric params. Add an `/app/findings` crumb entry. Unit test: the
  crumb reads `acme/widgets`, never `1`.
- [ ] 1.4 **Remediable wording (INV-0023 F4).** Rename the rule column
  `Remediates` to `Remediable rule`. Under `remediable no`, the facts
  card adds the hint "Remediation App has no access to this
  repository", as the mockup draws it.
- [ ] 1.5 **CSV.** Restore `lib/csv.ts` and `csv.test.ts` from `7ad928f^`
  with only `csvCell`, `tableCsv` and `downloadCsv`; drop the
  findings-era `columns`/`findingsCsv`. Add an `ExportButton` (page,
  filter and date in the filename) and put it on every existing table
  card: controls, control by-org, orgs, org controls, exclusions,
  repositories, evaluation log and events.
- [ ] 1.6 **Range tabs on `?days=`.** Restore `components/page/RangeTabs.tsx`
  on `?days=` with numeric values 1, 7, 30 and 90 (default per OQ9).
  The search validators parse a number, so the History numeric-search
  rule holds. Add `validateSearch` to the fleet, control, org and
  history routes, and drop the old `trendFrom` import. Include a unit
  test for the parse.
- [ ] 1.7 **Header org scope (OQ7).** Restore `components/page/OrgScope.tsx`
  as the page-header popover writing `?org=`, on Fleet, Controls,
  control detail, Findings and History. Hooks pass `org` through to
  the API.
- [ ] 1.8 **⌘K palette (DESIGN-0037 OQ9).** Restore
  `components/shell/CommandPalette.tsx` and `palette.test.tsx` on the
  surviving `ui/command.tsx`, over `/repositories?q=` (debounced),
  `/controls` and `/orgs`, and bind ⌘K/Ctrl-K with
  `useKeyboardShortcut`. The top-bar Search button opens it.
- [ ] 1.9 **Nav.** `nav.ts` gains a `badge` field and the `/findings` path,
  hidden until Phase 4 lands the route. Then add the IMPL-0031 6.3
  keyboard pass on the current pages: tab order, visible focus, and
  Escape closing popovers.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- A failing rule renders red on the repository page and in its events
  (unit test and one Playwright assertion on the tone class).
- The repository crumb reads `org/name`.
- Every table card has an Export that downloads its rows with formula
  cells defused.
- ⌘K opens the palette from every authenticated page, and `?days=` and
  `?org=` round-trip through the URL as a number and a string.
- `make lint-ui`, `make test-ui` and `make test-ui-e2e` pass.

<!--docz:criteria:end-->
<!--docz:phase:end-->

<!--docz:phase:start-->
### Phase 2: API additions

Each task is the full vertical slice: `api/openapi.yaml` →
`make generate-api` → `queries_controls/api_*.sql` →
`make generate-sql` → `postgres.APIReader` + `api.Reader` method →
handler → a case in `TestEndpoints_ValidateAgainstTheSpec` (it fails
on any operation without one) → `make generate-ui-api` →
`make lint-api lint-sql`. Every new query lives in an `api_*.sql` file
so `TestAPIQueries_AreScoped` lints its scope predicate.

<!--docz:tasks:start-->
#### Tasks

- [ ] 2.1 **`/compliance/history?aggregate=none|fleet|org|control`**
  (default `none`, today's rows). New queries sum the ten bucket
  columns of `compliance_snapshots` per UTC day (`snapshot_at::date`,
  not the raw timestamp, so per-org snapshot times on one day
  collapse) and per org or control, and compute `percent`/`coverage`
  with `floor_percent` in SQL, never averaging. The response shape
  follows OQ1. Store tests:
  - the `fleet` series equals summing `none` with
    `ControlPosture.Plus` on the same fixture;
  - the `org` series agrees with `ControlCompliance` for the latest
    day.
- [ ] 2.2 **`GET /results`.**
  - **Query** `api_results.sql` over `control_results` ⋈
    `control_assignments` ⋈ `repositories` ⋈ `repository_policy_state`,
    with the posture bucket computed by the same `CASE` as
    `compliance.sql`. It filters `status` or `bucket` (OQ2),
    `control`, `org`, `decision` (from `control_assignments`), and
    `rule`: an `EXISTS` on `rule_results` with status `fail` or
    `error`.
  - **Order and paging:** sort `since_asc` (default) or `since_desc`
    on `(last_changed_at, repository_id, control_id)`, with a keyset
    cursor bound to the filter hash (`cursor.go`).
  - **Rows:** each `ResultRow` carries repository id/org/name, control
    id/version, decision, status, bucket, `failing_rules[]`, `reason`
    (the first failing rule's evidence `reason`, else its
    `evidence_kind`) and `last_changed_at`.
  - **`total`:** computed per OQ3.
  - **Tests:** integration tests for scope (two-org authz), keyset
    paging with ties on `last_changed_at`, each filter, and `total`
    matching the filter. A parity test proves that with no filters,
    `total` for non-compliant equals `Summary.compliance.non_compliant`
    on the fixture.
- [ ] 2.3 **`Repository.compliance`** (`Posture` over the repository's
  assignments, same bucket `CASE`) on `/repositories` and
  `/repositories/{id}`, in one grouped query per page, not N+1.
  Integration test: one repository with mixed buckets.
- [ ] 2.4 **`Summary.parked_by_reason` and `Summary.controls_failing`**,
  computed in Go in `GetSummary` from the `store.SummaryView` it
  already reads (`Repositories` per park reason; `Compliance` rows
  with `non_compliant > 0`, counted by distinct control). No new SQL.
  `apitest` cases.
- [ ] 2.5 **`Summary.finding_age`**: six fixed buckets (`<1d`, `1–7d`,
  `7–30d`, `30–90d`, `90–180d`, `180d+`) of
  `now() − last_changed_at` over results in the `non_compliant`
  bucket, in `api_summary.sql` with the scope predicate. The bucket
  sum equals `Summary.compliance.non_compliant` (test). The fixture
  spans every boundary.
- [ ] 2.6 **Client paging.** `useRepositories` in `ui/web/src/api/queries.ts`
  becomes a `useInfiniteQuery` taking `org`, `active` and `q` (the
  tree, the Parked tab and the palette use it). It replaces today's
  single `limit: 100` fetch. Document in the spec that `q` is a name
  prefix.
- [ ] 2.7 **Perf fixture (OQ12).** Extend `TestAPIPerf` to seed
  `rule_results` and 90 days of `compliance_snapshots` at fleet size,
  add `/results` (each filter) and the history aggregates to the
  measured set, and log `EXPLAIN (ANALYZE, BUFFERS)` for `/results`.
  Record the numbers in this doc. If a sequential scan shows, add
  `00005_results_index.sql` (`control_results (control_id, status,
  last_changed_at)`), bump `ControlsSchemaVersion` to 5 and extend the
  controls dry-run test.
- [ ] 2.8 **Spec descriptions:** `Event.from_status`/`to_status` document
  rule statuses on rule-level events; `ParkReason` lists `suspended`.
  Bump `info.version` (2.1.0) and document every addition in
  `docs/usage/api.md`.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- `make lint-api lint-sql` and `make generate-api generate-sql
  generate-ui-api` leave no diff.
- `make test` and `make test-integration` pass, including the new
  parity tests (findings `total` = `Summary.compliance.non_compliant`;
  `finding_age` sum = the same; `aggregate=fleet` = summed `none`).
- `TestEndpoints_ValidateAgainstTheSpec` covers `/results`, and
  `TestAPIQueries_AreScoped` covers every new query.
- `TestStatus_PrivacyGuard` is unchanged, because nothing new touches
  `/status`.

<!--docz:criteria:end-->
<!--docz:phase:end-->

<!--docz:phase:start-->
### Phase 3: Persist next due

INV-0023 F3: `repositories.next_due_at` is selected by the API but
nothing on the controls line writes it. The evaluator role already
holds `UPDATE (next_due_at)` (`00001_core.sql`).

<!--docz:tasks:start-->
#### Tasks

- [ ] 3.1 **Store.** `store.Controls.SetNextDue(ctx, repositoryID,
  at time.Time)` with a sqlc query, plus an integration test that runs
  as `rg_evaluator` (the grant, not the owner).
- [ ] 3.2 **Workflow.** Write next due per OQ4, behind
  `workflow.GetVersion(ctx, "next-due-v1", workflow.DefaultVersion,
  1)`. It covers every path that calls `scheduleNext`: recorded,
  failed and skipped. The `DefaultVersion` branch keeps today's
  command order exactly.
- [ ] 3.3 **Replay.**
  - `TestReplay_CapturedHistories` still passes on
    `evaluation_first_run.json`.
  - Capture a second history on the new version with
    `go test -tags integration -run TestIntegration_OneEvaluation
    ./internal/activities -update-histories` and commit it beside the
    old one. Do not overwrite the old history.
- [ ] 3.4 **Parked repositories.** A park ends the workflow run, so
  `ParkRepository` clears `next_due_at` and the card reads "— parked".
  Integration test.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- After one evaluation in the integration test, `/repositories/{id}`
  returns a `next_due_at` inside the jitter window of `EVAL_INTERVAL`.
- Both histories replay.
- A parked repository returns none.

<!--docz:criteria:end-->
<!--docz:phase:end-->

<!--docz:phase:start-->
### Phase 4: Findings page and control detail

As the mockup's `#/findings` and `#/controls/<id>` draw them.

<!--docz:tasks:start-->
#### Tasks

- [ ] 4.1 **Hook and route.** Add `useResults(filter)` as a
  `useInfiniteQuery` over `/results`, with "Load more" and "N loaded
  of M" from `total`. Add the `/app/findings` route with
  `validateSearch` for `control`, `rule`, `org`, `decision`,
  `pr_stale`, `sort` and `open`. The page always asks for the
  non-compliant results (OQ2), and the nav entry un-hides with the
  `Summary.compliance.non_compliant` count badge.
- [ ] 4.2 **Filter chips** (restore `components/filters/*` from
  `7ad928f^`, reworked):
  - Control comes from `/controls`, and Rule enables once a control is
    chosen, listing that control's rule ids.
  - Org and Decision.
  - Stale PR is disabled with an "IMPL-0030" note until Phase 9.
  - Changing the control clears the rule.
- [ ] 4.3 **Table:** columns Repository (name, org beneath), Control
  (slug, version, non-baseline decision), Failing rules (red badges plus
  the first failing rule's evidence headline from `describeEvidence`),
  Remediation (`—` with the gated tooltip until Phase 9) and Since
  (relative, absolute on hover). Add the sort toggle and Export, which
  pages `/results` up to 10,000 rows with a notice when the cap cuts
  the export short (DESIGN-0037 OQ6).
- [ ] 4.4 **Finding drawer** (restore `views/findings/drawer.tsx` on
  `DetailDrawer`), opened by `?open=<repository_id>/<control>`. It
  shows:
  - status, remediation and last evaluated;
  - the control's rule table from `/repositories/{id}/controls`, with
    `RuleStatusBadge`;
  - the evidence of each failing rule as text;
  - a timeline from `/repositories/{id}/events` filtered to the
    control, plus the last two evaluations;
  - Re-evaluate, sharing the repository page's `EvaluateButton`, and
    "Open repository →".

  Escape and the scrim close it. A11y names come from the repository
  and control.
- [ ] 4.5 **Control detail cards:**
  - **Top failing rules:** a bar list of the control's findings by
    failing rule from `/results?control=`, grouped client-side over
    the fetched page, or a count per rule if 2.2 exposes one. Each row
    links to `/findings?control=&rule=`.
  - **Oldest findings:** ten rows of `/results?control=&sort=since_asc`
    that open the drawer.
  - **"Findings for this control"** button.
  - **The compliance chart** switches to `aggregate=control` with the
    delta over the range.
- [ ] 4.6 **Unit tests:** filter ↔ URL round trip, rule chip enablement,
  the drawer id parse, the export cap notice.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- `/findings` lists only non-compliant results and its filters
  round-trip through the URL.
- `M` equals the nav badge for the same scope.
- The drawer deep link survives a reload, and Re-evaluate returns 202
  in e2e.
- The control page's Oldest findings opens the same drawer.

<!--docz:criteria:end-->
<!--docz:phase:end-->

<!--docz:phase:start-->
### Phase 5: Orgs tree

As the mockup's `#/orgs` draws it (OQ8).

<!--docz:tasks:start-->
#### Tasks

- [ ] 5.1 **Rework `components/ui/group-grid.tsx`:**
  - child rows render in the same `<tbody>` (no nested table), so
    columns align;
  - each cell carries its own numerator and denominator, and the
    column header label is a prop;
  - the footer is supplied by the caller;
  - shading uses `share = value / (compliant + non_compliant)`, and an
    unmeasured cell renders "no data", never 0% or 100%.

  Rewrite `tables.test.tsx` for the new contract.
- [ ] 5.2 **Load children on expand.** Each org's children come from
  `/repositories?org=&active=true` (keyset, "Load more" inside the
  group, "showing N of M"). Collapsed groups fetch nothing.
  Expand all / Collapse all.
- [ ] 5.3 **Columns:** Org / repository, Tracked (org: active
  repositories; repository: assignments), Compliant, Non-compliant,
  Open PRs (`OrgSummary.open_prs`; repository rows `—` until Phase 9)
  and Parked. The page subtitle says the counts are assignments, as
  the mockup words it. The footer sums the visible org rows with
  `addPosture`.
- [ ] 5.4 **Toolbar:** KPI chips (Tracked, Non-compliant, Parked), tabs
  Compliance / Parked / PR activity, the repository filter (`?q=`,
  applied to loaded children) and Export.
  - The **Parked** tab lists parked repositories with reason and
    parked-at, grouped by org (`/repositories?org=&active=false`).
  - The **PR activity** tab shows the gated empty state until Phase 9.
- [ ] 5.5 **Unit tests:** footer sums equal the `/orgs` totals; the
  shading denominator excludes non-measured buckets; "no data" renders
  for an excluded repository.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- Expanding an org shows its repositories with aligned columns.
- No cell's colour disagrees with its percentage.
- The footer matches `/orgs`, and an excluded repository reads "no
  data".

<!--docz:criteria:end-->
<!--docz:phase:end-->

<!--docz:phase:start-->
### Phase 6: Fleet panels

As the mockup's `#/` draws it, panels 1 to 8 of DESIGN-0037 §Fleet
panels.

<!--docz:tasks:start-->
#### Tasks

- [ ] 6.1 **Compliance card:**
  - KPI strip: Tracked; Compliant % with a delta in points over the
    range; Non-compliant with `controls_failing`; Parked with its
    reason count; Snapshots and cadence.
  - Series pills (compliance %, findings, not applicable, unknown)
    choose what the area chart draws from `aggregate=fleet`. The pill
    choice persists as `?series=`.
  - Fewer than two points renders no delta.
- [ ] 6.2 **Finding age:** `DistBar` over `Summary.finding_age` with the
  "N% under 30d" pill and a link to `/findings?sort=since_asc`.
- [ ] 6.3 **Worst controls:** the five lowest measured percentages from
  `/controls`, each with its delta from `aggregate=control`.
- [ ] 6.4 **Parked by reason:** `BarList` over `Summary.parked_by_reason`.
- [ ] 6.5 **Compliance by control:** the table gains N/A, %, and a
  sparkline from one `aggregate=control` request (not one per row).
- [ ] 6.6 **Orgs card:** with one org in scope it becomes Exclusions by
  reason from `OrgDetail.exclusions`. Export on both.
- [ ] 6.7 **Assignments** stays (rc.11's bucket card).
- [ ] 6.8 **Open PRs by age** and **PR throughput (14d)** render the gated
  empty state decided from `Status.apps`. With a remediate App
  installed and no data, they read "No open pull requests".
- [ ] 6.9 **Org detail:** the compliance chart switches to
  `aggregate=org`.
- [ ] 6.10 **Unit tests:** delta with 0, 1 and 2+ points; the `?series=`
  parse; the gated-state decision table (no App → "not enabled", App
  and none open → "No open pull requests").

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- Every fleet panel the mockup draws renders on the e2e seed, and
  every history-backed panel follows the range tabs and org scope.
- The fleet makes one history request per aggregate, not one per
  control.

<!--docz:criteria:end-->
<!--docz:phase:end-->

<!--docz:phase:start-->
### Phase 7: Status page changelog

DESIGN-0037 §Status page changelog, with the build constraints from
the audit.

<!--docz:tasks:start-->
#### Tasks

- [ ] 7.1 **The changelog file.** `CHANGELOG.yaml` at the location OQ5
  chooses, seeded with rc.8 to rc.11 (rc.8: fleet history refetch fix;
  rc.9: design refresh; rc.10: repository page polish; rc.11: controls
  evaluation) with closed-set tags.
- [ ] 7.2 **Into the bundle** per OQ6. The UI also needs its own
  version at build time (a `VITE_` define from `package.json` or the
  chart appVersion) so the card marks the running release without
  reading `/status`.
- [ ] 7.3 **The check.** `ui/scripts/check-changelog.ts`, added to the
  `lint-ui` chain, fails when:
  - the newest entry's `version` is not
    `charts/repo-guardian/Chart.yaml` `appVersion`;
  - a tag is outside the closed set;
  - dates do not descend;
  - a version repeats.

  It has a `bun test` file with failing fixtures for each rule.
- [ ] 7.4 **CI.** In `.github/workflows/ci.yml`, the `ui` paths filter
  gains `charts/repo-guardian/Chart.yaml` and the changelog path, so a
  release-bump PR runs the gate. Verify it with a dry PR diff in the
  task note.
- [ ] 7.5 **The card.** The status page's Changelog card shows the newest
  five, "All releases" to the GitHub releases page, and a "running"
  badge on the bundle's version. The card makes no API call, so
  `TestStatus_PrivacyGuard` is untouched.
- [ ] 7.6 **Release process.** Document the release-bump step (add an
  entry with the chart bump) in CLAUDE.md §Release and
  `docs/operations/` where the rc procedure lives.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- Bumping `appVersion` without an entry fails `make lint-ui` locally
  and in CI.
- The status page shows the entries with "running" on rc.N.
- The Docker UI build succeeds (`make docker-build-ui`).

<!--docz:criteria:end-->
<!--docz:phase:end-->

<!--docz:phase:start-->
### Phase 8: e2e, accessibility, screenshots and docs

<!--docz:tasks:start-->
#### Tasks

- [ ] 8.1 **Seed.** `ui/e2e/seed.sql` grows:
  - several controls with failing rules and distinct evidence kinds,
    and an `error` rule;
  - parked repositories with three reasons;
  - `last_changed_at` across all six age buckets;
  - 30 days of snapshots for two orgs;
  - an excluded repository.

  globex stays invisible to ada.
- [ ] 8.2 **Specs** in `ui/e2e/ui.spec.ts`. Each one asserts globex never
  appears:
  - Findings filters round-trip, and the drawer deep link and
    Re-evaluate work.
  - Export downloads the expected row count.
  - Tree expand/collapse and the footer.
  - Fleet panels read the seeded numbers.
  - The changelog card.
  - The crumb and next due on the repository page.
  - A failing rule's badge carries the `bad` tone.
- [ ] 8.3 **Axe.** `a11y.spec.ts` loops over `/findings`, `/findings?open=`,
  `/orgs` (expanded), the fleet, and the status page with the
  changelog.
- [ ] 8.4 **Screenshots.** Re-render the snapshots in the pinned
  Playwright image (`make test-ui-e2e-update-snapshots`), then copy
  `ui/e2e/__screenshots__/status.png` to `docs/usage/img/ui/status.png`
  (INV-0023 F7.3). Add the findings and orgs pages to the visual spec
  if OQ5 of IMPL-0031 still allows it.
- [ ] 8.5 **Docs:**
  - `docs/usage/ui.md`: the Findings page, the tree, the fleet panels,
    export and the changelog;
  - `ui/README.md`;
  - CLAUDE.md: the DESIGN-0037 contracts (findings = bucket
    `non_compliant`; the changelog gate; `aggregate=` sums in SQL).
- [ ] 8.6 **Close the related docs.** Mark INV-0023 Concluded with each
  finding's outcome, and set DESIGN-0037 to Implemented
  (`docz update`).

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- `make test-ui-e2e` passes with the new specs, axe reports no
  violations on the new pages, and the status screenshot shows the
  controls status page in both places.

<!--docz:criteria:end-->
<!--docz:phase:end-->

<!--docz:phase:start-->
### Phase 9: Remediation data for the gated panels

Needs IMPL-0030's `remediations` rows. Handled per OQ10; by default
these tasks are carried into IMPL-0030 and marked here as such.

<!--docz:tasks:start-->
#### Tasks

- [ ] 9.1 **`Summary.open_prs_by_age`** (`<1d`, `1–7d`, `7–30d`, `30d+`
  of open pull-request remediations by `created_at`). The fleet card
  shows "N over threshold → `/findings?pr_stale=true`".
- [ ] 9.2 **`GET /remediations/activity?days=1..90`** (default 14) →
  `[{day, opened, closed}]`, feeding the PR throughput chart.
- [ ] 9.3 **`/results?pr_stale=`** and `ResultRow.remediation` (state,
  PR number/url, age). The Findings Remediation column and Stale PR
  chip un-gate, and `sort=pr_age` joins the sorts.
- [ ] 9.4 **Orgs PR activity tab** and repository-row Open PRs.
- [ ] 9.5 **e2e:** seeded remediations, panels reading them.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- With remediation installed, every gated panel shows data.
- With it absent, every gated panel still reads "not enabled".

<!--docz:criteria:end-->
<!--docz:phase:end-->

<!--docz:phase:start-->
### Phase 10: rc image and homelab review

`deferred - human required`.

<!--docz:tasks:start-->
#### Tasks

- [ ] 10.1 Merge into `v2`, cut `v2.0.0-rc.N` by hand with its
  changelog entry, and dispatch `ghcr.yml`.
- [ ] 10.2 Deploy to the homelab. Walk Findings (filters, drawer,
  re-evaluate), the Orgs tree, Fleet, the palette, and the status
  changelog against the mockup, and confirm next due shows on a
  repository. File divergences as tasks here.
- [ ] 10.3 Confirm the Keycloak group split still scopes `/results` and
  the tree (carries IMPL-0031 8.3).

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- The rc UI runs in the homelab with no console errors or CSP reports,
  and the pages match the accepted mockup.

<!--docz:criteria:end-->
<!--docz:phase:end-->

<!--docz:file-changes:start-->
## File Changes

| File | Action | Description |
| ---- | ------ | ----------- |
| `api/openapi.yaml`, `internal/api/gen/api.gen.go` | Modify | `/results`, `aggregate=`, Summary and Repository fields, descriptions, 2.1.0 |
| `internal/store/postgres/queries_controls/api_results.sql`, `api_history.sql`, `api_summary.sql`, `api_repositories.sql` | Create/Modify | new scoped queries |
| `internal/store/postgres/controlsdb/*` | Regenerate | sqlc |
| `internal/store/postgres/apireader_controls.go`, `internal/store/api_views.go` | Modify | reader methods, view types |
| `internal/store/postgres/migrations_controls/00005_results_index.sql`, `controls_migrate.go` | Create/Modify | only if OQ12 says so |
| `internal/api/server.go`, `controls.go`, `results.go` | Modify/Create | `api.Reader` methods, handlers |
| `internal/store/controls.go`, `queries_controls/*`, `internal/activities/evaluate.go` | Modify | `SetNextDue`, park clears it |
| `internal/workflows/evaluation.go`, `evaluation_types.go`, `testdata/histories/*` | Modify/Create | `next-due-v1`, new history |
| `internal/store/postgres/api_perf_test.go`, `*_integration_test.go`, `apiendpoints_integration_test.go` | Modify | perf seed, parity, endpoint cases |
| `ui/web/src/lib/format.ts`, `components/posture.tsx`, `components/badges.tsx` | Modify | tone rows, `RuleStatusBadge` |
| `ui/web/src/components/page/Breadcrumbs.tsx`, `RangeTabs.tsx`, `OrgScope.tsx` | Modify/Restore | crumb label, `?days=`, `?org=` |
| `ui/web/src/components/shell/CommandPalette.tsx`, `nav.ts`, `TopBar.tsx` | Restore/Modify | palette, findings nav badge |
| `ui/web/src/lib/csv.ts` (+ test), `components/ExportButton.tsx` | Restore/Create | CSV export |
| `ui/web/src/views/findings.tsx`, `views/findings/drawer.tsx`, `components/filters/*` | Restore/Rework | findings page |
| `ui/web/src/views/controls.tsx`, `orgs.tsx`, `fleet.tsx`, `repositories.tsx`, `status.tsx`, `history.tsx` | Modify | page work |
| `ui/web/src/components/ui/group-grid.tsx`, `tables.test.tsx` | Modify | tree contract |
| `ui/web/src/api/queries.ts`, `schema.gen.ts` | Modify/Regenerate | `useResults`, aggregates |
| changelog file (OQ5), `ui/scripts/check-changelog.ts` (+ test), `ui/package.json` | Create/Modify | changelog and gate |
| `.github/workflows/ci.yml` | Modify | `ui` paths filter |
| `ui/e2e/seed.sql`, `ui.spec.ts`, `a11y.spec.ts`, `__screenshots__/*`, `docs/usage/img/ui/status.png` | Modify | e2e, screenshots |
| `ui/design/repo-guardian-mockup.html` | Modify | only where a page must differ |
| `docs/usage/ui.md`, `docs/usage/api.md`, `ui/README.md`, `CLAUDE.md` | Modify | docs |

<!--docz:file-changes:end-->

<!--docz:testing:start-->
## Testing Plan

- **Go unit:** `GetSummary` derivations (parked by reason, controls
  failing), history aggregate handler parameter validation, cursor
  binding for `/results` sort and filters.
- **Go integration** (`make test-integration`):
  - each new query's scope;
  - keyset ties;
  - the three parity tests;
  - `SetNextDue` as `rg_evaluator`, and park clearing it;
  - the two-org authz test on `/results`;
  - endpoint spec validation.
- **Replay:** `TestReplay_CapturedHistories` over the old and the new
  evaluation history.
- **Perf:** `go test -tags 'integration perf' -run TestAPIPerf -v
  ./internal/store/postgres/`, with numbers and the `/results` plan
  recorded under task 2.7.
- **UI unit** (`make test-ui`): tone rows, crumb label, CSV defusing,
  `?days=`/`?org=`/`?series=` parsing, filter round trip, tree sums and
  shading, deltas, the gated-state decision, the changelog check.
- **e2e** (`make test-ui-e2e`): the specs in 8.2, axe in 8.3, and the
  visual snapshots in the pinned image.
- **Non-vacuous checks.** For the parity tests, the tone assertion and
  the changelog gate, break the code under test once, confirm the test
  fails, then restore.

<!--docz:testing:end-->

<!--docz:dependencies:start-->
## Dependencies

- DESIGN-0037 and its mockup accepted (PR #221 merged into `v2`).
- IMPL-0029 merged (done, rc.11).
- IMPL-0030 for Phase 9 only (OQ10, OQ14).
- The pinned Playwright image for screenshots (`ui/scripts/visual.ts`).

<!--docz:dependencies:end-->

## Open Questions

### OQ1: The shape of aggregated history points

`HistoryPoint` requires `org`, `control_id` and `decision`. A fleet
point has none of them, and a control point has no org.

- (a) ✅ recommended: **loosen `HistoryPoint`**. `org`, `control_id` and
  `decision` become optional, and the description states which are
  present per `aggregate`. One schema and one hook; the only consumer
  is our UI and spec 2.x is pre-release. It does relax a required
  field, contrary to DESIGN-0037's "nothing changes shape", so record
  it there.
- (b) A separate `AggregatePoint` schema returned when `aggregate` is
  not `none` (a `oneOf` on the response). Strictly additive, but the
  generated client needs a discriminator, and the strict server's
  `oneOf` response is awkward.
- (c) A new endpoint `/compliance/trend?by=fleet|org|control` leaving
  `/compliance/history` untouched. Additive and clean, but two history
  endpoints to explain.
- other:

### OQ2: What /results filters on: status or bucket

The nav badge and DESIGN-0037's "N of M" read
`Summary.compliance.non_compliant`, which is the posture *bucket*.
Raw `control_results.status = non_compliant` also includes stale
results and rows under access-denied parks and suspended installs.

- (a) ✅ recommended: **filter on `bucket`** (the ten posture buckets,
  computed by the shared `CASE`). The Findings page asks for
  `bucket=non_compliant`, and `ResultRow` carries both `status` and
  `bucket`. The badge, `total`, `finding_age` and the page agree by
  construction. DESIGN-0037's vocabulary is refined to "a finding is a
  result in the non-compliant bucket".
- (b) Filter on raw `status`. Simpler SQL, but the page shows stale and
  parked results the fleet count does not, and the counts disagree.
- (c) Both parameters. Flexible, but two ways to ask the same question.
- other:

### OQ3: When /results computes total

- (a) ✅ recommended: **count on the first page only** (no cursor), in
  the same read-only transaction. The client keeps it across "Load
  more". One count per filter change, and the number is stable while
  paging.
- (b) Count on every page. Always fresh, but repeats the count for
  each page.
- (c) No exact total: show "N loaded" and use the Summary count only
  when no filter is set. Cheapest, but filtered views lose "of M".
- other:

### OQ4: How next due is persisted

`RecordEvaluation` runs before `scheduleNext` draws the jitter, and
the failed and skipped paths never record.

- (a) ✅ recommended: **a `SetNextDue` local activity after
  `scheduleNext`** on every path, behind `GetVersion("next-due-v1")`.
  One write per iteration, uniform across recorded, failed and skipped,
  with no reordering of the existing commands.
- (b) Draw the jitter before recording and pass `NextDue` in
  `RecordEvaluationInput` (version-gated reorder). No extra activity,
  but the failed and skipped paths still need their own write, and
  the gate is more involved.
- (c) Drop `next_due_at` from the API and the card. No workflow
  change, but operators lose "when is this looked at next".
- other:

### OQ5: Where CHANGELOG.yaml lives

The UI image builds with context `ui/`.

- (a) ✅ recommended: **`ui/CHANGELOG.yaml`**. It is in the build
  context and the `ui/**` CI filter. The gate reads
  `../charts/repo-guardian/Chart.yaml` at lint time, never in Docker.
  It sits beside the root `CHANGELOG.md` (git-cliff), which stays the
  full commit log.
- (b) The root `CHANGELOG.yaml`, with the bake context widened to the
  repo root and a `.dockerignore` for the rest. As the design wrote
  it, but it changes the UI image build and its cache scope.
- (c) Generate it from git-cliff output. No hand-kept file, but the
  curated titles and summaries are the point.
- other:

### OQ6: How the changelog reaches the Vite bundle

Vite has no YAML loader.

- (a) ✅ recommended: **a build-time conversion.** A
  `ui/scripts/gen-changelog.ts` (Bun's native YAML) writes
  `web/src/generated/changelog.json`, committed, and
  `check-changelog.ts` fails on drift, the same pattern as
  `schema.gen.ts` and `check-api.ts`. No new dependency and no Vite
  plugin.
- (b) A small Vite plugin with a `yaml` dependency that parses at build
  time. No committed generated file, but a new dependency and plugin
  to maintain.
- (c) Author the changelog as JSON. Nothing to convert, but it is
  worse to hand-edit than YAML.
- other:

### OQ7: The header org scope

DESIGN-0037 said the header org scope was "kept", but `OrgScope.tsx`
was deleted in `7ad928f`. The accepted mockup draws it on Fleet,
Controls, Findings and History.

- (a) ✅ recommended: **restore it** (task 1.7) as the page-header
  popover writing `?org=`, because the mockup is the accepted spec.
- (b) Leave it out and filter by org inside each page (the Findings
  chip, the Orgs tree). Less to build, but the mockup changes in the
  same PR.
- other:

### OQ8: The org tree component

- (a) ✅ recommended: **rework the existing `GroupGrid`** (task 5.1)
  into flat child rows with per-cell denominators. It is unused, so
  there are no other callers to keep, and its tests come with it.
- (b) A new `OrgTree` component, leaving `GroupGrid` as is. A clean
  slate, but it leaves a dead component behind (delete it then).
- other:

### OQ9: Default range

- (a) ✅ recommended: **30 days**, matching the API's `days` default,
  the mockup and rc.11's history.
- (b) 90 days, the IMPL-0031 default. Longer trends, but four times
  the history rows on every fleet load.
- other:

### OQ10: The remediation data tasks and the one-PR rule

Phase 9 needs IMPL-0030's rows. This plan otherwise lands as one PR.

- (a) ✅ recommended: **carry Phase 9 into IMPL-0030.** Its tasks are
  added to IMPL-0030's plan and marked here "moved to IMPL-0030".
  IMPL-0032 ships the gated empty states, and IMPL-0030's PR fills
  them in beside the data it creates.
- (b) Keep Phase 9 here and hold this PR open until IMPL-0030 lands.
  One doc owns all the UI, but the rest of the work waits.
- (c) A follow-up IMPL just for Phase 9 after IMPL-0030. Separate and
  small, but a third doc for five tasks.
- other:

### OQ11: INV-0023 F7.1 and F7.2

INV-0023 assigned the unread `STORE_DSN_<ROLE>` and the `checks`
prune to IMPL-0030. The audit adds that pruning `checks` makes
`last_evaluated_at` read "never" for repositories idle longer than the
retention.

- (a) ✅ recommended: **keep both in IMPL-0030.** They are
  worker/credential and retention work, not UI, and the
  `last_evaluated_at` interaction should be designed with the prune.
- (b) Do them here as a Phase 0. They are known gaps fixed sooner, but
  they widen a UI PR into worker config and retention semantics.
- other:

### OQ12: The control_results index

- (a) ✅ recommended: **decide from task 2.7's plan output.** Add
  `00005` only if `/results` shows a sequential scan at fleet size.
  This follows DESIGN-0037's Data Model.
- (b) Add the index up front. One less decision, but a schema
  migration we may not need.
- other:

### OQ13: Testing the "not enabled" empty state

The e2e seed installs the remediate App on acme, so the gated panels
read "No open pull requests" there.

- (a) ✅ recommended: **unit-test the decision** ("not enabled" vs
  "No open pull requests" from `Status.apps`), and let e2e cover the
  installed case the seed already has. No second stack.
- (b) A second e2e seed or project without the remediate App. Both
  states end to end, but a second stack start.
- other:

### OQ14: Order against IMPL-0030

- (a) ✅ recommended: **IMPL-0032 before IMPL-0030.** The UI gaps and
  the INV-0023 fixes are visible on rc.11 today, and IMPL-0030 then
  lands into pages that already have their gated slots.
- (b) IMPL-0030 first, then IMPL-0032 with Phase 9 included. One pass
  over the UI, but rc.11's gaps stay for the length of IMPL-0030.
- (c) In parallel on separate branches. Fastest, but both touch
  `openapi.yaml`, the seed and the fleet view, so expect merge
  conflicts.
- other:

<!--docz:references:start-->
## References

- DESIGN-0037 (the design; its mockup `ui/design/repo-guardian-mockup.html`)
- INV-0023 (F1–F4, F7)
- DESIGN-0036 and IMPL-0031 (design language, the components and libs deleted in `7ad928f`)
- IMPL-0029 Phase 8 (the controls API and UI), IMPL-0030 (remediation)
- `internal/store/postgres/queries_controls/compliance.sql` (the bucket `CASE`)
- `internal/workflows/evaluation.go` (`record`, `scheduleNext`)

<!--docz:references:end-->
