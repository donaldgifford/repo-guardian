---
id: IMPL-0031
title: "Frontend design refresh for the v2 UI"
status: Draft
author: Donald Gifford
created: 2026-10-16
---

<!-- markdownlint-disable-file MD024 MD025 MD041 -->

# IMPL-0031: Frontend design refresh for the v2 UI

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [How the phases are ordered](#how-the-phases-are-ordered)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Tokens, fonts, primitives and charts](#phase-1-tokens-fonts-primitives-and-charts)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Shell and page anatomy](#phase-2-shell-and-page-anatomy)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Fleet, Rules and rule detail](#phase-3-fleet-rules-and-rule-detail)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: Orgs, Findings with the drawer, Repository, History and Policy](#phase-4-orgs-findings-with-the-drawer-repository-history-and-policy)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: Status page](#phase-5-status-page)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: Accessibility, screenshots, docs and cleanup](#phase-6-accessibility-screenshots-docs-and-cleanup)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
  - [Phase 7: API additions](#phase-7-api-additions)
    - [Tasks](#tasks-6)
    - [Success Criteria](#success-criteria-6)
  - [Phase 8: rc image, homelab review](#phase-8-rc-image-homelab-review)
    - [Tasks](#tasks-7)
    - [Success Criteria](#success-criteria-7)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
  - [OQ1: One PR or two?](#oq1-one-pr-or-two)
  - [OQ2: Do the two findings-shaped API additions ship here?](#oq2-do-the-two-findings-shaped-api-additions-ship-here)
  - [OQ3: How are component behaviours tested under bun test?](#oq3-how-are-component-behaviours-tested-under-bun-test)
  - [OQ4: Icons](#oq4-icons)
  - [OQ5: Where screenshot snapshots are generated and stored](#oq5-where-screenshot-snapshots-are-generated-and-stored)
  - [OQ6: Fleet rule sparklines before the history aggregate exists](#oq6-fleet-rule-sparklines-before-the-history-aggregate-exists)
  - [OQ7: The ?series= pill selection and ?range= as URL state](#oq7-the-series-pill-selection-and-range-as-url-state)
  - [OQ8: Phase 2 e2e breakage window](#oq8-phase-2-e2e-breakage-window)
  - [OQ9: Sidebar collapse breakpoint](#oq9-sidebar-collapse-breakpoint)
- [References](#references)
<!--toc:end-->

## Objective

Rebuild the look and layout of the v2 business UI and the public
status page to DESIGN-0036: one dark theme declared once as Tailwind
tokens, bundled fonts under the unchanged CSP, a shell with an icon
sidebar, top bar, breadcrumbs and a shared page header, the Fleet,
Rules, Orgs, Findings and Repository pages re-laid out on the data the
read-only API serves today, a findings detail drawer with evidence and
a timeline, a designed status page, and a set of generic table, grid,
drawer, chip and chart components that IMPL-0029 task 8.7 builds the
Controls views on. The last code phase adds the five small, additive
API fields the design names, each un-hiding one panel.

The picture of the result is `ui/design/repo-guardian-mockup.html`.
Every task below that says "as the mockup draws it" means that file's
route; where the mockup and this plan disagree, DESIGN-0036 wins and
the mockup is fixed in the same change.

**Implements:** DESIGN-0036 (all sections; Open Questions taken at
their ✅ recommendations unless the Open Questions below say
otherwise), the UI consequences of DESIGN-0027 §The UI, and the
additive API changes listed in DESIGN-0036 §API / Interface Changes.

## Scope

### In Scope

- `ui/web/src/index.css` tokens, fontsource fonts, the Vite inline
  limit and a build guard over `dist/`.
- The primitive and chart set in `ui/web/src/components/ui/` and
  `components/charts/`, the badge tone table, `formatTime` with the
  Local/UTC toggle.
- The shell (`router.tsx` → `components/shell/`): top bar, sidebar
  from a nav array, breadcrumbs, page header, org scope popover,
  range tabs, command palette, avatar menu, collapsed sidebar under
  900px.
- Fleet, Rules, rule detail, Orgs grid, org detail, Findings with the
  drawer, Repository, History and Policy restyles, and the status page.
- The one BFF change (`UI_DOCS_URL` → `docs_url` in `/ui/config`) and
  the matching chart value `ui.docsUrl`.
- Unit, component, build-guard, Playwright, axe and screenshot tests;
  updates to `docs/usage/ui.md`, `ui/README.md` and CLAUDE.md.
- Five additive API changes, each with its sqlc query, handler,
  `apitest` case, regenerated server and client, and the UI panel it
  un-hides: `aggregate=` on `/compliance/history`, `sort=` on
  `/findings`, `Summary.finding_age[]`, `Status.compliance.history[]`
  plus `Status.operations`, and `OrgSummary.open_prs`.

### Out of Scope

- Any write path. The drawer's `actions` slot stays empty; no POST
  route is added to the BFF (IMPL-0029 8.7 owns re-evaluate).
- A light theme, a portal shell (app switcher, bell), the "compliance
  drivers" map, the PR throughput card (DESIGN-0036 OQ15, deferred), a
  standalone `/repos` list (OQ11).
- CSP, session, OIDC or proxy changes. The CSP string in
  `ui/server/static.ts` does not change.
- Database tables or migrations. Every API addition is a query over
  existing columns.
- Replacing `lib/evidence.ts` renderers (INV-0021 / IMPL-0029) or the
  Controls, Remediations and `/policies` views (IMPL-0029, IMPL-0030).
- Grafana dashboards and alerts (DESIGN-0022, generated).

## How the phases are ordered

Phases 1 to 7 are code, each ending with `make lint-ui`, `make test-ui`
and, from Phase 2 on, `make test-ui-e2e` green, with a commit per
numbered task. Phase 1 is tokens, fonts and the component and chart
set, so every page after it is composition. Phase 2 is the shell and
the page anatomy; it is the phase that moves Playwright selectors, so
the e2e suite is repaired here once. Phases 3 and 4 are the pages, in
the order the design's rollout plan gives: Fleet and Rules first
because they exercise every chart, then the three table-shaped pages
and the drawer, which produce the generic `DataTable`, `GroupGrid`,
`DetailDrawer` and `FilterChips` that IMPL-0029 reuses. Phase 5 is the
status page, isolated because it is public and privacy-guarded. Phase
6 is accessibility, screenshots, docs and the removal of what the
restyle made dead. Phase 7 is Go: the five API additions, each its own
task, each un-hiding a panel that Phases 3 to 5 built hidden. Phase 8
is human-run: the rc image, the homelab deploy and the review of the
pages against the mockup.

The plan lands as one PR into `v2` with the `dont-release` label (OQ1)
and must merge before IMPL-0029 task 8.7 starts (DESIGN-0036 OQ12).
Phase 8 is `deferred - human required`.

## Implementation Phases

### Phase 1: Tokens, fonts, primitives and charts

Everything later phases compose from. No page changes yet, so the
existing views keep rendering on the new tokens through the aliases in
1.1.

#### Tasks

- [x] 1.1 `ui/web/src/index.css`: replace the shadcn oklch palette with
  the DESIGN-0036 token table (`--color-background` `#17191d`,
  `--color-panel` `#1f2227`, `--color-panel-2` `#262a30`,
  `--color-border` `#31363d`, `--color-foreground` `#e8eaed`,
  `--color-muted-foreground` `#9aa1ab`, `--color-accent` `#5fa8f5`,
  `--color-ok` `#3fb950`, `--color-warn` `#d9a020`, `--color-bad`
  `#f2554d`, `--color-info` `#7aa7ff`, `--color-avatar` `#3a4c6d`,
  `--radius-lg: 3px`, `--font-sans`, `--font-mono`). Keep
  `--color-compliant`, `--color-non-compliant`,
  `--color-not-applicable` and `--color-unknown` as aliases of `ok`,
  `bad`, `muted-foreground` and `warn` so unconverted views still
  render; they are removed in 6.6. `color-scheme: dark` on `:root`;
  the 2px `accent` focus ring with 2px offset as a global
  `:focus-visible` rule; `prefers-reduced-motion` zeroes every
  transition.
- [x] 1.2 Fonts: add `@fontsource/ibm-plex-sans` and
  `@fontsource/jetbrains-mono` to `ui/package.json`; import the latin
  400/500/600 and 400/500 subsets from `ui/web/src/main.tsx`; set
  `font-display: swap` through the fontsource CSS; add
  `build.assetsInlineLimit: 0` to `ui/web/vite.config.ts`. Copy both
  SIL OFL licences into `ui/THIRD_PARTY_NOTICES.md` (create it if the
  repo has no UI notices file; otherwise append).
- [x] 1.3 Build guard `ui/scripts/check-dist.ts` (+ `.test.ts`): after
  `bun run build`, fail if any file under `ui/dist/` contains `data:`
  URLs in CSS/JS or the string `fonts.googleapis.com`; wire it as
  `bun run check:dist` and append it to the `lint-ui` make target
  after `check:api`. The test proves the guard fails on a fixture
  containing each pattern (memory: verify tests are non-vacuous).
- [x] 1.4 Add `radix-ui` (the single meta-package, DESIGN-0036 OQ3) to
  dependencies; confirm it tree-shakes under Vite by checking the
  built chunk size before and after (record both numbers in the PR).
  Recorded: `index-*.js` 753,718 bytes and `index-*.css` 18,567 bytes
  both before and after adding `radix-ui@1.8.1` with nothing imported
  yet, so the meta-package adds nothing until a primitive uses it.
- [x] 1.5 Tone table and badges: `lib/format.ts` gains `toneFor(field,
  value)` over the DESIGN-0036 badge table (status, remediation,
  reason-under-status, component state, rule kind → outline) with
  `mute` for any unknown value; `components/ui/badge.tsx` becomes the
  reel's recipe (mono 12px, `5px 7px`, no radius, 18% tint, 75%
  toward white) with tones `ok | warn | bad | info | mute | outline`;
  `StatusBadge`, `RemediationBadge` (renders "—" for `none` in table
  context), `ReasonBadge`, `KindBadge`, `StateBadge` in
  `components/badges.tsx`. Table-driven test over every documented
  enum value plus one unknown per field.
- [x] 1.6 Time: `lib/time.ts` with `formatTime(iso, {zone, style})`
  (`YYYY-MM-DD HH:mm`, date-only, relative via the existing
  `relativeTime`), a `TimeZoneProvider` holding `local | utc` in
  React state persisted under `localStorage["rg.tz"]`, a `<Time>`
  element that renders the chosen zone and puts the other in `title`.
  Tests for both zones, a `Z`-less input, and a missing
  `localStorage`.
- [x] 1.7 Layout primitives in `components/ui/`: `Card` (header title
  13px/600 + 12px muted subtitle + right slot, optional `StatStrip`
  under the header, body), `StatStrip` (hairline-separated
  label/value/subline cells; values proportional, tables tabular),
  `Kpi` (label, value 22px/600 tabular, subline, optional delta with
  arrow and direction tone; replaces `Tile`), `Pill` (colour dot,
  label, value, `role="radio"` + `aria-checked` inside a
  `role="radiogroup"`), `Tabs` (segmented; value mirrored to a search
  param through `patchSearch`), `Chip` (`key = value` mono, click
  opens an editor), `Meter` (track `panel-2`, fill toned by threshold
  `bad < 70 ≤ warn < 90 ≤ ok`, percent printed), `Avatar` (20px
  square, initials rule from DESIGN-0036 §Shell), `Button` restyled
  (outline, ghost, sm).
- [ ] 1.8 Dialog-family primitives on radix in `components/ui/`:
  `Popover`, `DropdownMenu`, `Dialog` and `Drawer` (side right, 470px,
  scrim, `prefers-reduced-motion` aware, focus trap, focus return,
  Escape), `Command` (palette input + grouped results + arrow/Enter
  navigation). Component tests (OQ3) for: Drawer traps focus, Escape
  closes and focus returns to the opener; Pill toggles `aria-checked`;
  Tabs write the search param; Chip opens its popover.
- [ ] 1.9 Charts in `components/charts/`, Recharts for the two SVG
  charts and plain elements for the rest, every colour from a theme
  variable, axes in the mono face at 11px, hairline grid in `border`:
  - `TrendChart`: area, one series, 2px line, 10% fill, 4.5px end dot
    ringed in `panel`, end-of-line value label, `nice` floor/ceiling
    (percent axes never from zero when the band is narrow, never above
    100), x ticks fitted to the range, crosshair + tooltip, full-column
    hit area.
  - `BarBuckets`: single-series bars ≤24px wide, 4px rounded data end,
    value cap labels, per-bar tooltip.
  - `StackedBars`: per-category horizontal stacks from `div` segments
    10px tall with 2px `background` gaps, row total, one legend.
  - `BarList`: ranked rows (label mono, count, share, single-colour bar
    scaled to the largest), sorted descending, max eight rows + "other".
  - `Sparkline`: 20-point line, no axes, end dot.
  - `DistBar`: six-segment distribution bar with legend (built hidden;
    first used in 7.3).
  Each chart takes an `aria-label` and renders through `ChartCard`,
  which adds the `table` toggle swapping the plot for a dense table of
  the same data. Tests: `nice` bounds (narrow band, full range, single
  point, empty), tick fitting for 1d/7d/30d/90d, `BarList` folding at
  nine rows, `StackedBars` percent-of-row math.
- [ ] 1.10 `lib/viewmodel.ts` additions, each table-tested: `deltaFrom(
  series)` (one point → null, two points, gap at the range edge),
  `cadenceWord(series)` (median gap → `daily` / `every Nd` / `—`),
  `fleetSeries(points)` (sum per `snapshot_at`, percent floored to one
  decimal exactly like `compliantPercent`), `heatOpacity(count,
  total)`, `initials(name, subject)`, `orgCountLabel(me, orgs)`,
  `failingRuleCount(rules)`.

#### Success Criteria

- `make lint-ui` (now including `check:dist`) and `make test-ui` pass;
  the existing e2e suite still passes on the aliased tokens
  (`make test-ui-e2e`).
- `bun run build` emits woff2 files under `ui/dist/assets/` and
  `check:dist` passes; neutralising `assetsInlineLimit` makes it fail.
- Every component and chart listed in DESIGN-0036 §Component inventory
  exists with a test, and `grep -r "oklch" ui/web/src` returns nothing.

### Phase 2: Shell and page anatomy

The frame every authenticated page sits in, and the single Playwright
selector migration.

#### Tasks

- [ ] 2.1 `components/shell/nav.ts`: the nav array (`group`, `label`,
  `to`, `icon`, optional `count` selector) with `BUSINESS` (Fleet,
  Rules, Orgs, Findings ← `summary.findings.non_compliant`), `PUBLIC`
  (Status page) and `DOCS` (Documentation, present only when
  `uiConfig.docs_url` is set). Icons are an inline SVG set in
  `components/icons.tsx` (OQ4).
- [ ] 2.2 `components/shell/Sidebar.tsx`: 220px, group labels 10.5px
  uppercase tracked, current item `panel-2` + `accent` left rule,
  right-aligned mono counts, footer line (`policy <prefix> · <rollout
  state>` from `/policy`, 11px mono, links to `/policy`). Below 900px
  it collapses behind a top-bar button and opens as a `Dialog` side
  left.
- [ ] 2.3 `components/shell/TopBar.tsx`: brand (shield in `accent`,
  "Repo Guardian" 600, `compliance · read-only` muted) linking to `/`;
  a reserved empty slot left of the brand; right: Search button
  labelled `⌘K`, `TimeZoneToggle`, `Avatar` opening a `DropdownMenu`
  with name, subject (mono), Documentation when configured, and Sign
  out as the existing POST form to `/auth/logout`.
- [ ] 2.4 `components/shell/CommandPalette.tsx`: `⌘K` / `Ctrl-K` and
  the Search button open it; input debounced 150ms; repositories from
  `GET /repositories?q=&org=` (new `useRepositorySearch` hook, limit
  20), rules and orgs filtered from the cached `/rules` and `/orgs`
  queries; groups Repositories / Rules / Orgs; Enter navigates to
  `/repos/$id`, `/rules/$kind/$name` or `/orgs/$org`, carrying `?org=`.
- [ ] 2.5 Page anatomy in `components/page/`: `Breadcrumbs` (12px,
  derived from the matched route plus the Findings `?open=` state),
  `PageHeader` (title 20px/600, optional outline badge, muted subtitle
  with `<code>` for endpoint names, right-aligned controls slot),
  `OrgScope` (popover listing `All orgs (N)` with the DESIGN-0036 N
  rule and each visible org with its tracked count; writes `?org=`
  through `patchSearch`), `RangeTabs` (`1d | 7d | 30d | 90d` → `?range=`,
  default `90d`; `lib/range.ts` maps a range to a `from` timestamp).
  `appRoute` gains `range` in `retainSearchParams`.
- [ ] 2.6 `router.tsx`: `AppLayout` becomes `Shell` (top bar, sidebar,
  scrolling main); remove the header links, the native `OrgFilter`
  select and the inline sign-out; keep `parseOrgSearch`,
  `retainSearchParams`, `AccessView` and `ProblemView` behaviour
  unchanged.
- [ ] 2.7 BFF: `UI_DOCS_URL` (optional, must parse as an `https:` URL
  when set) in `ui/server/config.ts`, surfaced as `docs_url` in
  `publicConfig`; `config.test.ts` covers unset, valid and invalid.
  Chart: `ui.docsUrl` value → `UI_DOCS_URL` env in
  `charts/repo-guardian/templates/ui.yaml`, `values.schema.json`
  `format: uri`, a case in `charts/repo-guardian/tests/ui_test.yaml`,
  `make helm-docs`.
- [ ] 2.8 Playwright: update `ui/e2e/ui.spec.ts` for the moved
  selectors (Sign out inside the avatar menu; org scope through the
  popover instead of the select; breadcrumb row present on every
  view); add specs: org scope popover writes `?org=` and the scoping
  test still passes; `⌘K` finds the seeded repository and navigates;
  the sidebar Findings count equals `/summary`; `UI_DOCS_URL` unset
  hides the Docs group.

#### Success Criteria

- Every authenticated route renders inside the shell with breadcrumbs
  and a page header; `/status` renders outside it.
- `make test-ui-e2e` passes including the four new specs; the scoping
  and sign-out tests pass unchanged in intent.
- `helm unittest` passes with the `ui.docsUrl` case, and rendering
  without it produces no `UI_DOCS_URL` env.

### Phase 3: Fleet, Rules and rule detail

The pages that exercise every chart, on today's API. Panels that need
a Phase 7 field are built behind a `hidden-until` flag that Phase 7
flips; a hidden panel renders nothing, never a placeholder.

#### Tasks

- [ ] 3.1 `views/fleet.tsx` compliance card: `Card` with `StatStrip`
  (Tracked, Compliant + `deltaFrom`, Failing + `failingRuleCount` +
  7d delta, Parked sum + reasons, Snapshots count + `cadenceWord`),
  four `Pill`s as a radio group (compliant %, failing, not applicable,
  unknown) selecting the one series `TrendChart` draws, `?series=`
  in the URL, "full history" link to `/history`, the `truncated`
  notice when `useHistory` hits its cap. Series come from
  `fleetSeries()` over `/compliance/history` for the `?range=`
  window until 7.1 switches it to `aggregate=fleet`. A null percent
  renders "unmeasured".
- [ ] 3.2 Fleet second column and row: Open PRs by age (`BarBuckets`
  over `summary.open_prs`, subtitle with `summary.stale_after`
  humanised, `<stale_prs> PRs over threshold →
  /findings?pr_stale=true`), Worst rules (`worstRules(rules, 5)` with
  `Meter` and trend), Parked by reason (`BarList` over
  `summary.parked[]`, static rows), Findings by status (`StackedBars`
  single row over summed `/rules` counts with the "percent counts only
  the first two" subtitle). Finding age distribution (`DistBar`) is
  built hidden until 7.3.
- [ ] 3.3 Fleet third row: Compliance by rule (`DataTable` from 4.1 is
  not yet available, so use the styled `Table`; Rule, Kind badge,
  Compliant, Failing, N/A, %, Trend sparkline hidden until 7.1 (OQ6);
  rows link to the rule) and Orgs (Org, Tracked, Failing, Parked,
  Compliance meter; rows link to the org). With `?org=` set, read
  `/orgs/{org}` and replace the Orgs table with Not applicable by
  reason (`BarList`).
- [ ] 3.4 `views/rules.tsx` list: kind `Tabs` (`All` plus the kinds
  present, `?kind=`), `Kpi` row of the four worst rules, `StackedBars`
  per rule with legend, full table (Rule, Kind, Compliant, Failing,
  N/A, Unknown, Compliance, Trend in points, Previous snapshot date).
- [ ] 3.5 Rule detail (`/rules/$kind/$name`): `PageHeader` with the
  kind badge, the policy `description` subtitle and "Findings for this
  rule" → `/findings?kind=&rule=`; `Kpi`s (Compliance + trend,
  Compliant, Failing + `oldest: N days`, Not applicable); two-thirds
  per-org table with `Meter`s; one-third Top reasons `BarList` and
  Oldest failures list linking to `/repos/$id`.
- [ ] 3.6 Tests: viewmodel cases from 3.1–3.5 (7d delta window, stale
  threshold humanising, rule kind tab set from data); Playwright:
  range tabs change the history query's `from`, the series pills
  switch the chart's `aria-label`, the worst-rule link lands on the
  rule page, the rule page's "Findings for this rule" carries kind and
  rule.

#### Success Criteria

- Fleet, Rules and rule detail match the mockup's `#/`, `#/rules` and
  `#/rules/file/renovate` routes panel for panel against the e2e seed,
  with the two hidden panels absent.
- No request fan-out regression: Fleet issues at most one request per
  endpoint it names plus the history pages (`useHistory`), verified by
  a Playwright request count assertion.
- `make lint-ui`, `make test-ui`, `make test-ui-e2e` pass.

### Phase 4: Orgs, Findings with the drawer, Repository, History and Policy

The table-shaped pages, which produce the generic components the
controls rework reuses.

#### Tasks

- [ ] 4.1 `components/ui/data-table.tsx`: `DataTable<T>` from a column
  spec (`key`, header, cell render, `align`, `numeric`, `hideBelow`),
  a row key, optional `selectedKey`, `onActivate` (click, Enter,
  Space), 13px cells at `9px 10px`, hover `panel-2`, selected `accent`
  at 12%, keyboard focusable rows. Component test for Enter activation
  and the selected tint class.
- [ ] 4.2 `components/ui/group-grid.tsx`: `GroupGrid` with parent rows,
  lazily loaded child rows (render prop + loader), heat-shaded numeric
  cells (`heatOpacity`, text stays `foreground`, count and percent
  both printed), totals row, expand/collapse all, `?open=` list of
  expanded keys. Tests: expand/collapse, totals arithmetic, heat
  opacity bounds.
- [ ] 4.3 `views/orgs.tsx`: totals badge line, heat `Tabs`
  (`Compliance | Parked`; `PR activity` hidden until 7.5), toolbar
  (org scope, `All | Hide parked`, Expand all / Collapse all, Export
  CSV through `lib/csv.ts`), `GroupGrid` over `/orgs` whose expansion
  fetches `/orgs/{org}` and lists rules as child rows linking to
  `/rules/$kind/$name?org=`.
- [ ] 4.4 Org detail (`/orgs/$org`): header with `org` badge and
  "Findings in this org"; `Kpi`s; two-thirds `TrendChart` over the
  org's history (`fleetSeries` over `/compliance/history?org=`) and
  the rules table with `Meter`s; one-third Not applicable by reason
  and Parked by reason `BarList`s.
- [ ] 4.5 `components/filters/FilterChips.tsx`: chips for `status`,
  `reason`, `remediation`, `kind`, `rule`, `org`, `pr_stale` plus a
  `+ filter` chip; each opens a `Popover` of options (enums from the
  spec's documented values, rules and orgs from cached lists); values
  round-trip through `patchSearch` so every existing `/findings?...`
  deep link keeps working. Test: search → chips → search is the
  identity for every documented param.
- [ ] 4.6 `views/findings.tsx` table: `DataTable` with Repository
  (`org/name` mono, org as muted second line), Rule, Status badge,
  Reason, Remediation badge, PR (`prLink`), Age (`ageDays` of
  `pr.created_at` + `stale` badge when `pr_stale`), Since (`<Time>`
  relative, exact on hover); result count `<loaded> of
  <summary.findings.non_compliant>` when the status chip is
  `non_compliant`; Load-more and `useFindings` unchanged; Export CSV in
  the page header. Age and Since carry `hideBelow` so they hide when
  the drawer is open side-by-side.
- [ ] 4.7 `components/ui/detail-drawer.tsx` and the finding drawer body
  (`views/findings/drawer.tsx`): `?open=<repository_id>/<kind>/<name>`
  owns the state; side-by-side at ≥1600px, overlay with scrim below;
  a row not in the loaded pages is fetched through
  `/repositories/{id}` and matched by `(kind, name)`; title as
  `repoLink`, mono subtitle, key/value block (status, reason,
  remediation + `#n` + `pr.head` + `matched_term`), evidence inset
  around `describeEvidence`, `Timeline`, footer with the empty
  `actions` slot and "Open repository". Breadcrumbs add
  `org/repository` while open.
- [ ] 4.8 `components/timeline.tsx` + `lib/timeline.ts`: merge
  `/repositories/{id}/events` (finding events for the rule; repository
  `parked | unparked | renamed | transferred`) and
  `/repositories/{id}/checks` since `status_since` into one list,
  newest last, latest node in `accent`, checks rendered `re-checked ·
  <outcome>` with `PR #n still open (<age>)` after an open PR's
  `created_at`, capped at 20 with a "full timeline" link. Table tests:
  ordering, the still-open synthesis, the cap, a check before
  `status_since` excluded.
- [ ] 4.9 `views/repository.tsx`: header `org/name` + `active|parked`
  badge, subtitle `checked <relative> · installation <id>`, "Open on
  GitHub"; two-thirds Findings `DataTable` (Rule, Status, Reason /
  evidence, Remediation) and Recent checks (Time, Trigger, Outcome,
  Duration, Error); one-third Repository facts (default branch, park
  state and reason, catalog parse state, policy version) and the
  shared `Timeline` over all events.
- [ ] 4.10 `views/history.tsx` (small multiples: one `TrendChart` per
  org, rule `<select>` → `Tabs`-styled select, `?range=`) and
  `views/policy.tsx` (`Kpi`s Version / First seen / Rollout / Rules
  and the rules table) restyled on the primitives; no behaviour change.
- [ ] 4.11 Playwright: drawer opens from a row and from a pasted
  `?open=`; Escape returns focus to the row; the scrim closes it below
  1600px (`viewport` 1400) and the Age column hides above it
  (`viewport` 1700); the HTML-in-evidence test re-pointed at the
  drawer's evidence inset; Orgs grid expands `acme` to its rules and
  the totals row sums the seed; `Hide parked` removes the parked seed
  repository's org count; CSV export still downloads the filtered set.

#### Success Criteria

- Orgs, org detail, Findings (with and without the drawer), Repository,
  History and Policy match the mockup's routes against the seed.
- `DataTable`, `GroupGrid`, `DetailDrawer`, `FilterChips` and
  `Timeline` import nothing from `views/` and nothing rule-specific;
  a `grep` for `rule_kind|findings` under `components/ui/` returns
  nothing.
- Every pre-existing `/findings?...` deep link in `ui.spec.ts` still
  resolves to the same filtered set.
- `make lint-ui`, `make test-ui`, `make test-ui-e2e` pass.

### Phase 5: Status page

Public, outside the shell, aggregate-only, on today's `Status` schema.

#### Tasks

- [ ] 5.1 `views/status.tsx` rebuilt as DESIGN-0036 §Status page:
  centred 760px column; header row with the shield mark and `updated
  <relative> · refreshes every 30s`; state banner whose border takes
  the tone, pulsing dot (static under reduced motion), headline mapped
  from `state` (`operational` → "All systems operational", `degraded`
  → "Degraded performance", anything else → "Service disruption",
  `unknown` → "Status not yet available" in muted tone), the
  aggregate-only subline and the `StateBadge`.
- [ ] 5.2 Components card (tone dot, name mono, `detail`, badge; the
  `webhooks` informational footnote kept) and Fleet compliance card
  (percent at 40px or "unmeasured", 10px `Meter` in `ok`, footnote
  `measured: <bool> · compliant ÷ (compliant + non_compliant)`); the
  30-day sparkline and the operations strip are built hidden until
  7.4. Footer `GET /api/v1/status · served from an in-process cache`
  in muted mono, plus a documentation link when `docs_url` is set and
  a sign-in link to `/auth/login`.
- [ ] 5.3 Playwright: the public status page renders without a session;
  its text contains none of the seed's org or repository names
  (`acme`, `globex`, every `repositories.name` row); the banner
  headline follows the seeded state.

#### Success Criteria

- `/status` matches the mockup's `#/status` route on the seed and
  renders every string from the schema or the fixed headline map only.
- `TestStatus_PrivacyGuard` and the new Playwright name check pass.

### Phase 6: Accessibility, screenshots, docs and cleanup

#### Tasks

- [ ] 6.1 `@axe-core/playwright` as a dev dependency; an `a11y.spec.ts`
  running axe on Fleet, Orgs, Findings with the drawer open, Repository
  and Status with zero violations at `wcag2aa`; contrast of every
  badge tone verified by the run, not by hand.
- [ ] 6.2 Screenshot tests `visual.spec.ts` (OQ5): `toHaveScreenshot`
  for the same five pages at 1600×1000, Chromium, `maxDiffPixelRatio:
  0.002`, `page.clock.setFixedTime` so relative times are stable;
  snapshots committed under `ui/e2e/__screenshots__/` for linux only,
  the spec skipped on other platforms, regenerated through the
  `mcr.microsoft.com/playwright` image via a new `make
  test-ui-e2e-update-snapshots` target.
- [ ] 6.3 Keyboard pass: every interactive element reachable by Tab in
  reading order on the five pages (a Playwright spec that tabs through
  Fleet and asserts the focus order of the header controls, pills,
  rows and sidebar).
- [ ] 6.4 `docs/usage/ui.md`: rewrite the walkthrough for the new pages
  (shell, palette, time zone, drawer deep links, status page) with
  screenshots captured from the e2e stack and stored under
  `docs/usage/img/ui/`; `ui/README.md`: fonts, tokens, `check:dist`,
  `UI_DOCS_URL`, the mockup's role; `docs/operations/v2-overview.md`
  UI paragraph; CLAUDE.md UI bullet (gotchas found during this plan).
- [ ] 6.5 Keep the mockup in step: any panel, label or breakpoint that
  changed during Phases 2 to 5 is applied to
  `ui/design/repo-guardian-mockup.html`; DESIGN-0036 moves to
  `Accepted` with a note naming this IMPL.
- [ ] 6.6 Cleanup: remove the four legacy colour aliases from
  `index.css` and `statusTone` from `lib/format.ts`; shrink
  `components/common.tsx` to `ExternalLink`, `Evidence`, `Loading`,
  `Empty`, `Section`; delete `Tile` and the old `TrendChart`; delete
  dead CSS; `bun run lint` with `--max-warnings=0` stays green and
  `eslint-ban.test.ts` still fires.

#### Success Criteria

- axe reports zero violations on the five pages; the screenshot spec
  passes in CI and is skipped locally on macOS with a clear message.
- `docs/usage/ui.md` describes every nav entry and control that exists
  and none that does not; `mkdocs build --strict` passes.
- `grep -rn "color-compliant\|Tile\b" ui/web/src` returns nothing.

### Phase 7: API additions

Five independent, additive changes. Each task is the full vertical
slice: `api/openapi.yaml` → `make generate-api` → sqlc query →
store → handler → `apitest` case → `make generate-ui-api` → the UI
panel it un-hides → `make lint-api`, `make lint-sql`, `make lint-ui`.
New status fields are numbers, timestamps and enums only so
`TestStatus_PrivacyGuard` passes unchanged. Every new `api_*` query
carries the scope predicate (`TestAPIQueries_AreScoped`).

#### Tasks

- [ ] 7.1 `GET /compliance/history?aggregate=none|fleet|org` (default
  `none`): new queries in `queries/api_history.sql` summing
  `compliant`, `non_compliant`, `not_applicable`, `unknown` per
  `snapshot_at` (and per `org` for `org`) with the percent floored in
  SQL exactly as `ComplianceByRule`; `HistoryPoint` gains optional
  `org` and drops `rule_kind`/`rule_name` when aggregated (document in
  `description`); `apitest` cases for each mode and for scope; a store
  test proving `aggregate=fleet` equals `fleetSeries()` over `none`
  on the same fixture. UI: Fleet and org detail switch to
  `aggregate=fleet|org`, the `truncated` notice path stays for `none`;
  the rule sparklines un-hide using one `aggregate=fleet&kind&rule`
  query per rule (OQ6).
- [ ] 7.2 `GET /findings?sort=default|pr_age`: `pr_age` orders by
  `evidence->'pr'->>'created_at'` ascending with nulls last, then the
  default key; the cursor (`cursor.go`) binds the sort into the filter
  hash so a cursor from one sort is rejected under another; `apitest`
  covers paging under `pr_age` across a page boundary. UI: the `sort =
  pr_age` chip (default off), persisted as `?sort=`.
- [ ] 7.3 `Summary.finding_age[]` `{bucket, count}` over `non_compliant`
  findings' `status_since` in six buckets (`<1d`, `1-7d`, `7-30d`,
  `30-90d`, `90-180d`, `180d+`), computed in `api_summary.sql` with
  the scope predicate; `apitest` with a fixture spanning every bucket.
  UI: the Fleet Finding age distribution `DistBar` un-hides with the
  `<p>% under 30d · healthy threshold` pill.
- [ ] 7.4 `Status.compliance.history[]` (30 daily `{at, percent}` from
  `compliance_snapshots`, fleet-summed, percent floored) and
  `Status.operations{checked_24h, tracked, rollout_state,
  rollout_completed_at, last_snapshot_at}` from `checks`,
  `repositories`, `policy_versions` and `compliance_snapshots` in
  `queries/status.sql` and `APIReader.StatusInputs` (one read-only
  tx); `StatusPage` carries them through its atomic pointer;
  `TestStatus_PrivacyGuard` must pass without an allowlist change
  (`rollout_state` is an enum in the schema). UI: the status sparkline
  and operations strip un-hide.
- [ ] 7.5 `OrgSummary.open_prs` (count of findings with `remediation =
  pr_open` per org) in `api_rules.sql`/`api_orgs` queries behind
  `/orgs`; `apitest` parity with `/findings?org=&remediation=pr_open`
  totals on the fixture. UI: the Orgs `PR activity` heat tab un-hides.
- [ ] 7.6 `ui/e2e/seed.sql`: rows that exercise each addition (a PR
  with an old `created_at`, findings across every age bucket, 30 days
  of snapshots for two orgs, a completed rollout); Playwright specs
  asserting each panel is now present and reads the seeded numbers.
- [ ] 7.7 `docs/usage/api.md`: the five additions; `api/openapi.yaml`
  `info.version` bumped per the file's existing convention.

#### Success Criteria

- `make lint-api`, `make lint-sql`, `make generate-api` /
  `make generate-ui-api` produce no diff after commit, `make test`
  passes including the new `apitest` and store cases, and
  `TestStatus_PrivacyGuard` is unchanged.
- No panel in the UI is hidden any more; `grep -rn "hiddenUntil" ui/web/src`
  returns nothing.
- `TestCompliance_ParityAcrossReportAPIAndSnapshot` still passes and
  `aggregate=fleet` agrees with the report's fleet line on the
  integration fixture.

### Phase 8: rc image, homelab review

`deferred - human required`.

#### Tasks

- [ ] 8.1 Merge the PR into `v2`, cut `v2.0.0-rc.N` by hand and
  dispatch `ghcr.yml` with the tag so `repo-guardian-ui` is published
  (CLAUDE.md §The v2 branch). — *deferred: human required*
- [ ] 8.2 Deploy the rc to the homelab dev install with `ui.docsUrl`
  set; walk every nav entry, the palette, the drawer deep link, the
  time zone toggle and the status page against the mockup; file any
  divergence as a follow-up task on this doc. — *deferred: human
  required*
- [ ] 8.3 Confirm the Keycloak group split still shows different orgs
  in the org scope popover and the Orgs grid. — *deferred: human
  required*

#### Success Criteria

- The rc UI image runs in the homelab with no console errors and no
  CSP report; the five screenshot pages match the committed snapshots
  within tolerance.

## File Changes

| File | Action | Description |
| ---- | ------ | ----------- |
| `ui/web/src/index.css` | Modify | DESIGN-0036 tokens, focus ring, reduced motion; aliases removed in 6.6 |
| `ui/web/src/main.tsx`, `ui/web/vite.config.ts` | Modify | fontsource imports; `assetsInlineLimit: 0` |
| `ui/scripts/check-dist.ts` (+ test) | Create | build guard over `dist/` |
| `ui/package.json`, `ui/bun.lock` | Modify | `radix-ui`, two fontsource packages, `@axe-core/playwright` |
| `ui/web/src/components/ui/*` | Create/Modify | badge, button, card, stat-strip, kpi, pill, tabs, chip, meter, avatar, popover, dropdown-menu, dialog, drawer, command, data-table, group-grid, detail-drawer |
| `ui/web/src/components/charts/*` | Create | TrendChart, BarBuckets, StackedBars, BarList, Sparkline, DistBar, ChartCard |
| `ui/web/src/components/{badges,icons,timeline}.tsx` | Create | tone-table badges, inline icon set, shared timeline |
| `ui/web/src/components/shell/*`, `components/page/*`, `components/filters/*` | Create | Shell, Sidebar, TopBar, CommandPalette, nav array; Breadcrumbs, PageHeader, OrgScope, RangeTabs; FilterChips |
| `ui/web/src/components/common.tsx` | Modify | shrinks to ExternalLink, Evidence, Loading, Empty, Section |
| `ui/web/src/lib/{format,viewmodel,time,range,timeline}.ts` (+ tests) | Create/Modify | tone table, deltas, cadence, fleet series, heat, initials, time zone, range, timeline merge |
| `ui/web/src/router.tsx` | Modify | Shell replaces AppLayout; `range` retained |
| `ui/web/src/views/*.tsx`, `views/findings/drawer.tsx` | Modify/Create | every page re-laid out |
| `ui/web/src/api/queries.ts` | Modify | `useRepositorySearch`; `aggregate`, `sort` params |
| `ui/server/config.ts` (+ test) | Modify | `UI_DOCS_URL` → `docs_url` |
| `ui/e2e/{ui,a11y,visual}.spec.ts`, `ui/e2e/seed.sql`, `ui/e2e/__screenshots__/` | Modify/Create | selector migration, new specs, screenshots |
| `Makefile` | Modify | `lint-ui` runs `check:dist`; `test-ui-e2e-update-snapshots` |
| `charts/repo-guardian/{templates/ui.yaml,values.yaml,values.schema.json,tests/ui_test.yaml}` | Modify | `ui.docsUrl` |
| `api/openapi.yaml`, `internal/api/gen/*`, `ui/web/src/api/schema.gen.ts` | Modify | the five additions, regenerated both sides |
| `internal/store/postgres/queries/{api_history,api_findings,api_summary,api_rules,status}.sql`, `sqlcdb/*` | Modify | aggregate, sort, finding age, open PRs, status fields |
| `internal/api/{handlers,convert,cursor,status,resources}.go` (+ tests) | Modify | wiring and `apitest` cases |
| `docs/usage/{ui,api}.md`, `docs/usage/img/ui/*`, `ui/README.md`, `docs/operations/v2-overview.md`, `CLAUDE.md` | Modify/Create | documentation |
| `ui/design/repo-guardian-mockup.html` | Modify | kept in step (6.5) |

## Testing Plan

- [ ] Unit (`bun test ./web`): tone table over every documented enum
  value plus one unknown; `formatTime` Local/UTC; `deltaFrom`,
  `cadenceWord`, `fleetSeries` (equal to `compliantPercent` flooring),
  `heatOpacity`, `initials`, `orgCountLabel`, `nice` bounds, tick
  fitting, `BarList` folding, timeline merge (ordering, still-open
  synthesis, cap, pre-`status_since` exclusion), `FilterChips`
  round-trip identity.
- [ ] Component (`bun test`, OQ3): Drawer focus trap / Escape / return;
  Pill `aria-checked`; Tabs → search param; `DataTable` Enter
  activation; `GroupGrid` expand/collapse and totals; `Kpi` delta tone.
- [ ] Build guard: `check:dist` fails on a `data:` URL and on
  `fonts.googleapis.com`; passes on the real build.
- [ ] Playwright (`make test-ui-e2e`): the migrated selectors; org
  scope popover; `⌘K`; sidebar count; docs group gating; range tabs;
  series pills; drawer from row and from `?open=`; scrim and column
  hiding at two viewports; HTML-in-evidence as text in the drawer;
  Orgs expand and totals; Hide parked; CSV export; status page name
  check; Fleet request-count ceiling; Phase 7 panels present with
  seeded numbers; axe on five pages; screenshots on five pages.
- [ ] Go: `apitest` for every addition (modes, scope, paging under
  `pr_age`, every age bucket, status fields), store parity tests
  (`aggregate=fleet` = client aggregation; `open_prs` = findings
  count), `TestAPIQueries_AreScoped`, `TestStatus_PrivacyGuard`
  unchanged, `TestCompliance_ParityAcrossReportAPIAndSnapshot`.
- [ ] Chart: helm-unittest `ui.docsUrl` present/absent.
- [ ] Security regressions: `eslint-ban.test.ts` unchanged and green;
  CSP string unchanged (`static.test.ts` asserts the literal).

## Dependencies

- DESIGN-0036 (Draft → Accepted in 6.5) and its mockup.
- Must merge before IMPL-0029 task 8.7 (DESIGN-0036 OQ12); 8.7 then
  edits `nav.ts`, column specs and the drawer body rather than chrome.
- Bun pinned by `mise.toml` and `ui/Dockerfile` (`oven/bun:1.3.14`);
  Playwright `^1.63` already present; Chromium installed by the
  existing CI job.
- The e2e seed already carries 30 days of `compliance_snapshots` for
  two orgs and three rules; Phase 7 extends it.

## Open Questions

### OQ1: One PR or two?

**Resolved 2026-10-16: (a).**

- (a) ✅ recommended: **one PR for the whole plan** into `v2` with
  `dont-release`, Phases 1 to 7 included, Phase 8 `deferred - human
  required`; the API additions are small and the UI panels that depend
  on them are built hidden, so the PR is consistent at every commit.
- (b) Two PRs: Phases 1 to 6 (UI only, `ui/**` paths filter) then
  Phase 7 (Go + UI un-hiding). Smaller reviews; the first PR ships five
  hidden panels and the second waits on a merge.
- other:

### OQ2: Do the two findings-shaped API additions ship here?

**Resolved 2026-10-16: (a).**

`sort=pr_age` on `/findings` (7.2) and `Summary.finding_age[]` (7.3)
extend endpoints IMPL-0029 replaces with controls views and the
evaluations endpoint.

- (a) ✅ recommended: **ship both here**; the SQL is a dozen lines over
  `findings`, the controls line needs the same age histogram and
  oldest-PR ordering over its results table, and 8.7 ports the two
  queries alongside the views it replaces. Note the port in IMPL-0029
  8.7's task text when this merges.
- (b) Defer both to IMPL-0029 and leave the age panel and sort chip
  hidden until then.
- (c) Ship `finding_age` (a summary number that survives any model) and
  defer `sort`.
- other:

### OQ3: How are component behaviours tested under `bun test`?

**Resolved 2026-10-16: (a).**

Today's only component test renders with `renderToStaticMarkup`, which
cannot exercise focus, keyboard or state.

- (a) ✅ recommended: **`@happy-dom/global-registrator` +
  `@testing-library/react`** registered through a `bunfig.toml` preload
  for the handful of behaviour tests (Drawer, Pill, Tabs, DataTable,
  GroupGrid); render-to-string stays for everything static. Two dev
  dependencies, no second runner.
- (b) Push every interaction test to Playwright; the e2e job is slower
  and needs Docker, so the feedback loop for a focus-trap bug is
  minutes, not seconds.
- (c) Add vitest + jsdom beside `bun test`; a second runner and config
  for the same files.
- other:

### OQ4: Icons

**Resolved 2026-10-16: (a).**

The design needs eight icons (grid, list, boxes, activity, check,
book, shield, search, external link, clock). No icon library is
installed.

- (a) ✅ recommended: **an inline SVG set in `components/icons.tsx`**
  copied from the mockup (stroke 1.8, `currentColor`), ten functions,
  no dependency, nothing for the CSP or the bundle to absorb.
- (b) `lucide-react`, shadcn's default; tree-shaken it is small, but it
  is a dependency with its own release cadence for ten glyphs.
- (c) Text labels only, no icons; the sidebar loses its scan-ability.
- other:

### OQ5: Where screenshot snapshots are generated and stored

**Resolved 2026-10-16: (a).**

macOS and Linux rasterise the bundled fonts differently, so a snapshot
taken locally fails in CI and vice versa.

- (a) ✅ recommended: **Linux-only snapshots committed under
  `ui/e2e/__screenshots__/`**, the spec skipped off-Linux with a
  message, regenerated through the official Playwright container by a
  `make test-ui-e2e-update-snapshots` target that runs the e2e stack
  on the host and Chromium in the container against
  `host.docker.internal:3100`.
- (b) Commit per-platform snapshots (Playwright's default naming) and
  let each developer regenerate their own; doubles the PNGs and the
  macOS set drifts with every OS update.
- (c) No screenshot tests; axe and the Playwright layout assertions
  only (DESIGN-0036 OQ16 b).
- other:

### OQ6: Fleet rule sparklines before the history aggregate exists

**Resolved 2026-10-16: (a).**

Without `aggregate=`, a 20-snapshot sparkline per rule is one
`/compliance/history?kind&rule` request per rule returning one point
per org per snapshot (six orgs × 20 = 120 points, one page each at the
200 limit).

- (a) ✅ recommended: **build the Trend column hidden in 3.3 and un-hide
  it in 7.1** on `aggregate=fleet&kind&rule`; the same PR delivers
  both, so no user ever sees the column missing.
- (b) Ship it in 3.3 on the per-org points aggregated client-side; N
  rules × one request on every Fleet load, growing with orgs.
- (c) Drop the sparkline column; the Trend-in-points column already
  shows direction.
- other:

### OQ7: The `?series=` pill selection and `?range=` as URL state

**Resolved 2026-10-16: (a).**

- (a) ✅ recommended: **both in the URL** (`?series=percent|failing|
  not_applicable|unknown`, `?range=1d|7d|30d|90d`), retained across
  links by `retainSearchParams`, so a shared Fleet link reproduces the
  chart; default `percent` and `90d` are omitted from the URL.
- (b) `range` in the URL, `series` in component state; a shared link
  loses the selected series.
- (c) Both in `localStorage`; links stop being reproducible.
- other:

### OQ8: Phase 2 e2e breakage window

**Resolved 2026-10-16: (a).**

Phase 2 moves selectors the existing suite depends on, and the suite
must stay green at every commit for `git bisect`.

- (a) ✅ recommended: **2.6 and 2.8 land in the same commit**, so the
  shell swap and the selector migration are atomic; the tasks stay
  separate for tracking.
- (b) Mark the affected specs `test.fixme` in 2.6 and un-fixme them in
  2.8; two commits, a red-but-skipped window.
- other:

### OQ9: Sidebar collapse breakpoint

**Resolved 2026-10-16: (a).**

- (a) ✅ recommended: **900px**, as DESIGN-0036 states; below it the
  sidebar is a left `Dialog` opened from a top-bar button, and the
  Orgs grid and tables already scroll horizontally in their cards.
- (b) No collapse; the UI is desktop-only and 900px is below any
  supported viewport.
- other:

## References

- DESIGN-0036: Frontend design cleanup and refresh for the v2 UI
  (`docs/design/0036-frontend-design-cleanup-and-refresh-for-the-v2-ui.md`)
  and `ui/design/repo-guardian-mockup.html`.
- DESIGN-0027: v2 read-only API, business UI and status page (security
  list, "The UI").
- IMPL-0025 Phases 20 to 22 (BFF, views, chart, e2e stack);
  IMPL-0029 task 8.7 (Controls views on these components); INV-0021
  (rule-shaped views slated for replacement).
- `api/openapi.yaml`; `internal/api/{status,cursor,convert}.go`;
  `internal/store/postgres/queries/api_*.sql`.
- `ui/server/static.ts` (CSP), `ui/web/src/router.tsx`,
  `ui/web/src/index.css`, `ui/eslint.config.js` (HTML-sink ban),
  `ui/e2e/{ui.spec.ts,stack.ts,seed.sql}`.
- `docs/usage/ui.md`, `ui/README.md`.
