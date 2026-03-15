---
id: IMPL-0008
title: "Distributed Renovate via Per-Repo GitHub Actions"
status: Draft
author: Donald Gifford
created: 2026-03-15
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0008: Distributed Renovate via Per-Repo GitHub Actions

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-03-15

## Objective

Add Renovate file management to repo-guardian so that every repository
gets a standardized Renovate workflow and config. repo-guardian manages
these as ordinary file rules — if missing, PR them in; if drifted, detect
it. No new engine capabilities are required; this is purely templates,
default rules, assertions, and tests.

**Implements:** DESIGN-0009

## Scope

### In Scope

- Renovate workflow template (docker-based GitHub Actions workflow)
- Renovate config template (JSON extending org preset)
- Updated policy defaults with two new file rules (workflow + config)
- Content assertion validating org preset reference in `renovate.json`
- `workflow_sync` reconciler attached to the workflow rule (already exists)
- Unit tests for assertion pattern matching on `renovate.json`
- Integration tests for engine evaluating both renovate file rules
- Example HCL configuration in documentation
- Updated embedded template for existing `renovate.tmpl`

### Out of Scope

- Coordinated dispatch / trigger endpoints (DESIGN-0009 Future Work)
- Staggered cron scheduling (DESIGN-0009 Future Work)
- Slack integration (DESIGN-0009 Future Work)
- Renovate GitHub App setup / org secret configuration (operational)
- Org preset repo creation (`renovate-config/default.json`) (operational)
- Template variable substitution or per-repo rendering
- PR comment tracking for repeated non-compliance (future engine feature)
- Changes to the checker engine, policy loader, or reconciler system
  (beyond adding the `org` field to `GuardianConfig`)

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its
tasks are checked off and its success criteria are met.

---

### Phase 1: Embedded Templates

Create the two embedded templates that repo-guardian will use to PR
Renovate files into repositories.

#### Tasks

- [x] Create `internal/rules/templates/renovate-workflow.tmpl` with the
      docker-based GitHub Actions workflow from DESIGN-0009 (schedule
      trigger, `actions/create-github-app-token@v1`, `docker run
      renovate/renovate:latest`, `RENOVATE_REPOSITORIES` scoped to
      `${{ github.repository }}`)
- [x] Update `internal/rules/templates/renovate.tmpl` to use the org
      preset pattern (`"extends": ["github>ORG_NAME/renovate-config"]`)
      instead of the current `"extends": ["config:recommended"]`. The
      `ORG_NAME` placeholder is replaced at template load time using the
      configured org name (see Phase 2)
- [x] Verify templates load correctly via `TemplateStore` by checking
      that `Get("renovate-workflow")` and `Get("renovate")` return
      expected content

#### Success Criteria

- `go build ./...` succeeds
- Both templates are accessible via `TemplateStore.Get()`
- Template content matches DESIGN-0009 specification
- `make lint` passes

---

### Phase 2: Policy Default Rules

Update the built-in policy defaults to include two Renovate file rules:
one for the workflow (exact match) and one for the config (contains with
assertion).

#### Tasks

- [x] Add `org` field to `GuardianConfig` in `internal/policy/types.go`
      (`hcl:"org,optional"`) with env var override `GITHUB_ORG` applied
      in the loader. This is used for org-specific assertion patterns and
      template rendering.
- [x] Update `defaultRenovateRule()` in `internal/policy/defaults.go` to
      return the `renovate_config` rule: `check = "contains"`,
      `paths = ["renovate.json", "renovate.json5", ".renovaterc",
      ".renovaterc.json", ".github/renovate.json",
      ".github/renovate.json5"]` (search all standard locations),
      `target = "renovate.json"`, `template = "renovate"`, with an
      assertion matching `github>ORG_NAME/renovate-config` (using
      configured org name) and message "renovate.json must extend org
      preset"
- [x] Add `defaultRenovateWorkflowRule()` in
      `internal/policy/defaults.go` returning the `renovate_workflow`
      rule: `check = "exact"`,
      `paths = [".github/workflows/renovate.yml"]`,
      `target = ".github/workflows/renovate.yml"`,
      `template = "renovate-workflow"`, with a `workflow_sync` reconciler
      (`watch = true`)
- [x] Both rules should default to `enabled = false` (opt-in, same as
      current Renovate rule behavior)
- [x] Add the workflow rule to the `DefaultPolicyConfig()` function's
      `FileRules` slice
- [x] Update existing tests in `internal/policy/defaults_test.go` to
      cover both new rules, assertion config, and reconciler config
- [x] Run `make lint && make test` to verify

#### Success Criteria

- `DefaultPolicyConfig()` includes both `renovate_config` and
  `renovate_workflow` file rules
- Both rules are disabled by default
- `GuardianConfig.Org` is populated from HCL or `GITHUB_ORG` env var
- `renovate_config` rule has a compiled assertion for org-specific preset
  pattern (e.g., `github>donaldgifford/renovate-config`)
- `renovate_workflow` rule has a `workflow_sync` reconciler with
  `watch = true`
- All existing tests pass, new defaults tests pass
- `make lint` passes

---

### Phase 3: Assertion and Template Tests

Write focused unit tests validating the assertion pattern works correctly
against various `renovate.json` content and that the exact-match check
mode works with the workflow template.

#### Tasks

- [x] Write unit tests in `internal/policy/assertion_test.go` for the
      Renovate org preset assertion pattern
      (`github>donaldgifford/renovate-config`):
  - [x] Valid: `{"extends": ["github>donaldgifford/renovate-config"]}`
        — passes
  - [x] Valid: `{"extends": ["github>donaldgifford/renovate-config"],
        "labels": ["deps"]}` — passes (overrides allowed)
  - [x] Invalid: `{"extends": ["config:recommended"]}` — fails
  - [x] Invalid: `{"extends": ["github>wrongorg/renovate-config"]}`
        — fails (wrong org)
  - [x] Invalid: `{}` — fails (no extends)
  - [x] Invalid: empty string — fails
- [x] Write unit test in `internal/checker/engine_policy_test.go` for
      `evaluateExact()` with the renovate workflow template — verify that
      identical content passes and modified content triggers action
- [x] Write unit test in `internal/checker/engine_policy_test.go` for
      `evaluateContains()` with the renovate config — verify that present
      file with valid assertion passes and invalid assertion triggers
      action
- [x] Run `make test` to verify all tests pass

#### Success Criteria

- Assertion pattern correctly matches valid org preset references
- Assertion pattern correctly rejects content without org preset
- Exact-match check works with YAML workflow content
- Contains check with assertion works with JSON config content
- `make test` passes
- `make lint` passes

---

### Phase 4: Integration Tests

Write end-to-end integration tests that exercise the full engine pipeline
with both Renovate file rules, including PR creation and reconciler
triggering.

#### Tasks

- [x] Write integration test in `internal/checker/engine_policy_test.go`:
      `TestIntegration_RenovateFileRules_BothMissing` — engine detects
      both files missing, creates single PR containing both
      `renovate.yml` and `renovate.json` with correct template content
- [x] Write integration test:
      `TestIntegration_RenovateFileRules_WorkflowMissing` — engine detects
      missing `renovate.yml` (config exists and valid), creates PR with
      just the workflow file
- [x] Write integration test:
      `TestIntegration_RenovateFileRules_ConfigMissing` — engine detects
      missing `renovate.json`, creates PR with correct template content
- [x] Write integration test:
      `TestIntegration_RenovateFileRules_ConfigInvalidPreset` — engine
      detects `renovate.json` exists but fails assertion (missing org
      preset reference), creates PR
- [x] Write integration test:
      `TestIntegration_RenovateFileRules_WorkflowDrifted` — engine detects
      `renovate.yml` exists but differs from template (exact mode),
      creates PR
- [x] Write integration test:
      `TestIntegration_RenovateFileRules_AllPresent` — both files exist
      and pass checks, no PR created, reconciler runs
- [x] Run `make test` to verify all integration tests pass

#### Success Criteria

- All six integration test scenarios pass
- Both-missing scenario produces single PR with both files
- PR creation includes correct file content from templates
- Assertion failure on `renovate.json` triggers PR
- Exact-match drift on `renovate.yml` triggers PR
- Reconciler runs when files are present and valid
- `make test` passes
- `make lint` passes

---

### Phase 5: Documentation and HCL Examples

Update documentation with HCL configuration examples, template
descriptions, and rollout guidance.

#### Tasks

- [ ] Add example HCL configuration to `docs/` or project README showing
      how to enable the Renovate file rules in `guardian.hcl`:
      ```hcl
      guardian {
        org = "donaldgifford"  # or set GITHUB_ORG env var
      }

      rule "file" "renovate_workflow" {
        enabled  = true
        check    = "exact"
        paths    = [".github/workflows/renovate.yml"]
        target   = ".github/workflows/renovate.yml"
        template = "renovate-workflow"
        reconcile "workflow_sync" { watch = true }
      }
      rule "file" "renovate_config" {
        enabled  = true
        check    = "contains"
        paths    = ["renovate.json", "renovate.json5", ".renovaterc",
                    ".renovaterc.json", ".github/renovate.json",
                    ".github/renovate.json5"]
        target   = "renovate.json"
        template = "renovate"
        assertion {
          pattern = "github>donaldgifford/renovate-config"
          message = "renovate.json must extend org preset"
        }
      }
      ```
- [ ] Document the `renovate-workflow` and `renovate` template names and
      what they contain
- [ ] Document that both rules are disabled by default and must be opted
      in via HCL config or by modifying defaults
- [ ] Update DESIGN-0009 status from "Draft" to "Approved" (or as
      appropriate after review)
- [ ] Run `docz update` to regenerate README index tables
- [ ] Run `make lint` to verify no markdown issues

#### Success Criteria

- HCL example is documented and correct
- Template contents and names are documented
- Opt-in behavior is clearly documented
- Doc indexes are up to date
- `make lint` passes

---

### Phase 6: Final Validation

Full CI pipeline validation and cleanup.

#### Tasks

- [ ] Run `make ci` (lint + test + build) — all green
- [ ] Review test coverage for changed packages (`internal/policy/`,
      `internal/rules/`, `internal/checker/`)
- [ ] Verify no TODO/FIXME comments left from implementation
- [ ] Verify `renovate-workflow.tmpl` content exactly matches DESIGN-0009
      workflow YAML
- [ ] Verify `renovate.tmpl` content matches DESIGN-0009 config JSON
- [ ] Update this implementation doc status to "Completed"
- [ ] Commit all changes with conventional commit message

#### Success Criteria

- `make ci` passes with zero errors
- All templates match DESIGN-0009 specification
- No unresolved TODOs related to this implementation
- Implementation doc marked as Completed

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/rules/templates/renovate-workflow.tmpl` | Create | Docker-based GHA workflow for Renovate |
| `internal/rules/templates/renovate.tmpl` | Modify | Update to org preset JSON pattern |
| `internal/policy/types.go` | Modify | Add `Org` field to `GuardianConfig` |
| `internal/policy/loader.go` | Modify | Add `GITHUB_ORG` env var override for `Org` field |
| `internal/policy/defaults.go` | Modify | Add `renovate_workflow` rule, update `renovate` rule with assertion |
| `internal/policy/defaults_test.go` | Modify | Tests for new default rules |
| `internal/policy/assertion_test.go` | Modify | Tests for org preset assertion pattern |
| `internal/checker/engine_policy_test.go` | Modify | Integration tests for Renovate file rules |
| `internal/config/config.go` | Modify | Add `GITHUB_ORG` env var (if not using HCL loader path) |

## Testing Plan

- [ ] Unit tests for org preset assertion pattern (6 cases: 2 valid, 4
      invalid including wrong org)
- [ ] Unit tests for exact-match check on workflow template
- [ ] Unit tests for contains check with assertion on config
- [ ] Unit tests for default rule configuration (both rules, org config)
- [ ] Integration tests for engine pipeline (6 scenarios: both missing,
      missing workflow, missing config, invalid config, drifted workflow,
      all present)

## Resolved Questions

- **Org name configuration:** Add an `org` field to the `guardian {}`
  HCL block with `GITHUB_ORG` env var override. This value is used in
  assertion patterns and template rendering for the org preset reference.
  Works for both GitHub organizations and personal accounts (e.g.,
  `donaldgifford`). Config-first, env var to override.

- **Renovate config search paths:** Keep all 6 standard Renovate config
  locations (`renovate.json`, `renovate.json5`, `.renovaterc`,
  `.renovaterc.json`, `.github/renovate.json`,
  `.github/renovate.json5`). The `paths` field searches all locations to
  detect any existing config. The `target` is `renovate.json` (where a
  new file is created if none exist). The CI workflow's
  `RENOVATE_CONFIG_FILE` is set to `renovate.json`, but Renovate CLI
  auto-discovers any of these locations.

- **PR comment tracking for non-compliance:** Out of scope for this
  implementation. Adding comments to existing PRs noting repeated
  non-compliance ("still invalid on check N") is a useful feature but
  applies to all file rules, not just Renovate. Tracked as future work
  for the checker engine.

- **Assertion pattern specificity:** Use the configured org name for a
  specific match pattern (e.g., `github>donaldgifford/renovate-config`)
  rather than a generic wildcard. This prevents repos from satisfying the
  assertion with a preset from a different org.

- **PR search terms for workflow rule:** No search terms needed on the
  workflow rule. The engine already detects open PRs via the deterministic
  `repo-guardian/add-missing-files` branch name, which covers both files.
  The config rule keeps its existing `["renovate"]` search terms from the
  current defaults.

## Dependencies

- DESIGN-0009 (approved or in review)
- Existing `workflow_sync` reconciler (already implemented, IMPL-0007)
- Existing assertion system (already implemented, IMPL-0007)
- Existing `check = "exact"` and `check = "contains"` modes (already
  implemented, IMPL-0006)

## References

- [DESIGN-0009: Distributed Renovate via Per-Repo GitHub Actions](../design/0009-distributed-renovate-via-per-repo-github-actions.md)
- [RFC-0002: HCL-driven Policy Engine](../rfc/0002-hcl-driven-policy-engine-for-repo-guardian.md)
- [IMPL-0006: HCL Policy Configuration and Rule Engine](0006-hcl-policy-configuration-and-rule-engine.md)
- [IMPL-0007: Additional Rule Types and Ignore Lists](0007-additional-rule-types-and-ignore-lists.md)
