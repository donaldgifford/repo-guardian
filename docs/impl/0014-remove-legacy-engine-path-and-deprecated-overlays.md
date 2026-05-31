---
id: IMPL-0014
title: "Remove legacy engine path and deprecated overlays"
status: Implemented
author: Donald Gifford
created: 2026-05-30
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0014: Remove legacy engine path and deprecated overlays

**Status:** Implemented (2026-05-31 via PRs #85, #86, #87)
**Author:** Donald Gifford
**Date:** 2026-05-30

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Engine path collapse and internal/rules slim-down](#phase-1-engine-path-collapse-and-internalrules-slim-down)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Kustomize tree removal](#phase-2-kustomize-tree-removal)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Legacy docs purge](#phase-3-legacy-docs-purge)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Resolved Decisions](#resolved-decisions)
- [Dependencies](#dependencies)
- [References](#references)
<!--toc:end-->

## Objective

Delete three vestigial surfaces from the repo-guardian codebase
that are no longer used by the deployed binary, deployed chart, or
current docs pipeline: the `NewEngine` registry-based engine path,
the deprecated Kustomize tree, and the pre-docz legacy markdown
corpus. After this work, every code path is policy-driven, the
Helm chart is the only deployment artifact, and the docs site
contains only docz-managed content.

**Implements:** [DESIGN-0014](../design/0014-remove-legacy-engine-path-and-deprecated-overlays.md)

## Scope

### In Scope

- Delete the legacy `NewEngine` constructor and the
  `e.policy != nil` dispatch branch in `Engine.CheckRepo`.
- Rename `NewEngineFromPolicy` → `NewEngine` once it is the only
  constructor.
- Slim `internal/rules/` down to just `TemplateStore` + embedded
  templates; delete `Registry`, `NewRegistry`, `EnabledRules`,
  `RuleByName`, `AllRules`, `DefaultRules`, `FileRule`, and
  `BuildPRBody`.
- Migrate the 10 legacy test cases in
  `internal/checker/engine_test.go` to the policy-engine helper.
- Delete `deploy/base/` (7 manifests) and
  `deploy/overlays/{dev,prod,tailscale}` (12 files total).
- Replace `deploy/` with a standalone `deploy/MIGRATED.md`
  tombstone (bounded 6-month lifetime).
- Delete `docs/legacy/` (7 markdown files) and drop the `Legacy:`
  nav block from `mkdocs.yml`.
- Update `CLAUDE.md` to remove the legacy / DEPRECATED architecture
  notes (lines 51-52, 65, 68 per audit).
- Update the root `README.md` deployment section (lines 272-279)
  to replace the Kustomize examples with a chart pointer.

### Out of Scope

- Any new functionality. This is a removal-only change.
- Touching the policy engine itself — IMPL-0013 owns convergence
  fixes; this work only deletes the alternative path.
- Backwards compatibility for operators still consuming the
  Kustomize tree. The chart has been the recommended path since
  IMPL-0004 (chart 0.1.0).
- Republishing legacy doc URLs as redirects (DESIGN-0014 Q2
  resolved against this).
- Renaming the `internal/rules` package (the name becomes mildly
  misleading after slim-down, but renaming costs every import line
  in the tree for zero behavioural benefit — deferred per
  DESIGN-0014 Q5).
- Bumping chart `version` or binary `appVersion`. All three PRs
  carry the `dont-release` label because no operator-visible
  behaviour changes.

## Implementation Phases

Each phase ships as its own **independent PR against `main`**,
merged in order P1 → P2 → P3 (per Resolved Decision Q1). Phase 1
is code-heavy; Phases 2 and 3 are pure deletions plus tombstones
and nav updates.

---

### Phase 1: Engine path collapse and `internal/rules` slim-down

Largest phase. Deletes the legacy engine path, renames the policy
constructor, slims the `rules` package down to its surviving
`TemplateStore` surface, and migrates the legacy test cases.

#### Tasks

- [x] Confirm `BuildPRBody` has zero consumers outside legacy
  `engine.go` and `TestBuildPRBody` (verified during audit; will
  re-confirm at task start).
- [x] Confirm `BranchName` and `PRTitle` constants are used by
  both legacy and policy paths (verified during audit — they
  appear in `engine_policy.go:549,559,576` and 13 test sites).
  They stay in `engine.go` per Resolved Decision Q2 (`engine.go`
  remains the home for the surviving struct + constants +
  constructor + `CheckRepo`).
- [x] Delete from `internal/checker/engine.go`: `BuildPRBody`,
  `shouldSkip`, `findMissingFiles`, `createOrUpdatePR`, the
  legacy body of `CheckRepo`, and the `NewEngine` constructor.
- [x] Rename `NewEngineFromPolicy` → `NewEngine` in
  `internal/checker/engine_policy.go`. Drop the now-redundant
  `e.policy != nil` dispatch from `CheckRepo` (currently
  `engine_policy.go:99-101`).
- [x] Remove the `Engine.policy *policy.Policy` nullable field
  semantics — the field becomes non-nullable. Update the
  constructor doc comment.
- [x] Confirm `engine.go` is left holding only: the `Engine`
  struct, `BranchName`/`PRTitle` constants, the renamed
  `NewEngine` constructor, and a single-path `CheckRepo`
  (~80 LOC). `engine_policy.go` keeps the policy-path methods.
- [x] Delete from `internal/rules/registry.go`:
  - `FileRule` struct (lines 19-39)
  - `DefaultRules` slice (lines 43-75)
  - `Registry` struct (lines 78-80)
  - `NewRegistry`, `EnabledRules`, `RuleByName`, `AllRules`
    (lines 83-118)
- [x] Keep in `internal/rules/registry.go`: `TemplateStore` and
  its methods (`NewTemplateStore`,
  `NewTemplateStoreWithRenderer`, `Load`, `Get`, `Raw`) plus
  unexported helpers (`store`, `loadFromDir`,
  `loadEmbeddedDefaults`).
- [x] Drop test cases in `internal/rules/registry_test.go` that
  exercised the deleted symbols (`Registry`, `DefaultRules`,
  `FileRule`). Keep `TemplateStore` tests in place; keep the
  filename `registry_test.go` (per Resolved Decision Q6 —
  preserves git-blame history).
- [x] Migrate the 10 legacy test functions in
  `internal/checker/engine_test.go`:
  `TestCheckRepo_AllFilesExist`, `TestCheckRepo_MissingFiles_NoPR`,
  `TestCheckRepo_MissingFiles_ExistingPR`,
  `TestCheckRepo_MissingFiles_ThirdPartyPR`,
  `TestCheckRepo_Archived`, `TestCheckRepo_Fork`,
  `TestCheckRepo_EmptyRepo`, `TestCheckRepo_DryRun`,
  `TestCheckRepo_StaleBranchCleanup`, `TestBuildPRBody`. Each
  rewrites to use `testPolicyEngine(policy.BuiltinDefaults())`
  (the existing helper at `engine_test.go:14`) or its
  equivalent. `TestBuildPRBody` is deleted — the function
  itself is gone.
- [x] Update `cmd/repo-guardian/main.go:255` call site:
  `checker.NewEngineFromPolicy(...)` → `checker.NewEngine(...)`.
- [x] Update `CLAUDE.md`:
  - Line 65 ("Engine dual path: `NewEngine` (legacy registry,
    no reconcilers) and `NewEngineFromPolicy`...") — rewrite to
    single-path description.
  - Line 68 ("FileRule registry — each rule defines...Legacy
    path retained for backward compatibility.") — drop the
    legacy sentence.
  - The `internal/rules/` line in the package map (around line
    47) — change from "FileRule registry + TemplateStore" to
    "TemplateStore (embedded fallback templates)".
<!-- Resolved Decision Q4: no CI guard against regression of the
     deleted symbols — review discipline is sufficient. No task
     needed here. -->
- [x] Run `make fmt && make lint && make test` after each task.
- [x] Run `go test -race -count=10 ./internal/checker/...` once
  the test migration is complete, to catch flakiness in the
  rewrite.
- [x] Commit work in bisectable chunks (suggested split:
  constants relocation → legacy code deletion → renames → test
  migration → docs).

#### Success Criteria

- `make ci` passes locally and on the PR.
- `go test -race -count=10 ./internal/checker/...` stable.
- `grep -rn "NewEngineFromPolicy\|rules\.Registry\|rules\.NewRegistry\|rules\.FileRule\|rules\.DefaultRules\|rules\.EnabledRules\|rules\.AllRules\|rules\.RuleByName\|BuildPRBody" --include='*.go' .`
  returns zero matches in production code.
- The renamed `checker.NewEngine(client, policy, log)` is the only
  exported constructor in `internal/checker/`.
- `internal/rules/registry.go` is roughly 150 LOC, all
  `TemplateStore`.
- CLAUDE.md no longer references "legacy registry path" or
  "Legacy path retained for backward compatibility."
- Net LOC reduction: roughly 450 LOC across `engine.go` (~352)
  and `registry.go` (~100), modulo test migration churn.

---

### Phase 2: Kustomize tree removal

Pure-deletion phase. The chart has been the supported deployment
since IMPL-0004 (chart 0.1.0). The Kustomize tree predates several
chart fixes (PR #67 namespace stamping, IMPL-0011 multi-replica
resources, IMPL-0012 template ConfigMap) and would be broken
against the current binary anyway.

#### Tasks

- [x] Delete `deploy/base/` (7 files: `configmap.yaml`,
  `deployment.yaml`, `kustomization.yaml`, `secret.yaml`,
  `service.yaml`, `serviceaccount.yaml`, `servicemonitor.yaml`).
- [x] Delete `deploy/overlays/dev/` (2 files).
- [x] Delete `deploy/overlays/prod/` (2 files).
- [x] Delete `deploy/overlays/tailscale/` (5 files).
- [x] Create `deploy/MIGRATED.md` tombstone per Resolved
  Decision Q3 — single page explaining: why the tree is gone
  (chart is the recommended path), the one-line `helm install`
  command, and one paragraph each mapping the `dev`/`prod`/
  `tailscale` overlays to their chart-values equivalents.
  Pointer to `charts/repo-guardian/README.md`. Bounded
  6-month lifetime — delete the file itself after that.
- [x] Update `CLAUDE.md` lines 50-52: drop the Kustomize
  architecture note ("base, overlays — Kustomize... DEPRECATED").
- [x] Update root `README.md` (lines 272-279) per Resolved
  Decision Q5: replace the Kustomize-based deployment example
  with a one-line "deployed via the Helm chart at
  `charts/repo-guardian/` — see [chart
  README](charts/repo-guardian/README.md) for installation
  recipe."
- [x] Verify no CI workflow references the deploy tree
  (`grep -rn "deploy/" .github/`). Audit confirmed zero
  references; re-run at task time.
- [x] Also fixed stale `deploy/base/configmap.yaml` example in
  `docs/ADDING_RULES.md` Step 5 — replaced with chart
  `templates.files` values example.
- [x] Commit and run `make ci`.

#### Success Criteria

- `find deploy -type f -name '*.yaml' 2>/dev/null | wc -l`
  returns 0.
- `grep -rn "deploy/base\|deploy/overlays" --exclude-dir=.git .`
  returns only the chosen tombstone artifact, DESIGN-0014,
  this IMPL-0014, and historical references in DESIGN-0005
  ("replace existing Kustomize-based deployment") + INV-0001
  ("use the `deploy/overlays/tailscale/` overlay"). The latter
  two are pre-existing docs that documented the now-displaced
  tree; rewriting them would re-history these artifacts, so
  they remain as factual records of decisions taken in their
  time.
- `make ci` green (no code changes; CI runs unaffected jobs).
- If Q3 chose (b)/(c): `make helm-docs` regenerated the chart
  README cleanly; the rendered README is committed.
- CLAUDE.md no longer references the DEPRECATED Kustomize tree.

---

### Phase 3: Legacy docs purge

Smallest phase. The 7 files in `docs/legacy/` are unreferenced
outside their nav block in `mkdocs.yml`. The pre-docz markdown
imports were superseded one-for-one by `docs/rfc/0001`,
`docs/impl/0001`, `docs/impl/0002`, `docs/design/0002`, and
`docs/design/0003` when docz was adopted.

#### Tasks

- [x] Delete `docs/legacy/RFC.md` (superseded by RFC-0001).
- [x] Delete `docs/legacy/IMPLEMENTATION_PLAN.md` (superseded by
  IMPL-0001).
- [x] Delete `docs/legacy/ONE_PAGER.md` (superseded by docz docs).
- [x] Delete `docs/legacy/api_backoff.md` (superseded by
  DESIGN-0002).
- [x] Delete `docs/legacy/tailscale_research.md` (superseded by
  DESIGN-0003).
- [x] Delete `docs/legacy/custom_properties.md` (superseded by
  IMPL-0002).
- [x] Delete `docs/legacy/custom_properties_implementation.md`
  (superseded by IMPL-0002).
- [x] Remove the `Legacy:` nav block from `mkdocs.yml` (lines
  46-53 in the current file).
- [x] Run `grep -rn "docs/legacy\|legacy/" docs/ charts/ README.md
  CLAUDE.md` to confirm no cross-references survive. Audit found
  none outside `mkdocs.yml` and DESIGN-0014.
- [x] Run `mkdocs build --strict` (or `make docs-build`) to
  confirm the site builds with no dangling internal links. If
  the Makefile lacks a docs target, install mkdocs locally and
  run directly. **Outcome:** Phase 3 reduced strict-mode
  warnings from 21 → 14 (the 7 dropped were the deleted legacy
  pages + their nav entries). All 14 remaining warnings are
  pre-existing dangling cross-references to paths outside the
  `docs/` tree (`charts/`, `contrib/`, `internal/`, `examples/`)
  which work fine on GitHub's renderer but fail in mkdocs
  strict mode. Out of scope for IMPL-0014; tracked separately.
- [x] Run `docz update` to regenerate the section README tables
  if the deletions affect the docs index. (The legacy docs sit
  in their own nav block and are NOT docz-managed, so the
  section READMEs are likely unaffected — verify.) Verified
  unchanged; section READMEs read frontmatter `status:` not nav.
- [x] Commit.
- [x] Bonus cleanup: discovered during execution that
  `docs/legacy/` was gitignored (`.gitignore:48-49`), meaning
  the 7 files were never tracked in git. The `rm -rf` only
  affected the local working tree, but the `mkdocs.yml` Legacy
  nav block was pointing to files that wouldn't exist on a
  fresh clone (a latent bug). Removed the dead `docs/legacy`
  entry from `.gitignore` as part of this PR.

#### Success Criteria

- `find docs/legacy -type f 2>/dev/null | wc -l` returns 0
  (directory is gone).
- `grep -rn "docs/legacy" --exclude-dir=.git .` returns only
  DESIGN-0014 and IMPL-0014.
- `mkdocs build --strict` exits 0 (best-effort relative to the
  Phase-3 work). Pre-existing dangling refs to non-`docs/` paths
  (chart, contrib, internal, examples) remain and were
  documented as out-of-scope above. Phase 3 strictly reduced
  warning count 21 → 14.
- The `Legacy:` nav block is absent from `mkdocs.yml`.
- No docz-managed section README table loses entries (those
  tables only track docz docs, not the legacy tree).

---

## File Changes

| File | Action | Phase | Description |
|---|---|---|---|
| `internal/checker/engine.go` | Modify | 1 | Strip legacy methods + constructor; keep struct + constants + renamed `NewEngine` + single-path `CheckRepo` (~80 LOC) |
| `internal/checker/engine_policy.go` | Modify | 1 | Rename `NewEngineFromPolicy` → `NewEngine`; drop dispatch branch |
| `internal/checker/engine_test.go` | Modify | 1 | Migrate 10 legacy test cases to policy helper; delete `TestBuildPRBody` |
| `internal/rules/registry.go` | Modify | 1 | Delete `Registry`, `DefaultRules`, `FileRule`; keep `TemplateStore` |
| `internal/rules/registry_test.go` | Modify | 1 | Drop tests for deleted symbols; keep filename and `TemplateStore` tests |
| `cmd/repo-guardian/main.go` | Modify | 1 | Update constructor call (`NewEngineFromPolicy` → `NewEngine`) |
| `CLAUDE.md` | Modify | 1, 2 | Drop legacy-path architecture notes (P1); drop Kustomize DEPRECATED note (P2) |
| `deploy/base/*.yaml` (7 files) | Delete | 2 | Replaced by Helm chart |
| `deploy/overlays/{dev,prod,tailscale}/*.yaml` (12 files) | Delete | 2 | Replaced by chart values |
| `deploy/MIGRATED.md` | Create | 2 | Tombstone with migration recipe (6-month lifetime) |
| `README.md` (root) | Modify | 2 | Replace Kustomize commands (lines 272-279) with one-line chart pointer |
| `docs/legacy/*.md` (7 files) | Delete | 3 | Superseded by docz-managed docs |
| `mkdocs.yml` | Modify | 3 | Drop `Legacy:` nav block (lines 46-53) |

## Testing Plan

- [x] **Phase 1**: After each chunk, run `make fmt && make lint
  && make test`. After test migration is complete, run
  `go test -race -count=10 ./internal/checker/...` for
  flakiness verification.
- [x] **Phase 1**: Manual check —
  `grep -rn "NewEngineFromPolicy\|rules\.Registry\|rules\.FileRule\|rules\.DefaultRules\|BuildPRBody" --include='*.go' .`
  returns zero matches in production code (test migration
  artifacts in `_test.go` files are not allowed either).
- [x] **Phase 2**: `make ci` after the deletions (CI runs
  unaffected because paths-filter detects no Go/Docker/Helm
  changes; the Helm Chart Test job exercises the chart, not the
  deleted overlays).
- [x] **Phase 3**: `mkdocs build --strict` ran cleanly relative
  to Phase 3 work (21 → 14 warnings — only pre-existing
  out-of-`docs/` dangling refs remain). Manual cross-reference
  grep for `docs/legacy` returns only IMPL/DESIGN-0014 and the
  IMPL-0013 Q7 historical mention as expected.
- [ ] **Cross-phase**: After all three PRs merge, run the chart
  publish workflow once with `dry_run=true` to confirm the
  chart still packages cleanly without the Kustomize tree.

## Resolved Decisions

Open questions answered during pre-PR review (2026-05-30).

| # | Question | Decision | Notes |
|---|---|---|---|
| Q1 | PR sequencing | **(a)** Three independent PRs targeting `main`, merged in order P1 → P2 → P3. | Each is bisectable. P2/P3 carry no code; their CI is fast. Rebases onto `main` after each merge. |
| Q2 | Disposition of `internal/checker/engine.go` after surgery | **(a)** Keep `engine.go` as the home for the surviving struct + constants + renamed `NewEngine` + single-path `CheckRepo`. | Minimal diff. `engine_policy.go` keeps all policy-path methods. Filename's "policy" qualifier becomes a vestige but isn't worth churning every import line over. |
| Q3 | Kustomize tombstone location | **(a)** Standalone `deploy/MIGRATED.md` with 6-month lifetime. | Lives where git-history readers will navigate. Delete the file itself after 6 months. |
| Q4 | CI guard against regression of deleted symbols | **(a)** None. | Bisectable deletion + review discipline is sufficient. Lint scaffolding for a one-shot removal is overhead with minimal payoff. |
| Q5 | Update root `README.md` Kustomize section | **(a)** Bundle with Phase 2's PR. | Replace lines 272-279 with a one-line chart pointer. Don't leave broken examples in the front-door README. |
| Q6 | `registry_test.go` filename after slim-down | **(a)** Keep filename, drop deleted-symbol tests, leave `TemplateStore` tests in place. | Smallest diff; preserves git-blame history. |

## Dependencies

- **Hard prerequisite:** IMPL-0013 must be merged to `main`
  before Phase 1 starts. ✅ Done (PR #82 merged 2026-05-29 as
  commit `ee80603`). The test files that Phase 1 modifies
  (`internal/checker/engine_test.go`) were heavily touched by
  IMPL-0013; sequencing IMPL-0013 first avoids merge conflicts.
- No external Go dependency changes.
- No chart or helm changes (Phase 2 only removes the Kustomize
  tree; chart is independent).
- No database schema or migration changes.

## References

- [DESIGN-0014](../design/0014-remove-legacy-engine-path-and-deprecated-overlays.md)
  — the design this IMPL executes.
- [IMPL-0013](0013-reconcile-open-prs-when-file-rules-become-satisfied.md)
  — the work whose scope decision (policy-engine only) motivated
  DESIGN-0014. Hard prerequisite for IMPL-0014.
- [IMPL-0005](0005-hcl-policy-configuration-and-rule-engine.md) —
  introduced the policy engine path that supersedes the legacy
  registry path.
- [IMPL-0004](0004-helm-chart-for-repo-guardian.md) — the chart
  that supersedes `deploy/base/`.
- [IMPL-0003](0003-github-webhook-ip-allowlist-middleware.md) —
  the work that obsoleted the Tailscale overlay (covered by
  chart values now).
- [RFC-0001](../rfc/0001-repo-compliance-app-repo-guardian.md) —
  successor to `docs/legacy/RFC.md`.
- [IMPL-0001](0001-repo-guardian-implementation-plan.md) —
  successor to `docs/legacy/IMPLEMENTATION_PLAN.md`.
- `internal/checker/engine.go` — primary deletion target for
  Phase 1.
- `internal/checker/engine_policy.go:99-101` — dispatch site to
  be collapsed in Phase 1.
- `cmd/repo-guardian/main.go:255` — only production caller of
  `NewEngineFromPolicy`, to be renamed in Phase 1.
- `CLAUDE.md` lines 51-52, 65, 68 — architecture notes to be
  rewritten across Phases 1 and 2.
