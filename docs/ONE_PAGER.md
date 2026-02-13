# Repo Guardian

## The Problem

When an engineer creates a new GitHub repository, they rarely remember to add
baseline configuration files: CODEOWNERS for review routing, Dependabot or
Renovate for automated dependency updates, or a `catalog-info.yaml` for service
catalog registration. Across an organization with hundreds of repositories, this
leads to inconsistent code ownership, unpatched dependencies, unattributed
security findings, and compliance drift. Manual enforcement -- wiki pages, Slack
reminders, quarterly audits -- does not scale and is routinely ignored.

## The Insight

The gap is not that developers refuse to add these files. It is that there is no
automated system to detect the omission and surface it where developers already
work: in a pull request. The correct intervention is a timely, non-blocking
suggestion -- not a gate, not a policy document, not a ticket.

## The Solution

Repo Guardian is a GitHub App that monitors repository creation events and runs
weekly reconciliation across all installed repositories. When it detects missing
configuration files, it opens a single pull request with sensible default
templates. It also syncs ownership metadata from Backstage `catalog-info.yaml`
files to GitHub repository custom properties, enabling security tools like Wiz
to attribute findings to the correct team. A human reviews, customizes, and
merges. The app never auto-merges and never blocks.

**How it works:**

1. GitHub delivers a webhook when a repository is created or added to the app.
2. Repo Guardian checks whether required files exist (CODEOWNERS, Dependabot
   config, Renovate config).
3. If a file is missing and no existing PR addresses it, the app creates a
   branch and opens a PR with default content.
4. If custom properties mode is enabled, it reads the repo's
   `catalog-info.yaml`, extracts ownership metadata (Owner, Component,
   JiraProject, JiraLabel), and syncs to GitHub custom properties. Repos
   without a catalog-info.yaml are tagged as `Unclassified` and receive a PR
   with a template file.
5. A weekly scheduled scan catches any repositories that were missed or that
   had their PRs closed without merging.

The app requires a single externally accessible HTTPS endpoint for webhook
delivery. It runs as a lightweight container in Kubernetes or any equivalent
environment.

## Alternatives Considered

| Alternative | Why it falls short |
|---|---|
| **Mend Renovate Enterprise** ($1,000/dev/yr) | Manages dependency updates, not repository onboarding. Does not detect or create missing CODEOWNERS or config files. Full platform cost is disproportionate if the need is bootstrapping configs. |
| **GitHub Allstar** (OpenSSF) | Enforces repository-level settings (branch protection, collaborator policies). Does not operate at the file layer -- will not detect a missing CODEOWNERS or create a PR to add one. |
| **GitHub Safe-Settings / Probot Settings** | Synchronizes repository settings from a central YAML config. Does not create file-level PRs. Requires maintaining a centralized config repo for every repository in the org. |
| **Template Repositories** | Pre-populate files when a repo is created from a template. Developers forget to use templates, create repos from CLI/API, or fork repos that lack the files. Opt-in, not automatic. |

Repo Guardian is complementary to these tools, not a replacement. It fills the
specific gap of file-level compliance automation that none of them address.

## Key Benefits

**For engineering teams:** Every new repo is correctly configured within minutes,
with no manual steps. Default templates provide a starting point; teams
customize before merging.

**For security and compliance:** CODEOWNERS ensures defined code ownership
(supports SOC 2 access controls and review routing). Dependency automation
configs ensure security patches flow as PRs automatically, addressing the most
common vector for known-vulnerability exploitation. Custom properties sync
reads each repo's Backstage `catalog-info.yaml` and writes ownership metadata
(Owner, Component, JiraProject, JiraLabel) to GitHub repository custom
properties. Wiz consumes these properties to attribute security findings to the
correct team. Repos without a catalog-info.yaml are tagged as `Unclassified` so
they remain visible in security dashboards rather than falling through the
cracks.

**For platform teams:** Prometheus metrics provide organization-wide visibility
into compliance posture. Structured logs create an audit trail of every action.
Dry-run mode enables safe validation before enabling PR creation.

## Metrics That Matter

| Metric | What it tells you |
|---|---|
| Repositories checked per cycle | Coverage across the organization |
| Missing files detected (by rule) | Compliance gap by file type |
| PRs created | Volume of automated remediation |
| Properties checked / set / already correct | Custom properties sync coverage and drift |
| Check duration (p50/p99) | Operational health of the service |

## Cost and Operational Footprint

- **Licensing:** None. Internal tool built on open-source dependencies.
- **Infrastructure:** Single container, ~20 MB image, 100m CPU / 128Mi memory.
- **External dependencies:** GitHub API only. No database, no message broker, no
  third-party SaaS.
- **GitHub API budget:** ~1-3 calls per repository per reconciliation cycle.
  Built-in adaptive rate limiting prevents quota exhaustion.

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Developers ignore or close PRs | The weekly reconciliation re-detects gaps and can re-open. Metrics track PR closure rates for visibility. |
| Rate limiting on large orgs | Configurable worker concurrency, queue backpressure, and pre-emptive rate limit throttling are built in. |
| False positives (file exists in non-standard path) | Each rule checks multiple alternate paths before flagging a file as missing. |
| Template defaults are wrong for a team | PRs are suggestions, not auto-merges. Teams review and customize before merging. |

## Current Status

Production-ready. All implementation phases complete. Two file rules enabled by
default (CODEOWNERS, Dependabot), one additional rule defined and available
(Renovate). Custom properties sync from Backstage `catalog-info.yaml` is
implemented with two operational modes (`github-action` for least-privilege,
`api` for full automation) and is opt-in via the `CUSTOM_PROPERTIES_MODE`
environment variable. Adding new file rules requires a single struct definition
and a template file -- no changes to the core engine.

## Ask

Deploy to the production EKS cluster and install on the GitHub organization.
Begin in dry-run mode to validate behavior, then enable PR creation after one
reconciliation cycle confirms expected results. For custom properties, enable
`CUSTOM_PROPERTIES_MODE=github-action` (or `api` if org-level write permissions
are available) after validating the file-rule behavior.
