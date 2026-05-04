---
id: DESIGN-0013
title: "Customizable PR templates and extensible template ConfigMap"
status: Implemented
author: Donald Gifford
created: 2026-05-03
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0013: Customizable PR templates and extensible template ConfigMap

**Status:** Implemented
**Author:** Donald Gifford
**Date:** 2026-05-03

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [Unified template renderer](#unified-template-renderer)
  - [Variable contexts](#variable-contexts)
  - [Helpers (curated subset)](#helpers-curated-subset)
  - [Render-time behavior](#render-time-behavior)
  - [HCL grammar additions](#hcl-grammar-additions)
  - [Template variables](#template-variables)
  - [Multi-rule PR title resolution](#multi-rule-pr-title-resolution)
  - [Chart values surface](#chart-values-surface)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [Resolved Questions](#resolved-questions)
- [References](#references)
<!--toc:end-->

## Overview

repo-guardian has two separate, weakly-related templating mechanisms
today: file templates (`.tmpl` files rendered via simple
uppercase-placeholder substitution like `OWNER_VALUE`) and PR titles
/ bodies (hardcoded engine constants). Orgs that want Jira/Linear
prefixes, conventional-commit titles, or doc links in PR bodies
cannot adopt the tool without forking. Separately, the chart's
template ConfigMap exposes only three hardcoded slots
(`codeowners`, `dependabot`, `renovate`), so adding a sixth or
seventh templated rule requires both an HCL change and a chart
release.

This design **unifies all templating** behind a single Go
`text/template`-based renderer used by file-template generation,
PR titles, and PR bodies. Variables and helpers are shared across
all three contexts; only the variable struct passed in differs.
Embedded templates are rewritten from `OWNER_VALUE`-style
substitution to dotted-path Go template syntax (`{{ .Catalog.Owner }}`).
The chart's hardcoded ConfigMap slots are replaced with a generic
`templates.files` map plus an `existingConfigMap` escape hatch so
adding new rule templates is a config-only operation.

## Goals and Non-Goals

### Goals

- **One templating system everywhere.** A single renderer in
  `internal/template/` serves file-template rendering, PR titles,
  and PR bodies. Same syntax, same helpers, different typed
  variable contexts. Operators learn one mental model.
- **Per-rule PR title and body customization.** Each rule (and each
  reconciler that opens its own PR) can override the engine defaults
  with a Go-template string.
- **Global defaults at the policy level.** Orgs that want a uniform
  prefix or footer set it once at `defaults.pr {}` and all rules
  inherit unless they override.
- **Useful template variables.** Repo identity, rule identity, the
  list of files added in this PR, the current date, plus an `env`
  helper for runtime substitution (Jira project ID, support URL,
  etc.). Reconciler-specific contexts (e.g., catalog-info parsed
  fields) are exposed as nested struct fields.
- **Generic template ConfigMap.** Chart's `values.templates.files`
  is a map of `<filename>: <content>` so any template name is
  expressible without chart changes. Existing hardcoded slots
  (`codeowners`, `dependabot`, `renovate`) are removed in this
  release; users who set them must migrate to `templates.files`.
- **`templates.existingConfigMap` escape hatch.** Mirrors
  `policy.existingConfigMap`. Operator points at a ConfigMap they
  manage themselves (out-of-band, GitOps-managed, etc.) and the
  chart skips creating its own.
- **Helm values pass-through.** Operators expose arbitrary template
  variables by setting environment variables on the Deployment
  (via a chart values block); templates reference them with
  `{{ env "MY_VAR" }}`. No new mechanism needed beyond the existing
  `env` helper.
- **Safe-by-default templating.** Template parse errors at config
  load time, not at render time. A bad template fails the policy
  load with a clear message, not silently at PR creation.

### Non-Goals

- **A general-purpose templating language.** No conditionals, no
  loops over arbitrary data — just variable substitution + a small
  set of helpers (`env`, `join`, `default`). Anything that wants
  more belongs in a reconciler, not a PR title.
- **Markdown linting / rendering of bodies.** Operators write
  whatever Markdown they want; the engine only substitutes variables
  and forwards the result to GitHub.
- **Localization / i18n.** Single language per policy.
- **Per-installation overrides.** A given policy file is one
  configuration. Multi-org / multi-installation customization
  remains the responsibility of the per-org scope blocks
  (DESIGN-0010).
- **Replacing the embedded fallback templates.** The binary still
  ships embedded defaults so a chart with no `templates.files`
  configured still works for the built-in rules.
- **Backward compatibility with `OWNER_VALUE`-style placeholders.**
  This design replaces the simple substitution renderer with Go
  `text/template`. Existing reconciler templates (catalog-info,
  set-custom-properties, renovate) are rewritten in this PR. There
  is no compat layer — operators with custom `.tmpl` files using
  the old uppercase placeholders must migrate when they upgrade.

## Background

Today's engine state for PR generation:

- `internal/checker/engine_policy.go` defines `PRTitle = "chore: add
  missing files"` as a constant, and `buildPRBodyFromPolicy(actionable)`
  hardcodes a Markdown summary of the rules being reconciled.
- Reconcilers that open their own PRs also have hardcoded titles:
  `PropertiesPRTitle`, `CatalogInfoPRTitle` in
  `internal/reconciler/custom_properties.go`.
- HCL `rule` blocks accept a `pr {}` sub-block today, but its only
  field is `search_terms` (used for finding existing PRs to update).
- No template variable substitution exists in the binary for PR
  titles / bodies.

Today's state for file template rendering:

- `internal/reconciler/reconciler.go` has a private `renderTemplate`
  function that does naive `strings.ReplaceAll` substitution from
  a `map[string]string`. No Go `text/template`, no helpers, no
  validation.
- Reconcilers pass uppercase placeholder maps:
  - `catalog-info.tmpl` consumes `REPO_NAME` and `ORG_NAME`
  - `set-custom-properties.tmpl` consumes `OWNER_VALUE`,
    `COMPONENT_VALUE`, `JIRA_PROJECT_VALUE`, `JIRA_LABEL_VALUE`
  - `renovate.tmpl` consumes `ORG_NAME`
- This is the legacy renderer this design replaces. The new
  unified renderer means file templates and PR templates share
  syntax, helpers, and parse-time validation.

Today's chart state for templates:

- `charts/repo-guardian/templates/configmap.yaml` builds a ConfigMap
  with three hardcoded keys (`CODEOWNERS.tmpl`, `dependabot.yml.tmpl`,
  `renovate.json.tmpl`), each populated from
  `.Values.templates.<name>` if set.
- `cfg.TemplateDir` (env `TEMPLATE_DIR`, default
  `/etc/repo-guardian/templates`) on the binary side already supports
  loading any number of `*.tmpl` files from a directory and falling
  back to embedded defaults — so the binary is fully extensible.
- The bottleneck is the chart's hardcoded values surface, not the
  binary.

## Detailed Design

### Unified template renderer

A single package `internal/template/` exposes one renderer used by
all three contexts:

```go
package template

type Renderer struct {
    funcs template.FuncMap
}

func NewRenderer() *Renderer { /* ... */ }

// Parse compiles a template string at config-load time.
// Errors here surface as policy validation failures.
func (r *Renderer) Parse(name, body string) (*Compiled, error)

type Compiled struct {
    name string
    tpl  *template.Template
}

// Render executes the compiled template against any typed context.
// Returns wrapped error on render failure.
func (c *Compiled) Render(vars any) (string, error)
```

The renderer uses Go `text/template` with a curated function map.
Variable contexts are typed structs (not `map[string]any`) so
templates fail at parse time when they reference a non-existent
field — caught by `make test` instead of in production.

### Variable contexts

Three context structs, all sharing common base fields:

```go
package template

// Common is embedded into every context.
type Common struct {
    Owner         string  // org / repo owner
    Repo          string  // repo name
    DefaultBranch string  // e.g., "main"
    Date          string  // RFC3339 timestamp at render time
}

// FileVars is passed to file template rendering.
type FileVars struct {
    Common
    Rule    Rule          // rule being applied
    Catalog *CatalogInfo  // populated only for catalog-info-aware rules; nil otherwise
    Org     string        // alias for Owner; convenience for org-named templates
}

// PRVars is passed to PR title and body rendering.
type PRVars struct {
    Common
    Rule       Rule       // single-rule PR
    Rules      []Rule     // bundled-PR body; nil for single-rule
    Files      []string   // all files included in this PR
    Reconciler string     // reconciler name if this is a reconciler-opened PR; "" otherwise
}

type Rule struct {
    Name   string
    Target string
}

type CatalogInfo struct {
    Owner       string  // spec.owner from catalog-info.yaml
    Component   string  // metadata.name
    JiraProject string  // backstage.io/jira-project-key
    JiraLabel   string  // backstage.io/jira-component
}
```

The `Catalog` field is `nil` for any file template not produced by
a catalog-info-aware reconciler. Templates that reference
`{{ .Catalog.Owner }}` on a context where `Catalog` is nil fail
at render time with a clear error — and at parse time we can
optionally validate by running each template against a zero-value
context.

### Helpers (curated subset)

Available in all three contexts:

| Helper | Signature | Use |
|---|---|---|
| `env` | `env "VAR"` | read environment variable; "" if unset |
| `default` | `default fallback value` | fallback for empty values |
| `join` | `join sep slice` | string-join a slice |
| `lower`, `upper`, `title` | `lower str` | case helpers |

Not included: `now`, `regexReplaceAll`, `readFile`, `glob`,
`hasPrefix`, anything Sprig-specific. Adding a helper requires a
code change with a test — keeps the surface small and auditable.

### Render-time behavior

- **PR body truncation.** Rendered PR bodies that exceed ~65000
  chars (GitHub's PR body cap is 65535) are truncated to fit, with
  a visible marker appended:
  `<!-- truncated by repo-guardian: original length=N chars, max=65535 -->`.
  Engine emits `slog.Warn` with the original length. PR creation
  succeeds with the truncated body; the marker is visible to PR
  reviewers and the warning is visible to operators in logs. PR
  titles have no comparable limit issue (GitHub caps titles at 256
  chars; templates that hit this are operator bugs, fail loud).
- **Strict template validation.** Opt-in via `--strict-templates`
  CLI flag on the binary (env var `STRICT_TEMPLATES=true`). When
  enabled, every parsed template is execute-checked against a
  zero-value variable context at policy-load time, surfacing
  `.Catalog.Owner` references where `.Catalog` is nil before they
  hit production. Off by default for V1; V2 may make it default
  once the false-positive rate is known. Recommended in CI / dev
  environments to catch template bugs before deploy.

### HCL grammar additions

A new `pr {}` block grammar, valid at three scopes:

```hcl
# 1. Policy-level defaults — applies to every rule that doesn't override
defaults {
  pr {
    title  = "chore: {{ .Rule.Name }}"
    body   = <<-EOT
      Generated by repo-guardian.
      Files added:
      {{ range .Files }}- `{{ . }}`
      {{ end }}
    EOT
    labels = ["compliance", "automated"]
  }
}

# 2. Per-rule override
rule "file" "codeowners" {
  check    = "exists"
  target   = ".github/CODEOWNERS"
  template = "codeowners"

  pr {
    title = "[{{ env \"JIRA_PROJECT\" }}-CHORE] add CODEOWNERS"
    body  = "Adds CODEOWNERS per [SEC-001](https://jira.example.com/browse/SEC-1)"
    # labels not set → inherits ["compliance", "automated"] from defaults
  }
}

# 3. Per-reconciler override (reconcilers that open their own PRs)
rule "file" "catalog_info" {
  check    = "contains"
  target   = "catalog-info.yaml"
  template = "catalog-info"

  reconcile "custom_properties" {
    mode  = "github-action"
    watch = true

    pr {
      # inherits defaults to true; title and labels (unset here) come from
      # defaults.pr; body uses the explicit override below.
      body = "Installs the GitHub Action that syncs Backstage metadata to repo properties."
    }
  }
}

# Cleaner alternative — opt out of inheritance entirely
rule "file" "internal_audit_log" {
  check    = "exists"
  target   = ".github/audit-log.yml"
  template = "audit-log"

  pr {
    # This PR is NOT a compliance PR; don't pick up [JIRA-CHORE] prefix
    # or compliance/automated labels from defaults.pr.
    inherits = false
    title    = "audit: enable repo audit logging"
    body     = "Enables structured audit logging for SOC2 evidence collection."
    labels   = ["audit", "soc2"]
  }
}
```

Resolution order (most specific wins, walking up the chain):

1. Reconciler-level `reconcile { pr {} }` (only for reconciler PRs)
2. Rule-level `rule { pr {} }` (only for rule PRs)
3. Policy-level `defaults { pr {} }`
4. Engine built-in (current behavior)

Each `pr {}` block has an `inherits` boolean (default `true`) that
controls whether unset fields cascade up the chain:

- `inherits = true` (default): unset fields at this level pull from
  the next level up. So a reconciler `pr {}` that sets only `title`
  gets `body` and `labels` from `rule.pr` (if set) → `defaults.pr`
  → built-in.
- `inherits = false`: this block is canonical; unset fields skip
  parent levels and use the engine built-in directly. Useful when
  the `defaults.pr` template is compliance-flavored but a specific
  rule or reconciler PR needs to read differently.

`defaults.pr` itself ignores `inherits` since it has no parent to
inherit from.

### Template variables

Available in both `title` and `body`:

| Variable | Type | Notes |
|---|---|---|
| `.Owner` | string | Repo owner / org login |
| `.Repo` | string | Repo name |
| `.DefaultBranch` | string | Default branch name |
| `.Rule.Name` | string | HCL rule label (e.g., `codeowners`) |
| `.Rule.Target` | string | Target file path (e.g., `.github/CODEOWNERS`) |
| `.Files` | []string | All file paths included in this PR |
| `.Date` | string | RFC3339 timestamp at PR creation |
| `.Reconciler` | string | Empty for rule PRs; reconciler name for reconciler PRs |

Helpers (Sprig-style subset):

- `env "VAR"` — read environment variable; empty string if unset
- `join sep slice` — string-join a slice
- `default fallback value` — fallback if value is empty
- `lower`, `upper`, `title` — case helpers

The full Sprig library is **not** included — we want a minimal,
auditable surface. Adding a helper requires a code change and tests.

### Multi-rule PR title resolution

When multiple file rules fire on the same sweep against the same
repo, the engine bundles them into a single PR on
`repo-guardian/add-missing-files`. With per-rule titles, this
creates ambiguity. Resolution:

- If two or more rules with different `pr.title` values fire
  together: use the policy-level `defaults.pr.title` (or built-in
  if no default). The per-rule titles are ignored for the bundled
  PR. The engine emits a `slog.Info` log noting the rules whose
  titles were ignored.
- If exactly one rule fires (or all firing rules share the same
  resolved title): use that title.
- Future option: `pr.bundle = "separate"` on a rule could force
  it into its own PR (its own branch, its own title), at the cost
  of more open PRs per repo. Out of scope for V1.

The body is always built from a default template that lists *all*
included rules and files, but the body template can reference
`.Rules` (slice) instead of `.Rule.Name` to format the bundled list.

### Chart values surface

`values.yaml` schema change:

```yaml
templates:
  # Generic map: any filename, any content.
  # Filename WITHOUT .tmpl suffix matches the rule's `template = "..."` reference.
  # Filename WITH .tmpl suffix is what gets written into the ConfigMap.
  files: {}

  # Escape hatch: point at a ConfigMap managed outside the chart.
  # When set, the chart does NOT create its own template ConfigMap.
  existingConfigMap: ""

# REMOVED in this release (clean break):
#   templates.codeowners
#   templates.dependabot
#   templates.renovate
# Migration: move existing values into templates.files with the .tmpl suffix.

# Helm values pass-through to template `env` helper.
templating:
  vars: {}
  # Example:
  #   JIRA_PROJECT: "PLAT"
  #   DOCS_URL: "https://docs.example.com/compliance"
  # Each key becomes an env var on the Deployment; templates reference
  # them as {{ env "JIRA_PROJECT" }}.
```

Example filled-in:

```yaml
templates:
  files:
    codeowners.tmpl: |
      * @platform-team
    dependabot.yml.tmpl: |
      version: 2
      updates:
        - package-ecosystem: github-actions
          directory: /
          schedule:
            interval: weekly
    custom-properties.tmpl: |
      apiVersion: v1
      kind: ConfigMap
      ...
    wf-security-scan.tmpl: |
      name: Security Scan
      on: { push: { branches: [main] } }
      jobs: { ... }
```

`templates/configmap.yaml` rewrite:

- If `templates.existingConfigMap` is set: skip rendering the
  ConfigMap. Mount the named ConfigMap on the Deployment.
- Else: `range $name, $content := .Values.templates.files` and
  emit a key per entry.
- Legacy slots removed entirely. NOTES.txt explains the migration
  for users upgrading from a release that set them.

`templating.vars` rendering:

- Each `(key, value)` becomes an env var on the Deployment via
  `range $k, $v := .Values.templating.vars` in the deployment
  template. Standard k8s env-var rules apply (uppercase recommended,
  values shell-safe).
- Templates reference them as `{{ env "KEY" }}` in any of the three
  contexts. No special chart-side handling beyond emitting the env
  vars.

## API / Interface Changes

HCL grammar (added):

- `defaults { pr {} }` block at top level
- `pr {}` block inside `rule "file"` (extends existing)
- `pr {}` block inside `reconcile { ... }` (new)
- Fields: `title`, `body`, `labels`

Go (added):

- `internal/template/` — new package owning the renderer, variable
  contexts (`FileVars`, `PRVars`, `Common`), helpers, and parse-time
  validation. Mockable interface for tests.
- `internal/policy/pr.go` — `PRTemplate` struct holding compiled
  title/body/labels resolved at HCL load time.
- `internal/checker/engine_policy.go` — wire compiled `PRTemplate`
  into PR creation, fall through to defaults.
- `internal/reconciler/custom_properties.go` (and any reconciler
  that opens PRs) — accept a `*PRTemplate` from the reconciler
  config and invoke it.

Go (changed):

- `internal/reconciler/reconciler.go.renderTemplate` — removed.
  Replaced by `template.Renderer.Render(compiled, FileVars{...})`.
- `internal/rules/registry.go.TemplateStore` — `Get(name)` now
  returns a `*template.Compiled` (parsed at load time) instead of
  a raw string. Old callers that did `strings.ReplaceAll` on the
  return value are migrated to `Render(compiled, vars)`.

Embedded templates (rewritten — see Migration for the diff):

- `internal/rules/templates/catalog-info.tmpl` — `REPO_NAME` →
  `{{ .Repo }}`, `ORG_NAME` → `{{ .Owner }}`
- `internal/rules/templates/set-custom-properties.tmpl` —
  `OWNER_VALUE` → `{{ .Catalog.Owner }}`, `COMPONENT_VALUE` →
  `{{ .Catalog.Component }}`, `JIRA_PROJECT_VALUE` →
  `{{ .Catalog.JiraProject }}`, `JIRA_LABEL_VALUE` →
  `{{ .Catalog.JiraLabel }}`
- `internal/rules/templates/renovate.tmpl` — `ORG_NAME` →
  `{{ .Owner }}`

Chart `values.yaml` (changed):

- Add `templates.files` (map)
- Add `templates.existingConfigMap` (string)
- Add `templating.vars` (map) — Helm values → env-var pass-through
- **Remove** `templates.codeowners`, `templates.dependabot`,
  `templates.renovate` (clean break, no compat shim)

Env vars: any `templating.vars` entry becomes a Deployment env var.

## Data Model

No persistence. Everything is config-resolved at policy / template
load time.

`PRTemplate` struct (sketch — references the unified renderer
defined in Detailed Design):

```go
package policy

import "github.com/donaldgifford/repo-guardian/internal/template"

type PRTemplate struct {
    Title    *template.Compiled  // parsed at policy load; nil = inherit
    Body     *template.Compiled  // nil = inherit
    Labels   []string             // applied verbatim, no templating; nil = inherit
    Inherits bool                 // default true; false skips parent levels
}

func (p *PRTemplate) Render(r *template.Renderer, v *template.PRVars) (title, body string, err error) {
    title, err = p.Title.Render(v)
    if err != nil { return }
    body, err = p.Body.Render(v)
    return
}
```

The `*template.Compiled` and `*template.PRVars` types are defined
in `internal/template/` (see Detailed Design § Variable contexts).
The same package's `*template.FileVars` is used by reconcilers
when they render `.tmpl` files.

Templates are parsed once at config load (HCL parse for `pr {}`
blocks, template-store load for `.tmpl` files). Parse errors fail
the load with a clear message identifying which template and what
went wrong. Render-time errors are treated as engine errors
(logged, the failing operation aborts for that repo).

## Testing Strategy

- **Unit tests** in `internal/template/`:
  - Parse error surfacing (bad template → load fails with location)
  - Each helper (`env`, `default`, `join`, `lower`, `upper`, `title`)
    has positive + edge-case tests
  - Render against zero-value contexts to catch templates that
    deref nil pointers
  - Render against `FileVars` and `PRVars` proves the same renderer
    serves both
- **Unit tests** in `internal/policy/pr_test.go`:
  - Parse-time errors caught at policy load
  - Resolution-order tests (reconciler > rule > defaults > built-in)
- **Integration tests** in `internal/checker/engine_policy_test.go`:
  - Rule with custom title → PR created with rendered title
  - Bundled PR with conflicting titles → falls back to defaults
  - Reconciler with custom title → reconciler PR uses it
- **Reconciler tests** updated:
  - `internal/reconciler/custom_properties_test.go` — assert the
    rendered catalog-info / set-custom-properties bodies match
    expected output under the new dotted variables
- **Helm-unittest** for chart changes:
  - `templates.files` map renders into ConfigMap with all keys
  - `templates.existingConfigMap` skips ConfigMap rendering and
    mounts the named one
  - `templating.vars` keys appear as Deployment env vars
- **Smoke test in homelab** with at least one rule using a Jira-style
  title and `env "JIRA_PROJECT"` substitution.

## Migration / Rollout Plan

This design ships as one coordinated change set because the unified
renderer touches both PR creation and reconciler file rendering;
splitting them would leave the codebase mid-migration with two
templating systems active simultaneously.

1. **Land `internal/template/` package** with renderer, contexts,
   helpers, and tests. Pure addition; nothing yet calls it.
2. **Migrate `TemplateStore.Get` to return `*template.Compiled`.**
   Update the engine's file-creation path and reconcilers
   (`custom_properties.go`) to call `Render(compiled, FileVars{...})`
   instead of `renderTemplate(content, map)`. Delete
   `renderTemplate`. CI must stay green.
3. **Rewrite the embedded templates** to dotted-path syntax:

   | Template | Old → New |
   |---|---|
   | `catalog-info.tmpl` | `REPO_NAME` → `{{ .Repo }}`; `ORG_NAME` → `{{ .Owner }}` |
   | `set-custom-properties.tmpl` | `OWNER_VALUE` → `{{ .Catalog.Owner }}`; `COMPONENT_VALUE` → `{{ .Catalog.Component }}`; `JIRA_PROJECT_VALUE` → `{{ .Catalog.JiraProject }}`; `JIRA_LABEL_VALUE` → `{{ .Catalog.JiraLabel }}` |
   | `renovate.tmpl` | `ORG_NAME` → `{{ .Owner }}` |

4. **Land HCL grammar + `PRTemplate`** for `defaults { pr {} }`,
   per-rule `pr {}`, per-reconciler `pr {}`. Resolution order
   wired through the engine.
5. **Chart values surface change**: add `templates.files`,
   `templates.existingConfigMap`, `templating.vars`. Remove
   `templates.codeowners` / `dependabot` / `renovate` outright
   (clean break — the chart's NOTES.txt explains the migration).
6. **Update `examples/guardian-full.hcl`** to demonstrate the new
   `defaults { pr {} }` and per-rule `pr {}` blocks.
7. **Document migration** in chart README, `docs/ADDING_RULES.md`,
   and a CHANGELOG entry covering both the binary and chart breaking
   changes. Operators with custom `.tmpl` files using the old
   placeholders OR with the legacy chart slots populated must
   migrate before upgrading.

This is a binary-breaking change for operators with custom `.tmpl`
files using the old `OWNER_VALUE`-style placeholders. It's also a
chart-breaking change because `templates.codeowners` etc. are
removed. Both warrant a binary minor bump (e.g., 1.4.x → 1.5.0)
and a chart minor bump.

Rollback: revert the umbrella PR — there's no in-place rollback
because the old `renderTemplate` and old chart slots are deleted
in step 2 / 5.

## Open Questions

None outstanding. See **Resolved Questions** below.

## Resolved Questions

All resolved during PR #71 review walkthrough.

1. **Sprig subset or full Sprig?** → **Small curated subset**
   (`env`, `default`, `join`, `lower`, `upper`, `title`). Revisit if
   real user demand surfaces a missing helper. Adding helpers later
   is non-breaking; removing them is.
2. **`labels` templating.** → **Stay literal.** Labels are a fixed
   taxonomy in most orgs; the few dynamic-label cases (e.g., a
   `jira/PROJECT-123` label) can be hardcoded per rule or set on
   `defaults.pr.labels`. Going from literal to templated later is
   non-breaking (a no-`{{ }}` template is just a string), so we
   can flip it on if a real need surfaces.
3. **Multi-rule PR — opt-in to separate PRs?** → **Defer to V2.**
   V1 keeps "bundle and fall back to `defaults.pr.title` on title
   conflict" behavior. `pr.bundle = "separate"` flag is non-breaking
   to add later (default `"bundle"` preserves V1 behavior).
4. **Reconciler PR config inheritance.** → **Explicit `inherits`
   boolean on every `pr {}` block, default `true`.** Resolution
   order: reconciler → rule → defaults → built-in, with `inherits =
   false` skipping parents and falling through to built-in directly.
   See Detailed Design § HCL grammar additions for the resolved
   shape.
5. **`existingConfigMap` validation.** → **Let k8s native behavior
   handle it.** Pod stays in `CreateContainerConfigError` until the
   ConfigMap exists; chart README documents the dependency. No
   chart-time validation hook (would be skipped by `helm template`
   consumers like ArgoCD anyway).
6. **Body length limits.** → **Truncate-with-warning.** Engine
   truncates rendered bodies to ~65000 chars, appends a visible
   marker (`<!-- truncated by repo-guardian: original length=N
   chars, max=65535 -->`), and emits `slog.Warn` with the original
   length. PRs always succeed; reviewers see the marker; operators
   see the warning in logs.
7. **Global Markdown linter on bodies.** → **No.** Engine is a
   substitution layer, not a Markdown editor. Linter would add
   dependencies and stylistic-opinion false positives. Convention
   recommendations live in `docs/ADDING_RULES.md`.
8. **Zero-value-context render check at parse time.** → **Opt-in
   via `--strict-templates` CLI flag** for V1. Default-off because
   the engine knows which template is used in which context, and
   per-context-type matching is more involved than running every
   template against every zero-value context. V2 can be smarter
   (per-template context matching) and make strict the default
   once the false-positive rate is known.

## References

- DESIGN-0006 (HCL Policy Configuration and Rule Engine — the
  grammar this design extends)
- DESIGN-0007 (Reconciler Interface — the reconciler pattern whose
  PR creation this design also customizes)
- DESIGN-0010 (Per-org rule scoping — the multi-installation
  customization story this design intentionally does not duplicate)
- Go `text/template` package:
  https://pkg.go.dev/text/template
- Sprig template helpers:
  https://masterminds.github.io/sprig/
- GitHub PR body length limit (empirical, undocumented but
  consistent at 65535 chars)
