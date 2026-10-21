---
id: INV-0024
title: "Icon set for the UI"
status: Concluded
author: Donald Gifford
created: 2026-10-21
---

<!-- markdownlint-disable-file MD025 MD041 -->

# INV-0024: Icon set for the UI

<!--toc:start-->
<!-- markdownlint-capture -->
<!-- markdownlint-disable MD013 -->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [F1: devl.dev uses Lucide, which is ISC](#f1-devldev-uses-lucide-which-is-isc)
  - [F2: Tabler is the closest MIT set](#f2-tabler-is-the-closest-mit-set)
  - [F3: the mapping is one to one](#f3-the-mapping-is-one-to-one)
  - [F4: the look barely moves](#f4-the-look-barely-moves)
  - [F5: MIT still asks for one notice](#f5-mit-still-asks-for-one-notice)
  - [F6: the cost is small](#f6-the-cost-is-small)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!-- markdownlint-restore -->
<!--toc:end-->

<!--docz:question:start-->
## Question

Should the UI replace its hand-drawn inline SVG icons with a maintained
icon set under MIT or Apache-2.0, which set, and what does the mockup
look like with it?

<!--docz:question:end-->

<!--docz:hypothesis:start-->
## Hypothesis

Yes. The hand-drawn set already uses the common outline grammar (24px
grid, no fill, `currentColor` stroke, round caps and joins), so a set
in that style should barely change the look. It would remove the need
to draw every new icon by hand as remediation adds states (pull
requests, holds, conflicts, permissions).

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

**Constraint (2026-10-21):** the set must be MIT or Apache-2.0 licensed.
Lucide, the first candidate, is ISC and is ruled out.

<!--docz:context:end-->

<!--docz:approach:start-->
## Approach

1. Check what devl.dev uses, from its served HTML.
2. Shortlist maintained outline sets under MIT or Apache-2.0 and pick the
   one closest to the current look.
3. Map each mockup `ICON` key to the nearest icon in that set, preferring
   a name that matches its label where one exists.
4. Draw the mockup with that set's SVG bodies at one pinned version,
   keep the old map as `ICON_CUSTOM`, and add a switch for set
   (`?icons=custom`) and stroke (`?stroke=2|1.8`) so the two can be
   compared in place.
5. Check what the licence asks the repository to ship.

<!--docz:approach:end-->

<!--docz:environment:start-->
## Environment

| Component | Version / Value |
| --------- | --------------- |
| `@tabler/icons` (mockup SVG bodies) | 3.49.0, MIT |
| `@tabler/icons-react` (proposed for the SPA) | 3.49.0, MIT |
| Mockup | `ui/design/repo-guardian-mockup.html` |

<!--docz:environment:end-->

<!--docz:findings:start-->
## Findings

### F1: devl.dev uses Lucide, which is ISC

Every UI icon on devl.dev carries lucide-react's
`class="lucide lucide-<name>"` marker: `layout-grid`, `rows-3`, `search`,
`shuffle`, `sun` and `arrow-up-right`, all at stroke 2 and 12–16px. Its
other 18 SVGs are custom card illustrations. Lucide is ISC, outside the
MIT/Apache-2.0 constraint, so the look is the reference but the set is
not a candidate.

### F2: Tabler is the closest MIT set

| Set | Licence | Size | Fit |
| --- | --- | --- | --- |
| **Tabler Icons** | MIT | ~5,000 outline | 24px grid, stroke 2, round caps and joins, `currentColor`: the same grammar as today and as Lucide. Actively maintained, with a tree-shaken React package (`@tabler/icons-react`). |
| Feather | MIT | ~290 | The closest shapes (Lucide forked from it), but unmaintained since 2021 and too small for remediation's states. |
| Heroicons | MIT | ~300 | Smaller, and its 1.5 stroke and geometry read differently. |
| Phosphor | MIT | ~1,500 × 6 weights | Good, but its geometry moves further from the mockup. |
| Iconoir | MIT | ~1,600 | Stroke 1.5, thinner than today's 1.8. |
| Material Symbols | Apache-2.0 | ~3,000 | A Google look, variable-font first; a different visual language. |

### F3: the mapping is one to one

| Mockup key | Used for | Tabler icon |
| --- | --- | --- |
| `grid` | Fleet | `layout-grid` |
| `list` | Controls | `list-check` |
| `boxes` | Orgs | `building` |
| `activity` | Findings | `activity` |
| `wrench` | Remediations | `tool` |
| `repo` | Repositories | `folder-code` |
| `chart` | History | `chart-line` |
| `file` | Policy | `file-text` |
| `check` | Status page | `circle-check` |
| `book` | Documentation | `book` |
| `shield` | brand mark | `shield-check` |
| `search` | Search | `search` |
| `clock` | time zone | `clock` |
| `ext` | outbound links | `arrow-up-right` |

The SPA's `menu` maps to `menu-2`, and `git-pull-request` is on hand for
the remediation views. Controls takes `list-check` and Orgs `building`,
names that fit their labels better than a straight copy of today's
shapes. Each Tabler SVG opens with an invisible
`<path stroke="none" d="M0 0h24v24H0z"/>` bounding box; the mockup drops
it, and `@tabler/icons-react` renders it harmlessly.

### F4: the look barely moves

Side by side at 1440×900, Tabler and the current set read alike: same
size, colour and weight class. Tabler's shapes are slightly more detailed
(rounded grid cells, the check inside a circle, a shield with a check).
At stroke 2 they are a touch heavier than today's 1.8; `?stroke=1.8`
matches the old weight. `@tabler/icons-react` takes `stroke` per icon,
or once on the `Icon` wrapper.

### F5: MIT still asks for one notice

MIT requires its copyright and permission notice in all copies or
substantial portions of the software, the same as ISC. The built UI
image bundles Tabler's SVG paths, so the notice ships with it. That is
one entry in `ui/THIRD_PARTY_NOTICES.md` quoting Tabler's MIT licence
(Copyright (c) 2020-2026 Paweł Kuna). An Apache-2.0 set would ask for
the same, plus any NOTICE file it carries. MIT is compatible with the
project's Apache-2.0 licence.

### F6: the cost is small

`@tabler/icons-react` exports one component per icon and is tree-shaken,
so only imported icons ship, each well under 1KB. The icons are inline
SVG components with no icon font and no runtime fetch, so the UI's CSP
and the `dist/` CSP check are unaffected.

<!--docz:findings:end-->

<!--docz:conclusion:start-->
## Conclusion

**Answer:** Yes, with Tabler Icons (MIT) at stroke 2. It matches the
current look and devl.dev's, covers every icon the UI uses today with
room for remediation's states, and needs only one notice entry.

**Decided 2026-10-21:** Tabler, stroke 2. The two strokes were
indistinguishable side by side, so the set's default wins: it is what
`@tabler/icons-react` renders with no override. The mockup now draws
only Tabler at stroke 2; the comparison switch and the hand-drawn
`ICON_CUSTOM` map are gone.

<!--docz:conclusion:end-->

<!--docz:recommendation:start-->
## Recommendation

A small IMPL task for the SPA:

1. Add `@tabler/icons-react` to `ui/package.json`.
2. Turn the `icons` map in `ui/web/src/components/icons.tsx` into
   name → Tabler component behind the same `<Icon name>` API, using the
   mapping in F3 plus `menu` → `menu-2`. Callers and the `aria-hidden`
   handling stay as they are; the stroke is Tabler's default 2.
3. Add Tabler's MIT notice to `ui/THIRD_PARTY_NOTICES.md`.
4. Replace the comment that cites DESIGN-0036 OQ4 with a pointer to this
   investigation.
5. Re-render `status.png` and any screenshots in the pinned Playwright
   image.

<!--docz:recommendation:end-->

<!--docz:references:start-->
## References

- DESIGN-0036: frontend design cleanup and refresh (no icon decision)
- `ui/web/src/components/icons.tsx`
- `ui/design/repo-guardian-mockup.html`
- <https://tabler.io/icons>, licence: <https://github.com/tabler/tabler-icons/blob/main/LICENSE>
- <https://www.devl.dev/> (the look reference; Lucide, ISC)

<!--docz:references:end-->
