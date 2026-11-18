---
id: IMPL-0037
title: "Adopt @donaldgifford/design-system in the UI (DESIGN-0039)"
status: Draft
author: Donald Gifford
created: 2026-11-17
---

<!-- markdownlint-disable-file MD024 MD025 MD041 -->

# IMPL-0037: Adopt @donaldgifford/design-system in the UI (DESIGN-0039)

<!--toc:start-->
<!-- markdownlint-capture -->
<!-- markdownlint-disable MD013 -->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Install and auth](#phase-1-install-and-auth)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Stylesheet and tokens](#phase-2-stylesheet-and-tokens)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Leaves](#phase-3-leaves)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: Primitives and overlays](#phase-4-primitives-and-overlays)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: Tables](#phase-5-tables)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: Shell](#phase-6-shell)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
  - [Phase 7: Removal](#phase-7-removal)
    - [Tasks](#tasks-6)
    - [Success Criteria](#success-criteria-6)
  - [Phase 8: Release and documents](#phase-8-release-and-documents)
    - [Tasks](#tasks-7)
    - [Success Criteria](#success-criteria-7)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
  - [OQ1: How do the documents reach main before the work starts?](#oq1-how-do-the-documents-reach-main-before-the-work-starts)
  - [OQ2: What are the secrets called, and what if CI's GITHUB_TOKEN is refused?](#oq2-what-are-the-secrets-called-and-what-if-cis-github_token-is-refused)
  - [OQ3: What happens when a package problem appears mid-plan?](#oq3-what-happens-when-a-package-problem-appears-mid-plan)
  - [OQ4: How is sorting turned off on the embedded tables?](#oq4-how-is-sorting-turned-off-on-the-embedded-tables)
  - [OQ5: What does the chart version become?](#oq5-what-does-the-chart-version-become)
- [References](#references)
<!-- markdownlint-restore -->
<!--toc:end-->

<!--docz:objective:start-->
## Objective

Move `ui/web` onto `@donaldgifford/design-system` 0.3.0: its
stylesheet and tokens, its primitives, blocks and shell. Delete the
local copies (`components/ui`, `components/charts`, the local shell
chrome, the `@theme` in `index.css`) and the dependencies that only
they needed, `radix-ui` first among them. Ship it as v2.1.0.

**Implements:** [DESIGN-0039](../design/0039-adopting-donaldgifforddesign-system-in-the-ui.md),
which rests on [INV-0025](../investigation/0025-migrating-the-ui-to-the-shared-donaldgifforddesign-system.md).
Closes issue [#252](https://github.com/donaldgifford/repo-guardian/issues/252).

The design's section numbers are cited as "D§n". Its nine open
questions are decided and are not reopened here.

<!--docz:objective:end-->

<!--docz:scope:start-->
## Scope

<!--docz:in-scope:start-->
### In Scope

- `ui/`: `package.json`, `bun.lock`, `.npmrc`, `Dockerfile`, `web/`,
  `e2e/`, `scripts/`, `eslint.config.js`, `README.md`,
  `THIRD_PARTY_NOTICES.md`, `CHANGELOG.yaml`.
- Build and release wiring for the registry token: `Makefile`,
  `docker-bake.hcl`, `.github/workflows/ci.yml`,
  `.github/workflows/ghcr.yml`, `renovate.json5`,
  `.github/dependabot.yml`.
- The release bump: `charts/repo-guardian/Chart.yaml` and the chart
  tests that pin `appVersion`.
- Documents: `CLAUDE.md`, `docs/usage/ui.md` and its screenshot, a
  superseded note on DESIGN-0036, the docz statuses.

<!--docz:in-scope:end-->

<!--docz:out-of-scope:start-->
### Out of Scope

- `ui/server`, the Go code, the API contract, chart templates and
  values.
- New pages, panels, columns or filters; a light theme; a change of
  icon set.
- Changes to the package. A gap found here is an issue on
  `donaldgifford/design-system` (see OQ3).
- The shared Renovate preset (renovate-config#18). This plan does not
  wait for it.
- `ecr.yml`: it builds no UI image.

<!--docz:out-of-scope:end-->
<!--docz:scope:end-->

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its tasks
are checked off and its success criteria are met. The phases are the
design's seven stages plus the release; they land as commits on one
branch and one pull request. `make lint-ui`, `make test-ui` and
`make test-ui-e2e` pass at the end of every phase, so a regression
belongs to one phase.

Rules that hold throughout:

- A task marked *deferred - human required* is the maintainer's; work
  continues past it.
- A visual baseline is regenerated in Phase 2 (colour, if needed) and
  Phase 6 (the frame) and nowhere else.
- A changed e2e selector is run against the pre-change build first, to
  show it can fail.
- Something wrong in the package is handled per OQ3, never by
  restyling or patching its internals here.

---

<!--docz:phase:start-->
### Phase 1: Install and auth

The package is installable everywhere this repository installs, and nothing imports it yet. The app is byte-for-byte the same afterwards. This phase goes first because every later phase depends on CI and the image build being able to fetch the package (D§2).

<!--docz:tasks:start-->
#### Tasks

- [x] 1.1 Create the branch `feat/ui-design-system` from `main` once the documents PR is merged (OQ1).
- [x] 1.2 Add `ui/.npmrc` with the `@donaldgifford` scope registry and `_authToken=${NPM_TOKEN}`. Confirm `ui/.dockerignore` does not exclude it.
- [x] 1.3 `Makefile`: `ui-install` and `docker-build-ui` export `NPM_TOKEN` from the environment when set and from `gh auth token` otherwise. The fallback must not print the token and must fail with a message naming `ui/README.md` when neither is available.
- [x] 1.4 Add `@donaldgifford/design-system` at exactly `0.3.0` to `devDependencies` and commit `bun.lock`. Confirm the installed package has `src/` (a `dist/` with no `src/` is the stale-cache case from design-system#11).
- [x] 1.5 `ui/Dockerfile`, build stage only: `COPY package.json bun.lock .npmrc ./` and `RUN --mount=type=secret,id=NPM_TOKEN NPM_TOKEN="$(cat /run/secrets/NPM_TOKEN)" bun install --frozen-lockfile`. Leave the deps stage and the `# syntax` line alone.
- [x] 1.6 `docker-bake.hcl`: `secret = ["id=NPM_TOKEN,env=NPM_TOKEN"]` on `_ui`. `make docker-build-ui` builds; `docker history --no-trunc` on the result shows no token; the runtime image has no `node_modules/@donaldgifford`.
- [x] 1.7 `ci.yml`: the `ui` and `ui-e2e` jobs get `permissions: { contents: read, packages: read }` and `NPM_TOKEN: ${{ secrets.GITHUB_TOKEN }}` in the install step's `env`. No other job changes.
- [x] 1.8 Push and confirm both jobs install with `GITHUB_TOKEN`. If the registry refuses it, switch the two jobs to the repository secret from OQ2 and record which was needed in this plan. **Result:** `GITHUB_TOKEN` with `packages: read` was enough; both jobs installed and passed on #255. No Actions secret is needed for CI.
- [x] 1.9 `ghcr.yml`: `NPM_TOKEN: ${{ secrets.GITHUB_TOKEN }}` in the `env` of the `ui-image` bake step. Diff `ghcr.yml` against `ecr.yml` and confirm `ecr.yml` needs nothing. Run `actionlint`.
- [ ] 1.10 Dispatch `ghcr.yml` on the branch with `dry_run: true` and a tag input, and confirm the `ui-image` job builds both platforms. *deferred - human required* if the dispatch needs the maintainer. **Deferred - human required:** as written this cannot exercise the change. `ui-image` checks out `inputs.tag`, not the dispatched branch, and its bake step is skipped when `dry_run` is true. The first real proof is the release run (task 8.9); a pre-merge check would need an rc tag on this branch (rule 6) and a non-dry dispatch, which is the maintainer's call.
- [ ] 1.11 `renovate.json5`: add `github>donaldgifford/renovate-config:node` to `extends`, a `hostRules` entry for `npm.pkg.github.com` reading the Renovate secret, and a `packageRules` entry that keeps `@donaldgifford/design-system` in its own pull request, out of the non-major group and never automerged. **Partly done:** `:node` and the package rule are in. The `hostRules` entry waits on the maintainer: whether boop-bot (which runs Renovate here) already reads GitHub Packages, or needs `GH_PACKAGES_READ_TOKEN` exposed where it runs.
- [x] 1.12 `.github/dependabot.yml`: a `registries` entry for `npm.pkg.github.com` and a reference to it from the `bun` update for `/ui`.
- [ ] 1.13 Create the `read:packages` token and store it as the Renovate secret and the Dependabot secret named in OQ2. *deferred - human required* **Partly done (2026-11-18):** `GH_PACKAGES_READ_TOKEN` is set in the Actions and Dependabot stores; the Renovate side waits on 1.11.
- [x] 1.14 `ui/README.md`: a "Registry token" section (the scope, why a public package needs one, the `gh` fallback, the Docker secret).

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- `make ui-install` works on a laptop with only `gh` signed in, and in CI with `GITHUB_TOKEN`.
- `make lint-ui`, `make test-ui` and `make test-ui-e2e` pass with no source change under `ui/web`.
- `make docker-build-ui` produces an image with no token in any layer and no package source in the runtime stage.
- The `ghcr.yml` dry run built the UI image (or 1.10 is marked deferred with the reason).
- `git diff main -- ui/web` is empty.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 2: Stylesheet and tokens

The app takes its Tailwind entry, tokens, fonts, focus outline and reduced-motion rule from the package, and every class and `var()` uses the package's token names. Local components are still in place, so the pages keep their structure; the colours shift by the amounts the design accepts, the destructive red visibly (D§3).

<!--docz:tasks:start-->
#### Tasks

- [x] 2.1 Classify the 40 `accent` uses (33 utilities, 7 `var()`), file by file, with the D§3 rule: brand blue becomes `primary`, a focus ring becomes `ring`, a selected tint becomes `primary` at the same opacity, a hover surface stays `accent`. Record the table in the pull request description before editing.
- [x] 2.2 `web/src/index.css` becomes the single `@import "@donaldgifford/design-system/globals.css";`. Delete the `@theme`, the `:root` and `body` rules, the `:focus-visible` rule, the reduced-motion block, the `rg-*` keyframes and the `animate-*` utilities.
- [x] 2.3 The four local animation utilities are used by `components/ui/dialog.tsx` and `drawer.tsx`, which survive until Phases 4 and 6. Point them at `tw-animate-css` classes (`animate-in`, `fade-in-0`, `slide-in-from-right`, `slide-in-from-left`) so overlays still animate in between.
- [x] 2.4 `--breakpoint-nav` goes with the `@theme`. The local sidebar and top bar use `nav:` until Phase 6: change their two uses to `md:` now, which is the package shell's breakpoint.
- [x] 2.5 `web/index.html`: `class="dark"` on `<html>`.
- [x] 2.6 `web/src/main.tsx`: remove the five `@fontsource` imports.
- [x] 2.7 Rename utilities: `panel` to `card`, `panel-2` to `muted`, `ok`/`warn`/`bad` to `success`/`warning`/`destructive`, and `accent` per 2.1.
- [x] 2.8 Rename the inline `var(--color-…)` references the same way, including `toneVar` and chart colour constants. Meaningless series colours read `--chart-1` to `--chart-5`.
- [x] 2.9 `lib/format.ts`: `Tone` becomes `success | warning | destructive | info | muted | outline`; update `toneFor` and every tone literal and test.
- [x] 2.10 Add `ui/scripts/check-tokens.ts` and a `check:tokens` script that fail on `-panel`, `-panel-2`, `-ok`, `-warn`, `-bad` utilities and `--color-panel`, `--color-ok`, `--color-warn`, `--color-bad` under `web/src`. Add a test that proves it fires, as `scripts/eslint-ban.test.ts` does for the markup ban. Wire it into `lint-ui` in the `Makefile`.
- [x] 2.11 Run `make test-ui-e2e-visual`. If the four baselines differ beyond the threshold, regenerate them once with `make test-ui-e2e-update-snapshots` and review the images: the only visible changes should be colour.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- `index.css` is one line and `main.tsx` imports no font.
- `bun run check:tokens` passes, and its own test shows it failing on a planted old name.
- `make lint-ui`, `make test-ui`, `make test-ui-e2e` pass; `a11y.spec.ts` passes with no rule disabled.
- `check:dist` reports CSP-clean with the package's fonts.
- No page changed structure: any visual baseline diff is colour only.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 3: Leaves

Helpers and leaf components with no overlay behaviour move to the package: the `lib` and `hooks` modules, the charts, and the figure blocks. Each swap changes call sites only (D§5, "Blocks and charts").

<!--docz:tasks:start-->
#### Tasks

- [x] 3.1 `lib/utils.ts` (`cn`), `lib/use-media-query.ts`, `lib/csv.ts`, `lib/time.ts`, `charts/scale.ts`: replace every import with the package module and delete the files and their tests. `SIDE_BY_SIDE` becomes `DETAIL_INLINE_QUERY` from `shell/detail-panel`.
- [x] 3.2 `floorPercent`, `formatPercent`, `heatOpacity`: import from `lib/percent`; delete the local definitions in `lib/posture.ts`, `lib/format.ts` and `lib/viewmodel.ts`. `compliantPercent` stays. `api/testdata/compliant_percent.json` must still drive the SPA's test.
- [x] 3.3 `components/time.tsx` to `blocks/time`: `TimeZoneProvider storageKey="rg.tz"`. Rewrite the toggle's test for pressed buttons. Confirm a stored `rg.tz` from the old build is honoured.
- [x] 3.4 `components/ExportButton.tsx` to `blocks/export-button`: pass `name` for the accessible name and `filename="repo-guardian-<page>"` so file names do not change. The e2e download assertions must pass unedited.
- [x] 3.5 `components/LoadMore.tsx` to `blocks/load-more`.
- [x] 3.6 `ui/meter` to `blocks/meter` (`size`, required `label`); `ui/chip` to `blocks/filter-chip`; `ui/pill` to `blocks/series-pills`, including the fleet's hand-rolled series picker.
- [x] 3.7 `ui/kpi` to `blocks/kpi`. Move `DeltaText` and `Delta` to `components/DeltaText.tsx`, pass it as `hint`, and update `lib/fleet.ts`'s type import.
- [x] 3.8 `ui/stat-strip` to `blocks/stat-strip` (`cells` to `items`, `subline` to `sub`).
- [x] 3.9 `charts/ChartCard` and `tooltip` to `blocks/chart-card` (`description`, `action`, `strip`). The fleet card's strip must stay visible in table view.
- [x] 3.10 `charts/TrendChart` to `blocks/trend-chart`: `points: {at, value}[]`, `series`, and `trendTable(points, series, { head: "Snapshot", noun: "snapshots" })`.
- [ ] 3.11 `BarBuckets`, `PairBars`, `BarList`, `StackedBars`, `Sparkline` to their blocks. Check each chart with a null in its series: `Sparkline` now breaks the line.
- [ ] 3.12 `charts/DistBar` to `blocks/dist-bar` with `size="sm"` and `label`; rebuild `PostureBar` on it. The accessible name becomes `<label>: <summary>`; confirm the e2e `getByRole("img", { name: … })` still matches.
- [ ] 3.13 `ui/group-grid` to `blocks/group-grid` (`heading`, `label`, string headers). The footer row is `data-row="footer"`: update `e2e/ui.spec.ts` and the orgs tests.
- [ ] 3.14 Delete `components/charts/` (including its barrel and `charts.test.tsx`) and the leaf files replaced above. Remove `recharts` from `package.json` and confirm one copy remains in the lockfile.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- No file imports from `components/charts`, and the directory is gone.
- `recharts` is not a direct dependency.
- CSV file names, the stored time zone and every chart's table view are unchanged, shown by the existing e2e passing.
- `make lint-ui`, `make test-ui`, `make test-ui-e2e` pass. `make test-ui-e2e-visual` passes against the Phase 2 baselines; a diff here is a regression to fix, not to re-baseline.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 4: Primitives and overlays

Buttons, badges, cards, tabs, popovers, menus and the detail panel move to the package, which replaces Radix with Base UI under every overlay. The test harness changes first, because Base UI decides at module load whether a document exists (D§5, D§7, D§8).

<!--docz:tasks:start-->
#### Tasks

- [ ] 4.1 `web/testing/preload.ts` (the bunfig preload from #254): load `@base-ui/react` and `cmdk` under the temporary document, beside `radix-ui`, and drop `radix-ui` from it in Phase 7. Rendering test files keep `withDOM(cleanup)`. Run the unit suite in CI's file order and in 20 shuffled orders to show no overlay test depends on which file loads first.
- [ ] 4.2 `ui/button` to `components/button`. Give the three calls with no `variant` (`LoadMore`'s former call site if any remains, `OrgScope`, `views/repositories.tsx`) `variant="outline"`. Replace the four `<Link><Button tabIndex={-1}>` nestings with `render={<Link …/>}`.
- [ ] 4.3 `ui/badge` to `components/badge` with `variant`; rebuild every badge in `components/badges.tsx` on it. `HoldBadge` keeps its sentence as the title.
- [ ] 4.4 `ui/avatar` to `components/avatar` at its current call site (it moves again in Phase 6).
- [ ] 4.5 Add `components/Panel.tsx`: the `title`/`subtitle`/`right`/`bodyClassName` API on `Card render` as a `section` and `CardTitle render` as an `h3` (function form, to satisfy the heading lint rule). Move the roughly 40 `Card` call sites to it. The three bare-surface uses get explicit class resets; the status page's two labelled cards keep a region role.
- [ ] 4.6 Add `components/SegmentedTabs.tsx`: `Tabs({label, options, value, onChange})` and `SearchTabs` on the package's tabs. Verify with a test that it works with no panel and that unselected tabs are out of the tab order; if Base UI does not allow it, keep the roving-tabindex implementation local and note it here. Rebuild `RangeTabs` on it.
- [ ] 4.7 `ui/popover` to `components/popover`; move `PopoverHeading`, `PopoverItem` and `PopoverSeparator` to `components/OptionRows.tsx`. Call sites pass `align="start"`, `sideOffset={6}` and their width classes; `asChild` becomes `render`. `filters/FilterChips.tsx` and `page/OrgScope.tsx` are the consumers.
- [ ] 4.8 `ui/dropdown-menu` to `components/dropdown-menu` in `TopBar`: `onSelect` becomes `onClick` on Sign out, the label goes inside a `DropdownMenuGroup`, the Documentation item uses `render={<a …/>}`.
- [ ] 4.9 `ui/detail-drawer` to `shell/detail-panel` in both drawers: `onOpenChange`, `description`, `label` (the existing "Finding: …" and "Remediation: …" strings), `subject={open}`, and the page grid's second column as `var(--sheet-width-wide)`.
- [ ] 4.10 `DetailKV` to `DetailsGrid layout="list"` in `views/policy.tsx`, `views/repositories.tsx` and both drawers. Check a long value (a pull request URL, an evidence line) wraps.
- [ ] 4.11 Rewrite `components/ui/behaviour.test.tsx` and `primitives.test.tsx` cases that cover local behaviour (focus return, Escape, the options tabs) as tests of the local survivors; delete the cases that tested the deleted primitives.
- [ ] 4.12 e2e: the drawer's dialog-name assertions pass unedited. Any selector that named local markup moves to a role, and each changed selector is first shown to fail against the pre-change build.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- No file under `web/src` imports `radix-ui` except `components/ui/command.tsx`, `dialog.tsx` and `drawer.tsx`, which the shell still uses until Phase 6.
- Keyboard checks pass in e2e: focus enters each overlay on open, returns to the row or trigger on close, Escape closes.
- Sign out works from the menu (an e2e assertion, since a wrong handler fails silently).
- `make lint-ui`, `make test-ui`, `make test-ui-e2e` pass; `a11y.spec.ts` passes unchanged. Visual diffs are limited to overlay width and motion.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 5: Tables

Every table renders through the package's `DataTable`: the 14 embedded tables as plain tables, then the three cursor-paged lists in `manual` mode with `LoadMore` beneath. The routes' search parsing does not change (D§6).

<!--docz:tasks:start-->
#### Tasks

- [ ] 5.1 Add a unit test that a `DataTable` with `sortable={false}` renders plain-text headers (no button, no `aria-sort`) for a representative embedded table, and run it against the first converted table.
- [ ] 5.2 Convert the embedded tables' column definitions to `dataTableColumns<T>()`: `views/policy.tsx`, `controls.tsx` (two), `orgs.tsx` (four), `fleet.tsx` (three), `repositories.tsx` (three embedded), `findings/drawer.tsx`. `width` and `className` become `meta.className`; `numeric` and `hideBelow` move to `meta`.
- [ ] 5.3 Render them as `<DataTable sortable={false} bare label=… columns data getRowId empty />`. Each keeps its accessible name and its empty text.
- [ ] 5.4 Convert the three lists (`findings.tsx`, `remediations.tsx`, the repositories list) to `<DataTable manual bare label=… selectedId onRowClick compact />` followed by the package's `LoadMore`. No `search` or `onSearchChange` is passed.
- [ ] 5.5 Change the focus-return lookup in `findings.tsx` and `remediations.tsx` from `tr[data-key=…]` to `tr[data-row-id=…]`.
- [ ] 5.6 Verify row activation on the lists: a click on the repository or pull-request link inside a row follows the link and does not open the drawer; Enter and Space on the row open it; a key pressed inside the row does not.
- [ ] 5.7 Verify the side-by-side layout still drops `hideBelow: "compact"` columns at 1600px with the drawer open.
- [ ] 5.8 Delete `components/ui/data-table.tsx`, `table.tsx` and the table cases in `tables.test.tsx`; keep or move the cases that cover view columns.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- `components/ui/data-table` and `components/ui/table` have no importer and are deleted.
- No list shows a pager, a row count or a sort control, and no embedded table sorts (OQ4 as decided).
- The findings and remediations e2e flows (open, switch rows, close, `?open=` deep link, load more) pass unedited.
- `make lint-ui`, `make test-ui`, `make test-ui-e2e` pass. Visual diffs are table row and head heights only.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 6: Shell

The frame becomes the package's: a full-height collapsible sidebar with the app identity on top and the account menu at the foot, and a top bar with the trigger, breadcrumbs, search and time zone. This is the phase where the app looks different, and where the visual baselines are regenerated (D§4, D§9). The mockup already shows the target.

<!--docz:tasks:start-->
#### Tasks

- [ ] 6.1 `Shell.tsx`: `SidebarProvider` (controlled) and `SidebarInset` around the main column. Keep the `/me` gate and `AccessView` outside the frame, the 1400px content width and the page padding.
- [ ] 6.2 Persist the collapsed state in `localStorage` under `rg.sidebar`, read once at start with a safe default when storage is unavailable; unit-test read, write and the default.
- [ ] 6.3 `AppSidebar.tsx` on `ShellSidebar`: the header is a link to `/` built from `SidebarMenuButton` and `AppMark` with "Repo Guardian" and "compliance · read-only", without `ShellSidebarIdentity`.
- [ ] 6.4 Navigation from `shell/nav.ts`, unchanged: `ShellSidebarGroup` per group, `SidebarMenuButton render={<Link/>}` with `isActive`, `aria-current="page"` and `tooltip`; counts in `SidebarMenuBadge` from the same hooks; the Documentation entry as an external anchor. Icons are passed as nodes from the local `Icon`.
- [ ] 6.5 The footer: the account menu on `DropdownMenu` with `AvatarMark` and `initialsOf`, name and subject, Documentation, and Sign out submitting the hidden POST form with `onClick`.
- [ ] 6.6 `AppTopBar.tsx` on `TopBarLayout`: `SidebarTrigger`, a separator and the breadcrumbs in `start`; the Search button with `Kbd` and `TimeZoneToggle` in `end`. Remove the `data-slot="app-switcher"` element.
- [ ] 6.7 `Breadcrumbs.tsx` on `components/breadcrumb`, labelled "Breadcrumb", links through `BreadcrumbLink render={<Link/>}`, the leaf with `aria-current="page"`. `crumbsFor` and the label provider are untouched.
- [ ] 6.8 `CommandPalette.tsx` on `CommandDialog` with `commandProps={{ shouldFilter: false, loop: true }}`. `palette.test.tsx` keeps its matching cases (a control matches on its title) and the wrap-around case.
- [ ] 6.9 `page/PageHeader` to `blocks/page-header` (`meta`, `description`, `actions`); adjust padding through `className` so the page's spacing is the mockup's.
- [ ] 6.10 `components/problem.tsx`: `ProblemView` builds the package's `ApiError` from a `ProblemError` and renders `ErrorState`; `ErrorBoundary` stays and renders it. The request id shows for a body carrying `request_id`.
- [ ] 6.11 Delete `shell/Sidebar.tsx`, `shell/TopBar.tsx`, `MobileSidebar`, and `components/ui/command.tsx`, `dialog.tsx`, `drawer.tsx`, `avatar.tsx`.
- [ ] 6.12 Rewrite `shell.test.tsx`, `nav.test.ts` consumers and `page.test.tsx` against roles. In e2e, replace `b[aria-current=page]` with `[aria-current="page"]` inside the Breadcrumb nav, and the "Open navigation" flow with the trigger; show each changed selector failing first.
- [ ] 6.13 Add e2e for what is new: the sidebar collapses to the rail and stays collapsed after a reload; below 768px the trigger opens the sheet and choosing an entry closes it; `⌘K` still opens the palette.
- [ ] 6.14 Run `a11y.spec.ts` on the new frame, including the collapsed rail and the open sheet. Fix findings in local code; a finding inside a package component is an upstream issue (OQ3).
- [ ] 6.15 Regenerate the four visual baselines with `make test-ui-e2e-update-snapshots` and review each image against the mockup.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- The frame matches the mockup's: identity, groups, counts, account menu, trigger, breadcrumbs, search, time zone.
- No file under `web/src` imports `radix-ui`.
- Every route, URL parameter and deep link works as before; both Playwright projects pass, including the two-org visibility assertions.
- `a11y.spec.ts` passes with no rule disabled and no new exclusion.
- The four baselines are regenerated once, in the pinned image, and reviewed.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 7: Removal

What the migration made unnecessary is deleted, and the rules that keep it from coming back are added (D§1, D§2).

<!--docz:tasks:start-->
#### Tasks

- [ ] 7.1 Delete `components/ui/` (whatever remains, with its test files) and confirm `components/charts/` is gone.
- [ ] 7.2 Remove from `ui/package.json` whatever nothing imports: `radix-ui`, `clsx`, `tailwind-merge`, `class-variance-authority`, `@fontsource/ibm-plex-sans`, `@fontsource/jetbrains-mono`. Check each with a search before removing it, and run `bun install` to settle the lockfile.
- [ ] 7.3 `eslint.config.js`: `no-restricted-imports` for `radix-ui` and `@radix-ui/*` under `web/`, with a case in `scripts/eslint-ban.test.ts` proving it fires.
- [ ] 7.4 Check the layering rules of D§1 by search: no local barrel re-exporting package modules, no CSS targeting a package `data-slot`.
- [ ] 7.5 Give each local survivor (`Panel`, `SegmentedTabs`, `OptionRows`, `DeltaText`, `common`, `problem`, `icons`) a header comment stating why it is not in the package. Use the `todo-comments` format for any note left behind.
- [ ] 7.6 `THIRD_PARTY_NOTICES.md`: add the package (Apache-2.0) and the libraries now bundled through it; note that the fonts arrive through the package.
- [ ] 7.7 Measure the bundle (`bun run build`) and record JS and CSS sizes here beside `main`'s 989 kB and 31 kB.
- [ ] 7.8 Confirm issue #252's "done when" list item by item and record the result here.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- `ui/web/src/components/ui` and `components/charts` do not exist.
- `ui/package.json` matches D§2's table.
- The Radix import ban and the old-token check both fail on a planted violation and pass on the tree.
- `make lint-ui`, `make test-ui`, `make test-ui-e2e`, `make test-ui-e2e-visual` and `make docker-build-ui` pass.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 8: Release and documents

The change is released as v2.1.0 and the documents say what the code now does.

<!--docz:tasks:start-->
#### Tasks

- [ ] 8.1 `charts/repo-guardian/Chart.yaml`: `appVersion` to `2.1.0` and `version` to `2.1.0` (OQ5). Bump the `appVersion` pins in `charts/repo-guardian/tests/` in the same commit and run `make helm-docs`.
- [ ] 8.2 `ui/CHANGELOG.yaml`: a 2.1.0 entry at the top (tags `improved`, `ui`), then `cd ui && bun run gen:changelog`; commit the YAML, the generated JSON and `Chart.yaml` together. `check:changelog` passes.
- [ ] 8.3 `CLAUDE.md` §The UI: the package and its pin, the registry token on every install path, the local survivors, the Base UI preload in `web/testing/dom.ts`, the old-token check, and the corrected breakpoint. Remove what describes the deleted components.
- [ ] 8.4 `ui/README.md`: the design-language section points at the package and its Storybook; the make-target table and the token section are current.
- [ ] 8.5 `docs/usage/ui.md` §The shell: the collapsible sidebar, the account menu, 768px, the `rg.sidebar` key. Re-render `docs/usage/img/ui/status.png` in the pinned Playwright image.
- [ ] 8.6 Add a note at the top of DESIGN-0036 that its token and shell sections are superseded by DESIGN-0039.
- [ ] 8.7 Open the pull request with the `minor` label. State in the description that the label cuts `v2.1.0`, and include the `accent` table from 2.1 and the bundle sizes from 7.7.
- [ ] 8.8 Merge. *deferred - human required*
- [ ] 8.9 Confirm the push-triggered `release.yml` run created jobs, tagged `v2.1.0`, and that `ghcr.yml`'s `ui-image` and `ui-image-slsa` jobs succeeded with the build secret. *deferred - human required*
- [ ] 8.10 Dispatch `changelog-update.yml` with `-f tag=v2.1.0` and merge its pull request. *deferred - human required*
- [ ] 8.11 Set DESIGN-0039 to Implemented and this plan to Completed with `docz status set`, and close issue #252.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- `v2.1.0` exists, with signed images and chart, and the UI image carries provenance.
- The status page's Changelog card shows 2.1.0.
- `CLAUDE.md`, `ui/README.md` and `docs/usage/ui.md` describe the package-based UI and nothing they say about the deleted components remains.
- DESIGN-0039 is Implemented, this plan is Completed, issue #252 is closed.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:file-changes:start-->
## File Changes

| File | Action | Description |
| ---- | ------ | ----------- |
| `ui/.npmrc` | Create | scope registry and the `${NPM_TOKEN}` reference |
| `ui/package.json`, `ui/bun.lock` | Modify | add the package (dev, exact); remove seven dependencies |
| `ui/Dockerfile` | Modify | secret mount and `.npmrc` in the build stage |
| `docker-bake.hcl` | Modify | `NPM_TOKEN` secret on `_ui` |
| `Makefile` | Modify | token for `ui-install` and `docker-build-ui`; `check:tokens` in `lint-ui` |
| `.github/workflows/ci.yml` | Modify | `packages: read` and `NPM_TOKEN` on `ui`, `ui-e2e` |
| `.github/workflows/ghcr.yml` | Modify | `NPM_TOKEN` on the `ui-image` bake step |
| `renovate.json5`, `.github/dependabot.yml` | Modify | registry access; the package's own update rule |
| `ui/web/index.html`, `web/src/index.css`, `web/src/main.tsx` | Modify | `class="dark"`, one import, no font imports |
| `ui/scripts/check-tokens.ts` (+ test) | Create | fails on a removed token name |
| `ui/eslint.config.js`, `ui/scripts/eslint-ban.test.ts` | Modify | Radix import ban and its proof |
| `ui/web/testing/preload.ts` | Modify | preload Base UI and cmdk |
| `ui/web/src/components/ui/`, `components/charts/` | Delete | replaced by the package |
| `components/time.tsx`, `ExportButton.tsx`, `LoadMore.tsx`, `lib/{csv,time,utils,use-media-query}.ts` | Delete | replaced by the package |
| `components/Panel.tsx`, `SegmentedTabs.tsx`, `OptionRows.tsx`, `DeltaText.tsx` | Create | the local survivors, on package parts |
| `components/shell/{AppSidebar,AppTopBar}.tsx` | Create | the frame on the package shell |
| `components/shell/{Sidebar,TopBar}.tsx` | Delete | replaced |
| `components/shell/{Shell,CommandPalette}.tsx`, `page/Breadcrumbs.tsx`, `components/problem.tsx`, `badges.tsx`, `posture.tsx`, `filters/*`, `page/{OrgScope,RangeTabs}.tsx` | Modify | rebuilt on package parts |
| `ui/web/src/views/**`, `lib/{format,posture,viewmodel,fleet}.ts` | Modify | call sites, tone names, column definitions |
| `ui/e2e/*.spec.ts`, `e2e/__screenshots__/*.png` | Modify | selectors to roles; new shell cases; baselines |
| `ui/THIRD_PARTY_NOTICES.md`, `ui/README.md`, `ui/CHANGELOG.yaml`, `web/src/generated/changelog.json` | Modify | notices, setup, release entry |
| `charts/repo-guardian/Chart.yaml`, `charts/repo-guardian/tests/*` | Modify | the release bump and its pins |
| `CLAUDE.md`, `docs/usage/ui.md`, `docs/usage/img/ui/status.png`, DESIGN-0036 | Modify | documents |

<!--docz:file-changes:end-->

<!--docz:testing:start-->
## Testing Plan

- [ ] `make lint-ui` (typecheck of app and package source, ESLint, API drift, changelog, `check:tokens`, `check:dist`) passes at the end of every phase.
- [ ] `make test-ui` passes at the end of every phase; tests of deleted components are deleted with them, tests of local behaviour are rewritten against roles.
- [ ] `make test-ui-e2e` passes at the end of every phase on both Playwright projects, with the two-org visibility assertions intact.
- [ ] `a11y.spec.ts` passes at every phase with no rule disabled and no new exclusion, and covers the collapsed rail and the open sheet from Phase 6.
- [ ] `make test-ui-e2e-visual`: baselines regenerated at most in Phases 2 and 6, in the pinned image, each reviewed against the mockup.
- [ ] Keyboard paths checked explicitly in Phases 4 to 6: overlay focus in and out, Escape, row activation with Enter and Space, `⌘K`, `⌘B`.
- [ ] The two new guards (`check:tokens`, the Radix import ban) each have a test that fails on a planted violation.
- [ ] `make docker-build-ui` in Phases 1 and 7; the `ghcr.yml` dry-run dispatch in Phase 1; the real release run in Phase 8.
- [ ] Things with no automated check, verified by hand and noted in the pull request: the stored `rg.tz` survives the upgrade, CSV file names are unchanged, Sign out works.

<!--docz:testing:end-->

<!--docz:dependencies:start-->
## Dependencies

- `@donaldgifford/design-system` 0.3.0 on GitHub Packages (0.2.1 plus
  the `sortable` prop). A later 0.3.x may be taken if it fixes
  something found here; a 0.4.0 is not taken mid-plan.
- The documents (INV-0025, DESIGN-0039 with its mockup, this plan)
  merged to `main` first (OQ1).
- A `read:packages` token for Renovate and Dependabot, created by the
  maintainer (task 1.13). Not needed for CI if `GITHUB_TOKEN` is
  accepted (task 1.8).
- Docker and Go locally for the e2e and visual suites.
- #254 (the unit suite no longer depends on test file order) merged.
- Not a dependency: renovate-config#18.

## Open Questions

Answer with a letter, or write under `other`. Option (a) is the
recommendation in each.

### OQ1: How do the documents reach `main` before the work starts?

INV-0025 is on `docs/inv-design-system-migration` (pushed);
DESIGN-0039, the mockup and this plan are on
`docs/design-design-system-migration`, stacked on it (not yet
committed).

- **(a)** ✅ recommended: one documents pull request from the stacked
  branch, containing all four, labelled `dont-release`, merged before
  the implementation branch is cut. One review, and the design is
  accepted with its mockup as the standing rule asks.
- **(b)** Two pull requests, the investigation first and then design,
  mockup and plan. Keeps the investigation's history separate for one
  more merge.
- **(c)** No documents pull request: carry them in the implementation
  pull request. One merge in total, but the work would start from an
  unaccepted design and the release PR would carry 1,000 lines of
  documents.
- **other:**

**Decision:** (a), 2026-11-17. One `dont-release` documents pull request from the stacked branch, merged before `feat/ui-design-system` is cut.

### OQ2: What are the secrets called, and what if CI's `GITHUB_TOKEN` is refused?

Renovate and Dependabot each need a maintainer-created token with
`read:packages` (a classic token; fine-grained tokens do not cover
packages). CI should work with `GITHUB_TOKEN`, which task 1.8 proves.

- **(a)** ✅ recommended: one classic token stored under the name
  `GH_PACKAGES_READ_TOKEN` in three places: Renovate's secrets,
  Dependabot's secrets, and, only if 1.8 fails, Actions secrets for
  the `ui` and `ui-e2e` jobs. One name to remember and rotate.
- **(b)** Separate tokens per consumer with distinct names. Narrower
  blast radius on a leak, three things to rotate.
- **(c)** Skip the bots' secrets for now; do only the CI fallback if
  needed. Nothing to create today, and the pin is bumped by hand until
  renovate-config#18 lands.
- **other:**

**Decision:** (a), 2026-11-17. One classic `read:packages` token named `GH_PACKAGES_READ_TOKEN`, for Renovate and Dependabot, and for Actions only if task 1.8 shows `GITHUB_TOKEN` is refused.

### OQ3: What happens when a package problem appears mid-plan?

- **(a)** ✅ recommended: file it on `donaldgifford/design-system`,
  keep the affected local file for now, continue with the other
  phases, and take the fixed 0.3.x before Phase 7. The pull request
  does not merge with a local workaround in place.
- **(b)** Allow a temporary local shim marked with a `HACK` comment
  naming the upstream issue, and merge with it. Never blocks on
  upstream; leaves exactly the kind of patch the design rules out.
- **(c)** Stop the plan until the fix is published. Simplest to reason
  about, idle time on every finding.
- **other:**

**Decision:** (a), 2026-11-17. File upstream, keep the local file, continue, take the fixed 0.3.x before Phase 7. No merge with a workaround in place.

### OQ4: How is sorting turned off on the embedded tables?

DESIGN-0039 decided embedded tables do not sort. The package sorts a
plain table by default and offers no table-level switch.

- **(a)** ✅ recommended: a small local helper beside the views that
  wraps `dataTableColumns<T>()` and sets `enableSorting: false` on
  every column it builds. One place, no upstream wait, easy to remove
  if a table should sort later.
- **(b)** Ask upstream for a table-level `sortable` prop and wait for
  it. The cleanest call site; upstream chose inference over a prop
  once already (design-system#11), and it blocks Phase 5.
- **(c)** Write every embedded column as a display column. No helper,
  and it loses accessor typing on about 60 columns.
- **other:**

**Decision:** (a), 2026-11-17, then superseded the same day by (b): upstream published 0.3.0 with `sortable` on `DataTable` (design-system#11). The plan pins 0.3.0, the embedded tables pass `sortable={false}`, and no local helper is written.

### OQ5: What does the chart `version` become?

`appVersion` goes to 2.1.0. The chart's `version` is independent, and
the chart must be republished to carry the new `appVersion`. Chart
templates and values do not change.

- **(a)** ✅ recommended: `2.1.0`. The chart has tracked the app's
  version since 2.0.0, and one number is easier for an operator.
- **(b)** `2.0.1`. Says truthfully that the chart itself did not
  change, and lets chart and app versions drift apart from here.
- **other:**

**Decision:** (a), 2026-11-17. Chart `version` 2.1.0.

<!--docz:dependencies:end-->

<!--docz:references:start-->
## References

- [DESIGN-0039](../design/0039-adopting-donaldgifforddesign-system-in-the-ui.md)
  and [the mockup](https://github.com/donaldgifford/repo-guardian/blob/main/ui/design/repo-guardian-mockup.html)
- [INV-0025](../investigation/0025-migrating-the-ui-to-the-shared-donaldgifforddesign-system.md)
- Issue [#252](https://github.com/donaldgifford/repo-guardian/issues/252)
- [design-system#11](https://github.com/donaldgifford/design-system/issues/11)
  and the package's
  [README](https://github.com/donaldgifford/design-system#readme) and
  [Storybook](https://donaldgifford.github.io/design-system/)
- [renovate-config#18](https://github.com/donaldgifford/renovate-config/issues/18)
- [IMPL-0031](0031-frontend-design-refresh-for-the-v2-ui.md) and
  [IMPL-0032](0032-controls-ui-findings-org-tree-fleet-panels-changelog-and-rc11.md),
  which built what this plan replaces

<!--docz:references:end-->
