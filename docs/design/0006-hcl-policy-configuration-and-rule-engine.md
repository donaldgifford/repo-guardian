---
id: DESIGN-0006
title: "HCL Policy Configuration and Rule Engine"
status: Draft
author: Donald Gifford
created: 2026-03-15
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0006: HCL Policy Configuration and Rule Engine

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-03-15

## Overview

Introduce an `internal/policy` package that parses HCL configuration files
into typed Go structs, and refactor the hardcoded `FileRule` registry into a
config-driven rule engine with three check modes (`exists`, `contains`,
`exact`) and typed content assertions. This is the foundation for the
HCL-driven policy engine described in RFC-0002.

**Implements:** RFC-0002 Phases 1-2

## Goals and Non-Goals

### Goals

- Parse `guardian.hcl` (single file or directory of `.hcl` files) into typed
  Go config structs
- Validate HCL config at load time with clear error messages
- Merge config: built-in defaults, then HCL overrides, then env var overrides
- Replace the hardcoded `FileRule` registry with a config-driven rule engine
- Support three check modes for file rules: `exists`, `contains`, `exact`
- Support typed content assertions: regex for plaintext, `yaml_path` for
  YAML files
- Maintain full backward compatibility when no HCL config is present
- Mount `guardian.hcl` via Helm chart ConfigMap (inline or external)

### Non-Goals

- Reconciler interface and implementation (DESIGN-0007)
- Push event handling (DESIGN-0007)
- Ignore lists (DESIGN-0008)
- `rule "setting"` and `rule "branch_protection"` types (DESIGN-0008)
- Multi-org support (future work beyond RFC-0002)

## Background

repo-guardian's file rules are currently hardcoded Go structs in
`internal/rules/registry.go`:

```go
var DefaultRules = []FileRule{
    {Name: "CODEOWNERS", Paths: [...], Enabled: true, ...},
    {Name: "Dependabot", Paths: [...], Enabled: true, ...},
    {Name: "Renovate",   Paths: [...], Enabled: false, ...},
}
```

Adding a new rule requires Go code changes, recompilation, and redeployment.
Rules only check file presence -- there is no content validation.

Operational settings are env-var-only (`internal/config/config.go`), which
works but doesn't compose with rule definitions, per-repo overrides, or
structured configuration.

## Detailed Design

### HCL Schema

#### Guardian Block (Operational Settings)

```hcl
guardian {
  dry_run                       = false    # BOOL, env: DRY_RUN
  schedule_interval             = "168h"   # DURATION, env: SCHEDULE_INTERVAL
  worker_count                  = 5        # INT, env: WORKER_COUNT
  queue_size                    = 1000     # INT, env: QUEUE_SIZE
  log_level                     = "info"   # STRING, env: LOG_LEVEL
  skip_forks                    = true     # BOOL, env: SKIP_FORKS
  skip_archived                 = true     # BOOL, env: SKIP_ARCHIVED
  rate_limit_threshold          = 0.10     # FLOAT, env: RATE_LIMIT_THRESHOLD
  webhook_ip_allowlist          = true     # BOOL, env: WEBHOOK_IP_ALLOWLIST
  webhook_ip_allowlist_fail_open = false   # BOOL, env: WEBHOOK_IP_ALLOWLIST_FAIL_OPEN
  trust_proxy_headers           = false    # BOOL, env: TRUST_PROXY_HEADERS
}
```

Every field has a built-in default (matching current env var defaults). Every
field can be overridden by its corresponding env var. The `guardian` block is
optional -- omitting it uses all defaults.

#### File Rule Block

```hcl
rule "file" "<name>" {
  enabled  = true                  # optional, default true
  check    = "exists"              # exists | contains | exact
  paths    = [...]                 # required, paths to check
  target   = "<path>"              # required, where to create if missing
  template = "<name>.tmpl"         # required, template file name

  pr {                             # optional
    search_terms = [...]           # terms to match existing PRs
  }

  assertion {                      # optional, repeatable, requires check = "contains"
    pattern     = "<regex>"        # regex match (plaintext files)
    not_pattern = "<regex>"        # regex must NOT match
    yaml_path   = "<path>"        # YAML path expression
    contains    = "<value>"        # yaml_path must contain value
    equals      = "<value>"        # yaml_path must equal value
    message     = "<string>"       # required, human-readable failure message
  }

  ignore { ... }                   # per-rule ignore (DESIGN-0008)
  reconcile "<type>" { ... }       # reconciler (DESIGN-0007)
}
```

### Go Types

```go
// internal/policy/types.go

// PolicyConfig is the top-level parsed configuration.
type PolicyConfig struct {
    Guardian    GuardianConfig
    IgnoreList  IgnoreConfig       // DESIGN-0008
    FileRules   []FileRuleConfig
    // Future: SettingRules, BranchProtectionRules (DESIGN-0008)
}

type GuardianConfig struct {
    DryRun                    bool
    ScheduleInterval          time.Duration
    WorkerCount               int
    QueueSize                 int
    LogLevel                  string
    SkipForks                 bool
    SkipArchived              bool
    RateLimitThreshold        float64
    WebhookIPAllowlist        bool
    WebhookIPAllowlistFailOpen bool
    TrustProxyHeaders         bool
}

type FileRuleConfig struct {
    Name        string
    Enabled     bool
    Check       CheckMode           // "exists", "contains", "exact"
    Paths       []string
    Target      string
    Template    string
    PR          PRConfig
    Assertions  []AssertionConfig
    Ignore      IgnoreConfig        // DESIGN-0008
    Reconcilers []ReconcilerConfig  // DESIGN-0007
}

type CheckMode string

const (
    CheckExists   CheckMode = "exists"
    CheckContains CheckMode = "contains"
    CheckExact    CheckMode = "exact"
)

type PRConfig struct {
    SearchTerms []string
}

type AssertionConfig struct {
    Pattern    string  // regex match
    NotPattern string  // regex must not match
    YAMLPath   string  // YAML path expression
    Contains   string  // yaml_path contains value
    Equals     string  // yaml_path equals value
    Message    string  // failure message (required)
}
```

### Config Loading

```go
// internal/policy/loader.go

// Load reads policy configuration from the given path (file or directory),
// merges with built-in defaults, and applies env var overrides.
func Load(path string) (*PolicyConfig, error)
```

Loading order:

1. Start with built-in defaults (equivalent to current `DefaultRules` +
   current env var defaults)
2. If `path` is a file, parse it. If `path` is a directory, parse all
   `.hcl` files in it and merge
3. Apply env var overrides to `GuardianConfig` fields
4. Validate the final config (check mode vs assertions, required fields,
   template references)

When `GUARDIAN_CONFIG` is unset or the path doesn't exist, `Load` returns
the built-in defaults -- the system behaves identically to today.

#### Directory Loading

When `GUARDIAN_CONFIG` points to a directory:

- All files matching `*.hcl` are loaded (non-recursive)
- Files are processed in lexicographic order for deterministic behavior
- HCL natively handles block merging across files
- Duplicate rule names (same type + name) across files are a validation error

#### Env Var Override Mapping

| HCL Field | Env Var | Type |
|-----------|---------|------|
| `guardian.dry_run` | `DRY_RUN` | bool |
| `guardian.schedule_interval` | `SCHEDULE_INTERVAL` | duration |
| `guardian.worker_count` | `WORKER_COUNT` | int |
| `guardian.queue_size` | `QUEUE_SIZE` | int |
| `guardian.log_level` | `LOG_LEVEL` | string |
| `guardian.skip_forks` | `SKIP_FORKS` | bool |
| `guardian.skip_archived` | `SKIP_ARCHIVED` | bool |
| `guardian.rate_limit_threshold` | `RATE_LIMIT_THRESHOLD` | float |
| `guardian.webhook_ip_allowlist` | `WEBHOOK_IP_ALLOWLIST` | bool |
| `guardian.webhook_ip_allowlist_fail_open` | `WEBHOOK_IP_ALLOWLIST_FAIL_OPEN` | bool |
| `guardian.trust_proxy_headers` | `TRUST_PROXY_HEADERS` | bool |

Env vars for credentials (`GITHUB_APP_ID`, `GITHUB_PRIVATE_KEY_PATH`,
`GITHUB_PRIVATE_KEY`, `GITHUB_WEBHOOK_SECRET`) remain env-var-only -- they
are never read from HCL.

### Rule Engine Refactor

The current flow:

```
main.go → rules.NewRegistry(rules.DefaultRules) → checker.NewEngine(registry, ...)
```

The new flow:

```
main.go → policy.Load(configPath) → rules built from PolicyConfig or defaults
        → checker.NewEngine(rules, ...)
```

The checker engine's `CheckRepo` method is refactored to work with the new
rule types:

- **`exists` mode** -- same as current behavior (check file existence, PR if
  missing)
- **`contains` mode** -- check file existence. If missing, PR it. If
  present, run assertions. If assertions fail, create a PR to replace with
  the template
- **`exact` mode** -- check file existence. If missing, PR it. If present,
  compare against template. YAML files use semantic comparison (parsed
  equality, ignoring comments and whitespace). Plaintext uses byte
  comparison. If mismatch, create a PR to update the file

#### Content Assertions

Assertions are evaluated when `check = "contains"` and the file exists:

```go
// internal/policy/assertion.go

// Evaluate runs the assertion against the given file content.
// Returns nil if the assertion passes, or an error describing the failure.
func (a *AssertionConfig) Evaluate(content string, filePath string) error
```

The assertion type is determined by which fields are set:

- `pattern` set → compile as regex, match against content
- `not_pattern` set → compile as regex, fail if matched
- `yaml_path` set → parse content as YAML, evaluate path expression,
  check `contains` or `equals`

Assertion field combinations are validated at config load time:

- `pattern` and `yaml_path` are mutually exclusive
- `yaml_path` requires either `contains` or `equals`
- `message` is required for all assertions

#### YAML Path Expressions

YAML path expressions use a simple dot-separated syntax with array wildcard
support:

| Expression | Meaning |
|------------|---------|
| `spec.owner` | Value at `spec.owner` |
| `metadata.name` | Value at `metadata.name` |
| `metadata.annotations.jira/project-key` | Value at nested key (slash is literal) |
| `updates[*].package-ecosystem` | All values in array field |

This is a minimal subset of JSONPath/yq syntax -- enough for the current use
cases without pulling in a full JSONPath library.

### Template Store Changes

The existing `TemplateStore` continues to work as-is. Templates are loaded
from the `TEMPLATE_DIR` directory with embedded fallbacks. The HCL config
references templates by name (e.g., `template = "codeowners.tmpl"`), which
maps to the template store's key (strip `.tmpl` suffix).

New templates for new rules are added to the template directory -- no code
changes needed.

### Integration with Existing Config Package

The existing `internal/config` package remains for credential loading and
validation (`GITHUB_APP_ID`, private key, webhook secret, IP allowlist
settings). The new `internal/policy` package handles rule definitions and
operational settings.

The `config.Config` struct is trimmed: fields that move to `GuardianConfig`
(like `DryRun`, `WorkerCount`, `ScheduleInterval`, etc.) are read from the
policy config instead. A compatibility layer ensures `config.Load()` still
works when no HCL file is present.

### Helm Chart Changes

Add a new ConfigMap for the policy file:

```yaml
# values.yaml
policy:
  # -- Inline HCL policy configuration
  config: ""
  # -- Use an existing ConfigMap for the policy file
  existingConfigMap: ""
```

When `policy.config` is set, the chart creates a ConfigMap:

```yaml
# templates/policy-configmap.yaml
{{- if .Values.policy.config }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "repo-guardian.fullname" . }}-policy
data:
  guardian.hcl: |
    {{ .Values.policy.config | nindent 4 }}
{{- end }}
```

The deployment mounts the policy ConfigMap at
`/etc/repo-guardian/guardian.hcl` and sets `GUARDIAN_CONFIG` to that path.

## API / Interface Changes

### New Environment Variable

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `GUARDIAN_CONFIG` | No | `/etc/repo-guardian/guardian.hcl` | Path to HCL config file or directory |

### Changed Behavior

- When `GUARDIAN_CONFIG` points to a valid file/directory, rules are loaded
  from HCL instead of built-in defaults
- When `GUARDIAN_CONFIG` is unset or the path doesn't exist, behavior is
  identical to today
- Env vars continue to override all operational settings

## Data Model

No database or persistent storage changes. The `PolicyConfig` is an
in-memory struct loaded at startup.

## Testing Strategy

### Unit Tests

- **HCL parsing:** Valid configs parse correctly, invalid configs produce
  clear errors
- **Default fallback:** No config file → built-in defaults match current
  behavior
- **Env var override:** HCL values are overridden by env vars
- **Directory loading:** Multiple `.hcl` files merge correctly, duplicate
  rules error
- **Validation:** Invalid check modes, missing required fields, invalid
  assertion combinations
- **Assertion evaluation:** Regex match/no-match, YAML path extraction,
  contains/equals comparisons
- **Check modes:** `exists` skips content, `contains` runs assertions,
  `exact` compares against template
- **YAML semantic diff:** Comment/whitespace changes don't trigger updates
- **Backward compatibility:** Existing `DefaultRules` produce identical
  behavior when loaded as the default policy

### Integration Tests

- **End-to-end config loading:** `guardian.hcl` → `PolicyConfig` →
  checker engine → mock GitHub client → expected API calls
- **Helm template tests:** Policy ConfigMap renders correctly with inline
  and external ConfigMap values

## Migration / Rollout Plan

1. Ship the `internal/policy` package with HCL parsing and the refactored
   rule engine
2. Existing deployments continue to work unchanged (no `GUARDIAN_CONFIG`
   set, built-in defaults used)
3. Users opt in by creating a `guardian.hcl` and setting `GUARDIAN_CONFIG`
4. Document migration guide: show equivalent HCL for current env var config
5. Helm chart updated to support `policy.config` and
   `policy.existingConfigMap`

## Resolved Questions

1. **YAML path evaluator:** Write a minimal evaluator (~200 lines) for dot
   paths + array wildcards. No external dependency. The subset we need is
   small enough to own.

2. **`exact` mode with template variables:** `exact` mode only works with
   static templates (no placeholders). Templates with variables (like
   `catalog-info.tmpl`) should use `exists` or `contains` with assertions
   instead.

3. **`guardian` block scope:** All operational settings go in the `guardian`
   block, including `rate_limit_threshold`, `webhook_ip_allowlist`,
   `webhook_ip_allowlist_fail_open`, and `trust_proxy_headers`. The HCL
   file is the source of truth; env vars are overrides.

4. **HCL variables and locals:** Supported from the start. Full HCL
   expression evaluation including `locals {}` blocks for DRY configs.

## References

- [RFC-0002: HCL-driven Policy Engine](../rfc/0002-hcl-driven-policy-engine-for-repo-guardian.md)
- [DESIGN-0007: Reconciler Interface and Push Event Handler](0007-reconciler-interface-and-push-event-handler.md)
- [DESIGN-0008: Additional Rule Types and Ignore Lists](0008-additional-rule-types-and-ignore-lists.md)
- [hashicorp/hcl v2](https://github.com/hashicorp/hcl)
- Current rule registry: `internal/rules/registry.go`
- Current config: `internal/config/config.go`
