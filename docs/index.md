# repo-guardian

Welcome to the documentation for repo-guardian.

New here? Start with the [Getting Started walkthrough](usage/getting-started.md)
for what repo-guardian is, what it can (and can't) do, and how to roll it out —
then keep the [Policy Reference](usage/policy-reference.md) open while writing
your `guardian.hcl`.

## Document Types

- [RFCs](rfc/README.md)
- [ADRs](adr/README.md)
- [Design](design/README.md)
- [Implementation Plans](impl/README.md)
- [Plans](plan/README.md)
- [Investigations](investigation/README.md)

---

## What It Does

Repo Guardian is a GitHub App that automatically detects when repositories
across your GitHub organization are missing required configuration files --
CODEOWNERS, Dependabot, Renovate -- and opens pull requests with sensible
defaults. It also reads Backstage `catalog-info.yaml` files to sync ownership
metadata to GitHub repository custom properties, enabling downstream security
tools like Wiz to attribute findings to the correct team.

It works in real time (responding to new repo creation via webhooks) and on a
configurable schedule (a leader-elected stale-sweeper re-checks repositories
whose last check has aged past a freshness window). The app requires a single
externally accessible HTTPS endpoint for GitHub to deliver webhook events to.
It runs as a lightweight service in Kubernetes (EKS) or any container
environment, backed by Postgres (persistent per-repo state) and Valkey
(durable work queue + leader election) — both baked into the Helm chart by
default.

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
- **No ownership attribution for security scanning**: Security tools like Wiz
  need to know which team owns a repository to route findings. Without
  structured metadata on every repo, security findings land in a backlog with
  no clear owner, and remediation stalls.

Manual enforcement does not scale. Slack reminders get ignored. Wiki pages go
unread. The only reliable solution is automation that meets developers where
they work: in pull requests.

---

## How It Works

1. A new repository is created, or an existing repo is added to the app's
   installation. Webhooks seed a persistent state row and enqueue an
   immediate check; a periodic Discoverer catches repos whose webhooks were
   missed.
2. Repo Guardian checks whether required configuration files exist.
3. If files are missing and no one else has already opened a PR to add them,
   Repo Guardian creates a single pull request with default templates.
4. If custom properties mode is enabled, it reads the repo's
   `catalog-info.yaml` (Backstage Component entity), extracts ownership and
   component metadata, and syncs those values to GitHub repository custom
   properties (Owner, Component, JiraProject, JiraLabel). Repos without a
   catalog-info.yaml are tagged as `Unclassified`.
5. A human reviews the PR, customizes the defaults for their team, and merges.
6. When every rule on an open repo-guardian PR becomes satisfied on the
   default branch, the PR is auto-closed with a final reconcile-log comment
   (opt-out via `AUTO_CLOSE_PR=false`).

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
| Syncs repo ownership metadata | Yes | No | No | No | No |
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
  resource footprint (100m CPU, 128Mi memory). Postgres + Valkey backing
  services are required (in-memory backends were removed in chart 1.0;
  see IMPL-0016) but ship out-of-the-box as baked StatefulSets in the
  Helm chart, so the default install brings up the whole stack with no
  external infra. Operators can swap to managed Postgres (RDS, CloudSQL,
  CNPG) and managed Valkey (ElastiCache for Valkey) via chart values
  when scale demands it.
- **Rate-limit aware**: Built-in adaptive rate limiting prevents the app from
  exhausting GitHub API quotas, even during full reconciliation of large
  organizations. A per-installation `BudgetTracker` additionally gates
  scheduler enqueues so a single installation cannot burn the hourly
  rate-limit window (IMPL-0015).

---

## Deployment and Cost

| Item | Detail |
|---|---|
| **Infrastructure** | Single Kubernetes pod (or any container runtime) |
| **Image size** | ~20 MB (distroless base, static Go binary) |
| **Resource requests** | 100m CPU, 128Mi memory |
| **External dependencies** | GitHub API + Postgres + Valkey. Postgres and Valkey ship as baked StatefulSets in the chart by default, or can be swapped for managed services (RDS / CNPG, ElastiCache for Valkey) via chart values. |
| **Licensing cost** | None (internal tool, open-source dependencies) |
| **GitHub API usage** | ~1-3 API calls per repo per reconciliation cycle |

---

## Current Status

The application is feature-complete and production-ready. All implementation
phases are complete:

1. Foundation (GitHub client, policy engine, checker)
2. Webhook handler, scheduler, work queue, observability
3. Docker image, Helm chart deployment, CI pipeline
4. Production deployment via the chart (homelab Talos + AWS EKS)
5. Extensibility (template overrides, configurable rules)
6. Custom properties sync from Backstage catalog-info.yaml
7. Helm chart, webhook HMAC validation, HCL policy engine
8. Setting rules, branch-protection rules, ignore lists, reconcilers
9. Distributed Renovate via per-repo GitHub Actions
10. Per-org rule scoping (strict mode) and per-org metric labels
11. Helm chart distribution via OCI on GHCR with cosign keyless
    signing and SLSA Level 3 provenance — install via
    `helm install repo-guardian oci://ghcr.io/donaldgifford/charts/repo-guardian --version <v>`
12. Customizable PR templates (title/body/labels) at three HCL scopes
    with field-by-field inheritance; extensible template ConfigMap via
    `templates.files`
13. Persistent reconcile state and multi-replica coordination
    (Postgres-backed Store, Valkey-backed Queue and leader-elected
    Scheduler), initially opt-in with single-replica memory
    defaults preserved
14. PR auto-close when every rule on the PR is satisfied on the
    default branch, with a sticky reconcile-log comment on every
    sweep (opt-out via `policy.autoClosePR: false` / `AUTO_CLOSE_PR`)
15. Legacy engine path and Kustomize overlays removed — the Helm
    chart is the only supported deployment surface
16. Memory backend removed (chart 1.0). Postgres + Valkey become
    the only supported store/queue/scheduler backends; the chart
    bakes both as StatefulSets out-of-the-box. See
    `docs/operations/migrations.md#removing-memory-backend`
17. Stale-sweep cutover + repository discovery (IMPL-0015). The
    legacy full-enumeration sweep is gone: webhooks and a periodic
    `Discoverer` seed persistent `repo_state` rows, and the
    leader-elected `StaleSweeper` re-enqueues only repos whose
    check has aged past `RECONCILE_FRESHNESS` (or whose policy
    version drifted). A shared per-installation `BudgetTracker`
    gates both schedulers against the GitHub rate-limit window.

### Rule Types

repo-guardian's HCL policy engine supports three rule types, each with
optional global and per-rule ignore lists:

| Type | Block | Purpose |
|---|---|---|
| File | `rule "file" "name"` | Detect and add missing files; assert content via regex/YAML path |
| Setting | `rule "setting" "name"` | Check and remediate 8 repository properties (issues, wiki, default branch, vulnerability alerts, etc.) |
| Branch protection | `rule "branch_protection" "name"` | Check and remediate branch rulesets (required approvals, status checks, etc.) |

### Built-in File Rules

| Rule | Status | Purpose |
|---|---|---|
| CODEOWNERS | Enabled | Code ownership and review routing |
| Dependabot | Enabled | Automated dependency updates (GitHub-native) |
| Renovate Config | Defined (disabled by default) | Automated dependency updates (Mend/OSS) |
| Renovate Workflow | Defined (disabled by default) | Per-repo GitHub Actions Renovate runner |
| Catalog Info | Conditional (enabled when `CUSTOM_PROPERTIES_MODE` is set) | Backstage `catalog-info.yaml` source for custom-property sync |

New rules can be added with a single HCL block in `guardian.hcl` (no
code changes, no rebuild). Built-in defaults — for rules that ship to
every operator — live in `internal/policy/defaults.go`.

### Reconcilers

Pluggable post-check behaviors attached to file rules via `reconcile { }`
blocks:

| Reconciler | Purpose |
|---|---|
| `custom_properties` | Sync Backstage catalog-info.yaml -> GitHub custom properties |
| `label_sync` | YAML-driven label create/update/rename/delete |
| `branch_protection` | YAML-driven branch protection ruleset management |
| `workflow_sync` | Lightweight observability for watched workflow files |

### Multi-org Scoping (DESIGN-0010)

For installations spanning multiple GitHub organizations, an optional
top-level `scope { orgs = [...] }` block engages strict mode where every
rule must declare its own `scope { }` sub-block:

- **Legacy mode** (no top-level `scope { }`): every enabled rule applies
  to every repo. Single-org users do not need to learn this feature.
- **Strict mode** (top-level `scope { }` declared): every rule must
  declare its scope. Use the literal `["*"]` to apply to every in-scope
  org, or a subset (e.g., `["myorg-prod"]`) to target specific orgs.
  Strict-mode validation runs at config load time.

All per-rule and per-repo Prometheus counters carry an `org` label so
operators can slice work, errors, and PR creation rates per organization.
For a GitHub Enterprise App installed across every org in the enterprise,
see `examples/guardian-enterprise.hcl` — the policy enumerates which orgs
to reconcile, and each configured org can carry its own rules.

### Custom Properties

| Mode | Behavior | When to use |
|---|---|---|
| Disabled (default) | No custom properties sync | Initial rollout, file rules only |
| `github-action` | Creates PR with one-shot GHA workflow | Least-privilege: no org-level write permissions needed |
| `api` | Sets properties directly via API; creates catalog-info.yaml PR if missing | Full automation: requires `custom_properties:write` permission |

Controlled by the `CUSTOM_PROPERTIES_MODE` environment variable. Properties
synced: Owner, Component, JiraProject, JiraLabel. Source of truth:
`catalog-info.yaml` (Backstage Component entity).
