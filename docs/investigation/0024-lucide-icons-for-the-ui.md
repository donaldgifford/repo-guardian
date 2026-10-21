---
id: INV-0024
title: "Lucide icons for the UI"
status: Open
author: Donald Gifford
created: 2026-10-21
---

<!-- markdownlint-disable-file MD025 MD041 -->

# INV-0024: Lucide icons for the UI

<!--toc:start-->
<!-- markdownlint-capture -->
<!-- markdownlint-disable MD013 -->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [F1: devl.dev uses stock Lucide](#f1-devldev-uses-stock-lucide)
  - [F2: the mapping is one to one](#f2-the-mapping-is-one-to-one)
  - [F3: the look barely moves](#f3-the-look-barely-moves)
  - [F4: the licence asks for one notice](#f4-the-licence-asks-for-one-notice)
  - [F5: the cost is small](#f5-the-cost-is-small)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!-- markdownlint-restore -->
<!--toc:end-->

<!--docz:question:start-->
## Question

Should the UI replace its hand-drawn inline SVG icons with Lucide
(`lucide-react`), and what does the mockup look like if it does?

<!--docz:question:end-->

<!--docz:hypothesis:start-->
## Hypothesis

Yes. The hand-drawn set already follows Lucide's grammar (24px grid, no
fill, `currentColor` stroke, round caps and joins), so the swap should
barely change the look, cost a few KB, and remove the need to draw every
new icon by hand as remediation adds states (pull requests, holds,
conflicts, permissions).

<!--docz:hypothesis:end-->

<!--docz:context:start-->
## Context

**Triggered by:** a review of the UI's icons on 2026-10-21, and
<https://www.devl.dev/> as a reference for the target look.

The SPA draws 11 icons from `ui/web/src/components/icons.tsx` (`grid`,
`list`, `boxes`, `activity`, `check`, `book`, `shield`, `search`,
`clock`, `external`, `menu`) behind one `<Icon name>` component. The
mockup (`ui/design/repo-guardian-mockup.html`) has its own `ICON` map
with 14 keys. Both use stroke 1.8.

The `icons.tsx` comment says the inline set is "DESIGN-0036 OQ4". That
reference is wrong: DESIGN-0036's OQ4 is about where History and Policy
sit in the navigation, and the design never decides an icon approach.
The current set has no recorded decision behind it.

<!--docz:context:end-->

<!--docz:approach:start-->
## Approach

1. Check what devl.dev uses, from its served HTML.
2. Map each mockup `ICON` key to the nearest Lucide icon, preferring a
   name that matches its label where one exists.
3. Draw the mockup with `lucide-static` SVG bodies at one pinned version,
   keep the old map as `ICON_CUSTOM`, and add a switch for set
   (`?icons=custom|lucide`) and stroke (`?stroke=2|1.8`) so the two can
   be compared in place.
4. Check the licence terms and what the repository must ship.

<!--docz:approach:end-->

<!--docz:environment:start-->
## Environment

| Component | Version / Value |
| --------- | --------------- |
| `lucide-static` (mockup SVG bodies) | 1.61.0 |
| `lucide-react` (proposed for the SPA) | same release line |
| Mockup | `ui/design/repo-guardian-mockup.html`, branch `docs/inv-lucide-icons` |

<!--docz:environment:end-->

<!--docz:findings:start-->
## Findings

### F1: devl.dev uses stock Lucide

Every UI icon on devl.dev carries lucide-react's
`class="lucide lucide-<name>"` marker: `layout-grid`, `rows-3`, `search`,
`shuffle`, `sun` and `arrow-up-right`, all at Lucide's default stroke of
2 and sized 12–16px. Its other 18 SVGs are custom 80×60 card
illustrations, not icons.

### F2: the mapping is one to one

| Mockup key | Used for | Lucide icon |
| --- | --- | --- |
| `grid` | Fleet | `layout-grid` |
| `list` | Controls | `list-checks` |
| `boxes` | Orgs | `building-2` |
| `activity` | Findings | `activity` |
| `wrench` | Remediations | `wrench` |
| `repo` | Repositories | `folder-git-2` |
| `chart` | History | `chart-line` |
| `file` | Policy | `file-text` |
| `check` | Status page | `circle-check` |
| `book` | Documentation | `book-open` |
| `shield` | brand mark | `shield-check` |
| `search` | Search | `search` |
| `clock` | time zone | `clock` |
| `ext` | outbound links | `arrow-up-right` |

The SPA's `menu` maps to `menu`. Two keys get a better name than a
straight copy: Controls takes `list-checks` (a list of checks, not a
plain list) and Orgs takes `building-2` (an organisation, not a parcel).
`git-pull-request`, `circle-alert` and `scroll-text` are on hand for the
remediation views and as alternatives for Findings and Policy.

### F3: the look barely moves

Side by side at 1440×900, the two sets read alike: same size, colour and
weight class. Lucide's shapes are slightly more detailed (rounded grid
cells, the check inside a circle, a shield with a check). At stroke 2
they are a touch heavier than today's 1.8; `?stroke=1.8` matches the old
weight exactly. lucide-react takes `strokeWidth` per icon, or once on the
`Icon` wrapper.

### F4: the licence asks for one notice

Lucide is ISC. Its terms require the copyright and permission notice to
appear in all copies or substantial portions, which here means the
built UI image, since it bundles the SVG paths. That holds for any
distribution, not because repo-guardian is Apache-2.0: ISC is
compatible with Apache-2.0, and shipping the notice is enough. Lucide's
`LICENSE` also carries the MIT notice for icons derived from Feather.
`ui/THIRD_PARTY_NOTICES.md` already exists for this, so the change is
one entry quoting both notices.

### F5: the cost is small

lucide-react exports one component per icon and is tree-shaken, so
only imported icons ship, each well under 1KB. The icons are inline SVG
components with no icon font and no runtime fetch, so the UI's CSP and
the `dist/` CSP check are unaffected.

<!--docz:findings:end-->

<!--docz:conclusion:start-->
## Conclusion

**Answer:** Yes. Lucide matches the current look, covers every icon the
UI uses today with room for remediation's states, and needs only one
notice entry.

<!--docz:conclusion:end-->

<!--docz:recommendation:start-->
## Recommendation

1. Review the mockup on this branch: open
   `ui/design/repo-guardian-mockup.html` and use the switch in the
   bottom-right corner (Lucide or Custom, stroke 2 or 1.8). Decide the
   stroke: 2 (Lucide's default, as devl.dev uses it) or 1.8 (today's
   weight).
2. On acceptance, drop `ICON_CUSTOM` and the switch from the mockup and
   keep the chosen stroke.
3. Then a small IMPL task: add `lucide-react`, turn the `icons` map in
   `icons.tsx` into name → Lucide component behind the same `<Icon name>`
   API (callers and the `aria-hidden` handling unchanged), add the
   Lucide entry to `ui/THIRD_PARTY_NOTICES.md`, and fix the comment that
   cites DESIGN-0036 OQ4.

<!--docz:recommendation:end-->

<!--docz:references:start-->
## References

- DESIGN-0036: frontend design cleanup and refresh (no icon decision)
- `ui/web/src/components/icons.tsx`
- `ui/design/repo-guardian-mockup.html`
- <https://lucide.dev>, licence: <https://github.com/lucide-icons/lucide/blob/main/LICENSE>
- <https://www.devl.dev/>

<!--docz:references:end-->
