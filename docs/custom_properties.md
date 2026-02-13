# Feature Plan: Custom Properties from Backstage catalog-info.yaml

## Goal

Automatically set GitHub repository custom properties (`Owner`, `Component`,
`JiraProject`, `JiraLabel`) by reading values from each repo's Backstage
`catalog-info.yaml` file. This enables Wiz security scanning to tag
repositories with ownership and project metadata, ensuring repos are assigned
to the correct Wiz projects.

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

### Source of Truth

The `catalog-info.yaml` file (Backstage Component entity) is the source of
truth. The engine should prioritize reading this file. If the file does not
exist, the custom properties check is skipped for that repo (no PR is created
for properties -- this is a direct API write, not a file-based rule).

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

## Architecture Decision: Separate Concern from FileRules

Custom properties are fundamentally different from file rules:

- **File rules** detect a missing file and create a PR for a human to review.
- **Custom properties** read an existing file and write metadata via the GitHub
  API. There is no PR involved -- the properties are set directly.

This means custom properties should **not** be modeled as a `FileRule`. Instead,
they should be a parallel subsystem in the checker engine that runs alongside
file checks. The engine already has the `CheckRepo` method that is called for
every repo -- the properties check slots in there.

---

## Implementation Plan

### Phase 1: GitHub Client Interface Extensions

**File: `internal/github/github.go`**

Add a `CustomPropertyValue` type and two new methods to the `Client` interface:

```go
// CustomPropertyValue represents a single custom property key-value pair.
type CustomPropertyValue struct {
    PropertyName string
    Value        interface{} // string, []string, or nil
}

// Add to Client interface:

// GetCustomPropertyValues returns all custom property values set on a repository.
GetCustomPropertyValues(ctx context.Context, owner, repo string) ([]*CustomPropertyValue, error)

// SetCustomPropertyValues creates or updates custom property values on a repository.
SetCustomPropertyValues(ctx context.Context, owner, repo string, properties []*CustomPropertyValue) error
```

**File: `internal/github/client.go`**

Implement both methods using go-github v68's existing API support:

- `GetCustomPropertyValues` wraps `Repositories.GetAllCustomPropertyValues()`
- `SetCustomPropertyValues` wraps `Repositories.CreateOrUpdateCustomProperties()`

go-github v68 already has full support for these endpoints (verified in
`github/repos_properties.go`). The `CustomPropertyValue` type in go-github
uses `interface{}` for the value (can be `string`, `[]string`, or `nil`).

**GitHub App permission required:** The app needs the `custom_properties`
repository permission set to **Read & Write** in the GitHub App settings.
This is an additional permission that must be added to the app registration.

### Phase 2: Backstage Catalog Parser

**File: `internal/catalog/parser.go`** (new package)

A small parser that reads a `catalog-info.yaml` file content string and
extracts the four properties we care about:

```go
package catalog

// Properties holds the values extracted from a Backstage catalog-info.yaml.
type Properties struct {
    Owner       string // spec.owner
    Component   string // metadata.name
    JiraProject string // metadata.annotations["jira/project-key"]
    JiraLabel   string // metadata.annotations["jira/label"]
}

// Parse reads a catalog-info.yaml content string and extracts custom
// property values. Returns an error if the content is not valid YAML
// or is not a Backstage Component entity.
func Parse(content string) (*Properties, error)
```

Implementation notes:

- Use `gopkg.in/yaml.v3` (or the stdlib-compatible option) for YAML parsing.
  Check if an existing YAML dependency is already in `go.mod` -- if not, add
  one. Alternatively, since the catalog-info structure is well-defined, a
  minimal struct-based unmarshal is sufficient and avoids a new dependency.
- Validate that `apiVersion` is `backstage.io/v1alpha1` and `kind` is
  `Component`. Skip silently (return nil properties, no error) if the file
  is not a Backstage Component -- repos may have other YAML files at this
  path.
- Return an error only for genuinely malformed YAML, not for missing optional
  fields. If `jira/project-key` or `jira/label` annotations are absent,
  those properties are simply empty strings and should not be set.

**File: `internal/catalog/parser_test.go`**

Table-driven tests covering:
- Valid catalog-info.yaml with all four fields
- Missing annotations (jira fields absent)
- Wrong `kind` (not Component) -- should return nil, nil
- Wrong `apiVersion` -- should return nil, nil
- Malformed YAML -- should return error
- Empty `spec.owner` -- should still parse, Owner is empty string

### Phase 3: Properties Checker in Engine

**File: `internal/checker/properties.go`** (new file in existing package)

A dedicated function that the engine calls after file rule checks:

```go
// CheckCustomProperties reads the repo's catalog-info.yaml and sets
// GitHub custom properties based on its contents. Returns nil if the
// file does not exist or if all properties are already correct.
func (e *Engine) CheckCustomProperties(
    ctx context.Context,
    client ghclient.Client,
    owner, repo string,
) error
```

Flow:

1. **Read `catalog-info.yaml`** -- call `client.GetContents()` to check if it
   exists. Then call a new `GetFileContent()` client method (or reuse
   `GetContents` with content retrieval) to get the file body. See note below.
2. **Parse** the content using `catalog.Parse()`.
3. If parse returns nil properties (not a Backstage Component), log and return.
4. **Get current custom properties** via `client.GetCustomPropertyValues()`.
5. **Diff** current vs desired. Build a list of properties that need updating.
   Only set properties that are (a) non-empty in the catalog-info and (b)
   different from the current value.
6. If no changes needed, log and return.
7. If dry-run, log what would be set and return.
8. **Set properties** via `client.SetCustomPropertyValues()`.
9. Increment metrics.

**Note on reading file content:** The current `GetContents()` method only
returns a boolean (exists/not-exists). For custom properties, we need the
actual file content. Two options:

- **Option A:** Add a `GetFileContent(ctx, owner, repo, path string) (string, error)`
  method to the Client interface that returns the file body (base64-decoded).
  This is the cleaner approach since `GetContents` was intentionally designed
  as an existence check.
- **Option B:** Modify `GetContents` to return content as well. This changes
  the existing interface and all call sites.

**Recommendation: Option A.** Add `GetFileContent` as a new method. The
existing `GetContents` boolean check remains untouched. go-github's
`Repositories.GetContents()` already returns the file content -- we just need
to decode it.

### Phase 4: Engine Integration

**File: `internal/checker/engine.go`**

Modify `CheckRepo` to call `CheckCustomProperties` after the file rule loop.
The properties check runs regardless of whether file rules found missing files
-- a repo can have all its config files but still be missing custom properties.

```go
func (e *Engine) CheckRepo(ctx context.Context, client ghclient.Client, owner, repo string) error {
    // ... existing skip checks (archived, fork, empty) ...
    // ... existing file rule loop ...
    // ... existing PR creation ...

    // Check and set custom properties from catalog-info.yaml.
    if e.enableCustomProperties {
        if err := e.CheckCustomProperties(ctx, client, owner, repo); err != nil {
            log.Error("custom properties check failed", "error", err)
            // Non-fatal: log and continue. File checks already succeeded.
        }
    }

    return nil
}
```

Custom properties errors are logged but do not fail the overall check. This is
intentional: a missing or unparseable catalog-info.yaml should not prevent
file rule PRs from being created.

**File: `internal/checker/engine.go`**

Add `enableCustomProperties bool` field to the `Engine` struct and update
`NewEngine` to accept it.

### Phase 5: Configuration

**File: `internal/config/config.go`**

Add a new config option:

```go
EnableCustomProperties bool // Enable custom properties sync (default: false)
```

Environment variable: `ENABLE_CUSTOM_PROPERTIES` (default: `false`).

Default is `false` so the feature is opt-in. Existing deployments are not
affected until the operator explicitly enables it and adds the required GitHub
App permission.

### Phase 6: Metrics

**File: `internal/metrics/metrics.go`**

Add metrics for observability:

```go
// PropertiesCheckedTotal counts repos where custom properties were evaluated.
PropertiesCheckedTotal = promauto.NewCounter(prometheus.CounterOpts{
    Name: "repo_guardian_properties_checked_total",
    Help: "Total repositories where custom properties were evaluated.",
})

// PropertiesUpdatedTotal counts repos where custom properties were set/updated.
PropertiesUpdatedTotal = promauto.NewCounter(prometheus.CounterOpts{
    Name: "repo_guardian_properties_updated_total",
    Help: "Total repositories where custom properties were updated.",
})

// PropertiesSkippedTotal counts repos skipped (no catalog-info.yaml or not a Component).
PropertiesSkippedTotal = promauto.NewCounter(prometheus.CounterOpts{
    Name: "repo_guardian_properties_skipped_total",
    Help: "Total repositories skipped for custom properties (no catalog-info.yaml).",
})
```

### Phase 7: Tests

**File: `internal/checker/engine_test.go`**

Update `mockClient` with:
- `GetFileContent` mock method (returns configurable content)
- `GetCustomPropertyValues` mock method (returns configurable current properties)
- `SetCustomPropertyValues` mock method (records what was set)

Add test cases:
- `TestCheckCustomProperties_SetsFromCatalogInfo` -- happy path, all four
  properties extracted and set
- `TestCheckCustomProperties_NoCatalogFile` -- file doesn't exist, skip
- `TestCheckCustomProperties_NotBackstageComponent` -- file exists but wrong
  kind, skip
- `TestCheckCustomProperties_PropertiesAlreadyCorrect` -- no API call needed
- `TestCheckCustomProperties_PartialAnnotations` -- only Owner and Component
  set (missing Jira annotations)
- `TestCheckCustomProperties_DryRun` -- logs but doesn't call SetCustomPropertyValues
- `TestCheckCustomProperties_Disabled` -- engine with enableCustomProperties=false
  does not call any properties methods

**File: `internal/catalog/parser_test.go`**

As described in Phase 2.

### Phase 8: Documentation Updates

**File: `docs/SUMMARY.md`**

Add a section under "What It Brings to the Organization > Security and
Compliance" covering the Wiz integration:

- Custom properties sync enables Wiz to tag repositories with ownership
  metadata, ensuring repos are assigned to the correct Wiz projects.
- Properties are sourced from the existing Backstage catalog-info.yaml, so
  there is no new data entry burden -- teams already maintain this file for
  their service catalog.

**File: `docs/ONE_PAGER.md`**

Add a bullet under "Key Benefits > For security and compliance" about Wiz
integration and custom properties.

**File: `docs/RFC.md`**

Add a section (or appendix) documenting the custom properties feature,
the Backstage integration, and the Wiz tagging use case.

---

## File Change Summary

| File | Change | Phase |
|---|---|---|
| `internal/github/github.go` | Add `CustomPropertyValue` type, `GetFileContent`, `GetCustomPropertyValues`, `SetCustomPropertyValues` to interface | 1 |
| `internal/github/client.go` | Implement three new methods using go-github v68 | 1 |
| `internal/github/client_test.go` | Add httptest cases for new methods | 1 |
| `internal/catalog/parser.go` | New package: YAML parser for catalog-info.yaml | 2 |
| `internal/catalog/parser_test.go` | Table-driven tests for parser | 2 |
| `internal/checker/properties.go` | New file: `CheckCustomProperties` logic | 3 |
| `internal/checker/engine.go` | Add `enableCustomProperties` field, call `CheckCustomProperties` in `CheckRepo` | 4 |
| `internal/checker/engine_test.go` | Update mockClient, add properties test cases | 7 |
| `internal/config/config.go` | Add `EnableCustomProperties` config option | 5 |
| `internal/metrics/metrics.go` | Add 3 new properties metrics | 6 |
| `cmd/repo-guardian/main.go` | Pass `EnableCustomProperties` to `NewEngine` | 4 |
| `docs/SUMMARY.md` | Add Wiz/custom properties section | 8 |
| `docs/ONE_PAGER.md` | Add Wiz/custom properties bullet | 8 |
| `contrib/grafana/repo-guardian-dashboard.json` | Add properties panels | 8 |
| `contrib/prometheus/alerts.yaml` | Add properties alert (optional) | 8 |

---

## GitHub App Permission Change

The app registration must be updated to include the `custom_properties`
permission:

| Permission | Access | Reason |
|---|---|---|
| **Contents** | Read & Write | Existing -- read files, create branches |
| **Pull Requests** | Read & Write | Existing -- check/create PRs |
| **Metadata** | Read | Existing -- required for all apps |
| **Custom properties** | Read & Write | **New** -- read and set repo custom properties |

This is a one-time change in the GitHub App settings. Existing installations
will receive a permission update request that org admins must approve.

---

## Catalog-Info.yaml Paths to Check

The parser should look for the catalog-info file at these paths (in order):

1. `catalog-info.yaml`
2. `catalog-info.yml`

Most Backstage setups use `catalog-info.yaml` at the repo root. The `.yml`
variant is included for compatibility.

---

## Edge Cases and Decisions

### What if catalog-info.yaml is missing?

Skip the properties check for that repo. Do not create a PR to add
catalog-info.yaml -- that is a Backstage concern, not repo-guardian's job.
Increment `PropertiesSkippedTotal` metric.

### What if a property value in catalog-info.yaml is empty?

Do not set the property. Only set properties that have non-empty values in the
source file. This avoids overwriting a manually set property with an empty
string.

### What if the org hasn't defined the custom property schema?

The GitHub API returns a 422 error if you try to set a property that hasn't
been defined at the org level. The engine should log this error and continue.
The operator must create the property schema in the org settings (or via API)
before enabling this feature.

This could be documented as a prerequisite, or repo-guardian could optionally
create the org-level schema (using the Organizations API). Recommendation:
**do not auto-create the schema.** Property schema definition is an org admin
concern and should be done once, manually or via IaC. The app should require
the `organization_custom_properties` permission only if we decide to add
schema management later.

### What if the current property value already matches?

Do not call the update API. The diff step (Phase 3, step 5) ensures we only
make API calls when values actually change. This reduces unnecessary API usage
and avoids creating noise in audit logs.

### Idempotency

The properties check is fully idempotent. Running it multiple times on the
same repo with the same catalog-info.yaml produces no additional API calls
after the first successful run (because the diff detects no changes).

---

## Rollout Plan

1. **Merge code with `ENABLE_CUSTOM_PROPERTIES=false` (default).** No
   behavioral change to existing deployments.
2. **Define custom property schema in GitHub org settings.** Create the four
   properties: `Owner` (string), `Component` (string), `JiraProject` (string),
   `JiraLabel` (string).
3. **Update GitHub App permissions.** Add `custom_properties: read & write`.
   Org admins approve the permission request.
4. **Deploy with `ENABLE_CUSTOM_PROPERTIES=true` and `DRY_RUN=true`.** Observe
   logs to confirm correct property extraction from catalog-info.yaml files.
5. **Disable dry-run.** Properties are now being set on repos. Monitor
   `PropertiesUpdatedTotal` metric.
6. **Verify in Wiz.** Confirm that scanned repos now have the expected tags
   derived from custom properties.
