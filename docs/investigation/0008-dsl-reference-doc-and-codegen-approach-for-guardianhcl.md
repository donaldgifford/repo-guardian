---
id: INV-0008
title: "DSL reference doc and codegen approach for guardian.hcl"
status: Open
author: Donald Gifford
created: 2026-05-31
---
<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0008: DSL reference doc and codegen approach for guardian.hcl

**Status:** Open
**Author:** Donald Gifford
**Date:** 2026-05-31

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [DSL Surface Inventory](#dsl-surface-inventory)
- [Codegen approaches](#codegen-approaches)
  - [Approach A: struct-tag reflection at build time](#approach-a-struct-tag-reflection-at-build-time)
  - [Approach B: go/ast walker over types.go](#approach-b-goast-walker-over-typesgo)
  - [Approach C: hand-written reference with golden test](#approach-c-hand-written-reference-with-golden-test)
  - [Approach D: hybrid — reflection-generated skeleton + hand-curated prose](#approach-d-hybrid--reflection-generated-skeleton--hand-curated-prose)
- [Comparison matrix](#comparison-matrix)
- [Tooling survey](#tooling-survey)
- [Recommendation](#recommendation)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Question

**Should the `guardian.hcl` DSL reference doc be auto-generated from
the Go source (struct tags + comments) or hand-written? If
auto-generated, what's the toolchain?**

Stakes: a hand-written doc rots silently the moment a new field is
added to `AssertionConfig`, `FileRuleConfig`, etc. — and operators
debugging configuration would have no way to know whether the doc or
the code is canonical. Auto-generation eliminates drift but is real
infrastructure work; getting the trade-off wrong costs either weeks
of doc-rot debugging or weeks of unproductive codegen scaffolding.

## Hypothesis

A **hybrid approach** (Approach D below) is the sweet spot: reflection
generates the field tables (type, HCL key, required, default), and a
hand-written prose layer wraps them with semantic context (when to use,
edge cases, interactions). The reflection layer is small (~100 LOC) and
catches the drift cases that matter; the prose layer is where the
real reference value lives.

Pure codegen (Approach A or B) would either be too rigid (no room for
the "why" prose operators actually need) or too elaborate (parsing
Go AST for doc comments is real work to make stable).

## Context

The HCL configuration language for `guardian.hcl` has accumulated
significant surface area across IMPL-0005 (file rules + check modes),
IMPL-0007 (assertions + ignore lists), IMPL-0008 (setting + branch
protection rules), IMPL-0009 (scope), IMPL-0012 (PR templates),
IMPL-0013 (auto_close_pr), and PR #92 (non_empty assertion). There is
no single-source-of-truth document. Operators currently consult:

- `examples/guardian-full.hcl` — example-shaped, not reference-shaped
- `docs/design/0006-*.md` — design rationale for the original engine,
  not current behavior
- Three other DESIGN docs for the later additions
- `internal/policy/types.go` — authoritative but Go-shaped

This was raised after a homelab debugging session
(2026-05-31) where confusion about assertion semantics (`pattern` vs
`yaml_path + contains` vs the new `non_empty`) cost ~30 minutes of
back-and-forth. A reference doc would have answered it in seconds.

**Triggered by:** PR #92 conversation about assertion semantics +
operator request for "what is all this config language even"

## DSL Surface Inventory

Surveyed `internal/policy/types.go` at HEAD (post-#92):

| Top-level block | Type | Count of fields | Source |
|---|---|---|---|
| `guardian {}` | `GuardianConfig` | 14 | `types.go:23` |
| `scope {}` (top-level) | `ScopeConfig` | 1 (`orgs`) | `types.go` |
| `ignore {}` (top-level + per-rule) | `IgnoreConfig` | 2 (`repos`, `forks`) | `types.go:200` |
| `defaults { pr {} templating {} }` | `DefaultsConfig` | 3 | `types.go` |
| `rule "file" "<name>" {}` | `FileRuleConfig` | ~15 | `types.go:74` |
| `rule "setting" "<name>" {}` | `SettingRuleConfig` | 4 | `types.go` |
| `rule "branch_protection" "<name>" {}` | `BranchProtectionRuleConfig` | 6+ | `types.go` |
| `assertion {}` (inside file rule) | `AssertionConfig` | 7 (incl. NonEmpty) | `types.go:188` |
| `reconcile "<type>" {}` (inside file rule) | `ReconcilerConfig` | varies by type | `types.go` |
| `pr {}` (defaults / rule / reconcile) | `PRConfig` | 5+ | `types.go` |
| `templating {}` | `TemplatingConfig` | 2 (`vars`, `strict`) | `types.go` |

Plus the cross-cutting concerns:

- **Check modes**: `exists` / `contains` / `exact` — distinct evaluation paths
- **Reconciler types**: `custom_properties` / `label_sync` / `branch_protection` / `workflow_sync` — each has its own option struct
- **Setting rule properties**: 8 supported (vulnerability_alerts_enabled, default_branch, has_issues, has_wiki, delete_branch_on_merge, allow_merge_commit, allow_squash_merge, allow_rebase_merge) — enumerated in code, not via struct fields
- **Templating vars**: `Common.{Owner,Repo}` + `Catalog.{Owner,Component,JiraProject,JiraLabel}` — defined in `internal/template/`, not `internal/policy/`
- **Config merge order**: built-in defaults → HCL file → env overrides
- **HCL block routing**: `decodeRuleOrSettingBlock` dispatches on first label

Conservative estimate: ~80-100 distinct fields/options across ~12
struct types, plus 4 enum-like dispatches (check mode, rule type,
reconciler type, setting property).

## Codegen approaches

### Approach A: struct-tag reflection at build time

A small Go program (`tools/dsl-docs/`) imports `internal/policy`,
uses `reflect.TypeOf(policy.AssertionConfig{})` to walk fields,
extracts `hcl:"..."` tags, and emits a markdown table per struct.

**What it captures**:

- Field name → HCL key mapping
- Field type (string / bool / int / nested-struct)
- `optional` / `block` / `label` modifiers from the HCL tag
- Whether a field is a slice (`[]` block syntax)

**What it doesn't capture**:

- Field semantics (what does `non_empty` actually do?)
- Default values (not in tags; would need a separate convention)
- Interactions between fields (e.g., "yaml_path requires one of
  contains/equals/non_empty")
- Examples
- Deprecated/replaced-by relationships

**Estimated effort**: ~150 LOC of Go + a `make dsl-docs` target.

### Approach B: go/ast walker over types.go

A Go program parses `internal/policy/types.go` via `go/parser` +
`go/ast` and extracts both struct tags AND doc comments above each
field.

**What it captures over Approach A**:

- Doc comments above fields (operators get short per-field prose)
- Const blocks for enums (check modes, reconciler types)

**What it doesn't capture**:

- Cross-field interaction rules (those live in `validate.go`, not
  in field doc comments)
- Examples (doc comments aren't a great place for multi-line YAML)
- Imported types (e.g., the per-reconciler option structs may live
  in different packages)

**Estimated effort**: ~400 LOC of Go (AST walking is verbose) +
discipline to put doc comments on every field. New fields without a
doc comment would emit a placeholder, which is a useful failure mode
(visible during code review).

### Approach C: hand-written reference with golden test

A pure markdown reference under `docs/reference/policy-dsl.md`,
checked against reality via a Go test:

```go
func TestDSLReference_NoOrphanFields(t *testing.T) {
    // Walk reflect.TypeOf(AssertionConfig{}), assert every HCL
    // tag also appears as a `<code>name</code>` token in the
    // markdown file.
}
```

**What it captures**:

- Everything — prose, examples, interactions, edge cases
- Operator-grade quality

**What it doesn't capture**:

- New fields automatically — they need a manual doc entry
- BUT the golden test fails when a new field appears without a doc
  entry, so drift is loudly caught at PR time

**Estimated effort**: ~600-800 lines of markdown for v1 + ~50 LOC
of golden test.

### Approach D: hybrid — reflection-generated skeleton + hand-curated prose

A `make dsl-docs` target uses reflection (Approach A) to generate a
"field reference" table per struct as a single auto-generated section
of the larger hand-written doc. The auto block is delimited by
`<!-- BEGIN AUTO -->` / `<!-- END AUTO -->` markers (same convention
docz already uses for section indices), and `make dsl-docs --check`
verifies the auto block matches reality.

**What it captures**: everything from C, plus drift-proof field
tables that automatically pick up new fields, plus the same golden
test as C for orphan-field detection.

**Estimated effort**: ~150 LOC Go + ~600 lines markdown + ~30 LOC
golden test = ~700-800 LOC total. Same effort as Approach C plus
the codegen layer.

## Comparison matrix

| Criterion | A: reflect-only | B: ast-only | C: hand + golden | D: hybrid |
|---|---|---|---|---|
| Captures prose ("when to use") | No | Partial | Full | Full |
| Drift-proof on new fields | Yes | Yes | Detect-only via golden | Yes (auto block) |
| Captures cross-field rules | No | No | Yes | Yes |
| Captures examples | No | No | Yes | Yes |
| Effort (LOC) | ~150 | ~400 | ~700 | ~800 |
| Maintenance burden per new field | Zero | Zero (if doc-commented) | One doc edit | One prose edit (auto table refreshes) |
| Operator-facing quality | Low | Medium | High | High |

## Tooling survey

Things I checked for "is there already a Go tool for this":

- **`hclwrite` / `hclparse`**: HashiCorp's libs for reading/writing
  HCL. Not docs-oriented.
- **`govalidate-doc`, `hcldoc`**: searches turned up nothing
  comparable to e.g. `protoc-gen-doc` for protobuf or `kustomize
  schema` for Kustomize. The HCL ecosystem (Terraform mostly) leans
  on hand-written reference docs.
- **`reflect-walk` (mitchellh)**: useful primitive for Approach A but
  doesn't generate docs.
- **`structtag` (fatih)**: parses struct tags into named fields; would
  cut some boilerplate in Approach A by ~30%.
- **helm-docs**: precedent in this repo — uses reflection-via-text
  (parses values.yaml comments) + a `.gotmpl` skeleton, exact analog
  to Approach D for a different DSL. Could literally copy the
  pattern.

The helm-docs precedent is meaningful: we already maintain a chart
README via Approach D-style scaffolding (template skeleton +
generated value table from struct-tag-equivalent comments). The team
already understands and maintains that workflow.

## Recommendation

**Approach D (hybrid).** Reasoning:

- Operators need the prose layer that Approaches A/B can't provide.
- We already accept the maintenance pattern (helm-docs uses the
  exact same generated-block-inside-hand-written-doc shape).
- Pure hand-written (Approach C) is fragile in a project that's
  still adding DSL surface every few PRs (#92 just added
  `non_empty`; INV-0007's GitLab work will probably add forge-typed
  rules).
- The codegen layer is small (~150 LOC) and the golden test
  prevents the silent-drift failure mode the project has hit before
  (CLAUDE.md notes the IMPL-0012 README regeneration post-mortem).

**Phased rollout** if approved:

1. **Phase 1**: Draft `docs/reference/policy-dsl.md` hand-written
   v1 covering top-level blocks, file rules, assertions, scope/
   ignore, config merge order, and 4-5 cookbook recipes. Ship to
   close the "operators have no reference today" gap.
2. **Phase 2**: Add `tools/dsl-docs/main.go` reflection-walker
   generating field tables for each struct. Insert as
   `<!-- BEGIN AUTO --> ... <!-- END AUTO -->` blocks into the
   Phase 1 doc.
3. **Phase 3**: Add `make dsl-docs` target + Makefile `check` to CI
   that fails the build if the auto block is stale. Add a tiny
   golden test that asserts every HCL struct tag has a corresponding
   `<code>name</code>` token in the prose layer (prevents new fields
   landing without operator-facing prose).

Phase 1 alone gets the immediate value; Phases 2-3 lock in the
no-drift property over time. Could ship Phase 1 in a single PR,
Phases 2-3 in a follow-up.

## Open Questions

<!-- Letter-keyed: (a) is my recommendation; (b)+ are alternatives;
     `other:` is blank for free-form input. -->

**Q1: Phasing.**

- **(a)** Ship Phase 1 (hand-written v1) in a single PR. Phases 2-3
  (codegen + golden test) as a follow-up PR after we've used the
  doc for ~2 weeks and validated the section structure.
- **(b)** Ship Phases 1+2 together — codegen lands alongside the
  initial doc. Heavier first PR.
- **(c)** Ship Phases 1+2+3 together as a single comprehensive PR.
- **other:** _________

**Q2: Doc location.**

- **(a)** `docs/reference/policy-dsl.md` — new `reference/` subdir,
  clear category distinct from `operations/` and `design/`.
- **(b)** `docs/policy-dsl.md` — flat docs root, like the existing
  `docs/ADDING_RULES.md`.
- **(c)** `charts/repo-guardian/docs/policy-dsl.md` — alongside
  homelab-smoke and other operator-facing chart docs.
- **other:** _________

**Q3: Codegen language for Phase 2.**

- **(a)** Plain Go program at `tools/dsl-docs/main.go`. Uses
  `reflect` over imported `internal/policy` types. Same language
  as the rest of the project; runs under `go run`. Helm-docs
  precedent.
- **(b)** A `go/ast` walker that parses `internal/policy/types.go`
  as source. More features (doc comments) but ~2.5× the LOC.
- **(c)** External tool: vendor a third-party doc generator if one
  emerges. Defer Phase 2 until that exists.
- **other:** _________

**Q4: Auto-block delimiter convention.**

- **(a)** `<!-- BEGIN AUTO: field-table-<struct-name> --> ... <!--
  END AUTO: field-table-<struct-name> -->` — explicit named blocks,
  one per struct. Mirrors docz's section-index markers.
- **(b)** Single `<!-- BEGIN AUTO --> ... <!-- END AUTO -->` block
  containing every struct's table concatenated. Simpler regen,
  worse for hand-editing prose between tables.
- **(c)** No HTML-comment delimiters; mark auto sections with a
  markdown convention like `## Field reference (generated)`. Less
  robust to accidental hand-edits.
- **other:** _________

**Q5: What's in scope for Phase 1?**

- **(a)** Full inventory (12 sections per outline above) —
  top-level + guardian + scope/ignore + file rules + assertions +
  setting + branch protection + reconcilers + PR templates +
  templating + merge order + cookbook. ~700 lines.
- **(b)** File rules only — sections 1-5 + 8-10 + 12. Ship as v1,
  add the others later as follow-up PRs. ~400 lines.
- **(c)** Skip the cookbook section in Phase 1; ship as a separate
  "guardian.hcl cookbook" doc later.
- **other:** _________

**Q6: Golden test enforcement (Phase 3).**

- **(a)** Hard CI failure if any struct tag has no prose match.
  Forces every new DSL field to land with operator docs.
- **(b)** Warning-only — print missing prose tokens but don't fail
  CI. Devs can land docs in follow-up.
- **(c)** Skip the golden test entirely; rely on auto block
  regen-on-merge to catch drift.
- **other:** _________

**Q7: Examples policy.**

- **(a)** Every section has at least one runnable HCL snippet
  (compiles + loads without error). Adds a small integration test
  that loads each snippet via the policy package.
- **(b)** Examples are illustrative only, no test enforcement.
  Faster to write, can drift.
- **(c)** No inline examples; cross-link to
  `examples/guardian-full.hcl` for everything. Cleanest doc, worst
  scannable-reference UX.
- **other:** _________

**Q8: Naming.**

- **(a)** "guardian.hcl reference" (file name, prose, intra-doc
  links). Concrete, matches operator vocabulary.
- **(b)** "Policy DSL reference" (current proposed file name).
  Generic; matches the package name (`internal/policy`).
- **(c)** "repo-guardian configuration reference". Most explicit;
  longest.
- **other:** _________

## References

- `examples/guardian-full.hcl` — current annotated example
- `internal/policy/types.go` — HCL struct definitions
- `internal/policy/loader.go` — HCL decode dispatcher (includes
  blocks not in struct tags, e.g., assertion block decoded manually
  via `JustAttributes`)
- `internal/policy/validate.go` — cross-field interaction rules
  (where Approaches A and B fall short)
- [DESIGN-0006](../design/0006-hcl-policy-configuration-and-rule-engine.md) —
  original HCL engine rationale
- [DESIGN-0008](../design/0008-additional-rule-types-and-ignore-lists.md) —
  setting + branch_protection rules
- [DESIGN-0013](../design/0013-customizable-pr-templates-and-extensible-template-configmap.md)
  — PR template inheritance
- PR #92 — non_empty assertion (most recent DSL addition)
- helm-docs workflow in `make helm-docs` — precedent for
  hand-curated + auto-generated hybrid docs in this repo
