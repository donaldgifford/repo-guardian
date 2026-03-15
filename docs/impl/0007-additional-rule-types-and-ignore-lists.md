---
id: IMPL-0007
title: "Additional Rule Types and Ignore Lists"
status: Draft
author: Donald Gifford
created: 2026-03-15
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0007: Additional Rule Types and Ignore Lists

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-03-15

## Objective

Implement global and per-rule ignore lists with glob pattern matching,
`rule "setting"` and `rule "branch_protection"` types, and the `label_sync`,
`branch_protection`, and `workflow_sync` reconciler implementations.

**Implements:** DESIGN-0008

## Scope

### In Scope

- Global ignore lists (skip repos for all rules)
- Per-rule ignore lists (skip specific rules for specific repos)
- Glob pattern matching via `path.Match`
- `rule "setting"` type for repo settings
- `rule "branch_protection"` type for branch protection configuration
- `label_sync` reconciler
- `branch_protection` reconciler
- `workflow_sync` reconciler
- New GitHub client interface methods
- New Prometheus metrics

### Out of Scope

- Multi-org support
- Custom rule type plugins
- Ignore lists based on repo metadata (topics, visibility, team)
- Hot-reloadable config

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its tasks
are checked off and its success criteria are met.

---

### Phase 1: Ignore Lists

Implement global and per-rule ignore lists with glob pattern matching. This
is the lowest-risk addition and immediately useful.

#### Tasks

- [x] Populate `IgnoreConfig` struct in `internal/policy/types.go` (currently
  a placeholder from IMPL-0005):
  - `Repos []string` field with HCL struct tags
- [x] Create `internal/policy/ignore.go` with:
  - `(ic *IgnoreConfig) Matches(ownerRepo string) bool`
  - Use `path.Match` for glob pattern matching
  - Input is always `owner/repo` format
- [x] Add `ignore {}` block support to HCL parser for:
  - Top-level global ignore (in `PolicyConfig.IgnoreList`)
  - Per-rule ignore (in `FileRuleConfig.Ignore`)
- [x] Integrate into checker engine `CheckRepo`:
  - Check global ignore list once per repo (before any rules)
  - Check per-rule ignore list before each rule evaluation
  - Increment `repo_guardian_ignored_total{scope="global"}` or
    `{scope="rule"}` metric
- [x] Add `repo_guardian_ignored_total` counter metric with `scope` label
- [x] Write unit tests:
  - Exact match: `myorg/my-repo` matches `myorg/my-repo`
  - Glob wildcard: `myorg/terraform-vpc` matches `myorg/terraform-*`
  - No match: `myorg/my-repo` doesn't match `myorg/other-*`
  - Character set: `myorg/repo-[abc]` matches correctly
  - Global ignore skips all rules
  - Per-rule ignore skips only that rule
  - Empty ignore list: no repos skipped
  - Case sensitivity: matching is case-sensitive

#### Success Criteria

- Global ignore list prevents all rules from running on matched repos
- Per-rule ignore list prevents only that rule on matched repos
- Glob patterns work with `*`, `?`, `[abc]` syntax
- Ignored repos are counted in metrics
- `make test` and `make lint` pass

---

### Phase 2: GitHub Client Extensions

Add new methods to the GitHub client interface needed by setting rules,
branch protection rules, and reconcilers.

#### Tasks

- [x] Add to `github.Client` interface:
  - `GetVulnerabilityAlertsEnabled(ctx, owner, repo) (bool, error)`
  - `EnableVulnerabilityAlerts(ctx, owner, repo) error`
  - `DisableVulnerabilityAlerts(ctx, owner, repo) error`
  - `UpdateRepository(ctx, owner, repo, opts) error`
  - `GetFileContent(ctx, owner, repo, path) (string, error)`
- [x] Add to `github.Client` interface (rulesets):
  - `ListRepositoryRulesets(ctx, owner, repo) ([]*Ruleset, error)`
  - `GetRepositoryRuleset(ctx, owner, repo, rulesetID) (*Ruleset, error)`
  - `CreateRepositoryRuleset(ctx, owner, repo, ruleset) (*Ruleset, error)`
  - `UpdateRepositoryRuleset(ctx, owner, repo, rulesetID, ruleset) (*Ruleset, error)`
- [x] Add to `github.Client` interface (labels):
  - `ListLabels(ctx, owner, repo) ([]*Label, error)`
  - `CreateLabel(ctx, owner, repo, label) error`
  - `UpdateLabel(ctx, owner, repo, name, label) error`
  - `DeleteLabel(ctx, owner, repo, name) error`
- [x] Define `Ruleset` and `Label` types in `internal/github/types.go`
- [x] Implement all methods in `internal/github/client.go`
- [x] Update mock client in tests to implement new interface methods
- [x] Write unit tests for each new method using `httptest.Server`

#### Success Criteria

- All new methods compile and implement the interface
- Mock client updated for tests
- `make test` and `make lint` pass

---

### Phase 3: Setting Rules

Implement `rule "setting"` for checking and optionally remediating GitHub
repository settings.

#### Tasks

- [x] Add `SettingRuleConfig` to `internal/policy/types.go`:
  - Name, Enabled, Property, Expected (interface{}), Remediate, Ignore
  - HCL struct tags for the `rule "setting"` block
- [x] Add `SettingRules []SettingRuleConfig` to `PolicyConfig`
- [x] Update HCL parser to handle `rule "setting" "<name>" {}` blocks
- [x] Update validation to check:
  - `property` is one of the supported properties
  - `expected` type matches property type (bool or string)
- [x] Implement setting rule evaluation in checker engine:
  - Read current value from GitHub API
  - Compare against expected
  - Match → log, increment `settings_checked_total`
  - Mismatch + `remediate=false` → log, increment `settings_mismatched_total`
  - Mismatch + `remediate=true` + not dry_run → set via API, increment
    `settings_remediated_total`
  - Mismatch + `remediate=true` + dry_run → log "would remediate"
- [x] Add supported properties:
  - `vulnerability_alerts_enabled` (bool)
  - `default_branch` (string)
  - `has_issues` (bool)
  - `has_wiki` (bool)
  - `delete_branch_on_merge` (bool)
  - `allow_merge_commit` (bool)
  - `allow_squash_merge` (bool)
  - `allow_rebase_merge` (bool)
- [x] Add Prometheus metrics:
  - `repo_guardian_settings_checked_total{rule_name}`
  - `repo_guardian_settings_remediated_total{rule_name}`
  - `repo_guardian_settings_mismatched_total{rule_name}`
- [x] Write unit tests:
  - Setting matches expected: no action
  - Setting mismatches, remediate=false: logs mismatch
  - Setting mismatches, remediate=true: calls API to fix
  - Setting mismatches, remediate=true, dry_run: logs "would remediate"
  - Unsupported property: validation error at load time
  - Each supported property type works correctly

#### Success Criteria

- Setting rules check and optionally remediate repo settings
- All 8 supported properties work
- Metrics track checks, mismatches, and remediations
- Validation catches unsupported properties
- `make test` and `make lint` pass

---

### Phase 4: Branch Protection Rules

Implement `rule "branch_protection"` for checking and optionally enforcing
branch protection settings using the repository rulesets API.

#### Tasks

- [x] Add `BranchProtectionRuleConfig` to `internal/policy/types.go`:
  - Name, Enabled, Branch, RequirePR, RequiredApprovals,
    DismissStaleReviews, RequireStatusChecks, EnforceAdmins,
    RequireLinearHistory, Ignore, Reconcilers
  - HCL struct tags for `rule "branch_protection"` block
- [x] Add `BranchProtectionRules []BranchProtectionRuleConfig` to
  `PolicyConfig`
- [x] Update HCL parser to handle `rule "branch_protection" "<name>" {}`
  blocks
- [x] Implement branch protection rule evaluation:
  - Read current protection via rulesets API
  - Compare each field against expected
  - All match → log, increment `branch_protection_checked_total`
  - Mismatch → remediate flow (same pattern as setting rules)
- [x] Map `BranchProtectionRuleConfig` fields to ruleset API request
  format
- [x] Handle "branch doesn't exist" case: log warning, no error
- [x] Add Prometheus metrics:
  - `repo_guardian_branch_protection_checked_total{rule_name}`
  - `repo_guardian_branch_protection_remediated_total{rule_name}`
- [x] Write unit tests:
  - Protection matches: no action
  - Protection mismatches: logs differences
  - Protection with remediation: updates via API
  - Branch doesn't exist: logs warning
  - Multiple protection fields checked
  - Dry run mode

#### Success Criteria

- Branch protection rules check settings via rulesets API
- Remediation creates/updates rulesets
- Missing branches handled gracefully
- `make test` and `make lint` pass

---

### Phase 5: Label Sync Reconciler

Implement the `label_sync` reconciler that syncs repository labels from a
YAML configuration file.

#### Tasks

- [x] Create `internal/reconciler/label_sync.go` implementing `Reconciler`:
  - `NewLabelSyncReconciler(config ReconcilerConfig) (Reconciler, error)`
  - `Name()` returns `"label_sync"`
  - `Reconcile(ctx, params)` implements the sync flow
- [x] Define label file YAML schema:
  - `labels: [{name, color, description, renamed_from}]`
- [x] Implement reconcile flow:
  1. Parse file content as YAML label list
  2. List current repo labels via GitHub API
  3. Process renames first (`renamed_from` where old name exists)
  4. Diff desired vs current
  5. Create missing labels
  6. Update changed labels (color or description mismatch)
  7. If `delete_extra = true`, delete labels not in the file
- [x] Add `LabelSyncConfig` fields to `ReconcilerConfig`:
  - `DeleteExtra bool`
- [x] Register `label_sync` factory in `NewRegistry()`
- [x] Write unit tests:
  - Creates missing labels
  - Updates labels with changed color/description
  - Renames labels via `renamed_from`
  - Deletes extra labels when `delete_extra = true`
  - Keeps extra labels when `delete_extra = false`
  - Handles empty label file
  - Handles invalid YAML
  - Dry run mode: logs changes without making them

#### Success Criteria

- Label sync creates, updates, renames, and optionally deletes labels
- `renamed_from` preserves issue/PR associations
- `make test` and `make lint` pass

---

### Phase 6: Branch Protection Reconciler

Implement the `branch_protection` reconciler that reads protection settings
from a YAML file and applies them via the rulesets API.

#### Tasks

- [ ] Create `internal/reconciler/branch_protection.go` implementing
  `Reconciler`:
  - `NewBranchProtectionReconciler(config ReconcilerConfig) (Reconciler, error)`
  - `Name()` returns `"branch_protection"`
  - `Reconcile(ctx, params)` reads YAML file and applies settings
- [ ] Define branch protection YAML schema (mirrors
  `BranchProtectionRuleConfig` fields)
- [ ] Implement reconcile flow:
  1. Parse file content as YAML
  2. Read current rulesets via API
  3. Diff desired vs current
  4. Create or update rulesets as needed
- [ ] Register `branch_protection` factory in `NewRegistry()`
- [ ] Write unit tests:
  - Valid YAML → correct rulesets created
  - Existing rulesets updated when settings change
  - No changes when settings match
  - Invalid YAML produces clear error
  - Dry run mode

#### Success Criteria

- Branch protection reconciler reads YAML and applies via rulesets API
- `make test` and `make lint` pass

---

### Phase 7: Workflow Sync Reconciler

Implement the `workflow_sync` reconciler that detects workflow file changes
and triggers rescans.

#### Tasks

- [ ] Create `internal/reconciler/workflow_sync.go` implementing
  `Reconciler`:
  - `NewWorkflowSyncReconciler(config ReconcilerConfig) (Reconciler, error)`
  - `Name()` returns `"workflow_sync"`
  - `Reconcile(ctx, params)` detects file mismatch
- [ ] The reconciler is primarily useful for the `watch` capability
  (push event → rescan → template comparison via `exact` check mode)
- [ ] Register `workflow_sync` factory in `NewRegistry()`
- [ ] Write unit tests:
  - Reconciler runs without error when file matches template
  - Reconciler logs when file differs from template
  - Dry run mode

#### Success Criteria

- Workflow sync reconciler integrates with `watch` + `exact` mode
- `make test` and `make lint` pass

---

### Phase 8: Integration and Cleanup

End-to-end testing and documentation updates.

#### Tasks

- [ ] Write integration test: HCL config with ignore lists + setting rules
  + branch protection → engine → mock GitHub client → correct API calls
- [ ] Write integration test: label_sync reconciler end-to-end with mock
  GitHub client
- [ ] Verify all new metrics appear in `/metrics` endpoint
- [ ] Update `CLAUDE.md` architecture section if needed
- [ ] Run `make ci` (lint + test + build)
- [ ] Verify backward compatibility: no HCL config → identical behavior

#### Success Criteria

- `make ci` passes
- All new features work end-to-end
- Backward compatibility preserved
- Metrics observable

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/policy/types.go` | Modify | Add IgnoreConfig, SettingRuleConfig, BranchProtectionRuleConfig |
| `internal/policy/ignore.go` | Create | Ignore list matching with glob |
| `internal/policy/ignore_test.go` | Create | Ignore list tests |
| `internal/policy/loader.go` | Modify | Parse setting/branch_protection rule blocks |
| `internal/policy/validate.go` | Modify | Validate new rule types |
| `internal/github/client.go` | Modify | Add new API methods |
| `internal/github/types.go` | Modify | Add Ruleset, Label types |
| `internal/checker/engine.go` | Modify | Integrate ignore lists, setting rules, branch protection rules |
| `internal/metrics/metrics.go` | Modify | Add new counters |
| `internal/reconciler/label_sync.go` | Create | Label sync reconciler |
| `internal/reconciler/branch_protection.go` | Create | Branch protection reconciler |
| `internal/reconciler/workflow_sync.go` | Create | Workflow sync reconciler |
| `internal/reconciler/label_sync_test.go` | Create | Label sync tests |
| `internal/reconciler/branch_protection_test.go` | Create | Branch protection tests |
| `internal/reconciler/workflow_sync_test.go` | Create | Workflow sync tests |

## Testing Plan

- [ ] Unit tests for ignore list matching (exact, glob, edge cases)
- [ ] Unit tests for all new GitHub client methods
- [ ] Unit tests for setting rule evaluation (all supported properties)
- [ ] Unit tests for branch protection rule evaluation
- [ ] Unit tests for label sync reconciler (create, update, rename, delete)
- [ ] Unit tests for branch protection reconciler
- [ ] Unit tests for workflow sync reconciler
- [ ] Integration tests for ignore lists in CheckRepo flow
- [ ] Integration tests for end-to-end reconciler flows

## Dependencies

- IMPL-0005 (HCL Policy Configuration) — must be completed first
- IMPL-0006 (Reconciler Interface) — must be completed first
- DESIGN-0008 — design document (completed)
- GitHub rulesets API documentation

## Resolved Questions

1. **Ruleset API vs legacy branch protection API:** Rulesets only. GitHub's
   recommended path forward. If an org doesn't have access, that's a
   problem for later.

2. **Label sync conflict resolution:** Skip the rename and log a warning.
   No destructive action on conflicts — if both the source and target
   label names exist, the rename is silently skipped with a warning log.

3. **Setting rule property extensibility:** Curated list with type checking.
   Properties are validated against known types at config load time, similar
   to Terraform variable validation blocks. Keeps things safe and typed.

4. **Ignore list case sensitivity:** Normalize to lowercase before matching.
   GitHub repo names are case-insensitive, so `path.Match` input is
   lowercased to match that behavior.

## References

- [DESIGN-0008: Additional Rule Types and Ignore Lists](../design/0008-additional-rule-types-and-ignore-lists.md)
- [RFC-0002: HCL-driven Policy Engine](../rfc/0002-hcl-driven-policy-engine-for-repo-guardian.md)
- [GitHub Repository Rulesets API](https://docs.github.com/en/rest/repos/rules)
- [GitHub Labels API](https://docs.github.com/en/rest/issues/labels)
- Current GitHub client: `internal/github/client.go`
- Current metrics: `internal/metrics/metrics.go`
