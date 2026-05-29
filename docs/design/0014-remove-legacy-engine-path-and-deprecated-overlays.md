---
id: DESIGN-0014
title: "Remove legacy engine path and deprecated overlays"
status: Draft
author: Donald Gifford
created: 2026-05-28
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0014: Remove legacy engine path and deprecated overlays

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-05-28

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
  - [Legacy surface inventory](#legacy-surface-inventory)
- [Detailed Design](#detailed-design)
  - [1. `internal/checker/engine.go` — legacy registry path](#1-internalcheckerenginego--legacy-registry-path)
  - [2. `deploy/base/` + overlays — deprecated Kustomize](#2-deploybase--overlays--deprecated-kustomize)
  - [3. `docs/legacy/` — pre-docz markdown](#3-docslegacy--pre-docz-markdown)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

repo-guardian carries three legacy surfaces that predate current
production: the registry-based `NewEngine` code path in
`internal/checker/engine.go`, the Kustomize `deploy/base/` + overlays
marked DEPRECATED in CLAUDE.md, and the pre-docz markdown corpus in
`docs/legacy/`. None of these are used by the deployed binary,
deployed chart, or current docs pipeline; they exist because the
project predates each respective replacement and removal was deferred
to avoid churn.

This DESIGN proposes deleting all three in a single coordinated PR
sequence. The motivation is concrete: IMPL-0013 (PR-drift fix) is
policy-engine-only because mirroring the fix into the legacy path
would double its test surface for zero production benefit. As the
codebase matures, every new convergence guarantee will face the same
choice, and the right answer is to remove the legacy path now rather
than keep paying the divergence tax.

## Goals and Non-Goals

### Goals

- Delete `NewEngine` (legacy registry path) and the
  `e.policy != nil` dispatch branch in `CheckRepo`. Every code path
  ends up policy-driven.
- Delete `deploy/base/`, `deploy/overlays/dev`,
  `deploy/overlays/prod`, `deploy/overlays/tailscale`. Helm chart
  becomes the only supported deployment path.
- Delete `docs/legacy/` (7 markdown files). All docs land under
  the docz-managed sections.
- Migrate any test file still calling `NewEngine` to use
  `NewEngineFromPolicy` + an inline `policy.Policy` literal.
- Update `CLAUDE.md` to remove the "legacy path / DEPRECATED"
  caveats and `mkdocs.yml` to drop the Legacy nav section.
- Net negative LOC: ~352 LOC from `engine.go` + ~50 LOC of
  Kustomize + the legacy doc tree.

### Non-Goals

- Adding any new functionality. This is a removal-only change.
- Touching the policy engine itself. IMPL-0013 owns the convergence
  fixes; this DESIGN only deletes the alternative path.
- Backwards compatibility for operators still consuming
  `deploy/base/`. The chart has been the recommended path since
  IMPL-0004 (chart 0.1.0); the homelab and prod both run on the
  chart today. We will leave a `deploy/MIGRATED.md` tombstone with
  the upgrade recipe.
- Republishing the docs site to redirect old legacy URLs. The
  `docs/legacy/` files were never linked from anything except the
  Legacy nav; their loss is invisible to readers.
- Combining this with the IMPL-0013 fix. They ship as separate
  branches/PRs to keep blast radii independent.

## Background

### Legacy surface inventory

Verified 2026-05-28 against `main` at `b4fb821`:

| Surface | Location | LOC / files | Live callers |
|---|---|---|---|
| `NewEngine` constructor + dispatch | `internal/checker/engine.go` | 352 LOC | Only `internal/checker/engine_test.go:278` (`runEngine` test helper). `cmd/repo-guardian/main.go` calls `NewEngineFromPolicy` exclusively. |
| Kustomize base | `deploy/base/` | 7 files (`configmap`, `deployment`, `kustomization`, `secret`, `service`, `serviceaccount`, `servicemonitor`) | None — `CLAUDE.md` marks DEPRECATED. Homelab runs the chart via ArgoCD; prod runs the chart on EKS. |
| Kustomize overlays | `deploy/overlays/{dev,prod,tailscale}` | 3 subdirectories | None. The Tailscale path is covered by chart values (`trustProxyHeaders`) post-IMPL-0003. |
| Pre-docz legacy docs | `docs/legacy/*.md` | 7 files (`RFC.md`, `IMPLEMENTATION_PLAN.md`, `ONE_PAGER.md`, `api_backoff.md`, `tailscale_research.md`, `custom_properties.md`, `custom_properties_implementation.md`) | Listed in `mkdocs.yml` Legacy section only. Superseded one-for-one by `docs/rfc/0001`, `docs/impl/0001`, `docs/impl/0002`, `docs/design/0002`, `docs/design/0003`. |

The single non-test caller of `NewEngine` was the binary's `main.go`
before IMPL-0005 (HCL policy engine) made the policy path the
default. After IMPL-0005 the binary unconditionally constructs a
policy (defaults + HCL + env) before constructing the engine; the
`e.policy != nil` branch in `CheckRepo` is dead code in production
and exercised only by `engine_test.go`'s legacy helper.

The Kustomize tree predates IMPL-0004 (Helm chart). After chart
0.3.2 it has had no maintenance: no namespace stamping fix from
PR #67, no IMPL-0011 multi-replica resources, no IMPL-0012 template
ConfigMap. Anyone consuming it today would get the August 2025 era
behaviour, which is far enough behind that "migration" is more
honest framing than "support."

`docs/legacy/` is mkdocs-only inertia. The pre-docz markdown was
imported wholesale when docz was adopted (RFC-0001, IMPL-0001,
IMPL-0002, DESIGN-0002, DESIGN-0003 are the structured replacements).
Every file in the legacy tree has a 1:1 successor.

## Detailed Design

### 1. `internal/checker/engine.go` — legacy registry path

**Current dispatch** (`engine_policy.go:40-49`):

```go
func (e *Engine) CheckRepo(...) error {
    if e.policy != nil {
        return e.checkRepoWithPolicy(...)
    }
    // legacy registry path
    return e.checkRepoLegacy(...)
}
```

**After removal:** `CheckRepo` calls `checkRepoWithPolicy` directly
and drops the conditional. The `Engine` struct loses its
`policy *policy.Policy` nullable field (becomes non-nullable);
`NewEngine` is deleted; `NewEngineFromPolicy` is renamed to
`NewEngine` (claiming the simpler name now that there's only one
constructor).

The legacy registry types live in `internal/rules/` (`FileRule`
registry + `TemplateStore`). These stay — `NewEngineFromPolicy`
uses the `TemplateStore` for embedded fallback templates, and the
HCL loader translates policy file-rules into the same shape the
engine consumed before. Only the *constructor and dispatch* are
legacy, not the rule abstraction.

**Tests:** `internal/checker/engine_test.go` is the only call site.
`runEngine` (`engine_test.go:278`) is the helper that constructs
the legacy engine. Rewrite to:

```go
func runEnginePolicy(t *testing.T, client github.Client, dryRun bool) *Engine {
    p := policy.MustLoad(t, &policy.Policy{
        FileRules: policy.BuiltinDefaults().FileRules,
        Guardian:  policy.GuardianConfig{DryRun: dryRun},
    })
    e, err := NewEngine(client, p, slog.Default())  // formerly NewEngineFromPolicy
    require.NoError(t, err)
    return e
}
```

Roughly 12 test cases in `engine_test.go` consume the legacy helper.
Each test stays semantically equivalent — the legacy path was a
strict subset of the policy path, so any test that passed under
legacy will pass under policy with the equivalent built-in defaults.

### 2. `deploy/base/` + overlays — deprecated Kustomize

**Action:** Delete the entire `deploy/` directory.

**Tombstone:** Replace with `deploy/MIGRATED.md` (single file)
explaining:

- Why the tree is gone (chart is the recommended path).
- One-line `helm install` command to replicate the historical
  default-overlay behaviour.
- One paragraph each for the `dev`, `prod`, and `tailscale`
  overlays mapping their distinguishing values to chart values
  (e.g., `tailscale` → `trustProxyHeaders: true`).
- Pointer to `charts/repo-guardian/README.md`.

The tombstone exists for git-history readers who navigate to the
old path; it is not linked from `mkdocs.yml` or chart docs. After
6 months we delete `MIGRATED.md` too. Open Question 1 covers
whether to inline the migration into the chart README instead.

### 3. `docs/legacy/` — pre-docz markdown

**Action:** Delete all 7 files. Drop the `Legacy:` block from
`mkdocs.yml`. Verify the docz-managed successors exist and are
linked from their respective section README.

Optional: emit one redirect rule per legacy URL in a future
mkdocs `redirects` plugin config. Not in scope — the legacy URLs
were only discoverable via the in-site nav, which is gone, so
external inbound links are vanishingly unlikely. Open Question 2.

## API / Interface Changes

| Symbol | Change | Migration |
|---|---|---|
| `checker.NewEngine(reg, ts, log, ...)` | **Deleted** | Tests migrate to the renamed `NewEngine(client, policy, log)`. |
| `checker.NewEngineFromPolicy(client, policy, log)` | **Renamed** to `NewEngine`. Signature unchanged. | `cmd/repo-guardian/main.go` updates the call site. |
| `checker.Engine.policy *policy.Policy` | Non-nullable | No external consumers; field is unexported. |
| `deploy/base/*.yaml` | Deleted | Replaced by chart. |
| `deploy/overlays/{dev,prod,tailscale}/*.yaml` | Deleted | Replaced by chart values. |
| `docs/legacy/*.md` | Deleted | Superseded by docz-managed docs. |
| `mkdocs.yml` `Legacy:` block | Deleted | n/a |
| `CLAUDE.md` "legacy / DEPRECATED" notes | Removed | n/a |

No public Go API changes — `checker.NewEngine` is part of the
internal package and is not consumed by any external module.

## Data Model

No data model changes. The policy engine already owns all state.

## Testing Strategy

- **Unit tests:** `engine_test.go` migration described above. After
  migration, run `go test -count=10 ./internal/checker/...` to
  confirm no flakiness introduced by the helper rewrite.
- **Integration:** Existing `internal/scheduler/*_integration_test.go`
  and `internal/store/postgres/*_integration_test.go` unaffected —
  neither touches the engine constructor.
- **Helm:** Existing `helm-unittest` and `helm/chart-testing` jobs
  cover the chart; no test changes needed. The `deploy/` deletion
  is a no-op as far as CI is concerned (no jobs reference the
  Kustomize tree).
- **Docs:** `make docs-build` (if it exists) or a local
  `mkdocs build --strict` to confirm no dangling internal links
  after the legacy nav goes away. Section READMEs are docz-managed
  and auto-update.

## Migration / Rollout Plan

Split into **three PRs** to keep diffs reviewable and bisectable:

**PR 1 — Engine path collapse** (largest, code-only):

1. Rename `NewEngineFromPolicy` → `NewEngine`. Delete the old
   `NewEngine` constructor and the legacy `CheckRepo` branch.
2. Migrate `engine_test.go`'s `runEngine` helper and every test
   case using it.
3. Update `cmd/repo-guardian/main.go` call site.
4. Update `CLAUDE.md` ("Engine dual path" architecture note becomes
   "Engine path" — single-path description).
5. `make ci` green; bisect-clean commit.

**PR 2 — Kustomize tree removal** (deploy/ tombstone):

1. Delete `deploy/base/` and all overlays.
2. Add `deploy/MIGRATED.md` tombstone.
3. Update `CLAUDE.md` (drop DEPRECATED note).
4. No code or test changes; `make ci` green automatically.

**PR 3 — Legacy docs purge** (docs only):

1. Delete `docs/legacy/`.
2. Update `mkdocs.yml` (drop Legacy nav block).
3. Update any cross-references that pointed into the legacy tree
   (verify via `grep -rn "docs/legacy" docs/`).
4. `mkdocs build --strict` green.

All three carry the `dont-release` label (no binary or chart
behavioural change). Chart and binary versions do not bump for
PR 1's rename — `NewEngine` is an internal symbol.

Rollback for any single PR is `git revert` — no migrations, no
schema changes, no runtime state to preserve.

## Open Questions

Each option is letter-keyed: `a` is my recommendation; `b` onward
are alternatives; `other` is an escape hatch for "none of the
above, here's what I want."

### 1. `deploy/MIGRATED.md` tombstone — keep or skip?

- **(a) Keep the tombstone for 6 months, then delete.** Git-history
  readers who navigate to the old path get a one-page recipe;
  6 months covers any reasonable upgrade window for a forked or
  privately-vendored consumer. ✅ recommended.
- **(b) Skip the tombstone entirely.** Git log is the authoritative
  history; anyone deep enough to find the deleted path can read
  the commit message.
- **(c) Inline the migration into the chart README permanently.**
  Chart README is already documented; piling on adds noise for
  current-state operators.
- **other:** _________________________

### 2. Legacy URL redirects in mkdocs

- **(a) No redirects.** The legacy URLs were nav-only; external
  inbound links are vanishingly unlikely. ✅ recommended.
- **(b) Add `mkdocs-redirects` plugin with 7 entries.** Cheap
  insurance but introduces a build-time plugin dependency.
- **(c) Add `.htaccess` / hosting-layer redirects.** Couples the
  doc repo to the hosting platform.
- **other:** _________________________

### 3. Rename `NewEngineFromPolicy` → `NewEngine`?

The legacy `NewEngine` slot becomes free. Reclaim it or pick a new
name?

- **(a) Rename `NewEngineFromPolicy` to `NewEngine`.** Now there's
  only one constructor; the qualifier is dead weight. ✅ recommended.
- **(b) Leave `NewEngineFromPolicy` as-is.** Avoids touching the
  call site in `main.go`; preserves grep-ability in older
  branches.
- **(c) Rename to `NewPolicyEngine`.** More descriptive but the
  package is `checker`, so `checker.NewPolicyEngine` is stutter.
- **other:** _________________________

### 4. Sequence — IMPL-0013 first, then this DESIGN's PRs?

- **(a) Ship IMPL-0013 first; start this DESIGN's PRs only after
  IMPL-0013 lands.** Keeps the two work streams from interleaving;
  IMPL-0013 has operator-visible fixes that should not wait on a
  removal-only sequence. ✅ recommended.
- **(b) Ship this DESIGN's PR 1 first to simplify IMPL-0013's
  Phase 3 code path.** Risks scope creep on the operator-visible
  fix.
- **(c) Run in parallel on separate branches.** Test-helper rewrites
  in PR 1 will conflict with any IMPL-0013 tests added in the
  same files.
- **other:** _________________________

### 5. Delete `internal/rules/registry.go` along with the legacy path?

The `FileRule` registry is used by both legacy and policy paths
(policy path consumes embedded fallback templates from it).

- **(a) Keep the registry; it's still load-bearing for embedded
  templates.** Removing it would require inlining the embedded
  templates into the policy default-loader. ✅ recommended.
- **(b) Inline embedded templates into `policy/defaults.go` as a
  follow-up.** Clean separation between "rule abstraction" and
  "policy abstraction," but a refactor beyond this DESIGN's scope.
- **(c) Delete now and inline as part of PR 1.** Bloats PR 1
  unnecessarily.
- **other:** _________________________

## References

- [IMPL-0013](../impl/0013-reconcile-open-prs-when-file-rules-become-satisfied.md)
  — the work whose scope decision (policy-engine only) motivated
  this DESIGN.
- [IMPL-0005](../impl/0005-hcl-policy-configuration-and-rule-engine.md)
  — introduced the policy engine path; the legacy path has been
  vestigial since this landed.
- [IMPL-0004](../impl/0004-helm-chart-for-repo-guardian.md) — the
  chart that supersedes `deploy/base/`.
- [IMPL-0003](../impl/0003-github-webhook-ip-allowlist-middleware.md)
  — the work that obsoleted the Tailscale overlay (covered by
  chart values now).
- [RFC-0001](../rfc/0001-repo-compliance-app-repo-guardian.md) —
  successor to `docs/legacy/RFC.md`.
- [IMPL-0001](../impl/0001-repo-guardian-implementation-plan.md) —
  successor to `docs/legacy/IMPLEMENTATION_PLAN.md`.
- `internal/checker/engine.go:50` — `NewEngine` constructor (to be
  deleted).
- `internal/checker/engine_policy.go:40-49` — dispatch site (to be
  collapsed).
- `cmd/repo-guardian/main.go:255` — the only production call to
  `NewEngineFromPolicy` (to be renamed).
- `CLAUDE.md` — "Engine dual path" architecture note (to be
  rewritten).
