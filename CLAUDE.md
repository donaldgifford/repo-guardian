# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

repo-guardian is a GitHub App (Go) that automates repository onboarding and compliance across a GitHub organization. It detects missing configuration files (CODEOWNERS, Dependabot, Renovate) and creates PRs with sensible defaults. Target deployment is Kubernetes (EKS). The project is in early implementation phase — the RFC (`docs/RFC.md`) is the canonical design document.

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

## Architecture (Planned — see docs/RFC.md)

```
cmd/repo-guardian/main.go  → entrypoint
internal/
  config/     → configuration management (env vars)
  github/     → GitHub API client wrapper (go-github)
  checker/    → core check-and-PR engine
  rules/      → FileRule registry (extensible rule system)
  webhook/    → HTTP webhook handler for GitHub events
  scheduler/  → weekly reconciliation loop
```

**Core flow:** GitHub webhook OR weekly scheduler → work queue → checker engine → GitHub API (create PRs for missing files).

**Key design patterns:**
- **FileRule registry** — each rule defines paths to check, default templates, and PR detection logic. New rules are added without modifying core engine code.
- **Deterministic branch naming** — single branch per repo (`repo-guardian/add-missing-files`) for idempotent PR creation.
- **Work queue** with configurable concurrency for rate-limit-safe GitHub API usage.

## Code Style & Linting

- Follows **Uber Go Style Guide** conventions.
- golangci-lint config (`.golangci.yml`) enables 50+ linters with strict limits: cyclomatic complexity ≤15, cognitive complexity ≤30, function length ≤100 lines, nesting depth ≤4.
- Import ordering enforced by gci: stdlib → external → local (`github.com/donaldgifford`).
- Formatters: gofumpt (stricter gofmt), golines (150 char max).

## Testing

- Use standard Go testing with race detector enabled.
- Coverage target: 60% (threshold: 40%), tracked via Codecov.
- Coverage ignores: `main.go`, `docs/`, `scripts/`.

## Release

GoReleaser builds for linux/darwin on amd64/arm64 (CGO disabled). Releases are GPG-signed. Semantic versioning via PR labels (`major`, `minor`, `patch`).
