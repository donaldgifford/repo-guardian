---
id: IMPL-0020
title: "Pre-IMPL-0019 high-severity fixes (INV-0011 Group A High)"
status: Draft
author: Donald Gifford
created: 2026-07-23
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0020: Pre-IMPL-0019 high-severity fixes (INV-0011 Group A High)

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-07-23

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Background](#background)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: A1 — catalog parse-failure must not clear properties](#phase-1-a1--catalog-parse-failure-must-not-clear-properties)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: A2 — remove shell-injection from the generated workflow](#phase-2-a2--remove-shell-injection-from-the-generated-workflow)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Docs, migration, release](#phase-3-docs-migration-release)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Resolved Decisions](#resolved-decisions)
- [References](#references)
<!--toc:end-->

## Objective

Fix the two High-severity findings from
[INV-0011](../investigation/0011-tech-debt-cleanup-inventory-post-impl-0019.md)
(A1, A2) on the shipped IMPL-0017 custom-properties surface. Both are
independent of DESIGN-0020/IMPL-0019 and should land **before** that
feature work begins: A2 is a live command-injection vector in every
generated workflow PR, and A1 destroys operator data on a single
malformed commit. Neither blocks IMPL-0019 logically — this doc exists so
the fixes ship on their own clock rather than waiting behind a
multi-week feature.

**Implements:** INV-0011 findings A1, A2

## Scope

### In Scope

- `internal/catalog/catalog.go` — `Parse` returns an error on
  unparseable input instead of synthesizing destructive defaults.
- `internal/reconciler/custom_properties.go` — `Reconcile` skips (no
  GitHub write) on catalog parse failure.
- `internal/rules/templates/set-custom-properties.tmpl` — annotation
  values reach the shell via `env:` indirection, YAML-safe.
- `internal/metrics/metrics.go` — `catalog_parse_failed_total`.
- Tests, operator docs, migration note, release.

### Out of Scope

- The six Medium findings and all structural debt (A3–A8, B1–B4) — those
  are [IMPL-0021](0021-post-impl-0019-hardening-and-structural-cleanup-inv-0011-group.md).
- Any DESIGN-0020/IMPL-0019 feature code.
- Property-*name* charset validation (A6) — a separate Medium; note it is
  distinct from A2, which concerns runtime property *values*.

## Background

Both findings were verified on current `main` (`b3350ef`); see INV-0011
Group A for the full write-ups. Condensed:

- **A1** — `catalog.go.Parse` returns `defaults()`
  (Owner/Component = "Unclassified", empty `Extra`) for BOTH unparseable
  YAML and valid-but-non-Component entities. In API mode this is fed
  straight into full-state-sync, so a temporarily malformed
  `catalog-info.yaml` PATCHes `null` for every mapped property and
  overwrites Owner/Component. There is exactly one caller:
  `custom_properties.go:157` (`desired := catalog.Parse(content,
  r.annotationProps)`).
- **A2** — `set-custom-properties.tmpl` interpolates `.Catalog.Owner`,
  each `.Catalog.Properties` value, and `.Catalog.Component` directly
  inside single-quoted `gh api -f` arguments in the generated GitHub
  Actions workflow. Those values originate in the repo's
  `catalog-info.yaml` (attacker-controlled for a repo the app is
  installed on): a value like `x'$(command)'` achieves command execution
  under the workflow's `GITHUB_TOKEN` after the PR merges. API mode is
  unaffected — it sends values through the Go client
  (`SetCustomPropertyValues`), never a shell.

## Implementation Phases

Run `make fmt` + `make lint` after each task; commit per numbered task
with conventional commits.

---

### Phase 1: A1 — catalog parse-failure must not clear properties

Make "I could not understand the input" distinguishable from "the input
says these properties are empty", so the reconciler never treats an
error as destructive desired state.

#### Tasks

- [ ] 1.1 Change `catalog.Parse` to
      `Parse(content string, annotationProps map[string]string)
      (*Properties, error)`: return a wrapped error when
      `yaml.Unmarshal` fails. Non-Component-entity handling is an
      explicit decision — see Decision 1.
- [ ] 1.2 Update the sole caller `custom_properties.go.Reconcile:157`:
      on parse error, log `slog.Warn("catalog-info parse failed; skipping
      reconcile to avoid clearing properties", "err", err)`, increment
      `metrics.CatalogParseFailedTotal.WithLabelValues(owner)`, and
      return `nil` (skip — GitHub state untouched, retried next sweep).
- [ ] 1.3 Add `CatalogParseFailedTotal{org}` CounterVec to
      `internal/metrics/metrics.go` (reuse `labelOrg`).
- [ ] 1.4 Update `catalog` package tests: malformed YAML returns an
      error (not defaults); valid Component parses as today; the
      non-Component case per Decision 1.
- [ ] 1.5 Reconciler test: a malformed `catalog-info.yaml` in API mode
      issues zero `SetCustomPropertyValues` calls and increments the
      counter (stateful mock asserts no PATCH).

#### Success Criteria

- `make lint` and `make test` pass.
- A malformed `catalog-info.yaml` provably results in no custom-property
  write in API mode (was: clears every mapped property + sets
  Unclassified).
- Valid catalog-info parsing is behaviorally unchanged (existing
  reconciler tests pass without modification, save the signature update).

---

### Phase 2: A2 — remove shell-injection from the generated workflow

Repo-controlled values must never be shell-parsed. Render them into the
workflow's `env:` block (YAML-safe) and reference them as quoted shell
variables, which are passed literally and never re-evaluated.

#### Tasks

- [ ] 2.1 Rewrite `set-custom-properties.tmpl` so `Owner`, `Component`,
      and each `Properties` entry are emitted as workflow `env:` entries
      (e.g. `RG_PROP_Owner`, `RG_PROP_<name>`), then referenced in the
      `run:` script as `"$RG_PROP_Owner"` inside the `-f` arguments. The
      `${{ }}`-escape backtick convention still applies to the GHA
      expressions already in the file.
- [ ] 2.2 YAML-safe value emission: values render into `env:` such that a
      value containing a quote, `$`, newline, or `:` cannot break the
      workflow YAML or the shell (block scalar or a template helper that
      quotes/escapes — mechanism per Decision 2). A value that is
      empty still signals "clear" via the existing `-F
      'properties[][value]=null'` branch — the empty/clear distinction
      moves to checking the env var, not inline rendering.
- [ ] 2.3 Template parse + render tests
      (`internal/rules`/`internal/template`): a hostile value
      (`x'$(id)'`, embedded newline, `a: b`) renders to a workflow whose
      generated shell passes the literal string and whose YAML still
      parses. Assert the rendered output contains no unescaped
      interpolation of the value inside the `run:` block.
- [ ] 2.4 Confirm strict-template validation (`ValidateZero`) still
      passes for the rewritten template context (no new required
      `CatalogInfo` fields introduced).

#### Success Criteria

- `make lint` and `make test` pass.
- A crafted annotation value that previously produced command
  substitution now round-trips as an inert literal (regression test
  proves the injection is closed).
- The generated workflow YAML remains valid for values containing
  quotes, `$`, `:`, and newlines.

---

### Phase 3: Docs, migration, release

#### Tasks

- [ ] 3.1 `docs/usage/policy-reference.md` § `custom_properties`: note
      that a malformed `catalog-info.yaml` is skipped (not cleared), and
      that generated-workflow values are passed literally.
- [ ] 3.2 Short migration/security note (release notes or a
      `docs/operations/*-migration.md` entry): describes the A2 fix as a
      security fix, recommends operators using GHA mode regenerate any
      open properties PRs (old branches carry the vulnerable workflow —
      see Decision 3).
- [ ] 3.3 CLAUDE.md: architecture note on the catalog parse-failure
      skip contract and the env-indirection template convention (so
      future template edits don't reintroduce inline interpolation).
- [ ] 3.4 `docz update impl`; flip this doc to Completed; mkdocs
      strict-mode warning count unchanged from the 14-file baseline.
- [ ] 3.5 Release: single `fix/` PR, `patch` semver label, appVersion
      bump verified against the real tag line (per the IMPL-0017
      post-mortem — Chart.yaml appVersion must equal the tag the label
      will cut from the latest real release).

#### Success Criteria

- `make ci` passes.
- Operator-facing docs describe both new behaviors; security note
  published.
- One `patch`-labeled PR merged; real release cut with matching
  appVersion.

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/catalog/catalog.go` | Modify | `Parse` returns `(*Properties, error)` |
| `internal/reconciler/custom_properties.go` | Modify | skip on parse error + counter |
| `internal/metrics/metrics.go` | Modify | `CatalogParseFailedTotal{org}` |
| `internal/rules/templates/set-custom-properties.tmpl` | Modify | env-indirection, YAML-safe values |
| `internal/catalog/catalog_test.go` | Modify | error-on-malformed tests |
| `internal/reconciler/custom_properties_test.go` | Modify | no-write-on-parse-fail test |
| `internal/rules/*_test.go` / `internal/template/*_test.go` | Modify | injection regression test |
| `docs/usage/policy-reference.md` | Modify | behavior notes |
| `docs/operations/*-migration.md` or release notes | Create | security note |
| `CLAUDE.md` | Modify | contracts |

No `github.Client` interface change ⇒ no mockClient-parity sweep.

## Testing Plan

- [ ] Phase 1: catalog parse-error returns error; reconciler issues zero
      writes on malformed input; valid-catalog behavior unchanged.
- [ ] Phase 2: injection regression (hostile value → inert literal);
      YAML validity for quote/`$`/`:`/newline values; strict-template
      validation still passes.
- [ ] `go test ./examples/...` still green (guardian-full.hcl uses the
      Jira map through this reconciler).
- [ ] Homelab smoke (operator-side, stays open until run): push a
      deliberately malformed catalog-info.yaml ⇒ no property change +
      counter increments; GHA-mode repo with a quote-bearing annotation
      value ⇒ generated workflow runs correctly and sets the literal.

## Dependencies

- None blocking. Builds on current `main` (post-IMPL-0017).
- Should merge before IMPL-0019 implementation begins (sequencing, not a
  code dependency).
- A1's parse-abort is a prerequisite for IMPL-0021's A3 fix
  (clear-on-file-removal is only safe to enable once malformed input can
  no longer masquerade as "clear everything").

## Resolved Decisions

All four open questions resolved 2026-07-23 with the recommended option (a).

1. **Non-Component valid YAML is skipped, not cleared.** `catalog.Parse`
   returns a sentinel (e.g. `(nil, ErrNotComponent)` or a `Parsed bool`)
   that the reconciler maps to "skip, do not clear." Only a valid
   Component entity is a positive statement of desired property state; a
   non-Component file is "not something we manage here," and clearing on
   it is the same data-loss class as the A1 parse-failure case. Practical
   effect: `Parse` distinguishes three outcomes — valid Component (sync),
   unparseable (error → skip), valid non-Component (skip, no clear).

2. **A2 fix = env-indirection + YAML-safe emission.** Values render into
   the workflow `env:` block in a YAML-safe form (single-quoted scalar
   with `'`-doubling, or a `|`-block scalar, via a small template helper)
   and are referenced as `"$RG_PROP_x"` in `run:`. Env indirection closes
   the shell-execution vector; YAML-safe emission closes the
   "value breaks the generated workflow file" vector. Both are required
   because the value is baked into a generated file, not passed at
   runtime.

3. **No automated remediation of already-open properties PRs.** Documented
   only. The GHA-mode PR-refresh gap is A4 (a separate Medium in
   IMPL-0021); building refresh here would pull that scope into a
   security patch that should stay small. The migration note tells
   operators to close/regenerate open properties PRs. Most deployments
   (incl. homelab) run API mode, where the vector never existed.

4. **One `fix/` PR for both A1 and A2, `patch` label.** Both High, both
   small, both on the same custom-properties surface — one PR, one
   release, one security note (INV-0003 precedent: investigation → single
   tight fix PR).

## References

- [INV-0011](../investigation/0011-tech-debt-cleanup-inventory-post-impl-0019.md) — source findings A1, A2 (verified on `b3350ef`)
- [IMPL-0021](0021-post-impl-0019-hardening-and-structural-cleanup-inv-0011-group.md) — the Medium + structural remainder (after IMPL-0019)
- [IMPL-0019](0019-absent-check-mode-and-conditional-file-rules.md) — the feature this doc sequences before; its A3 fix depends on Phase 1 here
- INV-0003 — precedent for investigation → small standalone fix PR
- IMPL-0017 — the custom-properties surface being fixed; appVersion-vs-real-tag release gotcha (task 3.5)
- Audited code (as of `b3350ef`): `internal/catalog/catalog.go:57`, `internal/reconciler/custom_properties.go:157`, `internal/rules/templates/set-custom-properties.tmpl:31`
