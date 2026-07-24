# Absent rules and conditional gates migration (appVersion → next minor)

[IMPL-0019](../impl/0019-absent-check-mode-and-conditional-file-rules.md)
adds a fourth file-rule check mode, `check = "absent"`, whose remediation
is a **file-deletion PR**, plus a `when { rule_satisfied = "<rule>" }`
block that gates any file rule on a sibling rule being satisfied on the
default branch
([DESIGN-0020](../design/0020-absent-check-mode-and-conditional-file-rules.md)).

Absent-mode deletion is the engine's **first destructive remediation**.
This guide leads with the dry-run recipe, then covers the operational
edges an upgrading operator will hit. It is additive — existing policies
with no `absent` rule and no `when {}` block behave exactly as before.

## 1. Dry-run first (do this before enabling)

Because an absent rule *deletes* files, validate the blast radius before
letting it write. Set the engine to dry-run and inspect the logs:

```bash
# On the Deployment (env wins over HCL/defaults):
DRY_RUN=true
```

With `dry_run = true` an actionable absent rule logs its planned
deletions and mutates nothing:

```text
level=INFO msg="dry run: would create PR" actionable_rules=[no_dependabot] \
  planned_deletions=[.github/dependabot.yml]
```

Confirm the `planned_deletions` list matches what you expect across your
fleet, then flip `DRY_RUN=false` (or remove the override) to let the
removal PRs open.

## 2. Policy-hash bump → one full re-sweep

Adding an absent rule or a `when {}` block changes the policy version
hash (`policy.Version`). The stale-sweeper treats every `repo_state` row
whose `policy_version` predates the change as drifted, so the **first
sweep after upgrade re-checks every repo**. This is expected and
self-limiting — subsequent sweeps return to steady state once rows are
re-stamped. Expect a one-time spike in `repos_checked_total` and GitHub
API usage; the per-installation rate-limit reserve and budget gate still
apply, so the sweep paces itself.

## 3. Reconcile-log hash bump → one comment edit per open PR

The sticky reconcile-log comment gained absent-aware and gate-closed
status strings (`present on main, pending removal`, `absent from main`,
`skipped (when-gate closed: <referee> not satisfied)`). These strings
feed the comment's content hash, so **every open repo-guardian PR gets
exactly one extra comment edit** on the first reconcile after upgrade,
after which identical-state sweeps converge to zero edits again.

## 4. `search_terms` must not collide with the add-era PR

An absent rule's PR is discovered by the same `pr.search_terms`
mechanism as add rules. If a `no_dependabot` absent rule reused
`search_terms = ["dependabot"]`, its **removal** PR would be mistaken for
the old **add** PR and skipped. Give absent rules a distinct phrase:

```hcl
rule "file" "no_dependabot" {
  check = "absent"
  paths = [".github/dependabot.yml", ".github/dependabot.yaml"]

  when {
    rule_satisfied = "renovate_config"
  }

  pr {
    search_terms = ["remove dependabot"]
    title        = "chore({{ .Repo }}): remove Dependabot config (repo uses Renovate)"
  }
}
```

## 5. Two-PR convergence is deliberate

The gate reads the **default branch only**, so a repo that has Dependabot
but not yet Renovate converges over two PR cycles: sweep 1 opens the
add-Renovate PR (the gate is closed, so nothing is deleted); after a human
merges it, a later sweep sees the gate open and opens the
remove-Dependabot PR. repo-guardian never deletes Dependabot before
Renovate is actually live and reviewed. Merging the Renovate PR triggers
the gated re-check on the push path, so you don't wait for the next
scheduled sweep.

## 6. Downgrade behavior — fails loud

An older binary that predates absent mode rejects a policy containing
`check = "absent"` at load with an "invalid check mode" error and refuses
to start, rather than silently misinterpreting the rule. If you must roll
back the binary, roll back the policy in the same change.

## Fail-safe recap

Every destructive step is fail-safe, mirroring the IMPL-0013 Q9 stance:

- A gate evaluation error closes the gate (rule skipped), never opens it.
- A file already gone from the reconcile branch is an idempotent skip.
- Restoration of a no-longer-forbidden file (inverse orphan) and orphan
  cleanup both leave the path untouched on any API error, increment
  `pr_orphan_left_total`, and retry on the next sweep.
