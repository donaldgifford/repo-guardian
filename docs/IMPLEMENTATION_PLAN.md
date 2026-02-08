# Implementation Plan

Detailed phased implementation plan for repo-guardian, derived from [RFC.md](./RFC.md).

---

## Phase 1: Core Foundation

Goal: Buildable, testable core with GitHub client, rule registry, and checker engine — all backed by unit tests against mocked interfaces.

### 1.1 Define interfaces and types ✅

**Package:** `internal/github`

- Define a `Client` interface abstracting the GitHub operations the app needs:
  - `GetContents(ctx, owner, repo, path) (bool, error)` — check if a file exists
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
- Keep this interface narrow — only what the checker engine and scheduler actually call.
- This interface is the primary mock boundary for all unit tests.

**Acceptance criteria:**
- Interface compiles, is documented with godoc comments.
- No concrete implementation yet (that comes in 1.3).

### 1.2 FileRule registry ✅

**Package:** `internal/rules`

- Define `FileRule` struct per RFC section 6.1 (Name, Paths, PRSearchTerms, DefaultTemplateName, TargetPath, Enabled).
- Define `Registry` type holding `[]FileRule` with methods:
  - `EnabledRules() []FileRule` — returns only rules where `Enabled == true`.
  - `RuleByName(name string) (FileRule, bool)` — lookup by name.
- Define `DefaultRules` variable with three initial rules: CODEOWNERS (enabled), Dependabot (enabled), Renovate (disabled — present for future use but off by default).
- Embed fallback templates via `//go:embed templates/*.tmpl` in a `TemplateStore` type.
- `TemplateStore` loads templates from a directory (for ConfigMap mount) with embedded fallbacks.
  - `Load(dir string) error` — reads `*.tmpl` files from dir, falls back to embedded.
  - `Get(name string) (string, error)` — returns template content by name.

**Tests (`internal/rules/registry_test.go`):**
- `TestDefaultRulesCount` — verify 3 default rules exist (2 enabled, 1 disabled).
- `TestEnabledRulesFiltering` — confirm Renovate excluded from enabled list, CODEOWNERS and Dependabot included.
- `TestRuleByName` — lookup existing and missing.
- `TestTemplateStoreEmbeddedFallback` — load with empty dir, get embedded templates.
- `TestTemplateStoreDirectoryOverride` — write a temp dir with overrides, confirm override wins.

**Acceptance criteria:**
- `make lint` and `make test` pass.
- Templates for codeowners, dependabot, renovate exist as `.tmpl` files.

### 1.3 GitHub client implementation ✅

**Package:** `internal/github`

- Implement the `Client` interface using `google/go-github/v68` and `ghinstallation/v2`.
- Constructor: `NewClient(appID int64, privateKeyPath string) (*GitHubClient, error)`.
- Use `ghinstallation.NewAppsTransport` for app-level JWT.
- Use `ghinstallation.New` for per-installation tokens, cached per installation ID.
- All methods accept `context.Context` for cancellation/timeouts.
- Implement rate-limit logging: after each API call, log `X-RateLimit-Remaining` at debug level via slog.

**Dependencies to add to go.mod:**
- `github.com/google/go-github/v68`
- `github.com/bradleyfalzon/ghinstallation/v2`

**Tests (`internal/github/client_test.go`):**
- Use `httptest.Server` to mock GitHub API responses.
- `TestGetContents_Exists` / `TestGetContents_NotFound` — verify file existence check.
- `TestListOpenPullRequests` — verify PR listing and parsing.
- `TestCreatePullRequest` — verify request body sent to GitHub.

**Acceptance criteria:**
- Concrete client satisfies the `Client` interface.
- Tests pass against httptest mocks.

### 1.4 Configuration ✅

**Package:** `internal/config`

- Define `Config` struct with all env vars from RFC section 11.
- Parse from environment using stdlib `os.Getenv` with defaults.
- `Load() (*Config, error)` — validates required fields (GITHUB_APP_ID, GITHUB_PRIVATE_KEY_PATH, GITHUB_WEBHOOK_SECRET), returns error if missing.
- Expose `Config.Validate() error` for explicit validation.

**Tests (`internal/config/config_test.go`):**
- `TestLoadDefaults` — confirm default values for optional fields.
- `TestLoadRequired_Missing` — confirm error when required env vars are absent.
- `TestLoadOverrides` — set all env vars, confirm parsing.

**Acceptance criteria:**
- All 13 env vars from RFC are parsed.
- `make test` passes.

### 1.5 Checker engine ✅

**Package:** `internal/checker`

- `Engine` struct takes the `github.Client` interface, `rules.Registry`, and `rules.TemplateStore`.
- `CheckRepo(ctx, owner, repo, installationID) error` — core method implementing the flow from RFC section 6.2:
  1. Skip if archived or fork (per config).
  2. Get default branch.
  3. For each enabled rule: check file existence across all paths, then search open PRs for search terms.
  4. Collect missing files.
  5. If missing files: create/update branch, commit all files, create/update PR.
- `buildPRBody(missing []FileRule) string` — generate PR body per RFC section 9.2.
- Handle idempotency per RFC section 9.3 table:
  - Detect existing `repo-guardian/add-missing-files` branch.
  - Detect existing open PR from this app — update via force-push if new missing files found.
  - If branch exists but PR was closed (not merged), delete the stale branch and create fresh.

**Tests (`internal/checker/engine_test.go`):**
- Use a mock `github.Client` (generate with mockery or hand-write).
- `TestCheckRepo_AllFilesExist` — no action taken.
- `TestCheckRepo_MissingFiles_NoPR` — creates branch and PR.
- `TestCheckRepo_MissingFiles_ExistingPR` — updates existing PR.
- `TestCheckRepo_MissingFiles_ThirdPartyPR` — skips rules with existing third-party PRs.
- `TestCheckRepo_Archived` — skips entirely.
- `TestCheckRepo_Fork` — skips when SKIP_FORKS=true.
- `TestCheckRepo_EmptyRepo` — skips, logs warning.
- `TestBuildPRBody` — verify generated markdown matches expected format.

**Acceptance criteria:**
- All idempotency scenarios from RFC section 9.3 are covered by tests.
- `make ci` passes.

---

## Phase 2: Webhook Handler, Scheduler, and Work Queue

Goal: The app can receive GitHub webhooks and run scheduled reconciliation, dispatching work through a concurrent queue.

### 2.1 Work queue ✅

**Package:** `internal/checker`

- `RepoJob` struct: Owner, Repo, InstallationID, Trigger (webhook/scheduler/manual).
- `Queue` struct with buffered channel, configurable size.
  - `Enqueue(job RepoJob) error` — non-blocking send, returns error if queue full.
  - `Start(ctx context.Context, workers int, engine *Engine)` — launches N goroutines pulling from channel, calling `engine.CheckRepo`.
  - `Stop()` — drains queue, waits for in-flight workers.
- Workers log each job start/finish with slog, including duration.

**Tests (`internal/checker/queue_test.go`):**
- `TestEnqueue_Success` — enqueue under capacity.
- `TestEnqueue_Full` — enqueue when buffer is full, verify error.
- `TestWorkers_ProcessJobs` — enqueue multiple jobs, verify all processed.
- `TestStop_DrainsGracefully` — stop with in-flight work, verify completion.

**Acceptance criteria:**
- Queue handles concurrent access safely (race detector passes).
- Graceful shutdown works.

### 2.2 Webhook handler ✅

**Package:** `internal/webhook`

- `Handler` struct takes webhook secret, work queue reference.
- `ServeHTTP(w, r)` implements `http.Handler`.
  - Validate payload signature via `github.ValidatePayload(r, secret)`.
  - Parse event type via `github.ParseWebHook`.
  - Handle three event types per RFC section 6.3:
    - `RepositoryEvent` (action=created) — enqueue single repo.
    - `InstallationRepositoriesEvent` (action=added) — enqueue all added repos.
    - `InstallationEvent` (action=created) — enqueue all repos in installation.
  - Return 200 for handled events, 204 for ignored events, 400/401 for invalid payloads.
- Register at route: `POST /webhooks/github`.

**Tests (`internal/webhook/handler_test.go`):**
- `TestHandleWebhook_RepositoryCreated` — verify repo enqueued.
- `TestHandleWebhook_InstallationReposAdded` — verify all added repos enqueued.
- `TestHandleWebhook_InstallationCreated` — verify all repos enqueued.
- `TestHandleWebhook_InvalidSignature` — verify 401 response.
- `TestHandleWebhook_UnsupportedEvent` — verify 204 response, nothing enqueued.
- `TestHandleWebhook_IgnoredAction` — e.g., repository "deleted" action, verify no enqueue.

**Dependencies to add to go.mod:**
- None new — `go-github` already handles webhook parsing.

**Acceptance criteria:**
- Signature validation rejects bad payloads.
- All three webhook event types correctly enqueue jobs.

### 2.3 Scheduler ✅

**Package:** `internal/scheduler`

- `Scheduler` struct takes `github.Client`, work queue, schedule interval.
- `Start(ctx context.Context)` — in-process ticker (Option A from RFC).
  - Run `reconcileAll` immediately on startup.
  - Tick at configured interval (default 168h / 1 week).
  - Respect context cancellation for graceful shutdown.
- `reconcileAll(ctx context.Context) error`:
  - List all installations.
  - For each installation, list all repos.
  - Skip archived/fork repos per config.
  - Enqueue each repo as a `scheduler` trigger job.
  - Log total repos enqueued, duration.

**Tests (`internal/scheduler/scheduler_test.go`):**
- `TestReconcileAll` — mock client returns 2 installations with 3 repos each, verify 6 jobs enqueued.
- `TestReconcileAll_SkipsArchived` — verify archived repos not enqueued.
- `TestStart_RunsOnStartup` — start scheduler, verify reconcileAll called immediately.
- `TestStart_RespectsContextCancellation` — cancel context, verify scheduler stops.

**Acceptance criteria:**
- Scheduler runs reconciliation on startup and on interval.
- Graceful shutdown via context cancellation.

### 2.4 Observability ✅

**Packages:** Integrated across all packages, metrics registered in `cmd/repo-guardian/main.go`.

- Add `prometheus/client_golang` dependency.
- Define all 8 metrics from RFC section 10.1:
  - `repo_guardian_repos_checked_total` (counter, label: trigger)
  - `repo_guardian_prs_created_total` (counter)
  - `repo_guardian_prs_updated_total` (counter)
  - `repo_guardian_files_missing_total` (counter, label: rule_name)
  - `repo_guardian_check_duration_seconds` (histogram)
  - `repo_guardian_webhook_received_total` (counter, label: event_type)
  - `repo_guardian_errors_total` (counter, label: operation)
  - `repo_guardian_github_rate_remaining` (gauge)
- Instrument checker engine, webhook handler, and GitHub client with these metrics.
- Add slog structured logging throughout:
  - Log fields: `repo`, `installation_id`, `trigger`, `rule`, `action`.
  - JSON format in production, text format when `LOG_LEVEL=debug`.
- Expose metrics at `METRICS_ADDR` (default `:9090`) via `promhttp.Handler()`.

**Acceptance criteria:**
- `/metrics` endpoint returns Prometheus-format metrics.
- All 8 metrics are registered and incremented at the correct points.
- Structured log output includes consistent fields.

---

## Phase 3: Application Wiring and HTTP Server

Goal: Wire everything together in `main.go` with health checks, graceful shutdown, and a working binary.

### 3.1 Main entrypoint ✅

**File:** `cmd/repo-guardian/main.go`

- Load config via `config.Load()`.
- Initialize slog logger (JSON handler, configurable level).
- Initialize GitHub client.
- Initialize rule registry and template store.
- Initialize checker engine.
- Initialize work queue, start workers.
- Initialize webhook handler.
- Initialize scheduler.
- Set up two HTTP servers:
  - **Main server** (LISTEN_ADDR, default `:8080`):
    - `POST /webhooks/github` — webhook handler.
    - `GET /healthz` — liveness probe (always 200).
    - `GET /readyz` — readiness probe (200 when queue is accepting, 503 otherwise).
  - **Metrics server** (METRICS_ADDR, default `:9090`):
    - `GET /metrics` — Prometheus metrics.
- Graceful shutdown:
  - Listen for SIGINT/SIGTERM.
  - Stop accepting new webhooks.
  - Cancel scheduler context.
  - Drain work queue.
  - Shut down HTTP servers with timeout.

**Acceptance criteria:**
- `make build` produces a working binary.
- `make run-local` starts the server (with DRY_RUN=true for safe local testing).
- Health endpoints respond correctly.
- SIGTERM triggers clean shutdown.

### 3.2 Dockerfile

**File:** `Dockerfile`

- Multi-stage build:
  - **Builder stage:** `golang:1.25` base, copy source, `go build -o /repo-guardian ./cmd/repo-guardian`.
  - **Runtime stage:** `gcr.io/distroless/static-debian12`, copy binary from builder.
- Set `CGO_ENABLED=0` for static binary.
- Expose ports 8080 and 9090.
- Run as non-root user.

**Acceptance criteria:**
- `docker build` succeeds.
- Container starts and responds on health endpoint.
- Image size is minimal (distroless base).

---

## Phase 4: Deployment and Validation

Goal: Deploy to EKS dev cluster, validate end-to-end with dry-run mode.

### 4.1 Kubernetes manifests

**Directory:** `deploy/`

- Create Kustomize base (`deploy/base/`):
  - `deployment.yaml` — per RFC section 7.1 (single replica, resource limits, probes, volume mounts).
  - `service.yaml` — ClusterIP service exposing ports 80→8080, 9090→9090.
  - `configmap.yaml` — `repo-guardian-templates` with default file templates.
  - `kustomization.yaml` — ties it all together.
- Create dev overlay (`deploy/overlays/dev/`):
  - Patch for dev-specific values (image tag, DRY_RUN=true, reduced schedule interval for faster testing).
  - Ingress resource for dev domain.
- Create prod overlay (`deploy/overlays/prod/`):
  - Production image tag, DRY_RUN=false, production domain.

**Acceptance criteria:**
- `kubectl apply -k deploy/overlays/dev/` succeeds against a cluster.
- Pod starts, passes health checks.

### 4.2 GitHub App registration

Per the checklist in RFC Appendix A:
- Register GitHub App in org settings.
- Configure webhook URL to point to dev cluster ingress.
- Generate and store webhook secret in K8s secret.
- Set permissions: Contents (R/W), Pull Requests (R/W), Metadata (R).
- Subscribe to events: `repository`, `installation_repositories`, `installation`.
- Generate private key, store in K8s secret.
- Install app on a test org or subset of repos.

### 4.3 End-to-end validation (dry-run)

- Deploy to dev with `DRY_RUN=true`.
- Create a test repo in the test org — verify webhook received, checker runs, logs show what PR *would* be created.
- Trigger manual reconciliation — verify scheduler processes all repos.
- Check Prometheus metrics are populated.
- Review logs for correct structured output.

### 4.4 End-to-end validation (live)

- Set `DRY_RUN=false` on dev.
- Create a test repo — verify PR is actually created with correct files and body.
- Merge the PR, create another repo — verify new PR appears.
- Close the PR without merging, trigger reconciliation — verify behavior per RFC section 9.3 (open question #4).
- Add a file manually before the app runs — verify the app skips that rule.

---

## Phase 5: Production Rollout

Goal: Production deployment with monitoring.

### 5.1 Production deployment

- Deploy to prod EKS cluster via `deploy/overlays/prod/`.
- Point GitHub App webhook URL to production ingress.
- Install app on production org (start with a subset of repos if preferred).
- Run first reconciliation with `DRY_RUN=true` to preview scope.
- Disable dry-run once validated.

### 5.2 Monitoring and alerting

- Confirm Prometheus is scraping the metrics endpoint.
- Set up Grafana dashboard with:
  - Repos checked over time (by trigger).
  - PRs created/updated over time.
  - Error rate.
  - GitHub rate limit remaining.
  - Queue depth / processing latency.
- Set up alerts for:
  - `repo_guardian_errors_total` rate exceeding threshold.
  - `repo_guardian_github_rate_remaining` dropping below 100.
  - Pod not ready for >5 minutes.

### 5.3 Documentation and handoff

- Update `README.md` with operational documentation.
- Write runbook covering:
  - How to add a new FileRule.
  - How to update default templates.
  - How to debug webhook delivery issues.
  - How to manually trigger reconciliation.
  - Common failure modes and remediation.

---

## Future: Per-Repo Configuration (`.github/repo-guardian.yml`)

Out of scope for the initial build, but the planned next feature after production rollout.

Repos could opt into per-repo configuration via `.github/repo-guardian.yml`:

```yaml
# .github/repo-guardian.yml
exclude:
  - renovate    # Don't create Renovate config for this repo
  - dependabot  # Don't create Dependabot config for this repo
```

**Design notes:**
- The checker engine would check for this file before evaluating rules.
- Rule names in `exclude` match `FileRule.Name` (case-insensitive).
- An empty file or missing file means "enforce all enabled rules" (current behavior).
- This keeps exception tracking in-repo and version-controlled rather than in a central allowlist.
- Could later expand to support overrides (e.g., custom template per repo, custom target paths).

---

## Resolved Decisions

Answers to the open questions from RFC section 16:

1. **Renovate vs Dependabot defaults** — Default to Dependabot only for the initial build. Easier to test since it's GitHub-native. Renovate rule stays defined but `Enabled: false`. Can be flipped on later.
2. **CODEOWNERS default team** — Use a placeholder (`@org/CHANGEME`) in the template. The PR body should call this out explicitly so reviewers know to replace it before merging.
3. **PR auto-merge** — No. Always require human review. These are sensible defaults, not final configs.
4. **Closed-PR branch conflict** — Delete the stale branch and create a fresh one. Avoids stale state and keeps the checker engine logic simple (no diffing old vs new).
5. **Exemption mechanism** — Not in scope for the initial build. Future feature: support a `.github/repo-guardian.yml` file for per-repo configuration (rule excludes/ignores, overrides). This gives repos a way to track exceptions in-repo rather than maintaining a central allowlist. Track as a Phase 5+ item.
