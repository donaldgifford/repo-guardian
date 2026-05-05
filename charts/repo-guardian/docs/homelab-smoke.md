# Homelab smoke runbook — chart 0.4.0 / IMPL-0012

This runbook drives the [IMPL-0012 Phase 7.4 acceptance
criteria](../../../docs/impl/0012-customizable-pr-templates-and-extensible-template-configmap.md#phase-7-examples--migration-docs--smoke)
end-to-end. Time budget: ~5 minutes including PR inspection.

The goal is to confirm three behaviors against a live cluster:

1. A per-rule `pr.title` referencing `{{ env "JIRA_PROJECT" }}` resolves
   to the chart-supplied `templating.vars` value.
2. A multi-rule bundled PR falls back to `defaults.pr.title`.
3. A body that exceeds 65000 chars is truncated with the marker.

## Prerequisites

- repo-guardian deployed in homelab on chart `0.3.x` (any version).
- `donaldgifford/repo-guardian-test-repo` reachable by the GitHub App
  installation and currently empty of `CODEOWNERS`,
  `.github/dependabot.yml`, and `renovate.json`.
- Local `helm`, `kubectl`, and `gh` configured against the homelab.

## Step 1 — values overlay

Drop this fragment into your existing homelab values file (or apply it
as a separate `-f` overlay during `helm upgrade`). It sets the Jira
project env var and customizes the codeowners-rule title, then leaves
defaults available for bundled-PR fallback.

```yaml
templating:
  vars:
    JIRA_PROJECT: PLAT
  strict: true   # catch zero-value PRVars regressions at boot

policy:
  config: |
    defaults {
      pr {
        title  = "[{{ env \"JIRA_PROJECT\" | default \"GUARDIAN\" }}] guardian: {{ .Repo }}"
        labels = ["automated", "guardian"]
      }
    }

    rule "file" "codeowners" {
      check_mode = "exists"
      paths      = ["CODEOWNERS", ".github/CODEOWNERS"]
      template   = "codeowners"
      pr {
        title = "[{{ env \"JIRA_PROJECT\" }}-CHORE] add CODEOWNERS"
      }
    }

    rule "file" "dependabot" {
      check_mode = "exists"
      paths      = [".github/dependabot.yml"]
      template   = "dependabot"
    }
```

Note: the codeowners rule explicitly customizes only `title`; `labels`
and `inherits` cascade from `defaults`. The dependabot rule has no
`pr {}` block at all — it inherits everything from defaults.

## Step 2 — upgrade

```bash
helm upgrade repo-guardian \
  oci://ghcr.io/donaldgifford/charts/repo-guardian \
  --version 0.4.0 \
  --namespace repo-guardian \
  -f values.yaml \
  -f homelab-smoke-overlay.yaml
```

If `templating.strict: true` was already set on the previous chart
revision, an HCL syntax error in the overlay would have crashed boot
loop. Watch the rollout:

```bash
kubectl -n repo-guardian rollout status deploy/repo-guardian
kubectl -n repo-guardian logs deploy/repo-guardian | grep -i strict
```

A clean boot prints no strict-mode errors. Failures look like:

```text
strict template validation failed:
  rule "codeowners".pr.title: render template ...: template: ... map has no entry for key "Catalog"
```

## Step 3 — trigger reconcile

```bash
# Force the test repo onto the work queue immediately.
kubectl -n repo-guardian exec deploy/repo-guardian -- \
  curl -s -X POST localhost:8080/admin/reconcile \
  -d '{"repo":"donaldgifford/repo-guardian-test-repo"}'

# Or wait up to scheduleInterval (default 168h — not for smoke, use admin).
```

If the admin endpoint is not exposed in your build, push an empty
commit to the test repo's default branch to trigger the push handler:

```bash
gh api repos/donaldgifford/repo-guardian-test-repo/git/refs/heads/main \
  --method PATCH -f sha="$(git -C ../test-repo rev-parse HEAD)" \
  -f force=false
```

## Step 4 — verify acceptance criteria

### 4a. Per-rule custom title resolves

```bash
gh pr list --repo donaldgifford/repo-guardian-test-repo --state open
```

The codeowners-only PR title MUST be exactly:

```text
[PLAT-CHORE] add CODEOWNERS
```

Pass means `{{ env "JIRA_PROJECT" }}` resolved at render-time to the
chart-supplied `PLAT`. This exact chain is also locked in CI by
`TestEnginePR_JiraStyleTitle_FromTemplatingVars` in
`internal/checker/pr_test.go` — the homelab run is the live-traffic
confirmation, but a regression here would already break the unit
test.

### 4b. Bundled-PR title falls back to defaults

If both `codeowners` and `dependabot` are missing on the test repo,
the engine bundles them into a single PR. The bundle title MUST be:

```text
[PLAT] guardian: repo-guardian-test-repo
```

This proves the multi-rule conflict resolution chose the
`defaults.pr.title` over either per-rule title (per IMPL-0012 Phase 5
multi-rule resolution). Look in the deployment logs for an INFO line:

```text
multiple rule PR titles in bundle, falling back to defaults
```

### 4c. Body truncation marker

This one is harder to trigger in a clean smoke. Easiest synthetic
path:

1. Add a third rule with a `pr.body` template that emits >65000 chars
   (e.g., a long string repeated). Apply via `helm upgrade -f`.
2. Trigger a single-rule reconcile against that rule.
3. Inspect the resulting PR body — last line MUST contain:

```text
<!-- truncated by repo-guardian: original length=<N> chars, max=65535 -->
```

Skip if you can't synthesize a >65000-char body easily; the unit
test in `internal/checker/pr_test.go.TestEngine_PolicyPath_BodyTruncated`
covers the same path against a deterministic input.

## Cleanup

```bash
# Reset to the pre-smoke values file (no overlay).
helm upgrade repo-guardian \
  oci://ghcr.io/donaldgifford/charts/repo-guardian \
  --version 0.4.0 \
  --namespace repo-guardian \
  -f values.yaml

# Close the smoke PRs without merging if they're not actually
# something you'd like to land in the test repo.
gh pr list --repo donaldgifford/repo-guardian-test-repo --state open \
  --json number --jq '.[].number' | \
  xargs -I {} gh pr close {} --repo donaldgifford/repo-guardian-test-repo
```

## Failure modes

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Pod CrashLoopBackOff with `strict template validation failed` | Bad template referencing `.Catalog.X` from a `pr {}` block | PRVars has no Catalog. Move catalog references to file-content templates only. |
| PR title contains `<no value>` | `env` helper saw an unset var | Confirm `templating.vars.JIRA_PROJECT` is on the Deployment env. |
| PR title contains the literal `{{ env ... }}` | Old chart (≤0.3.x) — no template compilation | Upgrade to 0.4.0. |
| `helm upgrade` fails with `templating.vars contains reserved env var` | Overlap with chart-managed env (e.g., `STRICT_TEMPLATES`) | Remove the offending key from `templating.vars`. |

## After smoke passes

Mark the IMPL-0012 Phase 7.4 acceptance criteria checked in
`docs/impl/0012-customizable-pr-templates-and-extensible-template-configmap.md`
and the matching Testing Plan checkbox. That clears the only
remaining acceptance criterion in the implementation plan.
