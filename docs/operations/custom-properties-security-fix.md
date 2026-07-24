# Custom-properties security fix (IMPL-0020)

[IMPL-0020](../impl/0020-pre-impl-0019-high-severity-fixes-inv-0011-group-a-high.md)
ships two High-severity fixes to the `custom_properties` reconciler
surface introduced by IMPL-0017. Both were found in
[INV-0011](../investigation/0011-tech-debt-cleanup-inventory-post-impl-0019.md)
(findings A1 and A2). This note is for operators upgrading past the
release that carries them.

## A2 — command injection in generated workflows (security fix)

**Who is affected:** deployments running the `custom_properties`
reconciler in **`github-action` mode**. API mode was never affected — it
sends values through the Go GitHub client, never a shell.

**The vulnerability.** In `github-action` mode repo-guardian opens a PR
adding `.github/workflows/set-custom-properties.yml`, a workflow that
calls `gh api` to set the repository's custom properties. Before this
fix, the property *values* — which originate in the repository's own
`catalog-info.yaml`, and are therefore controllable by anyone who can
commit to a repo the App is installed on — were interpolated directly
inside single-quoted `gh api -f 'properties[][value]=<VALUE>'`
arguments. A crafted annotation value such as `x'$(command)'` broke out
of the quoting and executed `command` under the workflow's
`GITHUB_TOKEN` once the PR was merged.

**The fix.** Property values now reach the workflow only through its
`env:` block, rendered YAML-safe, and are referenced in the run script
as quoted `"$RG_PROP_*"` shell variables. The shell substitutes them
literally and never re-parses them, so a hostile value is passed to
`gh api` as an inert string. Values containing a literal GitHub Actions
expression opener (`${{`) are rejected at render time.

**What you should do.** If you run `github-action` mode and have **open,
unmerged** `repo-guardian/set-custom-properties` PRs, those PR branches
still carry the *old, vulnerable* workflow file — regenerating happens
on new PRs, not existing branches (automated remediation of open PRs is
deliberately out of scope; tracked separately as INV-0011 A4). Close any
open `set-custom-properties` PRs and let repo-guardian recreate them on
the next reconcile, or merge them only after confirming no repo's
`catalog-info.yaml` carries a suspicious property value. Review merged
workflow runs if you have reason to believe a repo published a malicious
value before the upgrade.

Most deployments — including the reference homelab — run **API mode**,
where this vector never existed; no action is required there.

## A1 — malformed catalog-info no longer clears properties

**Who is affected:** all `custom_properties` deployments (both modes).

**The behavior change.** Previously, a `catalog-info.yaml` that failed to
parse (or parsed as a non-`Component` entity) was treated as an empty
desired state: in API mode's full-state sync that PATCHed `null` over
every mapped property and reset `Owner`/`Component` to `Unclassified`. A
single temporarily malformed commit could wipe a repo's custom
properties.

Now a parse failure or non-`Component` file causes repo-guardian to
**skip** the reconcile — no property writes, retried on the next sweep.
Parse failures increment `repo_guardian_catalog_parse_failed_total{org}`
and log `"catalog-info parse failed; skipping reconcile to avoid
clearing properties"`. The `Unclassified` defaults now apply only when a
repo has **no** catalog-info file at all, which is a genuine
"unclassified" signal rather than a parse error.

No operator action is required; this is strictly safer. If you monitor
metrics, consider alerting on a sustained nonzero rate of
`repo_guardian_catalog_parse_failed_total` — it means a repo is
publishing a `catalog-info.yaml` that never syncs.
