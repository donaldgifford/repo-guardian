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
  github/     → GitHub API client wrapper (go-github v68 + ghinstallation v2)
  checker/    → core check-and-PR engine + work queue + custom properties checker
  rules/      → FileRule registry + TemplateStore (embedded fallback templates)
  webhook/    → HTTP handler for GitHub webhook events (HMAC-validated) + IP allowlist middleware
  scheduler/  → in-process ticker for weekly reconciliation
  metrics/    → Prometheus metrics (15 metrics total)
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

**Core flow:** GitHub webhook OR weekly scheduler → work queue (buffered channel) → checker engine → GitHub API (create PRs for missing files).

**Key design patterns:**

- **FileRule registry** — each rule defines paths to check, default templates, and PR detection logic. New rules are added without modifying core engine code.
- **Deterministic branch naming** — single branch per repo (`repo-guardian/add-missing-files`) for idempotent PR creation.
- **Work queue** with configurable concurrency (buffered channel + N worker goroutines) for rate-limit-safe GitHub API usage.
- **Installation-scoped clients** — each job creates a GitHub client scoped to the specific installation, with cached transport tokens.
- **Custom properties checker** — reads Backstage `catalog-info.yaml`, diffs against current GitHub custom properties, and either creates a PR with a GHA workflow (`github-action` mode) or sets properties directly via API (`api` mode). Controlled by `CUSTOM_PROPERTIES_MODE` env var (empty = disabled).
- **Webhook IP allowlist** — middleware wraps only the webhook route (not health/metrics). Two-layer defense: IP allowlist (403) then HMAC validation (401). See `SECURITY.md`.
- **Tailscale Funnel** — forwards client IPs via `X-Forwarded-For` (RemoteAddr is `127.0.0.1`). Tailscale overlay requires `TRUST_PROXY_HEADERS=true`.

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
- Coverage target: 60% (threshold: 40%), tracked via Codecov.
- Coverage ignores: `main.go`, `docs/`, `scripts/`.

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
