# Template migration guide (chart 0.3.x → 0.4.0)

Chart `0.4.0` ships [IMPL-0012](../impl/0012-customizable-pr-templates-and-extensible-template-configmap.md):
PR-template customization at three HCL scopes plus a generic
`templates.files` map that replaces the legacy hardcoded slots.

This guide walks operators through the breaking changes.

## Chart values delta

### Removed

The legacy slots are gone. Move existing values into the new
`templates.files` map keyed by filename:

```yaml
# Before (chart 0.3.x):
templates:
  codeowners: |
    * @platform-team
  dependabot: |
    version: 2
    updates: []
  renovate: |
    {"$schema": "https://docs.renovatebot.com/renovate-schema.json"}

# After (chart 0.4.0):
templates:
  files:
    codeowners.tmpl: |
      * @platform-team
    dependabot.tmpl: |
      version: 2
      updates: []
    renovate.tmpl: |
      {"$schema": "https://docs.renovatebot.com/renovate-schema.json"}
```

The chart NOTES.txt emits a warning when it detects the legacy
keys still present in your values.

### Added

| Key                            | Default | Purpose                                                         |
|--------------------------------|---------|-----------------------------------------------------------------|
| `templates.files`              | `{}`    | Map of filename → template content                              |
| `templates.existingConfigMap`  | `""`    | Mount a pre-existing ConfigMap at `TEMPLATE_DIR`                |
| `templating.vars`              | `{}`    | Map of env-var key → value, surfaced via the `env` template helper |
| `templating.strict`            | `false` | Sets `STRICT_TEMPLATES=true` for startup validation              |

## Embedded-template syntax change

The shipped templates were rewritten in IMPL-0012 Phase 3 to use
Go-template dotted-path syntax everywhere. If you ship custom
`.tmpl` files via `templates.files` and they use the **old**
placeholder syntax, you must rewrite them.

### Mapping

| Legacy placeholder      | Replace with               |
|-------------------------|----------------------------|
| `OWNER_VALUE`           | `{{ .Catalog.Owner }}`     |
| `COMPONENT_VALUE`       | `{{ .Catalog.Component }}` |
| `JIRA_PROJECT_VALUE`    | `{{ .Catalog.JiraProject }}` |
| `JIRA_LABEL_VALUE`      | `{{ .Catalog.JiraLabel }}` |
| `REPO_NAME`             | `{{ .Repo }}`              |
| `ORG_NAME`              | `{{ .Owner }}`             |

### GitHub Actions expression escapes

Templates that emit GHA `${{ ... }}` expressions must wrap them
in a backtick-raw-string Go-template action so the renderer sees
literal text:

```text
# Before:
GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}

# After (renders to the same literal):
GH_TOKEN: {{`${{ secrets.GITHUB_TOKEN }}`}}
```

Bare `${{` anywhere in a template (including YAML comments) trips
the Go-template parser with `unexpected <.> in operand`.

## PR template configuration

PR titles, bodies, and labels are now configurable at three
scopes: `defaults { pr {} }`, `rule { pr {} }`, and
`reconcile { pr {} }`. See the
["Customizing PR text" section in
docs/ADDING_RULES.md](../ADDING_RULES.md#customizing-pr-text) for
the full grammar, available variables, helpers, and resolution
chain.

Quick start:

```hcl
defaults {
  pr {
    title  = "[{{ env \"JIRA_PROJECT\" | default \"GUARDIAN\" }}] guardian: {{ .Repo }}"
    labels = ["automated", "guardian"]
  }
}

rule "file" "codeowners" {
  # ...
  pr {
    title = "chore({{ .Repo }}): adopt CODEOWNERS"
  }
}
```

## Validation steps

### Pre-merge in CI

Run a dry-render of your policy with strict mode enabled before
merging changes:

```bash
STRICT_TEMPLATES=true repo-guardian --strict-templates &
sleep 1
kill %1 || true
```

Or use the CLI flag directly. Strict mode walks every compiled
PR template in the loaded policy and validates against a
zero-value `PRVars` context. Any reference to a field that
doesn't exist on `PRVars` (e.g. `.Catalog.Owner`, which is on
`FileVars` only) fails startup with a location-prefixed error
like:

```text
strict template validation failed:
  rule "codeowners".pr.title: render template "rule \"codeowners\".pr.title": template: ... map has no entry for key "Catalog"
```

### Pre-deploy with helm

Validate the rendered chart against your cluster's API:

```bash
helm template my-release ./charts/repo-guardian \
  --namespace repo-guardian \
  --values my-values.yaml \
  | kubectl apply --dry-run=client -f -
```

The chart fails render time when `templating.vars` collides with
a reserved env-var name; check the error message for the
offending keys.

## Rollback

Chart `0.4.0` is backwards-incompatible at the values surface
only. The binary remains compatible with `appVersion 1.4.x`
deployments — if a rollback is needed:

1. Pin chart version: `helm upgrade --version 0.3.3 ...`
2. Restore old `templates.codeowners` / etc. from your previous
   values.yaml.
3. Roll the binary back independently if the issue is binary-side.

Both binary and chart cadences are independent, so chart
rollbacks don't require coordinated binary rollbacks.

## See also

- [IMPL-0012](../impl/0012-customizable-pr-templates-and-extensible-template-configmap.md) — full implementation plan
- [DESIGN-0013](../design/0013-customizable-pr-templates-and-extensible-template-configmap.md) — design rationale
- [docs/ADDING_RULES.md "Customizing PR text"](../ADDING_RULES.md#customizing-pr-text) — operator-facing reference
