# Policy Reference (guardian.hcl)

The complete specification of repo-guardian's HCL policy surface: every
block, attribute, type, default, and validation rule. For a guided
introduction, start with [Getting Started](getting-started.md).

Source of truth: `internal/policy/` (`types.go`, `defaults.go`, `loader.go`,
`validate.go`). This document tracks the code; when in doubt, the loader wins.

## Loading and precedence

- **Location** — set `GUARDIAN_CONFIG` to a single `.hcl` file **or a
  directory** of `.hcl` files (all files are parsed and merged; the
  multi-file layout in `examples/guardian-multi-org/` is the canonical
  split).
- **Merge order** — `built-in defaults → HCL file → environment variable
  overrides`. Env vars are applied last and always win (see
  [Environment overrides](#environment-variable-overrides)).
- **Rules are replace, not merge** — if your HCL declares *any* rule, the
  built-in file rules are replaced entirely. Declare everything you want.
  With no HCL config at all, the built-in defaults apply (see
  [Built-in defaults](#built-in-defaults)).
- **Strict-mode exception** — when a top-level `scope {}` block is present
  and you declare no rules, the loader does **not** fall back to built-in
  rules (the "every rule declares its scope" contract couldn't hold).
- **Validation is fail-fast** — all violations are collected and reported
  together at startup; the process exits non-zero on any error.

## Grammar overview

```hcl
guardian    { ... }                    # operational settings (0 or 1)
ignore      { repos = [...] }          # global ignore list (0 or 1)
scope       { orgs = [...] }           # org universe; presence = strict mode (0 or 1)
defaults    { pr { ... } }             # process-wide PR template defaults (0 or 1)

rule "file" "<name>" {                 # file compliance rule (0+)
  ...
  assertion { ... }                    # content assertions (0+, contains mode only)
  pr        { ... }                    # PR template + search terms (0 or 1)
  ignore    { repos = [...] }          # per-rule ignore (0 or 1)
  scope     { orgs = [...] }           # per-rule scope (required in strict mode)
  reconcile "<type>" {                 # post-check reconcilers (0+)
    ...
    pr { ... }                         # per-reconciler PR template (0 or 1)
  }
}

rule "setting" "<name>" { ... }            # repo setting rule (0+)
rule "branch_protection" "<name>" { ... }  # branch protection rule (0+)
```

Rule blocks are routed on their first label: `"file"`, `"setting"`, or
`"branch_protection"`. The second label is the rule's unique name — a
duplicate `type:name` pair fails validation.

## `guardian {}` — operational settings

| Attribute | Type | Default | Description |
|-----------|------|---------|-------------|
| `dry_run` | bool | `false` | Log intended actions without creating PRs or writing to GitHub. |
| `schedule_interval` | string (Go duration) | `"168h"` | Cadence of the scheduled stale sweep. |
| `worker_count` | int | `5` | Concurrent repo-check workers. Must be > 0. |
| `queue_size` | int | `1000` | Work queue buffer size. Must be > 0. |
| `log_level` | string | `"info"` | One of `debug`, `info`, `warn`, `error`. |
| `skip_forks` | bool | `true` | Skip forked repositories. |
| `skip_archived` | bool | `true` | Skip archived repositories. |
| `rate_limit_threshold` | float | `0.10` | Fraction of remaining rate-limit budget at which pre-emptive throttling begins. Must be in [0.0, 1.0]. |
| `webhook_ip_allowlist` | bool | `true` | Enable the GitHub webhook IP allowlist middleware (see `SECURITY.md`). |
| `webhook_ip_allowlist_fail_open` | bool | `false` | Allow webhook requests when the allowlist can't be fetched. |
| `trust_proxy_headers` | bool | `false` | Read client IP from `X-Forwarded-For` (required behind Tailscale Funnel or similar proxies). |
| `auto_close_pr` | bool | `true` | Auto-close a guardian PR (and delete its branch) when every rule it addresses is satisfied on the default branch. Set `false` to keep PRs open for manual close-out. The `AUTO_CLOSE_PR` env var overrides the HCL value. |

Unknown attributes in `guardian {}` fail load with an "Unsupported
argument" error naming the file and line — typos are caught at startup,
not silently ignored.

## `ignore {}` — ignore lists

```hcl
ignore {
  repos = ["myorg/terraform-*", "myorg/.github", "myorg/archive-*"]
}
```

| Attribute | Type | Description |
|-----------|------|-------------|
| `repos` | list(string) | Glob patterns matched against `owner/name` (Go `path.Match` semantics, input lowercased). |

Appears in two places:

- **Top level** — matching repos are skipped for **all** rules.
- **Inside any rule** — matching repos are skipped for **that rule only**.

Precedence relative to scope: scope is evaluated first, ignore second. The
`out_of_scope_total` and `ignored_total` metrics never both increment for
the same rule on the same repo.

## `scope {}` — org scoping (multi-org)

```hcl
# Top level: declares the org universe AND engages strict mode.
scope {
  orgs = ["prod-org", "staging-org"]
}

rule "file" "codeowners" {
  # In strict mode every rule MUST declare its own scope.
  scope { orgs = ["*"] }   # "*" = every org in the top-level universe
  ...
}
```

| Attribute | Type | Description |
|-----------|------|-------------|
| `orgs` | list(string) | Org glob patterns (`path.Match`, lowercased). At rule level, the literal `"*"` means "every in-scope org." |

Two modes:

- **Legacy mode** (no top-level `scope {}`) — every rule applies to every
  repo the App installation can see. A per-rule `scope` without a top-level
  `scope` is allowed but logs a warning at load time.
- **Strict mode** (top-level `scope {}` present) — repos outside the
  universe are skipped entirely, and **every rule must declare its own
  `scope {}`** (validated at load). No built-in rule fallback applies in
  strict mode.

Out-of-scope skips are observable via
`repo_guardian_out_of_scope_total{level="policy"|"rule"}`.

## `defaults {}` — process-wide PR defaults

```hcl
defaults {
  pr {
    title  = "chore: guardian baseline for {{ .Repo }}"
    body   = "..."
    labels = ["automated", "guardian"]
  }
}
```

Contains a single optional `pr {}` block (schema [below](#pr-pull-request-templates)).
Every rule-level and reconciler-level `pr {}` inherits from it
field-by-field unless it opts out.

## `rule "file" "<name>" {}` — file rules

```hcl
rule "file" "codeowners" {
  enabled  = true                                  # optional, default true
  check    = "exists"                              # exists | contains | exact | absent
  paths    = ["CODEOWNERS", ".github/CODEOWNERS"]  # required
  target   = ".github/CODEOWNERS"                  # required (not for absent)
  template = "codeowners"                          # required (not for absent)
}
```

| Attribute | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `enabled` | bool | no | `true` | Disabled rules are skipped entirely. |
| `check` | string | no | `"exists"` | One of `exists`, `contains`, `exact`, `absent`. |
| `paths` | list(string) | **yes** | — | Candidate locations checked in order. For add modes the rule is satisfied by the first hit; for `absent` the rule is actionable if **any** path exists. Must be non-empty. |
| `target` | string | conditional | — | Path where the PR creates the file when missing. **Required** for `exists`/`contains`/`exact`; **forbidden** for `absent`. |
| `template` | string | conditional | — | Template name (TemplateStore key, no `.tmpl` suffix). **Required** for `exists`/`contains`/`exact`; **forbidden** for `absent`. |

Nested blocks: `assertion` (0+), `pr` (0 or 1), `ignore` (0 or 1), `scope`
(0 or 1), `reconcile` (0+), `when` (0 or 1). Absent rules forbid `assertion`
and `reconcile` blocks (see below).

### Check modes

| Mode | File present? | Content checked? | Actionable when | Remediation |
|------|--------------|------------------|-----------------|-------------|
| `exists` | required | no | No candidate path exists. | Add the file. |
| `contains` | required | assertions must pass | File missing, or any assertion fails. | Add/fix the file. |
| `exact` | required | must equal rendered template | File missing or differs from the template. YAML files (`.yml`/`.yaml`) compare semantically (key order and formatting don't matter); everything else compares bytes. | Add/fix the file. |
| `absent` | must be **gone** | no | **Any** candidate path exists on the default branch. | **Delete** every present path via a deletion PR on the reconcile branch. |

Declaring `assertion` blocks on a rule whose mode is not `contains` is a
validation error. An `absent` rule additionally forbids `target`,
`template`, `assertion`, and `reconcile` — its only job is to ensure the
paths are gone, so there is nothing to render or reconcile.

### `absent` mode — removing forbidden files

`check = "absent"` inverts the usual polarity: the rule is satisfied when
**none** of its `paths` exist, and actionable when any do. Remediation is a
file-deletion commit on the same `repo-guardian/add-missing-files`
reconcile branch, described under a **Removed Files** section in the PR
body. Deletion is idempotent — a path already gone from the branch is
skipped — and it is the engine's only destructive remediation, so:

- **Dry-run first.** With `dry_run = true` the engine logs the planned
  deletions per rule and mutates nothing.
- **`search_terms` no longer collide with the add-era PR.** Since
  v1.10.1 the search only scans *third-party* pull requests;
  repo-guardian's own `repo-guardian/add-missing-files` PR is excluded
  and handled by the converge path instead. An `absent` rule forbidding
  `dependabot.yml` can safely reuse `search_terms = ["dependabot"]`
  without its removal PR being mistaken for the old *add* PR. A distinct
  phrase such as `["remove dependabot"]` is still clearer to humans
  reading the policy, just no longer load-bearing.

### `when {}` — conditional gating

Any file rule (any check mode) may be gated on a **sibling file rule** being
satisfied on the default branch:

```hcl
rule "file" "no_dependabot" {
  check = "absent"
  paths = [".github/dependabot.yml", ".github/dependabot.yaml"]

  when {
    rule_satisfied = "renovate_config"  # gate on this sibling file rule
  }
}
```

| Attribute | Type | Required | Description |
|-----------|------|----------|-------------|
| `rule_satisfied` | string | **yes** | HCL name of a sibling **file** rule. The gated rule fires only when that rule is satisfied on the default branch. |

Gate semantics:

- **Default-branch-only.** The gate reads the default branch, never the
  reconcile branch — repo-guardian never acts on a not-yet-merged state.
- **Content-only.** The referee's own `scope`/`ignore` never affect the
  gate; only its paths/assertions/content decide satisfaction.
- **Fail-closed.** If evaluating the referee errors (API glitch), the gate
  is treated as **closed** and the rule is skipped this sweep. A
  destructive gated action never proceeds from an unknown state.

Validated at load: the referee must exist as an enabled **file** rule
(setting/branch-protection names are rejected), must not be the rule itself,
and gates must not form a cycle of any length. Merging the referee's add PR
(or re-adding a removed file) triggers the gated rule's re-check on the push
path, not just the next sweep.

### `assertion {}` — content assertions

Two families, chosen per block: **regex** over the raw file, or
**YAML-path** structural checks.

| Attribute | Type | Description |
|-----------|------|-------------|
| `pattern` | string | Regex (Go RE2) that must match somewhere in the file. |
| `not_pattern` | string | Regex that must **not** match anywhere in the file. |
| `yaml_path` | string | Dotted path into the parsed YAML document (e.g. `spec.owner`). |
| `contains` | string | With `yaml_path`: the resolved value must contain this substring. |
| `equals` | string | With `yaml_path`: the resolved value must equal this string. |
| `non_empty` | bool | With `yaml_path`: the path must exist and resolve to a non-empty value. |
| `message` | string (**required**) | Human-readable failure text; appears in logs and PR bodies. |

Validation rules (enforced at load):

- `message` is required.
- `pattern`/`not_pattern` and `yaml_path` are **mutually exclusive** per
  block (use multiple `assertion` blocks to combine families).
- `yaml_path` requires exactly one of `contains`, `equals`, or `non_empty`.
- Every block must set at least one of `pattern`, `not_pattern`, `yaml_path`.

```hcl
assertion {
  pattern = "github>myorg/renovate-config"
  message = "renovate.json must extend the org preset"
}

assertion {
  yaml_path = "spec.owner"
  contains  = "team"
  message   = "spec.owner must reference a team"
}

assertion {
  not_pattern = "TODO"
  message     = "no TODO placeholders"
}
```

## `rule "setting" "<name>" {}` — repository setting rules

```hcl
rule "setting" "enable_vuln_alerts" {
  property  = "vulnerability_alerts_enabled"
  expected  = true
  remediate = true
}
```

| Attribute | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `enabled` | bool | no | `true` | Disabled rules are skipped. |
| `property` | string | **yes** | — | One of the supported properties below. |
| `expected` | bool or string | **yes** | — | Desired value. Type must match the property (validated at load). |
| `remediate` | bool | no | `false` | `true`: write the expected value via the API. `false`: report only. |

Nested blocks: `ignore`, `scope`.

Supported properties and their `expected` types:

| Property | Type |
|----------|------|
| `vulnerability_alerts_enabled` | bool |
| `default_branch` | string |
| `has_issues` | bool |
| `has_wiki` | bool |
| `delete_branch_on_merge` | bool |
| `allow_merge_commit` | bool |
| `allow_squash_merge` | bool |
| `allow_rebase_merge` | bool |

Setting remediation is a **direct API write** — settings are not files, so
there is no PR to review. Use `remediate = false` first to survey the fleet
via logs/metrics before enabling writes.

## `rule "branch_protection" "<name>" {}` — branch protection rules

```hcl
rule "branch_protection" "main_protection" {
  branch                 = "main"
  require_pr             = true
  required_approvals     = 1
  dismiss_stale_reviews  = true
  require_status_checks  = ["ci/test"]
  require_linear_history = true
  enforce_admins         = false
  remediate              = true
}
```

| Attribute | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `enabled` | bool | no | `true` | Disabled rules are skipped. |
| `branch` | string | **yes** | — | Branch name the ruleset targets. |
| `require_pr` | bool | no | `false` | Require a pull request before merging. |
| `required_approvals` | int | no | `0` | Required approving review count. Must be ≥ 0. |
| `dismiss_stale_reviews` | bool | no | `false` | Dismiss approvals when new commits land. |
| `require_status_checks` | list(string) | no | `[]` | Status check contexts that must pass. |
| `enforce_admins` | bool | no | `false` | Apply the rules to administrators too. |
| `require_linear_history` | bool | no | `false` | Forbid merge commits on the branch. |
| `remediate` | bool | no | `false` | `true`: create/update the ruleset via the rulesets API. `false`: report only. |

Nested blocks: `ignore`, `scope`. Repos where the target branch doesn't
exist are handled gracefully (skipped, not errored).

## `reconcile "<type>" {}` — reconcilers

Reconcilers attach to **file rules** and run after the rule's checks pass.
The block label selects one of four built-in types.

| Attribute | Type | Default | Applies to | Description |
|-----------|------|---------|------------|-------------|
| `watch` | bool | `false` | all | Register the rule's paths as watched: a push to the default branch touching them triggers an immediate re-check. |
| `mode` | string | — | `custom_properties` | `"api"` or `"github-action"` (required for this type). |
| `annotation_properties` | map(string) | `{}` | `custom_properties` | Maps a `catalog-info.yaml` annotation key to the GitHub custom property name it populates. See below. |
| `delete_extra` | bool | `false` | `label_sync` | Delete repo labels not present in the YAML source. |
| `pr {}` | block | — | all | Per-reconciler PR template (merges with `defaults.pr` **only** — deliberately skips the rule's `pr`). |

### `custom_properties`

Parses the repo's Backstage `catalog-info.yaml` and syncs GitHub repository
custom properties from it (DESIGN-0019).

- `mode = "api"` — write properties directly via the API.
- `mode = "github-action"` — open a PR adding a workflow that sets the
  properties from within the repo (for orgs that want the change reviewed,
  or Apps without org-level property permissions).

**The managed set** is `{Owner, Component}` plus every property name in
`annotation_properties`. `Owner` always comes from `spec.owner` and
`Component` always comes from `metadata.name` — these two are
contract-guaranteed by every Backstage `Component` entity and are not
configurable or remappable. Everything else is opt-in:

```hcl
reconcile "custom_properties" {
  mode = "api"
  annotation_properties = {
    "jira/project-key" = "JiraProject"
    "jira/label"        = "JiraLabel"
  }
}
```

With the map above, a `catalog-info.yaml` carrying
`metadata.annotations["jira/project-key"]: BILL` sets the GitHub custom
property `JiraProject` to `BILL`. Properties on GitHub outside the managed
set (e.g. a `CostCenter` a human set by hand) are never read, diffed, or
touched — repo-guardian only manages what it's told to.

**Full state sync, including clears.** If a mapped annotation later
disappears from `catalog-info.yaml` (removed, or the file itself removed),
the corresponding GitHub property is cleared (set to `null`) on the next
reconcile — not left stale. This applies to every property in the managed
set except `Owner`/`Component`, which always carry a value: they fall back
to `Unclassified` rather than clearing.

The file-removal half of that contract needs the reconciler to run even
though there is no file to read. `custom_properties` is invoked on absence
because clearing is part of its contract; reconcilers whose behavior is
purely a function of file *content* (`label_sync`, `branch_protection`,
`workflow_sync`) are not, since a missing config file is not a statement
that the repo should have no labels, no rulesets, or no workflow. When the
file rule that owns those paths is `exists`-mode, it opens its own PR to
restore the missing file; the reconciler only clears the properties and
never opens a competing PR for the same path.

**Malformed catalog-info is skipped, never cleared.** A clear only ever
comes from a *valid* `catalog-info.yaml` that no longer names the
annotation. If the file is present but does not parse as YAML, or parses
as a non-`Component` entity, repo-guardian skips the reconcile entirely —
it makes no property writes and retries on the next sweep, so a
temporarily broken commit can never wipe every mapped property. Parse
failures are logged (`"catalog-info parse failed; skipping reconcile to
avoid clearing properties"`) and counted via
`repo_guardian_catalog_parse_failed_total{org}`; a valid non-`Component`
file is logged at info and does not move the counter. The
"unclassified" defaults (`Owner`/`Component` = `Unclassified`) apply only
when the repo has **no** catalog-info file at all — that is a positive
"this repo is unclassified" state, distinct from a parse error.

**Generated-workflow values are passed literally (github-action mode).**
In `github-action` mode the property values are baked into the generated
`.github/workflows/set-custom-properties.yml`. Those values reach the
workflow's shell only through its `env:` block and are referenced as
quoted `"$RG_PROP_*"` variables, so a value containing quotes, `$`, or
`$(...)` is passed to `gh api` as an inert literal and never evaluated by
the shell. A property value containing a literal GitHub Actions
expression opener (`${{`) is rejected at render time rather than written
into the workflow.

**Schema preflight (values-only, least-privilege).** Before writing,
repo-guardian checks the org's actual custom-property *schema* (the set of
property names an org admin has defined) and drops any managed property
the schema doesn't define — the rest of the payload still syncs in the
same PATCH. This requires the App to have the org-level **"Custom
properties: read"** permission; without it (or on any schema-endpoint
error), repo-guardian fails open and sends the full, unfiltered payload —
exactly its pre-preflight behavior. A dropped property is logged
(`"custom properties missing from org schema"`, with `org` and
`missing_properties` fields — see
[docs/operations/scaling.md](../operations/scaling.md#custom-property-schema-preflight-impl-0017-phase-3)
for the Loki matching contract) and counted via
`repo_guardian_custom_property_missing_schema_total{org, property}`. The
App **never creates or mutates** schema definitions itself — an operator
who wants a mapped property must define it in the org's custom-property
schema through GitHub's own settings UI/API.

`annotation_properties` validation (fails policy load, all errors reported
together):

- Annotation keys and property names must be non-empty.
- Property names must match GitHub's charset/length constraint
  (`^[a-zA-Z0-9_$#-]{1,75}$`) — alphanumerics plus `-`, `_`, `$` and `#`.
  A period is **not** allowed. Releases before appVersion 1.10.1 used a
  pattern that had this backwards; see
  [property-name charset](../operations/property-name-charset.md) if a
  policy that used to load now fails.
- Property names may not be (case-insensitively) `Owner` or `Component` —
  those are the built-in, non-remappable names.
- Two annotations may not target the same property name.

### `label_sync`

Manages the repo's labels from the YAML file the rule targets:

```yaml
labels:
  - name: bug
    color: d73a4a
    description: Something isn't working
  - name: feature            # renames "enhancement" if it exists
    color: a2eeef
    renamed_from: enhancement
```

Creates missing labels, updates color/description drift, applies renames
via `renamed_from`, and — only when `delete_extra = true` — removes labels
not listed.

### `branch_protection`

Manages branch protection rulesets from the YAML file the rule targets
(per-repo, data-driven variant of `rule "branch_protection"`):

```yaml
rules:
  - branch: main
    require_pr: true
    required_approvals: 2
    dismiss_stale_reviews: true
    require_status_checks: ["ci/test"]
    require_linear_history: true
```

### `workflow_sync`

Lightweight observability for watched workflow files — logs and counts
drift events for the rule's paths. Pair with `watch = true` and
`check = "exact"` for immediate detection when someone edits the
org-standard workflow.

## `pr {}` — pull request templates

Valid at three scopes: `defaults { pr {} }`, `rule "file" { pr {} }`, and
`reconcile "<type>" { pr {} }`.

| Attribute | Type | Description |
|-----------|------|-------------|
| `search_terms` | list(string) | Substrings matched case-insensitively against the title and head branch of **third-party** open PRs; a match skips the rule so repo-guardian yields to whoever is already doing the work. repo-guardian's own reconcile PR is never matched. Blank entries are rejected at load (they would match every PR). Rule scope only in practice. |
| `title` | string (template) | PR title template. Parse errors fail policy load with a location-prefixed message. |
| `body` | string (template) | PR body template. Bodies over 65,000 characters are truncated with an HTML-comment marker appended. |
| `labels` | list(string) | Labels applied to the PR. `labels = []` is an explicit "no labels" override, distinct from omitting the attribute. |
| `inherits` | bool | Default `true`. When `true`, fields you leave unset inherit from the parent scope. `false` stops inheritance entirely — unset fields fall through to engine built-ins. |

Resolution rules:

- **Field-by-field merge, child wins.** A rule that sets only `title` gets
  `body` and `labels` from `defaults.pr`.
- **Reconciler PRs merge `reconcile.pr → defaults.pr` only** — the enclosing
  rule's `pr` is deliberately skipped.
- **Multi-rule bundles** (several file rules landing in one PR): each rule's
  title is rendered; on conflict the bundle falls back to `defaults.pr.title`
  (or the built-in title) and logs the ignored titles. Bundle bodies always
  use `defaults.pr.body` — per-rule bodies are implicitly single-rule.

## Template reference

Policy `pr.title`/`pr.body` strings and all file templates are Go
`text/template`, compiled at policy-load time.

### Variables — PR templates (`PRVars`)

| Variable | Type | Description |
|----------|------|-------------|
| `.Owner` | string | Org or user login owning the repo. |
| `.Repo` | string | Repository name (no owner prefix). |
| `.DefaultBranch` | string | The repo's default branch. |
| `.Date` | string | RFC3339 timestamp at render time. |
| `.Rule.Name` / `.Rule.Target` | string | The firing rule's name and target path (single-rule PRs). |
| `.Rules` | list | Every firing rule in a bundled PR (`{Name, Target}` each); nil for single-rule PRs. |
| `.Files` | list(string) | Every file path included in the PR. |
| `.Reconciler` | string | Reconciler name when the PR was opened by a reconciler; empty otherwise. |

### Variables — file templates (`FileVars`)

`.Owner`, `.Repo`, `.DefaultBranch`, `.Date`, and `.Rule` as above, plus:

| Variable | Type | Description |
|----------|------|-------------|
| `.Org` | string | Convenience alias for the owner login. |
| `.Catalog` | object or nil | Parsed catalog-info fields for catalog-aware templates: `.Owner`, `.Component`, and `.Properties` (a `map[string]string` keyed by the GitHub property name — every `annotation_properties` target is present, with an empty string meaning "absent, will clear"; access via `{{ index .Catalog.Properties "JiraProject" }}`). **Guard with `{{ if .Catalog }}`** — it is nil for most rules. |

### Helpers

| Helper | Usage | Result |
|--------|-------|--------|
| `env` | `{{ env "JIRA_PROJECT" }}` | Value of the process env var, `""` if unset. Never reference secrets — rendered output is public PR text. |
| `default` | `{{ .Field \| default "fallback" }}` | `fallback` when the value is empty. |
| `join` | `{{ .Files \| join ", " }}` | Slice joined with the separator. |
| `lower` / `upper` | `{{ lower .Repo }}` | Case conversion. |
| `title` | `{{ title "hello world" }}` | Uppercases each word's first ASCII letter (`Hello World`); leaves the rest untouched. |

### Escaping literal `{{ }}` (GitHub Actions templates)

Templates that must emit literal `${{ ... }}` (GHA expressions) wrap them in
a raw-string action — a bare `${{` anywhere in a template (even a comment)
is a parse error:

```text
{{`${{ secrets.GITHUB_TOKEN }}`}}
```

### Strict validation

`STRICT_TEMPLATES=true` (or `--strict-templates`) validates every compiled
PR template against a zero-value context at startup — typo'd field names
fail the boot instead of a render at 2am. File-content templates are not
strict-validated (legitimate catalog references would false-positive).

## Built-in defaults

With no `GUARDIAN_CONFIG` set, this policy applies:

| Rule | Mode | Enabled | Paths → Target | Notes |
|------|------|---------|----------------|-------|
| `codeowners` | exists | ✅ | `CODEOWNERS`, `.github/CODEOWNERS`, `docs/CODEOWNERS` → `.github/CODEOWNERS` | |
| `dependabot` | exists | ✅ | `.github/dependabot.yml`, `.github/dependabot.yaml` → `.github/dependabot.yml` | |
| `renovate_config` | contains | ❌ | `renovate.json`, `renovate.json5`, `.renovaterc`, `.renovaterc.json`, `.github/renovate.json`, `.github/renovate.json5` → `renovate.json` | Assertion: must match `github>.*renovate-config`. |
| `renovate_workflow` | exact | ❌ | `.github/workflows/renovate.yml` | `workflow_sync` reconciler with `watch = true`. |
| `catalog_info` | exists | only when `CUSTOM_PROPERTIES_MODE` is set | `catalog-info.yaml`, `catalog-info.yml` → `catalog-info.yaml` | `custom_properties` reconciler in the env var's mode (backward compat). |

## Environment variable overrides

Env vars are applied **after** HCL and win. The `guardian {}`-scope
overrides:

| Env var | Overrides |
|---------|-----------|
| `DRY_RUN` | `guardian.dry_run` |
| `SCHEDULE_INTERVAL` | `guardian.schedule_interval` |
| `WORKER_COUNT` | `guardian.worker_count` |
| `QUEUE_SIZE` | `guardian.queue_size` |
| `LOG_LEVEL` | `guardian.log_level` |
| `SKIP_FORKS` | `guardian.skip_forks` |
| `SKIP_ARCHIVED` | `guardian.skip_archived` |
| `RATE_LIMIT_THRESHOLD` | `guardian.rate_limit_threshold` |
| `WEBHOOK_IP_ALLOWLIST` | `guardian.webhook_ip_allowlist` |
| `WEBHOOK_IP_ALLOWLIST_FAIL_OPEN` | `guardian.webhook_ip_allowlist_fail_open` |
| `TRUST_PROXY_HEADERS` | `guardian.trust_proxy_headers` |
| `AUTO_CLOSE_PR` | `guardian.auto_close_pr` |

Related policy/template env vars (not `guardian {}` attributes):

| Env var | Effect |
|---------|--------|
| `GUARDIAN_CONFIG` | Path to the policy file or directory. |
| `TEMPLATE_DIR` | Directory of template overrides (default `/etc/repo-guardian/templates`). |
| `STRICT_TEMPLATES` | Enable strict PR-template validation at startup. |
| `CUSTOM_PROPERTIES_MODE` | Backward-compat: injects the `catalog_info` built-in rule when no HCL config is present. Ignored when HCL defines file rules. |

Deployment-level configuration (GitHub App credentials, listen addresses,
store/queue backends, sweep freshness, discovery tuning) is env-var-only
and documented in the
[Helm chart README](https://github.com/donaldgifford/repo-guardian/tree/main/charts/repo-guardian)
and [scaling guide](../operations/scaling.md).

## Validation reference

Startup fails (all errors reported together) when:

- `guardian.worker_count` ≤ 0, `guardian.queue_size` ≤ 0,
  `guardian.rate_limit_threshold` outside [0.0, 1.0], or
  `guardian.log_level` not in `debug|info|warn|error`.
- Any file rule: `check` not in `exists|contains|exact|absent`; empty
  `paths`; a non-`absent` rule missing `target` or `template`; assertions
  on a non-`contains` rule; any assertion violating the
  [assertion rules](#assertion-content-assertions).
- Any `absent` rule that declares `target`, `template`, an `assertion`
  block, or a `reconcile` block.
- Any `when {}` gate: empty block; `rule_satisfied` naming a rule that
  doesn't exist, is disabled, is a setting/branch-protection rule, or is
  the gated rule itself; or a gate cycle of any length
  (`a → b → a`, `a → b → c → a`, …).
- Any setting rule: empty name, unsupported `property`, missing `expected`,
  or `expected` type not matching the property.
- Any branch-protection rule: empty name or `branch`,
  `required_approvals` < 0.
- Duplicate rule names within a rule type.
- Any `pr.title`/`pr.body` template that fails to parse.
- Strict mode: a rule without its own `scope {}` block.
- A `reconcile "custom_properties"` block whose `mode` is not `api` or
  `github-action` (fails at engine construction).
- Any `annotation_properties` entry: empty annotation key or property name,
  a property name outside GitHub's charset/length constraint, a property
  name of `Owner`/`Component` (case-insensitive), or two annotations
  targeting the same property name.

## Cookbook

Minimal starting points — full runnable versions in
[`examples/`](https://github.com/donaldgifford/repo-guardian/tree/main/examples).

**1. Baseline files only** (mirror the built-ins, PRs only, no writes):
`guardian-minimal.hcl`.

**2. Enforce the org Renovate preset:**

```hcl
rule "file" "renovate_config" {
  check    = "contains"
  paths    = ["renovate.json", ".github/renovate.json"]
  target   = "renovate.json"
  template = "renovate"
  assertion {
    pattern = "github>myorg/renovate-config"
    message = "must extend the org preset"
  }
}
```

**3. Catalog-driven ownership → GitHub custom properties:**

```hcl
rule "file" "catalog_info" {
  check    = "contains"
  paths    = ["catalog-info.yaml", "catalog-info.yml"]
  target   = "catalog-info.yaml"
  template = "catalog-info"
  assertion {
    yaml_path = "spec.owner"
    non_empty = true
    message   = "spec.owner must be set"
  }
  reconcile "custom_properties" {
    mode  = "api"
    watch = true
    annotation_properties = {
      "jira/project-key" = "JiraProject"
      "jira/label"        = "JiraLabel"
    }
  }
}
```

**4. Settings hardening, survey first:**

```hcl
rule "setting" "vuln_alerts"  { property = "vulnerability_alerts_enabled" expected = true  remediate = false }
rule "setting" "no_wiki"      { property = "has_wiki"                     expected = false remediate = false }
rule "setting" "tidy_merges"  { property = "delete_branch_on_merge"       expected = true  remediate = false }
# Watch repo_guardian metrics + logs, then flip remediate = true.
```

**5. Multi-org strict mode** — shared rules on `["*"]`, org-specific rules
on subsets: `guardian-multi-org.hcl` (single file) or `guardian-multi-org/`
(directory layout), and `guardian-enterprise.hcl` for the one-App-many-orgs
enterprise topology.
