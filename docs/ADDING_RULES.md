# Adding a New Rule to Repo Guardian

This guide walks through adding a new file rule to repo-guardian. By the end,
the service will detect when a repository is missing the file and create a PR to
add a default version.

We will use a GitHub Actions CI workflow (`.github/workflows/ci.yml`) as the
example, but the process is identical for any file type.

---

## How Rules Work

A rule is a `FileRule` struct that tells the checker engine:

1. **What to look for** -- one or more file paths to check (if any exist, the
   rule is satisfied).
2. **How to detect existing PRs** -- search terms matched against open PR titles
   and branch names to avoid duplicate work.
3. **What to create** -- a target path and a default template for the file
   content.

The engine iterates over all enabled rules for every repository it checks. No
changes to the engine, webhook handler, scheduler, or queue code are needed.

---

## Step 1: Create the Default Template

Templates live in `internal/rules/templates/` and are embedded into the binary
at compile time via `//go:embed`. The file name (minus `.tmpl`) becomes the
template key referenced in the rule definition.

Create `internal/rules/templates/github-actions-ci.tmpl`:

```yaml
# Default CI workflow — adjust triggers and steps for your project.
# This file was added by repo-guardian. Review and customize before merging.
name: CI

on:
  pull_request:
    branches: [main]
  push:
    branches: [main]

permissions:
  contents: read

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Build
        run: echo "Add your build steps here"

      - name: Test
        run: echo "Add your test steps here"
```

The template should be a reasonable starting point that works without
modification but clearly signals where teams should customize. Comments in the
file help developers understand what to change.

---

## Step 2: Add the Rule to the Registry

Open `internal/rules/registry.go` and append a new entry to the `DefaultRules`
slice:

```go
var DefaultRules = []FileRule{
    // ... existing CODEOWNERS, Dependabot, and Renovate rules ...

    {
        Name: "GitHub Actions CI",
        Paths: []string{
            ".github/workflows/ci.yml",
            ".github/workflows/ci.yaml",
            ".github/workflows/build.yml",
            ".github/workflows/build.yaml",
            ".github/workflows/test.yml",
            ".github/workflows/test.yaml",
        },
        PRSearchTerms:       []string{"ci workflow", "github actions", "CI/CD"},
        DefaultTemplateName: "github-actions-ci",
        TargetPath:          ".github/workflows/ci.yml",
        Enabled:             true,
    },
}
```

### Field Reference

| Field | Purpose | Guidelines |
|---|---|---|
| `Name` | Human-readable label used in logs, PR body, and the `rule_name` Prometheus metric label. | Keep it short and descriptive. |
| `Paths` | All locations where the file might already exist. The rule is satisfied if **any** path exists. | Include common naming variations (`.yml` vs `.yaml`, alternate directories). |
| `PRSearchTerms` | Strings matched (case-insensitive) against open PR titles and branch names. If a match is found, the rule is skipped. | Use terms specific enough to avoid false positives but broad enough to catch related PRs from other tools or developers. |
| `DefaultTemplateName` | Key into the template store. Must match the template file name without the `.tmpl` extension. | Must exactly match the file created in Step 1. |
| `TargetPath` | Path where the file will be created in the PR branch. | Use the canonical/preferred location for the file. |
| `Enabled` | Whether the rule is active. Set to `false` to define a rule without activating it. | Start with `true` unless you want to ship the rule dormant. |

### A Note on `Paths`

The `Paths` field is intentionally broad. Many tools accept multiple file
locations or naming conventions. For the GitHub Actions CI example, a team might
already have a workflow named `build.yml` or `test.yml` that serves the same
purpose. Listing these alternate paths prevents repo-guardian from creating a
duplicate CI workflow when one already exists under a different name.

### A Note on `PRSearchTerms`

These terms prevent repo-guardian from opening a PR when someone is already
working on the same thing. Be specific enough to avoid false matches (a term
like `"add"` would match too many unrelated PRs) but broad enough to catch
PRs with different naming conventions.

---

## Step 3: Build and Test

Run the existing tests to make sure nothing is broken:

```bash
make check    # lint + tests with race detector
```

The existing tests exercise the registry and engine generically -- they iterate
over `DefaultRules` -- so adding a new rule entry does not require new test
code unless the rule has unusual behavior. The key things to verify:

1. The template file name matches `DefaultTemplateName` (the template store
   will fail to load if there is a mismatch).
2. `Paths` entries are valid file paths (no leading `/`, no glob patterns).
3. `TargetPath` does not conflict with another rule's `TargetPath`.

If you want to test the new rule in isolation:

```bash
go test -v -race -run TestCheckRepo ./internal/checker/...
```

---

## Step 4: Test with Dry-Run Mode

Before deploying, validate the new rule against real repositories using dry-run
mode. Set the `DRY_RUN=true` environment variable. The service will log every
action it would take without actually creating branches or PRs:

```
INFO  dry run: would create PR  owner=myorg  repo=new-project  missing_files="[GitHub Actions CI]"
```

This confirms the rule is detecting the right repositories and that the
template name resolves correctly.

---

## Step 5: Deploy

If you are overriding templates via the Kubernetes ConfigMap (rather than using
the compiled-in defaults), add the new template to the ConfigMap as well:

```yaml
# deploy/base/configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: repo-guardian-templates
data:
  # ... existing templates ...
  github-actions-ci: |
    name: CI
    on:
      pull_request:
        branches: [main]
      push:
        branches: [main]
    permissions:
      contents: read
    jobs:
      build:
        runs-on: ubuntu-latest
        steps:
          - uses: actions/checkout@v4
          - name: Build
            run: echo "Add your build steps here"
          - name: Test
            run: echo "Add your test steps here"
```

If you are relying on the embedded templates (the default), the ConfigMap step
is not needed -- the template is compiled into the binary.

Build and deploy:

```bash
make build
docker build -t repo-guardian:latest .
# Push to your registry and roll out the new version.
```

---

## What Happens at Runtime

Once deployed, the new rule participates in every repository check:

1. The engine loads all enabled rules from the registry.
2. For the new rule, it checks whether any of the `Paths` exist in the repo.
3. If none exist, it checks whether any open PR title or branch contains a
   `PRSearchTerms` match.
4. If the file is missing and no existing PR addresses it, the file is added to
   the repo-guardian PR branch using the default template.
5. The PR body lists all missing files, including the new one.

The `repo_guardian_files_missing_total` Prometheus counter will start recording
detections with `rule_name="GitHub Actions CI"`. If you are using the
contributed Grafana dashboard (`contrib/grafana/repo-guardian-dashboard.json`),
the new rule will appear automatically in the "Missing Files Detected by Rule"
and "Missing Files Total by Rule" panels.

---

## Summary

| Step | File | What to do |
|---|---|---|
| 1 | `internal/rules/templates/<name>.tmpl` | Create the default file content |
| 2 | `internal/rules/registry.go` | Add a `FileRule` entry to `DefaultRules` |
| 3 | -- | `make check` |
| 4 | -- | Deploy with `DRY_RUN=true` and verify logs |
| 5 | -- | Deploy to production |

No changes are needed in the checker engine, webhook handler, scheduler, work
queue, or any other package. The rule registry pattern is designed so that
adding a new compliance check is a two-file change.

---

## Alternative: HCL Policy Configuration

When an HCL policy config file is present (set via `GUARDIAN_CONFIG` env var),
file rules are defined in the config rather than the Go registry. This approach
supports additional check modes (`contains`, `exact`) with content assertions
and reconcilers for post-check actions.

See `docs/design/0006-hcl-policy-configuration-and-rule-engine.md` for the
full design, and `docs/design/0007-reconciler-interface-and-push-event-handler.md`
for reconciler details.

### Example: Adding a Rule via HCL

```hcl
rule "file" "github_actions_ci" {
  enabled  = true
  check    = "exists"
  paths    = [".github/workflows/ci.yml", ".github/workflows/ci.yaml"]
  target   = ".github/workflows/ci.yml"
  template = "github-actions-ci"

  pr {
    search_terms = ["ci workflow", "github actions"]
  }
}
```

Rules defined in HCL replace the built-in defaults entirely. The template file
(`internal/rules/templates/<name>.tmpl`) is still required.

### Reconcilers

HCL rules can attach reconcilers — pluggable post-check actions that run after
file checks pass. For example, the `custom_properties` reconciler reads
`catalog-info.yaml` and syncs ownership metadata to GitHub custom properties:

```hcl
rule "file" "catalog_info" {
  check    = "exists"
  paths    = ["catalog-info.yaml", "catalog-info.yml"]
  target   = "catalog-info.yaml"
  template = "catalog-info"

  pr {
    search_terms = ["catalog-info"]
  }

  reconcile "custom_properties" {
    mode  = "api"
    watch = true
  }
}
```

When `watch = true`, push events that modify the watched files on the default
branch trigger a re-check.

### Renovate File Rules

repo-guardian includes two built-in Renovate file rules that are **disabled
by default**. When enabled, they ensure every repository has a standardized
Renovate workflow and configuration extending the org preset.

To enable them, add the following to your `guardian.hcl`:

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

#### Templates

| Template Name | Description |
|---|---|
| `renovate-workflow` | Docker-based GitHub Actions workflow that runs `renovate/renovate:latest` on a weekly cron schedule. Uses `actions/create-github-app-token@v1` for authentication. |
| `renovate` | Minimal `renovate.json` extending the org preset (`github>ORG_NAME/renovate-config`). |

#### Check Modes

- **`renovate_workflow`** uses `check = "exact"` — the file must match the
  template byte-for-byte (YAML-semantic comparison). Any drift triggers a PR
  to restore the canonical workflow.
- **`renovate_config`** uses `check = "contains"` with an assertion — the file
  must exist and contain the org preset pattern. Teams can add additional
  Renovate configuration (labels, automerge rules) as long as the org preset
  reference is present.

#### Prerequisites

The Renovate workflow template expects two GitHub Actions secrets:

- `RENOVATE_APP_ID` — the GitHub App ID for Renovate
- `RENOVATE_APP_PRIVATE_KEY` — the GitHub App private key

These must be configured as organization-level secrets.

## Multi-org Configuration

repo-guardian supports two scope modes for installations that span
multiple GitHub organizations:

### Legacy mode (default)

When the HCL config has no top-level `scope { }` block, every
enabled rule applies to every repository the GitHub App sees. This
is the simpler default for single-org users — nothing changes.

### Strict mode

Declaring a top-level `scope { }` block engages strict mode. Every
rule must declare its own `scope { orgs = [...] }` sub-block:

```hcl
scope {
  orgs = ["myorg-prod", "myorg-staging"]
}

rule "file" "codeowners" {
  paths    = ["CODEOWNERS"]
  target   = "CODEOWNERS"
  template = "codeowners"

  scope {
    orgs = ["*"]   # applies to every org in the top-level scope
  }
}

rule "branch_protection" "main_required" {
  branch                 = "main"
  required_approvals     = 2
  remediate              = true

  scope {
    orgs = ["myorg-prod"]   # applies only to myorg-prod
  }
}
```

Scope semantics:

- **Glob matching** — `path.Match` patterns (`*`, `?`, `[abc]`)
  on lowercased org names, mirroring the `ignore` block.
- **Universal `["*"]`** — at the rule level, this is the explicit
  "apply to every org listed in the top-level scope" idiom.
- **Strict-mode validation** runs at config load:
  - Top-level `scope.orgs` must be non-empty
  - Every `rule { }` (file, setting, branch_protection) must
    have a `scope { }` sub-block with non-empty `orgs`
  - Across a directory load, only one top-level `scope { }`
    may exist
- **Splitting across files** — when `GUARDIAN_CONFIG` points to
  a directory, all `.hcl` files merge before strict validation
  runs. Put the top-level scope in a dedicated `scope.hcl` and
  per-org rules in separate files. See `examples/guardian-multi-org/`.

### Strict-mode error messages

| Error | Cause | Fix |
|-------|-------|-----|
| `top-level scope must declare at least one org` | `scope { }` block is empty | Add at least one entry to `orgs` |
| `only one top-level scope block allowed, found N` | Multiple files in the directory both declare `scope { }` | Consolidate to a single `scope.hcl` |
| `rule "X" must declare scope in strict mode` | A rule has no `scope { }` sub-block | Add `scope { orgs = ["*"] }` for shared rules, or a subset for org-specific rules |
| `rule "X" scope.orgs must not be empty` | Rule's `scope.orgs` is `[]` | Populate the list, or remove the `scope` block (which then triggers the previous error) |

### Legacy-mode warning

If a rule defines a `scope { }` sub-block while the top-level
`scope { }` is missing, repo-guardian logs a single warning at
load time:

```text
WARN per-rule scope ignored: no top-level scope { } block declared.
     Add a top-level scope block to enable strict mode, or remove
     per-rule scope blocks.
```

This means the per-rule scopes are present but ignored — the rule
will run against every repo. Either declare a top-level `scope { }`
to opt in, or remove the per-rule scope blocks.

### Observability

Out-of-scope evaluations are tracked via the
`repo_guardian_out_of_scope_total{level, org}` Prometheus counter:

- `level="policy"` — increments once per enabled rule when the
  top-level scope rejects the repo (the entire repo is skipped).
- `level="rule"` — increments once per rule when its own scope
  rejects the repo (other rules may still apply).

In legacy mode this counter is always zero. A sustained nonzero
`level="rule"` for an org with no other activity may indicate a
typo in `scope.orgs` — see the
[`RepoGuardianRuleNeverApplies`](../contrib/prometheus/alerts.yaml)
alert and the [metrics catalog](../contrib/README.md) for queries
and migration recipes.

## Customizing PR text

Repo Guardian renders PR titles and bodies through a unified
Go-template engine (`internal/template`) at three configurable
scopes. The grammar mirrors HCL's standard block style:

```hcl
defaults {
  pr {
    title  = "..."
    body   = "..."
    labels = ["..."]
  }
}

rule "file" "codeowners" {
  # ...
  pr {
    title    = "..."
    inherits = true   # default
  }
}

rule "file" "catalog_info" {
  reconcile "custom_properties" {
    pr {
      body     = "..."
      inherits = false  # opt out of defaults inheritance
    }
  }
}
```

### Resolution chain

| PR opened by                | Chain                                  |
|-----------------------------|----------------------------------------|
| File rule                   | `rule.pr → defaults.pr → built-in`     |
| Reconciler (e.g. catalog)   | `reconciler.pr → defaults.pr → built-in` |
| Bundled (multi-rule) PR     | per-rule titles voted; conflict → `defaults.pr.title`; body always from `defaults.pr.body` |

**Reconciler PRs deliberately skip `rule.pr`.** A file rule's PR
text describes its file change; a reconciler that opens a
side-channel PR (e.g. `set-custom-properties.yml`) is a separate
operation with its own narrative. Letting `rule.pr` flow into the
reconciler would mean `chore: adopt CODEOWNERS` rendering on a
custom-properties workflow PR.

### Field-by-field merge

Each of `title`, `body`, `labels` independently inherits when
unset and `inherits = true`. Setting `inherits = false` at any
scope short-circuits the chain — only fields that scope itself
declared are honored. `labels = []` is an explicit empty-list
override (different from omitting the attribute).

### Variables available in templates

PR templates render against `template.PRVars`:

| Field            | Type       | Notes                                        |
|------------------|------------|----------------------------------------------|
| `.Owner`         | string     | GitHub org login                             |
| `.Repo`          | string     | Repository name                              |
| `.DefaultBranch` | string     | Repository's default branch (e.g. `main`)    |
| `.Date`          | string     | RFC3339 timestamp at render time             |
| `.Rule.Name`     | string     | Single-rule PRs only                         |
| `.Rule.Target`   | string     | Single-rule PRs only                         |
| `.Rules`         | `[]Rule`   | Multi-rule bundles only (zero in single)     |
| `.Files`         | `[]string` | Every target path included in this PR        |
| `.Reconciler`    | string     | Reconciler name (e.g. `custom_properties`); empty for file-rule PRs |

### Helpers

| Helper          | Purpose                                              |
|-----------------|------------------------------------------------------|
| `env "VAR"`     | Read process env var. Returns empty string if unset. |
| `default x y`   | Return `y` if non-empty; otherwise `x`.              |
| `join sep list` | Comma-style join: `{{ join ", " .Files }}`.          |
| `lower s`       | Lowercase the input.                                 |
| `upper s`       | Uppercase the input.                                 |
| `title s`       | Capitalize the first ASCII letter.                   |

### Example

```hcl
defaults {
  pr {
    title  = "[{{ env \"JIRA_PROJECT\" | default \"GUARDIAN\" }}] guardian"
    body   = "Files:\n{{ range .Files }}- `{{ . }}`\n{{ end }}"
    labels = ["automated", "guardian"]
  }
}
```

With `JIRA_PROJECT=PLAT` set on the Deployment, the rendered title
becomes `[PLAT] guardian`. With it unset, `[GUARDIAN] guardian`.

### What NOT to do

**Never reference secret env vars from PR templates.** The
rendered output is visible to PR reviewers — anyone with read
access to the repository sees it. Operators who write the policy
HCL can read any process env var via `{{ env "VAR" }}`, including
`GITHUB_PRIVATE_KEY` and `WEBHOOK_SECRET`. The threat model
assumes the operator who provisions runtime secrets is the same
operator who writes the policy; reading the operator's own
secrets is not privilege escalation, but emitting them into PR
text is. The reserved-name list in
`charts/repo-guardian/templates/_helpers.tpl` blocks
`templating.vars` from declaring secret names directly, but the
`env` helper is unrestricted by design — operator discipline is
the only line of defense.

### Strict-mode validation

Set `STRICT_TEMPLATES=true` (or pass `--strict-templates` on the
binary command line; the chart toggles this via
`templating.strict: true`) to validate every compiled PR template
at startup against a zero-value PRVars context. Templates that
reference fields not on `PRVars` (e.g. `.Catalog.Owner`, which
exists only on `FileVars`) fail startup with a location-prefixed
error. Run this in CI to catch typos before deploy.
