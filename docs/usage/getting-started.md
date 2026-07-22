# Getting Started with repo-guardian

A walkthrough for onboarding new users and teams. Read it top to bottom, or
present it section by section — each heading is a self-contained "slide."
For the exhaustive policy schema, see the [Policy Reference](policy-reference.md).

## What is repo-guardian?

**repo-guardian is a GitHub App that keeps every repository in your
organization compliant with your baseline — automatically, continuously, and
via pull requests your teams review like any other change.**

You declare *what a healthy repository looks like* in a single HCL policy
file. repo-guardian watches every repository the App is installed on and:

- **opens PRs** to add missing or drifted configuration files (CODEOWNERS,
  Dependabot, Renovate, `catalog-info.yaml`, …) rendered from templates,
- **checks and optionally remediates** repository settings (wikis, merge
  strategies, vulnerability alerts, …) and branch protection rulesets,
- **syncs metadata** — e.g. Backstage `catalog-info.yaml` ownership into
  GitHub custom properties so downstream tools (Wiz, dashboards, CODEOWNERS
  tooling) can attribute repos to teams,
- **converges**: when a rule becomes satisfied (someone adds the file by
  hand), repo-guardian cleans up after itself — refreshes or auto-closes its
  own PR instead of leaving stale noise.

## The problem it solves

In an org with hundreds or thousands of repositories, configuration drift is
not a possibility — it's a guarantee:

- New repos get created without CODEOWNERS, so review routing and audit
  attribution silently break.
- Dependabot/Renovate configs are copy-pasted once and never updated; half
  the fleet runs a two-year-old workflow.
- "Please enable vulnerability alerts on your repos" emails get sent
  quarterly and actioned never.
- Ownership metadata lives in a spreadsheet (or nowhere), so security
  findings can't be routed to a team.

The usual fixes don't scale: wiki checklists rely on humans remembering,
one-shot scripts fix today's fleet but not next week's new repos, and
org-level GitHub settings can't create *files* in repositories.

repo-guardian's answer: **policy as code, enforcement as pull requests.**
The policy is reviewed and versioned like any other code. The enforcement is
a PR the owning team sees, understands, and merges — not a silent overwrite.

## How it works

```
                          ┌───────────────────────────────┐
  GitHub webhooks ───────▶│                               │
  (repo created,          │   Work queue (Valkey)         │      GitHub API
   App installed,         │      │                        │   ┌─────────────────┐
   push to default        │      ▼                        │   │ open/update PRs │
   branch)                │   Worker pool ──▶ Checker ────┼──▶│ repo settings   │
                          │   (policy engine)             │   │ rulesets        │
  Discovery (hourly) ────▶│      │                        │   │ custom props    │
  Stale sweep ───────────▶│      ▼                        │   │ labels          │
  (re-check repos older   │   Reconcilers                 │   └─────────────────┘
   than freshness window) │   (post-check actions)        │
                          └───────────────────────────────┘
                           State: Postgres (per-repo check state)
```

Four things put a repository on the queue:

1. **Webhooks** — the App is installed, a repo is created, or a repo is added
   to the installation. New repos are checked within seconds.
2. **Push events** — a push to the default branch that touches a *watched*
   file (e.g. the Renovate workflow) triggers an immediate re-check.
3. **Stale sweep** — a leader-elected scheduler periodically re-enqueues any
   repo whose last check is older than the freshness window (default 24h),
   or whose last check ran under an older policy version. Change the policy →
   the whole fleet converges on the next sweep.
4. **Discovery** — an hourly enumeration of installations catches anything
   webhooks missed (e.g. repos that existed before the App was installed).

For each repo, the checker evaluates every rule in the policy, bundles all
actionable file rules into **one PR on one deterministic branch**
(`repo-guardian/add-missing-files`), and runs any attached reconcilers.
Everything is idempotent: re-checking a repo with an open PR updates that PR
in place rather than opening a second one.

## What it can do

**Files** — three check modes, escalating in strictness:

| Mode | Meaning | Example |
|------|---------|---------|
| `exists` | File must be present at one of the listed paths | CODEOWNERS anywhere GitHub honors it |
| `contains` | File must exist **and** pass content assertions (regex, YAML-path) | `renovate.json` must extend the org preset |
| `exact` | File must match the rendered template exactly | The org-standard Renovate workflow, byte-for-byte (YAML compared semantically) |

Missing or non-compliant files produce a PR with the templated default
content. Templates are Go `text/template` with per-repo variables and
helpers — see [Templates in two minutes](#templates-in-two-minutes).

**Repository settings** — 8 properties checked and optionally remediated
directly via the API (no PR — settings aren't files): vulnerability alerts,
default branch, issues, wiki, delete-branch-on-merge, and the three merge
strategies. Each rule chooses `remediate = true` (fix it) or `false` (report
only, visible in logs and metrics).

**Branch protection** — declarative rules (require PR, approval count,
dismiss stale reviews, status checks, linear history) checked and optionally
remediated via the GitHub rulesets API.

**Metadata and post-check reconcilers** — four built-ins that run after a
rule's file checks pass:

- `custom_properties` — parse Backstage `catalog-info.yaml`, sync ownership
  (always) plus any operator-mapped annotations (opt-in via
  `annotation_properties`, e.g. a Jira project key) to GitHub custom
  properties (direct API mode, or a PR-delivered GitHub Action for orgs
  that want changes reviewed). Removed annotations clear the property
  value — a full state sync, not just adds.
- `label_sync` — manage the repo's label set from a YAML file (create,
  recolor, rename, optionally delete extras).
- `branch_protection` — manage rulesets from a YAML file in the repo.
- `workflow_sync` — lightweight observability for watched workflow files;
  pairs with push events for instant drift detection.

**PR experience** — fully templatable titles, bodies, and labels with
three-level inheritance (process defaults → per-rule → per-reconciler).
A sticky, edit-in-place "reconcile log" comment on every guardian PR shows
per-rule status. When every rule a PR addresses becomes satisfied on the
default branch, the PR is auto-closed and its branch deleted (opt-out
available for compliance workflows that require human close-out).

**Targeting** — glob-based ignore lists (global and per-rule), skip-forks and
skip-archived toggles, and per-org scoping: one policy file can serve 20
orgs, with shared rules scoped to `["*"]` and org-specific rules scoped to
subsets.

**Operations** — dry-run mode (log everything, change nothing), 40+
Prometheus metrics with per-org labels, starter alert rules, rate-limit
budgeting per installation (the sweep backs off before you hit GitHub's
limits), and multi-replica HA via Postgres state + Valkey queue and leader
election.

## What it doesn't do

Setting these expectations up front avoids the most common misunderstandings:

- **GitHub only (today).** GitLab and Forgejo backends have been
  investigated (INV-0002/0004/0007) but not built. One binary serves one
  GitHub App; that App can span many orgs.
- **It proposes, humans dispose — for files.** File compliance always
  arrives as a PR. repo-guardian never commits to the default branch and
  never merges its own PRs. (Settings and branch-protection remediation are
  the exception — those are direct API writes, and each rule opts in via
  `remediate = true`.)
- **Central policy only.** There is one policy per deployment, owned by the
  platform team. Repos cannot override rules with an in-repo dotfile — by
  design; exemptions are ignore-list entries in the reviewed, versioned
  policy, not per-repo opt-outs.
- **Not a scanner.** It checks the configuration files and settings you
  declare rules for. It does not scan code, secrets, or dependencies — it's
  the thing that makes sure your *scanners' config files* exist everywhere.
- **Not a blocking gate.** It won't stop a merge or fail a check on
  non-compliance. It nags via PRs and reports via metrics. Pair it with
  branch protection (which it can configure!) if you need hard gates.
- **Templates are content defaults, not per-repo intelligence.** A generated
  CODEOWNERS is a sensible starting point with per-repo variables filled in —
  the owning team still reviews and adjusts it in the PR.
- **One App credential.** All orgs are served by the same GitHub App key
  (per-org credentials investigated in INV-0006, deferred).

## The policy: a five-minute tour

Everything repo-guardian does is driven by one HCL file (or a directory of
them), pointed to by `GUARDIAN_CONFIG`. A representative policy:

```hcl
guardian {
  dry_run       = false
  skip_forks    = true
  skip_archived = true
}

# Repos no rule should ever touch (glob patterns).
ignore {
  repos = ["myorg/terraform-*", "myorg/archive-*"]
}

# Process-wide PR text. Every rule inherits this unless it overrides.
defaults {
  pr {
    title  = "chore: repo-guardian baseline for {{ .Repo }}"
    labels = ["automated", "guardian"]
  }
}

# Simplest rule: the file just has to exist somewhere GitHub honors it.
rule "file" "codeowners" {
  check    = "exists"
  paths    = ["CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"]
  target   = ".github/CODEOWNERS"   # where the PR creates it if missing
  template = "codeowners"           # which template renders the content
}

# Content-checked rule: exists AND extends the org preset.
rule "file" "renovate_config" {
  check    = "contains"
  paths    = ["renovate.json", ".github/renovate.json"]
  target   = "renovate.json"
  template = "renovate"

  assertion {
    pattern = "github>myorg/renovate-config"
    message = "renovate.json must extend the org preset"
  }
}

# File rule + reconciler: after catalog-info checks pass, sync its
# ownership metadata to GitHub custom properties.
rule "file" "catalog_info" {
  check    = "contains"
  paths    = ["catalog-info.yaml", "catalog-info.yml"]
  target   = "catalog-info.yaml"
  template = "catalog-info"

  assertion {
    yaml_path = "spec.owner"
    non_empty = true
    message   = "spec.owner must be set"
  }

  reconcile "custom_properties" {
    mode  = "api"
    watch = true   # pushes touching this file trigger instant re-checks

    # Optional: map additional catalog-info annotations to GitHub custom
    # properties. Owner/Component sync unconditionally; this map is how
    # you opt more annotations into the same sync + clear-on-removal
    # behavior.
    annotation_properties = {
      "jira/project-key" = "JiraProject"
    }
  }
}

# Settings are checked and (here) fixed directly — no PR.
rule "setting" "vuln_alerts" {
  property  = "vulnerability_alerts_enabled"
  expected  = true
  remediate = true
}

# Branch protection via the rulesets API.
rule "branch_protection" "main" {
  branch             = "main"
  require_pr         = true
  required_approvals = 1
  remediate          = true
}
```

The mental model:

- **`rule` blocks are the policy.** Three types: `file`, `setting`,
  `branch_protection`. Each is independently enabled, ignorable, and
  scopeable.
- **`assertion` blocks make `contains` rules precise** — regex over the raw
  file, or structural checks against a YAML path.
- **`reconcile` blocks attach behaviors** that run after a file rule's
  checks pass.
- **`pr` blocks control what humans see**, with inheritance so you write the
  boilerplate once.
- **If you provide an HCL file, it replaces the built-in defaults
  entirely** — you declare everything you want. No file at all gives you the
  built-in starter policy (CODEOWNERS + Dependabot enabled; Renovate rules
  present but disabled).

Ready-to-adapt policies live in [`examples/`](https://github.com/donaldgifford/repo-guardian/tree/main/examples),
from `guardian-minimal.hcl` to a multi-org enterprise layout.

## Templates in two minutes

The file content that lands in PRs comes from templates: five embedded
defaults (CODEOWNERS, Dependabot, Renovate config + workflow, catalog-info)
that operators can override or extend by mounting files into `TEMPLATE_DIR`
(the Helm chart exposes this as a simple `templates.files` values map).

Templates are Go `text/template` over per-repo variables:

```text
# {{ .Repo }} is owned by the platform team
* @{{ .Owner }}/platform-team
```

PR titles and bodies are templates too, with extras like `{{ .Files }}` (the
list of paths in the PR) and Backstage catalog fields for catalog-aware
rules. A curated helper set (`env`, `default`, `join`, `lower`, `upper`,
`title`) covers common needs — e.g. Jira-prefixed PR titles via
`{{ env "JIRA_PROJECT" | default "PLAT" }}`. Full details in the
[Policy Reference](policy-reference.md#template-reference).

## Rolling it out (demo script)

The safe on-ramp, and a natural live-demo sequence:

1. **Install the GitHub App** on one org, scoped to a couple of test repos.
   The App needs a public HTTPS webhook endpoint.
2. **Deploy the Helm chart** (`oci://ghcr.io/donaldgifford/charts/repo-guardian`)
   with `DRY_RUN=true`. Out of the box the chart brings its own Postgres and
   Valkey. Note: the `DRY_RUN` env var wins over the policy file — handy as
   an operator safety catch.
3. **Watch the logs** — dry-run logs every PR it *would* open and every
   setting it *would* change. This is where the policy gets tuned: add
   ignore patterns, adjust paths, fix assertions.
4. **Flip dry-run off.** Delete a CODEOWNERS from a test repo, push to a
   watched file, or wait for the sweep — then walk through the PR it opens:
   templated content, labels, the sticky reconcile-log comment.
5. **Show convergence**: hand-add the missing file on the default branch and
   re-check — repo-guardian auto-closes its own PR and deletes its branch.
6. **Scale out**: widen the App installation, add orgs to the policy `scope`,
   and let discovery + the stale sweep bring the fleet in gradually — the
   rate-limit budget gate paces enqueues so a 2,000-repo onboarding doesn't
   burn the API budget in one tick.

## Where to go next

- **[Policy Reference](policy-reference.md)** — every block, attribute,
  default, and validation rule in the HCL policy.
- **[`examples/`](https://github.com/donaldgifford/repo-guardian/tree/main/examples)** — runnable policies from
  minimal to multi-org enterprise.
- **[Adding a New Rule](../ADDING_RULES.md)** — extend repo-guardian itself
  with new rule types or reconcilers.
- **[Helm chart README](https://github.com/donaldgifford/repo-guardian/tree/main/charts/repo-guardian)**
  — deployment shapes (baked/CNPG/external Postgres, Valkey modes),
  values, and security posture.
- **[Operations docs](../operations/scaling.md)** — scaling, metrics,
  alerts, and migration runbooks.
