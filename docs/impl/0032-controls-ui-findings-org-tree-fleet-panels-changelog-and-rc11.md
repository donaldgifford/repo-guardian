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
- [The split with IMPL-0030](#the-split-with-impl-0030)
- [Pre-implementation audit](#pre-implementation-audit)
- [How the phases are ordered](#how-the-phases-are-ordered)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: INV-0023 fixes (its own pull request)](#phase-1-inv-0023-fixes-its-own-pull-request)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: UI foundations](#phase-2-ui-foundations)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Findings page and control detail](#phase-3-findings-page-and-control-detail)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: Orgs tree](#phase-4-orgs-tree)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: Fleet panels](#phase-5-fleet-panels)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: Remediations view and remediation panels](#phase-6-remediations-view-and-remediation-panels)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
  - [Phase 7: Status page changelog](#phase-7-status-page-changelog)
    - [Tasks](#tasks-6)
    - [Success Criteria](#success-criteria-6)
  - [Phase 8: e2e, accessibility, screenshots and docs](#phase-8-e2e-accessibility-screenshots-and-docs)
    - [Tasks](#tasks-7)
    - [Success Criteria](#success-criteria-7)
  - [Phase 9: rc image and homelab review](#phase-9-rc-image-and-homelab-review)
    - [Tasks](#tasks-8)
    - [Success Criteria](#success-criteria-8)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
  - [OQ1: Where CHANGELOG.yaml lives](#oq1-where-changelogyaml-lives)
  - [OQ2: How the changelog reaches the Vite bundle](#oq2-how-the-changelog-reaches-the-vite-bundle)
  - [OQ3: The header org scope](#oq3-the-header-org-scope)
  - [OQ4: The org tree component](#oq4-the-org-tree-component)
  - [OQ5: Default range](#oq5-default-range)
  - [OQ6: Testing the "not enabled" empty state](#oq6-testing-the-not-enabled-empty-state)
  - [OQ7: Does the fixes pull request cut its own rc](#oq7-does-the-fixes-pull-request-cut-its-own-rc)
  - [Resolved by the split](#resolved-by-the-split)
- [References](#references)
<!-- markdownlint-restore -->
<!--toc:end-->

<!--docz:objective:start-->
## Objective

Bring back, on the controls model, the UI that IMPL-0029 removed with
the findings model, add the Remediations view, and fix what rc.11
showed was rough:

- a Findings page over `GET /results`, filterable by control, rule, org and decision, with a deep-linked drawer and Re-evaluate;
- an expandable orgs tree whose children are repositories;
- the richer fleet panels (KPI strip with deltas, finding age, worst controls, parked by reason, compliance by control with sparklines, open PRs by age, PR throughput);
- the Remediations view, the repository remediation panel and the status page's remediation backlog;
- CSV export on every table;
- a curated changelog on the status page;
- first, in its own small pull request, the INV-0023 repository-page fixes (rule tone, name crumb, remediable wording) and the stale status screenshot.

The picture of the result is `ui/design/repo-guardian-mockup.html`,
accepted with DESIGN-0037. Every task that says "as the mockup draws
it" means that file's route. Where a page has to differ, the task
changes the mockup in the same commit; the plan never builds past it.

**Implements:** DESIGN-0037's UI (OQ1 = keep both words, OQ2–OQ10 =
(a)), DESIGN-0032 D32's Remediations view as DESIGN-0037 restates it,
and INV-0023 F1, F2, F4 and F7.3. F5 and F6 are already done (PR #221,
`eda8692`). The API every page reads, and INV-0023 F3, F7.1 and F7.2,
are IMPL-0030's (see [the split](#the-split-with-impl-0030)).

<!--docz:objective:end-->

<!--docz:scope:start-->
## Scope

<!--docz:in-scope:start-->
### In Scope

- **The INV-0023 fixes** (Phase 1, a separate pull request): the `rule` tone row and `RuleStatusBadge`, a view-supplied breadcrumb label, the `Remediable rule` wording and hint, and the status screenshot in both places.
- **UI foundations:** restored `lib/csv.ts`, range tabs on `?days=`, the header org scope, the ⌘K palette, `useRepositories` paging, the nav entries.
- **Pages:** Findings and control-detail cards, the Orgs tree, the Fleet panels, the Remediations view, the repository remediation panel, the status backlog cell, the changelog card and its `lint-ui` gate.
- **e2e and docs:** seed growth (including remediations), specs, axe, screenshots, `docs/usage/ui.md`, `ui/README.md`, CLAUDE.md.
- **CI:** the `ui` paths filter gains the chart's `Chart.yaml` and the changelog, so the version gate runs on a release-bump PR.

<!--docz:in-scope:end-->

<!--docz:out-of-scope:start-->
### Out of Scope

- **Every Go change.** The API additions, persisting next due, and INV-0023 F7.1 (`STORE_DSN_<ROLE>`) and F7.2 (`checks` prune) are IMPL-0030's. This plan regenerates the UI client from IMPL-0030's spec and consumes it.
- **Remediation itself** (IMPL-0030).
- **The compliance drivers map and per-repository metrics** (DESIGN-0037 non-goals).
- **Any write path beyond the existing re-evaluate POST.**
- **CSP, session or BFF proxy changes.** Every new call is a GET on the existing `/api/*` proxy.

<!--docz:out-of-scope:end-->
<!--docz:scope:end-->

## The split with IMPL-0030

Decided 2026-10-18. The first draft of this plan carried its own API,
next-due and remediation-data phases. Those are Go work that shares
the controls schema, `openapi.yaml` and the `EvaluationWorkflow`
version gate with remediation, so they moved to IMPL-0030, and
IMPL-0030's UI task moved here:

| Was | Now |
|-----|-----|
| IMPL-0032 Phase 2 (API additions, 2.1–2.5, 2.7, 2.8) | IMPL-0030 Phase 7 (API for the controls UI) |
| IMPL-0032 2.6 (client paging) | stays: Phase 2 task 2.5 here |
| IMPL-0032 Phase 3 (persist next due, INV-0023 F3) | IMPL-0030 Phase 2, under the same version gate as `StartDueRemediations` |
| IMPL-0032 Phase 9 data (9.1–9.3) | IMPL-0030 Phase 7 |
| IMPL-0032 Phase 9 UI (9.3 UI, 9.4, 9.5) | Phases 3, 4 and 8 here |
| INV-0023 F7.1, F7.2 (old OQ11) | IMPL-0030 Phases 1 and 6 |
| IMPL-0030 7.5 (UI: Remediations view, repository panel, status backlog) | Phase 6 here |
| Old OQ1–OQ4, OQ12 (history shape, bucket vs status, total, next due, index) | IMPL-0030's open questions |

So the order is: Phase 1 here ships now, IMPL-0030 ships next with
every API this plan reads, and Phases 2 to 9 here build the UI on it.

## Pre-implementation audit

An audit against the code (2026-10-18) checked DESIGN-0037's
assumptions. The rows that shape this plan (the API rows went with the
API to IMPL-0030):

| Finding | Effect on the plan |
|---------|--------------------|
| The UI image builds with context `ui/`, so a root `CHANGELOG.yaml` is not in it; the build is Vite 8, which has no YAML loader | OQ1, OQ2; Phase 7 |
| The CI `ui` filter is `ui/**` + `api/openapi.yaml`, so a chart-only bump PR never runs `lint-ui` | 7.4 |
| `GroupGrid` shares one denominator (tracked) for percent and shading, nests children in an inner table and is unused | OQ4; Phase 4 |
| `RangeTabs`, `OrgScope`, `CommandPalette`, `csv.ts`, `range.ts` were deleted in `7ad928f`; `RangeTabs` bound `?range=` strings, and `csv.ts` carried findings-era helpers | 2.1–2.4 restore and rework them |
| `BucketBadge`/`DecisionBadge` keep their own tone maps outside `toneFor` | 1.1 folds them in |
| The e2e seed installs the remediate App on acme, so only the installed state is tested end to end | OQ6; 8.7 adds the absent state |
| `docs/usage/img/ui/status.png` is a hand copy of the e2e snapshot | 1.5 copies it after re-rendering |
| `Status.apps` names the Apps `eval` and `remediate` | every gated panel decides from `remediate` |

## How the phases are ordered

Phase 1 is the INV-0023 fixes: UI-only, no new API, visible on rc.11
today, so it ships now as its own small pull request. Phases 2 to 9
start once IMPL-0030 has merged, because every page reads an API it
adds. Phase 2 rebuilds the shared pieces the pages compose from.
Phases 3 to 5 are DESIGN-0037's pages in its order (Findings first, it
needs the most), Phase 6 is the Remediations view and the other
remediation panels, Phase 7 the changelog (it touches the build,
`lint-ui` and CI), Phase 8 the seed, axe, screenshots and docs, and
Phase 9 is human-run.

Each code phase ends with `make lint-ui`, `make test-ui` and
`make test-ui-e2e` green, with a commit per numbered task. Phase 1
lands as one PR into `v2`; Phases 2 to 9 land as a second PR (the
one-PR-per-IMPL rule, with the fixes split out by decision). Both carry
the `dont-release` label (Rule 6).

## Implementation Phases

<!--docz:phase:start-->
### Phase 1: INV-0023 fixes (its own pull request)

The repository-page fixes rc.11 showed are needed, and the stale
status screenshot. UI-only, no new API, branched from `v2` now.

<!--docz:tasks:start-->
#### Tasks

- [x] 1.1 **Tone table (INV-0023 F1).** In `ui/web/src/lib/format.ts`,
  add `rule` (`pass` ok, `fail` bad, `error` bad, `unknown` warn,
  `not_applicable` mute), `bucket` and `decision` to `ToneField`. Move
  `BucketBadge`'s `bucketTones` and `DecisionBadge` in
  `components/posture.tsx` onto `toneFor`, and add `RuleStatusBadge`.
  Extend `format.test.ts` with every rule value, including a failing
  rule rendering `bad`.
- [x] 1.2 **Repository page rule badges.** The repository page's
  `ruleColumns` (`views/repositories.tsx`) and the Events tab use
  `RuleStatusBadge` when `rule_id` is set and `StatusBadge` otherwise.
- [x] 1.3 **Breadcrumb label (INV-0023 F2).** Add a breadcrumb label
  context in `components/page/Breadcrumbs.tsx` that a view sets, and
  have the repository view set `org/name` from `useRepository`. The
  crumb falls back to the param while loading, or reads `…` for
  numeric params. Unit test: the crumb reads `acme/widgets`, never `1`.
- [x] 1.4 **Remediable wording (INV-0023 F4).** Rename the rule column
  `Remediates` to `Remediable rule`. Under `remediable no`, the facts
  card adds the hint "Remediation App has no access to this
  repository", as the mockup draws it.
- [x] 1.5 **Status screenshot (INV-0023 F7.3).** Re-render the visual
  snapshots in the pinned Playwright image
  (`make test-ui-e2e-update-snapshots`), then copy
  `ui/e2e/__screenshots__/status.png` to `docs/usage/img/ui/status.png`.
- [x] 1.6 **e2e tone assertion.** One Playwright assertion in
  `ui/e2e/ui.spec.ts` that a seeded failing rule's badge carries the
  `bad` tone class, and one that the repository crumb reads `org/name`.
  Break `toneFor`'s `rule` row once, confirm the spec fails, restore.
- [x] 1.7 **Ship.** Open the PR into `v2` with `dont-release` (#222); record in
  INV-0023 which findings it closes (F1, F2, F4, F7.3). Then, after merge,
  tag `v2.0.0-rc.12` (chart `2.0.0-rc.12`, appVersion bumped by hand)
  and dispatch `ghcr.yml` with the tag (OQ7; the tag and dispatch are
  `deferred - human required`).

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- A failing rule renders red on the repository page and in its events
  (unit test and the Playwright assertion, proven non-vacuous).
- The repository crumb reads `org/name`.
- The rule column reads `Remediable rule`, with the hint under
  `remediable no`.
- `docs/usage/img/ui/status.png` shows the controls status page.
- `make lint-ui`, `make test-ui` and `make test-ui-e2e` pass.

<!--docz:criteria:end-->
<!--docz:phase:end-->

<!--docz:phase:start-->
### Phase 2: UI foundations

The shared pieces the pages compose from. Starts after IMPL-0030
merges: task 2.6 regenerates the client from its spec.

<!--docz:tasks:start-->
#### Tasks

- [ ] 2.1 **CSV.** Restore `lib/csv.ts` and `csv.test.ts` from
  `7ad928f^` with only `csvCell`, `tableCsv` and `downloadCsv`; drop the
  findings-era `columns`/`findingsCsv`. Add an `ExportButton` (page,
  filter and date in the filename) and put it on every existing table
  card: controls, control by-org, orgs, org controls, exclusions,
  repositories, evaluation log and events.
- [ ] 2.2 **Range tabs on `?days=`.** Restore
  `components/page/RangeTabs.tsx` on `?days=` with numeric values 1, 7,
  30 and 90 (default per OQ5). The search validators parse a number, so
  the History numeric-search rule holds. Add `validateSearch` to the
  fleet, control, org and history routes, and drop the old `trendFrom`
  import. Include a unit test for the parse.
- [ ] 2.3 **Header org scope (OQ3).** Restore
  `components/page/OrgScope.tsx` as the page-header popover writing
  `?org=`, on Fleet, Controls, control detail, Findings, Remediations
  and History. Hooks pass `org` through to the API.
- [ ] 2.4 **⌘K palette (DESIGN-0037 OQ9).** Restore
  `components/shell/CommandPalette.tsx` and `palette.test.tsx` on the
  surviving `ui/command.tsx`, over `/repositories?q=` (debounced),
  `/controls` and `/orgs`, and bind ⌘K/Ctrl-K with
  `useKeyboardShortcut`. The top-bar Search button opens it.
- [ ] 2.5 **Client paging.** `useRepositories` in
  `ui/web/src/api/queries.ts` becomes a `useInfiniteQuery` taking
  `org`, `active` and `q` (the tree, the Parked tab and the palette use
  it). It replaces today's single `limit: 100` fetch.
- [ ] 2.6 **Client and gating.** `make generate-ui-api` against
  IMPL-0030's spec. Add `useRemediationEnabled()` (true when
  `Status.apps` has `remediate`) and a `GatedPanel` that renders "Remediation
  is not enabled on this deployment" without it and the caller's empty
  state ("No open pull requests") with it. Unit-test the decision table;
  e2e covers both states (8.7, OQ6).
- [ ] 2.7 **Nav.** `nav.ts` gains a `badge` field and the `/findings`
  and `/remediations` paths, each hidden until its phase lands the
  route. Then add the IMPL-0031 6.3 keyboard pass on the current pages:
  tab order, visible focus, and Escape closing popovers.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- Every table card has an Export that downloads its rows with formula
  cells defused.
- ⌘K opens the palette from every authenticated page, and `?days=` and
  `?org=` round-trip through the URL as a number and a string.
- `make lint-ui` shows no generated-types drift against IMPL-0030's
  spec.

<!--docz:criteria:end-->
<!--docz:phase:end-->

<!--docz:phase:start-->
### Phase 3: Findings page and control detail

As the mockup's `#/findings` and `#/controls/<id>` draw them, over
IMPL-0030's `GET /results` (bucket filter, `total` on the first page,
`pr_stale`, `ResultRow.remediation`, `sort=pr_age`).

<!--docz:tasks:start-->
#### Tasks

- [ ] 3.1 **Hook and route.** Add `useResults(filter)` as a
  `useInfiniteQuery` over `/results`, with "Load more" and "N loaded
  of M" from `total`. Add the `/app/findings` route with
  `validateSearch` for `control`, `rule`, `org`, `decision`,
  `pr_stale`, `sort` and `open`, and an `/app/findings` crumb entry.
  The page always asks for the non-compliant bucket, and the nav entry
  un-hides with the `Summary.compliance.non_compliant` count badge.
- [ ] 3.2 **Filter chips** (restore `components/filters/*` from
  `7ad928f^`, reworked):
  - Control comes from `/controls`, and Rule enables once a control is
    chosen, listing that control's rule ids.
  - Org and Decision.
  - Stale PR, through `GatedPanel`'s decision: disabled with
    "remediation not enabled" without the remediate App.
  - Changing the control clears the rule.
- [ ] 3.3 **Table:** columns Repository (name, org beneath), Control
  (slug, version, non-baseline decision), Failing rules (red badges plus
  the first failing rule's evidence headline from `describeEvidence`),
  Remediation (state, PR link, age, stale badge; `—` with the gated
  tooltip without the App) and Since (relative, absolute on hover). Add
  the sort toggle (`since_asc`, `since_desc`, `pr_age` with the App)
  and Export, which pages `/results` up to 10,000 rows with a notice
  when the cap cuts the export short (DESIGN-0037 OQ6).
- [ ] 3.4 **Finding drawer** (restore `views/findings/drawer.tsx` on
  `DetailDrawer`), opened by `?open=<repository_id>/<control>`. It
  shows:
  - status, remediation (linking to the Remediations drawer) and last
    evaluated;
  - the control's rule table from `/repositories/{id}/controls`, with
    `RuleStatusBadge`;
  - the evidence of each failing rule as text;
  - a timeline from `/repositories/{id}/events` filtered to the
    control, plus the last two evaluations;
  - Re-evaluate, sharing the repository page's `EvaluateButton`, and
    "Open repository →".

  Escape and the scrim close it. A11y names come from the repository
  and control.
- [ ] 3.5 **Control detail cards:**
  - **Top failing rules:** a bar list of the control's findings by
    failing rule from `/results?control=`. Each row links to
    `/findings?control=&rule=`.
  - **Oldest findings:** ten rows of `/results?control=&sort=since_asc`
    that open the drawer.
  - **"Findings for this control"** button.
  - **The compliance chart** switches to `aggregate=control` with the
    delta over the range.
- [ ] 3.6 **Unit tests:** filter ↔ URL round trip, rule chip enablement,
  the drawer id parse, the export cap notice, the gated Remediation
  column.

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
### Phase 4: Orgs tree

As the mockup's `#/orgs` draws it (OQ4).

<!--docz:tasks:start-->
#### Tasks

- [ ] 4.1 **Rework `components/ui/group-grid.tsx`:**
  - child rows render in the same `<tbody>` (no nested table), so
    columns align;
  - each cell carries its own numerator and denominator, and the
    column header label is a prop;
  - the footer is supplied by the caller;
  - shading uses `share = value / (compliant + non_compliant)`, and an
    unmeasured cell renders "no data", never 0% or 100%.

  Rewrite `tables.test.tsx` for the new contract.
- [ ] 4.2 **Load children on expand.** Each org's children come from
  `/repositories?org=&active=true` (keyset, "Load more" inside the
  group, "showing N of M"). Collapsed groups fetch nothing.
  Expand all / Collapse all.
- [ ] 4.3 **Columns:** Org / repository, Tracked (org: active
  repositories; repository: assignments), Compliant, Non-compliant,
  Open PRs (`OrgSummary.open_prs`; repository rows from the
  repository's open pull-request remediations, gated) and Parked. The
  page subtitle says the counts are assignments, as the mockup words
  it. The footer sums the visible org rows with `addPosture`.
- [ ] 4.4 **Toolbar:** KPI chips (Tracked, Non-compliant, Parked), tabs
  Compliance / Parked / PR activity, the repository filter (`?q=`,
  applied to loaded children) and Export.
  - The **Parked** tab lists parked repositories with reason and
    parked-at, grouped by org (`/repositories?org=&active=false`).
  - The **PR activity** tab lists open pull requests per org with age
    from `/remediations?kind=pull_request&state=open`, gated.
- [ ] 4.5 **Unit tests:** footer sums equal the `/orgs` totals; the
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
### Phase 5: Fleet panels

As the mockup's `#/` draws it, panels 1 to 8 of DESIGN-0037 §Fleet
panels.

<!--docz:tasks:start-->
#### Tasks

- [ ] 5.1 **Compliance card:**
  - KPI strip: Tracked; Compliant % with a delta in points over the
    range; Non-compliant with `controls_failing`; Parked with its
    reason count; Snapshots and cadence.
  - Series pills (compliance %, findings, not applicable, unknown)
    choose what the area chart draws from `aggregate=fleet`. The pill
    choice persists as `?series=`.
  - Fewer than two points renders no delta.
- [ ] 5.2 **Finding age:** `DistBar` over `Summary.finding_age` with the
  "N% under 30d" pill and a link to `/findings?sort=since_asc`.
- [ ] 5.3 **Worst controls:** the five lowest measured percentages from
  `/controls`, each with its delta from `aggregate=control`.
- [ ] 5.4 **Parked by reason:** `BarList` over `Summary.parked_by_reason`.
- [ ] 5.5 **Compliance by control:** the table gains N/A, %, and a
  sparkline from one `aggregate=control` request (not one per row).
- [ ] 5.6 **Orgs card:** with one org in scope it becomes Exclusions by
  reason from `OrgDetail.exclusions`. Export on both.
- [ ] 5.7 **Assignments** stays (rc.11's bucket card).
- [ ] 5.8 **Open PRs by age** (`Summary.open_prs_by_age`, "N over
  threshold → `/findings?pr_stale=true`") and **PR throughput (14d)**
  (`/remediations/activity`), both through `GatedPanel`.
- [ ] 5.9 **Org detail:** the compliance chart switches to
  `aggregate=org`.
- [ ] 5.10 **Unit tests:** delta with 0, 1 and 2+ points; the `?series=`
  parse; the two gated panels in all three states.

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
### Phase 6: Remediations view and remediation panels

As the mockup's `#/remediations`, `#/repositories/<id>` and `#/status`
draw them, over IMPL-0030's `/remediations`, the latest remediation on
`/repositories/{id}/controls` and `Status.operations.remediation_backlog`.
This is IMPL-0030's former task 7.5 (DESIGN-0037 §Remediations view).

<!--docz:tasks:start-->
#### Tasks

- [ ] 6.1 **Hook and route.** `useRemediations(filter)` as a
  `useInfiniteQuery` over `/remediations`; the `/app/remediations`
  route with `validateSearch` for `view`, `kind`, `state`, `org`,
  `control`, `hold`, `older_than` and `open`, and its crumb (the open
  row reads `org/name · control`). Without the remediate App the page
  is `GatedPanel`'s "not enabled" state and the nav count is blank;
  with it the nav entry shows the outstanding count (reserved, open,
  recommended, failed).
- [ ] 6.2 **Holds as sentences.** `lib/holds.ts` maps `pr_cap`,
  `foreign_branch`, `stale_branch`, `conflict` and `permission` to
  DESIGN-0037's sentences, with a `hold` tone row in `toneFor` and a
  `HoldBadge`. An unknown hold renders its code, never blank. Unit test
  every value.
- [ ] 6.3 **Remediation state tones.** A `remediation` tone row over the
  schema's states (`open` info, `merged`/`closed_compliant`/`resolved`/
  `applied` ok, `recommended` warn, `failed` bad, the rest mute) and a
  `RemediationBadge` (state, PR number link, age, stale at 30 days),
  shared by Findings, the drawer and the repository page.
- [ ] 6.4 **Page.** KPIs (Open PRs with the stale count,
  Recommendations, Held with a count per reason, Failed applies), the
  segmented views All / Open PRs / Recommendations / Held / Failed,
  filter chips for kind, state, org, control, hold and age, the table
  (Repository, Control, Kind, State, Hold with its sentence, Age,
  Updated) and Export, as the mockup draws them.
- [ ] 6.5 **Drawer** (`?open=<remediation id>`): state, the pull request
  and its `repo-guardian/<control>` branch, hold with its sentence,
  age, the proposal for a recommendation, the steps journal for a
  direct apply, the result's timeline, and links to the finding and the
  repository. Escape and the scrim close it.
- [ ] 6.6 **Repository remediation panel.** Each control card on the
  repository page shows `RemediationBadge`, the hold sentence, the
  apply steps when there are any, and "details →" to the drawer.
- [ ] 6.7 **Status backlog.** The status page's operations strip gains
  the Remediation backlog cell when the remediate App is installed.
  `TestStatus_PrivacyGuard` is IMPL-0030's to extend; this task only
  renders the number.
- [ ] 6.8 **Unit tests:** filter ↔ URL round trip, the view presets, the
  drawer id parse, every state and hold rendering against API fixtures
  (IMPL-0030 7.5's success criterion, carried).

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- Every remediation state and hold renders in the UI against API
  fixtures, holds as sentences.
- With the remediate App absent, the page, the nav count, the
  repository panel and the status cell all read "not enabled" or are
  hidden; with it present and nothing outstanding they read empty, not
  "not enabled".
- The Findings drawer and the Remediations drawer link to each other.

<!--docz:criteria:end-->
<!--docz:phase:end-->

<!--docz:phase:start-->
### Phase 7: Status page changelog

DESIGN-0037 §Status page changelog, with the build constraints from
the audit.

<!--docz:tasks:start-->
#### Tasks

- [ ] 7.1 **The changelog file.** `CHANGELOG.yaml` at the location OQ1
  chooses, seeded from rc.8 to the current rc (rc.8: fleet history
  refetch fix; rc.9: design refresh; rc.10: repository page polish;
  rc.11: controls evaluation; rc.12: the INV-0023 fixes (OQ7); and
  IMPL-0030's) with closed-set tags. Add the `tag` tone row (`new` ok,
  `improved` info, `fixed` warn, `breaking` bad, areas outline).
- [ ] 7.2 **Into the bundle** per OQ2. The UI also needs its own
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
  - an excluded repository;
  - remediations on acme: open PRs at several ages (one past 30
    days), a recommendation with a proposal, a failed direct apply
    with steps, a merged and a user-closed PR, and results held for
    each hold reason.

  globex stays invisible to ada.
- [ ] 8.2 **Specs** in `ui/e2e/ui.spec.ts`. Each one asserts globex never
  appears:
  - Findings filters round-trip, and the drawer deep link and
    Re-evaluate work.
  - Export downloads the expected row count.
  - Tree expand/collapse and the footer.
  - Fleet panels read the seeded numbers, the PR panels included.
  - Remediations views and filters round-trip, the drawer deep link
    works, and a hold reads as its sentence.
  - The repository remediation panel and the status backlog cell.
  - The changelog card.
- [ ] 8.3 **Axe.** `a11y.spec.ts` loops over `/findings`,
  `/findings?open=`, `/orgs` (expanded), `/remediations`,
  `/remediations?open=`, the fleet, and the status page with the
  changelog.
- [ ] 8.4 **Screenshots.** Re-render the snapshots in the pinned
  Playwright image and copy `status.png` to `docs/usage/img/ui/` again.
  Add the findings, orgs and remediations pages to the visual spec if
  IMPL-0031's OQ5 still allows it.
- [ ] 8.5 **Docs:**
  - `docs/usage/ui.md`: the Findings page, the tree, the fleet panels,
    the Remediations view, export and the changelog;
  - `ui/README.md`;
  - CLAUDE.md: the DESIGN-0037 UI contracts (findings = bucket
    `non_compliant`; gated panels decide from `Status.apps`; holds are
    sentences in `lib/holds.ts`; the changelog gate).
- [ ] 8.6 **Close the related docs.** Mark INV-0023 Concluded with each
  finding's outcome (F3, F7.1 and F7.2 pointing at IMPL-0030), and set
  DESIGN-0037 to Implemented (`docz update`).
- [ ] 8.7 **The "not enabled" stack (OQ6).** `ui/e2e/seed-no-remediate.sql`
  (the base seed without the remediate App's installations, access rows
  and remediations), loaded into a second database in the same Postgres
  container, and a Playwright project `no-remediation` whose
  `webServer` entry starts a second `api` role and BFF on their own
  ports against it (`ui/e2e/stack.ts`). Its spec walks Fleet, Findings,
  Orgs (PR activity), the repository page, Remediations and the status
  page, and asserts every gated panel reads "Remediation is not
  enabled", the Remediations nav count is blank and the status backlog
  cell is absent. `e2e/teardown.ts` sweeps both databases.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- `make test-ui-e2e` passes with the new specs and both projects, axe
  reports no violations on the new pages, and the screenshots show the
  controls pages.

<!--docz:criteria:end-->
<!--docz:phase:end-->

<!--docz:phase:start-->
### Phase 9: rc image and homelab review

`deferred - human required`.

<!--docz:tasks:start-->
#### Tasks

- [ ] 9.1 Merge into `v2`, cut `v2.0.0-rc.N` by hand with its
  changelog entry, and dispatch `ghcr.yml`.
- [ ] 9.2 Deploy to the homelab. Walk Findings (filters, drawer,
  re-evaluate), the Orgs tree, Fleet, Remediations, the palette, and
  the status changelog against the mockup. File divergences as tasks
  here.
- [ ] 9.3 Confirm the Keycloak group split still scopes `/results`,
  `/remediations` and the tree (carries IMPL-0031 8.3).

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
| `ui/web/src/lib/format.ts`, `components/posture.tsx`, `components/badges.tsx` | Modify | tone rows (`rule`, `bucket`, `decision`, `remediation`, `hold`, `tag`), `RuleStatusBadge`, `RemediationBadge`, `HoldBadge` |
| `ui/web/src/components/page/Breadcrumbs.tsx`, `RangeTabs.tsx`, `OrgScope.tsx` | Modify/Restore | crumb label, `?days=`, `?org=` |
| `ui/web/src/views/repositories.tsx` | Modify | rule badges, wording, remediation panel |
| `ui/e2e/__screenshots__/*`, `docs/usage/img/ui/status.png` | Modify | Phase 1 and Phase 8 renders |
| `ui/web/src/components/shell/CommandPalette.tsx`, `nav.ts`, `TopBar.tsx` | Restore/Modify | palette, nav badges |
| `ui/web/src/lib/csv.ts` (+ test), `components/ExportButton.tsx` | Restore/Create | CSV export |
| `ui/web/src/components/GatedPanel.tsx`, `lib/holds.ts` (+ tests) | Create | remediation gating, hold sentences |
| `ui/web/src/views/findings.tsx`, `views/findings/drawer.tsx`, `components/filters/*` | Restore/Rework | findings page |
| `ui/web/src/views/remediations.tsx`, `views/remediations/drawer.tsx` | Create | Remediations view |
| `ui/web/src/views/controls.tsx`, `orgs.tsx`, `fleet.tsx`, `status.tsx`, `history.tsx` | Modify | page work |
| `ui/web/src/components/ui/group-grid.tsx`, `tables.test.tsx` | Modify | tree contract |
| `ui/web/src/api/queries.ts`, `schema.gen.ts` | Modify/Regenerate | `useResults`, `useRemediations`, aggregates |
| changelog file (OQ1), `ui/scripts/check-changelog.ts` (+ test), `ui/package.json` | Create/Modify | changelog and gate |
| `.github/workflows/ci.yml` | Modify | `ui` paths filter |
| `ui/e2e/seed.sql`, `ui.spec.ts`, `a11y.spec.ts` | Modify | e2e |
| `ui/e2e/seed-no-remediate.sql`, `no-remediation.spec.ts`, `stack.ts`, `teardown.ts`, `playwright.config.ts` | Create/Modify | the second project (OQ6) |
| `ui/design/repo-guardian-mockup.html` | Modify | only where a page must differ |
| `docs/usage/ui.md`, `ui/README.md`, `CLAUDE.md` | Modify | docs |

<!--docz:file-changes:end-->

<!--docz:testing:start-->
## Testing Plan

- **UI unit** (`make test-ui`): tone rows, crumb label, CSV defusing,
  `?days=`/`?org=`/`?series=` parsing, filter round trips (findings and
  remediations), tree sums and shading, deltas, the gated-state
  decision, every remediation state and hold, the changelog check.
- **e2e** (`make test-ui-e2e`): the Phase 1 tone and crumb assertions,
  the specs in 8.2, axe in 8.3, the `no-remediation` project in 8.7,
  and the visual snapshots in the pinned image.
- **Non-vacuous checks.** For the tone assertion, the gated decision
  and the changelog gate, break the code under test once, confirm the
  test fails, then restore.
- The Go side (queries, parity, replay, perf) is tested in IMPL-0030.

<!--docz:testing:end-->

<!--docz:dependencies:start-->
## Dependencies

- DESIGN-0037 and its mockup accepted (PR #221 merged into `v2`).
- Phase 1: nothing else (rc.11 is out).
- Phases 2 to 9: IMPL-0030 merged into `v2` (every API they read, and
  the remediation rows the gated panels show).
- The pinned Playwright image for screenshots (`ui/scripts/visual.ts`).

<!--docz:dependencies:end-->

## Open Questions

### OQ1: Where CHANGELOG.yaml lives

**Resolved 2026-10-19: (a).**

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

### OQ2: How the changelog reaches the Vite bundle

**Resolved 2026-10-19: (a).**

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

### OQ3: The header org scope

**Resolved 2026-10-19: (a).**

DESIGN-0037 said the header org scope was "kept", but `OrgScope.tsx`
was deleted in `7ad928f`. The accepted mockup draws it on Fleet,
Controls, Findings, Remediations and History.

- (a) ✅ recommended: **restore it** (task 2.3) as the page-header
  popover writing `?org=`, because the mockup is the accepted spec.
- (b) Leave it out and filter by org inside each page (the Findings
  chip, the Orgs tree). Less to build, but the mockup changes in the
  same PR.
- other:

### OQ4: The org tree component

**Resolved 2026-10-19: (a).**

- (a) ✅ recommended: **rework the existing `GroupGrid`** (task 4.1)
  into flat child rows with per-cell denominators. It is unused, so
  there are no other callers to keep, and its tests come with it.
- (b) A new `OrgTree` component, leaving `GroupGrid` as is. A clean
  slate, but it leaves a dead component behind (delete it then).
- other:

### OQ5: Default range

**Resolved 2026-10-19: (a).**

- (a) ✅ recommended: **30 days**, matching the API's `days` default,
  the mockup and rc.11's history.
- (b) 90 days, the IMPL-0031 default. Longer trends, but four times
  the history rows on every fleet load.
- other:

### OQ6: Testing the "not enabled" empty state

**Resolved 2026-10-19: (b).** Both states run end to end: task 8.7 adds a second seed and Playwright project without the remediate App. The unit test of the decision table (2.6) stays.

The e2e seed installs the remediate App on acme, so the gated panels
read "No open pull requests" there.

- (a) ✅ recommended: **unit-test the decision** ("not enabled" vs
  "No open pull requests" from `Status.apps`), and let e2e cover the
  installed case the seed already has. No second stack.
- (b) A second e2e seed or project without the remediate App. Both
  states end to end, but a second stack start.
- other:

### OQ7: Does the fixes pull request cut its own rc

**Resolved 2026-10-19: (a).** Phase 1 ships as `v2.0.0-rc.12`.

Phase 1 is four UI fixes and a screenshot.

- (a) ✅ recommended: **yes, `v2.0.0-rc.12` with only the fixes.** The
  homelab shows red failing rules and the right crumb now, rather than
  after the length of IMPL-0030. The tag and `ghcr.yml` dispatch are a
  few minutes of human time, and the image is UI-only (the Go image is
  rebuilt but unchanged).
- (b) Merge it and let it ride IMPL-0030's rc. No extra release, but
  the fixes stay invisible in the homelab until remediation ships.
- other:

### Resolved by the split

Answered by the 2026-10-18 decision to split the plan (see
[the split](#the-split-with-impl-0030)):

- The old OQ10 (remediation data tasks and the one-PR rule): the data
  is in IMPL-0030; its UI is Phases 3, 4, 5 and 6 here.
- The old OQ11 (INV-0023 F7.1 and F7.2): in IMPL-0030.
- The old OQ14 (order against IMPL-0030): the fixes first, then
  IMPL-0030, then the rest of this plan.
- The old OQ1 to OQ4 and OQ12 (history shape, bucket vs status, total,
  next due, the index) moved with the API to IMPL-0030's open
  questions.

<!--docz:references:start-->
## References

- DESIGN-0037 (the design; its mockup `ui/design/repo-guardian-mockup.html`)
- DESIGN-0032 (D32's Remediations view; the remediation states and holds)
- INV-0023 (F1, F2, F4, F7.3 here; F3, F7.1, F7.2 in IMPL-0030)
- IMPL-0030 (the API this plan reads)
- DESIGN-0036 and IMPL-0031 (design language, the components and libs deleted in `7ad928f`)

<!--docz:references:end-->
