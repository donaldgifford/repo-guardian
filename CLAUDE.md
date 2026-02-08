# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

repo-guardian is a GitHub App (Go) that automates repository onboarding and compliance across a GitHub organization. It detects missing configuration files (CODEOWNERS, Dependabot, Renovate) and creates PRs with sensible defaults. Target deployment is Kubernetes (EKS). The RFC (`docs/RFC.md`) is the canonical design document; the implementation plan (`docs/IMPLEMENTATION_PLAN.md`) tracks build progress.

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

Managed via `mise.toml`. Key tools: Go 1.25.4, golangci-lint v2.8.0, mockery v2, golines, yamlfmt, yamllint, yq.

## Architecture

```
cmd/repo-guardian/main.go  → entrypoint (dual HTTP servers, graceful shutdown)
internal/
  config/     → configuration management (12-factor env vars)
  github/     → GitHub API client wrapper (go-github v68 + ghinstallation v2)
  checker/    → core check-and-PR engine + work queue
  rules/      → FileRule registry + TemplateStore (embedded fallback templates)
  webhook/    → HTTP handler for GitHub webhook events (HMAC-validated)
  scheduler/  → in-process ticker for weekly reconciliation
  metrics/    → Prometheus metrics (8 metrics total)
deploy/
  base/       → Kustomize base (deployment, service, configmap, serviceaccount)
  overlays/   → dev (dry-run, debug) and prod (live, info) overlays
```

**Core flow:** GitHub webhook OR weekly scheduler → work queue (buffered channel) → checker engine → GitHub API (create PRs for missing files).

**Key design patterns:**
- **FileRule registry** — each rule defines paths to check, default templates, and PR detection logic. New rules are added without modifying core engine code.
- **Deterministic branch naming** — single branch per repo (`repo-guardian/add-missing-files`) for idempotent PR creation.
- **Work queue** with configurable concurrency (buffered channel + N worker goroutines) for rate-limit-safe GitHub API usage.
- **Installation-scoped clients** — each job creates a GitHub client scoped to the specific installation, with cached transport tokens.

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
