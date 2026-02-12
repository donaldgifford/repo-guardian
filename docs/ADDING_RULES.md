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
