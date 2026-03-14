---
id: IMPL-0002
title: "Custom Properties Implementation Plan"
status: Completed
author: Donald Gifford
created: 2026-03-01
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0002: Custom Properties Implementation Plan

**Status:** Completed
**Author:** Donald Gifford
**Date:** 2026-03-01

Detailed implementation plan derived from
[DESIGN-0001](../design/0001-custom-properties-from-backstage.md). Each phase
is independently verifiable. Phases must be completed in order -- later phases
depend on earlier ones.

## Design Decisions

1. **YAML library:** Use `gopkg.in/yaml.v3` as a direct dependency.
2. **GHA workflow token:** Uses `${{ secrets.GITHUB_TOKEN }}` (standard GitHub
   Actions token), not a custom org secret.
3. **Stale branch cleanup:** Follows the same `findOurPR` pattern as the
   file-rules engine.

## Phase 1: Catalog Parser

**Package:** `internal/catalog`

- Parse `catalog-info.yaml` using Go struct tags for type-safe YAML
  unmarshaling.
- `Entity`, `Metadata`, `Spec` structs with `yaml` tags.
- `Properties` struct with `Owner`, `Component`, `JiraProject`, `JiraLabel`.
- `Parse(content string) *Properties` function with defaults for
  invalid/missing input.
- Table-driven tests: 9 cases covering all edge conditions.

## Phase 2: GitHub Client Extensions

**Package:** `internal/github`

- `CustomPropertyValue` type.
- `GetFileContent(ctx, owner, repo, path)` -- returns decoded file content.
- `GetCustomPropertyValues(ctx, owner, repo)` -- reads current properties.
- `SetCustomPropertyValues(ctx, owner, repo, properties)` -- writes properties
  (api mode only).
- httptest-based tests for all three methods.
- Mock client updated with new methods for checker tests.

## Phase 3: Configuration

**Package:** `internal/config`

- `CustomPropertiesMode string` field.
- Env var: `CUSTOM_PROPERTIES_MODE`.
- Valid values: `""` (disabled), `"github-action"`, `"api"`.
- Validation rejects invalid values at startup.

## Phase 4: Metrics

**Package:** `internal/metrics`

Four new Prometheus counters:

- `repo_guardian_properties_checked_total`
- `repo_guardian_properties_prs_created_total`
- `repo_guardian_properties_set_total`
- `repo_guardian_properties_already_correct_total`

## Phase 5: Templates

Two embedded templates:

- `set-custom-properties.tmpl` -- GHA workflow for `github-action` mode.
- `catalog-info.tmpl` -- default Backstage entity for `api` mode.

## Phase 6: Properties Checker

**File:** `internal/checker/properties.go`

Core custom properties logic:

- `CheckCustomProperties(ctx, client, owner, repo, defaultBranch, openPRs)`
- Common steps: read catalog-info.yaml, parse with `catalog.Parse()`, read
  current properties, diff desired vs current.
- `github-action` mode: render GHA workflow template, create PR.
- `api` mode: set properties directly, create catalog-info PR if file missing.
- Stale branch cleanup following `findOurPR` pattern.
- Helper functions: `findPropertiesPR`, `diffProperties`,
  `renderTemplate`, `buildPropertiesPRBody`.

## Phase 7: Engine Integration & Wiring

- Add `customPropertiesMode string` to `Engine` struct.
- Update `NewEngine` signature.
- Call `CheckCustomProperties` in `CheckRepo` when mode is non-empty.
- Thread `cfg.CustomPropertiesMode` from `main.go`.

## Phase 8: Tests

**File:** `internal/checker/properties_test.go`

- 9 `github-action` mode tests (catalog-info present, missing, unparseable,
  wrong kind, already correct, partial annotations, existing PR, dry-run,
  stale branch cleanup).
- 6 `api` mode tests (catalog-info present, missing, already correct, existing
  PR, stale branch, dry-run).
- 1 disabled mode test.
- 4 helper tests.

## Phase 9: Documentation & Observability Updates

- Updated `SUMMARY.md`, `ONE_PAGER.md` with Wiz/custom properties coverage.
- Grafana dashboard panels for new metrics.
- Prometheus alert for PR burst detection.
- `CLAUDE.md` architecture section updated.

## File Change Summary

| Phase | New Files | Modified Files |
|-------|-----------|----------------|
| 1 | `internal/catalog/catalog.go`, `catalog_test.go` | `go.mod`, `go.sum` |
| 2 | -- | `internal/github/github.go`, `client.go`, `client_test.go`, `checker/engine_test.go` |
| 3 | -- | `internal/config/config.go`, `config_test.go` |
| 4 | -- | `internal/metrics/metrics.go` |
| 5 | `templates/set-custom-properties.tmpl`, `catalog-info.tmpl` | -- |
| 6 | `internal/checker/properties.go` | -- |
| 7 | -- | `internal/checker/engine.go`, `cmd/repo-guardian/main.go` |
| 8 | `internal/checker/properties_test.go` | -- |
| 9 | -- | `docs/SUMMARY.md`, `ONE_PAGER.md`, `contrib/grafana/...`, `contrib/prometheus/...`, `CLAUDE.md` |

## References

- [DESIGN-0001: Custom Properties from Backstage](../design/0001-custom-properties-from-backstage.md)
- [RFC-0001: Repo Compliance App](../rfc/0001-repo-compliance-app-repo-guardian.md)
