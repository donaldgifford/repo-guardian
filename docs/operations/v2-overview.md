# repo-guardian and v2 at a glance

What repo-guardian enforces and why that matters, what v2 changes, what
it deliberately does not, and what that means for a policy you are
editing today. Every attribute is specified in the
[policy reference](../usage/policy-reference.md). The step-by-step
cutover is in
[v1 → v2 migration](v2-migration.md). The designs are
[DESIGN-0025](../design/0025-v2-findings-model-and-v1-data-migration.md)
(findings), [DESIGN-0026](../design/0026-v2-temporal-control-plane-and-role-split.md)
(runtime) and
[DESIGN-0027](../design/0027-v2-read-only-api-business-ui-and-status-page.md)
(API and UI).

<!--toc:start-->
- [Why it matters](#why-it-matters)
- [What repo-guardian enforces](#what-repo-guardian-enforces)
  - [File rules](#file-rules)
  - [Content assertions](#content-assertions)
  - [Conditional rules](#conditional-rules)
  - [Repository settings](#repository-settings)
  - [Branch protection](#branch-protection)
  - [Reconcilers](#reconcilers)
  - [Who a rule applies to](#who-a-rule-applies-to)
  - [How it fixes things](#how-it-fixes-things)
  - [Rolling out a new rule safely](#rolling-out-a-new-rule-safely)
- [What does not change](#what-does-not-change)
- [Writing rules before you migrate](#writing-rules-before-you-migrate)
- [Where v2 is a real improvement](#where-v2-is-a-real-improvement)
  - [Compliance you can trust](#compliance-you-can-trust)
  - [A durable control plane instead of a hand-rolled one](#a-durable-control-plane-instead-of-a-hand-rolled-one)
  - [One rate budget per installation, with priorities](#one-rate-budget-per-installation-with-priorities)
  - [Tuning no longer re-checks the fleet](#tuning-no-longer-re-checks-the-fleet)
  - [Policy changes roll out, they do not stampede](#policy-changes-roll-out-they-do-not-stampede)
  - [Least privilege by role](#least-privilege-by-role)
  - [Webhooks that fail loudly and deduplicate](#webhooks-that-fail-loudly-and-deduplicate)
  - [Repositories keep their identity across renames and transfers](#repositories-keep-their-identity-across-renames-and-transfers)
  - [Parking that never guesses](#parking-that-never-guesses)
  - [Answers for people who do not read PromQL](#answers-for-people-who-do-not-read-promql)
  - [Safe rollouts of the service itself](#safe-rollouts-of-the-service-itself)
- [Summary](#summary)
<!--toc:end-->

## Why it matters

A standard that nobody checks is a suggestion. Across hundreds of
repositories and several organisations, the baseline everyone agrees on
drifts as soon as it is written down:

- **Ownership.** A repository with no `CODEOWNERS` has no required
  reviewer and no one to page. A repository with no `catalog-info.yaml`
  is missing from the service catalogue, so nobody can say who owns it,
  what it runs or which Jira project its bugs go to.
- **Supply chain.** A repository without Dependabot or Renovate stops
  receiving dependency and security updates silently. One configured
  against the wrong preset receives the wrong ones. A repository with
  vulnerability alerts turned off does not tell anyone about a known CVE.
- **Change control.** Without branch protection, a single push can put
  unreviewed code on `main`, skip CI, or rewrite history. Merge settings
  that allow merge commits on a linear-history repository slowly make
  its history unreadable.
- **Audit evidence.** Frameworks such as SOC 2 and ISO 27001 ask for
  evidence that change control and dependency management are applied to
  every in-scope system, continuously. A spreadsheet assembled before
  the audit is out of date on the day it is finished.

repo-guardian turns the baseline into code (`guardian.hcl`), checks every
repository against it continuously, and fixes what it can: by pull
request for files, so a human reviews every change to a repository's
contents, and by direct API write for settings and rulesets, but only
where the policy says so. The result is the set of findings the
business asks for (who is compliant with what, since when, and why
not) without anyone building that spreadsheet.

## What repo-guardian enforces

Five kinds of control, all declared in one `guardian.hcl`. Each example
below is valid policy; the full attribute tables are in the
[policy reference](../usage/policy-reference.md).

### File rules

A file rule names candidate `paths` and a check mode. Four modes:

| Mode | Holds when | Remediation | Typical use |
| --- | --- | --- | --- |
| `exists` | any candidate path exists | PR adding the file from a template | `CODEOWNERS`, `dependabot.yml`, `SECURITY.md` |
| `contains` | the file exists **and** every assertion passes | PR writing the template to `target` | Renovate must extend the org preset; `catalog-info.yaml` must name an owner |
| `exact` | the file equals the rendered template (YAML compared semantically) | PR writing the template to `target` | an org-standard workflow nobody should hand-edit |
| `absent` | **none** of the paths exist | PR deleting them | retire `dependabot.yml` once Renovate is in place; ban committed `.env` files |

```hcl
rule "file" "codeowners" {
  paths    = ["CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners"
}
```

For `contains` and `exact`, the PR replaces the contents of a file that
exists but fails. That is a deliberate choice of a PR over a direct
write: the repository's owners see the diff and can push back.

Templates are Go `text/template` files. Six ship embedded (`codeowners`,
`dependabot`, `renovate`, `renovate-workflow`, `catalog-info`,
`set-custom-properties`), and operators add their own through the
chart's `templates.files` map. A template can render per-repository
values: the org and repository name, the default branch and the rule,
and, for catalogue-aware templates, the repository's Backstage entry.

**Why:** these are the files the rest of your tooling depends on. Code
review routing reads `CODEOWNERS`. Update bots read their config. The
service catalogue reads `catalog-info.yaml`. A missing file does not
fail loudly; it just quietly turns that tooling off for one repository.

### Content assertions

A `contains` rule checks what is inside the file, with regular
expressions over the raw text or structural checks on YAML:

```hcl
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

rule "file" "catalog_info" {
  check    = "contains"
  paths    = ["catalog-info.yaml", "catalog-info.yml"]
  target   = "catalog-info.yaml"
  template = "catalog-info"

  assertion {
    yaml_path = "spec.owner"
    non_empty = true
    message   = "spec.owner must name a team"
  }
}
```

| Assertion | Passes when |
| --- | --- |
| `pattern` | the regex matches somewhere in the file |
| `not_pattern` | the regex matches nowhere (for example, no `TODO` placeholders) |
| `yaml_path` + `equals` / `contains` / `non_empty` | the value at that YAML path equals, contains, or is present and non-empty |

**Why:** a file that exists but is wrong is worse than one that is
missing, because it looks compliant. An update bot pointed at the wrong
preset, or a catalogue entry with an empty owner, passes an `exists`
check and fails everyone who relies on it.

### Conditional rules

A `when` block makes one file rule depend on another being satisfied on
the default branch. The main use is a safe migration between tools:

```hcl
rule "file" "no_dependabot" {
  check = "absent"
  paths = [".github/dependabot.yml", ".github/dependabot.yaml"]

  when {
    rule_satisfied = "renovate_config"
  }
}
```

Dependabot is removed only from repositories where Renovate is already
configured correctly, so no repository is ever left with neither. Gates
read the default branch only, never an unmerged PR, and they fail
closed: if the referee cannot be evaluated, the gated rule does nothing.

**Why:** the dangerous moment in any fleet-wide change is the middle,
when some repositories have the new thing and some still have the old.
Gating removes the window where a repository has neither.

### Repository settings

A setting rule compares one repository setting with an expected value:

```hcl
rule "setting" "vulnerability_alerts" {
  property  = "vulnerability_alerts_enabled"
  expected  = true
  remediate = true
}
```

| Property | Expected | Why you might enforce it |
| --- | --- | --- |
| `vulnerability_alerts_enabled` | bool | Dependabot alerts for known CVEs in dependencies |
| `default_branch` | string | tooling, protection rules and docs all assume one name |
| `has_issues` | bool | send issues to the tracker you actually triage |
| `has_wiki` | bool | stop documentation fragmenting into unreviewed wikis |
| `delete_branch_on_merge` | bool | no graveyard of stale branches |
| `allow_merge_commit` | bool | enforce a merge strategy... |
| `allow_squash_merge` | bool | ...so history stays readable |
| `allow_rebase_merge` | bool | ...across every repository |

Settings are not files, so remediation is a direct API write, and only
when `remediate = true`. With `remediate = false` the rule only reports,
which is how you survey the fleet before changing anything.

### Branch protection

A branch-protection rule describes the protection a branch must have,
and is enforced through GitHub's rulesets API:

```hcl
rule "branch_protection" "main" {
  branch                 = "main"
  require_pr             = true
  required_approvals     = 1
  dismiss_stale_reviews  = true
  require_status_checks  = ["ci/test"]
  require_linear_history = true
  remediate              = true
}
```

It can require a pull request, a number of approvals, re-approval after
new commits, named status checks and linear history, and whether admins
are bound too. A repository without the target branch is recorded as not
applicable, not as a failure.

**Why:** this is the control auditors ask about first. Every other rule
is only as strong as the guarantee that changes to `main` are reviewed
and tested.

### Reconcilers

Reconcilers attach to a file rule and act on its contents once the file
passes its checks. They turn a file in the repository into GitHub state:

| Reconciler | Reads | Does |
| --- | --- | --- |
| `custom_properties` | Backstage `catalog-info.yaml` | sets the GitHub custom properties `Owner` and `Component`, plus any annotation you map (a Jira project, a cost centre), by API or by a reviewed workflow PR |
| `label_sync` | a labels YAML file | creates, updates and renames labels, and optionally deletes unlisted ones |
| `branch_protection` | a rulesets YAML file | per-repository branch protection declared in the repository itself |
| `workflow_sync` | an org-standard workflow | reports when someone edits it |

With `watch = true`, a push to the default branch that touches the file
triggers a re-check immediately rather than at the next scheduled pass.

**Why:** GitHub custom properties are what org-wide rulesets, search
and reporting select on. Driving them from the catalogue makes the
catalogue the single source of truth for ownership, instead of a second
copy that drifts from it.

### Who a rule applies to

- **`scope { orgs = [...] }`** limits a rule to some organisations. Once
  a top-level scope is declared, every rule must declare its own, so a
  new org is never enforced by accident.
- **`ignore { repos = [...] }`**, at the top level or inside one rule,
  skips repositories by glob (`myorg/terraform-*`).
- **Archived and forked repositories** are parked (when `skip_archived`
  and `skip_forks` are on, the default): they are not checked while they
  stay archived or forked.

### How it fixes things

- **One pull request per repository.** Every file change for that
  repository lands on a single branch, `repo-guardian/add-missing-files`,
  and a single PR is updated in place as the findings change. The PR
  title, body and labels are templated from the policy.
- **It gets out of the way.** If someone else already has a PR open for
  the same thing (`search_terms`), repo-guardian leaves that rule alone.
- **It cleans up after itself.** When every rule in its PR is satisfied
  on the default branch, the PR is closed and the branch deleted. A rule
  that stops being actionable has its file removed from the branch. A
  sticky comment on the PR logs what changed on each pass.
- **It reacts to change.** New repositories, installations and pushes
  that touch watched files trigger a check within seconds. Everything
  else is re-checked on a schedule.

### Rolling out a new rule safely

1. Add the rule so it cannot write yet. Setting and branch-protection
   rules take `remediate = false`. File rules have no per-rule switch,
   so either run the whole policy with `dry_run = true` or `scope` the
   new rule to a test organisation first.
2. Read the findings: the report, the API or the dashboards show how
   many repositories fail and why.
3. Add `ignore` entries for the legitimate exceptions.
4. Turn remediation on. File rules open PRs for owners to review;
   settings and rulesets are applied.

## What does not change

- **The policy language.** `guardian.hcl` is read by the same loader:
  file rules (`exists`, `contains`, `exact`, `absent`), `when` gates,
  setting and branch-protection rules, `scope`, `ignore`, `pr {}`
  templates and every reconciler. The GitHub actions the engine takes are
  pinned identical by the parity suite
  (`internal/checker/testdata/parity/`).
- **Pull requests.** The branch `repo-guardian/add-missing-files`, the PR
  title, and the reconcile-log marker and hash tag are frozen by
  `TestPRIdentity_IsFrozen`. v2 adopts v1's open PRs, and v1 adopts v2's
  on rollback.
- **The webhook.** The same URL, secret and Service name.
- **The database.** v2 adds tables beside v1's and never writes v1's,
  which is what makes rollback work.

## Writing rules before you migrate

Add rules to v1 now. Nothing needs rewriting for v2: every engine fix
lands on `main` first and is merged forward into `v2`, so any rule v1
accepts, v2 accepts and evaluates the same way.

Four things to know:

1. **Give every rule a unique name across kinds.** v1 keeps one posture
   row per name, so if a file rule and a setting rule share a name, the
   last kind evaluated wins. v2 keeps a separate finding per (kind,
   name) and logs a warning at load. Nothing breaks, but distinct names
   keep the two versions' numbers comparable.
2. **`guardian { worker_count, queue_size, schedule_interval }` are v1
   knobs.** v2 still parses them and ignores them. Its equivalents are
   the `WORKER_ACTIVITY_CONCURRENCY` and `CHECK_INTERVAL` environment
   variables. Delete them from the file when you cut over.
3. **A new rule costs one fleet pass on v1.** v1's policy version hashes
   the whole configuration, so any change makes every repository stale.
   The next stale sweep re-checks all of them. Cutover re-checks
   everything anyway, spread across `policyRolloutWindow`, so a rule
   added now costs nothing extra at cutover.
4. **Let a PR-heavy rule settle before cutting over.** If the new rule
   opens many PRs, give v1 a sweep or two to converge, so v2's first pass
   is not doing the migration re-check and the rule's churn at the same
   time.

## Where v2 is a real improvement

### Compliance you can trust

v1 records one boolean per repository and rule: `actionable` or not.
Everything that is not actionable reads as compliant, including cases
that are not.

| Case | v1 says | v2 says |
| --- | --- | --- |
| Someone else's PR is open for the rule | compliant | `non_compliant`, reason `foreign_pr_open` |
| Branch-protection target branch does not exist | compliant | `not_applicable`, reason `branch_missing` |
| Rule out of scope, ignored, or its gate closed | no row | `not_applicable`, with the reason and matching pattern |
| The gate's referee rule errored | no row | `unknown`, reason `gate_error`, shown rather than hidden |
| A file rule and a setting rule share a name | one row, last kind wins | two findings |

Every v2 finding carries a status, a reason, a remediation and typed
evidence. The reason says exactly what is wrong for each kind of rule:

| Rule kind | Non-compliant reasons |
| --- | --- |
| file | `file_missing`, `assertion_failed`, `content_differs`, `forbidden_present` |
| setting | `setting_mismatch` |
| branch protection | `ruleset_missing`, `ruleset_mismatch` |
| any | `foreign_pr_open` |

Not-applicable findings say why too: `out_of_scope_policy`,
`out_of_scope_rule`, `ignored_global`, `ignored_rule`, `gate_closed`,
`branch_missing` or `empty_repository`. The remediation records what was
done about it: `pr_open`, `applied`, `dry_run`, `disabled` (report-only)
or `foreign_pr`.

Each finding also has an append-only history (`finding_events`), so
"when did this repository start failing, and what changed?" has an
answer.

The percentage is computed in one place. A single SQL query
(`ComplianceByRule`) feeds the report, the API and the daily snapshots,
and a test pins all of them to the same number. An empty denominator is
*unmeasured*, never 100%: a dead exporter cannot report a perfect fleet.

### A durable control plane instead of a hand-rolled one

v1 runs its own job system: a Valkey LIST, an in-flight ZSET, a delayed
ZSET, a reaper that redelivers stuck jobs, and SETNX leader election for
the sweeps. Every invariant is hand-maintained Lua. The reaper once
cloned still-running jobs every interval while the rate limiter slept
in-handler, amplifying load against an already exhausted budget
(INV-0012).

v2 hands all of that to Temporal. Each repository is a long-lived
`RepoWorkflow` that schedules its own next check, coalesces signals that
arrive during a check, and survives pod restarts mid-check. Schedules
need no leader. Retries, timeouts and deferrals are workflow semantics
rather than queue bookkeeping, and a deferral never spends a retry
attempt. Valkey is gone.

### One rate budget per installation, with priorities

v1 grew four throttling mechanisms that did not compose, and one of them
(the BudgetTracker) never gated anything in production. IMPL-0022
reduced that to one reactive mechanism: defer when throttled.

v2 plans ahead instead. An `InstallationWorkflow` per GitHub installation
holds its budget: remaining calls, reset time, outstanding leases, and a
moving estimate of what a check costs. A check must acquire a lease
before it spends. Webhook and push checks (priority ≤ 2) see half the
reserve, which is how a human-triggered check jumps the queue during a
full-fleet re-check. A denied check waits until the reset, spread by
lease number so the fleet does not wake all at once.

### Tuning no longer re-checks the fleet

v1 hashes the entire configuration into its policy version, including
`log_level` and `worker_count`. Raising concurrency to drain a backlog
therefore makes every repository stale, which creates a backlog.

v2's `VersionV2` hashes only the fields that can change an outcome or an
action. Operational knobs are excluded, and a test fails if a new policy
field is not explicitly classified as one or the other.

### Policy changes roll out, they do not stampede

In v1 a policy change makes every repository stale at once, and the
sweep works through them as fast as the rate limit allows.

In v2 a new policy version starts a `policy-rollout` workflow. It gives
every repository a due time spread across `policyRolloutWindow` (24h by
default), then re-signals stragglers once. A newly discovered repository
gets its first check spread across `CHECK_INTERVAL` in the same way.

### Least privilege by role

v1 is one binary holding everything: the App private key, the webhook
secret and the database DSN, on a pod exposed to the internet.

v2 splits into roles:

- **ingest** receives webhooks. It holds the webhook secret and a
  Temporal connection, and **refuses to start** if given the App key or
  a store DSN, so a compromised ingest pod has neither.
- **worker** runs checks. It holds the App key and the store.
- **api** reads compliance through a read-only Postgres role, over a
  pool that enforces read-only transactions.

The chart scopes each Secret to the roles that need it, and a
helm-unittest suite asserts every absence.

### Webhooks that fail loudly and deduplicate

v1 answers `202 Accepted` even when the enqueue to Valkey fails: it
logs the error and counts it, but GitHub records a successful delivery,
so there is nothing to redeliver. Redeliveries are not deduplicated
either.

v2's ingest starts a workflow keyed on `X-GitHub-Delivery`:

- **Redeliveries are free.** A duplicate delivery ID is a no-op
  success.
- **Failures are visible.** If Temporal cannot accept the event, ingest
  returns 503 and GitHub records the delivery as failed and
  redeliverable.
- **Uninteresting events stop at the edge.** Tag pushes, non-default
  branches and pushes that touch no watched path get a 204 before any
  work is scheduled.

### Repositories keep their identity across renames and transfers

v1 keys a repository by `owner/name`. A rename looks like one repository
disappearing and another appearing.

v2 matches on GitHub's repository ID first. A rename or transfer keeps
the same row, the same findings history and the same workflow, and
writes a `renamed` or `transferred` event. A name now held by a
*different* repository ID is refused as a conflict rather than guessed
at.

### Parking that never guesses

Both versions park repositories they cannot or should not check:
archived, forked, or with access lost. v2 adds a guard on removal.
Discovery parks a repository as `removed` only after a *complete*
listing of its installation, so a failed or partial API listing parks
nothing. The subset invariant, that the check path only parks what
discovery also filters, is proved by a test that drives the real
engine.

### Answers for people who do not read PromQL

v1's compliance lives in leader-published Prometheus gauges. Reading them
correctly takes `max by` rather than `sum` (or every failover
double-counts), and "which repository?" is answered only by logs.

v2 adds:

- **A read-only REST API** (OpenAPI 3.1, OIDC, per-org authorization)
  over findings, repositories, rules, organisations and history.
- **A web UI** for the business view: compliance by rule and org, drill
  down to a repository's findings and their history.
- **A public status page**, privacy-checked by a test so it cannot leak
  a repository or org name.

Generated Grafana dashboards and alerts still exist, and can now be
scoped to one install (`--prometheus-selector`, `--name`).

### Safe rollouts of the service itself

v2 workers use Temporal worker versioning. A worker promotes its own
build to the deployment's current version only when it is newer by
semver, so a crash-looping pod of the previous release cannot pull a
rollout backwards. A worker whose build is not current fails readiness
rather than sitting idle unnoticed. Workflow code is replay-tested
against captured histories in CI, so a change that would corrupt
in-flight workflows fails the build instead of production.

## Summary

| | v1 | v2 |
| --- | --- | --- |
| Outcome per rule | `actionable` boolean | status, reason, remediation, evidence, history |
| Job system | Valkey queue, reaper, Lua, SETNX leader | Temporal workflows and schedules |
| Rate limiting | reactive defer | per-installation budget, leases, priorities |
| Tuning a knob | re-checks the fleet | changes nothing |
| Policy change | every repository stale at once | rolled out over a window |
| Secrets | one pod holds all | scoped per role; ingest holds no key |
| Webhook failure | 202 anyway; lost | 503, redeliverable, deduplicated |
| Rename or transfer | new repository | same repository, event recorded |
| Consumers | Prometheus and logs | API, UI, status page, plus Prometheus |
| Policy file | `guardian.hcl` | the same `guardian.hcl` |
