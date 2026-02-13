# Repo Guardian - Executive Summary

## What It Does

Repo Guardian is a GitHub App that automatically detects when repositories
across your GitHub organization are missing required configuration files --
CODEOWNERS, Dependabot, Renovate -- and opens pull requests with sensible
defaults. It works in real time (responding to new repo creation via webhooks)
and on a configurable schedule (weekly reconciliation across all repositories).

The app requires a single externally accessible HTTPS endpoint for GitHub to
deliver webhook events to. It runs as a lightweight service in Kubernetes (EKS)
or any container environment.

---

## The Problem

In organizations with hundreds or thousands of repositories, configuration drift
is inevitable. Engineers create repositories and forget to add baseline files.
The result:

- **No code ownership**: Missing CODEOWNERS means no automatic review routing,
  unclear accountability, and slower incident response.
- **No dependency automation**: Missing Dependabot or Renovate configs means
  dependencies go stale, security patches are missed, and CVEs accumulate
  silently.
- **Inconsistent compliance posture**: Some repos are well-configured, others
  are not, and there is no visibility into which is which.

Manual enforcement does not scale. Slack reminders get ignored. Wiki pages go
unread. The only reliable solution is automation that meets developers where
they work: in pull requests.

---

## How It Works

1. A new repository is created, or an existing repo is added to the app's
   installation.
2. Repo Guardian checks whether required configuration files exist.
3. If files are missing and no one else has already opened a PR to add them,
   Repo Guardian creates a single pull request with default templates.
4. A human reviews the PR, customizes the defaults for their team, and merges.

The app never auto-merges. It is additive, not gatekeeping -- it creates
suggestions, not mandates.

---

## Why Not Use an Existing Tool?

Several tools overlap with parts of what Repo Guardian does. None address the
specific problem of automated repository onboarding with file-level compliance.

### Mend Renovate Enterprise

Renovate is a dependency update tool, not a repository onboarding tool. Mend's
enterprise platform is priced at **$1,000/developer/year** and bundles SCA,
SAST, container scanning, and Renovate together. If you only need dependency
update configuration bootstrapped into new repositories, you do not need the
full Mend platform. Repo Guardian creates the Renovate (or Dependabot)
configuration file so that the free, open-source Renovate or GitHub-native
Dependabot can do the ongoing work.

### GitHub Allstar (OpenSSF / Google)

Allstar enforces repository-level security settings -- branch protection, outside
collaborator policies, binary artifact detection. It operates at the GitHub API
settings layer (repository configuration, branch rules), not at the file content
layer. Allstar does not check for or create missing files like CODEOWNERS or
dependency manager configs. The two tools are complementary: Allstar enforces
repository settings, Repo Guardian ensures file-level configuration exists.

### Probot Settings / GitHub Safe-Settings

These tools synchronize repository settings (labels, branch protection, team
access) from a central YAML configuration. They are policy-as-code for GitHub
repository settings. They do not create pull requests to add missing files to
repositories. They also require maintaining a centralized configuration
repository that defines settings for every repo, which creates its own
maintenance burden.

### GitHub Repository Rulesets

GitHub's built-in rulesets enforce branch protection and merge requirements at
the organization level. They do not detect missing files or create pull requests.
They are a guardrail mechanism, not an onboarding mechanism.

### Manual Processes / Template Repositories

GitHub template repositories can pre-populate files when someone creates a repo
from the template. But developers forget to use templates, create repos from the
CLI or API without templates, or fork existing repos that lack the files.
Template repos are opt-in; Repo Guardian is automatic.

### Summary Comparison

| Capability | Repo Guardian | Renovate Enterprise | Allstar | Safe-Settings | Template Repos |
|---|---|---|---|---|---|
| Detects missing files | Yes | No | No | No | No |
| Creates PRs for missing configs | Yes | No | No | No | No |
| Works on existing repos | Yes | N/A | Yes | Yes | No |
| Requires per-developer licensing | No | $1,000/dev/yr | No | No | No |
| Enforces repo-level settings | No | No | Yes | Yes | No |
| Dependency updates | No (bootstraps config) | Yes | No | No | No |
| Real-time webhook response | Yes | N/A | Yes | Yes | No |
| Scheduled reconciliation | Yes | Yes | Yes | No | No |

---

## What It Brings to the Organization

### Developer Tooling

- **Zero-friction onboarding**: Every new repo gets the right configuration
  files within minutes of creation, without any action from the developer.
- **Sensible defaults, not rigid mandates**: Default templates provide a
  starting point. Teams customize before merging. The PR description explains
  what each file does and why it matters.
- **Respects existing work**: If a developer or another tool has already opened
  a PR to add the file, Repo Guardian detects it and does nothing. No duplicate
  PRs, no conflicts.
- **Extensible rule system**: Adding a new required file (LICENSE, security
  policy, CI config) means adding a single rule definition. No changes to core
  engine code.

### Security and Compliance

- **CODEOWNERS enforcement**: Ensures every repository has defined code
  ownership, which is a prerequisite for GitHub's required review routing. This
  directly supports SOC 2 access control requirements and reduces blast radius
  during incidents.
- **Dependency automation bootstrapping**: Ensures every repository has
  Dependabot or Renovate configured, so security patches flow automatically as
  PRs. This is the single most effective measure against known-vulnerability
  exploitation (the majority of breaches involve known, patched CVEs in
  unpatched dependencies).
- **Custom properties sync for Wiz integration**: Reads Backstage
  `catalog-info.yaml` files to extract ownership and component metadata, then
  syncs those values to GitHub repository custom properties (Owner, Component,
  JiraProject, JiraLabel). These properties are consumed by Wiz for security
  scanning attribution. Two operational modes: `github-action` (creates a PR
  with a one-shot GitHub Actions workflow, requires no write permissions) and
  `api` (sets properties directly via the GitHub API, also creates a
  `catalog-info.yaml` PR if the file is missing). Repositories without a valid
  catalog-info.yaml are tagged as `Unclassified` so they remain visible in
  security dashboards.
- **Organization-wide visibility**: Prometheus metrics expose compliance posture
  across all repositories -- how many repos are fully configured, how many
  missing files were detected, how many PRs were created. This data feeds
  dashboards and audit reports.
- **Audit trail**: Structured JSON logs record every check, every PR created,
  and every skip decision with full context (repo, trigger, rule, timestamp).

### Operational Efficiency

- **Eliminates manual follow-up**: No more Slack messages asking teams to add
  CODEOWNERS. No more quarterly audits finding gaps. The automation runs
  continuously.
- **Safe by default**: Dry-run mode allows validation in production without side
  effects. The app can be deployed, observed, and tuned before enabling PR
  creation.
- **Minimal infrastructure cost**: Single container (~20 MB image), minimal
  resource footprint (100m CPU, 128Mi memory). No database, no external
  dependencies beyond the GitHub API.
- **Rate-limit aware**: Built-in adaptive rate limiting prevents the app from
  exhausting GitHub API quotas, even during full reconciliation of large
  organizations.

---

## Deployment and Cost

| Item | Detail |
|---|---|
| **Infrastructure** | Single Kubernetes pod (or any container runtime) |
| **Image size** | ~20 MB (distroless base, static Go binary) |
| **Resource requests** | 100m CPU, 128Mi memory |
| **External dependencies** | GitHub API only |
| **Licensing cost** | None (internal tool, open-source dependencies) |
| **GitHub API usage** | ~1-3 API calls per repo per reconciliation cycle |

---

## Current Status

The application is feature-complete and production-ready. All five implementation
phases are complete:

1. Foundation (GitHub client, rule registry, checker engine)
2. Webhook handler, scheduler, work queue, observability
3. Docker image, Kubernetes manifests (Kustomize), CI pipeline
4. Production deployment configuration (dev and prod overlays)
5. Extensibility (template overrides, configurable rules)

### Built-in Rules

| Rule | Status | Purpose |
|---|---|---|
| CODEOWNERS | Enabled | Code ownership and review routing |
| Dependabot | Enabled | Automated dependency updates (GitHub-native) |
| Renovate | Defined (disabled by default) | Automated dependency updates (Mend/OSS) |

New rules can be added with a single struct definition and a template file.
