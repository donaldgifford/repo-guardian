---
id: IMPL-0001
title: "Repo Guardian Implementation Plan"
status: Completed
author: Donald Gifford
created: 2026-02-06
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0001: Repo Guardian Implementation Plan

**Status:** Completed
**Author:** Donald Gifford
**Date:** 2026-02-06

Detailed phased implementation plan for repo-guardian, derived from
[RFC-0001](../rfc/0001-repo-compliance-app-repo-guardian.md).

## Phase 1: Core Foundation

Goal: Buildable, testable core with GitHub client, rule registry, and checker
engine -- all backed by unit tests against mocked interfaces.

### 1.1 Define interfaces and types

**Package:** `internal/github`

- Define a `Client` interface abstracting the GitHub operations the app needs:
  - `GetContents(ctx, owner, repo, path) (bool, error)` -- check if a file
    exists
  - `ListOpenPullRequests(ctx, owner, repo) ([]*PullRequest, error)`
  - `GetDefaultBranch(ctx, owner, repo) (string, error)`
  - `CreateBranch(ctx, owner, repo, baseSHA, branchName) error`
  - `CreateFileCommit(ctx, owner, repo, branch, path, content, message) error`
  - `CreatePullRequest(ctx, owner, repo, title, body, head, base) (*PullRequest, error)`
  - `UpdateBranch(ctx, owner, repo, branch, baseSHA) error`
  - `ListInstallations(ctx) ([]*Installation, error)`
  - `ListInstallationRepos(ctx, installationID) ([]*Repository, error)`
  - `IsArchived(ctx, owner, repo) (bool, error)`
  - `IsFork(ctx, owner, repo) (bool, error)`

### 1.2 FileRule registry

**Package:** `internal/rules`

- `FileRule` struct per RFC section 6.1 (Name, Paths, PRSearchTerms,
  DefaultTemplateName, TargetPath, Enabled).
- `Registry` type with `EnabledRules()` and `RuleByName()`.
- `DefaultRules` variable with three initial rules: CODEOWNERS (enabled),
  Dependabot (enabled), Renovate (disabled).
- Embedded fallback templates via `//go:embed templates/*.tmpl`.
- `TemplateStore` with `Load(dir)` and `Get(name)`.

### 1.3 GitHub client implementation

**Package:** `internal/github`

- Implement `Client` interface using `google/go-github/v68` and
  `ghinstallation/v2`.
- Constructor: `NewClient(appID, privateKeyPath)`.
- Installation token caching per installation ID.
- Rate-limit logging at debug level via slog.

### 1.4 Configuration

**Package:** `internal/config`

- `Config` struct with all env vars from RFC section 11.
- `Load() (*Config, error)` with validation of required fields.

### 1.5 Checker engine

**Package:** `internal/checker`

- `Engine` struct with `CheckRepo(ctx, owner, repo, installationID) error`.
- Implements full flow from RFC section 6.2.
- Handles idempotency per RFC section 9.3.

## Phase 2: Webhook Handler, Scheduler, and Work Queue

Goal: The app can receive GitHub webhooks and run scheduled reconciliation,
dispatching work through a concurrent queue.

### 2.1 Work queue

- `RepoJob` struct with Owner, Repo, InstallationID, Trigger.
- Buffered channel with configurable size.
- N goroutine workers calling `engine.CheckRepo`.
- Graceful shutdown with drain.

### 2.2 Webhook handler

- `Handler` struct implementing `http.Handler`.
- Signature validation via `github.ValidatePayload`.
- Handles `RepositoryEvent`, `InstallationRepositoriesEvent`,
  `InstallationEvent`.
- Returns 200/204/400/401 as appropriate.

### 2.3 Scheduler

- In-process ticker (Option A).
- Runs `reconcileAll` on startup and at configured interval.
- Lists all installations and repos, enqueues each.

### 2.4 Observability

- 8 Prometheus metrics per RFC section 10.1.
- Structured slog logging with consistent fields.
- Metrics endpoint at `:9090`.

## Phase 3: Application Wiring and HTTP Server

Goal: Wire everything together in `main.go` with health checks, graceful
shutdown, and a working binary.

### 3.1 Main entrypoint

- Two HTTP servers: main (`:8080`) and metrics (`:9090`).
- Health endpoints: `/healthz` (liveness), `/readyz` (readiness).
- Graceful shutdown on SIGINT/SIGTERM.

### 3.2 Dockerfile

- Multi-stage build: golang:1.25 builder + distroless runtime.
- CGO_ENABLED=0 for static binary.
- Non-root user.

## Phase 4: Deployment and Validation

Goal: Deploy to EKS dev cluster, validate end-to-end with dry-run mode.

### 4.1 Kubernetes manifests

- Kustomize base with deployment, service, configmap, serviceaccount.
- Dev overlay (DRY_RUN=true, debug logging).
- Prod overlay (DRY_RUN=false, info logging).

### 4.2 GitHub App registration

Per RFC Appendix A checklist.

### 4.3 End-to-end validation (dry-run)

Deploy with DRY_RUN=true, verify webhook processing, scheduler reconciliation,
metrics, and structured logging.

### 4.4 End-to-end validation (live)

Set DRY_RUN=false, verify actual PR creation, merge behavior, idempotency.

## Phase 5: Production Rollout

Goal: Production deployment with monitoring.

### 5.1 Production deployment

Deploy to prod EKS, point webhook URL, install on production org.

### 5.2 Monitoring and alerting

Prometheus scraping, Grafana dashboard, alerts for error rate, rate limiting,
and pod health.

### 5.3 Documentation and handoff

README, runbook, operational documentation.

## Future: Per-Repo Configuration

Out of scope for initial build. Planned: `.github/repo-guardian.yml` for
per-repo rule exclusions and overrides.

## Resolved Decisions

1. **Renovate vs Dependabot** -- Default to Dependabot only. Renovate rule
   stays defined but `Enabled: false`.
2. **CODEOWNERS default team** -- Use placeholder `@org/CHANGEME`.
3. **PR auto-merge** -- No. Always require human review.
4. **Closed-PR branch conflict** -- Delete stale branch and create fresh.
5. **Exemption mechanism** -- Not in initial scope. Future:
   `.github/repo-guardian.yml`.

## References

- [RFC-0001: Repo Compliance App](../rfc/0001-repo-compliance-app-repo-guardian.md)
