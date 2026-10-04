---
id: DESIGN-0034
title: "CODEOWNERS ownership control"
status: Draft
author: Donald Gifford
created: 2026-10-04
---

<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN-0034: CODEOWNERS ownership control

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [The type, extended](#the-type-extended)
  - [The .wiz example, corrected](#the-wiz-example-corrected)
  - [Reader surface](#reader-surface)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Decisions](#decisions)
- [Review findings carried from DESIGN-0031](#review-findings-carried-from-design-0031)
- [Open Questions](#open-questions)
  - [OQ1: What does "pattern is owned by owners" mean for CODEOWNERS?](#oq1-what-does-pattern-is-owned-by-owners-mean-for-codeowners)
- [References](#references)
<!--toc:end-->

> **Deferred, not decided (2026-10-04).** This document holds the in-depth CODEOWNERS design that DESIGN-0031 carried until 2026-10-04: ownership rules, effective ownership under GitHub's last-match-wins, the CODEOWNERS errors endpoint. The maintainer deferred all of it. This version of repo-guardian's `codeowners` control is existence plus template (DESIGN-0031 D11), exactly v1's behaviour, because repo-guardian should not be the first option used for ownership enforcement. Everything below is provisional: carried over with the adversarial-review corrections applied, and open to re-design when a team needs it. Nothing here is an input to the IMPL plan for the controls model.

## Overview

A CODEOWNERS control that can say "every `.wiz` file in this repository is owned by exactly these two teams, and nothing else about the file changes". It extends the existence-only `codeowners` control of DESIGN-0031 with rule kinds that read the file's entries, evaluate who GitHub would actually request for a path, and edit the file minimally so a required line is the last match for the paths it covers. It is deferred: the requirement exists (it motivated the controls model), but repo-guardian will not be the first tool to carry it, and the semantics need the spike described under Migration before they are decided.

## Goals and Non-Goals

### Goals

- Express "`pattern` is owned by `owners`" as a control rule whose pass/fail agrees with what GitHub does, not with a literal line.
- Remediate without breaking any other owner in the file: the control's own line is the last match for its paths, and no human line is reordered or edited.
- Report CODEOWNERS validity (unknown owners, bad patterns) from GitHub's own errors endpoint, never inferred from a local parser.
- Keep DESIGN-0031's guarantees: one owner per resource, remediation as a value, the conformance suite.

### Non-Goals

- Shipping before a team needs it. The existence-plus-template control covers today's fleet.
- A universal "specificity sort" of a CODEOWNERS file. There is none that preserves intentional exceptions (AR-0031-01); the design works with append-last and explicit conflicts instead.
- Managing CODEOWNERS on user-owned repositories with team owners. GitHub has no teams there; the control reports `not_applicable` (DESIGN-0031 D10).

## Background

The brief's motivating case is a large monorepo where only two GitHub teams may approve a change to any `.wiz` file, while every other team keeps ownership of its own directories. v1 cannot express it: a `codeowners` rule and a "CODEOWNERS contains the `.wiz` line" rule target the same file, and rules that share a file delete, overwrite or loop against each other (DESIGN-0029). The controls model was shaped so that one control owns CODEOWNERS outright and carries every requirement on it.

The first draft of DESIGN-0031 then got the mechanics wrong: it inserted a narrow pattern *before* a trailing `*` line, which under last-match-wins hands the path to the `*` owners (AR-0031-01, critical). The review correction, and the maintainer's `.wiz` example, produced the amended semantics below. On 2026-10-04 the maintainer deferred the whole ownership control rather than decide it now, and DESIGN-0031's `codeowners` dropped to `exists` plus template.

GitHub facts every part of this design rests on:

- **Last match wins.** Later lines override earlier ones for the paths they match. A `* @org/default` line at the top is the fallback; a `.wiz @org/security @org/platform` line after it wins for every `.wiz` path; a `*` at the bottom would negate everything above it.
- **A pattern without a leading slash matches at any depth**, as in gitignore, so `.wiz` already means "any `.wiz` file in the repository". Negation, escaping and character ranges are not supported.
- **A later, broader line takes paths back.** `/services/foo/ @org/foo` placed after the `.wiz` line owns `services/foo/.wiz`. The control's line must therefore be the last line that matches `.wiz` paths.
- **Any one owner on the matching line can approve** when "require review from code owners" is on, so an extra owner on the effective line is an extra approver. "Only these two teams" needs the effective owner set to be *exactly* the required set.
- **Patterns apply to paths that do not exist yet**, so a requirement on an absent path can be checked against the pattern alone.
- **Teams exist only in organisations.** A personal-account policy names users (`@login`).

## Detailed Design

Provisional. Carried from DESIGN-0031 with AR-0031-01 and AR-0031-05 applied.

### The type, extended

`internal/controls/codeowners` grows from "locate and write" to "locate, parse, evaluate, edit":

| Aspect | Behaviour |
| ------ | --------- |
| Locations | `.github/CODEOWNERS`, `CODEOWNERS`, `docs/CODEOWNERS`; the first found is the one GitHub uses (DESIGN-0029 A21) |
| Resources | `file:.github/CODEOWNERS`, `file:CODEOWNERS`, `file:docs/CODEOWNERS` |
| Model | ordered entries `{pattern, owners, line}`, with comments and blank lines preserved byte-for-byte |
| Matcher | a local implementation of GitHub's gitignore-style patterns and last-match-wins, used only to compute the owners GitHub would request for a path; validated against GitHub-documented examples and, in the integration suite, against the errors endpoint on real repositories |
| Rule kind `exists` | as DESIGN-0031: a file is found and non-empty |
| Rule kind `valid` | GitHub's CODEOWNERS errors endpoint, called with the observed commit, reports no errors. `remediate = false`: unknown owners and bad patterns need a human. Unsupported endpoint → `unknown{reason=capability_unsupported}` with a note, never `pass`; permission error → `unknown{reason=permission}`; transient failure → `error`. The local parser additionally enforces GitHub's size limit and reports an oversized file as a definite failure. `valid` is declared per catalogue definition, so an instance without the endpoint omits it rather than carrying a permanent unknown |
| Rule kind `owners` | the owners GitHub would request for every path `pattern` covers are exactly `owners` (OQ1(a), undecided; (b) is the superset form) |
| Rule kind `default_owner` | `*` has an owner |
| Fix for `owners` | append the control's own `pattern owners…` line at the end of the file, or, if a line with exactly this pattern exists, rewrite its owners to the required set and move it to the end when a later line would override it. Never reorder or edit a human's lines. A later human line that *specifically* targets covered paths (a pattern equal to or narrower than ours that matches nothing else, or an ownerless exception) is a deliberate conflict: the rule fails with a note naming the line and is non-remediable until a human resolves it |
| Fix for `default_owner` | write a `*` line **first** (or leave an existing one where it is), never last, so it cannot override the narrow lines written after it |
| Template | the operator's `codeowners` template, with its `*` line first and every required narrow line after it; the load-time template check (DESIGN-0031 D2) proves the template passes every remediable rule |

Why append-last is safe: a narrow pattern only ever matches its own paths, so moving the control's line to the end changes who owns `.wiz` files and nothing else. `@org/foo` keeps the rest of `services/foo/`. That is what "enforce these owners on this path without breaking the rest of the file" means.

### The `.wiz` example, corrected

- **No CODEOWNERS:** the template is written with its `*` line first and the `.wiz` line after it.
- **A team's CODEOWNERS without `.wiz`:** the `.wiz` line is appended at the end, after the team's `*` when there is one; the team's file is otherwise untouched.
- **A team later appends `/services/foo/ @org/foo`:** the next evaluation finds `services/foo/.wiz` owned by `@org/foo`; remediation moves the `.wiz` line back to the end. `@org/foo` keeps everything else under `services/foo/`.
- **A team adds `/services/foo/.wiz @org/foo`:** a specific conflict. The rule fails with a note naming that line; nothing is rewritten until a human decides.
- **`default_owner` and `owners` both failing:** the new `*` line goes first and the `.wiz` line after it, so the default owner cannot override the owners it was written next to.
- **An org that does not get the control:** it is never evaluated there (DESIGN-0030), so the file is never touched.

### Reader surface

`Reader.CodeownersErrors(ctx, ref)` takes the ref and is called with the observed commit, so validation is bound to the file that was read. A self-check on an uncommitted PR-head overlay cannot reach the endpoint; it uses the local parser only and its evidence says `validator = parser`.

## API / Interface Changes

- Catalogue: `rule "<id>" { kind = "owners"  pattern = "..."  owners = [...]  remediate = true }` and `kind = "default_owner"` on the `codeowners` type; `kind = "valid"` with `remediate = false`.
- `Reader` gains `CodeownersErrors(ctx, ref)` (DESIGN-0031's three-interface split, D7).
- No API or UI change beyond the rule results already modelled.

## Data Model

None beyond DESIGN-0032's rule results. Evidence for `owners` carries the effective owners found, the covered paths sampled and, on conflict, the overriding line number.

## Testing Strategy

Carried from DESIGN-0031's conformance suite, plus what AR-0031-01 and AR-0031-05 required:

- **Independent expected owners.** Fixture expectations come from a table of GitHub-documented examples, never from the type's own matcher, so the evaluator is checked against GitHub rather than against itself.
- **Ordering cases:** a trailing `*`, later overlapping wildcards, duplicate patterns, ownerless exceptions, and `default_owner` and `owners` failing together; the remediated file must pass both and leave untouched lines byte-identical.
- **Validity cases:** nonexistent and ineligible owners, an oversized file, an unsupported endpoint, and different main/PR CODEOWNERS contents; none may yield a validity pass solely because the local parser accepted the text.
- **Integration:** the local matcher compared against the errors endpoint on real repositories.

## Migration / Rollout Plan

1. Stay deferred until a team needs ownership enforcement in repo-guardian rather than in the tool that owns that workflow today.
2. Before deciding OQ1, spike the matcher against GitHub on real repositories (the integration case above) and confirm the "patterns apply to absent paths" and "any one owner approves" behaviours on a current GitHub version.
3. Ship as a new `codeowners` version in the catalogue (`codeowners@N+1` adds the rule kinds; DESIGN-0029 D3), trialled in one org through `replace` (DESIGN-0030) before the enterprise policy picks it up.

## Decisions

None. Everything in this document is provisional until it is picked up.

## Review findings carried from DESIGN-0031

The two findings below were raised against DESIGN-0031 on 2026-10-03 and accepted there; their responses are the semantics above. They stay in DESIGN-0031 as the record of what was reviewed, and are summarised here because this is where they now apply.

- **AR-0031-01 (critical): CODEOWNERS remediation orders the rules backwards.** The first draft inserted narrow patterns before a trailing `*`, which selects the `*` owners under last-match-wins. Accepted: a `*` line is written first or left in place, a required narrow line is appended last, `owners` is evaluated as effective ownership through a local matcher, conflicts with a human's specific exceptions fail with a note rather than being reordered, and fixture expectations come from GitHub-documented examples.
- **AR-0031-05 (high): parser fallback cannot prove CODEOWNERS validity.** Accepted: the parser fallback is removed, an unsupported endpoint yields `unknown`, validation is bound to the observed ref, the size limit is enforced locally, and PR-head self-checks are parser-only with evidence saying so.

## Open Questions

### OQ1: What does "`pattern` is owned by `owners`" mean for CODEOWNERS?

**Deferred.** Not decided; moved from DESIGN-0031 OQ1 on 2026-10-04 with its amended options. Decide it only when this design is picked up, after the spike in the Migration plan.

- (a) ✅ recommended: **effective ownership, exact set.** The owners GitHub would request for every path `pattern` covers are exactly `owners`: no missing owner, no extra approver. Remediation appends or moves the control's own line to the end, never reorders a human's lines, and fails with a note on a specific conflict. This is what "only these two teams can approve `.wiz` changes" requires.
- (b) Effective ownership, superset: every required owner is among the owners GitHub would request; extra owners are allowed. Weaker for the exclusivity case, since any extra owner can approve. Could be a per-rule relaxation of (a).
- (c) A literal line with exactly the pattern and owners. Rejected by AR-0031-01: a broader later line silently overrides it and the check still passes.
- (d) A literal line *and* effective ownership. Strict, but it fails files that are correct under (a) and written differently.
- other:

## References

- DESIGN-0029 (controls and policies), DESIGN-0030 (policy model), DESIGN-0031 (control framework; D11 defers this design), DESIGN-0032 (evaluation and remediation).
- `notes/2026-10-02-controls-and-policies-brief.md`, the original brief with the `.wiz` requirement.
- GitHub, [About code owners](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-code-owners): syntax, last-match-wins, size limit, the errors endpoint.
