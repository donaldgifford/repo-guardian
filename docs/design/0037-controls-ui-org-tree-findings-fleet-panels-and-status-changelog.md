---
id: DESIGN-0037
title: "Controls UI: org tree, findings, fleet panels and status changelog"
status: Draft
author: Donald Gifford
created: 2026-10-18
---

<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN-0037: Controls UI: org tree, findings, fleet panels and status changelog

<!--toc:start-->
<!-- markdownlint-capture -->
<!-- markdownlint-disable MD013 -->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
  - [What rc.11 shows](#what-rc11-shows)
  - [What IMPL-0029 removed](#what-impl-0029-removed)
  - [Panel inventory](#panel-inventory)
  - [The mockup and DESIGN-0036](#the-mockup-and-design-0036)
- [Detailed Design](#detailed-design)
  - [Vocabulary](#vocabulary)
  - [Findings page](#findings-page)
  - [Control detail](#control-detail)
  - [Orgs tree](#orgs-tree)
  - [Fleet panels](#fleet-panels)
  - [Status page changelog](#status-page-changelog)
  - [CSV export](#csv-export)
  - [Remediation-gated panels](#remediation-gated-panels)
  - [Remediations view](#remediations-view)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!-- markdownlint-restore -->
<!--toc:end-->

<!--docz:overview:start-->
## Overview

rc.11 put the UI on the controls schema (IMPL-0029 Phase 8) and, to get
there, removed every page and panel that read the findings model. This
design brings the useful ones back on the controls model: a findings
page filterable by control, an expandable org tree, the richer fleet
panels, CSV export, and a changelog on the status page. It names the
API each needs, marks what waits on remediation (IMPL-0030), adds the
Remediations view, and ships the INV-0023 repository-page fixes in a
small pull request ahead of the rest.

The mockup is part of this design. `ui/design/repo-guardian-mockup.html`
is updated in the same pull request to the controls line and to every
page below, and is accepted with the design: the implementation builds
from an accepted mockup, it does not draw one (OQ10).

<!--docz:overview:end-->

## Goals and Non-Goals

<!--docz:goals:start-->
### Goals

- A **findings page**: every non-compliant (repository, control) result
  across the visible fleet, filterable by control, rule, org and
  decision, keyset-paged, exportable as CSV.
- An **orgs tree**: one grid of orgs that expand to their repositories,
  with compliant/non-compliant counts and shares per row, a parked tab,
  repository search, and a totals footer.
- **Fleet panels**: a range selector driving the compliance card, a KPI
  strip with deltas, a finding age distribution, worst controls,
  parked by reason, and a compliance-by-control table with trend
  sparklines.
- **CSV export** on every table card again.
- A **changelog** on the status page with a source that cannot silently
  go stale.
- The panels that need remediation data (open PRs by age, PR
  throughput, the remediation column, the stale-PR filter) are designed
  now and switched on by IMPL-0030.

<!--docz:goals:end-->

<!--docz:non-goals:start-->
### Non-Goals

- The **compliance drivers map** (initiatives → inputs → north star →
  KPIs with correlations). It needs data repo-guardian does not hold
  (GitHub Projects, initiative state) and correlation analytics; it
  stays deferred as in DESIGN-0036 (OQ15) and wants its own design.
- Writes beyond the existing re-evaluate POST. The UI stays read-only.
- A new design language. DESIGN-0036's tokens, primitives and tone
  table stay; this design adds pages and panels to them.
- Per-repository metrics labels. "Which repository" stays an API and
  log question (IMPL-0023 Finding G); nothing here touches Prometheus.

<!--docz:non-goals:end-->

<!--docz:background:start-->
## Background

### What rc.11 shows

| Page | rc.11 content |
|------|---------------|
| Fleet | assignments posture card, controls table, orgs table |
| Controls | control list, control detail with per-org breakdown |
| Orgs | org list; org detail with controls, worst controls, exclusions |
| Repositories | list (org, active, park reason, policy state, name prefix); detail with Controls / Evaluation log / Events tabs |
| History | compliance history by control and days |
| Policy | the visible policy layers |
| Status | components, compliance, deployment card, apps, overdue |

### What IMPL-0029 removed

IMPL-0029 deleted the findings-era views and their helpers rather than
port them: `views/findings.tsx` (+ drawer), `views/rules.tsx`, the org
expand/collapse grid usage (`orgs.test.tsx`), `views/repository.tsx`,
`lib/csv.ts` (the defused CSV writer), `lib/range.ts` +
`page/RangeTabs.tsx`, `lib/pr.ts`, the timeline and the command
palette. The `GroupGrid` primitive survived unused. Those files are the
starting point for this design's components; their data hooks are not,
because they read `/findings` and `/rules`, which no longer exist.

### Panel inventory

Each mockup panel (`ui/design/repo-guardian-mockup.html`, the
findings-era platform mockup) against the controls API:

| Panel | Data today | Needs |
|-------|-----------|-------|
| Findings page | nothing cross-repository; `/repositories/{id}/controls` per repository | `GET /results` |
| Orgs tree, org rows | `GET /orgs` (`OrgSummary`: tracked, parked, compliance, open_prs) | nothing |
| Orgs tree, repository rows | `GET /repositories?org=` without posture | `Repository.compliance` |
| Orgs parked tab | `GET /repositories?active=false` | nothing |
| Compliance card + range | `GET /summary`, `GET /compliance/history?days=` (one row per day, org, control and decision) | `aggregate=fleet` on history |
| KPI: failing controls, parked reasons | partial | `Summary.parked_by_reason`, controls with any non-compliant |
| Finding age distribution | `control_results.last_changed_at` exists, not exposed | `Summary.finding_age` |
| Worst controls (fleet) | `GET /controls` | delta from `aggregate=control` history |
| Parked by reason | not exposed | `Summary.parked_by_reason` |
| Compliance by control + trend | `GET /controls`; history unaggregated | `aggregate=control` on history |
| Orgs table with bars | `GET /orgs` | nothing |
| Open PRs by age | `Summary.open_prs` count only | IMPL-0030 + `Summary.open_prs_by_age` |
| PR throughput (14d) | nothing | IMPL-0030 + `GET /remediations/activity` |
| Status changelog | nothing | a source (OQ4) |
| Compliance drivers map | nothing | non-goal |

### The mockup and DESIGN-0036

`ui/design/repo-guardian-mockup.html` is the UI's reference (DESIGN-0036
OQ17), but until this design it was still the findings-era mockup:
IMPL-0029 replaced the pages underneath it and did not touch it. This
design's pull request redraws it; the tables below record what it
showed, what rc.11 shows and what the redraw makes of each. Out of step
with rc.11 itself, before anything here:

| Mockup today | rc.11 / controls line |
|--------------|-----------------------|
| Rules and rule detail; rows are `(kind, name)` rules | Controls and control detail; rows are `slug@version` controls |
| Finding statuses, reason codes (`file_missing`, `setting_mismatch`, …), remediation `pr_open`/`applied`/`foreign_pr`/`disabled`/`dry_run` | result buckets, rule statuses `pass`/`fail`/`error`, evidence kinds; remediation states arrive with IMPL-0030 |
| One reconcile branch `repo-guardian/add-missing-files` per repository | per-control pull requests (IMPL-0030) |
| Repository page: Findings, Recent checks, Repository (installation and rate limit), Timeline | Controls / Evaluation log / Events tabs, Repository card, Re-evaluate |
| Status: `github api budget` component; sparkline and operations strip marked deferred | no budget component; sparkline, operations, deployment, apps, coverage, overdue shipped |
| Policy: one rules table | policy layers (enterprise, catalogue, orgs) |
| History: per-org small multiples by rule | history by control and days |
| Tone table knows finding statuses only | the same gap as INV-0023 F1 |

**Already drawn, made real here.** The findings-era mockup already drew
most of this design, on the findings model; the redraw moves each onto
results and controls:

| Mockup | This design |
|--------|-------------|
| Orgs grouped grid: heat cells by share, Tracked/Failing/Parked chips, Compliance/Parked tabs, totals row, Export CSV | [Orgs tree](#orgs-tree); children become repositories (see below) |
| Findings: filter chips with "+ filter", "N loaded of M", Export CSV, drawer with evidence, timeline and an "actions slot (IMPL-0029 re-evaluate)" | [Findings page](#findings-page) with the drawer; the actions slot becomes Re-evaluate |
| Fleet: KPI strip with deltas, series pills, open PRs by age, worst rules, parked bar list, findings-by-status stacked bar, compliance by rule with sparklines, orgs (or N/A by reason when one org is scoped) | [Fleet panels](#fleet-panels) |
| Rule detail: top reasons, oldest failures, "Findings for this rule" | [Control detail](#control-detail) |
| Header org scope and 1d/7d/30d/90d range | kept; the range tabs come back |

**The mockup's deferred banners**, and what happens to each:

| Banner | Outcome |
|--------|---------|
| Finding age distribution (DESIGN-0036 OQ8) | here, `Summary.finding_age` over `control_results.last_changed_at` |
| PR throughput (OQ15) | here, gated on IMPL-0030 |
| "+N discovered this week" (OQ7) | stays out, as OQ7 (a) decided |
| Repository rows under an org (OQ13) | here, superseding OQ13 (a) |
| PR activity tab (OQ14) | here, gated on IMPL-0030 |
| `sort = pr_age` (OQ5) | here, as a `/results` sort (API in IMPL-0030) |
| Status sparkline and operations strip (OQ10) | shipped in rc.10, carried onto the controls schema in rc.11 |

**Not in the committed mockup at all.** The status changelog, the
compliance drivers map and the "Stale PR (>30d)" checkbox come from the
platform mockup, a source DESIGN-0036 OQ17 chose not to commit. The
changelog and the stale-PR filter are designed here; the drivers map
stays a non-goal.

**The redraw in this pull request.** The mockup now draws the
controls line and every page here: Fleet (panels 1–8), Controls and
control detail, the Orgs tree with its Parked and PR activity tabs, org
detail, Findings with filters, sort, drawer and Re-evaluate,
Repositories and the repository page (Controls / Evaluation log /
Events, the `rule` tone, the name crumb, next due, the remediable hint,
the remediation panel), Remediations (views, filters, hold sentences,
drawer), History, Policy layers, and the status page with deployment,
operations, the remediation backlog and the changelog. Its in-page `api` mirrors `api/openapi.yaml` plus the
additions in [API / Interface Changes](#api--interface-changes), so a
panel the mockup draws is a panel the API can back. A top-bar switch
marked "mockup" previews the remediation-gated panels with a
Remediation App installed; it is not a product control. Export buttons
download real CSV through the `lib/csv.ts` rules.

**DESIGN-0036's API additions on the controls line.** All six were
findings-era endpoints or fields; IMPL-0029 removed or carried them:

| DESIGN-0036 addition | Controls line |
|----------------------|---------------|
| `/compliance/history?aggregate=` (OQ6) | removed with `/findings`; the controls history is unaggregated again, so it comes back here as `aggregate=none\|fleet\|org\|control` |
| `/findings?sort=pr_age` (OQ5) | `/results?sort=pr_age`, IMPL-0030 |
| `Summary.finding_age` (OQ8) | here, same name and buckets, over results |
| `Status.compliance.history`, `Status.operations` (OQ10) | carried, shipped |
| `OrgSummary.open_prs` (OQ14) | carried; reads 0 until IMPL-0030 |
| PR throughput aggregate (OQ15) | `GET /remediations/activity`, IMPL-0030 |

**DESIGN-0036 decisions this design supersedes**: OQ13 (an org row
expands to repositories, not rules; the rules-in-org view stays on
`/orgs/$org`), §Status page's "No changelog" (the source is now
`CHANGELOG.yaml` with a release gate), and OQ11's palette-only
repository access, which IMPL-0029 already replaced with the
`/repositories` page (OQ9 here asks whether the palette comes back).

**DESIGN-0032 decision this design supersedes**: the D32 row "Findings
view is removed; its filters move to the Remediations view". Findings
and remediations answer different questions: a finding is a
non-compliant result, whether or not anything is being done about it;
a remediation is the pull request or API change repo-guardian makes.
Most findings have no remediation (an evaluate-mode control, a rule
that is not remediable, a deployment without the Remediation App), so
folding findings into the Remediations view would hide them. Both
pages exist: the Findings page over `/results`, and the Remediations
view over DESIGN-0032's `/remediations` with the filters D32 gave it.
Each links to the other's row.

<!--docz:background:end-->

<!--docz:detailed-design:start-->
## Detailed Design

### Vocabulary

Two words, two meanings (OQ1):

- A **result** is the outcome of one assigned control on one repository
  (`control_results`: status, rule results, evidence,
  `last_changed_at`), whatever its status.
- A **finding** is a result that failed: status `non_compliant`.

The API serves results (`GET /results`, any status, filterable); the
Findings page lists findings, which is `/results?status=non_compliant`.
Neither word means a rule: the failing rules are the detail inside a
finding.

### Findings page

Route `/findings`, nav entry under Business with a count badge (the
findings in the visible orgs, `Summary.compliance.non_compliant`). The
page always asks `/results` for `status=non_compliant`; there is no
status filter, because a compliant row is not a finding.

Filters, each mapping 1:1 to a `/results` query parameter and persisted
in the URL search (the History page's numeric-search rule applies):

| Filter | Parameter | Values |
|--------|-----------|--------|
| Control | `control` | a control slug from `/controls` |
| Rule | `rule` | enabled once a control is chosen; a rule id of that control, matched against rules whose status is `fail` or `error` |
| Org | `org` | an org from `/orgs` |
| Decision | `decision` | `baseline`, `replaced`, `org_added` |
| Stale PR | `pr_stale` | IMPL-0030 |

Columns: Repository (name, org beneath), Control (slug, version), Failing rules (rule ids with the first failing rule's
evidence headline from `describeEvidence`), Remediation (IMPL-0030;
`—` until then), Since (`last_changed_at`, relative with absolute on
hover). A row opens the **finding drawer** (OQ8), deep-linked as
`?open=<repository_id>/<control>` per DESIGN-0036 OQ9: the control's
rule table with each failing rule's evidence (text only), the control's
events from `/repositories/{id}/events`, **Re-evaluate** (the mockup's
actions slot, now the existing POST) and "Open repository →".

Paging is keyset (`cursor`, `limit` 50) like `/repositories`, with a
"Load more" button and "N of M" from a `total` the endpoint returns for
the current filter (OQ3). Sort is `last_changed_at` ascending by
default (oldest problems first), toggled to descending.

### Control detail

`/controls/$id` gains the mockup's rule-detail cards on results: **Top
failing rules** (a bar list of the control's findings by failing rule)
and **Oldest findings** (`/results?control=<slug>&sort=since_asc`,
ten rows, opening the drawer), plus a "Findings for this control"
button to `/findings?control=<slug>`.

### Orgs tree

`/orgs` becomes one `GroupGrid`: org rows are groups, repositories are
children loaded per org on expand (`/repositories?org=<org>&active=true`,
keyset, "Load more" inside the group).

| Column | Org row | Repository row |
|--------|---------|----------------|
| Org / repository | org, "N% compliant · M parked" | name |
| Tracked | active repositories | assignments |
| Compliant | count, share of measured, shaded | count, share, shaded |
| Non-compliant | count, share, shaded | count, share, shaded |
| Open PRs | `OrgSummary.open_prs` | IMPL-0030 |
| Parked | parked repositories | `parked` badge or blank |

Cells shade by share of measured (`compliant + non_compliant`), the
same denominator as `percent`, so a cell's colour and the row's
percentage never disagree. Unmeasured rows render "no data", never 0%
or 100% (Posture contract).

Above the grid: the KPI chips (Tracked, Non-compliant, Parked), tabs
**Compliance** / **Parked** (and **PR activity** after IMPL-0030), an org
filter, a repository-name search (`q`), Expand all / Collapse all, and
Export. The footer row sums the visible rows with `addPosture`, never
by averaging percentages. The Parked tab lists parked repositories with
reason and parked-at, grouped by org. The org detail page
(`/orgs/$org`) stays and is reached from the org name.

### Fleet panels

Header gains a range selector (1d / 7d / 30d / 90d, restored
`RangeTabs`, `?days=`) that drives every history-backed panel.

1. **Compliance card**: KPI strip (Tracked repositories · Compliant %
   with delta in points over the range · Non-compliant results and the
   number of controls with any · Parked and the number of reasons ·
   Snapshots and cadence), series pills (compliance %, findings,
   not applicable, unknown) choosing what the area chart draws, and the
   chart from `/compliance/history?aggregate=fleet`. The delta is last point minus first point in
   the range; fewer than two points renders no delta.
2. **Finding age distribution**: a segmented bar over
   `<1d`, `1–7d`, `7–30d`, `30–90d`, `90–180d`, `180d+` of
   `now − last_changed_at` for non-compliant results, with a "N% under
   30d" pill. Buckets are fixed; they are not a tuning knob.
3. **Worst controls**: the five lowest measured percentages from
   `/controls`, each with its delta over the range from
   `aggregate=control` history, linking to the control.
4. **Parked by reason**: a bar list from `Summary.parked_by_reason`
   (bar lists, not donuts, per DESIGN-0036 §Charts; OQ5).
5. **Compliance by control**: Control, type (`KindBadge`), Compliant,
   Non-compliant, N/A, %, and a sparkline over the range from
   `aggregate=control` history.
6. **Orgs**: the existing table with a meter per row; with one org in
   scope it becomes **Exclusions by reason** from `OrgDetail.exclusions`
   (the mockup's "Not applicable by reason").
7. **Assignments**: rc.11's bucket card stays, as the controls form of
   the mockup's "Findings by status" stacked bar.
8. **Open PRs by age** and **PR throughput (14d)**: see
   [Remediation-gated panels](#remediation-gated-panels).

Every table card carries Export.

### Status page changelog

DESIGN-0036 left the changelog out because the API had no source and
a hand-kept list goes stale. The source proposed here is a curated
`CHANGELOG.yaml` at the repository root (OQ4):

```yaml
- version: 2.0.0-rc.11
  date: 2026-10-17
  title: Controls evaluation
  summary: Policies are a directory of controls; each repository is evaluated against its resolved assignments.
  tags: [new, policy, ui]
```

It is embedded into the UI bundle at build time and rendered as the
status page's Changelog card (newest five, then "All releases" linking
to the GitHub releases page). Staleness is a test, not a promise:
`lint-ui` fails when the newest entry's `version` is not the chart's
`appVersion`, so the release-bump PR cannot land without an entry. Tags
are a closed set (`new`, `improved`, `fixed`, `breaking`, plus an area
from `fleet`, `findings`, `policy`, `ui`, `api`, `chart`) so they tone
through `toneFor`. Nothing goes through `/status`, so
`TestStatus_PrivacyGuard` keeps its allowlist unchanged.

### CSV export

`lib/csv.ts` comes back as it was: RFC 4180 quoting plus formula
defusing (a cell starting `=`, `+`, `-`, `@`, tab or CR gets a leading
`'`), with its tests. Tables export the rows they show. The findings
page exports the whole current filter by paging `/results` in the
browser up to a cap of 10,000 rows, with a notice when the cap cuts it
short (OQ6). Filenames carry the page, filter and date
(`findings-renovate-2026-10-18.csv`).

### Remediation-gated panels

These render in the layout now and fill in when IMPL-0030 writes
`remediations` rows (OQ7):

- **Open PRs by age** (fleet): `<1d`, `1–7d`, `7–30d`, `30d+` of open
  pull-request remediations by `created_at`, with "N over threshold →
  findings?pr_stale=true".
- **PR throughput (14d)** (fleet): per day, pull-request remediations
  opened (`created_at`) vs closed (`closed_at`).
- **Remediation column and Stale PR filter** (findings), **PR activity
  tab** and repository-row **Open PRs** (orgs).
- **The Remediations view**, the **repository remediation panel** and
  the status page's **remediation backlog** (next section).

Until then each shows an empty state naming why ("Remediation is not
enabled on this deployment"), decided from `Status.apps` (no
`remediate` App) rather than from an empty result, so a deployment with
remediation and nothing open reads "No open pull requests" instead.

### Remediations view

`/remediations` lists what repo-guardian is doing about findings: one
row per `remediations` row, over DESIGN-0032's `GET /remediations`
(IMPL-0030). The mockup draws it at `#/remediations`.

- **Nav**: a Remediations entry under Findings, its count the
  outstanding rows (reserved, open, recommended, failed) in the org
  scope. Gated like every remediation panel: with no `remediate` App in
  `Status.apps` the page is the "Remediation is not enabled" empty
  state and the nav count is blank.
- **KPIs**: Open PRs (with how many are stale at 30 days), outstanding
  Recommendations, Held (with a count per hold reason), Failed applies.
- **Views and filters**: segmented views All / Open PRs /
  Recommendations / Held / Failed, then filter chips for kind, state,
  org, control, hold and age (`older_than` days), each mapping 1:1 to a
  `/remediations` query parameter and round-tripping through the URL.
- **Columns**: Repository (name over org), Control, Kind
  (`pull_request` or `api`), State (the schema's states; a pull request
  links its number), Hold (badge plus the human sentence below), Age
  (from `created_at`, with the stale badge on an open PR at 30 days),
  Updated.
- **Drawer** (`?open=<remediation id>`): state, the pull request and its
  `repo-guardian/<control>` branch, hold with its sentence, age, the
  proposal for a recommendation (the failing rules and what each
  needs), the steps journal for a direct apply, and the result's
  timeline; links to the finding and the repository.
- **Export**: the current filter, through `lib/csv.ts`.

**Hold reasons are sentences, not codes.** The UI maps each to what an
operator does about it:

| Hold | Shown as |
|------|----------|
| `pr_cap` | the repository is at `max_open_prs`; waits for a slot |
| `foreign_branch` | a `repo-guardian/` branch exists that the bot did not create; rename or delete it to resume |
| `stale_branch` | branch left from a closed PR; delete it to resume |
| `conflict` | the PR branch conflicts with the default branch and could not be updated |
| `permission` | the Remediation App lacks permission for this change |

`remediations.hold` is `permission` only; the branch holds live on
`control_results.hold` and are set before any pull request exists, so
the Held view needs those results too. Whether `/remediations` returns
them (as rows with no pull request) or the view reads them from
`/results?hold=` is an IMPL-0030 question.

**Repository remediation panel.** Each control card on the repository
page shows its remediation under the rules: the state badge, pull
request link and age, the hold sentence, the apply steps when there
are any, and "details →" to the Remediations drawer.

**Status page backlog.** The operations strip gains a Remediation
backlog cell (outstanding remediations, aggregate only, so the privacy
guard holds) when a Remediation App is installed.

<!--docz:detailed-design:end-->

<!--docz:api-changes:start-->
## API / Interface Changes

All additions; nothing existing changes shape. Every new SQL query
carries the scope predicate (`TestAPIQueries_AreScoped`) and every new
operation gets a case in `TestEndpoints_ValidateAgainstTheSpec`.

| Change | Shape | Phase |
|--------|-------|-------|
| `GET /results` | query `status`, `control`, `rule`, `org`, `decision`, `sort` (`since_asc`, `since_desc`), `cursor`, `limit`; returns `ResultPage{items: [ResultRow], total, next_cursor}`. `ResultRow`: repository id/org/name, control id/version, decision, status, `failing_rules: [rule_id]`, `reason` (first failing rule's evidence `reason` or `evidence_kind`), `last_changed_at` || IMPL-0030 |
| `Repository.compliance` | `Posture` over the repository's assignments, on list and detail || IMPL-0030 |
| `Summary.parked_by_reason` | `[{reason, count}]` over visible orgs || IMPL-0030 |
| `Summary.finding_age` | `[{bucket, count}]`, the six fixed buckets || IMPL-0030 |
| `Summary.controls_failing` | count of controls with any non-compliant result || IMPL-0030 |
| `/compliance/history?aggregate=` | `none` (today's rows, default) \| `fleet` \| `org` \| `control`; sums per day in SQL with `floor_percent`, never averaged; DESIGN-0036 OQ6 on the controls schema || IMPL-0030 |
| `Summary.open_prs_by_age` | `[{bucket, count}]` | IMPL-0030 |
| `GET /remediations/activity` | `days` (1–90, default 14) → `[{day, opened, closed}]` | IMPL-0030 |
| `/results` `pr_stale`, `ResultRow.remediation` | remediation state, PR number/url, PR age | IMPL-0030 |
| `GET /remediations` | DESIGN-0032's endpoint: `kind`, `state`, `org`, `control`, `hold`, `older_than_days`, `cursor`, `limit`; the Remediations view reads it as specified there | IMPL-0030 |
| `Status.operations.remediation_backlog` | count of outstanding remediations | IMPL-0030 |

`/results` is the API's first cross-repository row list over
`control_results`; it reads through `rg_api_ro` like everything else and
its RLS stays PUBLIC read.

<!--docz:api-changes:end-->

<!--docz:data-model:start-->
## Data Model

No new tables. Everything reads existing columns:
`control_results(status, last_changed_at, control_id)` and its rule
results, `repositories(active, park_reason)`, `compliance_snapshots`
(per org, control, decision, day) for trends, and `remediations
(kind, state, created_at, closed_at)` once IMPL-0030 writes it.

`/results` filters by control and status and sorts by
`last_changed_at`; if the perf fixture (`TestAPIPerf`) shows a
sequential scan at fleet size, add
`control_results (control_id, status, last_changed_at)` in a controls
migration. Decide from the plan output, not up front.

<!--docz:data-model:end-->

<!--docz:testing:start-->
## Testing Strategy

- **Go**: store integration tests per new query (scope, keyset paging
  with ties on `last_changed_at`, the age buckets at their boundaries,
  `total` matching the filter); `TestEndpoints_ValidateAgainstTheSpec`
  cases for every new operation and field; the two-org authz test
  extended to `/results` (a globex row never appears for an acme-only
  caller).
- **UI unit**: view models for the tree (footer sums via `addPosture`,
  share shading, "no data"), the age distribution, deltas with fewer
  than two points, CSV defusing, the changelog version check.
- **e2e**: the seed gains several controls with failing rules and
  distinct evidence, parked repositories with three reasons,
  `last_changed_at` spread over every age bucket and 30 days of
  snapshots. Specs per page (filters round-trip through the URL, export
  downloads the expected rows, tree expand/collapse, globex never
  appears), axe on every new page, visual snapshots in the pinned
  Playwright image.
- **Tone**: one assertion per badge family that a failing value carries
  the `bad` tone (the INV-0023 F1 gap).

<!--docz:testing:end-->

<!--docz:rollout:start-->
## Migration / Rollout Plan

UI and API ship together in rc releases from `v2` (Rule 6). The
mockup is not a phase: it was updated and accepted with this design.
Each phase builds its pages to the accepted mockup, and a page that has
to differ from it changes the mockup in the same pull request. The
work splits three ways (decided 2026-10-18):

1. **The INV-0023 fixes**, a small pull request now (IMPL-0032 Phase 1):
   F1 (rule tone), F2 (crumb), F4 (remediable wording), F7.3 (status
   screenshot).
2. **Every API addition in the table above, and persisting next due**,
   in IMPL-0030 beside the remediation API they share the schema and
   the EvaluationWorkflow version gate with.
3. **The UI** in IMPL-0032 once IMPL-0030 merges: foundations (restore
   `csv.ts` and `RangeTabs`, Export, the IMPL-0031 6.3 keyboard pass),
   Findings page, Orgs tree, Fleet panels, the Remediations view and
   the other remediation panels, and the changelog (`CHANGELOG.yaml`
   seeded with rc.9 to rc.11, the `lint-ui` version check, the status
   card).

<!--docz:rollout:end-->

<!--docz:open-questions:start-->
## Open Questions

**OQ1. What the page and the API call a row.**

- (a) page **Findings**, API **`/results`** and `ResultRow`; operators
  search for "findings", the schema has results.
- (b) both **Results**; consistent, but loses the word people look for.
- (c) both **Findings**; reintroduces a type name the controls line
  deleted on purpose.
- other: keep both words with distinct meanings: a result is any
  control outcome, a finding is a result that failed.

**Decision (2026-10-18):** other. `/results` serves results of any
status; the Findings page lists findings (`status=non_compliant`) and
has no status filter. See [Vocabulary](#vocabulary).

**OQ2. Repository rows in the orgs tree.**

- (a) ✅ recommended: load per org on expand from `/repositories`
  with `compliance` added; the page stays fast at hundreds of
  repositories per org.
- (b) one `/orgs?include=repositories` call; one request, unbounded
  payload.
- other:

**Decision (2026-10-18):** (a).

**OQ3. A `total` on `/results`.**

- (a) ✅ recommended: return `total` (a `count(*)` over the same
  filter in the same read-only transaction) for "N of M" and the export
  cap notice.
- (b) no total, "Load more" only; cheaper, but the page cannot say how
  much is left.
- other:

**Decision (2026-10-18):** (a).

**OQ4. The changelog's source.**

- (a) ✅ recommended: curated `CHANGELOG.yaml`, embedded in the UI
  bundle, gated by a `lint-ui` check that the newest entry is the
  chart `appVersion`.
- (b) generated from the git-cliff `CHANGELOG.md`; no upkeep, but the
  entries are commit subjects, not release notes.
- (c) a link to GitHub releases only; nothing to maintain or render.
- other:

**Decision (2026-10-18):** (a).

**OQ5. Parked by reason chart.**

- (a) ✅ recommended: a bar list, as DESIGN-0036 settled for every
  share chart.
- (b) the mockup's donut; one exception to the chart rules.
- other:

**Decision (2026-10-18):** (a).

**OQ6. Exporting the findings filter.**

- (a) ✅ recommended: page `/results` in the browser up to 10,000
  rows, with a notice when capped; no new endpoint.
- (b) a server-side `GET /results.csv` streaming the whole filter;
  unbounded, and a second representation to keep defused.
- (c) export the loaded rows only.
- other:

**Decision (2026-10-18):** (a).

**OQ7. Remediation-gated panels before IMPL-0030.**

- (a) ✅ recommended: render them with an empty state decided from
  `Status.apps`; the layout is final and nothing moves when remediation
  lands.
- (b) hide them until a `remediate` App exists; a sparser page that
  changes shape on upgrade.
- other:

**Decision (2026-10-18):** (a).

**OQ8. Where a findings row goes.**

- (a) ✅ recommended: the finding drawer, deep-linked with `?open=`,
  as the mockup and DESIGN-0036 OQ9 drew it; triage stays on one page
  with its filters, and Re-evaluate sits next to the evidence.
- (b) navigate to the repository page's Controls tab; no drawer to
  build, but every row is a page load and the filter context is lost.
- other:

**Decision (2026-10-18):** (a).

**OQ9. The ⌘K command palette.**

- (a) ✅ recommended: bring it back (`ui/command.tsx` survived) over
  `/repositories?q=`, controls and orgs; the mockup draws it and the
  fleet is too big to reach a repository by browsing.
- (b) leave it out; the `/repositories` search covers the need.
- other:

**Decision (2026-10-18):** (a).

**OQ10. How the mockup catches up.**

- (a) ✅ recommended: update the one mockup in place to the controls
  line and this design's pages; one reference, as DESIGN-0036 OQ17
  chose.
- (b) a new controls mockup beside it, the findings-era one archived;
  a clean start, two files to explain.
- (c) stop maintaining a mockup; the design docs and e2e screenshots
  are the reference.
- other:

**Decision (2026-10-18):** (a), with the mockup updated inside this
design's pull request rather than in an IMPL phase: the mockup is what
specifies the UI, so it is accepted together with the design, before
any implementation.

<!--docz:open-questions:end-->

<!--docz:references:start-->
## References

- INV-0023 (the rc.11 fixes, IMPL-0032 Phase 1)
- DESIGN-0036 (design language, tone table, charts, OQ5–OQ15, OQ17)
- DESIGN-0027 (read-only API, status page, privacy guard)
- IMPL-0029 Phase 8 (the controls API and UI), IMPL-0031 (6.3)
- IMPL-0030 (remediation, which feeds the gated panels)
- `ui/design/repo-guardian-mockup.html`

<!--docz:references:end-->
