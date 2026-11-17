---
id: INV-0025
title: "Migrating the UI to the shared @donaldgifford/design-system package"
status: Concluded
author: Donald Gifford
created: 2026-11-16
---

<!-- markdownlint-disable-file MD025 MD041 -->

# INV-0025: Migrating the UI to the shared @donaldgifford/design-system package

<!--toc:start-->
<!-- markdownlint-capture -->
<!-- markdownlint-disable MD013 -->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [F1: the local surface the package replaces](#f1-the-local-surface-the-package-replaces)
  - [F2: old token names in use](#f2-old-token-names-in-use)
  - [F3: Radix is confined to six files](#f3-radix-is-confined-to-six-files)
  - [F4: eight view files use the table](#f4-eight-view-files-use-the-table)
  - [F5: install paths that need the token](#f5-install-paths-that-need-the-token)
  - [F6: what the package (0.1.1) does not provide](#f6-what-the-package-011-does-not-provide)
  - [F7: upstream decisions, and 0.2.0](#f7-upstream-decisions-and-020)
  - [F8: the install spike](#f8-the-install-spike)
  - [F9: dependencies and licences](#f9-dependencies-and-licences)
  - [F10: the 0.2.0 DataTable against the list views](#f10-the-020-datatable-against-the-list-views)
  - [Handed forward](#handed-forward)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!-- markdownlint-restore -->
<!--toc:end-->

<!--docz:question:start-->

## Question

What exactly has to change in `ui/` to replace its own design tokens and
components with `@donaldgifford/design-system` (0.2.1 or later), so that
`ui/web/src` keeps no local copy of anything the package provides and `radix-ui`
leaves `ui/package.json`, with the UI looking the same apart from differences a
design doc lists and accepts?

Three sub-questions decide the shape of the design that follows:

1. Does the issue's component mapping hold, export by export and prop by prop,
   against the package's Storybook and source?
2. Can the package's `DataTable` (TanStack Table, URL-bound list state) carry
   every list view here, given how the views use TanStack Router search params
   and keyset cursors today?
3. Do install and build work everywhere this repo installs: a developer's
   machine, CI, both stages of `ui/Dockerfile`, and Renovate?

<!--docz:question:end-->

<!--docz:hypothesis:start-->

## Hypothesis

Yes, and mostly mechanically. The package's 0.1.0 was backported from this
repo's `components/ui` and `components/charts`, and its dark theme, fonts and
3px radius follow repo-guardian's, so the expected differences are in APIs, not
in the design.

The expected exceptions, in order of cost:

- **`DataTable`** is a different table, not a port. It is the one piece that may
  need a change in the package, or a local adapter, before the list views can
  move.
- **The `accent` token** changes meaning (the blue here, a hover surface in the
  package), so the token rename cannot be a blind search and replace.
- **Radix to Base UI** changes composition (`render` in place of `asChild`) and
  some event names in the six overlay files and in the tests that drive them.
- **Registry auth** adds a token to every install path, including the Docker
  build.

<!--docz:hypothesis:end-->

<!--docz:context:start-->

## Context

**Triggered by:** issue
[#252](https://github.com/donaldgifford/repo-guardian/issues/252), "ui: migrate
to the shared @donaldgifford/design-system package". The issue is the brief;
this investigation is the first document of the trail it asks for
(investigation, then a design for the staging, then an implementation plan). On
the package's side this migration is IMPL-0001 phase 5.

The UI's design language is DESIGN-0036 (IMPL-0031), extended by DESIGN-0037
(IMPL-0032). Its primitives were hand-copied into `ui/web/src/components/ui/` in
the shadcn style and are kept free of domain knowledge, which is what made the
backport into a shared package possible. INV-0024 put Tabler icons behind the
`Icon` component; the package also uses Tabler.

What the package is, from the issue:

- `@donaldgifford/design-system` on GitHub Packages
  (`https://npm.pkg.github.com/`), public, but the registry asks for a token
  even so.
- React 19, Tailwind v4 (CSS-first), shadcn components on Base UI (not Radix),
  Tabler icons, Recharts, TanStack Table.
- No build step: it ships TypeScript and CSS source, so this app's Vite,
  Tailwind and `tsc` compile and check it under this repo's flags.
- Imports are per module with no barrel: `globals.css`, `tokens.css`,
  `components/<name>`, `blocks/<name>`, `shell/<name>`, `lib/<name>`,
  `hooks/<name>`.

What stays here: anything that knows repo-guardian's domain
(`components/badges.tsx`, `posture.tsx`, `GatedPanel.tsx`, `filters/`,
`OrgScope`, `RangeTabs`, the views, `lib/posture.ts`, `lib/viewmodel.ts` and the
rest of `lib/`). A generic gap found in the package is an issue or PR on
`donaldgifford/design-system`, not a local patch.

<!--docz:context:end-->

<!--docz:approach:start-->

## Approach

1. **Inventory this side.** List every file under `components/ui`,
   `components/charts`, `components/page`, `components/shell` and the shared
   `lib/` files with its exports and its importers, so each has a named
   destination or a reason to stay.
2. **Verify the mapping.** For each row of the issue's mapping table, read the
   package's story and source at the pinned version and record the prop, event
   and markup differences. Mark each row "drop-in", "call sites change" or "gap
   in the package".
3. **Tokens.** Count utility and `var()` uses of the old token names per file.
   Classify every `accent` use as "the blue" (becomes `primary`) or a real hover
   surface. Compare the package's dark values with `index.css` value by value.
4. **`globals.css` against `index.css`.** Decide the fate of each block here:
   the `@theme`, the `nav` breakpoint, the global `:focus-visible` outline, the
   `prefers-reduced-motion` block, the `rg-*` keyframes and the `animate-*`
   utilities.
5. **`DataTable`.** For each list view, write down its current sort, filter,
   cursor and `?open=` search state, then check whether `lib/list-search` and
   `dataTableColumns<T>()` can express it. Server-side keyset paging and "load
   more" are the cases to prove.
6. **Radix to Base UI.** For the six overlay files and their tests, list the
   `asChild`, `onOpenChange`, `onCloseAutoFocus` and `data-state` uses that
   change, and re-check the documented happy-dom and Playwright gotchas
   (module-load document detection, the modal aria-hiding the page) against Base
   UI.
7. **Spike the install** on a throwaway branch: `ui/.npmrc`, `bun install`,
   `tsc`, `vite build`, `check:dist`, the Docker build with a build secret, and
   a CI run using `GITHUB_TOKEN` with `packages: read`.
8. **Dependencies and licences.** Diff the lockfile for the new transitive set,
   look for a second copy of Recharts in the bundle, and list what
   `THIRD_PARTY_NOTICES.md` and the licence check need.
9. **Tests.** List the unit tests that move to the package with their
   components, the ones that stay, and the visual baselines that will change.

<!--docz:approach:end-->

<!--docz:environment:start-->

## Environment

Measured on `main` at `fe48e6c` (2026-11-16).

| Component                      | Version / Value                                                                                        |
| ------------------------------ | ------------------------------------------------------------------------------------------------------ |
| `@donaldgifford/design-system` | 0.2.1 or later (not installed; the spike ran 0.2.0); F6 read 0.1.1 |
| Bun                            | 1.3.14                                                                                                 |
| TypeScript                     | 5.9.3, with `strict`, `noUncheckedIndexedAccess`, `exactOptionalPropertyTypes`, `verbatimModuleSyntax` |
| React                          | 19.3.0                                                                                                 |
| Tailwind CSS                   | 4.3.3                                                                                                  |
| Recharts                       | 3.10.1 here; the package pins 3.8.0                                                                    |
| `radix-ui`                     | 1.8.1                                                                                                  |
| Vite                           | `assetsInlineLimit: 0` (`ui/web/vite.config.ts`)                                                       |

<!--docz:environment:end-->

<!--docz:findings:start-->

## Findings

F1 to F5 are the baseline measured in this repository when the investigation was
opened. Nothing on the package's side has been verified yet; the open items
below carry that work.

### F1: the local surface the package replaces

- `components/ui/`: 19 components (`avatar`, `badge`, `button`, `card`, `chip`,
  `command`, `data-table`, `detail-drawer`, `dialog`, `drawer`, `dropdown-menu`,
  `group-grid`, `kpi`, `meter`, `pill`, `popover`, `stat-strip`, `table`,
  `tabs`) and three test files.
- `components/charts/`: eight charts (`BarBuckets`, `BarList`, `ChartCard`,
  `DistBar`, `PairBars`, `Sparkline`, `StackedBars`, `TrendChart`), `scale.ts`,
  `tooltip.tsx`, an `index.ts` barrel and one test file.
- `components/`: `time.tsx`, `ExportButton.tsx`, `LoadMore.tsx`;
  `components/page/`: `PageHeader`, `Breadcrumbs`; `components/shell/`:
  `Sidebar`, `TopBar`, `CommandPalette`.
- `lib/`: `csv.ts`, `time.ts`, `use-media-query.ts`, `utils.ts`.

Not named in the issue's mapping and still to place: `charts/tooltip.tsx`,
`components/icons.tsx`, `components/common.tsx`, `components/problem.tsx`,
`shell/Shell.tsx` and `shell/nav.ts`.

### F2: old token names in use

`index.css` is 89 lines: one `@theme` with 12 colours, a `nav` breakpoint
(`56.25rem`), three radii and two font stacks; a global `:focus-visible` outline
in `--color-accent`; the `prefers-reduced-motion` block; four `rg-*` keyframes
and four `animate-*` utilities.

Uses of the names that change, under `ui/web/src`:

| Old name  | Utility classes | `var(--color-*)` |
| --------- | --------------- | ---------------- |
| `accent`  | 33              | 7                |
| `panel`   | 20              | 2                |
| `panel-2` | 20              | 2                |
| `bad`     | 6               | 11               |
| `ok`      | 4               | 11               |
| `warn`    | 2               | 7                |
| Total     | 85              | 40               |

The 40 `var()` references sit in 16 files. `accent` is the largest group and the
one a blind rename gets wrong, so all 40 of its uses need classifying by hand.
The counts match the issue's estimate (about 90 and about 40).

### F3: Radix is confined to six files

`radix-ui` is imported only by
`components/ui/{command, detail-drawer, dialog, drawer, dropdown-menu, popover}.tsx`.
No view imports it directly. The only `data-state` in the source is the status
page's own attribute on the overall-status card (`views/status.tsx`), which is
not Radix's; test queries against Radix's `data-state` still need listing
(approach step 6).

### F4: eight view files use the table

`components/ui/data-table` is imported by `views/controls.tsx`, `findings.tsx`,
`findings/drawer.tsx`, `fleet.tsx`, `orgs.tsx`, `policy.tsx`, `remediations.tsx`
and `repositories.tsx`. `group-grid` is imported only by `views/orgs.tsx`.

### F5: install paths that need the token

- `ui/Dockerfile` runs `bun install --frozen-lockfile` twice (build stage and
  production-dependencies stage); both need the build secret.
- There is no `ui/.npmrc` today.
- Renovate is configured in `renovate.json5` at the repository root; it needs a
  host rule for `npm.pkg.github.com`.

### F6: what the package (0.1.1) does not provide

Compared against the package source at `v0.1.1` (`53685b7`), file by
file, with the call sites here. "Gap" means a call site, test or e2e
assertion here relies on something the package component cannot
express; renames and prop changes are not listed.

| # | Package module | What repo-guardian needs and the package lacks | Evidence here |
| - | -------------- | ---------------------------------------------- | ------------- |
| G1 | `blocks/data-table` | A table without list state. `search` and `onSearchChange` are required and the row count and pager always render; most of the 17 tables here are small tables inside cards with no search state. | `views/policy.tsx`, `controls.tsx`, `orgs.tsx`, `fleet.tsx`, `repositories.tsx`, `findings/drawer.tsx` |
| G2 | `blocks/data-table` | Keyset paging. The pager is page numbers and server mode needs `total`; the list views here page by cursor with `LoadMore`. | `views/findings.tsx`, `remediations.tsx`, `repositories.tsx` |
| G3 | `blocks/data-table` | A row click that ignores clicks on a link or button inside the row, Space to activate, and a key handler that ignores keys from inside the row. | `components/ui/data-table.tsx` (`activate`, `keyActivate`) |
| G4 | `blocks/data-table` | An accessible name for the table, a column `width` and `className`, and no forced border and max-height wrapper. | every `aria-label` on a `DataTable` call |
| G5 | `shell/detail-panel` | A name other than the title. The dialog is always named by its title; here the title is the repository link and the name is "Finding: acme/web renovate". | `views/findings/drawer.tsx`, 17 e2e assertions |
| G6 | `blocks/details-grid` | A key and value list whose values wrap. `DetailsGrid` right-aligns and truncates the value. | `DetailKV` in `policy.tsx`, `repositories.tsx`, both drawers |
| G7 | `components/command` | `CommandDialog` passes nothing to its inner `Command`, so `shouldFilter={false}` (server-side search) and `loop` are unreachable. | `shell/CommandPalette.tsx` |
| G8 | `blocks/chart-card` | A `strip` slot between header and body that stays when the plot switches to the table. | `views/fleet.tsx` |
| G9 | `blocks/dist-bar` | A caller's accessible name, a thin bar (fixed `h-7`), zero-value segments skipped, a per-segment title. | `views/fleet.tsx`, `components/posture.tsx` |
| G10 | `blocks/stat-strip` | A second line under the value. `hint` is inline in a no-wrap value. | `views/fleet.tsx` |
| G11 | `blocks/kpi` | `DeltaText`, the `Delta` type and a `delta` prop. | `views/controls.tsx`, `fleet.tsx`, `lib/fleet.ts` |
| G12 | `components/card` | `Card` and `CardTitle` take no `render`, so a card cannot be a `<section>` with an `<h3>`; e2e finds cards by heading. | about 40 `Card` call sites |
| G13 | `components/popover` | Menu-like popover rows: a heading, an item with `current`, `detail` and `disabled`, a separator. | `filters/FilterChips.tsx`, `page/OrgScope.tsx` |
| G14 | `lib/problem`, `blocks/error-state` | The API's `request_id` (the schema reads `requestId`), a view showing status, title and detail, and an error boundary. | `components/problem.tsx`, `api/client.ts` |
| G15 | `styles` | Reduced motion for the components' own animations: only the duration tokens are zeroed, while the components use fixed `duration-*` and `animate-in`/`animate-out`. Whether `tw-animate-css` handles it itself is not checked. | `index.css` |
| G16 | `components/sidebar` | A configurable collapse breakpoint (fixed at 768px in `hooks/use-mobile`; 900px here). | `index.css` `--breakpoint-nav` |

Not gaps, corrected from the issue:

- The package's `globals.css` has the global `:focus-visible` outline
  (2px, `--ring`, offset 2px), the same rule as here.
- `charts/tooltip.tsx` is `ChartTip` in `blocks/chart-card`;
  `SIDE_BY_SIDE` is `DETAIL_INLINE_QUERY`; `icons.tsx`, `Loading`,
  `Empty`, the options-driven `Tabs`/`SearchTabs` and `RangeTabs`
  stay local.

Visible differences for the design doc to accept or send back:

- Dark `destructive` is about `#f7756e` in the package, `#f2554d`
  here. The other eleven colours are within two units per channel.
- The package shell is the full-height collapsible sidebar (16rem)
  with the top bar beside it; here the top bar spans the page over a
  220px sidebar.
- Sheets slide for 500ms in and 300ms out; DESIGN-0036 says 200ms.
- `TimeZoneToggle` is a toggle group of pressed buttons, not a radio
  group.

Test-harness note: `web/testing/dom.ts` must preload Base UI and cmdk
in place of `radix-ui`; both decide at module load whether a document
exists.

### F7: upstream decisions, and 0.2.0

F6 went upstream as
[design-system#11](https://github.com/donaldgifford/design-system/issues/11)
(29 gaps, 22 differences, 5 call-site notes). The rule applied there:
a gap or bug in a component is fixed in the package; a design
difference is the package's design and repo-guardian adopts it.
**0.2.0** carries every "package" decision. Each one below was
checked in the source at `v0.2.0` (`11daf7f`); none was rendered or
installed here.

| F6 | Decision | In 0.2.0 |
| -- | -------- | -------- |
| G1 | package | `DataTable` `search`/`onSearchChange` optional; without them a plain table |
| G2 | package | `DataTable` `manual`: server-driven without `total`, no pager; `LoadMore` goes under it |
| G3 | package | clicks and keys from controls inside a row are ignored; Space activates |
| G4 | package | `label`, column `meta.className`, `bare` (no border, no scroll cap) |
| G5 | package | `DetailPanel` `label` in both forms; `subject` refocuses an open inline panel |
| G6 | package | `DetailsGrid` `layout="list"` |
| G7 | package | `CommandDialog` `commandProps` |
| G8 | package | `ChartCard` `strip`; its title is a heading (`headingLevel`, default 3) |
| G9 | package | `DistBar` `label`, `size="sm"`, `legend={false}`, empty segments skipped, segment titles. The name is `<label>: <summary>` |
| G10 | package | `StatStrip` `sub` |
| G11 | local | keep `DeltaText`, pass it as `Kpi`'s `hint` |
| G12 | package and local | `Card` and `CardTitle` take `render`; the `title`/`subtitle`/`right` API stays a local wrapper on the card parts |
| G13 | local | popover option rows stay here for now |
| G14 | package and local | `request_id` is read; `ErrorState` heads a problem with status and title; `ErrorBoundary` stays local |
| G15 | package | `globals.css` ships the blanket reduced-motion rule; the block in `index.css` can go |
| G16 | accept | the 768px breakpoint comes with the package shell |

Design differences, decided:

- **Accepted, the UI will look different:** the package shell (the
  full-height collapsible sidebar with identity and user in it, the top
  bar beside it, collapse at 768px, nav counts in `SidebarMenuBadge`),
  and the dark destructive red (`#f7756e`; ours is under 4.5:1 on
  `panel-2`). The visual re-baseline has to be planned around the
  shell.
- **Changed in the package:** sheets move in 200ms; `PageHeader`
  actions wrap; `trendTable` takes its head and noun; `ExportButton`
  has a separate `filename`.
- **Accepted as call-site or test changes:** the sheet widths and
  close button, the time-zone toggle group, `Button`'s default
  variant, the popover defaults, the breadcrumb leaf, and the smaller
  markup and attribute changes.

Stays local by decision: `DeltaText`, the card convenience wrapper,
popover option rows, `ErrorBoundary`, inline `Loading`/`Empty`, the
options-driven `Tabs`/`SearchTabs`, the drawer header composed from
the sheet parts.

Upgrade notes from upstream that matter here: 0.2.0 is the floor, not
0.1.1; `render={<h3 />}` may trip a heading-has-content lint rule
(use the function form); a bun cache holding an older, unrelated
package of the same name and version installs a `dist/` with no
`src/`, so clear that cache entry if it appears.

### F8: the install spike

Run in a scratch worktree off `main` (`fe48e6c`) with the package at
0.2.0, then discarded. The token was `gh auth token`, which carries
`read:packages`.

| Step | Result |
| ---- | ------ |
| `ui/.npmrc` (scope registry and `_authToken=${NPM_TOKEN}`), `bun add --exact` | installs; the package arrives as `src/`, not `dist/` |
| `bun run typecheck` with a probe importing all 76 modules | passes under `strict`, `noUncheckedIndexedAccess`, `exactOptionalPropertyTypes`, `verbatimModuleSyntax` |
| `index.css` reduced to `@import "@donaldgifford/design-system/globals.css"`, the `@fontsource` imports removed from `main.tsx`, `bun run build` | builds; the same five font faces are emitted as files |
| `bun run check:dist` | "dist/ is CSP-clean"; no `data:` in the CSS |
| `docker buildx build --secret id=NPM_TOKEN,env=NPM_TOKEN` | builds |

What the spike settled:

- **Make it a `devDependency`.** Vite compiles the package into the
  bundle and the BFF never imports it. As a dev dependency the
  Dockerfile's second stage (`bun install --frozen-lockfile
  --production`) skips it and needs neither the token nor `.npmrc`;
  only the build stage does. Verified by building that way.
- **The Dockerfile's `# syntax=docker/dockerfile:1.7` has no
  `env=` on a secret mount.** `RUN --mount=type=secret,id=NPM_TOKEN
  NPM_TOKEN="$(cat /run/secrets/NPM_TOKEN)" bun install
  --frozen-lockfile` works as it is; the other way is to raise the
  syntax version. The build stage must also `COPY .npmrc`. The token
  does not appear in the image history.
- **Two copies of Recharts.** The package pins 3.8.0 and installs it
  nested beside our 3.10.1. Once the local charts are gone nothing
  here imports Recharts, so dropping our direct dependency removes the
  duplicate; until then both are bundled.
- **Bundle size, upper bound.** With every module imported the JS is
  1,761 kB (518 kB gzip) against 989 kB (295 kB) on `main`, and the
  CSS 108 kB against 31 kB. This is before any local component is
  deleted and includes modules the app will never import.

Not run: CI with `GITHUB_TOKEN` and `packages: read`, the multi-arch
bake targets (`ui-ci`, `ui-release`) with the secret, and Renovate's
host rule. Those need a pushed branch.

### F9: dependencies and licences

New in the lockfile: `@base-ui/react` 1.9.0, `@base-ui/utils` 0.5.0,
`@tanstack/react-table` 9.3.1 and `@tanstack/table-core` 9.3.1 (with
`@tanstack/react-store` and `@tanstack/store`), `cmdk` 1.1.1, `sonner`
2.0.8, `tw-animate-css` 1.4.0, `zod` 4.6.7, a nested `recharts` 3.8.0
(with its `immer` and `reselect`). All MIT; the package itself is
Apache-2.0. `cmdk` still depends on `@radix-ui/react-dialog`, so
removing `radix-ui` from `package.json` does not remove every Radix
package from the tree.

`THIRD_PARTY_NOTICES.md` today covers the icon set and the fonts; the
fonts now arrive through the package. There is no licence-check
script in `ui/scripts` or the Makefile, so the issue's "licence check"
is the notices file alone.

### F10: the 0.2.0 `DataTable` against the list views

The three cursor-paged lists (Findings, Remediations, Repositories)
keep their state as they do now: hand-written `validateSearch`
parsers, filter chips, a closed `sort` enum chosen with tabs, an
infinite query and `LoadMore`, and `?open=` for the drawer. The 0.2.0
table carries that without binding to it:

```tsx
<DataTable manual bare label="Findings" columns={columns} data={items}
  getRowId={findingKey} selectedId={open} onRowClick={openFinding} compact={inline} />
<LoadMore ... />
```

With no `search` prop the table is unbound, so the routes do not have
to adopt `listSearchSchema` or its `page`/`size` params, and with
`manual` and no `total` the footer and pager do not render. The 14
embedded tables are the same call without `manual`.

One trap, found in 0.2.0 and fixed in **0.2.1**: an unbound `manual`
table rendered a sort button on any sortable column, which flipped its
arrow while the rows stayed put. Reported on design-system#11; 0.2.1
turns sorting off when the table is `manual` and has no
`onSearchChange` (`enableSorting: !(manual && onSearchChangeProp ===
undefined)`, read at `v0.2.1`, `2328b3b`), so the call above needs no
per-column setting. Embedded tables, which are not `manual`, gain a
working client-side header sort; whether that is wanted is a design
decision.

Call-site changes, not gaps: `Column<T>` becomes
`dataTableColumns<T>()` definitions; `rowKey`/`selectedKey`/`onActivate`
become `getRowId`/`selectedId`/`onRowClick`; the focus-return lookup
`tr[data-key=…]` becomes `tr[data-row-id=…]`.

### Handed forward

- To the design doc: the fate of each `index.css` block (the spike
  shows `globals.css` alone builds; the `nav` breakpoint goes with the
  package shell), light theme or dark only, whether embedded tables
  sort, the accepted visual differences in F7, and the CI, bake and
  Renovate wiring F8 did not run.
- To the implementation plan: classifying the 40 `accent` uses (F2),
  and the inventory of unit tests and visual baselines that move or
  change.

<!--docz:findings:end-->

<!--docz:conclusion:start-->

## Conclusion

**Answer:** Yes. With `@donaldgifford/design-system` 0.2.1 the UI can
drop its local tokens, `components/ui`, `components/charts`, the shell
and `radix-ui`, and nothing found blocks it.

- The package installs from GitHub Packages, typechecks under this
  repo's flags, builds, passes `check:dist` and builds in Docker (F8).
- Every gap found against 0.1.1 was decided upstream and the "package"
  ones are in 0.2.0 (F6, F7). Six small things stay local by decision:
  `DeltaText`, the card convenience wrapper, popover option rows,
  `ErrorBoundary`, inline `Loading`/`Empty`, and the tabs wrapper.
- The 0.2.0 `DataTable` carries both the embedded tables and the
  cursor-paged lists without changing how the routes hold their
  search state (F10).
- The UI will not look the same, by upstream decision: it takes the
  package shell and the package's destructive red (F7). The issue's
  "looks the same" condition has to be read with those two accepted.

The hypothesis held except in one place: it expected `DataTable` to
need an adapter or to be the hard part. After 0.2.0 it is a call-site
change.

<!--docz:conclusion:end-->

<!--docz:recommendation:start-->

## Recommendation

Write the design doc for the migration against 0.2.1 (the design and
IMPL-0037 later moved the pin to 0.3.0, which adds only `DataTable`'s
`sortable` prop), starting from
the issue's seven stages with these changes:

1. Pin `@donaldgifford/design-system` exactly, as a `devDependency`,
   and wire the token into the build stage only (F8).
2. Plan the shell swap and the visual re-baseline together, since the
   shell is the largest accepted difference (F7).
3. Treat `DataTable` as a call-site migration in two passes, embedded
   tables then the three lists, and decide there whether embedded
   tables sort (F10).
4. Drop the direct `recharts` dependency in the stage that deletes
   `components/charts` (F8).
5. Keep the six local pieces named in the conclusion, each built on
   the package's parts.

Report anything wrong found in 0.2.0 during the work as an issue on
`donaldgifford/design-system`, not as a patch here.

<!--docz:recommendation:end-->

<!--docz:references:start-->

## References

- Issue [#252](https://github.com/donaldgifford/repo-guardian/issues/252), the
  brief
- [`donaldgifford/design-system`](https://github.com/donaldgifford/design-system):
  README, RFC-0001, ADR-0001 to ADR-0004, IMPL-0001 (phase 5 is this migration)
- [design-system#11](https://github.com/donaldgifford/design-system/issues/11),
  the gaps and differences in F6 sent upstream for a decision per item
- [Storybook catalog](https://donaldgifford.github.io/design-system/)
- [DESIGN-0036](../design/0036-frontend-design-cleanup-and-refresh-for-the-v2-ui.md)
  and [IMPL-0031](../impl/0031-frontend-design-refresh-for-the-v2-ui.md), the
  UI's design language
- [DESIGN-0037](../design/0037-controls-ui-org-tree-findings-fleet-panels-and-status-changelog.md)
  and
  [IMPL-0032](../impl/0032-controls-ui-findings-org-tree-fleet-panels-changelog-and-rc11.md),
  the controls pages
- [INV-0024](0024-icon-set-for-the-ui.md), Tabler icons

<!--docz:references:end-->
