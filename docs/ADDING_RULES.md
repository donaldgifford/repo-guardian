# Adding a New Rule to Repo Guardian

This guide walks through adding a new file rule to repo-guardian. By the end,
the service will detect when a repository is missing the file and create a PR
to add a default version.

We will use a GitHub Actions CI workflow (`.github/workflows/ci.yml`) as the
example, but the process is identical for any file type.

---

## How Rules Work

Rules are defined in HCL (the operator-facing config) and live in
`guardian.hcl` (pointed at by the `GUARDIAN_CONFIG` env var). The
checker engine loads the policy on startup and iterates every enabled
rule for every repository it processes. No changes to the engine,
webhook handler, scheduler, or queue code are needed for a new rule.

A file rule tells the engine:

1. **What to look for** — one or more file paths (the rule is satisfied
   if any one exists; for `contains`/`exact` modes the rule additionally
   evaluates content assertions).
2. **What to create** — a target path and the name of a template
   (resolved from the operator's `templates.files` map or the binary's
   embedded defaults).
3. **How to render the PR** — title, body, labels, and search terms
   used to detect existing PRs and avoid duplicate work.

If you want a rule to ship as a built-in default (enabled out of the
box for every operator), it goes in `internal/policy/defaults.go`
alongside the existing four. The HCL path is preferred for everything
else — it requires no rebuild.

---

## Step 1: Create the Default Template

Templates live in `internal/rules/templates/` and are embedded into the
binary at compile time via `//go:embed`. The file name (minus `.tmpl`)
becomes the template key referenced from HCL or the Helm
`templates.files` map.

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
modification but clearly signals where teams should customize.

> **GitHub Actions `${{ ... }}` syntax.** Templates are rendered by
> `internal/template/Renderer` (text/template). To emit a literal
> `${{ secrets.X }}` you must wrap it in a backtick-raw-string action:
> `` {{`${{ secrets.X }}`}} ``. A bare `${{` anywhere — including a
> YAML comment — trips the parser with `unexpected <.> in operand`.

If you do not need the template baked into the binary, you can also
ship the template content directly via Helm values
(`templates.files: <name>: <content>`) — no rebuild required.

---

## Step 2: Declare the Rule in HCL

Add a `rule "file" "..." { ... }` block to your `guardian.hcl`:

```hcl
rule "file" "github_actions_ci" {
  enabled  = true
  check    = "exists"
  paths    = [
    ".github/workflows/ci.yml",
    ".github/workflows/ci.yaml",
    ".github/workflows/build.yml",
    ".github/workflows/build.yaml",
    ".github/workflows/test.yml",
    ".github/workflows/test.yaml",
  ]
  target   = ".github/workflows/ci.yml"
  template = "github-actions-ci"

  pr {
    search_terms = ["ci workflow", "github actions"]
  }
}
```

### Field Reference

| Field | Purpose | Guidelines |
|---|---|---|
| `enabled` | Whether the rule is active. | Defaults to `true`. Set `false` to define a rule without activating it. |
| `check` | One of `exists`, `contains`, `exact`. | `exists` is the default. Use `contains` with `assertion { ... }` blocks to enforce content patterns. Use `exact` to require a byte-for-byte (or YAML-semantic) match against the template. |
| `paths` | All locations where the file might already exist. The rule is satisfied if **any** path exists (in `exists` mode). | Include common naming variations (`.yml` vs `.yaml`, alternate directories). |
| `target` | Path where the file will be created in the PR branch. | Use the canonical/preferred location. |
| `template` | Key into the template store. Must match the template file name without the `.tmpl` suffix. | Must match the file created in Step 1. |
| `pr { search_terms }` | Strings matched case-insensitively against the titles and branch names of *third-party* open PRs. A match causes the rule to skip (someone else's PR is assumed to be in flight). repo-guardian's own reconcile PR is never matched. | Specific enough to avoid false positives but broad enough to catch related work. Blank entries are rejected at load. |

### Notes

- **`paths` is intentionally broad.** Many tools accept multiple file
  locations. Listing alternates prevents repo-guardian from creating a
  duplicate when a team already has a workflow under a different name.
- **`search_terms`** prevents repo-guardian from opening a PR when
  someone is already working on the same thing. A term like `"add"`
  matches too many unrelated PRs; pick something specific to the rule.
  The search skips repo-guardian's own reconcile PR, so a term that
  happens to appear in your own `pr.title` will not make the rule
  suppress itself.

### Built-in vs. operator-defined rules

If you want this rule shipped to every operator out of the box, add a
corresponding constructor to `internal/policy/defaults.go` (alongside
`defaultCodeownersRule`, `defaultDependabotRule`, etc.) and append it
to the `BuiltinDefaults()` slice. The HCL block above is then no
longer required — the rule is part of the binary's defaults.

For all other cases (operator-specific rules, optional rules, rules
that depend on local conventions), keep the rule in HCL only.

---

## Step 3: Build and Test

Run the existing tests to make sure nothing is broken:

```bash
make check    # lint + tests with race detector
```

The engine iterates over `policy.PolicyConfig.FileRules` generically,
so a new rule entry does not require new test code unless the rule
has unusual behavior. The key things to verify:

1. The template file name matches the rule's `template` value (the
   template store fails to load if there is a mismatch).
2. `paths` entries are valid file paths (no leading `/`, no glob
   patterns — globs apply only to `ignore { paths = [...] }`).
3. `target` does not conflict with another rule's `target`.

If you want to exercise the new rule in isolation:

```bash
go test -v -race -run TestEnginePolicy ./internal/checker/...
```

---

## Step 4: Test with Dry-Run Mode

Before deploying, validate the new rule against real repositories using
dry-run mode. Set `DRY_RUN=true` on the Deployment (or via Helm
`policy.dryRun: true`). The service logs every action it would take
without creating branches or PRs:

```
INFO  dry run: would create PR  owner=myorg  repo=new-project  missing_files="[github_actions_ci]"
```

> **`DRY_RUN` env wins over HCL.** Env vars are applied last in
> `policy.Load`, so a Deployment-level `DRY_RUN=true` silently
> overrides anything in `guardian.hcl`. If the engine looks stuck in
> dry-run when you didn't expect it, check
> `kubectl get deploy ... -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="DRY_RUN")]}'`
> first.

This confirms the rule is detecting the right repositories and that
the template name resolves correctly.

---

## Step 5: Deploy

If you are overriding templates via the chart's `templates.files` map
(rather than using the compiled-in defaults), add the new template to
your Helm values:

```yaml
# values.yaml — passed to `helm install ... -f values.yaml`
templates:
  files:
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

If you are relying on embedded templates (the default), no values
override is needed — the template is compiled into the binary.

Build and deploy:

```bash
make build
docker build -t repo-guardian:latest .
# Push to your registry and roll out the new version, or
helm upgrade --install repo-guardian \
  oci://ghcr.io/donaldgifford/charts/repo-guardian \
  --version <chart-version> \
  -f values.yaml
```

---

## What Happens at Runtime

Once deployed, the new rule participates in every repository check:

1. The engine loads the merged policy (built-in defaults → HCL file →
   env overrides) on startup.
2. For the new rule, it checks whether any of the `paths` exist in the
   repo (and evaluates any assertions for `contains`/`exact` modes).
3. If the rule is actionable, it checks whether any *third-party* open
   PR's title or branch matches a `pr.search_terms` entry. If so, the
   rule is skipped for this run. repo-guardian's own reconcile PR is
   excluded from this search — it is handled by step 4 instead.
4. If the rule is still actionable, the engine adds the rendered
   template to the repo-guardian PR branch
   (`repo-guardian/add-missing-files`). One branch per repo;
   subsequent runs update the same branch idempotently.
5. The PR body lists every rule contributing to this PR. When all
   rules on the PR become satisfied on the default branch, the PR is
   auto-closed (toggle via `guardian.auto_close_pr` / `AUTO_CLOSE_PR`).

The `repo_guardian_files_missing_total{rule_name, org}` Prometheus
counter records detections; `repo_guardian_prs_created_total{org}`
records PR creation.

Dashboards are generated from the policy, so a new rule reaches them by
regenerating rather than by editing a panel:

```bash
repo-guardian monitoring generate --config guardian.hcl --out ./monitoring
```

Rule-keyed panels (compliance by rule, actionable repositories by rule)
pick the rule up automatically, because they aggregate by the
`rule_name` label. What regeneration adds is the per-rule row on E1 and
the alert gating — a rule the config does not declare gets no panel and
no alert at all, which is the point. See `contrib/README.md`.

---

## Summary

| Step | File / Surface | What to do |
|---|---|---|
| 1 | `internal/rules/templates/<name>.tmpl` (or Helm `templates.files`) | Create the default file content |
| 2 | `guardian.hcl` (or `internal/policy/defaults.go` for a built-in) | Declare the `rule "file" "<name>" { ... }` block |
| 3 | — | `make check` |
| 4 | — | Deploy with `DRY_RUN=true` and verify logs |
| 5 | — | Roll out via Helm |

No changes are needed in the checker engine, webhook handler,
scheduler, work queue, or any other package.

---

## Reconcilers

HCL rules can attach reconcilers — pluggable post-check actions that
run after file checks pass. For example, the `custom_properties`
reconciler reads `catalog-info.yaml` and syncs ownership metadata to
GitHub custom properties:

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

When `watch = true`, push events that modify the watched files on the
default branch trigger a re-check.

Four built-in reconcilers ship with the binary:

| Reconciler | Purpose |
|---|---|
| `custom_properties` | Sync Backstage `catalog-info.yaml` → GitHub repo custom properties (`api` or `github-action` mode) |
| `label_sync` | YAML-driven label create / update / rename / delete |
| `branch_protection` | YAML-driven branch protection ruleset management |
| `workflow_sync` | Lightweight observability for watched workflow files |

See `docs/design/0007-reconciler-interface-and-push-event-handler.md`
and `docs/design/0008-additional-rule-types-and-ignore-lists.md` for
the full design.

### Renovate File Rules

repo-guardian includes two built-in Renovate file rules that are
**disabled by default**. When enabled, they ensure every repository
has a standardized Renovate workflow and configuration extending the
org preset.

To enable them, add the following to your `guardian.hcl`:

```hcl
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

- **`renovate_workflow`** uses `check = "exact"` — the file must match
  the template byte-for-byte (YAML-semantic comparison). Any drift
  triggers a PR to restore the canonical workflow.
- **`renovate_config`** uses `check = "contains"` with an assertion —
  the file must exist and contain the org preset pattern. Teams can
  add additional Renovate configuration (labels, automerge rules) as
  long as the org preset reference is present.

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
