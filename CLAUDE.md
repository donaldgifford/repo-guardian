# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

repo-guardian is a GitHub App (Go) that automates repository onboarding and compliance across a GitHub organization. It detects missing configuration files (CODEOWNERS, Dependabot, Renovate) and creates PRs with sensible defaults. Deployment targets: Talos k8s cluster (dev/test) and EKS (production). Documentation is managed via docz — see `docs/rfc/`, `docs/design/`, `docs/impl/`, `docs/investigation/` for structured docs.

## Build & Development Commands

```bash
make build            # Build binary to build/bin/repo-guardian
make test             # Run tests with race detector (go test -v -race ./...)
make test-coverage    # Run tests with coverage report
make lint             # Run golangci-lint
make lint-fix         # Run golangci-lint with auto-fix
make fmt              # Format code (gofmt, goimports, gofumpt, golines, gci)
make check            # Quick pre-commit check (lint + test)
make ci               # Full CI pipeline (lint + test + build)
make run-local        # Build and run locally
```

Run a single test: `go test -v -race -run TestName ./internal/package/...`

## Tool Versions

Managed via `mise.toml`. Key tools: Go 1.25.4, golangci-lint v2.8.0, mockery v2, golines, yamlfmt, yamllint, yq, helm 3.19, helm-cr, helm-ct, helm-diff, helm-docs, helm-unittest.

## Architecture

```
cmd/repo-guardian/main.go  → entrypoint (dual HTTP servers, graceful shutdown)
internal/
  catalog/    → Backstage catalog-info.yaml parser (gopkg.in/yaml.v3)
  config/     → configuration management (12-factor env vars)
  policy/     → HCL policy config: parser, loader, validation, YAML path evaluator, content assertions
  github/     → GitHub API client wrapper (go-github v68 + ghinstallation v2)
  checker/    → core check-and-PR engine + work queue + setting rules + branch protection rules
  reconciler/ → pluggable post-check reconcilers (custom_properties, label_sync, branch_protection, workflow_sync) with factory registry
  rules/      → FileRule registry + TemplateStore (embedded fallback templates)
  webhook/    → HTTP handler for GitHub webhook events (HMAC-validated) + IP allowlist middleware + push event handler
  scheduler/  → in-process ticker for weekly reconciliation
  metrics/    → Prometheus metrics (21 metrics total, most labeled with org)
charts/
  repo-guardian/ → Helm chart (recommended deployment method)
deploy/
  base/       → Kustomize base (DEPRECATED — use Helm chart)
  overlays/   → dev, prod, tailscale overlays (DEPRECATED)
docs/
  rfc/        → High-level proposals (docz managed)
  design/     → Technical design documents (docz managed)
  impl/       → Implementation plans (docz managed)
  investigation/ → Research and spike investigations (docz managed)
  adr/        → Architecture decision records (docz managed)
```

**Core flow:** GitHub webhook OR weekly scheduler OR push event → work queue (buffered channel) → checker engine → GitHub API (create PRs for missing files) → reconcilers (post-check actions like custom property sync).

**Key design patterns:**

- **HCL policy engine** — optional `guardian.hcl` config (set via `GUARDIAN_CONFIG` env var) defines file rules with three check modes: `exists`, `contains` (with regex/YAML path assertions), `exact` (YAML semantic or byte comparison). Also supports `rule "setting"` blocks (8 repo properties with optional remediation) and `rule "branch_protection"` blocks (rulesets API). Global and per-rule `ignore {}` blocks with glob pattern matching skip repos. Config merge order: built-in defaults → HCL file → env var overrides. Uses `hclparse.NewParser()` (not `hclsimple` due to `hcl:"-"` tag incompatibility). The engine has dual paths: `NewEngine` (legacy registry) and `NewEngineFromPolicy` (policy-based), dispatched via `e.policy != nil` in `CheckRepo`.
- **FileRule registry** — each rule defines paths to check, default templates, and PR detection logic. New rules are added without modifying core engine code. Legacy path retained for backward compatibility.
- **Deterministic branch naming** — single branch per repo (`repo-guardian/add-missing-files`) for idempotent PR creation.
- **Work queue** with configurable concurrency (buffered channel + N worker goroutines) for rate-limit-safe GitHub API usage.
- **Installation-scoped clients** — each job creates a GitHub client scoped to the specific installation, with cached transport tokens.
- **Reconciler pattern** — pluggable post-check behaviors attached to file rules via `reconcile` blocks in HCL config. Four built-in reconcilers: `custom_properties` (Backstage catalog-info.yaml → GitHub custom properties, `api` or `github-action` mode), `label_sync` (YAML-driven label create/update/rename/delete with `delete_extra` option), `branch_protection` (YAML-driven ruleset management via rulesets API), `workflow_sync` (lightweight observability for watched workflow files). Reconcilers run after file existence/assertion checks pass. Factory registry in `internal/reconciler/`. Backward compat: `CUSTOM_PROPERTIES_MODE` env var injects a `catalog_info` rule with reconciler into built-in defaults when no HCL config is present.
- **Push event handler** — webhook handler accepts watched file paths (extracted from reconcilers with `watch = true`). Pushes to the default branch that add or modify watched files trigger a re-check via `TriggerPush`. Tag pushes and removed-only changes are ignored.
- **Webhook IP allowlist** — middleware wraps only the webhook route (not health/metrics). Two-layer defense: IP allowlist (403) then HMAC validation (401). See `SECURITY.md`.
- **Tailscale Funnel** — forwards client IPs via `X-Forwarded-For` (RemoteAddr is `127.0.0.1`). Tailscale overlay requires `TRUST_PROXY_HEADERS=true`.
- **Per-org rule scoping (DESIGN-0010)** — optional top-level `scope { orgs = [...] }` block engages strict mode where every rule must declare its own `scope { }` sub-block. Absence preserves legacy mode (every rule applies to every repo). Rule-level `["*"]` is the universal "applies to every in-scope org" idiom. Per-rule scope without top-level scope emits a single `slog.Warn` at load time. Two evaluation gates in `engine_policy.go.checkRepoWithPolicy`: policy-level (skips entire repo, increments `OutOfScopeTotal{level=policy}` once per enabled rule) and rule-level (skips that rule, increments `OutOfScopeTotal{level=rule}`). Helpers and `OutOfScopeTotal` counter live in `internal/checker/scope.go`.
- **Per-org metrics labels** — all per-rule and per-repo Prometheus counters carry an `org` label (`repos_checked_total[trigger, org]`, `files_missing_total[rule_name, org]`, `prs_created_total[org]` (CounterVec), etc.). Pre-existing scalar query semantics are preserved with `sum(...)`. Catalog and migration recipes in `contrib/README.md`.

## Docker

```bash
docker build -t repo-guardian:dev .   # Multi-stage: golang:1.25 builder + distroless runtime (~19.5MB)
```

## Code Style & Linting

- Follows **Uber Go Style Guide** conventions.
- golangci-lint config (`.golangci.yml`) enables 50+ linters with strict limits: cyclomatic complexity ≤15, cognitive complexity ≤30, function length ≤100 lines, nesting depth ≤4.
- Import ordering enforced by gci: stdlib → external → local (`github.com/donaldgifford`).
- Formatters: gofumpt (stricter gofmt), golines (150 char max).

## Testing

- Use standard Go testing with race detector enabled.
- Tests use hand-written mock clients implementing the `github.Client` interface (no mockery generation).
- `httptest.Server` is used for GitHub API mocks in `internal/github/client_test.go`.
- Note: `t.Parallel()` cannot be used with `t.Setenv()` in Go 1.25+ (panics at runtime). Config tests avoid `t.Parallel()`.
- Counter assertions: `prometheus/client_golang/prometheus/testutil.ToFloat64(metric.WithLabelValues(...))` for value inspection; `metric.Reset()` between tests when state must be isolated.
- Coverage target: 60% (threshold: 40%), tracked via Codecov.
- Coverage ignores: `main.go`, `docs/`, `scripts/`.

## Engine and Loader Subtleties

- **Strict-mode loader behavior** — when `cfg.Scope != nil` and the user declares no rules, the loader does **not** fall back to `BuiltinDefaults().FileRules`. This is the only way the contract "every rule must declare its own scope" can hold. See the `switch` in `loader.go.hclConfigToPolicy`.
- **File-rule double-iteration** — `engine_policy.go` iterates over `policy.FileRules` in two passes: `findActionableRules` (primary) and `runReconcilers` (post-check). Counter increments belong only to the primary pass; `runReconcilers` short-circuits on scope/ignore mismatch silently. Forgetting this leads to double counts on `OutOfScopeTotal{level=rule}` and `IgnoredTotal{scope=rule}`.
- **Scope vs. ignore precedence** — both gates run before evaluation. Scope is checked first (out-of-scope skips counted in `OutOfScopeTotal`); ignore is checked second (skipped repos counted in `IgnoredTotal`). The two counters never both increment for the same rule on the same repo.
- **Phantom `gopls` diagnostics** — when adding a new file in `internal/checker/`, `gopls` may show `undefined: Engine` errors for a few seconds while the cache rebuilds. Real compile/test results are authoritative; ignore the IDE warnings if `make lint` and `make test` pass.

## Release

GoReleaser builds for linux/darwin on amd64/arm64 (CGO disabled). Releases are GPG-signed. Semantic versioning via PR labels (`major`, `minor`, `patch`).

## Rules

These rules must always be followed when working in this repository.

1. **Use the `todo-comments` skill for code annotations.** All TODO, FIX, HACK,
   WARN, PERF, NOTE, and TEST comments must follow the todo-comments format.
   Respect and obey `CLAUDE` type directives — these are binding behavioral
   instructions embedded in code.
2. **Never commit directly to `main`.** All changes go through feature branches
   and pull requests. Use the `git-workflow` skill (`/branch`) to create
   branches with the correct type prefix (feat/, fix/, chore/, docs/, bug/).
3. **Always look for enabled skills to use.** Check what skills are enabled for
   the repo and use those as guiding tools for work.
4. **Always check for make target for a command.** Check if there is an existing
   make target for what you are trying to run. This helps with automating your
   ability to run commands within the scope of safety we have defined.
5. **Use docz for all structured documentation.** When creating design docs,
   investigations, implementation plans, RFCs, or ADRs, use the `docz` skill
   (`docz create <type> "Title"`). Do not create raw markdown files in `docs/` —
   use the docz types configured in `.docz.yaml`.
