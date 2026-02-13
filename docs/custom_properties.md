# Feature Plan: Custom Properties from Backstage catalog-info.yaml

## Goal

Ensure every repository in the GitHub organization has four custom properties
set (`Owner`, `Component`, `JiraProject`, `JiraLabel`) by reading values from
each repo's Backstage `catalog-info.yaml` file. When properties are missing or
stale, repo-guardian creates a PR containing a GitHub Actions workflow that
sets them. This enables Wiz security scanning to tag repositories with
ownership and project metadata, ensuring repos are assigned to the correct Wiz
projects.

### Why This Matters

Wiz imports GitHub repository custom properties as tags. Without these
properties, repositories show up in Wiz untagged -- they cannot be attributed
to an owning team or mapped to a Wiz project. Setting four specific custom
properties on every repo closes this gap:

| Custom Property | Source in catalog-info.yaml | Wiz Usage |
|---|---|---|
| `Owner` | `spec.owner` | Identifies the owning team/individual |
| `Component` | `metadata.name` | Identifies the service/component name |
| `JiraProject` | `metadata.annotations["jira/project-key"]` | Maps to Jira project for issue routing |
| `JiraLabel` | `metadata.annotations["jira/label"]` | Additional Jira label for filtering |

### Source of Truth and Defaults

The `catalog-info.yaml` file (Backstage Component entity) is the preferred
source of truth. The engine reads this file first and extracts values using
struct-based YAML unmarshaling.

**When catalog-info.yaml is missing or cannot be parsed:**

- `Owner` = `Unclassified`
- `Component` = `Unclassified`
- `JiraProject` = `` (empty -- left unset)
- `JiraLabel` = `` (empty -- left unset)

This guarantees every repo gets at minimum an `Owner` and `Component` value.
Repos without a catalog-info.yaml are explicitly tagged as `Unclassified` so
they surface in Wiz as needing attention rather than silently going untagged.

Example `catalog-info.yaml` (from `examples/catalog-info.yaml`):

```yaml
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: repo-guardian
  title: Repo Guardian
  description: "Github App to automate repo onboarding and settings"
  annotations:
    jira/project-key: "DON"
    jira/label: "repo-guardian"
  tags:
    - go
    - github
  namespace: default
spec:
  lifecycle: production
  type: service
  owner: donaldgifford
  system: infrastructure
```

Extracted properties:

- `Owner` = `donaldgifford` (from `spec.owner`)
- `Component` = `repo-guardian` (from `metadata.name`)
- `JiraProject` = `DON` (from `metadata.annotations["jira/project-key"]`)
- `JiraLabel` = `repo-guardian` (from `metadata.annotations["jira/label"]`)

---

## Architecture Decision: PR with GHA Workflow, Not Direct API Write

Repo-guardian will **not** have write access to custom properties. The GitHub
App permission for custom properties will be **Read only** (or possibly not
granted at all). Instead of writing properties directly via the API, the app
creates a PR containing a GitHub Actions workflow that sets the properties when
merged.

This is the cleaner path because:

1. **Least-privilege** -- the app never needs `custom_properties: write`. The
   GHA workflow runs with the repo's `GITHUB_TOKEN` or an org-level PAT
   configured as a secret, which has the necessary permissions.
2. **Human review** -- property values go through the same PR review process as
   file changes. Teams see exactly what will be set before it takes effect.
3. **Auditability** -- the PR and workflow run create a clear audit trail in
   both GitHub and Wiz.
4. **Consistency** -- the approach mirrors how repo-guardian handles missing
   files: detect the gap, create a PR, let a human merge.

### How It Works

1. Engine reads the repo's `catalog-info.yaml` (if present) and extracts
   property values. If the file is missing or unparseable, defaults to
   `Unclassified` for Owner and Component.
2. Engine reads the repo's current custom properties via the GitHub API (read
   only).
3. Engine diffs desired vs current values. If all properties are already
   correct, no action.
4. If properties need updating, the engine creates a PR containing:
   - `.github/workflows/set-custom-properties.yml` -- a one-shot GHA workflow
     that uses `gh api` to PATCH the repo's custom properties.
   - The workflow is configured to run once on push to the PR branch (or on
     merge to default branch) and then self-deletes or is manually removed.

**Prerequisite:** The org-level custom property schema (`Owner`, `Component`,
`JiraProject`, `JiraLabel`) must be created by an org admin before enabling
this feature. Repo-guardian will never have permissions to modify the schema --
only to read property values.

---

## Implementation Plan

### Phase 1: GitHub Client Interface Extensions

**File: `internal/github/github.go`**

Add a `CustomPropertyValue` type and two new methods to the `Client` interface:

```go
// CustomPropertyValue represents a single custom property key-value pair.
type CustomPropertyValue struct {
    PropertyName string
    Value        string
}

// Add to Client interface:

// GetFileContent returns the decoded content of a file in a repository.
// Returns empty string and no error if the file does not exist.
GetFileContent(ctx context.Context, owner, repo, path string) (string, error)

// GetCustomPropertyValues returns all custom property values set on a repository.
GetCustomPropertyValues(ctx context.Context, owner, repo string) ([]*CustomPropertyValue, error)
```

No `SetCustomPropertyValues` method needed -- the app does not write properties
directly.

**File: `internal/github/client.go`**

Implement both methods:

- `GetFileContent` wraps `Repositories.GetContents()` from go-github and
  returns the decoded file content. Returns `("", nil)` if the file does not
  exist (404). This is distinct from the existing `GetContents()` method which
  only returns a boolean existence check.
- `GetCustomPropertyValues` wraps
  `Repositories.GetAllCustomPropertyValues()` from go-github v68
  (`github/repos_properties.go`). Maps go-github's `CustomPropertyValue`
  (which uses `interface{}` for Value) to our string-typed struct.

**GitHub App permission:** `custom_properties: read`. Read-only access to check
current values. No write access needed since the actual property update happens
via the GHA workflow in the PR.

### Phase 2: Backstage Catalog Parser (Struct Tags)

**File: `internal/catalog/catalog.go`** (new package)

Parse `catalog-info.yaml` using Go struct tags for type-safe YAML unmarshaling.
The Backstage entity format is well-defined, so struct-based parsing with tags
gives us validation, clear field mapping, and compile-time safety.

```go
package catalog

import "gopkg.in/yaml.v3"

// Entity represents a Backstage catalog entity. Only the fields
// relevant to custom property extraction are included.
type Entity struct {
    APIVersion string   `yaml:"apiVersion"`
    Kind       string   `yaml:"kind"`
    Metadata   Metadata `yaml:"metadata"`
    Spec       Spec     `yaml:"spec"`
}

// Metadata holds the metadata section of a Backstage entity.
type Metadata struct {
    Name        string            `yaml:"name"`
    Annotations map[string]string `yaml:"annotations"`
}

// Spec holds the spec section of a Backstage Component entity.
type Spec struct {
    Owner     string `yaml:"owner"`
    Lifecycle string `yaml:"lifecycle"`
    Type      string `yaml:"type"`
    System    string `yaml:"system"`
}

// Properties holds the extracted custom property values.
type Properties struct {
    Owner       string
    Component   string
    JiraProject string
    JiraLabel   string
}

const (
    DefaultOwner     = "Unclassified"
    DefaultComponent = "Unclassified"
)

// Parse unmarshals a catalog-info.yaml content string into an Entity
// and extracts custom property values. Returns default Properties
// (Owner and Component set to "Unclassified") if the content cannot
// be parsed or is not a Backstage Component entity.
func Parse(content string) *Properties {
    var entity Entity
    if err := yaml.Unmarshal([]byte(content), &entity); err != nil {
        return defaults()
    }

    if entity.APIVersion != "backstage.io/v1alpha1" || entity.Kind != "Component" {
        return defaults()
    }

    p := &Properties{
        Owner:       entity.Spec.Owner,
        Component:   entity.Metadata.Name,
        JiraProject: entity.Metadata.Annotations["jira/project-key"],
        JiraLabel:   entity.Metadata.Annotations["jira/label"],
    }

    // Apply defaults for empty required fields.
    if p.Owner == "" {
        p.Owner = DefaultOwner
    }
    if p.Component == "" {
        p.Component = DefaultComponent
    }

    return p
}

func defaults() *Properties {
    return &Properties{
        Owner:     DefaultOwner,
        Component: DefaultComponent,
    }
}
```

The struct tag approach means:

- YAML field mapping is explicit and self-documenting.
- Adding new fields in the future is a struct field + tag, not string
  manipulation.
- Invalid YAML fails at `yaml.Unmarshal`, giving a clear error.
- The `Annotations` map handles arbitrary annotation keys without needing a
  struct field per annotation.

**Dependency:** `gopkg.in/yaml.v3`. This is the standard Go YAML library.
Check `go.mod` -- if not already present, add it via `go get`.

**File: `internal/catalog/catalog_test.go`**

Table-driven tests:

| Test Case | Input | Expected |
|---|---|---|
| All fields present | Full catalog-info.yaml | All four properties populated |
| Missing Jira annotations | No `jira/` annotations | Owner + Component set, JiraProject + JiraLabel empty |
| Empty `spec.owner` | `owner: ""` | Owner = `Unclassified` |
| Empty `metadata.name` | `name: ""` | Component = `Unclassified` |
| Wrong `kind` (e.g., API) | `kind: API` | Defaults (Unclassified/Unclassified) |
| Wrong `apiVersion` | `apiVersion: v2` | Defaults |
| Malformed YAML | `{{{` | Defaults |
| Empty string | `""` | Defaults |
| Valid YAML, not Backstage | Random YAML doc | Defaults |

### Phase 3: GHA Workflow Template

**File: `internal/rules/templates/set-custom-properties.tmpl`**

A GitHub Actions workflow template that sets custom properties on the repo.
The template uses placeholder values that are replaced by the engine before
committing:

```yaml
# This workflow was created by repo-guardian to set repository custom
# properties. It runs once when merged, then can be safely deleted.
#
# Prerequisites:
#   - Org-level custom property schema must define: Owner, Component,
#     JiraProject, JiraLabel
#   - The GITHUB_TOKEN must have custom_properties:write permission,
#     or use a PAT/GitHub App token stored as a repository or org secret.
name: Set Custom Properties

on:
  push:
    branches:
      - main

permissions:
  contents: read

jobs:
  set-properties:
    runs-on: ubuntu-latest
    steps:
      - name: Set repository custom properties
        env:
          GH_TOKEN: ${{ secrets.CUSTOM_PROPERTIES_TOKEN }}
        run: |
          gh api \
            --method PATCH \
            "repos/${{ github.repository }}/properties/values" \
            -f 'properties[][property_name]=Owner' \
            -f 'properties[][value]=OWNER_VALUE' \
            -f 'properties[][property_name]=Component' \
            -f 'properties[][value]=COMPONENT_VALUE' \
            -f 'properties[][property_name]=JiraProject' \
            -f 'properties[][value]=JIRA_PROJECT_VALUE' \
            -f 'properties[][property_name]=JiraLabel' \
            -f 'properties[][value]=JIRA_LABEL_VALUE'
```

The engine replaces `OWNER_VALUE`, `COMPONENT_VALUE`, `JIRA_PROJECT_VALUE`,
and `JIRA_LABEL_VALUE` with actual values before committing. Empty values
(JiraProject, JiraLabel when not available) are omitted from the API call --
the template is rendered dynamically, not used as-is.

**Note:** The exact `gh api` invocation and token setup (org secret vs repo
secret vs GitHub App token) will depend on the org's auth strategy. The
template provides a working starting point that the team customizes during PR
review. The `CUSTOM_PROPERTIES_TOKEN` secret name is a placeholder.

### Phase 4: Properties Checker in Engine

**File: `internal/checker/properties.go`** (new file in existing package)

```go
// CheckCustomProperties reads the repo's catalog-info.yaml, extracts
// desired custom property values, compares them against current values,
// and creates a PR with a GHA workflow to set properties if they differ.
func (e *Engine) CheckCustomProperties(
    ctx context.Context,
    client ghclient.Client,
    owner, repo, defaultBranch string,
    openPRs []*ghclient.PullRequest,
) error
```

Flow:

1. **Check for existing properties PR** -- search `openPRs` for a PR with
   branch `repo-guardian/set-custom-properties` or title containing "custom
   properties". If found, skip (already in progress).
2. **Try to read `catalog-info.yaml`** -- call `client.GetFileContent()` for
   `catalog-info.yaml` then `catalog-info.yml`. If neither exists, use empty
   string (which causes `catalog.Parse` to return defaults).
3. **Parse** using `catalog.Parse(content)`. This always returns a non-nil
   `*Properties` -- either extracted values or defaults.
4. **Read current custom properties** via
   `client.GetCustomPropertyValues()`. Build a map of current values.
5. **Diff** desired vs current. Build list of properties that need updating.
   Compare `Owner`, `Component` always. Compare `JiraProject`, `JiraLabel`
   only if the desired value is non-empty (don't overwrite existing values
   with empty).
6. If no changes needed, log and return.
7. If dry-run, log what would be set and return.
8. **Render the GHA workflow** with actual property values substituted.
9. **Create PR** on branch `repo-guardian/set-custom-properties` with the
   rendered workflow file at `.github/workflows/set-custom-properties.yml`.
   Use a dedicated PR title (e.g., `chore: set repository custom properties`)
   and body explaining the property values and their source.
10. Increment metrics.

**Branch naming:** Use a separate branch from file rules:
`repo-guardian/set-custom-properties`. This keeps properties PRs independent
from missing-file PRs, since they serve different purposes and may be reviewed
by different people.

### Phase 5: Engine Integration

**File: `internal/checker/engine.go`**

Add `enableCustomProperties bool` field to the `Engine` struct and update
`NewEngine` to accept it.

Modify `CheckRepo` to call `CheckCustomProperties` after the file rule loop.
Pass the already-fetched `openPRs`, `repoInfo.DefaultRef`, etc. to avoid
duplicate API calls:

```go
func (e *Engine) CheckRepo(ctx context.Context, client ghclient.Client, owner, repo string) error {
    // ... existing skip checks (archived, fork, empty) ...

    openPRs, err := client.ListOpenPullRequests(ctx, owner, repo)
    // ... existing file rule loop using openPRs ...
    // ... existing PR creation for missing files ...

    // Check and set custom properties.
    if e.enableCustomProperties {
        if err := e.CheckCustomProperties(ctx, client, owner, repo, repoInfo.DefaultRef, openPRs); err != nil {
            log.Error("custom properties check failed", "error", err)
            // Non-fatal: log and continue. File checks already succeeded.
        }
    }

    return nil
}
```

Custom properties errors are logged but do not fail the overall check. A
missing or unparseable catalog-info.yaml should not prevent file rule PRs from
being created.

### Phase 6: Configuration

**File: `internal/config/config.go`**

Add a new config option:

```go
EnableCustomProperties bool // Enable custom properties check (default: false)
```

Environment variable: `ENABLE_CUSTOM_PROPERTIES` (default: `false`).

Default is `false` so the feature is opt-in. Existing deployments are not
affected until the operator explicitly enables it.

**File: `cmd/repo-guardian/main.go`**

Pass `cfg.EnableCustomProperties` to `NewEngine`.

### Phase 7: Metrics

**File: `internal/metrics/metrics.go`**

```go
// PropertiesCheckedTotal counts repos where custom properties were evaluated.
PropertiesCheckedTotal = promauto.NewCounter(prometheus.CounterOpts{
    Name: "repo_guardian_properties_checked_total",
    Help: "Total repositories where custom properties were evaluated.",
})

// PropertiesPRsCreatedTotal counts PRs created to set custom properties.
PropertiesPRsCreatedTotal = promauto.NewCounter(prometheus.CounterOpts{
    Name: "repo_guardian_properties_prs_created_total",
    Help: "Total pull requests created to set custom properties.",
})

// PropertiesAlreadyCorrectTotal counts repos where properties already matched.
PropertiesAlreadyCorrectTotal = promauto.NewCounter(prometheus.CounterOpts{
    Name: "repo_guardian_properties_already_correct_total",
    Help: "Total repositories where custom properties already matched desired values.",
})
```

### Phase 8: Tests

**File: `internal/checker/engine_test.go`**

Update `mockClient` with:

- `GetFileContent(ctx, owner, repo, path) (string, error)` -- returns
  configurable content per path.
- `GetCustomPropertyValues(ctx, owner, repo) ([]*CustomPropertyValue, error)` --
  returns configurable current properties.

Add test cases:

| Test | Scenario | Expected |
|---|---|---|
| `TestCheckCustomProperties_SetsFromCatalogInfo` | catalog-info.yaml present with all four fields, current properties differ | PR created with workflow containing correct values |
| `TestCheckCustomProperties_NoCatalogFile` | No catalog-info.yaml | PR created with Unclassified defaults for Owner/Component |
| `TestCheckCustomProperties_UnparseableFile` | catalog-info.yaml contains invalid YAML | PR created with Unclassified defaults |
| `TestCheckCustomProperties_NotBackstageComponent` | Valid YAML but `kind: API` | PR created with Unclassified defaults |
| `TestCheckCustomProperties_AlreadyCorrect` | Current properties match desired | No PR created |
| `TestCheckCustomProperties_PartialAnnotations` | catalog-info.yaml missing Jira annotations | PR sets Owner + Component, omits Jira fields |
| `TestCheckCustomProperties_ExistingPR` | Open PR for properties already exists | Skipped |
| `TestCheckCustomProperties_DryRun` | Dry-run enabled | Logged but no PR created |
| `TestCheckCustomProperties_Disabled` | `enableCustomProperties=false` | Not called |

**File: `internal/catalog/catalog_test.go`**

As described in Phase 2.

### Phase 9: Documentation Updates

**File: `docs/SUMMARY.md`**

Add a section under "What It Brings to the Organization > Security and
Compliance" covering:

- Custom properties sync for Wiz integration -- repos are tagged with
  ownership metadata so Wiz can assign them to the correct projects.
- Properties sourced from Backstage catalog-info.yaml where available;
  defaults to `Unclassified` otherwise, ensuring no repo goes untagged.
- Delivered via PR with a GHA workflow, maintaining the human-review-first
  approach.

**File: `docs/ONE_PAGER.md`**

Add a bullet under "Key Benefits > For security and compliance" about the Wiz
integration and custom properties tagging.

**File: `docs/RFC.md`**

Add a section documenting the custom properties feature, Backstage integration,
the GHA workflow approach, and the Wiz tagging use case.

---

## File Change Summary

| File | Change | Phase |
|---|---|---|
| `internal/github/github.go` | Add `CustomPropertyValue` type, `GetFileContent`, `GetCustomPropertyValues` to interface | 1 |
| `internal/github/client.go` | Implement two new methods using go-github v68 | 1 |
| `internal/github/client_test.go` | Add httptest cases for new methods | 1 |
| `internal/catalog/catalog.go` | New package: struct-tag-based YAML parser for catalog-info.yaml | 2 |
| `internal/catalog/catalog_test.go` | Table-driven tests for parser | 2 |
| `internal/rules/templates/set-custom-properties.tmpl` | GHA workflow template for setting properties | 3 |
| `internal/checker/properties.go` | New file: `CheckCustomProperties` logic with diff + PR creation | 4 |
| `internal/checker/engine.go` | Add `enableCustomProperties` field, call `CheckCustomProperties` in `CheckRepo` | 5 |
| `internal/config/config.go` | Add `EnableCustomProperties` config option | 6 |
| `cmd/repo-guardian/main.go` | Pass `EnableCustomProperties` to `NewEngine` | 6 |
| `internal/metrics/metrics.go` | Add 3 new properties metrics | 7 |
| `internal/checker/engine_test.go` | Update mockClient, add 9 properties test cases | 8 |
| `internal/catalog/catalog_test.go` | Table-driven parser tests (9 cases) | 8 |
| `docs/SUMMARY.md` | Add Wiz/custom properties section | 9 |
| `docs/ONE_PAGER.md` | Add Wiz/custom properties bullet | 9 |
| `docs/RFC.md` | Add custom properties appendix | 9 |
| `contrib/grafana/repo-guardian-dashboard.json` | Add properties panels | 9 |
| `contrib/prometheus/alerts.yaml` | Add properties alert (optional) | 9 |

---

## GitHub App Permission Change

| Permission | Access | Reason |
|---|---|---|
| **Contents** | Read & Write | Existing -- read files, create branches, commit workflow |
| **Pull Requests** | Read & Write | Existing -- check/create PRs |
| **Metadata** | Read | Existing -- required for all apps |
| **Custom properties** | Read | **New** -- read current property values to diff against desired |

The app only needs read access to custom properties. The actual write happens
via the GHA workflow using a token with write permissions (configured as an org
or repo secret).

---

## Catalog-Info.yaml Paths to Check

The engine looks for the catalog-info file at these paths (in order):

1. `catalog-info.yaml`
2. `catalog-info.yml`

Most Backstage setups use `catalog-info.yaml` at the repo root. The `.yml`
variant is included for compatibility. If neither exists, the engine proceeds
with default values (`Unclassified`).

---

## Edge Cases and Decisions

### What if catalog-info.yaml is missing?

Proceed with defaults: `Owner=Unclassified`, `Component=Unclassified`,
`JiraProject=""`, `JiraLabel=""`. Create a PR to set Owner and Component to
`Unclassified` if they aren't already. This ensures every repo has at minimum
an ownership tag in Wiz, even if it's a placeholder that signals "this repo
needs attention."

### What if catalog-info.yaml cannot be parsed?

Same as missing: use defaults. The YAML might be malformed or the file might
not be a Backstage entity (wrong `apiVersion` or `kind`). In all cases, fall
back to `Unclassified` defaults. The `catalog.Parse()` function always returns
a non-nil `*Properties`.

### What if JiraProject or JiraLabel annotations are missing?

Leave them empty. Only `Owner` and `Component` get the `Unclassified` default.
Jira-related properties are left unset (not included in the GHA workflow API
call) since the correct default values are not yet determined.

### What if the org hasn't defined the custom property schema?

The GHA workflow will fail when it runs because the GitHub API returns 422 for
undefined properties. This is an operator prerequisite: the org admin must
create the property schema before enabling this feature. Document this in the
deployment guide and the PR body.

### What if current properties already match?

No PR is created. The diff step compares desired values against current values
read via the API. If all properties already match, log it and move on.
Increment `PropertiesAlreadyCorrectTotal` metric.

### What about the GHA workflow file after merge?

The workflow runs on push to `main` (when the PR is merged). After it runs
successfully, it remains in the repo as a dormant file (it only triggers on
push to main, and future pushes won't re-set properties unless the file
changes). Teams can delete it after verifying properties are set, or it can be
left in place as a record. A follow-up enhancement could have the workflow
self-delete via a step that removes itself and commits.

### Idempotency

Fully idempotent. The engine diffs current vs desired properties on every run.
If properties match, no PR. If a PR is already open, no duplicate. If the
workflow has already run and properties are set, subsequent runs detect the
match and skip.

---

## Rollout Plan

1. **Merge code with `ENABLE_CUSTOM_PROPERTIES=false` (default).** No
   behavioral change to existing deployments.
2. **Define custom property schema in GitHub org settings.** Create the four
   properties: `Owner` (string), `Component` (string), `JiraProject` (string),
   `JiraLabel` (string). This is a one-time org admin action.
3. **Configure a token secret.** Create an org-level secret
   (`CUSTOM_PROPERTIES_TOKEN`) containing a PAT or GitHub App token with
   `custom_properties: write` permission. This is used by the GHA workflow in
   the PRs.
4. **Update GitHub App permissions.** Add `custom_properties: read` to the
   repo-guardian app. Org admins approve the permission request.
5. **Deploy with `ENABLE_CUSTOM_PROPERTIES=true` and `DRY_RUN=true`.** Observe
   logs to confirm correct property extraction and diff logic.
6. **Disable dry-run.** Properties PRs are now being created. Monitor
   `PropertiesPRsCreatedTotal` metric.
7. **Review and merge initial PRs.** Verify that the GHA workflow runs
   successfully and properties appear in GitHub.
8. **Verify in Wiz.** Confirm that scanned repos now have the expected tags
   derived from custom properties.
