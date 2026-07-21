---
id: IMPL-0018
title: "Fix silently ignored operator config"
status: Completed
author: Donald Gifford
created: 2026-07-20
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0018: Fix silently ignored operator config

**Status:** Completed
**Author:** Donald Gifford
**Date:** 2026-07-20

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 0: Wire autoclosepr through the HCL loader](#phase-0-wire-autoclosepr-through-the-hcl-loader)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 1: Chart render-time guard for cross-mode existingSecret](#phase-1-chart-render-time-guard-for-cross-mode-existingsecret)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 2: Docs, versioning, PR assembly](#phase-2-docs-versioning-pr-assembly)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Rollback](#rollback)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Fix the two silently-ignored operator-config bugs diagnosed in INV-0010:
(1) `auto_close_pr` declared in the HCL `guardian {}` block has no effect —
wire it through decode AND merge; (2) any of the four chart `existingSecret`
knobs set under a `mode` that doesn't consume it is silently ignored — fail
at chart-render time with an actionable message.

Both fixes ship on one branch / one PR in separate commits (one commit per
phase).

**Implements:** INV-0010 (Resolved)

## Scope

### In Scope

- `setGuardianAttr` + `mergeGuardianConfig` wiring for `AutoClosePR *bool`,
  preserving set-vs-unset and env-wins precedence
- Guardian block decode hardening (per OQ 1)
- Render-time guard for the four cross-mode `existingSecret` combinations
  (mechanism per OQ 2, coverage per OQ 3)
- Loader tests, helm-unittest cases, doc touch-ups, version bumps (per OQ 4)

### Out of Scope

- Any new config attributes or chart values
- The `githubApp`-level `existingSecret` (values.yaml:108) — always consumed,
  no mode dispatch, no cross-mode failure class
- IMPL-0017 work (custom properties) — separate branch/PR
- Runtime (binary-side) validation of chart values — the chart is the
  render-time contract

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its tasks
are checked off and its success criteria are met. Run `make lint && make
test` after each task. One commit per phase, all on
`fix/auto-close-pr-hcl-and-existing-secret-guard`, single PR.

---

### Phase 0: Wire auto_close_pr through the HCL loader

Both gaps from INV-0010 Observation 1, plus the decode hardening decided in
OQ 1.

#### Tasks

- [x] Add `case "auto_close_pr"` to `setGuardianAttr`
      (`internal/policy/loader.go:383-411`): `b := val.True();
      g.AutoClosePR = &b` — pointer assignment preserves set-vs-unset
- [x] Add the carry to `mergeGuardianConfig` (`loader.go:943-986`):
      `if src.AutoClosePR != nil { dst.AutoClosePR = src.AutoClosePR }`
- [x] Convert `decodeGuardianBlock` from `JustAttributes()` to a strict
      `hcl.BodySchema` + `Content()` listing every supported attribute
      including `auto_close_pr` (OQ 1 → a); audit `examples/*.hcl` and test
      fixtures for guardian attributes that would now fail load
- [x] Loader tests in `internal/policy/loader_test.go`:
      `auto_close_pr = false` ⇒ `AutoClosePREnabled() == false`;
      `auto_close_pr = true` ⇒ true; attribute absent ⇒ `AutoClosePR == nil`
      and `AutoClosePREnabled() == true`; HCL false + `AUTO_CLOSE_PR=true`
      env (via `t.Setenv`, no `t.Parallel`) ⇒ env wins
- [x] Test that an unknown guardian attribute (e.g. the historical
      `org = "x"`) now fails load with an "Unsupported argument"
      diagnostic naming file/line
- [x] Verify `docs/usage/policy-reference.md`'s `auto_close_pr` entry is
      accurate post-fix (it documents the attribute that was dead; the fix
      makes the doc true — adjust wording only if it hedges)
- [x] `make lint && make test` green

#### Success Criteria

- `auto_close_pr` in HCL round-trips: guardian block → decode → merge →
  `AutoClosePREnabled()`, with env override still winning
- Set-vs-unset distinction intact (`nil` ⇒ default true)
- Decode-hardening behavior (per OQ 1) covered by a test
- No other guardian attribute's behavior changes (full existing loader
  suite passes untouched)

---

### Phase 1: Chart render-time guard for cross-mode existingSecret

Guard the four knob/mode mismatches from INV-0010 Observation 3 (final list
per OQ 3) using the mechanism from OQ 2.

#### Tasks

- [x] Add `repo-guardian.validateBackendSecrets` to
      `charts/repo-guardian/templates/_helpers.tpl` and include it at the
      top of `deployment.yaml` (always rendered); one clear `fail` message
      per combo, e.g. `store.postgres.existingSecret is set but
      store.postgres.mode=baked never reads it — use
      store.postgres.baked.existingSecret, or set mode=external`
      (OQ 2 → a)
- [x] Cover all four combos (OQ 3 → a):
      `store.postgres.existingSecret` + mode ∈ {baked, cnpg};
      `store.postgres.baked.existingSecret` + mode ∈ {cnpg, external};
      `queue.valkey.existingSecret` + mode=baked;
      `queue.valkey.baked.existingSecret` + mode=external
- [x] helm-unittest negative cases (one per guarded combo) asserting the
      render fails — plus positive cases confirming all legitimate shapes
      in `tests/backend_shapes_test.yaml` still render (guard must not
      false-positive on empty-string defaults)
- [x] Comment the guard block with the INV-0010 rationale so future modes
      (e.g. a new store mode) extend it rather than bypass it
- [x] helm-unittest suite green; `helm template` smoke over the four modes
      documented in the chart README

#### Success Criteria

- Every guarded combo fails `helm template`/`helm install` with a message
  that names the offending value path and the correct alternative
- All existing valid value shapes (baked/cnpg/external × baked/external,
  empty-string secret defaults) render unchanged
- helm-unittest passes with the new negative + positive cases

---

### Phase 2: Docs, versioning, PR assembly

#### Tasks

- [x] `CLAUDE.md`: note the guardian-block decode behavior (per OQ 1
      outcome) and the chart guard in the relevant sections
- [x] Chart README (`README.md.gotmpl` — never the rendered README): brief
      "existingSecret is mode-scoped" note near the backend-shapes docs;
      run `make helm-docs`
- [x] Version bumps (OQ 4 → a): Chart.yaml `version` `1.0.0-rc.4` →
      `1.0.0-rc.5`, `appVersion` `1.9.0` → `1.9.1`; PR label `patch`
- [x] `docz update inv && docz update impl` to refresh indices; check off
      IMPL-0018 tasks; flip INV-0010 → Resolved if not already
- [x] Assemble the PR: commit 1 = Phase 0 (`fix(policy): ...`), commit 2 =
      Phase 1 (`fix(chart): ...`), commit 3 = Phase 2 (`docs/chore: ...`);
      PR body links INV-0010; label per OQ 4
- [x] `make ci` green; helm-unittest green

#### Success Criteria

- Single PR, three conventional commits, CI green
- Chart README regenerated from the `.gotmpl` (rendered README not edited
  directly)
- Version/label state matches the OQ 4 decision

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/policy/loader.go` | Modify | `setGuardianAttr` case, `mergeGuardianConfig` carry, decode hardening per OQ 1 |
| `internal/policy/loader_test.go` | Modify | round-trip, merge, env-precedence, unknown-attr tests |
| `charts/repo-guardian/templates/_helpers.tpl` | Modify | `validateValues` fail-helper guard |
| `charts/repo-guardian/templates/deployment.yaml` | Modify | include the guard |
| `charts/repo-guardian/tests/backend_shapes_test.yaml` (or new file) | Modify | negative + positive guard cases |
| `charts/repo-guardian/Chart.yaml` | Modify | version/appVersion per OQ 4 |
| `charts/repo-guardian/README.md.gotmpl` | Modify | mode-scoped existingSecret note |
| `docs/usage/policy-reference.md` | Verify | `auto_close_pr` entry accurate post-fix |
| `CLAUDE.md` | Modify | decode + guard notes |

## Testing Plan

- [x] Loader round-trip: HCL true/false/absent ⇒ `AutoClosePREnabled()`
- [x] Merge carry: raw guardian block → effective config
- [x] Env precedence: HCL false + `AUTO_CLOSE_PR=true` ⇒ true (`t.Setenv`,
      no `t.Parallel`)
- [x] Unknown guardian attribute behavior per OQ 1 (error/warn/ignore)
- [x] helm-unittest: one negative case per guarded combo (render fails,
      message content asserted where the framework allows)
- [x] helm-unittest: positive cases — every legitimate backend shape still
      renders; empty-string defaults never trip the guard
- [x] `make ci` + full helm-unittest suite

## Dependencies

- INV-0010 (Resolved) — diagnosis
- None external; no store/queue/webhook impact; no new chart values

## Rollback

Simple revert of the PR. One caveat if OQ 1 lands the strict schema: configs
carrying junk/typo guardian attributes that loaded silently before will fail
at startup after upgrading — that is the intended behavior change
(roll-forward posture); reverting restores the silent-swallow behavior.

## Open Questions

All resolved 2026-07-20 — every question decided as (a); resolutions folded
into the phase tasks above.

1. Guardian-block decode hardening — how far to take the root-cause fix? —
   **Decided: (a)**
   - (a) convert `decodeGuardianBlock` from `JustAttributes()` to a strict
     `hcl.BodySchema` + `Content()` listing every supported attribute
     (including `auto_close_pr`) — unknown attributes and typos fail load
     with the standard "Unsupported argument" diagnostic, exactly like
     every other block in the loader; kills the whole
     silently-dropped-attribute bug class (recommended)
   - (b) minimal fix — add the `auto_close_pr` case only, keep the
     permissive decode; smallest diff, but the next `GuardianConfig` field
     can silently regress the same way
   - (c) keep permissive decode but `slog.Warn` on unrecognized attributes
     — catches typos without hard-failing existing configs that carry
     stale junk
   - other:
2. Chart guard mechanism? — **Decided: (a)**
   - (a) `fail` in a `repo-guardian.validateBackendSecrets` helper included by
     `deployment.yaml` — fully custom, actionable error messages ("X is
     set but mode=Y never reads it — use Z instead"), which is the whole
     point for a misconfiguration this confusing; pattern is trivially
     extensible for future modes (recommended)
   - (b) `values.schema.json` `allOf`/`if`/`then` conditionals — consistent
     with the IMPL-0016 schema-rejection precedent and also fires on
     `helm lint`, but JSON-Schema conditional failures render as cryptic
     "must match" errors that name the path without explaining the fix
   - (c) both — schema for machine validation plus `fail` for the human
     message
   - other:
3. Guard coverage? — **Decided: (a)**
   - (a) all four knob/mode mismatch combos from INV-0010 Observation 3,
     including `baked.existingSecret` ignored under cnpg/external — same
     mechanism either way, symmetric, and prevents the mirror-image of the
     homelab incident (recommended)
   - (b) only the originally-reported direction (`existingSecret` set under
     baked mode) — smaller test matrix, leaves the mirror cases silent
   - other:
4. Versioning and PR label for the combined Go + chart PR? —
   **Decided: (a)**
   - (a) label `patch` (binary 1.9.0 → 1.9.1 carries the loader fix);
     bump chart `1.0.0-rc.4` → `1.0.0-rc.5` AND `appVersion` → `1.9.1` in
     the same PR so the chart ships the fixed binary and the guard
     together (recommended)
   - (b) label `patch`, Go fix only; hold the chart guard for the next
     chart-only PR (`dont-release`) — decouples the artifacts but splits
     the fix pair the PR is meant to keep together
   - other:

## References

- INV-0010 — Silently ignored operator config (parent investigation)
- `internal/policy/loader.go:361-411, 943-1023` — decode, merge, env paths
- `internal/policy/types.go:66-100` — `GuardianConfig`, `AutoClosePREnabled`
- `charts/repo-guardian/templates/_helpers.tpl:112-140` — mode dispatch
- `charts/repo-guardian/values.schema.json` — IMPL-0016 schema precedent
- `charts/repo-guardian/tests/backend_shapes_test.yaml` — shape test home
- PR #161 — `baked.existingSecret` introduction
- IMPL-0013 Phase 3 — `AutoClosePR` origin (`applyEnvBoolPtr`)
