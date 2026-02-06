# RFC: Repo Compliance App (repo-guardian)

| Field          | Value                                      |
| -------------- | ------------------------------------------ |
| **Status**     | Draft                                      |
| **Author**     | Donald                                     |
| **Created**    | 2026-02-06                                 |
| **Target**     | EKS (Kubernetes)                           |
| **Language**   | Go                                         |
| **Integration**| GitHub App (OAuth / Webhook)               |

---

## 1. Problem Statement

Across a large GitHub organization with hundreds (or thousands) of repositories, there is no automated enforcement ensuring that newly created or onboarded repositories contain baseline configuration files such as `CODEOWNERS`, Dependabot configuration, and Renovate configuration. Engineers frequently create repos and forget to add these files, leading to inconsistent dependency management, unclear ownership, and compliance drift.

Manual enforcement does not scale. We need an automated system that detects missing files and creates pull requests to add sensible defaults, while being easily extensible to enforce additional files in the future.

---

## 2. Goals

1. Automatically detect when a repository is **created** or **newly added** (i.e., the app is installed on a repo it hasn't processed before) within a GitHub org or set of repos.
2. Run a **scheduled weekly reconciliation** across all installed repositories.
3. For each repo, check for the existence of a configurable set of required files.
4. If a file is missing, check whether an **open PR already exists** to add it before taking action.
5. If no file and no open PR exist, create a **branch with the missing file(s)** and open a PR.
6. Design the file-check system to be **easily extensible** — adding a new required file should require minimal code changes.

---

## 3. Non-Goals

- Enforcing file *content* beyond providing sensible defaults (no linting/validation of existing files).
- Blocking merges or acting as a required status check (this is additive, not gatekeeping).
- Managing GitHub App installation lifecycle (admins install manually; the app reacts).
- Multi-GitHub-Enterprise support (single GitHub instance target, extendable later).

---

## 4. Architecture Overview

```
┌──────────────────────────────────────────────────────────┐
│                        EKS Cluster                       │
│                                                          │
│  ┌────────────────────────────────────────────────────┐  │
│  │              repo-guardian Deployment              │  │
│  │                                                    │  │
│  │  ┌──────────────┐  ┌────────────┐  ┌────────────┐  │  │
│  │  │  Webhook     │  │  Scheduler │  │  Checker   │  │  │
│  │  │  Handler     │  │  (CronJob  │  │  Engine    │  │  │
│  │  │  (HTTP)      │  │   or tick) │  │            │  │  │
│  │  └──────┬───────┘  └─────┬──────┘  └─────┬──────┘  │  │
│  │         │                │               │         │  │
│  │         └────────────────┴───────────────┘         │  │
│  │                       │                            │  │
│  │              ┌────────▼────────┐                   │  │
│  │              │  File Rule      │                   │  │
│  │              │  Registry       │                   │  │
│  │              │  (Extensible)   │                   │  │
│  │              └────────┬────────┘                   │  │
│  │                       │                            │  │
│  │              ┌────────▼────────┐                   │  │
│  │              │  GitHub API     │                   │  │
│  │              │  Client         │                   │  │
│  │              └─────────────────┘                   │  │
│  └────────────────────────────────────────────────────┘  │
│                                                          │
│  ┌──────────────────┐                                    │
│  │  K8s Secret      │  ← GitHub App private key,         │
│  │                  │    app ID, webhook secret          │
│  └──────────────────┘                                    │
│                                                          │
│  ┌──────────────────┐                                    │
│  │  ConfigMap       │  ← Default file templates,         │
│  │                  │    rule configuration              │
│  └──────────────────┘                                    │
└──────────────────────────────────────────────────────────┘
```

### Components

| Component | Responsibility |
| --- | --- |
| **Webhook Handler** | Receives GitHub webhook events (`installation_repositories`, `repository`), validates signatures, enqueues repo checks. |
| **Scheduler** | Triggers a full reconciliation of all installed repos on a weekly cadence. Implemented as either an in-process ticker or a Kubernetes CronJob that hits an internal endpoint. |
| **Checker Engine** | Core logic: given a repo, iterate through the file rule registry, check for existence, check for open PRs, and create branches/PRs for missing files. |
| **File Rule Registry** | A pluggable list of "rules" — each rule defines what file(s) to look for, alternate paths, and a default template. Adding a new rule = adding a new struct instance. |
| **GitHub API Client** | Thin wrapper around `google/go-github` for repo contents, branches, commits, and PRs. Uses GitHub App installation tokens. |

---

## 5. GitHub App Configuration

### 5.1 Authentication

The app uses the standard **GitHub App** authentication flow (not OAuth user tokens):

1. **App-level JWT**: Signed with the app's RSA private key, used to list installations and generate installation tokens.
2. **Installation Access Token**: Short-lived token scoped to the specific org/repos where the app is installed. Used for all API operations (reading files, creating branches, opening PRs).

This is the correct model for a server-side automation app — no user OAuth flow is needed since the app acts on its own behalf.

### 5.2 Required Permissions

| Permission | Access | Reason |
| --- | --- | --- |
| **Contents** | Read & Write | Read repo files, create branches, push commits |
| **Pull Requests** | Read & Write | Check for open PRs, create new PRs |
| **Metadata** | Read | Required for all apps, list repos |

### 5.3 Webhook Events

| Event | Trigger |
| --- | --- |
| `repository` (action: `created`) | A new repo is created in the org |
| `installation_repositories` (action: `added`) | Repos are added to an existing app installation |
| `installation` (action: `created`) | App is newly installed on an org/repos |

### 5.4 Webhook Delivery

GitHub delivers webhooks to a single HTTPS endpoint. The app exposes this via a Kubernetes `Service` + `Ingress` (or ALB Ingress Controller given the existing EKS setup).

---

## 6. Detailed Design

### 6.1 File Rule Registry (Extensibility Core)

The central design principle: every file we want to enforce is represented as a `FileRule`. Adding a new file means adding a new rule to the registry — no other code changes required.

```go
// rule.go

// FileRule defines a required file and how to detect/create it.
type FileRule struct {
    // Human-readable name for logging and PR descriptions.
    Name string

    // Paths to check in priority order. If ANY path exists, the rule is satisfied.
    // Supports exact paths and glob patterns.
    Paths []string

    // PRSearchTerms are strings to search for in open PR titles/branches
    // to determine if someone is already working on adding this file.
    PRSearchTerms []string

    // DefaultTemplateName is the key into the template store (ConfigMap)
    // for the default file content.
    DefaultTemplateName string

    // TargetPath is where the default file will be created if missing.
    TargetPath string

    // Enabled allows rules to be toggled without removal.
    Enabled bool
}
```

**Initial rule set:**

```go
var DefaultRules = []FileRule{
    {
        Name:                "CODEOWNERS",
        Paths:               []string{"CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"},
        PRSearchTerms:       []string{"codeowners", "CODEOWNERS"},
        DefaultTemplateName: "codeowners",
        TargetPath:          ".github/CODEOWNERS",
        Enabled:             true,
    },
    {
        Name:                "Dependabot",
        Paths:               []string{".github/dependabot.yml", ".github/dependabot.yaml"},
        PRSearchTerms:       []string{"dependabot"},
        DefaultTemplateName: "dependabot",
        TargetPath:          ".github/dependabot.yml",
        Enabled:             true,
    },
    {
        Name:  "Renovate",
        Paths: []string{
            "renovate.json",
            "renovate.json5",
            ".renovaterc",
            ".renovaterc.json",
            ".github/renovate.json",
            ".github/renovate.json5",
        },
        PRSearchTerms:       []string{"renovate"},
        DefaultTemplateName: "renovate",
        TargetPath:          "renovate.json",
        Enabled:             true,
    },
}
```

**Extending with a new rule** (example — adding a `LICENSE` check):

```go
// Just append to the registry. No other changes needed.
DefaultRules = append(DefaultRules, FileRule{
    Name:                "LICENSE",
    Paths:               []string{"LICENSE", "LICENSE.md", "LICENSE.txt"},
    PRSearchTerms:       []string{"license", "LICENSE"},
    DefaultTemplateName: "license",
    TargetPath:          "LICENSE",
    Enabled:             true,
})
```

### 6.2 Checker Engine Flow

For each repo processed (whether triggered by webhook or scheduler):

```
┌─────────────────────────┐
│  Receive repo reference │
│  (owner/name, install)  │
└───────────┬─────────────┘
            │
            ▼
┌─────────────────────────┐
│  Get installation token │
└───────────┬─────────────┘
            │
            ▼
┌─────────────────────────┐
│  Iterate FileRule list  │◄──── for each enabled rule:
└───────────┬─────────────┘
            │
            ▼
┌─────────────────────────┐     ┌───────────────┐
│  Check if any Path      ├────►│  File exists  │──► Skip rule
│  exists in repo         │     └───────────────┘
└───────────┬─────────────┘
            │ (not found)
            ▼
┌─────────────────────────┐     ┌──────────────┐
│  Search open PRs for    ├────►│  PR exists   │──► Skip rule
│  PRSearchTerms          │     └──────────────┘
└───────────┬─────────────┘
            │ (no PR)
            ▼
┌─────────────────────────┐
│  Add to "missing" list  │
└───────────┬─────────────┘
            │
            ▼  (after all rules)
┌─────────────────────────┐
│  Any missing files?     │──── No ──► Done
└───────────┬─────────────┘
            │ Yes
            ▼
┌─────────────────────────┐
│  Create single branch   │
│  from default branch    │
│  "repo-guardian/add-    │
│   missing-files"        │
└───────────┬─────────────┘
            │
            ▼
┌─────────────────────────┐
│  Commit all missing     │
│  default files to       │
│  branch in one commit   │
└───────────┬─────────────┘
            │
            ▼
┌─────────────────────────┐
│  Create PR with summary │
│  of added files         │
└─────────────────────────┘
```

**Key design decisions:**

- All missing files for a given repo are bundled into a **single branch and single PR** to avoid noise.
- The branch name is deterministic (`repo-guardian/add-missing-files`) so the app can detect if it already has an open PR from a previous run and update it rather than creating duplicates.
- If the app's own PR branch already exists and is open, it **updates the existing PR** by force-pushing the branch with any newly missing files included.

### 6.3 Webhook Handler

```go
// POST /webhooks/github
func (h *WebhookHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
    payload, err := github.ValidatePayload(r, h.webhookSecret)
    // ...

    event, err := github.ParseWebHook(github.WebHookType(r), payload)
    // ...

    switch e := event.(type) {
    case *github.RepositoryEvent:
        if e.GetAction() == "created" {
            h.enqueue(e.GetRepo(), e.GetInstallation().GetID())
        }

    case *github.InstallationRepositoriesEvent:
        if e.GetAction() == "added" {
            for _, repo := range e.RepositoriesAdded {
                h.enqueue(repo, e.GetInstallation().GetID())
            }
        }

    case *github.InstallationEvent:
        if e.GetAction() == "created" {
            for _, repo := range e.Repositories {
                h.enqueue(repo, e.GetInstallation().GetID())
            }
        }
    }
}
```

### 6.4 Scheduler

Two implementation options (recommend Option A for simplicity):

**Option A: In-process ticker**

```go
func (s *Scheduler) Start(ctx context.Context) {
    ticker := time.NewTicker(7 * 24 * time.Hour) // weekly
    defer ticker.Stop()

    // Run once on startup to catch anything missed
    s.reconcileAll(ctx)

    for {
        select {
        case <-ticker.C:
            s.reconcileAll(ctx)
        case <-ctx.Done():
            return
        }
    }
}

func (s *Scheduler) reconcileAll(ctx context.Context) {
    installations, _ := s.ghClient.ListInstallations(ctx)
    for _, install := range installations {
        repos, _ := s.ghClient.ListInstallationRepos(ctx, install.GetID())
        for _, repo := range repos {
            s.enqueue(repo, install.GetID())
        }
    }
}
```

**Option B: Kubernetes CronJob** that calls an internal `/reconcile` endpoint. Better for observability but more moving parts.

**Recommendation:** Start with Option A. The single-replica deployment with leader election (or simply accepting single-replica) keeps things simple. If the app needs to scale or you want CronJob-level observability, migrate to Option B.

### 6.5 Work Queue

To avoid blocking webhook responses and to handle rate limiting:

```go
type WorkQueue struct {
    ch chan RepoJob
}

type RepoJob struct {
    Owner          string
    Repo           string
    InstallationID int64
    Trigger        string // "webhook", "scheduler", "manual"
}
```

Use a buffered channel with configurable worker count. Workers pull jobs and call the Checker Engine. This naturally provides concurrency control and backpressure against GitHub API rate limits.

### 6.6 Template Management

Default file contents are stored in a Kubernetes ConfigMap mounted into the pod:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: repo-guardian-templates
data:
  codeowners: |
    # Default CODEOWNERS - update with your team
    # https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-code-owners
    * @<org>/<default-team>

  dependabot: |
    version: 2
    updates:
      - package-ecosystem: "github-actions"
        directory: "/"
        schedule:
          interval: "weekly"

  renovate: |
    {
      "$schema": "https://docs.renovatebot.com/renovate-schema.json",
      "extends": [
        "config:recommended"
      ]
    }
```

Templates are loaded at startup and can be reloaded on ConfigMap changes (via fsnotify or pod restart).

---

## 7. Kubernetes Deployment

### 7.1 Manifests

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: repo-guardian
  namespace: platform-tools
spec:
  replicas: 1  # Single replica to avoid duplicate processing
  selector:
    matchLabels:
      app: repo-guardian
  template:
    metadata:
      labels:
        app: repo-guardian
    spec:
      serviceAccountName: repo-guardian
      containers:
        - name: repo-guardian
          image: <registry>/repo-guardian:latest
          ports:
            - containerPort: 8080
              name: http
            - containerPort: 9090
              name: metrics
          env:
            - name: GITHUB_APP_ID
              valueFrom:
                secretKeyRef:
                  name: repo-guardian-github
                  key: app-id
            - name: GITHUB_WEBHOOK_SECRET
              valueFrom:
                secretKeyRef:
                  name: repo-guardian-github
                  key: webhook-secret
          volumeMounts:
            - name: github-private-key
              mountPath: /etc/repo-guardian/private-key
              readOnly: true
            - name: templates
              mountPath: /etc/repo-guardian/templates
              readOnly: true
          resources:
            requests:
              cpu: 100m
              memory: 128Mi
            limits:
              cpu: 500m
              memory: 256Mi
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
          readinessProbe:
            httpGet:
              path: /readyz
              port: http
      volumes:
        - name: github-private-key
          secret:
            secretName: repo-guardian-github
            items:
              - key: private-key
                path: private-key.pem
        - name: templates
          configMap:
            name: repo-guardian-templates
---
apiVersion: v1
kind: Service
metadata:
  name: repo-guardian
  namespace: platform-tools
spec:
  selector:
    app: repo-guardian
  ports:
    - name: http
      port: 80
      targetPort: 8080
    - name: metrics
      port: 9090
      targetPort: 9090
```

### 7.2 Ingress

Using ALB Ingress Controller (already in use in the EKS environment):

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: repo-guardian
  namespace: platform-tools
  annotations:
    alb.ingress.kubernetes.io/scheme: internet-facing
    alb.ingress.kubernetes.io/target-type: ip
    alb.ingress.kubernetes.io/listen-ports: '[{"HTTPS": 443}]'
    alb.ingress.kubernetes.io/certificate-arn: <acm-cert-arn>
spec:
  ingressClassName: alb
  rules:
    - host: repo-guardian.platform.example.com
      http:
        paths:
          - path: /webhooks
            pathType: Prefix
            backend:
              service:
                name: repo-guardian
                port:
                  name: http
```

---

## 8. Project Structure

```
repo-guardian/
├── cmd/
│   └── repo-guardian/
│       └── main.go              # Entrypoint, wiring
├── internal/
│   ├── config/
│   │   └── config.go            # Env/flag parsing, ConfigMap loading
│   ├── github/
│   │   ├── client.go            # GitHub API wrapper (install tokens, etc.)
│   │   └── client_test.go
│   ├── checker/
│   │   ├── engine.go            # Core check-and-PR logic
│   │   ├── engine_test.go
│   │   └── queue.go             # Work queue implementation
│   ├── rules/
│   │   ├── registry.go          # FileRule type + default rules
│   │   ├── registry_test.go
│   │   └── templates/           # Fallback embedded templates
│   │       ├── codeowners.tmpl
│   │       ├── dependabot.tmpl
│   │       └── renovate.tmpl
│   ├── webhook/
│   │   ├── handler.go           # HTTP handler for GitHub webhooks
│   │   └── handler_test.go
│   └── scheduler/
│       ├── scheduler.go         # Weekly reconciliation loop
│       └── scheduler_test.go
├── deploy/
│   ├── base/
│   │   ├── deployment.yaml
│   │   ├── service.yaml
│   │   ├── configmap.yaml
│   │   └── kustomization.yaml
│   └── overlays/
│       ├── dev/
│       └── prod/
├── Dockerfile
├── go.mod
├── go.sum
└── README.md
```

---

## 9. PR Behavior Details

### 9.1 Branch Naming

```
repo-guardian/add-missing-files
```

Deterministic so the app can detect its own existing branches/PRs.

### 9.2 PR Title & Body

**Title:** `chore: add missing repo configuration files`

**Body (auto-generated):**

```markdown
## Repo Guardian — Missing Configuration Files

This PR was automatically created by **repo-guardian** because the following
required configuration files were not found in this repository:

### Added Files

- `.github/CODEOWNERS` — Defines code ownership for review routing
- `.github/dependabot.yml` — Enables automated dependency updates via Dependabot
- `renovate.json` — Enables automated dependency updates via Renovate

### What to do

1. Review the default file contents and adjust for your team's needs.
2. Merge when ready — these are sensible defaults, not one-size-fits-all.

---
*Automated by [repo-guardian](https://github.com/apps/repo-guardian). Questions? Reach out in #platform-engineering.*
```

### 9.3 Idempotency

| Scenario | Behavior |
| --- | --- |
| All files exist | No action |
| Files missing, no open PR | Create branch + PR |
| Files missing, app's own PR already open | Update existing PR (force-push branch) |
| Files missing, someone else's PR addresses it | No action (detected via PR search) |
| Repo is archived | Skip entirely |
| Repo is a fork | Skip (configurable) |
| Empty repo (no default branch) | Skip, log warning |

---

## 10. Observability

### 10.1 Metrics (Prometheus)

| Metric | Type | Description |
| --- | --- | --- |
| `repo_guardian_repos_checked_total` | Counter | Total repos processed (label: trigger) |
| `repo_guardian_prs_created_total` | Counter | PRs created |
| `repo_guardian_prs_updated_total` | Counter | Existing PRs updated |
| `repo_guardian_files_missing_total` | Counter | Missing files detected (label: rule_name) |
| `repo_guardian_check_duration_seconds` | Histogram | Time to check a single repo |
| `repo_guardian_webhook_received_total` | Counter | Webhooks received (label: event_type) |
| `repo_guardian_errors_total` | Counter | Errors (label: operation) |
| `repo_guardian_github_rate_remaining` | Gauge | GitHub API rate limit remaining |

### 10.2 Logging

Structured JSON logging via `slog` (Go stdlib). Log fields: `repo`, `installation_id`, `trigger`, `rule`, `action`.

---

## 11. Configuration

All configuration via environment variables (12-factor):

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `GITHUB_APP_ID` | Yes | — | GitHub App ID |
| `GITHUB_PRIVATE_KEY_PATH` | Yes | — | Path to PEM private key |
| `GITHUB_WEBHOOK_SECRET` | Yes | — | Webhook HMAC secret |
| `LISTEN_ADDR` | No | `:8080` | HTTP listen address |
| `METRICS_ADDR` | No | `:9090` | Prometheus metrics address |
| `WORKER_COUNT` | No | `5` | Concurrent repo check workers |
| `QUEUE_SIZE` | No | `1000` | Work queue buffer size |
| `TEMPLATE_DIR` | No | `/etc/repo-guardian/templates` | ConfigMap template mount |
| `SCHEDULE_INTERVAL` | No | `168h` | Reconciliation interval |
| `SKIP_FORKS` | No | `true` | Skip forked repos |
| `SKIP_ARCHIVED` | No | `true` | Skip archived repos |
| `DRY_RUN` | No | `false` | Log actions without creating PRs |
| `LOG_LEVEL` | No | `info` | Log verbosity |

---

## 12. Security Considerations

1. **Webhook signature validation**: All incoming webhooks are validated against the shared secret using `github.ValidatePayload()`. Reject all unsigned or mismatched payloads.
2. **Least-privilege permissions**: The app requests only Contents (R/W), Pull Requests (R/W), and Metadata (R). No admin access.
3. **Secret management**: Private key and webhook secret stored in Kubernetes Secrets, optionally backed by AWS Secrets Manager via External Secrets Operator.
4. **Installation token scope**: Tokens are automatically scoped to only the repos where the app is installed. No org-wide access unless the app is installed org-wide.
5. **Network policy**: Restrict egress to GitHub API endpoints only. Restrict ingress to ALB health checks + GitHub webhook IPs.
6. **IRSA**: Use IAM Roles for Service Accounts if any AWS API calls are needed (e.g., for Secrets Manager).

---

## 13. Testing Strategy

| Layer | Approach |
| --- | --- |
| **Unit tests** | Test each FileRule path-matching, PR search logic, template rendering in isolation. Mock the GitHub client interface. |
| **Integration tests** | Use `go-github`'s test helpers or a test GitHub org with throwaway repos. Validate full check → branch → PR flow. |
| **Webhook tests** | Send synthetic webhook payloads to the handler, verify correct enqueue behavior and signature rejection for bad payloads. |
| **E2E (staging)** | Install the app on a staging org. Create a repo, verify PR appears within webhook delivery window. |
| **Dry-run mode** | Always available via `DRY_RUN=true` for safe validation in production without side effects. |

---

## 14. Rollout Plan

### Phase 1: Foundation (Week 1-2)

- Register GitHub App in the org with required permissions.
- Scaffold Go project with `cmd/`, `internal/` structure.
- Implement GitHub client wrapper with installation token management.
- Implement FileRule registry with the three initial rules.
- Implement Checker Engine with unit tests against mocked GitHub client.

### Phase 2: Webhook + Scheduler (Week 3)

- Implement webhook handler with signature validation.
- Implement in-process scheduler with reconciliation loop.
- Implement work queue with configurable concurrency.
- Add Prometheus metrics and structured logging.

### Phase 3: Deployment (Week 4)

- Build Docker image, push to ECR.
- Deploy to EKS dev cluster with Kustomize overlays.
- Configure ALB Ingress, point GitHub webhook URL.
- Validate with dry-run mode on a test set of repos.

### Phase 4: Production (Week 5)

- Install app on the production org (start with a subset of repos if preferred).
- Monitor metrics/logs for the first weekly reconciliation cycle.
- Disable dry-run mode once validated.
- Document runbook and hand off to platform team.

### Phase 5: Extend (Ongoing)

- Add additional FileRule entries as compliance requirements evolve.
- Consider adding a ConfigMap-driven rule definition (no redeploy for new rules).
- Evaluate adding Slack notifications for created PRs.

---

## 15. Future Considerations

- **ConfigMap-driven rules**: Define rules entirely in YAML so new file checks require zero code changes, just a ConfigMap update and pod restart.
- **Status checks**: Optionally report a commit status or check run indicating compliance state.
- **Dashboard**: Simple web UI or Grafana dashboard showing compliance posture across all repos.
- **Notification integration**: Post to Slack/Teams when PRs are created so teams know to review.
- **Rate limit awareness**: Implement exponential backoff and respect `X-RateLimit-Remaining` headers to avoid hitting GitHub API limits during large reconciliation runs.
- **Multi-GitHub support**: Abstract the GitHub client to support multiple GitHub Enterprise instances.
- **Webhook replay**: Store raw webhook payloads for debugging and replay capability.

---

## 16. Open Questions

1. **Renovate vs Dependabot**: Should the app create *both* configs as defaults, or should it be configurable per-org/repo to prefer one over the other? Given the Mend Renovate Enterprise evaluation, we may want Renovate-only defaults soon.
2. **CODEOWNERS default team**: What should the default `CODEOWNERS` entry be? A specific org team, or a placeholder that forces manual editing?
3. **PR auto-merge**: Should the app's PRs be configured for auto-merge if the repo allows it, or should we always require human review?
4. **Existing branch conflict**: If `repo-guardian/add-missing-files` branch exists but the PR was closed (not merged), should the app re-open or create a new branch with a suffix?
5. **Exemption mechanism**: Should repos be able to opt out (e.g., via a `.repo-guardian-ignore` file or a topic/label)?

---

## Appendix A: GitHub App Registration Checklist

- [ ] Create GitHub App in org settings
- [ ] Set homepage URL
- [ ] Set webhook URL to `https://repo-guardian.platform.example.com/webhooks/github`
- [ ] Generate and store webhook secret
- [ ] Set permissions: Contents (R/W), Pull Requests (R/W), Metadata (R)
- [ ] Subscribe to events: `repository`, `installation_repositories`, `installation`
- [ ] Generate private key, store in K8s Secret
- [ ] Install app on target org (all repos or select repos)

## Appendix B: Dependencies

| Package | Purpose |
| --- | --- |
| `github.com/google/go-github/v68` | GitHub API client |
| `github.com/bradleyfalzon/ghinstallation/v2` | GitHub App installation token transport |
| `github.com/prometheus/client_golang` | Prometheus metrics |
| `log/slog` (stdlib) | Structured logging |
| `net/http` (stdlib) | Webhook HTTP server |
