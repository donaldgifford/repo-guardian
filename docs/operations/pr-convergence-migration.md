# Chart 0.6.0 / appVersion 1.7.0 — PR convergence migration

**Implements:** [IMPL-0013](../impl/0013-reconcile-open-prs-when-file-rules-become-satisfied.md)
**Resolves:** [INV-0005](../investigation/0005-stale-prs-when-file-rules-become-satisfied-on-main.md)

## What changes after this upgrade

Existing repo-guardian PRs that have **every file rule satisfied on
the default branch** are now auto-closed on the next reconcile.

Concretely: a PR addressing `codeowners` and `dependabot` rules sits
open. A maintainer manually merges a CODEOWNERS file on a side
branch (resolving the codeowners rule out of band). The dependabot
file is then handled the same way later. After both merges land on
the default branch, the next repo-guardian reconcile will:

1. Discover that every rule the PR addressed is now satisfied.
2. Post a final sticky markdown-table comment on the PR summarising
   per-rule status (the `<!-- repo-guardian:reconcile-log:v1 -->`
   marker on row 1 identifies it).
3. Close the PR (state → `closed`).
4. Delete the `repo-guardian/add-missing-files` branch.

Before this release, that PR would sit open until a human noticed
and closed it.

The PR-body refresh path is also new: when an open repo-guardian
PR addresses rules A + B and rule A is satisfied on main
mid-flight, the next reconcile removes the orphan file A from the
reconcile branch and rewrites the PR body to describe only rule B.
Previously, rule A's stale file stayed on the branch and the PR
body kept advertising A.

## How to opt out

If you have a compliance workflow that requires manual PR-close
attestation, opt out of the auto-close behaviour:

### Via chart values

```yaml
policy:
  autoClosePR: false
```

### Via env override (wins over chart values)

```yaml
extraEnv:
  - name: AUTO_CLOSE_PR
    value: "false"
```

With `autoClosePR=false`:

- The drift counter still increments
  (`pr_open_with_empty_actionable_total`) so you can see how many
  stuck PRs exist.
- The sticky reconcile-log comment still posts on every reconcile,
  including a note that the PR is intentionally stuck per policy.
- The PR stays open until a human closes it.
- The reconcile branch is not deleted.
- Orphan files are NOT removed from the reconcile branch in this
  mode (since the PR isn't closing anyway).

## How to read the sticky reconcile-log comment

Every reconcile that finds an existing repo-guardian PR upserts a
single sticky markdown-table comment to the PR. The first row is
the marker:

```html
<!-- repo-guardian:reconcile-log:v1 -->
```

Following the marker is a table with one row per file rule:

| Rule | Status |
|------|--------|
| `codeowners` | satisfied on main |
| `dependabot` | still actionable |
| `renovate_config` | orphan removed from branch |

The three statuses:

- **satisfied on main** — the rule's file exists on the default
  branch.
- **still actionable** — the rule's file is missing on the default
  branch and the PR is fronting the change.
- **orphan removed from branch** — the rule's file was committed
  to the reconcile branch in an earlier sweep but is now satisfied
  on main; repo-guardian removed the orphan from the branch on
  this reconcile.

The comment is edited in place across sweeps — there will only
ever be one reconcile-log comment per PR.

## Smoke checks after upgrading

Run these in order against your homelab or staging deployment
before rolling to production.

### 1. Pre-upgrade baseline

Capture the current drift count so you can confirm it drops to
zero after the auto-close path runs.

```promql
sum(repo_guardian_pr_open_with_empty_actionable_total)
```

Note the value. Also list any 30-day-plus stuck PRs:

```promql
sum by (org, rule) (repo_guardian_open_prs_by_rule{age_bucket="30d+"})
```

### 2. Deploy chart 0.6.0

```bash
helm upgrade repo-guardian \
  oci://ghcr.io/donaldgifford/charts/repo-guardian \
  --version 0.6.0 \
  -n repo-guardian \
  -f values.yaml
```

Confirm the Deployment came up with the new env var:

```bash
kubectl -n repo-guardian get deploy repo-guardian \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="AUTO_CLOSE_PR")]}'
```

Expected output:

```json
{"name":"AUTO_CLOSE_PR","value":"true"}
```

### 3. Sticky comments appear on existing PRs

Pick one open repo-guardian PR and wait for the next sweep. After
the sweep:

```bash
gh pr view <PR_URL> --json comments \
  --jq '.comments[].body' | grep -c "repo-guardian:reconcile-log:v1"
```

Expected: `1` (exactly one sticky reconcile-log comment).

### 4. Stuck PRs auto-close

Identify a PR where every rule is satisfied on main (use the drift
counter from step 1). After the next sweep, that PR should be
closed with a final sticky comment ending in `_Auto-closing per_`.

```bash
gh pr list --state closed --search "repo-guardian/add-missing-files" \
  --json number,closedAt,title
```

### 5. Drift counter drops to zero

```promql
sum(repo_guardian_pr_open_with_empty_actionable_total)
```

Expected: drops to zero within one sweep cycle after step 4 runs
(per-org counters keep their absolute counts; the rate over a
short window should be zero).

### 6. Auto-close metric is populated

```promql
sum by (org) (repo_guardian_prs_closed_total{reason="satisfied"})
```

Expected: matches the number of PRs you observed closing in step 4.

### 7. Orphan-cleanup error budget

```promql
sum by (org) (rate(repo_guardian_pr_orphan_left_total[1h]))
```

Expected: zero. A sustained non-zero rate indicates the GitHub App
lacks permission to delete files on the reconcile branch — check
the App's Contents permission grant.

### 8. Alert sanity check

```bash
kubectl -n repo-guardian get prometheusrule repo-guardian \
  -o yaml | grep -E "alert: (RepoGuardianStaleOpenPRs|RepoGuardianPRDrift)"
```

Expected: both alerts rendered.

## Rollback

The behaviour change is the auto-close path; rollback is
`helm upgrade` to the previous chart version (0.5.1 or earlier).
Any PRs auto-closed during 0.6.0 stay closed — re-opening is
manual via `gh pr reopen`.

The 0.5.x release line keeps the diagnostic metrics
(`pr_open_with_empty_actionable_total`, `open_prs_by_rule`,
`pr_orphan_left_total`, `prs_closed_total`) from IMPL-0013 Phase 1,
so rollback does not blind you to drift.

## References

- [INV-0005](../investigation/0005-stale-prs-when-file-rules-become-satisfied-on-main.md) — bug investigation.
- [IMPL-0013](../impl/0013-reconcile-open-prs-when-file-rules-become-satisfied.md) — implementation plan.
- [contrib/README.md](../../contrib/README.md) — PromQL recipes for the new metrics.
- [examples/guardian-full.hcl](../../examples/guardian-full.hcl) — the `auto_close_pr` knob documented inline.
