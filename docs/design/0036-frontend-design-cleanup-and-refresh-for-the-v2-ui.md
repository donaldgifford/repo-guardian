---
id: DESIGN-0036
title: "Frontend design cleanup and refresh for the v2 UI"
status: Approved
author: Donald Gifford
created: 2026-10-15
---

<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN-0036: Frontend design cleanup and refresh for the v2 UI

> **Approved 2026-10-16.** Implemented by
> [IMPL-0031](../impl/0031-frontend-design-refresh-for-the-v2-ui.md); the
> mockup `ui/design/repo-guardian-mockup.html` is kept in step with it.

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
  - [What the UI is today](#what-the-ui-is-today)
  - [Two sources, two jobs](#two-sources-two-jobs)
  - [Constraints already in force](#constraints-already-in-force)
  - [The controls rework is coming](#the-controls-rework-is-coming)
- [Detailed Design](#detailed-design)
  - [Design language](#design-language)
  - [Fonts under the CSP](#fonts-under-the-csp)
  - [Shell](#shell)
  - [Page anatomy](#page-anatomy)
  - [Navigation map](#navigation-map)
  - [Fleet](#fleet)
  - [Rules and rule detail](#rules-and-rule-detail)
  - [Orgs and org detail](#orgs-and-org-detail)
  - [Findings and the detail drawer](#findings-and-the-detail-drawer)
  - [Repository](#repository)
  - [Status page](#status-page)
  - [Component inventory](#component-inventory)
  - [Charts](#charts)
  - [Surviving the controls rework](#surviving-the-controls-rework)
  - [Accessibility](#accessibility)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

The v2 business UI shipped in IMPL-0025 Phases 20 to 22 with shadcn's
default light palette, a text-link header, native `<select>` filters and
a single line chart. It works, and it looks like a scaffold. Two
source mockups informed what it should become. A sizzle reel settled
the **look**: one dark charcoal theme, a mono-and-sans type pairing,
3px radii, filled tinted badges, dense tables. A platform mockup
contributed **layout ideas**: an icon sidebar with grouped entries and
counts, a page header with breadcrumbs and range controls, a
compliance card that puts the KPIs and the trend in one frame,
sparklines in the rules table, an expandable org grid with heat-shaded
cells, a two-thirds plus one-third repository page, and a draft status
page.

Neither source is kept. Their decisions are folded into one artifact,
**`ui/design/repo-guardian-mockup.html`**: a single self-contained
page, no build step and no network, that renders every route of the
redesigned UI from an in-page fake of today's read-only API. Every
number on it comes from a field `api/openapi.yaml` serves now; a panel
that would need a field the API lacks renders as a one-line "deferred"
note naming the open question, so the gap is visible instead of drawn
over. "The mockup" below means that file. This document is the
specification; the mockup is the picture of it, and the two are kept
in step.

This design takes the look from the reel, the layouts from the
platform mockup where they survive contact with real data, maps every
panel to the field behind it, and names the handful of places where a
panel needs a field the API does not have. The status page, which both
sources treated as an afterthought, gets a proper design here because
it is the one public surface and today's version is a list.

## Goals and Non-Goals

### Goals

- One theme, declared once as Tailwind v4 `@theme` tokens taken from the
  sizzle, so components stop carrying their own colours.
- A shell worth the name: icon sidebar with groups and counts, a top bar
  with search, time zone and account, breadcrumbs, and a consistent page
  header with the scope and range controls on the right.
- Fleet, Rules, Orgs, Findings and Repository re-laid out on the
  platform mockup's patterns, each panel wired to a real API field.
- A findings detail drawer with badges, evidence and a timeline, deep
  linkable, so "what is wrong and what was done" is a click.
- A designed public status page: a state banner, components, fleet
  compliance with a sparkline, and nothing that names a repository.
- Keep every security property DESIGN-0027 established: text-only
  evidence, links built from structured fields, no inline scripts, no
  external origins, a GET-only proxy.
- Build the table, grid, drawer and chip components generically so the
  IMPL-0029 Controls views reuse them.

### Non-Goals

- No write path. The drawer gets an empty actions slot for IMPL-0029's
  re-evaluate button; this design adds no POST.
- No light theme in this change. Tokens make it a later swap (OQ1).
- No portal chrome. The platform mockup is a multi-app shell (app
  switcher, notifications bell, favourites). Repo Guardian ships alone;
  the shell is built so it can later sit inside that portal, but the
  switcher and the bell are not drawn.
- No "compliance drivers" map. The mockup's initiatives → inputs → KPIs
  flow has no data behind it and no owner.
- No BFF, session, OIDC or CSP changes beyond one optional config value
  for the documentation link. Where the CSP constrains the design
  (fonts, images) the design bends, not the CSP.
- No Grafana, operator or system dashboards; those stay generated
  (DESIGN-0022).
- No new tables or migrations. API additions are small, additive, and
  listed in one place.

## Background

### What the UI is today

`ui/web/src/router.tsx` renders a top header with a wordmark, horizontal
text links (Fleet, Rules, Orgs, Findings, History, Policy), a native
`<select>` org filter written to `?org=`, the user's name as text and a
POST sign-out button. There is no sidebar. `index.css` holds a light-only
shadcn neutral palette in oklch plus four status colours; there is no
dark mode and no font declaration. `components/ui/` has Badge (five
tones), Button, Card and Table. `components/common.tsx` has
`StatusBadge`, `ExternalLink`, `Evidence`, `Remediation`, `Tile`,
`Section`, `Loading`, `Empty` and `TrendChart`, the only Recharts use.

Fleet is four tiles, the 90d line chart, open-PR buckets as text, the
worst five rules as a link list and parked-by-reason as text. Findings
is native selects over a six-column table with Load-more and CSV
export; there is no row detail. Repository has the only timeline and
the recent-checks list. History is one small chart per org. Policy is
tiles and a table. The public status page is a component list.

### Two sources, two jobs

| Concern | Taken from | Not taken |
| --- | --- | --- |
| Colour tokens, type, radius, badge recipe, table density | sizzle reel (`--st-*`, `.badge`, `.tbl`, `.card`) | the mockup's navy palette and its HSL variables |
| Sidebar structure (groups, icons, counts), top bar controls, breadcrumbs, page header with scope and range | platform mockup | app switcher, bell, favourites |
| Fleet layout: combined compliance card, PR-age bars with threshold link, finding-age distribution, worst rules, parked-by-reason bar list, findings-by-status bar, by-rule and by-org tables | platform mockup | drivers map, PR throughput (no endpoint yet, OQ15), donuts (replaced by bar lists, see Charts) |
| Rules: kind tabs, KPIs, stacked bar, table; rule detail with per-org bars, top-reasons bar list, oldest failures | platform mockup | |
| Orgs: expandable grid, heat-shaded cells, tabs, totals row, export | platform mockup | repository-level rows under an org (OQ13) |
| Findings table and detail drawer, evidence block, timeline | sizzle reel (drawer), mockup (filter bar and result count) | the reel's `sort = pr_age` chip until the API sorts |
| Repository two-column page | platform mockup | |
| Status page: banner, components, compliance with sparkline | platform mockup draft, redesigned here | changelog (no source) |

The synthesis, and the thing to open when a sentence below is unclear,
is `ui/design/repo-guardian-mockup.html`. It is deliberately built
with hand-written CSS and SVG rather than the SPA's stack so that it
stays a design document: the implementation copies its tokens,
layouts and chart rules into Tailwind, React and Recharts, not its
code. Its fake API (`api.*` in the script) mirrors the operation set
and field names of `api/openapi.yaml`, so a mockup panel that reads a
field the schema lacks is a bug in the mockup, not a proposal.

The reel's application frame uses the sans face for page titles and the
drawer; its display face (Bricolage Grotesque) appears only in the
marketing scenes. The app ships two families, not three.

### Constraints already in force

- **CSP.** `ui/server/static.ts` sends `default-src 'self'; connect-src
  'self'; frame-ancestors 'none'; script-src 'self'`. With no `font-src`,
  `style-src` or `img-src`, each falls back to `'self'`. External fonts
  are blocked, and so is any `data:` URI Vite inlines for assets under
  4 KB (`assetsInlineLimit`). Remote avatar images are blocked; `/me`
  carries no avatar anyway.
- **Evidence is text.** ESLint bans every HTML sink; `evidence.ts`
  renders only reasons it knows. Every evidence surface in this design
  is a styled container around that output.
- **Links are built, never trusted.** `lib/pr.ts` builds PR and
  repository URLs from host, org, name and number.
- **Org scope lives in the URL** as `?org=`, retained across links; the
  Playwright scoping test depends on it.
- **Findings have no id.** Identity is `(repository_id, rule_kind,
  rule_name)`. There is no `/findings/{id}`; events are per repository
  and record status transitions only; checks are per repository.
- **No server-side sort on `/findings`**, and no aggregates beyond
  `/summary`, `/rules`, `/orgs` and `/orgs/{org}`.
- **History is per (org, rule) and capped client-side** at 4,000
  points; 90 days across six orgs and eight rules already exceeds it.
- **The status page is public and privacy-guarded.**
  `TestStatus_PrivacyGuard` fails on any string field in the `Status`
  schema outside an allowlist. Anything added to it must be a number,
  a timestamp or an enum.

### The controls rework is coming

IMPL-0029 task 8.7 replaces the Rules and Findings views with Controls
views, Policy with `/policies`, and gives Repository a controls tab and
an evaluation log; INV-0021 marks `rules.tsx`, `findings.tsx` and
`evidence.ts` as rule-shaped and slated for replacement; IMPL-0030 adds
a Remediations view. The durable deliverables here are the tokens, the
shell, the page anatomy, the Fleet page and a set of generic
components; the rule-shaped views become thin consumers so that 8.7
swaps data and columns, not chrome (OQ12).

## Detailed Design

### Design language

One theme, dark, declared once in `index.css` as Tailwind v4 `@theme`
variables. The sizzle's stage tokens map as follows.

| Token | Value | Reel source | Used for |
| --- | --- | --- | --- |
| `--color-background` | `#17191d` | `--st-bg` | page, evidence block, inset panels |
| `--color-panel` | `#1f2227` | `--st-panel` | cards, top bar, sidebar, drawer |
| `--color-panel-2` | `#262a30` | `--st-panel2` | current nav item, selected tab, meter track, hover rows |
| `--color-border` | `#31363d` | `--st-line` | every 1px rule and border |
| `--color-foreground` | `#e8eaed` | `--st-fg` | text |
| `--color-muted-foreground` | `#9aa1ab` | `--st-muted` | labels, sublines, inactive nav |
| `--color-accent` | `#5fa8f5` | `--st-accent` | links, brand mark, primary series, current timeline node, focus ring |
| `--color-ok` | `#3fb950` | `--ok` | compliant, applied, operational |
| `--color-warn` | `#d9a020` | `--warn` | foreign_pr, degraded, reason badges, unknown |
| `--color-bad` | `#f2554d` | `--bad` | non_compliant, failing, down |
| `--color-info` | `#7aa7ff` | `--info` | pr_open, secondary series |
| `--color-avatar` | `#3a4c6d` | `--avatar` | initials avatar |
| `--radius-lg` | `3px` | `--st-radius` | cards, tabs, nav items, avatar, inputs |

Chart series colours are derived from the same set: `accent` for the
primary series, `info` for a second, `ok`/`warn`/`bad` where a series
means good, caution or bad, and `muted-foreground` for not-applicable.
The mockup's categorical palette is not adopted; a chart never needs
more than the five tones above plus muted.

The existing `--color-compliant`, `--color-non-compliant`,
`--color-not-applicable` and `--color-unknown` become aliases of
`ok`, `bad`, `muted-foreground` and `warn` while views are converted,
and are removed with the last consumer.

**Type.** Sans: IBM Plex Sans 400/500/600. Mono: JetBrains Mono
400/500. Root 14px; tables, nav, chips and filters 13px; card and KPI
labels 12px, uppercase with `0.06em` tracking for stat-strip labels as
in the mockup; KPI values 22px/600 with `tabular-nums`; page titles
20px/600 in the sans face. Monospace is semantic: identifiers
(repository and rule names, reason codes, PR numbers, timestamps, filter
values, query names in subtitles) are mono; prose is sans.

**Badges** (the reel's `.badge`): mono 12px, `5px 7px`, no radius,
background `color-mix(in srgb, <tone> 18%, transparent)`, text
`color-mix(in srgb, <tone> 65%, white)` (75% in the first draft; IMPL-0031 6.1's axe run put the bad tone on a selected row under AA). One tone table in
`lib/format.ts` covers every documented value, with `mute` for anything
unknown (both enums are open):

| Field | Value | Tone |
| --- | --- | --- |
| status | `compliant` | ok |
| status | `non_compliant` | bad |
| status | `not_applicable` | mute |
| status | `unknown` | warn |
| remediation | `pr_open` | info |
| remediation | `applied` | ok |
| remediation | `foreign_pr` | warn |
| remediation | `disabled`, `dry_run`, `none` | mute (`none` renders "—" in tables) |
| reason | any, in the drawer | warn under `non_compliant`, else mute |
| component state | `operational` / `degraded` / anything else | ok / warn / bad |
| rule kind | `file`, `setting`, `branch_protection`, … | outline (border only, muted text) |

**Surfaces.** A card is `panel`, 1px `border`, 3px radius, a header of
a 13px/600 title and a 12px muted subtitle, then a body. Cards may
carry a **stat strip** under the header (the mockup's compliance card):
a row of label-over-value cells separated by hairlines. No shadows
except the drawer edge; no gradients except the chart area fill.

**Density.** Tables at 13px with `9px 10px` cells and hairline rows;
numeric columns right-aligned tabular; a hover tint of `panel-2`; a
selected row tinted `accent` at 12%.

**Motion.** None of the reel's entrance animations ship. Drawer slide
and popover fades at 200ms, off under `prefers-reduced-motion`.

### Fonts under the CSP

Fonts are bundled: `@fontsource/ibm-plex-sans` (latin, 400/500/600) and
`@fontsource/jetbrains-mono` (latin, 400/500), imported from `main.tsx`
so Vite emits hashed woff2 files under `/assets/`, which the BFF already
caches for a year. `build.assetsInlineLimit: 0` keeps every asset a
file rather than a `data:` URI, and a build test fails if `dist/`
contains a `data:` URL or a `fonts.googleapis.com` reference.
`font-display: swap`. Both families are SIL OFL; licences ship with the
other third-party notices. The CSP does not change (OQ2).

### Shell

The frame is a 40px top bar over a two-column body: a 220px sidebar
(the mockup's width, a little wider than the reel's 196px to fit icons
and counts) and a scrolling main area. Both bars are `panel` on a
`background` page.

**Top bar**, left to right:

- Brand: the shield mark in `accent`, "Repo Guardian" in `foreground`
  600, and the mockup's muted qualifier `compliance · read-only`. The
  brand links to `/`. There is no app switcher; the slot to the left of
  the brand is reserved so a portal can add one later.
- Right: a search button labelled `⌘K` that opens the command palette;
  a time-zone control; the avatar.

**Command palette** (`⌘K` / `Ctrl-K`): a dialog with one input that
searches repositories through `GET /repositories?q=` (name prefix,
scoped by `?org=`), and matches rules and orgs from the already-loaded
`/rules` and `/orgs` lists. Results are grouped (Repositories, Rules,
Orgs); Enter navigates. It is the primary way to reach a repository
page and replaces the mockup's placeholder "Repository (example)" nav
entry (OQ11).

**Time zone.** Timestamps render in the browser's zone by default. The
control toggles `Local` / `UTC`, persisted in `localStorage`, and every
timestamp goes through one `formatTime` helper that honours it. Hovering
a timestamp shows the other zone.

**Avatar**: a 20px `avatar`-coloured square with initials from
`me.name` (first letters of the first two words, else the first two
characters of `subject`). It opens a menu with the display name, the
subject in mono, the docs link when configured, and Sign out, which
stays a POST form to `/auth/logout` so the BFF's Origin check keeps
working.

**Sidebar**: groups with 10.5px uppercase tracked labels, each entry an
icon, a label and an optional right-aligned mono count.

- `BUSINESS`: Fleet (grid icon), Rules (list), Orgs (boxes), Findings
  (activity, count = `summary.findings.non_compliant`).
- `PUBLIC`: Status page (check).
- `DOCS`: Documentation (book), shown only when the BFF's `/ui/config`
  carries a `docs_url` (new optional `UI_DOCS_URL` env; the one BFF
  change in this design).

The current item is `panel-2` with `foreground` text and an `accent`
left rule; others are `muted-foreground`. The nav is one array (`label`,
`to`, `icon`, `group`, optional count selector) so IMPL-0029 renames
entries by editing data. A footer line in 11px mono shows the policy
version prefix and rollout state from `/policy` and links to `/policy`
(OQ4). Below 900px the sidebar collapses behind a top-bar button.

**Org scope** is a page-header control (below), not a top-bar one: the
mockup places it beside the range control, and that is where the eye
looks for "what am I looking at". It writes `?org=` exactly as today;
the label is `All orgs (N)` where `N` is `orgs.items.length` when
`me.all_orgs`, else `me.orgs.length`.

### Page anatomy

Every authenticated page uses the same header block:

1. Breadcrumbs in 12px: `Repo Guardian / <Section> [/ <item>]`, derived
   from the route (and the drawer state on Findings).
2. Title at 20px/600, with an optional outline badge after it (a rule's
   kind, `org`, `active`/`parked`).
3. A one-line muted subtitle that says what the page measures, in plain
   words, naming the endpoint in mono where the mockup did
   ("Fleet compliance % from `/compliance/history`").
4. Right-aligned controls: the org scope popover; where the page has a
   time dimension, a segmented range `1d | 7d | 30d | 90d`; page actions
   as outline buttons (Export, Findings for this rule).

Range and scope are URL state (`?range=`, `?org=`), so every page is a
shareable link and the browser back button works.

### Navigation map

| Nav | Route | Today | Change |
| --- | --- | --- | --- |
| Fleet | `/` | `fleet.tsx` | re-laid out |
| Rules | `/rules`, `/rules/$kind/$name` | `rules.tsx` | re-laid out |
| Orgs | `/orgs`, `/orgs/$org` | `orgs.tsx` | re-laid out (expandable grid) |
| Findings | `/findings` | `findings.tsx` | re-laid out, drawer added |
| ⌘K → repository | `/repos/$id` | detail only | re-laid out; reached by palette and links |
| Status page | `/status` | `status.tsx` | redesigned |
| (footer) | `/policy` | `policy.tsx` | restyled, reached from the sidebar footer (OQ4) |
| (link) | `/history` | `history.tsx` | restyled, reached from the compliance card (OQ4) |

### Fleet

The mockup's layout, top to bottom, with the data behind each panel.

**Compliance card** (two-thirds width). Title `Compliance (<range>)`,
subtitle "Fleet compliance % from `/compliance/history`; compliant ÷
(compliant + non_compliant) over active repositories." Then:

- A **stat strip** of five cells: Tracked (`summary.tracked` repos),
  Compliant (`summary.compliant_percent` with a delta against the
  previous snapshot), Failing (`summary.findings.non_compliant` and the
  number of rules with any failure from `/rules`), Parked (sum of
  `summary.parked[].count` and the number of reasons), Snapshots (point
  count in range and the cadence word derived from the median gap
  between points). A null percent reads "unmeasured", never 100%.
- **Legend pills** for the four series the history points carry:
  compliant %, failing findings, not applicable, unknown. The pills are
  a radio group: exactly one series is drawn at a time and the y axis
  rescales to it. There is no right axis and never two scales in one
  frame; a reader who wants two series side by side opens History,
  where each gets its own small chart. The pill shows the series'
  current value, so the strip, the pills and the chart agree by
  construction. Pills are buttons with `role="radio"` and
  `aria-checked`.
- The **area chart** over the fleet series within `?range=`: line and
  fill in `accent`, mono 11px axes, y from a nice floor below the
  minimum to 100, x ticks at a cadence that fits the range. The series
  comes from `/compliance/history` aggregated to one point per snapshot;
  until the aggregate parameter exists it is `complianceSeries()` over
  the per-rule points, with the `truncated` notice when the cap trips
  (OQ6). A "full history" link goes to `/history`.

Deltas (compliant, failing) are computed in `lib/viewmodel.ts` from the
same series the chart draws, so the strip and the chart cannot
disagree. The reel's "+38 this week" on Tracked has no source and is
not drawn (OQ7). Stat values use proportional figures (they are read
one at a time); every number in a table column is tabular so the
digits align.

**Open PRs by age** (one-third width). Subtitle "Remediation PRs opened
by repo-guardian[bot]. `<stale_after>`+ counts as stale.", threshold
from `summary.stale_after`. A bar chart over `summary.open_prs` in
bucket order; below it, the mockup's one-liner `<stale_prs> PRs over
threshold → findings?pr_stale=true` as a link.

**Finding age distribution** (full width). Subtitle "How long
non-compliant findings have been open. Under 30 days is the healthy
threshold." A single horizontal stacked bar with six segments (`<1d`,
`1-7d`, `7-30d`, `30-90d`, `90-180d`, `180d+`) shaded ok → warn → bad,
a pill `<p>% under 30d · healthy threshold`, and a legend row of label
and share. This needs a histogram of `status_since` over
`non_compliant` findings, which no endpoint returns; it ships with the
`finding_age` addition to `/summary` (OQ8) and is hidden until then.

**Second row**, three cards:

- **Worst rules**: `worstRules(rules, 5)` as name, kind, percent with
  the `previous_percent` trend, and a meter whose fill is `bad` under
  70%, `warn` under 90%, else `ok`. Rows link to the rule.
- **Parked by reason**: a bar list over `summary.parked[]`, sorted
  descending, each row the reason in mono, the count and its share,
  and a single-colour bar scaled to the largest row. Parking is a
  repository state, not a finding, so rows do not link to Findings;
  they link to a repository list filtered by `park_reason` once one
  exists (OQ11), and are static until then.
- **Findings by status**: one horizontal stacked bar over the sum of
  `/rules` counts (compliant `ok`, non_compliant `bad`, not_applicable
  muted, unknown `warn`) with the total at its end and a legend row.
  Its subtitle says the percent counts only the first two, which is
  the one sentence that stops "why is 84% not 25,000 of 33,000".

**PR throughput (14d)**, opened versus closed remediation PRs per day,
has no source: events are per repository. The card is not drawn in the
mockup and is not built until an aggregate exists (OQ15).

**Third row**, two tables:

- **Compliance by rule** from `/rules`: Rule, Kind (outline badge),
  Compliant, Failing, N/A, %, Trend (a 20-point sparkline from the
  rule's history, fleet-aggregated). Rows link to the rule.
- **Orgs** from `/orgs`: Org, Tracked, Failing, Parked, and a
  Compliance column that is a meter plus the percent. Rows link to the
  org.

With `?org=` set, the same layout reads from `/orgs/{org}` and the
Orgs table becomes **Not applicable by reason** (`not_applicable[]`).

### Rules and rule detail

**Rules.** Header with kind tabs (`All | file | setting |
branch_protection`, values from the kinds present). A KPI row of the
four worst rules (name, percent, failing and n/a counts, trend). A
stacked bar per rule (compliant `ok`, non_compliant `bad`,
not_applicable muted, unknown `warn`), rendered as flex `div` segments
with a 2px surface gap rather than SVG so the bars stay 10px tall at
any width, a row total at the end and one legend. Then the full table:
Rule, Kind,
Compliant, Failing, N/A, Compliance, Trend in points, chevron. All from
`/rules`.

**Rule detail.** Title is the rule name with a kind badge; subtitle is
the policy `description` from `/rules/{kind}/{name}`. Right: an outline
button "Findings for this rule" → `/findings?kind=&rule=`. KPIs:
Compliance with trend, Compliant, Failing with "oldest: N days" from
`oldest_failures[0].status_since`, Not applicable. Two-thirds: the
per-org table (`orgs[]`) with meter columns. One-third: a Top reasons
bar list over `top_reasons[]` and an Oldest failures list over
`oldest_failures[]` (repository, "failing for N days"), each linking to
the repository.

### Orgs and org detail

**Orgs** is the mockup's expandable grid. Above it a totals line of
badges (Tracked, Failing in `bad`, Parked in `warn`) summed from
`/orgs`, then tabs that choose which columns are heat-shaded:
`Compliance` (Compliant `ok`, Failing `bad`), `Parked` (Parked `warn`),
and `PR activity` once an open-PR count per org exists (OQ14). A
toolbar holds the org scope, a parked toggle (`All | Hide parked`),
and Expand/Collapse all. Export writes the visible grid as CSV through
the existing defused CSV helper.

Each org row shows the org name, `<pct>% compliant · <n> parked`, and
the row count badge, with numeric columns Tracked, Compliant (count and
percent), Failing (count and percent), Parked. Cells shade by share of
tracked: the tone at an opacity proportional to the percentage, so a
column reads as a heat map. Expanding an org fetches `/orgs/{org}` and
lists its **rules** as child rows with the same columns (OQ13: the
platform mockup drew repositories here, but no endpoint gives per-repository
counts cheaply, and per-rule rows answer "where in this org is the
problem" with data that exists). A totals row closes the grid.

**Org detail** (`/orgs/$org`): title with an `org` badge, KPIs
(Tracked, Compliance, Failing, Parked), two-thirds compliance area
chart over the org's history and the rules table with meters,
one-third Not-applicable-by-reason and Parked-by-reason bar lists from
`OrgDetail.not_applicable[]` and `OrgDetail.parked[]`.

### Findings and the detail drawer

**Filter bar.** A card holding filter chips (the reel's `key = value`
style, mono) for status, reason, remediation, kind, rule, org and
`pr_stale`, a trailing `+ filter` chip, and on the right the mockup's
result count: `<loaded> of <summary.findings.non_compliant>` when the
status filter is `non_compliant`, else just the loaded count. Chips
open a popover with that filter's options; state stays in the URL
through `patchSearch`, so every existing deep link works. A
`sort = pr_age desc` chip appears only once the API sorts (OQ5). CSV
export is an outline button in the page header.

**Table.** Repository (`org/name` mono, `foreground`, with the org as
a muted second line as in the mockup), Rule (mono), Status badge,
Reason (mono text, "—" when absent), Remediation badge ("—" for
`none`), PR (`#n` built with `prLink`, "—" when absent), Age (PR age
from `pr.created_at` via `ageDays`; "—" otherwise), Since
(`status_since`, relative, exact on hover). Rows are keyboard
activatable; the selected row is tinted. Load-more and the infinite
query are unchanged.

**Drawer.** A 470px right panel in `panel` with a left `border`,
pushing the table at 1600px and wider (the Age and Since columns hide
so the rest fit without a scrollbar) and sliding over it behind a scrim
below that. Opening writes
`?open=<repository_id>/<kind>/<name>`; a reload or shared link looks
the row up in loaded pages or fetches it through `/repositories/{id}`
(OQ9). Escape, the close button and the scrim close it; focus moves in
on open and back to the row on close. Contents:

1. Title `org/repository` as an external link built with `repoLink`;
   mono subtitle `rule <name> (<kind>) · since <status_since>`. Findings
   have no id, so identity is the subtitle.
2. Key/value block: Status badge; Reason badge; Remediation badge, then
   `#<number>` as a `prLink` and the branch (`pr.head`) in mono when
   present; `matched_term` after the number for a foreign PR.
3. Evidence: a `background` inset, mono 12px, rendering
   `describeEvidence(finding)` exactly as today, `✗` in `bad` tint
   before a failed assertion. Unknown reasons show the code alone.
4. Timeline: a left-rule list with square nodes, newest last, latest in
   `accent`. Composed client-side from `/repositories/{id}/events`
   filtered to `source = finding` for this rule plus repository events
   of kind `parked`, `unparked`, `renamed`, `transferred`, and from
   `/repositories/{id}/checks` since `status_since`, rendered as
   `re-checked · <outcome>`; a check after an open PR's `created_at`
   adds `PR #n still open (<age>)`. Capped at 20 with a "full timeline"
   link to the repository.
5. Footer: an empty `actions` slot reserved for IMPL-0029's re-evaluate
   control, and "Open repository".

### Repository

The mockup's two-column page. Header: `org/name` with an
`active`/`parked` badge; subtitle `checked <relative> · installation
<id> · owner <custom property when known>`; right, "Open on GitHub".
Left two-thirds: the Findings table (Rule, Status, Reason / evidence,
Remediation), then Recent checks (Time, Trigger badge, Outcome badge,
Duration, Error). Right one-third: a Repository facts card (default
branch, park state and reason, catalog parse state, policy version of
the last check, from `RepositoryDetail`), and the Timeline sharing the
drawer's component over the merged events. Repository is reached from
the palette, the findings drawer, the org grid and the rule detail's
oldest failures; `/repos` as a standalone list is deferred (OQ11).

### Status page

Public, unauthenticated, outside the shell, aggregate-only. Today it is
a component list. The design is the platform mockup's draft made deliberate,
within what the `Status` schema allows and with the additions the page
is worth.

Layout, centred at 760px on the `background` page:

1. **Header row**: the shield mark and "Repo Guardian status"; right, a
   muted `updated <relative> · refreshes every 30s` matching the API's
   refresh cadence.
2. **State banner**: a card whose border takes the overall tone. A
   pulsing dot, the headline (`All systems operational`, `Degraded
   performance`, or `Service disruption` mapped from `state`), the
   subline "Public, aggregate-only. No org, repository or rule names
   appear here." and the state badge.
3. **Components** (left): one row per `components[]` entry with a tone
   dot, the component name, its `detail` string and a state badge. The
   names are the API's (`webhooks`, backlog per queue, and the rest the
   status page already reports); `webhooks` keeps its informational
   footnote.
4. **Fleet compliance** (right): the percent at 40px, a 10px meter in
   `ok`, the footnote `measured: <bool> · compliant ÷ (compliant +
   non_compliant)`, and a 30-day sparkline. The sparkline needs a
   numeric series on the public schema: `compliance.history[]` of
   `{at, percent}` (OQ10). Until it exists the card shows the number and
   the meter.
5. **Operations strip** (full width, OQ10): three cells that the sizzle
   asked for and the schema lacks, each a number or enum and so
   admissible under the privacy guard: `Repositories checked in the
   last 24h` as `checked / tracked`, `Last policy rollout` as
   `<rollout_state> · <completed_at>`, and `Last snapshot` as a relative
   time. Hidden until the fields exist.
6. **Footer**: `GET /api/v1/status · served from an in-process cache`
   in muted mono, and a link to the public documentation when
   `docs_url` is configured. No changelog: there is no source for one
   in the API, and a hand-maintained list on a status page goes stale.

`unknown` before the first refresh renders the banner in muted tone
with "Status not yet available". The page carries no string the schema
does not give it; `TestStatus_PrivacyGuard` keeps policing the schema,
and a Playwright test asserts the page text contains no org or
repository name from the seed.

### Component inventory

New or changed primitives in `components/ui/`, shadcn-style and
hand-copied as before; radix-based ones use `radix-ui` for focus and
keyboard behaviour (OQ3).

| Component | Role |
| --- | --- |
| `Badge` | tones ok/warn/bad/info/mute plus `outline`; `StatusBadge`, `RemediationBadge`, `ReasonBadge`, `KindBadge`, `StateBadge` wrap it with the tone table |
| `Kpi` | label, value, subline, delta; replaces `Tile` |
| `StatStrip` | hairline-separated label/value cells inside a card header |
| `Pill` | legend toggle with a colour dot and `aria-pressed` |
| `Card` | header with title, subtitle and right slot; body |
| `PageHeader`, `Breadcrumbs` | the page anatomy |
| `Table`, `DataTable` | density, numeric alignment, selectable and keyboard-activatable rows, column spec and row key |
| `GroupGrid` | expandable parent/child rows, heat-shaded numeric cells, totals row, expand/collapse all |
| `Drawer` (radix Dialog, side right) and `DetailDrawer` | 470px panel, focus trap, URL-backed open state; title/subtitle/kv/actions layout |
| `Popover`, `DropdownMenu`, `CommandPalette` (radix) | org scope, chip editors, avatar menu, ⌘K |
| `Tabs` | segmented range and kind/heat tabs; values mirrored to the URL |
| `Chip`, `FilterChips` | `key = value` filters over search params |
| `Meter`, `DistBar` | threshold-toned bar; six-segment distribution bar with legend |
| `Avatar` | initials square; no image path |
| `Sidebar`, `TopBar` | shell; data-driven nav array |
| `Timeline` | left-rule event list shared by drawer and repository |
| `TrendChart`, `BarBuckets`, `StackedBars`, `BarList`, `Sparkline` | the chart set |
| `TimeZoneToggle`, `formatTime` | Local/UTC |

`components/common.tsx` shrinks to `ExternalLink`, `Evidence`,
`Loading`, `Empty` and `Section`; the rest move to their own files.

### Charts

Recharts stays (DESIGN-0027 fixed shadcn charts). Five chart
components: `TrendChart` (area, one series at a time), `BarBuckets`
(single series bars), `StackedBars` (per-category stacked status
counts, plain elements), `BarList` (ranked rows with count, share and
a single-colour bar), `Sparkline` (20-point line, no axes). `Meter`
and `DistBar` are plain elements. All read colours from the theme
variables, use the mono face for axes at 11px, draw solid hairline
grid lines in `border`, and ship an accessible alternative: an
`aria-label` summary and a `table` toggle in the card header that
swaps the plot for the same data as a dense table.

The rules the mockup follows, which the implementation keeps:

- **One axis per chart.** Two measures of different scale get two
  charts (History's small multiples), never a second y axis.
- **No categorical palette.** The theme's `accent` and `info` are too
  close to tell apart for a colour-vision-deficient reader (an OKLab
  distance of about 3, against a floor of 8), so no chart asks a reader
  to separate series by hue. Magnitude is drawn in `accent`; status in
  the four status tones, and nowhere else; shares as ranked bar lists
  rather than donuts, where the label does the identifying.
- **Marks.** Lines 2px with a 4.5px end dot ringed in the panel colour;
  area wash at 10% opacity; bars at most 24px wide with a 4px rounded
  data end; 2px surface gaps between stacked segments; the end-of-line
  value is the one direct label, never a number on every point.
- **Hover by default.** Line and area charts get a crosshair and a
  tooltip, bars and cells a per-mark tooltip; hit targets are the full
  column, not the mark.
- **Percent axes** never start at zero when the series lives in a
  narrow band, and a `nice` floor and ceiling bracket the data so the
  trend is legible; the end label states the current value.
- **Bar lists** are sorted descending, show count and share, and
  never carry more than eight rows; the rest fold into "other".

### Surviving the controls rework

- `DataTable`, `GroupGrid`, `DetailDrawer`, `FilterChips`, `Timeline`,
  `Kpi`, `StatStrip` and the charts know nothing about rules or
  findings. They take column specs, row keys and render props.
- `findings.tsx` and `rules.tsx` become column specs, filter specs and
  a drawer body. The Controls views are other specs against other
  endpoints.
- `evidence.ts` is untouched except for its container; INV-0021
  replaces its renderers, not this design.
- The sidebar nav is an array; renaming Rules to Controls and Findings
  to Evaluations is a two-line edit and the count moves with the entry.
- The drawer's `actions` slot is where 8.7's re-evaluate button lands,
  so the BFF's eventual POST route needs no layout change.

### Accessibility

- Contrast against the actual hex values: `muted-foreground` on `panel`
  6.1:1, `foreground` on `background` 14.6:1, every badge tone at 65%
  toward white on an 18% tint clears 5:1. Heat-shaded cells keep their
  text in `foreground` and never rely on the shade alone (the number
  and the percent are printed). `@axe-core/playwright` runs on Fleet,
  Orgs, Findings with the drawer open, Repository and Status.
- Focus is a 2px `accent` ring with 2px offset on every interactive
  element, including table rows and legend pills.
- The drawer and the palette are dialogs: labelled, focus-trapped,
  Escape closes, focus returns.
- Charts carry text alternatives; colour is never the only signal
  (badges carry text, deltas carry arrows, meters print the percent,
  distribution segments have a legend).
- No motion beyond drawer and popovers, none under
  `prefers-reduced-motion`.

## API / Interface Changes

The shell, tokens, page anatomy, Fleet compliance card, Rules, Orgs
grid (compliance and parked tabs), Findings table, drawer, Repository
and the status page banner, components and compliance number all ship
on today's API. The panels below each need one additive change; each is
independent, each is an open question, and each panel is hidden until
its field exists so the UI never shows a placeholder.

| Addition | Backs | OQ |
| --- | --- | --- |
| `GET /compliance/history?aggregate=none\|fleet\|org` (sum per `snapshot_at` in SQL, percent floored in SQL like `ComplianceByRule`) | Fleet area chart and rule sparklines without the 4,000-point cap | OQ6 |
| `GET /findings?sort=default\|pr_age` with the cursor bound to the sort | the `sort = pr_age` chip | OQ5 |
| `Summary.finding_age[]` `{bucket, count}` over `non_compliant` findings' `status_since`, six buckets | Finding age distribution | OQ8 |
| `Status.compliance.history[]` `{at, percent}` (30 daily points), `Status.operations{checked_24h, tracked, rollout_state, rollout_completed_at, last_snapshot_at}` | status sparkline and operations strip | OQ10 |
| `OrgSummary.open_prs` (count of `pr_open` findings per org) | Orgs "PR activity" tab | OQ14 |
| A PR throughput aggregate over `finding_events` (opened/closed per day) | Fleet PR throughput card | OQ15 |

Every addition goes through `api/openapi.yaml` first, regenerates the
server (`make generate-api`) and the client (`make generate-ui-api`),
and is drift-gated by `make lint-api` and `make lint-ui`. New status
fields are numbers, timestamps and enums only, so
`TestStatus_PrivacyGuard` passes without widening its allowlist.

BFF: one optional env, `UI_DOCS_URL`, surfaced as `docs_url` in
`/ui/config`; nothing else. Chart: the matching optional value
`ui.docsUrl`. `ui/package.json` grows by `radix-ui`,
`@fontsource/ibm-plex-sans`, `@fontsource/jetbrains-mono` and, as a dev
dependency, `@axe-core/playwright`.

## Data Model

None. No table, column or migration changes. The optional API
additions are new sqlc queries over existing columns
(`compliance_snapshots`, `findings.status_since`, `finding_events`,
`checks`, `policy_versions`).

## Testing Strategy

- **Unit (`bun test ./web`).** Table-driven tests for the new
  `viewmodel.ts` functions: deltas from a series (one point, two points,
  a gap at the range edge), snapshot cadence word, fleet aggregation,
  heat-shade opacity, the timeline merge (ordering, "still open"
  synthesis, the cap), initials, the org-count rule, `formatTime` in
  Local and UTC, and the badge tone table over every documented value
  plus an unknown one.
- **Component tests.** Badge tones; `DataTable` row activation on
  Enter; `GroupGrid` expand/collapse and totals; `Drawer` focus trap,
  Escape and focus return; `FilterChips` round-trip through search
  params; `Pill` `aria-pressed`; `Kpi` delta colouring by direction.
- **Build guard.** A test over `dist/` asserts no `data:` URL and no
  Google Fonts reference, so an inlined asset cannot break the CSP.
- **Playwright.** Existing specs updated where selectors change (Sign
  out in the avatar menu; chips instead of selects; the breadcrumb
  row). New specs: drawer from a row and from `?open=`; range tabs
  change the chart query; org scope popover writes `?org=`; ⌘K finds
  the seeded repository; Orgs grid expands an org to its rules; status
  page contains no seeded org or repository name; axe passes on five
  pages. Screenshot tests for Fleet, Orgs, Findings with the drawer,
  Repository and Status under Chromium with the seeded stack (OQ16).
- **Contract drift.** `ui/scripts/check-api.ts` keeps failing on any
  divergence from `api/openapi.yaml`; each API addition regenerates both
  sides in the same change, with a sqlc test and an `apitest` case.
- **Security regressions.** The ESLint HTML-sink ban and
  `eslint-ban.test.ts` are unchanged and cover every new component; the
  "HTML in evidence renders as text" e2e test is re-pointed at the
  drawer.
- **Chart.** A helm-unittest case for `ui.docsUrl` rendering into the
  UI env; nothing else changes.

## Migration / Rollout Plan

A UI-image change with no data migration, shipped in a v2 release
candidate. The IMPL that follows is expected to phase it as:

1. Tokens, fonts, Vite inline limit, build guard, primitives and charts.
2. Shell: top bar, sidebar, palette, time zone, avatar menu, page
   anatomy; Playwright selector updates.
3. Fleet (on today's API), Rules and rule detail.
4. Orgs grid and org detail; Findings filter bar, table and drawer;
   Repository.
5. Status page redesign on today's schema.
6. Axe, screenshots, `docs/usage/ui.md` text and screenshots.
7. API additions, each its own task, each un-hiding its panel: history
   aggregate, findings sort, finding age, status fields, org open PRs.

Rollback is the previous UI image tag; the API stays backward
compatible throughout. This should land before IMPL-0029 task 8.7 so
the Controls views are built on the new components (OQ12).

## Open Questions

**OQ1. Theme scope.**

- (a) ✅ recommended: dark only, the sizzle's "settled Platform UI";
  tokens make a light theme a later swap. The platform mockup's sun icon is not
  drawn.
- (b) dark and light following `prefers-color-scheme`; doubles contrast
  and screenshot work for a surface nobody has asked to see in light.
- (c) a user toggle persisted in `localStorage`; (b) plus state.
- other:

**OQ2. Fonts.**

- (a) ✅ recommended: bundle IBM Plex Sans and JetBrains Mono via
  fontsource (about 150 KB of woff2), CSP untouched; do not ship
  Bricolage Grotesque, which the application frame never uses.
- (b) system fonts only; zero bytes, but the look depends on the
  mono/sans pairing and tabular numerals.
- (c) also ship Bricolage for page titles; a third family for a face
  the reel reserves for marketing scenes.
- other:

**OQ3. Primitive base for dialog, popover, palette and tabs.**

- (a) ✅ recommended: `radix-ui`, which shadcn's own components wrap;
  focus trapping, typeahead and ARIA come for free and the copied
  components stay close to upstream.
- (b) native `<dialog>` and the `popover` attribute, hand-rolled; no
  dependency, but focus return, scrim and keyboard handling are ours to
  get right and test.
- other:

**OQ4. History and Policy, which neither mockup puts in the nav.**

- (a) ✅ recommended: keep both routes out of the primary nav; the range
  control and a "full history" link cover History, and a sidebar footer
  line (policy version and rollout state) links to Policy.
- (b) a third nav group holding both; two reference pages promoted to
  daily navigation.
- (c) delete History; loses the per-org, per-rule series the Fleet
  chart cannot show.
- other:

**OQ5. Findings sort.**

- (a) ✅ recommended: `sort=pr_age` on `GET /findings` with the cursor
  bound to the sort; the only correct "oldest PR first" across pages.
- (b) sort loaded pages client-side; wrong as soon as Load-more appends,
  and the chip would lie.
- (c) no sort; rely on `pr_stale=true`.
- other:

**OQ6. The history cap on Fleet and the rule sparklines.**

- (a) ✅ recommended: `aggregate=fleet|org` on `/compliance/history`,
  summed and floored in SQL; ninety points for ninety days, and a
  sparkline per rule is one small query.
- (b) raise `maxPages` and show the `truncated` notice; still quadratic
  in orgs × rules.
- (c) a dedicated `/compliance/history/fleet` path; the same SQL behind
  a second route and a second scope predicate to lint.
- other:

**OQ7. "+38 this week" on Tracked.**

- (a) ✅ recommended: do not draw it; Tracked shows repos and the parked
  count, and the compliance and failing deltas come from the snapshot
  series client-side.
- (b) add `discovered_7d` to `/summary` from `repository_events`; a
  small query, but a new number to keep honest across report, API and
  snapshot.
- other:

**OQ8. Finding age distribution.**

- (a) ✅ recommended: `Summary.finding_age[]`, six buckets over
  `status_since` for `non_compliant` findings, computed in SQL with the
  same scope predicate as every `api_*` query; the panel is hidden
  until it exists.
- (b) compute client-side from `/findings?status=non_compliant` pages;
  thousands of rows for one bar, and wrong until every page is loaded.
- (c) drop the panel; it is the clearest "are we getting better at
  fixing things" view either mockup has.
- other:

**OQ9. Drawer deep-link key.**

- (a) ✅ recommended: `?open=<repository_id>/<kind>/<name>` as a search
  param; one route owns both filters and detail, and a missing row is
  fetched through `/repositories/{id}`.
- (b) a child route `/findings/$repo/$kind/$name`; cleaner in the
  router, but the drawer owns a path while its parent owns filter
  state, and `retainSearchParams` has to carry both.
- (c) no deep link; "now it's a click" loses its shareable URL.
- other:

**OQ10. Status page additions.**

- (a) ✅ recommended: extend `Status` with `compliance.history[]` (30
  daily `{at, percent}`) and an `operations` object (`checked_24h`,
  `tracked`, `rollout_state`, `rollout_completed_at`,
  `last_snapshot_at`), all numeric, timestamp or enum, so the privacy
  guard passes unchanged; the sparkline and the operations strip are
  what make the page worth a bookmark.
- (b) ship the redesign on today's schema only (banner, components,
  percent and meter) and add fields later.
- (c) also add a changelog; no source in the API, and a hand-kept list
  on a status page goes stale.
- other:

**OQ11. How a repository is reached.**

- (a) ✅ recommended: the ⌘K palette over `GET /repositories?q=` plus
  rules and orgs from loaded lists; no standalone `/repos` page yet,
  and the mockup's "Repository (example)" placeholder is not a nav
  entry.
- (b) a `/repos` list page with search and park filters as well; a
  second surface to build and test, worth doing when the parked list
  needs a home (DESIGN-0027's replacement table promised one).
- (c) links only; a repository is reachable only from findings, orgs
  and rule detail.
- other:

**OQ12. Sequencing against IMPL-0029 task 8.7.**

- (a) ✅ recommended: land this first, with generic table, grid, drawer
  and chips, so Controls views are built on the new components; 8.7
  then edits column specs and nav data.
- (b) fold the restyle into 8.7; one larger change that restyles pages
  while replacing them.
- (c) wait for IMPL-0029; the rc keeps the scaffold look for however
  long the controls work takes.
- other:

**OQ13. What an org row expands to in the Orgs grid.**

- (a) ✅ recommended: the org's rules from `/orgs/{org}`, fetched on
  expand; the data exists and it answers "where in this org".
- (b) repositories, as the mockup draws; needs per-repository status
  counts the API does not aggregate, or one `/repositories/{id}` call
  per row.
- (c) both, as nested levels; the grid's second level becomes a tree
  and the heat map stops reading at a glance.
- other:

**OQ14. Orgs "PR activity" tab.**

- (a) ✅ recommended: add `open_prs` to `OrgSummary` (count of `pr_open`
  findings per org) and ship the tab with it; until then the tab is
  hidden.
- (b) derive it from `/findings?org=&remediation=pr_open` per org;
  N paged queries for one column.
- (c) drop the tab.
- other:

**OQ15. PR throughput card.**

- (a) ✅ recommended: defer; design the card, build it when an
  aggregate over `finding_events` exists (DESIGN-0027 promised the
  series under History), and keep the Fleet second row at two cards
  until then.
- (b) add a `/compliance/pr_throughput` endpoint now; a new aggregate
  query inside a styling change.
- (c) drop the card for good.
- other:

**OQ16. Visual regression tests.**

- (a) ✅ recommended: Playwright `toHaveScreenshot` for five pages,
  Chromium only, 0.2% threshold, snapshots committed; bundled fonts and
  seed data make them deterministic and the e2e job already runs
  Chromium.
- (b) axe only; catches accessibility, not a broken layout.
- (c) a hosted visual-diff service; an external dependency for one SPA.
- other:

**OQ17. Where the mockup lives, and what happens to its sources.**

- (a) ✅ recommended: `ui/design/repo-guardian-mockup.html` tracked in
  the repository, referenced from this doc by path and kept in step
  with it; the two source files (the sizzle reel and the platform
  mockup) are not committed, since every decision taken from them is
  written down here and drawn in the mockup, and the reel links Google
  Fonts, which the published site should not serve. `ui/design/` sits
  outside `ui/web/`, so Vite, ESLint and the generated-types gate never
  see it.
- (b) commit the sources beside it for provenance; two more files to
  explain, both already superseded.
- (c) `docs/design/assets/0036/`; discoverable from the doc, but
  published by mkdocs.
- other:

**OQ18. Time zone control.**

- (a) ✅ recommended: a `Local | UTC` toggle in the top bar persisted in
  `localStorage`, every timestamp through one helper, the other zone on
  hover; cheap and it ends "which zone is this" in incident threads.
- (b) browser zone only, no control.
- (c) a full zone picker as the mockup draws; a list of 400 zones for a
  question that has two useful answers.
- other:

## References

- `ui/design/repo-guardian-mockup.html`: the design artifact, every
  route of the redesigned UI over a fake of today's API (OQ17). Open it
  in a browser; the hash router carries `org` and `range`, `⌘K` opens
  the palette, and `?open=<repository_id>/<kind>/<name>` on Findings
  opens the drawer.
- [DESIGN-0027: v2 read-only API, business UI, and status page](0027-v2-read-only-api-business-ui-and-status-page.md),
  especially "The UI", "What the UI replaces" and the security list.
- [DESIGN-0022: Compliance posture state, dashboard suite, and OTEL-first observability](0022-compliance-posture-state-dashboard-suite-and-otel-first.md)
  for the posture taxonomy the Fleet page mirrors.
- [IMPL-0025](../impl/0025-v2-findings-model-temporal-control-plane-read-only-api-and-ui.md)
  Phases 20 to 22 (BFF, views, chart and e2e).
- [IMPL-0029](../impl/0029-controls-evaluation-policy-model-control-framework-and.md)
  task 8.7 and OQ9, and
  [INV-0021](../investigation/0021-controls-model-assumptions-a1-to-a23-against-the-v2-code.md)
  on the rule-shaped views.
- `api/openapi.yaml`: `Summary`, `RuleCompliance`, `RuleDetail`,
  `OrgSummary`, `OrgDetail`, `Finding`, `FindingPR`, `Event`,
  `HistoryPoint`, `Status`, `Me`.
- `ui/server/static.ts` (CSP), `ui/web/src/router.tsx` (shell and
  routes), `ui/web/src/index.css` (current tokens),
  `ui/web/src/lib/{evidence,format,viewmodel,pr}.ts`.
- `docs/usage/ui.md`.
