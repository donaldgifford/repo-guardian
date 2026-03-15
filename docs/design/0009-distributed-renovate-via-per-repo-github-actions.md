---
id: DESIGN-0009
title: "Distributed Renovate via Per-Repo GitHub Actions"
status: Draft
author: Donald Gifford
created: 2026-03-15
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0009: Distributed Renovate via Per-Repo GitHub Actions

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-03-15

## Overview

Rather than running a single central Renovate process that iterates through
repositories serially, this approach distributes Renovate execution across
individual repositories via a standardized GitHub Actions workflow managed
by repo-guardian. Each repository runs its own Renovate job independently,
achieving native parallelism through GitHub Actions without requiring
Renovate Enterprise Edition.

For v1, repo-guardian treats the Renovate workflow and config as **just
another set of managed files** -- no different from CODEOWNERS or
Dependabot config. If they're missing, PR them in. If they drift, detect
it. The workflow includes a `schedule` trigger so repos run Renovate
independently. Coordinated dispatch, trigger endpoints, and durable state
are future work.

## Goals and Non-Goals

### Goals

- Distribute Renovate execution across repositories via per-repo GitHub
  Actions workflows
- Achieve native parallelism without Renovate Enterprise Edition
- Use repo-guardian's existing file rules to manage workflow and config
  files as enforced resources
- Authenticate via a dedicated Renovate GitHub App with per-org rate
  limit budgets
- Share configuration through an org-level Renovate preset repository

### Non-Goals

- Coordinated dispatch from repo-guardian (future work)
- Trigger endpoints or Slack integration (future work)
- Durable job state, external queue, or database (future work)
- Template variable substitution or per-repo template rendering
- Replacing Renovate Enterprise Edition for organizations that need
  webhook-driven immediacy or smart merge control
- Building a self-hosted Renovate server/worker architecture

## Background

### The Serial Execution Problem

At 10,000+ repositories across multiple GitHub organizations, serial
Renovate execution is not viable:

- At ~30-60 seconds per repo, a single Renovate process takes 80-170
  hours to cycle through all repositories
- By the time a full cycle completes, early repos are days stale
- This is the primary differentiator Mend uses to justify Renovate
  Enterprise Edition ($50K-$250K+/year), which adds a Server + Worker
  architecture for parallel execution

### repo-guardian as the Distribution Mechanism

We already have repo-guardian -- a system that enforces the presence of
standard files across all repositories. The distribution problem (getting
a CI file into every repo and keeping it in sync) is already solved via
the HCL policy engine, file rules, and reconciler system.

### Dependabot vs Renovate Rate Limits

Dependabot is a first-party GitHub product. It runs on GitHub's own
infrastructure, does not consume Actions minutes, and does not count
against API rate limits. GitHub manages scheduling and parallelism
internally, so a shared cron schedule (e.g., Sunday 6am) is fine.

Renovate running as a GitHub Action is different -- every run consumes
your Actions minutes and your GitHub App's API rate limit budget. At
scale, this matters. Each repo's Renovate run authenticates independently
via the GitHub App, so rate limit consumption is distributed across org
installations rather than concentrated in a single process.

## Detailed Design

### Architecture

#### Current State (Central Runner, Serial)

```
renovate-runner repo
└── 1 GitHub Actions job
    └── 1 Renovate process
        ├── repo-001  (t=0s)
        ├── repo-002  (t=45s)
        ├── repo-003  (t=90s)
        │   ...
        └── repo-10000  (t=~170hrs)
```

#### Proposed State (Per-Repo, Parallel)

```
repo-001/.github/workflows/renovate.yml
└── GitHub Actions job
    └── docker run renovate/renovate
        └── RENOVATE_REPOSITORIES=org/repo-001  ← scoped to this repo only

repo-002/.github/workflows/renovate.yml
└── GitHub Actions job
    └── docker run renovate/renovate
        └── RENOVATE_REPOSITORIES=org/repo-002  ← scoped to this repo only

...

repo-10000/.github/workflows/renovate.yml
└── GitHub Actions job
    └── docker run renovate/renovate
        └── RENOVATE_REPOSITORIES=org/repo-10000  ← scoped to this repo only
```

Each container is a completely hermetic Renovate process. No autodiscover,
no central coordinator, no shared state. GitHub Actions is the scheduler
and runner -- Renovate itself has zero awareness of other repositories in
the org.

All runs share the **org-level Renovate preset** hosted in a dedicated
config repo. The GitHub App token is distributed as an organization-level
secret, available automatically to all repositories without per-repo
configuration.

repo-guardian's role is simple: **ensure the workflow and config files
exist and are correct in every repo.** This is the same thing it does
for CODEOWNERS, Dependabot, and every other managed file.

### Components

#### 1. Renovate Workflow Template (managed by repo-guardian)

```yaml
# .github/workflows/renovate.yml
# Managed by repo-guardian — do not edit manually
name: Renovate

on:
  schedule:
    - cron: '0 3 * * 1'   # Weekly, Monday 3am UTC
  workflow_dispatch:

permissions:
  contents: write
  pull-requests: write
  issues: write

jobs:
  renovate:
    name: Renovate Dependency Updates
    runs-on: ubuntu-latest
    steps:
      - name: Generate GitHub App Token
        id: app-token
        uses: actions/create-github-app-token@v1
        with:
          app-id: ${{ secrets.RENOVATE_APP_ID }}
          private-key: ${{ secrets.RENOVATE_APP_PRIVATE_KEY }}

      - name: Run Renovate
        run: |
          docker run --rm \
            -e RENOVATE_TOKEN \
            -e RENOVATE_REPOSITORIES \
            -e RENOVATE_CONFIG_FILE \
            -e LOG_LEVEL \
            renovate/renovate:latest  # TODO: pin to specific version
        env:
          RENOVATE_TOKEN: ${{ steps.app-token.outputs.token }}
          RENOVATE_REPOSITORIES: ${{ github.repository }}
          RENOVATE_CONFIG_FILE: renovate.json
          LOG_LEVEL: info
```

Key points:

- **Docker-based execution** -- Renovate CLI runs directly in a container,
  not via the `renovatebot/github-action` wrapper. Each job is a
  completely isolated, hermetic Renovate process.
- **No `autodiscover`** -- `RENOVATE_REPOSITORIES` is set to
  `${{ github.repository }}`, scoping this Renovate process to exactly
  one repo. The container has zero awareness of any other repository in
  the org.
- **Token generation** -- `actions/create-github-app-token@v1` generates
  a short-lived installation token from the GitHub App credentials. The
  raw private key never reaches the Renovate container.
- **Static template** -- identical content for every repo, no per-repo
  variable substitution
- Credentials come from org-level secrets -- no per-repo secret management
- `schedule` trigger runs weekly -- all repos share the same cron, GitHub
  Actions runner queue naturally distributes execution over time
- `workflow_dispatch` allows manual triggers from the Actions UI or
  GitHub API for on-demand runs

#### 2. Renovate Config (managed by repo-guardian)

```json
{
  "$schema": "https://docs.renovateapp.com/renovate-schema.json",
  "extends": ["github>YOUR_ORG/renovate-config"]
}
```

Repositories may add per-repo overrides beneath `extends`, but the org
preset is always the base. repo-guardian validates this structure via
content assertions and can flag non-compliant overrides.

#### 3. Org Preset Repo (`renovate-config`)

A dedicated repository in each GitHub org hosts the shared preset:

```
renovate-config/
└── default.json
```

```json
{
  "$schema": "https://docs.renovateapp.com/renovate-schema.json",
  "extends": ["config:recommended"],
  "timezone": "America/New_York",
  "schedule": ["before 6am on Monday"],
  "prHourlyLimit": 4,
  "prConcurrentLimit": 10,
  "labels": ["dependencies"],
  "automerge": false,
  "packageRules": [
    {
      "matchUpdateTypes": ["minor", "patch"],
      "matchCurrentVersion": "!/^0/",
      "automerge": true
    },
    {
      "matchUpdateTypes": ["major"],
      "automerge": false,
      "labels": ["dependencies", "major-update"]
    }
  ]
}
```

#### 4. GitHub App Authentication

A dedicated **Renovate GitHub App** is installed across all orgs. This is
the authentication mechanism for all Renovate runs -- not a PAT.

**Why a GitHub App over a PAT:**

| | PAT | GitHub App |
|---|---|---|
| Rate limit | 5,000 req/hr (shared with user) | 15,000 req/hr per org installation |
| Identity | Your personal user | Bot account (`renovate[bot]`) |
| Expiry | Requires rotation | Key rotation only, no expiry |
| Auditability | Attributed to your user | Clearly attributed to the app |
| Multi-org | One PAT across all orgs | Install per org, separate rate limit budgets |

At 10,000+ repos with many concurrent runs, the **per-org 15,000 req/hr
rate limit** is what makes this architecture viable. Each org installation
gets its own budget -- parallel runs in org-A don't consume org-B's rate
limit.

**App permissions required:**

- Contents: Read & Write
- Pull requests: Read & Write
- Issues: Read & Write
- Metadata: Read
- Workflows: Read & Write (required to manage the workflow file itself)

**Secret distribution:**

Both secrets are set at the **organization level** in GitHub, making them
automatically available to all repositories without any per-repo
configuration:

- `RENOVATE_APP_ID` -- the GitHub App's numeric ID
- `RENOVATE_APP_PRIVATE_KEY` -- the App's PEM private key

### repo-guardian HCL Policy

Two file rules manage the Renovate files using the existing policy engine:

```hcl
rule "file" "renovate_workflow" {
  check    = "exact"
  paths    = [".github/workflows/renovate.yml"]
  target   = ".github/workflows/renovate.yml"
  template = "renovate-workflow"

  reconcile "workflow_sync" {
    watch = true
  }
}

rule "file" "renovate_config" {
  check    = "contains"
  paths    = ["renovate.json"]
  target   = "renovate.json"
  template = "renovate-config"

  assertion {
    pattern = "github>.*renovate-config"
    message = "renovate.json must extend org preset"
  }
}
```

**How this maps to existing repo-guardian behavior:**

| Concern | Mechanism |
|---|---|
| File missing | `check = "exact"` / `check = "contains"` detects absence, creates PR with template |
| File drifted from template | `check = "exact"` on workflow detects content mismatch, creates PR |
| Config missing org preset | `assertion` with regex pattern validates content |
| Someone edits managed workflow | `reconcile "workflow_sync"` with `watch = true` triggers re-check on push |
| Repo should be excluded | `ignore {}` block (global or per-rule) with glob patterns |
| Dry run / audit only | `dry_run = true` in `guardian {}` block -- logs what would happen, no changes |

No new enforcement modes, template versioning, or config formats are
needed. The existing HCL policy engine, check modes, assertions, and
reconciler system handle all of this.

### Rate Limit Analysis

With a GitHub App installed per org:

| Metric | Value |
|---|---|
| Rate limit per org installation | 15,000 req/hr |
| Estimated API calls per Renovate run | 50-150 (varies by repo size and dep count) |
| Concurrent runs (worst case, same cron) | All repos in org |
| GitHub Actions runner queue | Naturally distributes start times |

All repos sharing the same cron sounds like a thundering herd, but in
practice GitHub Actions doesn't start all workflows simultaneously. The
runner queue distributes execution based on runner availability. Each
repo's Renovate run authenticates independently via the GitHub App, so
API calls are spread over the duration of all runs, not concentrated at
the start.

For orgs where this becomes a real problem, the future dispatch
coordination (see Future Work) provides a controlled throttle.

### GitHub Actions Minutes

GitHub Enterprise Cloud includes **50,000 Actions minutes/month** per org
by default (verify your contract).

| Scenario | Estimate |
|---|---|
| Repos per org | ~2,000 avg (5 orgs, 10k total) |
| Avg run duration | 2-3 minutes |
| Cadence | Weekly |
| Minutes/org/month | 2,000 x 2.5 x 4 = **20,000 min/month** |
| % of 50k budget | **40%** |

This leaves 60% of your Actions budget available for other workflows. If
specific repos need an on-demand Renovate run, `workflow_dispatch` can
be triggered manually from the GitHub Actions UI.

## API / Interface Changes

### No New API Surface for v1

v1 requires no new endpoints, no new GitHub client methods, and no new
permissions beyond what repo-guardian already has. The renovate workflow
and config are managed as standard file rules through the existing policy
engine.

### GitHub App Permissions (Renovate App, Not repo-guardian)

The **Renovate GitHub App** (separate from repo-guardian's App) needs:

| Permission | Access | Required For |
|---|---|---|
| `contents` | Read & Write | Reading repo, creating branches |
| `pull_requests` | Read & Write | Creating/updating Renovate PRs |
| `issues` | Read & Write | Dependency dashboard issue |
| `metadata` | Read | Repository metadata |
| `workflows` | Read & Write | Managing workflow files |

repo-guardian's own GitHub App permissions are unchanged.

## Data Model

No database or persistent storage changes. repo-guardian treats the
Renovate files identically to all other managed files -- no special state
tracking.

## Testing Strategy

- **Unit tests** for `renovate.json` assertion -- validate that the
  `contains` check catches missing org preset references
- **Integration tests** -- repo-guardian engine with renovate file rules,
  mock GitHub client, verify correct PR content and assertion evaluation
- **Manual validation** -- pilot org with ~200 repos, observe Renovate
  run behavior, rate limit headroom, PR quality

## Failure Modes

| Failure | Impact | Mitigation |
|---|---|---|
| Renovate run fails in one repo | That repo misses an update cycle | Next scheduled run self-heals; no impact on other repos |
| GitHub App private key expires/rotates | All runs fail org-wide | Key rotation runbook; monitor via GitHub App expiry alerts |
| repo-guardian reconcile PR not merged | Repo stays on old template | PR remains open; next check cycle is idempotent |
| Actions minutes exhausted | No runs until next billing cycle | Alert at 80% consumption; adjust cadence |

## Comparison to Alternatives

| | This Approach | Central Runner (Serial) | Renovate EE |
|---|---|---|---|
| **Parallelism** | Native (one runner per repo) | Serial | Worker pool |
| **Cost** | $0 (Actions included) | $0 | $50K-$250K+/yr |
| **Cycle time (10k repos)** | Minutes (runner queue) | 80-170 hours | Minutes |
| **Rate limits** | Per-org App budget | Single process, low usage | Managed by Mend |
| **Operational complexity** | Low (repo-guardian manages files) | Low | High (Kubernetes infra) |
| **Config drift risk** | Low (repo-guardian reconciles) | None | None |
| **Webhook-driven runs** | Manual dispatch only | No | Yes |
| **Dependency Dashboard** | Per repo | Yes | Yes |
| **Smart Merge Control** | No | No | Yes |
| **Support SLA** | No | No | Yes |

### When to Reconsider Renovate EE

This approach covers 95% of the value at 0% of the cost. Consider EE if:

- **Webhook-driven immediacy is required** -- EE reacts to repo events in
  near-realtime. This approach is cron-driven plus manual dispatch.
- **Smart Merge Control is needed** -- EE's ML-based merge confidence
  scoring is not replicable with the open-source CLI.
- **Actions minutes become a constraint** -- If other workflows consume
  your Actions budget, a self-hosted EE deployment on your own compute
  avoids the minutes problem entirely.

## Migration / Rollout Plan

### Phase 1 -- Pilot (2 weeks)

- Select one non-critical org with ~200 repos
- Add renovate file rules to `guardian.hcl` with `dry_run = true`
- Review compliance report -- which repos are missing files
- Manually apply template to 10 repos, trigger via GitHub Actions UI
- Observe run behavior, logs, PR quality
- Validate rate limit headroom via GitHub API rate limit monitoring

### Phase 2 -- Controlled Rollout (4 weeks)

- Disable `dry_run`, repo-guardian creates PRs for missing files
- Monitor: Actions minutes consumption, rate limit usage, PR merge rate,
  noise level
- Tune `default.json` preset (schedule, PR limits, automerge rules) based
  on feedback
- Expand to a second org

### Phase 3 -- Full Rollout (ongoing)

- Enable across all orgs
- Onboard remaining orgs in waves of ~1,000 repos per week
- Monitor and tune

## Open Questions

- Determine which repo types are excluded (archived, forks, template
  repos) -- likely handled by existing `skip_forks` and `skip_archived`
  flags plus ignore lists

## Resolved Decisions

### Actions Minutes / Runner Capacity

Assume sufficient GitHub Enterprise Cloud minutes, or use ARC (Actions
Runner Controller) self-hosted runners on existing Kubernetes
infrastructure. Minutes budget is not a constraint for planning purposes.

### Reconcile PR Approval Policy

Reconcile PRs from repo-guardian **always require human approval** -- no
auto-merge. This applies to both `renovate.yml` workflow updates and
`renovate.json` config updates pushed by repo-guardian.

### Template Versioning

Not needed as a separate concept. repo-guardian's templates live in git.
When a template changes and is deployed, the weekly scheduler re-checks
all repos. `check = "exact"` detects drift from the current template and
creates a PR. Git is the source of truth for template versions.

### Shared Cron vs Staggered Scheduling

v1 uses a shared cron schedule across all repos. GitHub Actions runner
queue naturally distributes execution. If this becomes a bottleneck at
scale, coordinated dispatch via repo-guardian's work queue is available
as a future optimization (see Future Work).

## Future Work

### Coordinated Dispatch via repo-guardian

If the shared cron approach causes rate limit or runner queue issues at
scale, repo-guardian can take over dispatch coordination:

- Remove the `schedule` trigger from the workflow template
- Add a dispatch job type to repo-guardian's work queue
- The scheduler enqueues dispatch jobs, workers call `workflow_dispatch`
  via the GitHub API
- Worker concurrency provides natural throttling
- Requires `actions: write` permission on repo-guardian's GitHub App and
  a new `DispatchWorkflow` method on the GitHub client interface

This adds complexity to the scheduler, work queue, and job model. It
should only be pursued if the shared cron approach proves insufficient.

### Trigger Endpoint and Slack Integration

A stateless HTTP endpoint for on-demand single-repo dispatch:

```
POST /api/v1/trigger/{owner}/{repo}/renovate
```

This becomes the integration point for a Slack app:

```
/guardian renovate run org/repo-name
/guardian renovate run org/repo-name --all   # trigger all repos in org
/guardian renovate status org/repo-name      # last run status + PR count
```

This is not Renovate-specific -- it's a **repo-guardian control plane**
exposed via Slack. The same command surface applies to any workflow
repo-guardian manages:

```
/guardian run renovate org/repo-name
/guardian run license-check org/repo-name
/guardian status org/repo-name              # full compliance status
/guardian audit org/repo-name               # list all managed file violations
/guardian sync org/repo-name                # trigger reconcile PR
```

This should be scoped as a separate project. It requires the dispatch
coordination capability above and benefits from durable state.

### Custom Property-Driven Scheduling

GitHub repository custom properties can signal desired Renovate cadence:

```
Property name:  renovate-schedule
Type:           Single select
Allowed values: daily | 3x-weekly | weekly | custom
Default:        weekly
```

repo-guardian reads this property at dispatch time and adjusts frequency
accordingly. This requires the coordinated dispatch capability and the
ability to read custom properties FROM GitHub (the reverse direction from
the current custom properties reconciler, which writes TO GitHub from
catalog-info.yaml).

Other candidates for property-driven behavior:

- `renovate-automerge-policy` -- auto-merge low-risk template updates
- `codeowners-policy` -- enforcement strictness of CODEOWNERS file
- `branch-protection-profile` -- which branch protection preset to apply
- `sbom-scan` -- enable/disable SBOM generation workflow

### Durable State Store

When the following requirements emerge, add external state:

- **Redis** -- durable job queue with at-least-once delivery, dispatch
  cycle tracking
- **PostgreSQL** -- job history, dispatch audit trail, repo compliance
  status over time
- **Web UI** -- admin dashboard for dispatch cycles, repo status,
  manual triggers, configuration

This is explicitly not part of v1. v1 priorities are file compliance
enforcement and catalog-info.yaml custom properties reconciliation.
Everything else is a fancy file checker that puts stuff in repos.

## References

- [RFC-0002: HCL-driven Policy Engine](../rfc/0002-hcl-driven-policy-engine-for-repo-guardian.md)
- [DESIGN-0006: HCL Policy Configuration](0006-hcl-policy-configuration-and-rule-engine.md)
- [DESIGN-0007: Reconciler Interface](0007-reconciler-interface-and-push-event-handler.md)
- [DESIGN-0008: Additional Rule Types and Ignore Lists](0008-additional-rule-types-and-ignore-lists.md)
- [Renovate GitHub Action](https://github.com/renovatebot/github-action)
- [GitHub App Rate Limits](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/rate-limits-for-github-apps)
- [GitHub Workflow Dispatch API](https://docs.github.com/en/rest/actions/workflows#create-a-workflow-dispatch-event)
