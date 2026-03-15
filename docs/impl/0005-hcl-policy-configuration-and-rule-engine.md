---
id: IMPL-0005
title: "HCL Policy Configuration and Rule Engine"
status: Draft
author: Donald Gifford
created: 2026-03-15
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0005: HCL Policy Configuration and Rule Engine

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-03-15

## Objective

Implement the `internal/policy` package that parses HCL configuration files
into typed Go structs, and refactor the hardcoded `FileRule` registry into a
config-driven rule engine with three check modes (`exists`, `contains`,
`exact`) and typed content assertions.

**Implements:** DESIGN-0006

## Scope

### In Scope

- HCL parser using `hashicorp/hcl/v2` for `guardian.hcl`
- Go types: `PolicyConfig`, `GuardianConfig`, `FileRuleConfig`,
  `AssertionConfig`, `PRConfig`
- Config loading: single file and directory of `.hcl` files
- Config merge: built-in defaults → HCL → env var overrides
- Validation at load time with clear error messages
- Three check modes: `exists`, `contains`, `exact`
- Content assertions: `pattern`, `not_pattern`, `yaml_path` +
  `contains`/`equals`
- Minimal YAML path evaluator (dot paths + array wildcards)
- Refactor `main.go` wiring to use `PolicyConfig`
- Backward compatibility when no HCL config is present
- Helm chart `policy` ConfigMap support
- `GUARDIAN_CONFIG` env var

### Out of Scope

- Reconciler interface and types (IMPL-0006)
- Push event handler (IMPL-0006)
- Ignore lists (IMPL-0007)
- `rule "setting"` and `rule "branch_protection"` types (IMPL-0007)
- Additional reconciler implementations (IMPL-0007)

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its tasks
are checked off and its success criteria are met.

---

### Phase 1: Go Types and HCL Schema

Establish the foundational Go types and HCL schema definition. No parsing
logic yet -- just the data structures that everything else builds on.

#### Tasks

- [ ] Create `internal/policy/types.go` with all Go types:
  `PolicyConfig`, `GuardianConfig`, `FileRuleConfig`, `CheckMode` constants,
  `PRConfig`, `AssertionConfig`
- [ ] Add placeholder fields for future types: `IgnoreConfig`,
  `ReconcilerConfig` (empty structs, filled in IMPL-0006/0007)
- [ ] Add `hashicorp/hcl/v2` and `hashicorp/hcl/v2/hclsimple` to `go.mod`
- [ ] Add HCL struct tags to all types for schema binding
- [ ] Write unit tests validating Go type construction and zero-value
  defaults

#### Success Criteria

- `go build ./...` succeeds
- `make lint` passes
- Types compile with correct HCL struct tags
- Tests cover type construction

---

### Phase 2: Built-in Defaults

Create the default policy config that mirrors current hardcoded behavior,
ensuring backward compatibility when no HCL config is present.

#### Tasks

- [ ] Create `internal/policy/defaults.go` with `BuiltinDefaults() *PolicyConfig`
- [ ] Map current `rules.DefaultRules` to `FileRuleConfig` structs
  (CODEOWNERS, Dependabot, Renovate)
- [ ] Map current `config.Config` defaults to `GuardianConfig` fields
- [ ] Write unit tests asserting built-in defaults match current
  `DefaultRules` and `config.Load()` defaults exactly
- [ ] Write comparison test: `BuiltinDefaults().FileRules` produces the same
  rule names, paths, targets, and enabled states as `rules.DefaultRules`

#### Success Criteria

- `BuiltinDefaults()` returns a config equivalent to current hardcoded
  behavior
- All three default rules (CODEOWNERS, Dependabot, Renovate) are represented
- All `GuardianConfig` fields have defaults matching current env var defaults
- `make test` passes

---

### Phase 3: HCL Parser and Config Loader

Implement the core `Load` function that reads HCL files, merges with
defaults, applies env var overrides, and validates the result.

#### Tasks

- [ ] Create `internal/policy/loader.go` with
  `Load(path string) (*PolicyConfig, error)`
- [ ] Implement single-file loading (`.hcl` file)
- [ ] Implement directory loading (all `*.hcl` files, lexicographic order)
- [ ] Implement env var override layer for `GuardianConfig` fields (use
  same env var names as current `config.Config`)
- [ ] Implement merge logic: built-in defaults, then HCL overrides
- [ ] Handle `GUARDIAN_CONFIG` unset or path not found → return
  `BuiltinDefaults()`
- [ ] Support HCL variables and `locals {}` blocks
- [ ] Write unit tests:
  - Valid HCL file parses correctly
  - Directory with multiple `.hcl` files merges correctly
  - Duplicate rule names across files produce validation error
  - Env var overrides take precedence over HCL values
  - Missing config path returns built-in defaults
  - HCL syntax errors produce clear error messages
  - `locals {}` blocks resolve correctly

#### Success Criteria

- `Load("/path/to/guardian.hcl")` returns correct `PolicyConfig`
- `Load("/path/to/dir/")` loads and merges all `.hcl` files
- `Load("")` returns built-in defaults
- Env vars override HCL values
- `make test` passes

---

### Phase 4: Config Validation

Add comprehensive validation at load time to catch configuration errors
early with clear, actionable error messages.

#### Tasks

- [ ] Create `internal/policy/validate.go` with
  `Validate(cfg *PolicyConfig) error`
- [ ] Validate `FileRuleConfig`:
  - `check` must be one of `exists`, `contains`, `exact`
  - `paths` must be non-empty
  - `target` must be non-empty
  - `template` must be non-empty
  - Assertions require `check = "contains"`
  - `pattern` and `yaml_path` are mutually exclusive
  - `yaml_path` requires either `contains` or `equals`
  - `message` is required for all assertions
- [ ] Validate `GuardianConfig`:
  - `worker_count` > 0
  - `queue_size` > 0
  - `rate_limit_threshold` between 0.0 and 1.0
  - `log_level` is one of debug, info, warn, error
- [ ] Validate no duplicate rule names (same type + name)
- [ ] Write unit tests for each validation rule with both valid and invalid
  inputs
- [ ] Write unit tests for error message clarity (errors should name the
  field and explain the constraint)

#### Success Criteria

- All invalid configs produce clear, specific error messages
- Valid configs pass validation without error
- Validation runs as part of `Load()` before returning
- `make test` and `make lint` pass

---

### Phase 5: YAML Path Evaluator

Build the minimal YAML path evaluator for content assertions on structured
files. Uses `gopkg.in/yaml.v3` (already in `go.mod`).

#### Tasks

- [ ] Create `internal/policy/yamlpath.go` with
  `EvaluateYAMLPath(content string, path string) ([]string, error)`
- [ ] Support dot-separated paths: `spec.owner`, `metadata.name`
- [ ] Support literal slashes in keys:
  `metadata.annotations.jira/project-key`
- [ ] Support array wildcards: `updates[*].package-ecosystem`
- [ ] Return all matching values as `[]string`
- [ ] Return clear errors for invalid YAML or invalid path expressions
- [ ] Write table-driven unit tests covering:
  - Simple dot paths
  - Nested dot paths
  - Keys containing slashes
  - Array wildcard paths
  - Non-existent paths (empty result, no error)
  - Invalid YAML content
  - Invalid path syntax

#### Success Criteria

- All YAML path expressions from DESIGN-0006 table work correctly
- No external YAML path library dependency (just `gopkg.in/yaml.v3`)
- `make test` and `make lint` pass

---

### Phase 6: Content Assertions

Implement the assertion evaluation engine that runs when `check = "contains"`
and the file exists.

#### Tasks

- [ ] Create `internal/policy/assertion.go` with
  `(a *AssertionConfig) Evaluate(content string, filePath string) error`
- [ ] Implement `pattern` assertion: compile regex, match against content
- [ ] Implement `not_pattern` assertion: compile regex, fail if matched
- [ ] Implement `yaml_path` + `contains` assertion: evaluate path, check
  if any value contains the string
- [ ] Implement `yaml_path` + `equals` assertion: evaluate path, check
  if any value equals the string
- [ ] Return error with `message` field when assertion fails
- [ ] Pre-compile regexes at config load time (not per-evaluation)
- [ ] Write unit tests:
  - Regex match passes and fails
  - Regex not_pattern passes and fails
  - YAML path contains passes and fails
  - YAML path equals passes and fails
  - Multiple assertions all pass
  - Multiple assertions with one failure
  - Clear error messages include the assertion `message` field

#### Success Criteria

- All assertion types evaluate correctly
- Assertions return user-friendly failure messages
- Regexes are validated at config load time
- `make test` and `make lint` pass

---

### Phase 7: Rule Engine Refactor

Refactor `internal/checker/engine.go` to use `PolicyConfig` instead of the
hardcoded `rules.Registry`. Implement the three check modes.

#### Tasks

- [ ] Modify `checker.Engine` to accept `*policy.PolicyConfig` instead of
  `*rules.Registry`
- [ ] Refactor `CheckRepo` to iterate over `PolicyConfig.FileRules`
- [ ] Implement `exists` mode: same as current behavior (check file
  existence, PR if missing)
- [ ] Implement `contains` mode: check existence → if present, run
  assertions → if assertions fail, create PR to replace with template
- [ ] Implement `exact` mode: check existence → if present, compare against
  template → YAML semantic comparison for `.yml`/`.yaml` files, byte
  comparison otherwise → create PR if mismatch
- [ ] Add YAML semantic diff helper (parse both, compare parsed structures)
- [ ] Update `findMissingFiles` to handle the new check modes
- [ ] Preserve existing metrics (`files_missing_total`, `prs_created_total`,
  etc.)
- [ ] Maintain backward compatibility: when using built-in defaults, behavior
  is identical to current
- [ ] Write unit tests:
  - `exists` mode: file missing → PR created
  - `exists` mode: file present → no action
  - `contains` mode: file missing → PR created
  - `contains` mode: file present, assertions pass → no action
  - `contains` mode: file present, assertions fail → PR created
  - `exact` mode: file missing → PR created
  - `exact` mode: file matches template → no action
  - `exact` mode: file differs from template → PR created
  - `exact` mode: YAML files use semantic comparison
  - Backward compatibility with `BuiltinDefaults()`

#### Success Criteria

- All three check modes work correctly
- Existing tests continue to pass
- Backward compatibility with default rules
- Metrics are preserved
- `make test` and `make lint` pass

---

### Phase 8: Main Wiring and Config Integration

Update `cmd/repo-guardian/main.go` to load policy config and wire it through
the application. Trim `config.Config` to only hold credentials.

#### Tasks

- [ ] Add `GUARDIAN_CONFIG` to `config.Config` (path to HCL file/dir)
- [ ] Call `policy.Load()` in `main.go` after `config.Load()`
- [ ] Pass `PolicyConfig.Guardian` fields to components that currently read
  from `config.Config` (engine, scheduler, queue)
- [ ] Remove operational fields from `config.Config` that are now in
  `GuardianConfig` (keep credentials-only)
- [ ] Update `checker.NewEngine` signature to accept `*policy.PolicyConfig`
- [ ] Update `scheduler.NewScheduler` to use `GuardianConfig` fields
- [ ] Update `checker.NewQueue` to use `GuardianConfig.QueueSize`
- [ ] Log the config source at startup (HCL file path or "built-in defaults")
- [ ] Write integration test: `config.Load()` + `policy.Load()` → engine
  creation → no errors

#### Success Criteria

- `main.go` compiles and runs with both HCL config and without
- Existing env vars continue to work as overrides
- Credential config remains env-var-only
- `make ci` passes (lint + test + build)

---

### Phase 9: Helm Chart Policy ConfigMap

Add Helm chart support for mounting the HCL policy file via ConfigMap.

#### Tasks

- [ ] Add `policy.config` and `policy.existingConfigMap` to `values.yaml`
- [ ] Create `templates/policy-configmap.yaml` (rendered when
  `policy.config` is set)
- [ ] Update `templates/deployment.yaml` to mount policy ConfigMap at
  `/etc/repo-guardian/guardian.hcl`
- [ ] Set `GUARDIAN_CONFIG` env var in deployment when policy ConfigMap is
  present
- [ ] Write helm-unittest tests:
  - Policy ConfigMap renders correctly with inline config
  - Policy ConfigMap not rendered when `policy.config` is empty
  - External ConfigMap used when `policy.existingConfigMap` is set
  - Deployment volume mount present when policy is configured
  - `GUARDIAN_CONFIG` env var set correctly
- [ ] Update `ci/ci-values.yaml` if needed for chart-testing

#### Success Criteria

- `helm template` renders correctly with policy config
- `helm template` renders correctly without policy config (backward compat)
- helm-unittest tests pass
- `make lint` passes (includes chart linting)

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/policy/types.go` | Create | Go types with HCL struct tags |
| `internal/policy/defaults.go` | Create | Built-in defaults matching current behavior |
| `internal/policy/loader.go` | Create | HCL parser, file/dir loading, env var merge |
| `internal/policy/validate.go` | Create | Config validation with clear errors |
| `internal/policy/yamlpath.go` | Create | Minimal YAML path evaluator |
| `internal/policy/assertion.go` | Create | Content assertion evaluation |
| `internal/policy/types_test.go` | Create | Type construction tests |
| `internal/policy/defaults_test.go` | Create | Default equivalence tests |
| `internal/policy/loader_test.go` | Create | Parser and loader tests |
| `internal/policy/validate_test.go` | Create | Validation tests |
| `internal/policy/yamlpath_test.go` | Create | YAML path evaluator tests |
| `internal/policy/assertion_test.go` | Create | Assertion evaluation tests |
| `internal/checker/engine.go` | Modify | Refactor to use PolicyConfig |
| `internal/checker/engine_test.go` | Modify | Add check mode tests |
| `internal/config/config.go` | Modify | Add GUARDIAN_CONFIG, trim operational fields |
| `cmd/repo-guardian/main.go` | Modify | Wire policy.Load() into startup |
| `go.mod` | Modify | Add hashicorp/hcl/v2 |
| `charts/repo-guardian/values.yaml` | Modify | Add policy.config section |
| `charts/repo-guardian/templates/policy-configmap.yaml` | Create | Policy ConfigMap template |
| `charts/repo-guardian/templates/deployment.yaml` | Modify | Mount policy ConfigMap |

## Testing Plan

- [ ] Unit tests for all exported functions in `internal/policy/`
- [ ] Table-driven tests for YAML path evaluator
- [ ] Table-driven tests for assertion evaluation
- [ ] Table-driven tests for config validation
- [ ] Integration tests: HCL file → PolicyConfig → engine
- [ ] Backward compatibility tests: no HCL → identical behavior
- [ ] Helm-unittest tests for policy ConfigMap

## Dependencies

- `hashicorp/hcl/v2` — HCL parser (new dependency)
- `gopkg.in/yaml.v3` — already in go.mod, used for YAML path evaluator
- DESIGN-0006 — design document (completed)

## Resolved Questions

1. **Config trimming scope:** Fully trim `config.Config` of operational
   fields in Phase 8. No compatibility shim — clean cut. `config.Config`
   keeps only credentials (`GitHubAppID`, private key, webhook secret).

2. **HCL library choice:** Use `hclsimple` as the primary parsing API.
   Both `hclsimple` and `gohcl` are subpackages of `github.com/hashicorp/hcl/v2`
   — single dependency. Pull in other parts of the library as needed
   (e.g., `hclparse` for better diagnostics).

3. **Regex compilation caching:** Use a separate compiled assertion type.
   Regexes are compiled at config load time into a distinct type — compile
   errors are caught early, and the type is independently testable and
   mockable.

## References

- [DESIGN-0006: HCL Policy Configuration and Rule Engine](../design/0006-hcl-policy-configuration-and-rule-engine.md)
- [RFC-0002: HCL-driven Policy Engine](../rfc/0002-hcl-driven-policy-engine-for-repo-guardian.md)
- Current rule registry: `internal/rules/registry.go`
- Current config: `internal/config/config.go`
- Current checker engine: `internal/checker/engine.go`
