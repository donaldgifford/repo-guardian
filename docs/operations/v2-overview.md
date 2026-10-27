# repo-guardian and v2 at a glance

What repo-guardian enforces and why it matters, how v2 changes it, and
what that means for a `guardian.hcl` you run today. The policy language
is specified in the [policy reference](../usage/policy-reference.md),
every control type in the generated
[control-type reference](../usage/controls-reference.md), and the
cutover in [v1 → v2 migration](v2-migration.md). The designs are
[DESIGN-0029](../design/0029-controls-and-policies-opinionated-compliance-with-separate.md)
(controls and the two Apps),
[DESIGN-0030](../design/0030-policy-model-enterprise-and-org-policies-and-control-assignment.md)
(the policy model),
[DESIGN-0031](../design/0031-control-framework-controls-control-rules-and-file-controls.md)
(the control framework) and
[DESIGN-0032](../design/0032-evaluation-and-remediation-workflows-change-driven-remediation.md)
(evaluation and remediation).

<!--toc:start-->
- [Why it matters](#why-it-matters)
- [The short version](#the-short-version)
- [What repo-guardian enforces](#what-repo-guardian-enforces)
  - [Controls and rules](#controls-and-rules)
  - [The built-in control types](#the-built-in-control-types)
  - [Who a control applies to](#who-a-control-applies-to)
  - [Evaluate and remediate](#evaluate-and-remediate)
  - [How it fixes things](#how-it-fixes-things)
  - [Rolling out a new control safely](#rolling-out-a-new-control-safely)
- [What changed from guardian.hcl](#what-changed-from-guardianhcl)
  - [Policy compatibility](#policy-compatibility)
  - [Rule kinds become control types](#rule-kinds-become-control-types)
  - [Pull requests](#pull-requests)
  - [What was dropped](#what-was-dropped)
  - [Names that changed](#names-that-changed)
- [The status model](#the-status-model)
- [Where v2 is a real improvement](#where-v2-is-a-real-improvement)
  - [Compliance you can trust](#compliance-you-can-trust)
  - [A durable control plane instead of a hand-rolled one](#a-durable-control-plane-instead-of-a-hand-rolled-one)
  - [One rate budget per installation, per App](#one-rate-budget-per-installation-per-app)
  - [Cosmetic edits re-evaluate nothing](#cosmetic-edits-re-evaluate-nothing)
  - [Policy changes roll out, they do not stampede](#policy-changes-roll-out-they-do-not-stampede)
  - [Least privilege by App and by role](#least-privilege-by-app-and-by-role)
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
- **Change control.** Without a branch ruleset, a single push can put
  unreviewed code on `main`, skip CI, or rewrite history.
- **Audit evidence.** Frameworks such as SOC 2 and ISO 27001 ask for
  evidence that change control and dependency management are applied to
  every in-scope system, continuously. A spreadsheet assembled before
  the audit is out of date on the day it is finished.

repo-guardian turns the baseline into code, evaluates every repository
against it continuously, and records the answer the business asks for:
who is compliant with which control, since when, and why not. Where the
policy says so, it also fixes what it finds.

## The short version

- **Policy is a directory, not a file.** `enterprise.hcl` names the
  organisations and the controls every repository gets, `catalogue/`
  defines the controls, `orgs/` adds per-org controls, exclusions and
  overrides, and `templates/` overrides the embedded file templates.
  `guardian.hcl` is not read by v2.
- **The unit is a control.** A control (`codeowners@1`) has a type, a
  version and a set of rules. Results, compliance and history are per
  control, and per rule inside it.
- **Two GitHub Apps.** The Evaluation App reads. The Remediation App
  writes, and only to repositories you grant it.
- **Nothing is written until you say so.** Every assignment evaluates;
  only one in `mode = "remediate"`, on a repository the Remediation App
  can reach, opens pull requests or writes through the API.

## What repo-guardian enforces

### Controls and rules

A control is defined once in the catalogue and assigned by name and
version:

```hcl
# catalogue/codeowners.hcl
control "codeowners" {
  version     = 1
  type        = "codeowners"
  title       = "CODEOWNERS"
  description = "A CODEOWNERS file exists in one of GitHub's locations"
  template    = "codeowners"

  rule "exists" {
    title     = "A CODEOWNERS file exists"
    remediate = true
  }
}
```

```hcl
# enterprise.hcl
enterprise {
  orgs     = ["myorg"]
  mode     = "evaluate"
  controls = ["codeowners@1"]
}
```

Each rule has a `kind` the control type declares (it defaults to the
rule id when the id names a kind) and the parameters that kind takes.
The control's status is derived from its rules' statuses, so a control
with one failing rule is non-compliant and the result says which rule.
`remediate = true` marks a rule repo-guardian may fix; a rule without it
is reported only.

### The built-in control types

| Type | Checks | Remediated by |
| --- | --- | --- |
| `file` | one file exists, matches a template, or is absent | pull request |
| `codeowners` | a `CODEOWNERS` file in one of GitHub's locations | pull request |
| `catalog_info` | `catalog-info.yaml` is a Backstage Component with the fields you require and no placeholders | pull request |
| `dependency_updates` | Renovate or Dependabot is configured, extends your preset, and is the only bot | pull request |
| `repo_settings` | repository settings equal expected values | API (`apply`) |
| `branch_ruleset` | a named repository ruleset exists and matches the declaration | API (`apply`) |
| `labels` | labels are present with the right colour and description, or absent | API (`apply`) |
| `custom_properties` | GitHub custom properties match `catalog-info.yaml` and the org schema | API or a workflow PR (`apply`) |

The parameters and rule kinds of each are in the
[control-type reference](../usage/controls-reference.md), which is
generated from the types themselves and cannot drift from the code.

**Why:** these are the files and settings the rest of your tooling
depends on. Review routing reads `CODEOWNERS`, update bots read their
config, the service catalogue reads `catalog-info.yaml`, and org-wide
rulesets, search and reporting select on custom properties. A missing or
wrong one does not fail loudly; it quietly turns that tooling off for
one repository.

### Who a control applies to

- **`enterprise.orgs`** lists every organisation repo-guardian manages.
  A repository in any other organisation is unmanaged: it is never
  evaluated and never counts.
- **`enterprise.controls`** assigns controls to every managed
  repository. An org file's `controls` adds more for that org, and a
  `repos "<name>" { match = [...] }` block adds or replaces controls for
  a set of repositories.
- **`exclude_repos`** and per-control `exclude` blocks take repositories
  or controls out of scope, and each requires a `reason`. An exclusion
  is recorded and reported with its reason; it is not a silent skip.
- **Archived and forked repositories** are parked: not evaluated while
  they stay archived or forked, and their results cleared so they do not
  count.

### Evaluate and remediate

`mode` is set at the enterprise, org, repository-set or per-control
level, and the most specific setting wins:

- **`evaluate`** records results and changes nothing.
- **`remediate`** also fixes `remediate = true` rules, but only where
  the Remediation App has access to the repository through an
  installation that is not suspended. Elsewhere the assignment runs as
  `evaluate`, with the reason (`remediation_app_no_access` or
  `remediation_app_suspended`) recorded on it and shown in the UI. That
  is not an error, and granting the App the repository lifts it without
  a policy edit.

API-remediated controls also choose how they write, with
`remediation { apply = ... }` in the catalogue:

| `apply` | Does |
| --- | --- |
| `recommend` (default) | records the before and after values as a recommendation; writes nothing |
| `direct` | the remediator writes the change through the API, reads it back, and journals it per resource |
| `workflow` | a pull request adds `.github/workflows/repo-guardian-<slug>.yml`, which applies the change on merge (`custom_properties` only) |

### How it fixes things

The remediator acts as the Remediation App, one run at a time per
repository and control, and only for assignments in `remediate` mode.

- **One pull request per control.** Each control that needs file changes
  gets its own branch, `repo-guardian/<control slug>`, and its own PR,
  updated in place as the results change. The branch carries no
  version, so a control version bump updates the same PR. A reviewer
  sees one concern per PR, and merging the Renovate PR never waits on
  the CODEOWNERS one.
- **Bounded noise.** `remediation { max_open_prs }` (default 3) caps how
  many repo-guardian PRs a repository has open at once; the rest wait
  in policy order, held as `pr_cap`, and go out as PRs merge or close.
  `reopen_after` (default 14 days) is how long a PR a human closed stays
  closed while the control still fails; to stop it for good, exclude the
  control for that repository.
- **Humans win.** A human may push to repo-guardian's PR. The remediator
  merges main into it, keeps human edits, reverts only its own obsolete
  changes, and never force-pushes. It adopts an existing branch only
  when the open PR on it was authored by the Remediation App's bot.
- **It closes what it opened.** A PR is closed `closed_compliant` when a
  fresh read of main shows the control passing (a human's own fix, for
  instance), and `closed_withdrawn` when the control is no longer
  assigned, each with a comment. A merged PR is the end of that run.
- **Holds say why nothing happened.** A control that cannot be
  remediated right now is held with a reason the UI and
  `/remediations?hold=` show:

  | Hold | Means | Clears when |
  | --- | --- | --- |
  | `pr_cap` | the repository is at `max_open_prs` | one of its PRs merges or closes |
  | `foreign_branch` | `repo-guardian/<slug>` exists but its PR is not the App's | the branch is removed or renamed |
  | `stale_branch` | a branch is left from a PR a human closed without merging | the branch is deleted |
  | `conflict` | the PR's branch no longer descends from the App's last commit, or main will not merge cleanly | a human resolves the branch |
  | `permission` | the App may not make the change (for example a workflow file without the Workflows permission), or cannot reach the repository to clean up | the App is granted the permission or the access |

- **Branches are never deleted.** Not after merge, not after a close,
  not to clear a hold. Branch cleanup after merge is the repository's
  `delete_branch_on_merge` setting, which a `repo_settings` control can
  enforce.
- **API controls follow `apply`.** `recommend` writes nothing and
  records the change for a human; `direct` writes, reads back and
  journals each resource; `workflow` opens a PR with a workflow that
  applies it on merge. A recommendation ends `superseded`, `withdrawn`
  or `resolved`; it is never left dangling.

### Rolling out a new control safely

1. Add the control to the catalogue and assign it with `mode =
   "evaluate"` (a per-control override inside a `remediate` org works:
   `control "name@1" { mode = "evaluate" }`).
2. Check it before deploying: `repo-guardian evaluate --repo org/name
   --policy <root>` evaluates one repository against the new policy with
   no database.
3. Deploy and read the results: the UI, the API or
   `repo-guardian report` show how many repositories fail and on which
   rule.
4. Add exclusions, each with its reason, for the legitimate exceptions.
5. Switch the assignment to `remediate` and watch the first PRs, holds
   and the cap in the UI's Remediations view.

## What changed from guardian.hcl

### Policy compatibility

None at the file level: v2 does not read `guardian.hcl`, and there is no
converter. Every concern v1 enforced has a control type, and
[the policy reference](../usage/policy-reference.md#from-guardianhcl)
maps each v1 rule kind to its control. `examples/policy/full` is v1's
`guardian-full.hcl` rewritten as a policy root, one control per concern.

### Rule kinds become control types

| v1 | v2 |
| --- | --- |
| `rule "file"` with `check = "exists"` | `file`, or the typed control for that file (`codeowners`, `catalog_info`, `dependency_updates`) |
| `check = "contains"` and `assertion {}` blocks | the typed control's rules (`catalog_info`'s `field_set`, `dependency_updates`'s `extends`) |
| `check = "exact"` | `file` with an `exact` rule |
| `check = "absent"` | `file` with an `absent` rule; retiring Dependabot is `dependency_updates`'s `exclusive` rule |
| `when { rule_satisfied }` gates | gone: a typed control checks the whole migration itself (`exclusive` passes only once Renovate is configured and Dependabot is gone) |
| `rule "setting"` | `repo_settings`, one rule per property |
| `rule "branch_protection"` | `branch_ruleset`, declared as the rulesets API body |
| `custom_properties` reconciler | `custom_properties` control (`annotation_properties` carries over) |
| `label_sync` reconciler | `labels` control |
| `branch_protection` reconciler | `branch_ruleset` control |
| `workflow_sync` reconciler | `file` with an `exact` rule on the workflow |
| `scope { orgs }` | `enterprise.orgs` plus per-org controls |
| `ignore { repos }` | `exclude_repos` and per-control `exclude`, each with a `reason` |
| `remediate = false` on a rule | `mode = "evaluate"`, or a rule without `remediate = true` |
| `guardian { dry_run = true }` | `mode = "evaluate"` |

### Pull requests

v1 put every file change for a repository on one branch,
`repo-guardian/add-missing-files`, under one PR. v2 opens one PR per
control on `repo-guardian/<slug>`, capped by `max_open_prs`. Because the
branch names differ, v2 does not adopt v1's open PRs: close them per
org with rgctl once that org turns on `remediate`
([cutover step 6](v2-migration.md#6-close-the-old-pull-requests)). PR
title, body and labels are still
templated, now per control with `pr {}` in the catalogue.

### What was dropped

- **`search_terms` and foreign-PR detection.** v1 left a rule alone when
  it found someone else's PR that looked like it addressed the rule.
  That guess made a repository read as compliant while nothing was
  merged. v2 reports the repository's actual state; a human PR that
  fixes it turns the control compliant when it merges.
- **The sticky reconcile-log comment.** The UI's per-repository event
  history replaces it.
- **Auto-close and orphan cleanup knobs.** One PR per control makes them
  part of the model instead of options.

### Names that changed

| v1 / v2 rc | Controls line |
| --- | --- |
| `CHECK_INTERVAL` | `EVAL_INTERVAL` |
| `GUARDIAN_CONFIG` = a file | `GUARDIAN_CONFIG` = the policy root directory |
| `worker` role | `evaluator` and `remediator` roles, one per App |
| `RepoWorkflow` (`repo/<id>`) | `EvaluationWorkflow` (`evaluation/<id>`) |
| `DiscoveryWorkflow`, `discovery` schedule | `ControlsDiscoveryWorkflow`, `controls-discovery` schedule |
| `SnapshotWorkflow`, `snapshot` schedule | `ControlsSnapshotWorkflow`, `controls-snapshot` schedule |
| `policy-rollout/<v>` | `controls-rollout/<version>/<activated at>` |
| findings, `/findings`, `/rules` | results, `/repositories/{id}/controls`, `/controls` |

## The status model

Every rule result is `compliant`, `non_compliant`, `not_applicable` or
`unknown`, with a reason and typed evidence. A control's status follows
from its rules.

Compliance is reported over **assignments** (repository × control), and
every assignment lands in exactly one bucket:

| Bucket | Means |
| --- | --- |
| `compliant`, `non_compliant` | measured |
| `not_applicable` | measured, the control does not apply (for example, a `catalog-info.yaml` that is not a Component) |
| `unknown` | evaluated, but the input could not be judged (a parse error, an inherited ruleset) |
| `pending` | assigned, no result yet |
| `stale` | the result is from an older assignment or control revision |
| `unmeasurable_access_denied`, `unmeasurable_suspended` | the Evaluation App cannot read the repository |
| `unmeasurable_conflict` | two assigned controls own the same resource; an operator must fix the policy |
| `excluded` | excluded by policy, with a reason |

Two numbers come out of that. **Percent** is compliant over compliant
plus non-compliant. **Coverage** is measured (compliant, non-compliant,
not applicable) over assigned. Both are floored to one decimal in SQL,
and both are *no data*, never 100%, on an empty denominator: nothing
measured is not a perfect fleet.

## Where v2 is a real improvement

### Compliance you can trust

v1 records one boolean per repository and rule, and everything that is
not actionable reads as compliant, including an open foreign PR, a
missing branch, an unreadable repository and a rule nobody evaluated.

v2 gives every control result a status, a reason, rule-level detail and
evidence, keeps an append-only event history per result, and reports
what it could not measure as its own bucket instead of folding it into
"compliant". The percentage is computed by one SQL function shared by
the report, the API, the UI and the daily snapshots.

### A durable control plane instead of a hand-rolled one

v1 runs its own job system on Valkey (a list, two sorted sets, a reaper
and SETNX leader election), with every invariant in hand-written Lua.

v2 hands that to Temporal. Each repository is a long-lived
`EvaluationWorkflow` that schedules its own next evaluation, coalesces
the signals that arrive meanwhile (a push, a policy change, a
re-evaluate button press), and survives pod restarts. Schedules need no
leader, and Valkey is gone.

### One rate budget per installation, per App

An `InstallationWorkflow` per installation and App holds its budget:
remaining calls, the reset time, outstanding leases and a moving
estimate of what an evaluation costs. An evaluation acquires a lease
before it spends. Webhook-triggered work sees half the reserve, so a
human-triggered evaluation jumps the queue during a fleet-wide one. The
Evaluation App's reads can never starve the Remediation App's writes,
because they are separate budgets.

### Cosmetic edits re-evaluate nothing

A control's identity for results is its **revision**: a digest over its
type, version, rules and parameters, `apply`, its template and the files
it references, but not its title, description or PR text. Editing a
template or a rule re-evaluates the repositories assigned that control.
Fixing a typo in a title re-evaluates nothing. Operational knobs are
environment variables and never part of the policy version.

### Policy changes roll out, they do not stampede

A newly activated policy version starts one `controls-rollout` workflow,
which spreads every affected repository's next evaluation across the
rollout window. A revert to an earlier version rolls out again, because
each activation is its own rollout. Results from an assignment that
changed mid-evaluation are fenced off by the assignment's epoch and
discarded rather than written over the new one.

### Least privilege by App and by role

- **Two Apps.** The Evaluation App has read permissions only. The
  Remediation App writes, and is installed only where you want writes.
- **ingest** receives webhooks for both Apps on separate routes, holds
  only the webhook secrets, and refuses to start with an App key or a
  database DSN.
- **evaluator** holds the Evaluation App key and a database role that
  can write results but nothing remediation-related.
- **remediator** holds the Remediation App key and its own database
  role. It scales to zero independently.
- **api** reads through a read-only database role. Its one write is
  sending a re-evaluate signal.

The chart mounts each key and DSN only into the role that needs it, and
a helm-unittest suite asserts every absence.

### Webhooks that fail loudly and deduplicate

Ingest starts a workflow keyed on `X-GitHub-Delivery`, so a redelivery
is a no-op success. If Temporal cannot accept the event, ingest answers
503 and GitHub records the delivery as failed and redeliverable. Events
nothing acts on get a 204 before any work is scheduled, and a push
re-evaluates only the controls that own or read a path it changed.

### Repositories keep their identity across renames and transfers

Repositories are matched on GitHub's repository ID first. A rename or
transfer keeps the same row, results, history and workflow, and records
a `renamed` or `transferred` event. A name now held by a different
repository ID is refused as a conflict rather than guessed at.

### Parking that never guesses

Archived and forked repositories, and repositories the Evaluation App
lost, are parked. Discovery parks a repository as `removed` only after a
complete listing of its installation, so a failed or partial listing
parks nothing. Parks that mean "we know nothing applies" (archived,
fork, removed) clear the repository's results; parks that mean "we
could not read it" (access denied) keep them, so a transient 403 never
makes a repository look compliant.

### Answers for people who do not read PromQL

- **A read-only REST API** (OpenAPI 3.1, OIDC, per-org authorization)
  over controls, organisations, repositories, results, events, history
  and the active policy. See [the API](../usage/api.md).
- **A web UI**: fleet posture, per-control and per-org drill-downs,
  each repository's controls with rule detail and evidence, compliance
  history, and the active policy. See [the business UI](../usage/ui.md).
- **A public status page**, privacy-checked by a test so it cannot leak
  a repository or org name.

Generated Grafana dashboards and alerts are derived from the policy
(`repo-guardian monitoring generate --config <root>`), keyed on control.

### Safe rollouts of the service itself

The evaluator and remediator each run their own Temporal worker
deployment. A worker promotes its build only when it is newer by
semver, so a crash-looping pod of the previous release cannot pull a
rollout backwards, and a remediator at zero replicas never blocks the
evaluator. Workflow code is replay-tested against captured histories in
CI.

## Summary

| | v1 | v2 (controls) |
| --- | --- | --- |
| Policy | one `guardian.hcl` | a policy directory: enterprise, catalogue, orgs, templates |
| Unit | rule, `actionable` boolean | control with rules: status, reason, evidence, history |
| Unmeasured | reads as compliant | its own bucket; coverage reported beside percent |
| GitHub access | one App, reads and writes | Evaluation App reads, Remediation App writes |
| Pull requests | one branch per repository | one branch per control, capped by `max_open_prs` |
| Job system | Valkey queue, reaper, Lua, SETNX leader | Temporal workflows and schedules |
| Rate limiting | reactive defer | per-installation, per-App budget with leases and priorities |
| Cosmetic policy edit | re-checks the fleet | changes nothing |
| Policy change | every repository stale at once | rolled out over a window, fenced by epoch |
| Webhook failure | 202 anyway; lost | 503, redeliverable, deduplicated |
| Rename or transfer | new repository | same repository, event recorded |
| Consumers | Prometheus and logs | API, UI, status page, report, plus Prometheus |
