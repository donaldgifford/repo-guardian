# Feature Plan: Custom Properties from Backstage catalog-info.yaml

## Goal

Ensure every repository in the GitHub organization has four custom properties
set (`Owner`, `Component`, `JiraProject`, `JiraLabel`) by reading values from
each repo's Backstage `catalog-info.yaml` file. This enables Wiz security
scanning to tag repositories with ownership and project metadata, ensuring
repos are assigned to the correct Wiz projects.

The feature supports two operational modes, configured via
`CUSTOM_PROPERTIES_MODE`:

| Mode | Default | App Permission | How Properties Are Set |
|---|---|---|---|
| `github-action` | Yes | `custom_properties: read` | PR with a GHA workflow that sets properties on merge |
| `api` | No | `custom_properties: read & write` | Direct API write; PR to add catalog-info.yaml if missing |

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

## Architecture: Two Operational Modes

The feature is gated behind `CUSTOM_PROPERTIES_MODE`. When unset or set to an
empty string, the feature is disabled entirely. When set to `github-action` or
`api`, the engine runs custom property checks after file rule checks.

### Mode: `github-action` (default / recommended)

The app has read-only access to custom properties. When properties need
updating, it creates a PR containing a GitHub Actions workflow that sets them
on merge.

```
catalog-info.yaml exists?
├── Yes → Parse, extract Owner/Component/Jira values
└── No  → Use defaults (Unclassified/Unclassified)
              ↓
Read current custom properties via API (read-only)
              ↓
Diff desired vs current
├── Match    → No action
└── Mismatch → Create PR with .github/workflows/set-custom-properties.yml
                 ↓
               Human reviews PR → Merges → GHA workflow runs → Properties set
```

**Why this is the default:**

1. **Least-privilege** -- the app never needs `custom_properties: write`.
2. **Human review** -- property values go through the PR review process.
3. **Auditability** -- the PR and workflow run create a clear audit trail.
4. **Standard token** -- the GHA workflow uses the built-in `GITHUB_TOKEN`,
   no custom org secrets required. The app itself only reads.

### Mode: `api`

The app has read/write access to custom properties. When catalog-info.yaml
exists, it sets properties directly via the API. When catalog-info.yaml is
missing, it sets `Unclassified` defaults via the API AND creates a PR asking
the repo owners to add a catalog-info.yaml file.

```
catalog-info.yaml exists?
├── Yes → Parse, extract Owner/Component/Jira values
│           ↓
│         Diff desired vs current properties
│         ├── Match    → No action
│         └── Mismatch → Set properties directly via API
│
└── No  → Set Owner=Unclassified, Component=Unclassified via API
            ↓
          Create PR with default catalog-info.yaml template
            ↓
          Human reviews PR → Merges → Next reconciliation extracts real values
```

**When to use `api` mode:**

- The org is comfortable granting `custom_properties: write` to the app.
- Faster feedback loop is needed -- properties are set immediately, no PR
  merge required.
- The org wants to actively drive catalog-info.yaml adoption by creating PRs
  that prompt repo owners to add the file.

---

## Implementation Plan

### Phase 1: GitHub Client Interface Extensions

**File: `internal/github/github.go`**

Add a `CustomPropertyValue` type and new methods to the `Client` interface:

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

// SetCustomPropertyValues creates or updates custom property values on a repository.
// Used only in "api" mode.
SetCustomPropertyValues(ctx context.Context, owner, repo string, properties []*CustomPropertyValue) error
```

**File: `internal/github/client.go`**

Implement all three methods:

- `GetFileContent` wraps `Repositories.GetContents()` from go-github and
  returns the decoded file content. Returns `("", nil)` if the file does not
  exist (404). This is distinct from the existing `GetContents()` method which
  only returns a boolean existence check.
- `GetCustomPropertyValues` wraps
  `Repositories.GetAllCustomPropertyValues()` from go-github v68
  (`github/repos_properties.go`). Maps go-github's `CustomPropertyValue`
  (which uses `interface{}` for Value) to our string-typed struct.
- `SetCustomPropertyValues` wraps
  `Repositories.CreateOrUpdateCustomProperties()` from go-github v68. Only
  called in `api` mode.

**GitHub App permission depends on mode:**

| Mode | Permission |
|---|---|
| `github-action` | `custom_properties: read` |
| `api` | `custom_properties: read & write` |

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

### Phase 3: Templates

Two templates are needed: one for the GHA workflow (`github-action` mode) and
one for a default catalog-info.yaml (`api` mode when the file is missing).

**File: `internal/rules/templates/set-custom-properties.tmpl`**

Used in `github-action` mode. A GitHub Actions workflow that sets custom
properties on the repo. The engine renders this with actual property values
substituted before committing:

```yaml
# This workflow was created by repo-guardian to set repository custom
# properties. It runs once when merged, then can be safely deleted.
#
# Prerequisites:
#   - Org-level custom property schema must define: Owner, Component,
#     JiraProject, JiraLabel
#   - GITHUB_TOKEN must have custom_properties:write permission
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
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
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
and `JIRA_LABEL_VALUE` with actual values. Empty values (JiraProject,
JiraLabel when not available) are omitted from the API call -- the template is
rendered dynamically, not used as-is.

**File: `internal/rules/templates/catalog-info.tmpl`**

Used in `api` mode when catalog-info.yaml is missing. A default Backstage
Component entity that prompts repo owners to fill in their details:

```yaml
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: REPO_NAME
  description: "TODO: Add a description of this service"
  annotations:
    backstage.io/source-location: url:https://github.com/ORG_NAME/REPO_NAME
    jira/project-key: "TODO"
    jira/label: "REPO_NAME"
  tags: []
spec:
  lifecycle: production
  type: service
  owner: "TODO: Add the owning team or individual"
  system: "TODO: Add the system this belongs to"
```

The engine replaces `REPO_NAME` and `ORG_NAME` before committing. The `TODO`
placeholders make it obvious what the repo owner needs to fill in.

### Phase 4: Properties Checker in Engine

**File: `internal/checker/properties.go`** (new file in existing package)

The checker implements both modes behind a common interface. The mode is stored
on the `Engine` struct and determines which code path runs.

```go
// CheckCustomProperties reads the repo's catalog-info.yaml, extracts
// desired custom property values, and either creates a PR (github-action
// mode) or sets them directly via API (api mode).
func (e *Engine) CheckCustomProperties(
    ctx context.Context,
    client ghclient.Client,
    owner, repo, defaultBranch string,
    openPRs []*ghclient.PullRequest,
) error
```

#### Common steps (both modes):

1. **Check for existing properties PR** -- search `openPRs` for a PR with
   branch `repo-guardian/set-custom-properties` or title containing "custom
   properties". If found, skip.
2. **Try to read `catalog-info.yaml`** -- call `client.GetFileContent()` for
   `catalog-info.yaml` then `catalog-info.yml`. Track whether the file was
   found.
3. **Parse** using `catalog.Parse(content)`. If content is empty (file not
   found), returns defaults (`Unclassified`).
4. **Read current custom properties** via
   `client.GetCustomPropertyValues()`. Build a map of current values.
5. **Diff** desired vs current. Compare `Owner`, `Component` always. Compare
   `JiraProject`, `JiraLabel` only if the desired value is non-empty.

#### `github-action` mode flow (after common steps):

6. If no changes needed, log and return.
7. If dry-run, log and return.
8. Render the GHA workflow template with actual property values.
9. Create PR on branch `repo-guardian/set-custom-properties` with the
   workflow at `.github/workflows/set-custom-properties.yml`.
10. Increment `PropertiesPRsCreatedTotal`.

#### `api` mode flow (after common steps):

6. **If catalog-info.yaml was found:**
   - If no property changes needed, log and return.
   - If dry-run, log and return.
   - Call `client.SetCustomPropertyValues()` with the desired values.
   - Increment `PropertiesSetTotal`.
7. **If catalog-info.yaml was NOT found:**
   - Set `Owner=Unclassified`, `Component=Unclassified` via
     `client.SetCustomPropertyValues()` (unless already set).
   - Check for existing catalog-info PR (search `openPRs` for
     `repo-guardian/add-catalog-info`). If found, skip.
   - If dry-run, log and return.
   - Render the catalog-info.yaml template with `REPO_NAME` and `ORG_NAME`.
   - Create PR on branch `repo-guardian/add-catalog-info` with the file at
     `catalog-info.yaml`.
   - Increment `PropertiesPRsCreatedTotal`.

**Branch naming:**

| Mode | Branch |
|---|---|
| `github-action` | `repo-guardian/set-custom-properties` |
| `api` (catalog-info PR) | `repo-guardian/add-catalog-info` |

Both are separate from the file-rules branch (`repo-guardian/add-missing-files`).

### Phase 5: Engine Integration

**File: `internal/checker/engine.go`**

Replace the `enableCustomProperties bool` field with
`customPropertiesMode string` on the `Engine` struct. Valid values: `""`,
`"github-action"`, `"api"`.

```go
type Engine struct {
    registry             *rules.Registry
    templates            *rules.TemplateStore
    logger               *slog.Logger
    skipForks            bool
    skipArchived         bool
    dryRun               bool
    customPropertiesMode string // "", "github-action", or "api"
}
```

Update `NewEngine` to accept the mode string.

Modify `CheckRepo` to call `CheckCustomProperties` when mode is non-empty:

```go
func (e *Engine) CheckRepo(ctx context.Context, client ghclient.Client, owner, repo string) error {
    // ... existing skip checks (archived, fork, empty) ...

    openPRs, err := client.ListOpenPullRequests(ctx, owner, repo)
    // ... existing file rule loop using openPRs ...
    // ... existing PR creation for missing files ...

    // Custom properties check.
    if e.customPropertiesMode != "" {
        if err := e.CheckCustomProperties(ctx, client, owner, repo, repoInfo.DefaultRef, openPRs); err != nil {
            log.Error("custom properties check failed", "error", err)
            // Non-fatal: log and continue.
        }
    }

    return nil
}
```

### Phase 6: Configuration

**File: `internal/config/config.go`**

```go
// CustomPropertiesMode controls how custom properties are managed.
// Valid values: "" (disabled), "github-action" (PR with GHA workflow),
// "api" (direct API write).
CustomPropertiesMode string
```

Environment variable: `CUSTOM_PROPERTIES_MODE`

| Value | Behavior |
|---|---|
| `` (empty/unset) | Feature disabled. No custom properties checks. |
| `github-action` | Read-only. Creates PRs with GHA workflows to set properties. |
| `api` | Read/write. Sets properties directly. PRs catalog-info.yaml if missing. |

Validation: reject any value other than `""`, `"github-action"`, or `"api"`.

**File: `cmd/repo-guardian/main.go`**

Pass `cfg.CustomPropertiesMode` to `NewEngine`.

### Phase 7: Metrics

**File: `internal/metrics/metrics.go`**

```go
// PropertiesCheckedTotal counts repos where custom properties were evaluated.
PropertiesCheckedTotal = promauto.NewCounter(prometheus.CounterOpts{
    Name: "repo_guardian_properties_checked_total",
    Help: "Total repositories where custom properties were evaluated.",
})

// PropertiesPRsCreatedTotal counts PRs created for custom properties
// (GHA workflow PRs in github-action mode, catalog-info PRs in api mode).
PropertiesPRsCreatedTotal = promauto.NewCounter(prometheus.CounterOpts{
    Name: "repo_guardian_properties_prs_created_total",
    Help: "Total pull requests created for custom properties.",
})

// PropertiesSetTotal counts repos where properties were set via API (api mode only).
PropertiesSetTotal = promauto.NewCounter(prometheus.CounterOpts{
    Name: "repo_guardian_properties_set_total",
    Help: "Total repositories where custom properties were set via API.",
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
- `SetCustomPropertyValues(ctx, owner, repo, properties) error` -- records
  what was set (api mode tests only).

Test cases for `github-action` mode:

| Test | Scenario | Expected |
|---|---|---|
| `TestGHAMode_SetsFromCatalogInfo` | catalog-info.yaml present, properties differ | PR with GHA workflow containing correct values |
| `TestGHAMode_NoCatalogFile` | No catalog-info.yaml | PR with GHA workflow using Unclassified defaults |
| `TestGHAMode_UnparseableFile` | Invalid YAML | PR with Unclassified defaults |
| `TestGHAMode_NotBackstageComponent` | `kind: API` | PR with Unclassified defaults |
| `TestGHAMode_AlreadyCorrect` | Properties match | No PR created |
| `TestGHAMode_PartialAnnotations` | Missing Jira annotations | PR sets Owner + Component only |
| `TestGHAMode_ExistingPR` | PR already open | Skipped |
| `TestGHAMode_DryRun` | Dry-run enabled | Logged, no PR |

Test cases for `api` mode:

| Test | Scenario | Expected |
|---|---|---|
| `TestAPIMode_SetsFromCatalogInfo` | catalog-info.yaml present, properties differ | Properties set via API directly |
| `TestAPIMode_NoCatalogFile` | No catalog-info.yaml | Unclassified set via API + PR with catalog-info.yaml template |
| `TestAPIMode_AlreadyCorrect` | Properties match | No API call, no PR |
| `TestAPIMode_NoCatalog_ExistingPR` | No file, but catalog-info PR open | API sets defaults, no duplicate PR |
| `TestAPIMode_DryRun` | Dry-run enabled | Logged, no API call, no PR |

Test cases for disabled mode:

| Test | Scenario | Expected |
|---|---|---|
| `TestCustomProperties_Disabled` | `customPropertiesMode=""` | Not called |

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
- Two modes: `github-action` (PR-based, least-privilege) and `api`
  (direct write, drives catalog-info.yaml adoption).

**File: `docs/ONE_PAGER.md`**

Add a bullet under "Key Benefits > For security and compliance" about the Wiz
integration and custom properties tagging.

**File: `docs/RFC.md`**

Add a section documenting the custom properties feature, Backstage integration,
the two modes, and the Wiz tagging use case.

---

## File Change Summary

| File | Change | Phase |
|---|---|---|
| `internal/github/github.go` | Add `CustomPropertyValue` type, `GetFileContent`, `GetCustomPropertyValues`, `SetCustomPropertyValues` to interface | 1 |
| `internal/github/client.go` | Implement three new methods using go-github v68 | 1 |
| `internal/github/client_test.go` | Add httptest cases for new methods | 1 |
| `internal/catalog/catalog.go` | New package: struct-tag-based YAML parser for catalog-info.yaml | 2 |
| `internal/catalog/catalog_test.go` | Table-driven tests for parser (9 cases) | 2 |
| `internal/rules/templates/set-custom-properties.tmpl` | GHA workflow template for setting properties | 3 |
| `internal/rules/templates/catalog-info.tmpl` | Default Backstage catalog-info.yaml template | 3 |
| `internal/checker/properties.go` | New file: `CheckCustomProperties` with both mode implementations | 4 |
| `internal/checker/engine.go` | Add `customPropertiesMode` field, call `CheckCustomProperties` in `CheckRepo` | 5 |
| `internal/config/config.go` | Add `CustomPropertiesMode` config option with validation | 6 |
| `cmd/repo-guardian/main.go` | Pass `CustomPropertiesMode` to `NewEngine` | 6 |
| `internal/metrics/metrics.go` | Add 4 new properties metrics | 7 |
| `internal/checker/engine_test.go` | Update mockClient, add 14 properties test cases | 8 |
| `docs/SUMMARY.md` | Add Wiz/custom properties section | 9 |
| `docs/ONE_PAGER.md` | Add Wiz/custom properties bullet | 9 |
| `docs/RFC.md` | Add custom properties appendix | 9 |
| `contrib/grafana/repo-guardian-dashboard.json` | Add properties panels | 9 |
| `contrib/prometheus/alerts.yaml` | Add properties alert (optional) | 9 |

---

## GitHub App Permission by Mode

### `github-action` mode (default)

| Permission | Access | Reason |
|---|---|---|
| **Contents** | Read & Write | Existing -- read files, create branches, commit workflow |
| **Pull Requests** | Read & Write | Existing -- check/create PRs |
| **Metadata** | Read | Existing -- required for all apps |
| **Custom properties** | Read | **New** -- read current values to diff |

The app only reads custom properties. The GHA workflow in the PR uses the
standard `GITHUB_TOKEN` provided by GitHub Actions.

### `api` mode

| Permission | Access | Reason |
|---|---|---|
| **Contents** | Read & Write | Existing + commit catalog-info.yaml template |
| **Pull Requests** | Read & Write | Existing + create catalog-info PRs |
| **Metadata** | Read | Existing |
| **Custom properties** | Read & Write | **New** -- read current values + set new values directly |

---

## Catalog-Info.yaml Paths to Check

The engine looks for the catalog-info file at these paths (in order):

1. `catalog-info.yaml`
2. `catalog-info.yml`

Most Backstage setups use `catalog-info.yaml` at the repo root. The `.yml`
variant is included for compatibility. If neither exists, behavior depends on
mode.

---

## Edge Cases and Decisions

### What if catalog-info.yaml is missing?

**Both modes:** Use defaults: `Owner=Unclassified`,
`Component=Unclassified`, `JiraProject=""`, `JiraLabel=""`.

**`github-action` mode:** Create a PR with a GHA workflow that sets Owner and
Component to `Unclassified`. On next reconciliation after the catalog-info.yaml
is eventually added by the team, a new PR is created with the real values.

**`api` mode:** Set `Unclassified` defaults directly via API, then create a
separate PR with a default `catalog-info.yaml` template prompting the repo
owners to fill in their details. On next reconciliation after they merge the
catalog-info PR, the engine reads the real values and updates properties via
API.

### What if catalog-info.yaml cannot be parsed?

Same as missing in both modes. The YAML might be malformed or the file might
not be a Backstage entity (wrong `apiVersion` or `kind`). `catalog.Parse()`
always returns a non-nil `*Properties` with defaults.

### What if JiraProject or JiraLabel annotations are missing?

Leave them empty. Only `Owner` and `Component` get the `Unclassified` default.
Jira-related properties are left unset since the correct default values are not
yet determined.

### What if the org hasn't defined the custom property schema?

**Prerequisite for both modes.** The org admin must create the property schema
(`Owner`, `Component`, `JiraProject`, `JiraLabel`) in the GitHub org settings
before enabling this feature.

- **`github-action` mode:** The GHA workflow will fail with a 422 from the API.
- **`api` mode:** The `SetCustomPropertyValues` call will return a 422. The
  engine logs the error and continues (non-fatal).

### What if current properties already match?

No action in either mode. The diff step compares desired values against current
values. Increment `PropertiesAlreadyCorrectTotal` metric.

### What about the GHA workflow file after merge? (`github-action` mode)

The workflow runs on push to `main` (when the PR is merged). After running, it
remains as a dormant file. Teams can delete it, or a follow-up enhancement
could add a self-delete step. On subsequent reconciliation, if properties still
match, no new PR is created.

### What about the catalog-info.yaml PR? (`api` mode)

The PR contains a template with `TODO` placeholders. After the team fills in
their details and merges, the next reconciliation picks up the real values and
updates properties via API. If the team closes the PR without merging, the
engine will re-create it on the next cycle (stale branch detection handles
cleanup).

### Idempotency

Fully idempotent in both modes. The engine diffs current vs desired properties
on every run. If properties match, no action. If a PR is already open, no
duplicate. Multiple runs produce the same result.

---

## Rollout Plan

### For `github-action` mode (recommended starting point)

1. **Merge code with `CUSTOM_PROPERTIES_MODE` unset.** No behavioral change.
2. **Define custom property schema in GitHub org settings.** Create `Owner`
   (string), `Component` (string), `JiraProject` (string), `JiraLabel`
   (string). One-time org admin action.
3. **Verify `GITHUB_TOKEN` permissions.** The GHA workflow uses the standard
   `GITHUB_TOKEN`. Ensure the org's default token permissions include
   `custom_properties: write`, or configure this at the repo/workflow level.
4. **Update GitHub App permissions.** Add `custom_properties: read`.
5. **Deploy with `CUSTOM_PROPERTIES_MODE=github-action` and `DRY_RUN=true`.**
   Observe logs.
6. **Disable dry-run.** Monitor `PropertiesPRsCreatedTotal`.
7. **Review and merge initial PRs.** Verify workflows run and properties appear.
8. **Verify in Wiz.**

### For `api` mode

1. Steps 1-2 same as above.
2. **Update GitHub App permissions.** Add `custom_properties: read & write`.
3. **Deploy with `CUSTOM_PROPERTIES_MODE=api` and `DRY_RUN=true`.** Observe.
4. **Disable dry-run.** Monitor `PropertiesSetTotal` and
   `PropertiesPRsCreatedTotal` (catalog-info PRs).
5. **Verify in Wiz.**

### Switching modes

Switching from `github-action` to `api` (or vice versa) is safe. The engine
diffs properties on every run regardless of mode. Any already-correct
properties are skipped. Open PRs from the previous mode can be closed manually
or will be ignored (different branch names).
