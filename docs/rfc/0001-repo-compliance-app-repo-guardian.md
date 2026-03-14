---
id: RFC-0001
title: "Repo Compliance App (repo-guardian)"
status: Draft
author: Donald Gifford
created: 2026-02-06
target: EKS (Kubernetes)
language: Go
integration: GitHub App (OAuth / Webhook)
---
<!-- markdownlint-disable-file MD025 MD041 -->

# RFC 0001: Repo Compliance App (repo-guardian)

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-02-06

## Summary

Automated GitHub App that detects missing configuration files (CODEOWNERS,
Dependabot, Renovate) across a GitHub organization and creates pull requests
with sensible defaults. Deployed to Kubernetes (EKS) with webhook-driven and
scheduled reconciliation.

## Problem Statement

Across a large GitHub organization with hundreds (or thousands) of repositories,
there is no automated enforcement ensuring that newly created or onboarded
repositories contain baseline configuration files such as `CODEOWNERS`,
Dependabot configuration, and Renovate configuration. Engineers frequently
create repos and forget to add these files, leading to inconsistent dependency
management, unclear ownership, and compliance drift.

Manual enforcement does not scale. We need an automated system that detects
missing files and creates pull requests to add sensible defaults, while being
easily extensible to enforce additional files in the future.

## Goals

1. Automatically detect when a repository is **created** or **newly added**
   (i.e., the app is installed on a repo it hasn't processed before) within a
   GitHub org or set of repos.
2. Run a **scheduled weekly reconciliation** across all installed repositories.
3. For each repo, check for the existence of a configurable set of required
   files.
4. If a file is missing, check whether an **open PR already exists** to add it
   before taking action.
5. If no file and no open PR exist, create a **branch with the missing file(s)**
   and open a PR.
6. Design the file-check system to be **easily extensible** -- adding a new
   required file should require minimal code changes.

## Non-Goals

- Enforcing file *content* beyond providing sensible defaults (no
  linting/validation of existing files).
- Blocking merges or acting as a required status check (this is additive, not
  gatekeeping).
- Managing GitHub App installation lifecycle (admins install manually; the app
  reacts).
- Multi-GitHub-Enterprise support (single GitHub instance target, extendable
  later).

## Proposed Solution

A Go-based GitHub App deployed to EKS that combines webhook-driven reactions
with scheduled reconciliation. The core design centers on a pluggable
**FileRule registry** where each rule defines what file to check, where to look
for it, and what default template to use.

## Design

### Architecture Overview

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
│  │  K8s Secret      │  <- GitHub App private key,        │
│  │                  │    app ID, webhook secret          │
│  └──────────────────┘                                    │
│                                                          │
│  ┌──────────────────┐                                    │
│  │  ConfigMap       │  <- Default file templates,        │
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
| **File Rule Registry** | A pluggable list of "rules" -- each rule defines what file(s) to look for, alternate paths, and a default template. Adding a new rule = adding a new struct instance. |
| **GitHub API Client** | Thin wrapper around `google/go-github` for repo contents, branches, commits, and PRs. Uses GitHub App installation tokens. |

### GitHub App Configuration

#### Authentication

The app uses the standard **GitHub App** authentication flow (not OAuth user
tokens):

1. **App-level JWT**: Signed with the app's RSA private key, used to list
   installations and generate installation tokens.
2. **Installation Access Token**: Short-lived token scoped to the specific
   org/repos where the app is installed. Used for all API operations (reading
   files, creating branches, opening PRs).

#### Required Permissions

| Permission | Access | Reason |
| --- | --- | --- |
| **Contents** | Read & Write | Read repo files, create branches, push commits |
| **Pull Requests** | Read & Write | Check for open PRs, create new PRs |
| **Metadata** | Read | Required for all apps, list repos |

#### Webhook Events

| Event | Trigger |
| --- | --- |
| `repository` (action: `created`) | A new repo is created in the org |
| `installation_repositories` (action: `added`) | Repos are added to an existing app installation |
| `installation` (action: `created`) | App is newly installed on an org/repos |

### File Rule Registry (Extensibility Core)

The central design principle: every file we want to enforce is represented as a
`FileRule`. Adding a new file means adding a new rule to the registry -- no
other code changes required.

```go
type FileRule struct {
    Name                string
    Paths               []string
    PRSearchTerms       []string
    DefaultTemplateName string
    TargetPath          string
    Enabled             bool
}
```

Initial rule set: CODEOWNERS, Dependabot, Renovate.

### Checker Engine Flow

For each repo processed (whether triggered by webhook or scheduler):

1. Get installation token.
2. Iterate FileRule list. For each enabled rule:
   - Check if any Path exists in repo. If found, skip rule.
   - Search open PRs for PRSearchTerms. If found, skip rule.
   - Otherwise, add to "missing" list.
3. If any missing files: create single branch
   (`repo-guardian/add-missing-files`), commit all missing default files in one
   commit, create PR with summary.

**Key design decisions:**

- All missing files bundled into a **single branch and single PR**.
- Deterministic branch name for idempotent PR creation.
- If the app's own PR branch already exists and is open, it **updates the
  existing PR** by force-pushing.

### Webhook Handler

Handles three event types: `RepositoryEvent` (created), `InstallationRepositoriesEvent` (added), `InstallationEvent` (created). Each enqueues repos to the work queue.

### Scheduler

In-process ticker (Option A from RFC). Runs `reconcileAll` on startup and at
configured interval (default 168h / 1 week). Lists all installations, all
repos, and enqueues each.

### Work Queue

Buffered channel with configurable worker count. Workers pull jobs and call the
Checker Engine. Provides concurrency control and backpressure against GitHub API
rate limits.

### Template Management

Default file contents stored in a Kubernetes ConfigMap mounted into the pod.
Templates loaded at startup with embedded fallbacks.

### Kubernetes Deployment

Single replica deployment with resource limits, liveness/readiness probes,
volume mounts for private key and templates. Service exposes ports 80->8080 and
9090->9090. ALB Ingress Controller for webhook endpoint.

### PR Behavior

| Scenario | Behavior |
| --- | --- |
| All files exist | No action |
| Files missing, no open PR | Create branch + PR |
| Files missing, app's own PR already open | Update existing PR (force-push branch) |
| Files missing, someone else's PR addresses it | No action (detected via PR search) |
| Repo is archived | Skip entirely |
| Repo is a fork | Skip (configurable) |
| Empty repo (no default branch) | Skip, log warning |

### Observability

8 Prometheus metrics: repos checked, PRs created/updated, files missing, check
duration, webhooks received, errors, GitHub rate limit remaining. Structured
JSON logging via `slog`.

### Configuration

All configuration via environment variables (12-factor): `GITHUB_APP_ID`,
`GITHUB_PRIVATE_KEY_PATH`, `GITHUB_WEBHOOK_SECRET`, `LISTEN_ADDR`,
`METRICS_ADDR`, `WORKER_COUNT`, `QUEUE_SIZE`, `TEMPLATE_DIR`,
`SCHEDULE_INTERVAL`, `SKIP_FORKS`, `SKIP_ARCHIVED`, `DRY_RUN`, `LOG_LEVEL`.

### Security

1. Webhook signature validation via `github.ValidatePayload()`.
2. Least-privilege permissions (Contents R/W, Pull Requests R/W, Metadata R).
3. Secret management via Kubernetes Secrets (optionally backed by AWS Secrets
   Manager).
4. Installation token scope limited to installed repos.
5. Network policy restricts egress to GitHub API, ingress to ALB + GitHub
   webhook IPs.

## Alternatives Considered

- **Kubernetes CronJob** for scheduling instead of in-process ticker. More
  observability but more moving parts. Starting with in-process ticker for
  simplicity.
- **Per-file PRs** instead of bundling all missing files into one PR. Rejected
  to avoid notification noise.
- **Status checks / merge blocking** instead of additive PRs. Rejected as this
  should be additive, not gatekeeping.

## Implementation Phases

### Phase 1: Foundation (Week 1-2)

- Register GitHub App, scaffold Go project.
- Implement GitHub client wrapper, FileRule registry, Checker Engine.

### Phase 2: Webhook + Scheduler (Week 3)

- Webhook handler, scheduler, work queue, observability.

### Phase 3: Deployment (Week 4)

- Docker image, EKS deployment, ALB Ingress, dry-run validation.

### Phase 4: Production (Week 5)

- Production rollout, monitoring, disable dry-run.

### Phase 5: Extend (Ongoing)

- Additional FileRule entries, ConfigMap-driven rules, Slack notifications.

## Risks and Mitigations

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| GitHub API rate limiting during large reconciliation | High | Medium | Work queue with configurable concurrency, rate limit transport middleware |
| Duplicate PRs from race conditions | Medium | Low | Deterministic branch naming, idempotent PR creation |
| Template content not suitable for all repos | Low | High | PRs require human review, sensible defaults as starting point |
| App credentials compromised | High | Low | K8s Secrets, least-privilege permissions, IRSA for AWS |

## Success Criteria

- All new repositories automatically receive PRs for missing configuration
  files within the webhook delivery window.
- Weekly reconciliation catches any repos that were missed or had files removed.
- Adding a new required file takes < 30 minutes of developer time (new
  FileRule + template).
- Zero duplicate PRs created across webhook and scheduler triggers.

## References

- [GitHub Apps Documentation](https://docs.github.com/en/apps)
- [go-github v68](https://github.com/google/go-github)
- [ghinstallation v2](https://github.com/bradleyfalzon/ghinstallation)
- [Kustomize](https://kustomize.io/)
