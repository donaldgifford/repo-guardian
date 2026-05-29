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
  template/   → unified text/template renderer (Renderer/Compiled, FileVars/PRVars contexts, curated helpers, ValidateZero strict-mode validator)
  webhook/    → HTTP handler for GitHub webhook events (HMAC-validated) + IP allowlist middleware + push event handler
  scheduler/  → abstract Scheduler interface (IMPL-0011 P1) + ticker/ + valkey/ (SETNX leader-election); main.go drives sweep cadence via Schedule, Sweeper.ReconcileAll is the handler
  metrics/    → Prometheus metrics (34 metrics total, most labeled with org; queue_*/store_query_seconds/scheduler_*/rate_limit_remaining/reserve_blocked added IMPL-0011 P2-P5; pr_open_with_empty_actionable_total + open_prs_by_rule added IMPL-0013 P1, with hard-coded PRAgeBucket helper)
  store/      → persistent per-repo state interface (IMPL-0011 P1) + memory/ + postgres/ (pgx/v5 + pgxpool, golang-migrate embedded SQL)
  queue/      → abstract work-queue interface (IMPL-0011 P1) + memory/ buffered-channel + valkey/ (LIST + ZSET + reaper goroutine, leader-elected via SETNX)
  worker/     → in-process worker pool consuming queue.Queue.Subscribe (IMPL-0011 P1); replaces legacy internal/checker/queue.go
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
- **PR templates (DESIGN-0013, IMPL-0012 Phase 4-5)** — operators may declare `pr { title body labels inherits }` blocks at three scopes: `defaults { pr {} }`, `rule "file" "..." { pr {} }`, and `reconcile "..." { pr {} }`. Title/Body strings are compiled into `*template.Compiled` at policy-load time by `internal/policy/pr.go.compilePolicyTemplates`; parse errors fail load with a location-prefixed message. Resolution: `ResolveRulePR(rule, defaults)` and `ResolveReconcilerPR(reconciler, defaults)` perform field-by-field merge; child wins on every explicitly-set field, unset fields inherit from parent only when `inherits=true`. `inherits=false` short-circuits parent inheritance. `labels = []` is an explicit empty-list override (tracked via sidecar `LabelsSet bool` because HCL `[]string` decode loses the absent-vs-empty-list distinction). Reconciler PRs deliberately skip `rule.pr` — they merge `reconciler.pr → defaults.pr` only. Engine integration (`internal/checker/pr.go`): single-rule PRs resolve via the rule's PR template; multi-rule bundles render every rule's title and fall back to `defaults.pr.title` (or `PRTitle` const) on conflict with an `slog.Info` enumerating the ignored titles. Bodies for multi-rule bundles always use `defaults.pr.body` only (per-rule `pr.body` is implicitly single-rule, Open Q5). Bodies > 65000 chars are truncated and a marker is appended (`<!-- truncated by repo-guardian: original length=N chars, max=65535 -->`) with an `slog.Warn`. Labels are applied to PRs via the new `Client.AddLabelsToPR` API method. Reconcilers consume the pre-resolved template via `ReconcileParams.PRTemplate`.
- **Strict template validation (IMPL-0012 Phase 5.5)** — `--strict-templates` CLI flag on `cmd/repo-guardian` and `STRICT_TEMPLATES` env var enable post-load validation via `policy.ValidatePRTemplates`. Walks every compiled PR template (defaults, per-rule, per-reconciler) and runs `tmpl.ValidateZero[tmpl.PRVars]`. Failures aggregate into a single error with location prefixes; startup fails non-zero. CLI flag default is the env var, so the flag wins on the command line for one-off CI runs. Strict mode validates PR templates only (PRVars context); file-content templates in TemplateStore are not validated because legitimate Catalog references would false-positive against zero `FileVars`.
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
- Existing `github.Client` tests use hand-written mock clients (`internal/checker/engine_test.go`, `internal/reconciler/*_test.go`). New interfaces added under DESIGN-0012 (Store, Queue, Scheduler) use generated mocks via `mockery v2` (pinned in `mise.toml`, config in `.mockery.yaml`). Regenerate via `make mocks`. Hand-written mocks for the legacy `github.Client` can stay as-is; migrating to mockery is a follow-up, not a prerequisite.
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

GoReleaser builds for linux/darwin on amd64/arm64 (CGO disabled). Releases are GPG-signed. Semantic versioning via PR labels (`major`, `minor`, `patch`, `dont-release`). One semver label is required to merge — the workflow fails without it.

**Helm chart distribution (DESIGN-0011, Approved):** `oci://ghcr.io/donaldgifford/charts/repo-guardian`, public visibility, signed with cosign keyless. The legacy chart-releaser → `gh-pages` flow is deprecated because `gh-pages` serves the mkdocs site. Chart `version` is independent of binary `appVersion`; do not auto-bump on every binary release.

**Changelog generation (IMPL-0010):** `git-cliff` (mise-managed, currently `2.12.0`) drives both the root `CHANGELOG.md` and the chart-only `charts/repo-guardian/CHANGELOG.md`. Two configs: `cliff.toml` (root) and `charts/repo-guardian/cliff.toml` (chart). The chart config is gitignored from the `.tgz` via `charts/repo-guardian/.helmignore` (build-time only). The chart `CHANGELOG.md` itself is regenerated on-the-fly by the publish workflow before `helm package`, so the published artifact always ships with a current changelog. Invocations: `git-cliff --config cliff.toml --output CHANGELOG.md` (root) and `git-cliff --config charts/repo-guardian/cliff.toml --include-path 'charts/**' --output charts/repo-guardian/CHANGELOG.md` (chart). The `--include-path` filter lives at invocation time, not in config.

**Chart publish workflow (`.github/workflows/chart-release.yml`):** OCI publish to GHCR with cosign keyless signing and SLSA Level 3 provenance. Two jobs: `publish` (checkout → setup-helm → cosign-installer → **`docker/login-action@v4` (cosign auth)** → git-cliff regenerates chart CHANGELOG → read version → idempotency check via `helm pull` → **`helm registry login` (helm auth)** → package → upload artifact on `workflow_dispatch` only → push → cosign sign → summary) and `slsa-provenance` (gated on `needs.publish.outputs.published == 'true'`, uses `slsa-framework/slsa-github-generator/.github/workflows/generator_container_slsa3.yml@v2.0.0`). Triggers: `push` to `main` scoped to `charts/**` and the workflow file, plus `workflow_dispatch` with `dry_run: boolean` input. Idempotent: re-runs against an already-published version exit early. The `helm-oci-push` ECR fan-out job is removed; ECR setup lives as a manual recipe in chart docs.

**Cosign + helm dual-login gotcha (IMPL-0010 post-mortem, PR #61):** `helm registry login` writes to `~/.config/helm/registry/config.json`. Cosign reads `~/.docker/config.json`. The two stores are disjoint, so a workflow that only does `helm registry login` will have helm pushing fine but cosign signing failing with `UNAUTHORIZED: unauthenticated`. Fix is to add `docker/login-action@v4` for cosign's auth path BEFORE the cosign step. Both logins are kept — they serve different binaries. This bit us on the 0.3.0 first publish; rolled forward to 0.3.1 with the fix. Documented yank-by-roll-forward (don't delete-and-resurrect a published version) was exercised in practice.

**GHCR new-package visibility:** First push from a workflow auto-creates the GHCR package as **private**. Consumers' anonymous `helm pull` will 401 until the maintainer flips the package visibility to **public** via the GHCR settings UI. There's no API for this flip; it's a one-time manual step per package. After flipping, subsequent publishes inherit the public visibility setting — no re-flip needed on every chart version bump.

**Chart template namespace stamping (PR #67, chart 0.3.1 → 0.3.2):** Every template in `charts/repo-guardian/templates/` MUST include `namespace: {{ .Release.Namespace }}` in its `metadata` block. Helm convention says skip this because `helm install --namespace X` sets it at apply time, but that breaks the kustomize+helm via ArgoCD consumption pattern: `helm template` doesn't write namespace into rendered manifests, kustomize's namespace transformer doesn't reliably propagate to helmCharts output, and ArgoCD then applies the rendered resources to whatever `spec.destination.namespace` is configured (often the wrong one). Symptom in the homelab: chart resources landed in `argocd` namespace while OnePasswordItems with explicit `metadata.namespace: repo-guardian` landed correctly — Deployment couldn't reach its secrets. RoleBinding subjects also need explicit `namespace:` for the same reason. Verify after any new template: `helm template ... | grep -E '^kind:|^  namespace:'` should show every kind paired with the expected namespace.

**ArgoCD `app get` NAMESPACE column is misleading.** When troubleshooting placement, `argocd app get <name>` shows a NAMESPACE column that doesn't always reflect where resources actually live. Use `kubectl get <kind> -A | grep <name>` to find the real namespace, then compare against `spec.destination.namespace` on the Application. Resources WITHOUT explicit `metadata.namespace` go to the destination namespace; resources WITH it go where they say (which is why our OnePasswordItems landed correctly while helm-templated resources didn't).

**`DRY_RUN` precedence at runtime:** env var (set on the Deployment) overrides any `dry_run` from HCL or built-in defaults via `applyEnvOverrides` running last in `Load()`. A kustomize patch or Helm values setting `DRY_RUN=true` will silently keep the engine in dry-run regardless of the policy file. Always check `kubectl get deploy ... -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="DRY_RUN")]}'` first when the binary appears to be reading the wrong policy state.

**Idempotent file commits on the reconcile branch (INV-0003, appVersion 1.4.1):** `internal/github/client.go.CreateOrUpdateFile` does GET-on-target-branch first, then either skips (identical content), updates with the existing blob `sha` (different content), or creates fresh (file missing). Before this fix the wrapper called `Repositories.CreateFile` unconditionally, so a second reconcile against a repo with an open `repo-guardian/add-missing-files` branch would 422 with "sha wasn't supplied." If you ever rename or rewrite this function, preserve the three-branch behavior — the package-level `GetContents(ctx, owner, repo, path)` helper is *default-branch-only* and will not protect you against the same bug if you swap to it.

**IMPL-0013 Phase 4 — sticky reconcile-log PR comment.** `upsertReconcileLog` posts a markdown table comment with per-rule status (`satisfied on main` / `still actionable` / `orphan removed from branch`) on every reconcile that finds an existing repo-guardian PR. Skips the upsert when the rendered body matches the existing marker-tagged comment (idempotent on no-op sweeps). Wired into three call sites: (1) `createOrUpdatePRFromPolicy` after orphan cleanup + body refresh; (2) `autoClosePR` as the final close-comment with an "Auto-closing" footer (same marker — the comment persists on the closed PR); (3) the AutoClosePR=false convergent branch so operators see why the PR is intentionally stuck. The rendered body is inline-built via `renderReconcileLog` (markdown table + RFC3339 timestamp), not routed through `internal/template/` — there's no operator-tunable surface for this comment today and the renderer's compile machinery would be overkill for a < 1KB markdown blob. `buildReconcileLogEvents` joins `allRules`, `actionable`, and `removedOrphans` into a flat `[]reconcileLogEvent` for the table; orphans get their own bucket so the cleanup action is visible in the log. The marker `<!-- repo-guardian:reconcile-log:v1 -->` is shared with the close path — there's exactly one sticky reconcile comment per PR, and it gets edited in place across the PR's lifetime.

**IMPL-0013 Phase 3 — convergence fix in `checkRepoWithPolicy`.** The empty-actionable+open-PR branch now optionally auto-closes the PR via `autoClosePR` in `internal/checker/drift.go` (UpsertPRComment final close-comment → ClosePullRequest → DeleteBranch; each step is best-effort, ClosePullRequest is the only hard failure). The non-empty-actionable+existing-PR branch in `createOrUpdatePRFromPolicy` discovers orphans (rules with a file on the reconcile branch but no longer in `actionable`) via `discoverOrphans` + `GetContentsOnBranch`, deletes them via `cleanupOrphans`, then calls `refreshPolicyPR` to PATCH the title/body with the current actionable set. `auto_close_pr` HCL knob on `guardian {}` (default true) and `AUTO_CLOSE_PR` env var override gate the close behavior. New metrics: `PRsClosedTotal{org, reason="satisfied"}` and `PROrphanLeftTotal{org}`. Fail-safe semantics: `GetContentsOnBranch` errors are treated as "still actionable" (rule omitted from orphan list — never delete under transient API glitch); `DeleteFile` partial failures log Warn + increment `PROrphanLeftTotal` + continue (next sweep retries). `Engine.createOrUpdatePRFromPolicy` was refactored to extract `syncActionableFiles` to keep gocyclo under the 15 threshold. Convergence multi-sweep tests in `internal/checker/convergence_test.go` exercise sweep1→PR-opened, sweep2→orphan-cleanup+body-refresh, sweep3→auto-close, sweep3-alt→auto-close-disabled, GetContentsOnBranch-error→fail-safe, DeleteFile-error→counter-increments. A sixth `github.Client` method `GetContentsOnBranch(ctx, owner, repo, path, branch) (sha string, exists bool, err error)` joins the Phase-2 surface; mockClient stubs added in all three sites. `applyEnvBoolPtr` helper handles `*bool` env overrides without losing the "unset vs explicitly-false" distinction. `AutoClosePREnabled()` method on `GuardianConfig` returns the default-true semantics.

**IMPL-0013 Phase 2 — five new `github.Client` methods.** `DeleteFile` (Contents API + sha for optimistic concurrency, used by orphan cleanup), `UpdatePullRequest` (PATCH title/body only, leaves state untouched), `ClosePullRequest` (PATCH `state=closed`, separate from update so the two never accidentally co-occur), `ListPRComments` (paginated `Issues.ListComments` — PR comments are issue comments under the hood; returns a thin `*Comment{ID, Body}`), and `UpsertPRComment(ctx, owner, repo, number, marker, body)`. **Marker convention for sticky comments:** the marker line lives on row 1 of the comment body in the format `<!-- repo-guardian:reconcile-log:v1 -->` — the `v1` suffix lets us evolve the comment schema without breaking the upsert match. `UpsertPRComment` lists, scans for `strings.HasPrefix(body, marker)`, and either `Issues.EditComment` or `Issues.CreateComment` accordingly. Future reconcilers that want their own sticky comments MUST use the same upsert API with their own versioned marker (e.g., `<!-- repo-guardian:label-sync:v1 -->`) so future tooling can identify and operate on repo-guardian comments without false-matching humans. The marker must be the LITERAL first line of the comment body; do not pad or interleave.

**`mockClient` parity contract:** every package that constructs a `*mockClient` for `github.Client` substitution must implement the full interface. The five new methods need no-op stubs in `internal/checker/engine_test.go`, `internal/scheduler/sweep_test.go`, and `internal/reconciler/custom_properties_test.go` (the last is embedded by `bpMockClient` and `labelMockClient`, so one stub covers all reconciler tests). `internal/checker/sweep_test.go` uses `fakeRateLimit` not `mockClient` and does NOT need stubs. Forget any one of these three files and the compiler will tell you with a `cannot use *mockClient as Client value: missing method X` error in the consuming `_test.go`.

**Chart 0.6.0 / appVersion 1.7.0 (IMPL-0013 Phases 3-5):** behavioural-change release. Auto-close enabled by default for PRs whose every file rule is satisfied on the default branch (the INV-0005 drift fix). Opt-out via `policy.autoClosePR: false` in chart values or `AUTO_CLOSE_PR=false` env on Deployment (env wins). Sticky reconcile-log comment posts on every reconcile that finds an existing repo-guardian PR; marker `<!-- repo-guardian:reconcile-log:v1 -->` on row 1. Two new metrics surfaced (`prs_closed_total{org, reason}`, `pr_orphan_left_total{org}`) and two new alerts (`RepoGuardianStaleOpenPRs`, `RepoGuardianPRDrift`). Operator runbook at `docs/operations/pr-convergence-migration.md`. Chart README regenerated from `README.md.gotmpl` — DO NOT edit the rendered README directly per the IMPL-0012 post-mortem.

**Chart 0.5.1 / appVersion 1.6.1 (IMPL-0013 P1):** patch bump shipping diagnostic-only PR-drift observability. Two new metrics: `pr_open_with_empty_actionable_total{org}` (CounterVec, increments inside `checkRepoWithPolicy` when an open repo-guardian PR has no actionable rule — the INV-0005 drift surface) and `open_prs_by_rule{org, rule, age_bucket}` (GaugeVec, populated per-sweep by joining the open-PR snapshot against the actionable set; reset to zero at sweep start by `metrics.ResetOpenPRsByRule` called from BOTH sweep paths). Age buckets are hard-coded (`<1d`, `1-7d`, `7-30d`, `30d+`) via `metrics.PRAgeBucket(ageDays float64) string`; tuning would explode label cardinality and was deliberately not exposed (IMPL-0013 Q8). `PullRequest` struct grew a `CreatedAt time.Time` field populated from `pr.GetCreatedAt().Time`; the two `&PullRequest{}` literal call sites in `internal/github/client.go` MUST set it or the gauge collapses to the `<1d` fallback. Two new starter alerts in `prometheusrule.yaml`: `RepoGuardianStaleOpenPRs` (gauge[age_bucket="30d+"] > 0 for 1h, warning) and `RepoGuardianPRDrift` (rate(drift counter)[1h] > 0 for 30m, warning). No behavioural change — Phase 3 lands the convergence fix in a separate PR sequence.

**Chart 0.5.0 / appVersion 1.6.0 (IMPL-0011):** new optional backend shapes for multi-replica deployments. `store.backend=postgres` (with `store.postgres.mode=baked|cnpg|external`) wires the persistent state store; `queue.backend=valkey` (with `queue.valkey.mode=baked|external`) wires the durable queue; `scheduler.backend=valkey` enables SETNX leader election. New chart resources: `store-postgres.yaml` (StatefulSet+Service), `store-postgres-secret.yaml`, `store-cnpg-cluster.yaml`, `store-cnpg-pooler.yaml`, `queue-valkey.yaml`, `queue-valkey-secret.yaml`, `prometheusrule.yaml` (5 starter alerts). Defaults preserved: `store.backend=memory`, `queue.backend=memory`, `scheduler.backend=ticker` — single-replica behaviour unchanged. `terminationGracePeriodSeconds` raised to 60s. Helm-unittest cases for the four shapes in `tests/backend_shapes_test.yaml`.

**Webhook 202 Accepted contract (IMPL-0011 P5):** `internal/webhook/handler.go` enqueues to `Queue.Enqueue` and returns `202 Accepted` with no body — the engine call is no longer inline. Tests assert `http.StatusAccepted` (not `http.StatusOK`); any new webhook test must follow the same convention. The pre-IMPL-0011 `200 OK` response is gone; consumers reading the response code (e.g., GitHub's webhook delivery view) will see 202 going forward.

**Multi-backend contract test convention (IMPL-0011 P4):** when a behavior is implemented by multiple backends (Scheduler/Queue/Store), the contract suite lives in the package's `_test.go` (`internal/scheduler/contract_test.go`) and exercises the in-memory/ticker backend by default. The durable backend's `_integration_test.go` re-runs the same sub-test bodies under the `integration` build tag — **inlined**, not imported, because the contract helper is in the test-only `package scheduler_test`. Factories take `*testing.T` so per-test cleanup (testcontainer teardown, etc.) registers via `t.Cleanup`. This is the template for any future durable-backend contract tests.

**`StaleSweeper` replaces per-tick repo enumeration (IMPL-0011 P5):** `internal/checker/sweep.go` implements the post-Phase-5 sweep handler: query `Store.StaleRepos(freshness, currentPolicyVersion, batch)`, gate on per-installation rate-limit reserve via `Client.RateLimitRemaining`, and `Queue.Enqueue` each candidate. The legacy "iterate every installation, list every repo, push to channel" pattern is gone. `policy.Version()` produces a stable hash of the loaded policy (used as the freshness gate) and is the only consumer of the policy-version state; if you change the policy struct, validate this hash still discriminates intended changes from cosmetic ones. The sweep handler is wired in via `Scheduler.Schedule(name="sweep", interval=...)` from `cmd/repo-guardian/main.go`.

**Chart 0.4.0 / appVersion 1.5.0 (IMPL-0012):** breaking chart values change — legacy `templates.codeowners`/`dependabot`/`renovate` slots removed in favor of `templates.files: <name>: <content>` map. New keys: `templates.existingConfigMap`, `templating.vars`, `templating.strict`. Embedded `.tmpl` files were rewritten from `OWNER_VALUE`-style to dotted-path Go template syntax; GHA `${{ ... }}` expressions in any custom template must be wrapped in a backtick-raw-string action. PR templates (title/body/labels) configurable at three HCL scopes (`defaults.pr`, `rule.pr`, `reconcile.pr`) with field-by-field inheritance and `inherits=false` short-circuit. Migration recipe in `docs/operations/template-migration.md`; homelab smoke runbook in `charts/repo-guardian/docs/homelab-smoke.md`.

**`PRConfig` vs `PRTemplate` in `internal/policy` (IMPL-0012):** these are deliberately different shapes and easy to confuse. `PRConfig` is the *raw HCL block* — `Title`/`Body` are `*string` (presence-distinguished), `Labels` carries a sidecar `LabelsSet bool`, and `CompiledTitle`/`CompiledBody` cache the post-load parse output. `PRTemplate` is the *resolved* output of `ResolveRulePR` / `ResolveReconcilerPR` — `Title`/`Body` are `*tmpl.Compiled` ready for `Render(*PRVars)`. Engine and reconciler call sites consume `PRTemplate`, not `PRConfig`. The `compilePolicyTemplates` pass populates the `Compiled*` fields on `PRConfig` at load time; resolution lifts those into a fresh `PRTemplate` via `asTemplate`.

**Helm chart README is generated from `README.md.gotmpl`** — `make helm-docs` (and the chart-publish workflow) regenerates `charts/repo-guardian/README.md` from the template. Adding handwritten content directly to `README.md` is silently stripped on the next regeneration. Session post-mortem: the IMPL-0012 "Security considerations" section was first added to `README.md` and lasted exactly one `make helm-docs` invocation before disappearing. New static content must go into `README.md.gotmpl`. Backtick-escape rule still applies inside `.gotmpl` for literal `{{ env "VAR" }}` examples in markdown code blocks.

**`docz update <type>` regenerates indices AND TOCs** — running `docz update impl design` rewrites the section README tables based on each doc's frontmatter `status:` field and also incidentally regenerates the `<!--toc:start-->` blocks inside any doc whose body has drifted from its TOC. Watch the diff and don't be surprised when an unrelated DESIGN gets a TOC bump.

**`Renderer.Raw` is gone (post-Phase 3)** — the Phase 2 raw-passthrough mechanism for GHA `${{ ... }}` templates was replaced in Phase 3 with the backtick-escape rewrite. The `Compiled` struct holds only `tpl *texttemplate.Template`; there is no `raw` field, no `Raw()` constructor on `Renderer`, and `Render` does not have a non-template fallback path. Any future templates that need to emit literal `{{` must use the same `{{`+backtick+...+backtick+`}}` wrapper.

**CI paths-filter convention (PR #77):** `.github/workflows/ci.yml` runs a `changes` setup job using `dorny/paths-filter@v3` that emits boolean outputs for `go`, `docker`, `helm`, `workflows`. Every downstream job MUST declare `needs: changes` and an `if:` gate against the relevant outputs — otherwise the docs-only-skip benefit silently regresses. Reference matrix: `lint` runs on `go || helm || workflows`; Go-only jobs (`test-go`, `security`, `build`) run on `go || workflows`; `docker-build` on `go || docker || workflows`; helm jobs (`helm-unittest`, `helm-test`) on `helm || workflows`. `labeler` is the only exception — it runs unconditionally because labeling is cheap and informs every PR. Skipped jobs gated by `if:` on the job (not via workflow-level `paths:`) satisfy branch-protection required checks; no protection changes needed when adding new gated jobs.

**Helm CLI semantics drift in CI (PR #76):** two gotchas bit the helm-unittest job when migrating off the third-party `d3adb5/helm-unittest-action@v2`. (1) `helm plugin install --version "X.Y.Z"` on helm 3.20+ triggers verify mode; the helm-unittest source has no provenance data, so install fails with "plugin source does not support verification." Pass `--verify=false` explicitly, or omit `--version` to install latest (which skips the verify path). (2) `helm unittest --color` is a boolean flag on older plugin versions and an enum (`never|auto|always`) on newer ones — passing `--color charts/foo` on the newer CLI consumes the chart path as the enum value. Easiest fix: omit `--color`, since GitHub Actions terminals enable color via env detection. Both fixes live in the helm-unittest job in `.github/workflows/ci.yml`; if you ever swap that job back to a packaged action, re-verify these behaviors against whatever helm version it pins.

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
