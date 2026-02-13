# Custom Properties Implementation Plan

Detailed implementation plan derived from `docs/custom_properties.md`. Each phase
is independently verifiable. Phases must be completed in order -- later phases
depend on earlier ones.

Reference: `docs/custom_properties.md` (feature plan), `examples/catalog-info.yaml`
(sample Backstage entity).

### Design Decisions (from review)

1. **YAML library:** Use `gopkg.in/yaml.v3` as a direct dependency. The
   existing indirect `go.yaml.in/yaml/v2` is a transitive dependency from
   `prometheus/client_golang` and cannot be removed.
2. **GHA workflow token:** The `set-custom-properties` workflow template uses
   `${{ secrets.GITHUB_TOKEN }}` (the standard GitHub Actions token), not a
   custom org secret.
3. **Stale branch cleanup:** The properties checker follows the same
   `findOurPR` pattern as the file-rules engine. When a PR is closed without
   merging and the files are still needed, the stale branch is deleted, a new
   branch is created from the current default branch HEAD, and a fresh PR is
   opened.

---

## Phase 1: Catalog Parser

Add the `internal/catalog` package that parses Backstage `catalog-info.yaml`
files into a `Properties` struct using Go struct tags. This package has no
dependency on the rest of the codebase and can be developed and tested in
isolation.

### Tasks

- [ ] Add `gopkg.in/yaml.v3` as a direct dependency (`go get gopkg.in/yaml.v3`)
- [ ] Create `internal/catalog/catalog.go` with:
  - [ ] `Entity` struct with `yaml` struct tags (`APIVersion`, `Kind`, `Metadata`, `Spec`)
  - [ ] `Metadata` struct with `Name string` and `Annotations map[string]string` (yaml tags)
  - [ ] `Spec` struct with `Owner string` and other Backstage fields (yaml tags)
  - [ ] `Properties` struct with `Owner`, `Component`, `JiraProject`, `JiraLabel` string fields
  - [ ] Constants: `DefaultOwner = "Unclassified"`, `DefaultComponent = "Unclassified"`
  - [ ] `Parse(content string) *Properties` function:
    - Unmarshal YAML into `Entity` using `yaml.v3`
    - Validate `apiVersion == "backstage.io/v1alpha1"` and `kind == "Component"`
    - Extract `Owner` from `spec.owner`, `Component` from `metadata.name`
    - Extract `JiraProject` from `metadata.annotations["jira/project-key"]`
    - Extract `JiraLabel` from `metadata.annotations["jira/label"]`
    - Apply `Unclassified` defaults for empty `Owner` or `Component`
    - Return defaults for unparseable or non-Component entities
  - [ ] Unexported `defaults()` helper returning `*Properties` with default values
- [ ] Create `internal/catalog/catalog_test.go` with table-driven tests:
  - [ ] All fields present (full catalog-info.yaml from `examples/`)
  - [ ] Missing Jira annotations (Owner + Component set, Jira fields empty)
  - [ ] Empty `spec.owner` (Owner = `Unclassified`)
  - [ ] Empty `metadata.name` (Component = `Unclassified`)
  - [ ] Wrong `kind` (e.g., `kind: API`) returns defaults
  - [ ] Wrong `apiVersion` (e.g., `apiVersion: v2`) returns defaults
  - [ ] Malformed YAML (`{{{`) returns defaults
  - [ ] Empty string returns defaults
  - [ ] Valid YAML but not a Backstage entity (random YAML doc) returns defaults

### Success Criteria

- `go test -v -race ./internal/catalog/...` passes with all 9+ test cases green
- `Parse()` returns correct `Properties` for valid input matching `examples/catalog-info.yaml`
- `Parse()` returns `Unclassified` defaults for every invalid/missing/unparseable input
- No changes to any existing files outside the new `internal/catalog/` package
- `go mod tidy` shows `gopkg.in/yaml.v3` in `go.mod` and `go.sum`

---

## Phase 2: GitHub Client Extensions

Extend the `Client` interface and `GitHubClient` implementation with three new
methods for reading file content, reading custom properties, and writing custom
properties. The write method is only used in `api` mode.

### Tasks

- [ ] **`internal/github/github.go`** -- add types and interface methods:
  - [ ] Add `CustomPropertyValue` struct:
    ```go
    type CustomPropertyValue struct {
        PropertyName string
        Value        string
    }
    ```
  - [ ] Add `GetFileContent(ctx, owner, repo, path string) (string, error)` to `Client` interface
    - Returns decoded file content as string
    - Returns `("", nil)` if the file does not exist (404)
    - Distinct from existing `GetContents()` which returns `(bool, error)`
  - [ ] Add `GetCustomPropertyValues(ctx, owner, repo string) ([]*CustomPropertyValue, error)` to `Client` interface
  - [ ] Add `SetCustomPropertyValues(ctx, owner, repo string, properties []*CustomPropertyValue) error` to `Client` interface

- [ ] **`internal/github/client.go`** -- implement the three methods:
  - [ ] `GetFileContent`: use `Repositories.GetContents()`, call `content.GetContent()` to decode Base64, return `("", nil)` on 404
  - [ ] `GetCustomPropertyValues`: use `Repositories.GetAllCustomPropertyValues()` from go-github v68 (`repos_properties.go`), map `*github.CustomPropertyValue` (which uses `interface{}` for Value) to our string-typed `CustomPropertyValue`
  - [ ] `SetCustomPropertyValues`: use `Repositories.CreateOrUpdateCustomProperties()` from go-github v68, convert our `CustomPropertyValue` slice to go-github's format

- [ ] **`internal/github/client_test.go`** -- add httptest cases:
  - [ ] `TestGetFileContent_Exists` -- mock 200 response with Base64-encoded content, verify decoded string
  - [ ] `TestGetFileContent_NotFound` -- mock 404 response, verify returns `("", nil)`
  - [ ] `TestGetCustomPropertyValues` -- mock response with property values, verify mapping
  - [ ] `TestSetCustomPropertyValues` -- mock PATCH endpoint, verify request body contains correct properties

- [ ] **`internal/checker/engine_test.go`** -- add the three new methods to `mockClient`:
  - [ ] `fileContents map[string]string` field (keyed by `"owner/repo/path"`)
  - [ ] `customProperties map[string][]*ghclient.CustomPropertyValue` field (keyed by `"owner/repo"`)
  - [ ] `setProperties []*ghclient.CustomPropertyValue` field (records what was set)
  - [ ] `getFileContentErr error` field
  - [ ] `getCustomPropsErr error` field
  - [ ] `setCustomPropsErr error` field
  - [ ] Implement `GetFileContent` -- return from `fileContents` map, `("", nil)` if missing
  - [ ] Implement `GetCustomPropertyValues` -- return from `customProperties` map
  - [ ] Implement `SetCustomPropertyValues` -- record in `setProperties`

### Success Criteria

- `go test -v -race ./internal/github/...` passes with all new test cases green
- `go test -v -race ./internal/checker/...` passes (existing tests still pass with updated mockClient)
- `GetFileContent` correctly decodes Base64 file content from GitHub API response
- `GetFileContent` returns empty string with nil error for 404 (not found)
- `GetCustomPropertyValues` correctly maps go-github's `interface{}` values to strings
- `SetCustomPropertyValues` correctly formats the request body for go-github
- `make lint` passes with no new warnings

---

## Phase 3: Configuration

Add `CUSTOM_PROPERTIES_MODE` to the config package. Validation rejects invalid
values at startup. The feature is disabled when the value is empty (the default).

### Tasks

- [ ] **`internal/config/config.go`**:
  - [ ] Add `CustomPropertiesMode string` field to `Config` struct with comment documenting valid values (`""`, `"github-action"`, `"api"`)
  - [ ] Load from env var `CUSTOM_PROPERTIES_MODE` using `envOrDefault("CUSTOM_PROPERTIES_MODE", "")` in `Load()`
  - [ ] Add validation in `Validate()`: if value is non-empty and not `"github-action"` or `"api"`, append error `"CUSTOM_PROPERTIES_MODE must be \"\", \"github-action\", or \"api\""`

- [ ] **`internal/config/config_test.go`**:
  - [ ] Test valid values: `""`, `"github-action"`, `"api"` all pass validation
  - [ ] Test invalid value: `"invalid"` fails validation with descriptive error
  - [ ] Test default: when `CUSTOM_PROPERTIES_MODE` is unset, defaults to `""`

### Success Criteria

- `go test -v -race ./internal/config/...` passes with new test cases
- `Config.CustomPropertiesMode` defaults to `""` (disabled) when env var is not set
- Setting `CUSTOM_PROPERTIES_MODE=github-action` or `CUSTOM_PROPERTIES_MODE=api` passes validation
- Setting `CUSTOM_PROPERTIES_MODE=invalid` causes `Validate()` to return an error
- `make check` passes

---

## Phase 4: Metrics

Add four new Prometheus counters for custom properties observability. These
follow the existing naming pattern (`repo_guardian_` prefix) and use `promauto`
for automatic registration.

### Tasks

- [ ] **`internal/metrics/metrics.go`** -- add four new metrics:
  - [ ] `PropertiesCheckedTotal` -- `Counter` named `repo_guardian_properties_checked_total`
    - Help: `"Total repositories where custom properties were evaluated."`
  - [ ] `PropertiesPRsCreatedTotal` -- `Counter` named `repo_guardian_properties_prs_created_total`
    - Help: `"Total pull requests created for custom properties."`
  - [ ] `PropertiesSetTotal` -- `Counter` named `repo_guardian_properties_set_total`
    - Help: `"Total repositories where custom properties were set via API."`
  - [ ] `PropertiesAlreadyCorrectTotal` -- `Counter` named `repo_guardian_properties_already_correct_total`
    - Help: `"Total repositories where custom properties already matched desired values."`

### Success Criteria

- `make build` compiles without errors (metrics are registered at import time via `promauto`)
- `make lint` passes
- The four new metrics follow the `repo_guardian_` naming convention
- No existing metrics are modified

---

## Phase 5: Templates

Create two template files embedded into the binary via `//go:embed`. One is the
GHA workflow for `github-action` mode. The other is a default `catalog-info.yaml`
for `api` mode when the file is missing.

### Tasks

- [ ] **`internal/rules/templates/set-custom-properties.tmpl`** -- GHA workflow template:
  - [ ] `name: Set Custom Properties`
  - [ ] Triggers on push to `main`
  - [ ] Single job with `gh api --method PATCH repos/${{ github.repository }}/properties/values` step
  - [ ] Uses `${{ secrets.GITHUB_TOKEN }}` (standard GHA token) for auth
  - [ ] Placeholder values: `OWNER_VALUE`, `COMPONENT_VALUE`, `JIRA_PROJECT_VALUE`, `JIRA_LABEL_VALUE`
  - [ ] Comments explaining prerequisites and cleanup
  - [ ] `permissions: contents: read`

- [ ] **`internal/rules/templates/catalog-info.tmpl`** -- default Backstage entity:
  - [ ] `apiVersion: backstage.io/v1alpha1`, `kind: Component`
  - [ ] Placeholder `REPO_NAME` for `metadata.name`
  - [ ] Placeholder `ORG_NAME` in `backstage.io/source-location` annotation
  - [ ] `TODO` placeholders for `description`, `jira/project-key`, `spec.owner`, `spec.system`
  - [ ] Sensible defaults for `lifecycle: production`, `type: service`

- [ ] Verify templates are picked up by existing `//go:embed templates/*.tmpl` directive in `internal/rules/registry.go`
- [ ] Verify `TemplateStore.Load()` correctly loads the new templates (no code change needed -- glob pattern handles it)

### Success Criteria

- `go test -v -race ./internal/rules/...` passes (template store loads all templates including new ones)
- Template content is accessible via `TemplateStore.Get("set-custom-properties")` and `TemplateStore.Get("catalog-info")`
- Templates contain valid YAML (parseable by a YAML linter)
- GHA workflow template is a valid GitHub Actions workflow structure
- `make lint` passes

---

## Phase 6: Properties Checker

Implement the core custom properties logic in a new file within the existing
`checker` package. This is the main business logic that reads catalog-info.yaml,
diffs properties, and either creates a PR (github-action mode) or sets
properties directly (api mode).

### Tasks

- [ ] **`internal/checker/properties.go`** -- new file in existing package:
  - [ ] Define constants:
    - `PropertiesBranchName = "repo-guardian/set-custom-properties"` (github-action mode)
    - `CatalogInfoBranchName = "repo-guardian/add-catalog-info"` (api mode, catalog-info PR)
    - `PropertiesPRTitle = "chore: set repository custom properties"`
    - `CatalogInfoPRTitle = "chore: add catalog-info.yaml"`
  - [ ] `CheckCustomProperties(ctx, client, owner, repo, defaultBranch string, openPRs []*ghclient.PullRequest) error` method on `*Engine`:
    - Increment `PropertiesCheckedTotal`
    - Read `catalog-info.yaml` via `client.GetFileContent()` (try `.yaml` then `.yml`)
    - Parse content with `catalog.Parse()` (returns defaults if empty/invalid)
    - Read current custom properties via `client.GetCustomPropertyValues()`
    - Diff desired vs current (compare `Owner`, `Component` always; compare `JiraProject`, `JiraLabel` only if desired value is non-empty)
    - If match: increment `PropertiesAlreadyCorrectTotal`, return
    - Branch to mode-specific logic (below)

  - [ ] Stale branch cleanup (follows `findOurPR` pattern from `engine.go`):
    - [ ] `findPropertiesPR(openPRs, branchName)` helper -- find open PR with matching head branch
    - [ ] If our branch exists (via `GetBranchSHA`) but no open PR found, delete the stale branch
    - [ ] After stale branch deletion, continue to create a new branch from current default branch HEAD and open a fresh PR
    - [ ] Pattern must be consistent with `findOurPR` / `createOrUpdatePR` in `engine.go` so both can be refactored together later
    - [ ] If an open PR is found for the branch, skip (no duplicate work)

  - [ ] `github-action` mode flow:
    - If dry-run: log what would happen, return
    - Handle stale branch cleanup for `PropertiesBranchName`
    - Render `set-custom-properties` template with actual values substituted for placeholders
    - Get default branch SHA, create branch `repo-guardian/set-custom-properties`
    - Commit rendered workflow to `.github/workflows/set-custom-properties.yml`
    - Create PR with descriptive body explaining what properties will be set
    - Increment `PropertiesPRsCreatedTotal`

  - [ ] `api` mode flow -- catalog-info.yaml found:
    - If dry-run: log what would happen, return
    - Call `client.SetCustomPropertyValues()` with desired values
    - Increment `PropertiesSetTotal`

  - [ ] `api` mode flow -- catalog-info.yaml NOT found:
    - Set `Owner=Unclassified`, `Component=Unclassified` via API (if not already set)
    - Increment `PropertiesSetTotal` (if values changed)
    - Handle stale branch cleanup for `CatalogInfoBranchName`
    - Check for existing catalog-info PR (via `findPropertiesPR`) -- skip PR creation if found
    - If dry-run: log, return
    - Render `catalog-info` template with `REPO_NAME` and `ORG_NAME` substituted
    - Create branch `repo-guardian/add-catalog-info`, commit template, create PR
    - Increment `PropertiesPRsCreatedTotal`

  - [ ] Helper: `findPropertiesPR(openPRs []*ghclient.PullRequest, branchName string) *ghclient.PullRequest`
    - Mirrors `findOurPR` from `engine.go` -- finds open PR whose Head matches the given branch name
    - Keeps pattern consistent so both can be unified later if needed

  - [ ] Helper: `diffProperties(desired *catalog.Properties, current []*ghclient.CustomPropertyValue) bool`
    - Returns true if any desired property differs from current
    - Only compares Jira fields when desired value is non-empty

  - [ ] Helper: `renderTemplate(templateContent string, replacements map[string]string) string`
    - Simple string replacement for template placeholders

  - [ ] Helper: `buildPropertiesPRBody(props *catalog.Properties, mode string) string`
    - Generates markdown PR body explaining the properties being set

### Success Criteria

- `make build` compiles without errors
- `make lint` passes
- `CheckCustomProperties` is callable from `Engine` (same package, method receiver)
- Both mode flows are implemented with dry-run support
- Stale branch cleanup follows the `findOurPR` pattern from `engine.go`:
  branch exists but no open PR → delete branch → create new branch from HEAD → open fresh PR
- All branch/PR operations follow existing patterns from `createOrUpdatePR` in `engine.go`
- Metrics are incremented at the correct points
- Logging follows existing `slog` patterns with contextual fields

---

## Phase 7: Engine Integration & Wiring

Wire the properties checker into the engine's `CheckRepo` flow and thread the
configuration from `main.go` through to the engine constructor.

### Tasks

- [ ] **`internal/checker/engine.go`**:
  - [ ] Add `customPropertiesMode string` field to `Engine` struct
  - [ ] Update `NewEngine` signature to accept `customPropertiesMode string` parameter
  - [ ] Store the mode on the engine struct
  - [ ] Add `CheckCustomProperties` call at the end of `CheckRepo`, after file rule processing:
    ```go
    if e.customPropertiesMode != "" {
        if err := e.CheckCustomProperties(ctx, client, owner, repo, repoInfo.DefaultRef, openPRs); err != nil {
            log.Error("custom properties check failed", "error", err)
            // Non-fatal: log and continue. File rules already succeeded.
        }
    }
    ```

- [ ] **`cmd/repo-guardian/main.go`**:
  - [ ] Pass `cfg.CustomPropertiesMode` to `checker.NewEngine()` call
  - [ ] Log the configured mode at startup: `logger.Info("custom properties mode", "mode", cfg.CustomPropertiesMode)`

### Success Criteria

- `make build` compiles without errors
- Existing tests in `internal/checker/...` still pass (updated `NewEngine` call in `testEngine` helper)
- `CheckRepo` calls `CheckCustomProperties` when mode is non-empty
- `CheckRepo` does NOT call `CheckCustomProperties` when mode is `""`
- `main.go` compiles and correctly threads the config value
- `make check` passes

---

## Phase 8: Tests

Comprehensive test coverage for the properties checker. Tests use the existing
`mockClient` pattern in `engine_test.go`. Both modes, dry-run, edge cases, and
error paths are covered.

### Tasks

- [ ] **`internal/checker/properties_test.go`** -- new file (or add to `engine_test.go`):

  - [ ] `github-action` mode tests:
    - [ ] `TestGHAMode_SetsFromCatalogInfo` -- catalog-info.yaml present, properties differ from current -> PR created with GHA workflow containing correct values
    - [ ] `TestGHAMode_NoCatalogFile` -- no catalog-info.yaml -> PR with GHA workflow using Unclassified defaults
    - [ ] `TestGHAMode_UnparseableFile` -- invalid YAML content -> PR with Unclassified defaults
    - [ ] `TestGHAMode_NotBackstageComponent` -- `kind: API` -> PR with Unclassified defaults
    - [ ] `TestGHAMode_AlreadyCorrect` -- properties already match -> no PR created, `PropertiesAlreadyCorrectTotal` incremented
    - [ ] `TestGHAMode_PartialAnnotations` -- missing Jira annotations -> PR sets only Owner + Component
    - [ ] `TestGHAMode_ExistingPR` -- PR already open with properties branch -> skipped
    - [ ] `TestGHAMode_DryRun` -- dry-run enabled -> logged, no branch/PR/file created
    - [ ] `TestGHAMode_StaleBranchCleanup` -- branch exists but no open PR -> stale branch deleted, new branch created from HEAD, fresh PR opened

  - [ ] `api` mode tests:
    - [ ] `TestAPIMode_SetsFromCatalogInfo` -- catalog-info.yaml present, properties differ -> `SetCustomPropertyValues` called with correct values
    - [ ] `TestAPIMode_NoCatalogFile` -- no catalog-info.yaml -> Unclassified set via API + PR with catalog-info.yaml template
    - [ ] `TestAPIMode_AlreadyCorrect` -- properties match -> no API call, no PR
    - [ ] `TestAPIMode_NoCatalog_ExistingPR` -- no file but catalog-info PR open -> API sets defaults, no duplicate PR
    - [ ] `TestAPIMode_NoCatalog_StaleBranchCleanup` -- catalog-info branch exists but no open PR -> stale branch deleted, new branch + PR created
    - [ ] `TestAPIMode_DryRun` -- dry-run enabled -> no API call, no PR

  - [ ] Disabled mode test:
    - [ ] `TestCustomProperties_Disabled` -- `customPropertiesMode=""` -> `CheckCustomProperties` never called (verify via `CheckRepo`)

  - [ ] Each test verifies:
    - Correct branches created/not created
    - Correct files committed/not committed
    - Correct PRs created/not created
    - Correct API calls made/not made (api mode)
    - Stale branches deleted when appropriate
    - Correct metrics incremented (where applicable)
    - Correct log output (dry-run cases)

### Success Criteria

- `go test -v -race ./internal/checker/...` passes with all 16 test cases green
- `go test -v -race -coverprofile=cover.out ./internal/checker/...` shows coverage of both mode paths
- No flaky tests (no timing dependencies, no global state leaks)
- All tests use the existing `mockClient` pattern -- no new mock frameworks
- `make check` passes (lint + all tests)

---

## Phase 9: Documentation & Observability Updates

Update existing documentation and observability assets to reflect the new
feature. This phase does not change application behavior.

### Tasks

- [ ] **`docs/SUMMARY.md`** -- add section under "Security and Compliance":
  - [ ] Custom properties sync for Wiz integration
  - [ ] Backstage catalog-info.yaml as source of truth
  - [ ] Two modes: `github-action` (default, least-privilege) and `api`
  - [ ] `Unclassified` defaults for repos without catalog-info.yaml

- [ ] **`docs/ONE_PAGER.md`** -- add bullet under "Key Benefits > For security and compliance":
  - [ ] Wiz integration via custom properties tagging
  - [ ] Ownership attribution for security scanning

- [ ] **`docs/RFC.md`** (if exists) -- add appendix section for custom properties feature

- [ ] **`contrib/grafana/repo-guardian-dashboard.json`** -- add panels for new metrics:
  - [ ] "Properties Checked" stat panel
  - [ ] "Properties PRs Created" stat panel
  - [ ] "Properties Set via API" stat panel
  - [ ] "Properties Already Correct" stat panel

- [ ] **`contrib/prometheus/alerts.yaml`** -- add optional properties alert:
  - [ ] `RepoGuardianHighUnclassifiedRepos` (if applicable)

- [ ] **`CLAUDE.md`** -- update architecture section:
  - [ ] Add `catalog/` package to the architecture tree
  - [ ] Mention `CUSTOM_PROPERTIES_MODE` config option
  - [ ] Update metric count (10 -> 14)

### Success Criteria

- All documentation accurately reflects the implemented feature
- Grafana dashboard JSON is valid and importable
- Prometheus alert YAML is valid
- `CLAUDE.md` architecture tree includes the new `catalog/` package
- No documentation references features that were not implemented
- `make check` still passes (no code changes in this phase)

---

## Phase Summary

| Phase | Description | New Files | Modified Files |
|-------|-------------|-----------|----------------|
| 1 | Catalog Parser | `internal/catalog/catalog.go`, `internal/catalog/catalog_test.go` | `go.mod`, `go.sum` |
| 2 | GitHub Client Extensions | -- | `internal/github/github.go`, `internal/github/client.go`, `internal/github/client_test.go`, `internal/checker/engine_test.go` |
| 3 | Configuration | -- | `internal/config/config.go`, `internal/config/config_test.go` |
| 4 | Metrics | -- | `internal/metrics/metrics.go` |
| 5 | Templates | `internal/rules/templates/set-custom-properties.tmpl`, `internal/rules/templates/catalog-info.tmpl` | -- |
| 6 | Properties Checker | `internal/checker/properties.go` | -- |
| 7 | Engine Integration & Wiring | -- | `internal/checker/engine.go`, `cmd/repo-guardian/main.go` |
| 8 | Tests | `internal/checker/properties_test.go` | -- |
| 9 | Documentation & Observability | -- | `docs/SUMMARY.md`, `docs/ONE_PAGER.md`, `contrib/grafana/...`, `contrib/prometheus/...`, `CLAUDE.md` |

### Commit Strategy

Each phase is a single commit on the `feat/custom-properties-from-backstage`
branch. Commit messages follow the project convention:

1. `feat: add catalog-info.yaml parser package`
2. `feat: extend GitHub client with file content and custom properties methods`
3. `feat: add CUSTOM_PROPERTIES_MODE configuration`
4. `feat: add custom properties Prometheus metrics`
5. `feat: add custom properties and catalog-info templates`
6. `feat: implement custom properties checker with two-mode support`
7. `feat: wire custom properties into engine and main`
8. `test: add comprehensive custom properties test coverage`
9. `docs: update documentation for custom properties feature`

### Verification After All Phases

```bash
make check            # lint + all tests pass
make build            # binary compiles
make test-coverage    # coverage meets threshold
```
