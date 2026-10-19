---
id: INV-0023
title: "rc.11 follow-ups: rule-status badges, repository page and release verification"
status: Open
author: Donald Gifford
created: 2026-10-18
---

<!-- markdownlint-disable-file MD025 MD041 -->

# INV-0023: rc.11 follow-ups: rule-status badges, repository page and release verification

<!--toc:start-->
<!-- markdownlint-capture -->
<!-- markdownlint-disable MD013 -->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [F1: rule statuses render grey](#f1-rule-statuses-render-grey)
  - [F2: the repository breadcrumb shows the id](#f2-the-repository-breadcrumb-shows-the-id)
  - [F3: next due is always empty](#f3-next-due-is-always-empty)
  - [F4: remediable and Remediates read as a contradiction](#f4-remediable-and-remediates-read-as-a-contradiction)
  - [F5: the documented provenance check fails on cosign v3](#f5-the-documented-provenance-check-fails-on-cosign-v3)
  - [F6: a wall-clock test failed CI](#f6-a-wall-clock-test-failed-ci)
  - [F7: gaps carried from PR 219](#f7-gaps-carried-from-pr-219)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!-- markdownlint-restore -->
<!--toc:end-->

<!--docz:question:start-->
## Question

What is wrong or rough in v2.0.0-rc.11 as first seen on the homelab
install and during its release, and what is the fix for each?

<!--docz:question:end-->

<!--docz:hypothesis:start-->
## Hypothesis

Each problem is small and local: the controls line replaced the
findings model underneath the UI (IMPL-0029 Phase 8) and a few
findings-era assumptions survived in places no test looks at.

<!--docz:hypothesis:end-->

<!--docz:context:start-->
## Context

**Triggered by:** the rc.11 release (IMPL-0029 task 9.6, PR #219) and
the first look at the repository page on the homelab install.

rc.11 is the first release on the controls line. The repository page
for `donaldgifford/s3uploadscripts` showed two non-compliant controls
(`codeowners`, `dependabot`) whose failing rules carried a grey `fail`
badge next to a red `non compliant` control badge.

<!--docz:context:end-->

<!--docz:approach:start-->
## Approach

1. Trace each visible problem on the repository page to the code that
   renders it, and check whether a test covers it.
2. Re-run the release verification (cosign signature, SLSA provenance)
   for the three rc.11 artifacts exactly as the chart README documents.
3. Collect the gaps PR #219 listed so they live in one tracked place.

<!--docz:approach:end-->

<!--docz:environment:start-->
## Environment

| Component | Version |
|-----------|---------|
| repo-guardian | v2.0.0-rc.11 (`7ad928f`), chart 2.0.0-rc.11 |
| UI | `ghcr.io/donaldgifford/repo-guardian-ui:2.0.0-rc.11` |
| cosign | v3.1.3 |
| SLSA generator | `generator_container_slsa3.yml@v2.1.0` |

<!--docz:environment:end-->

<!--docz:findings:start-->
## Findings

### F1: rule statuses render grey

`ui/web/src/lib/format.ts` holds the one tone table (DESIGN-0036
§Badges). Its `status` row knows the *control* statuses the store
writes (`compliant`, `non_compliant`, `not_applicable`, `unknown`).
The repository page's rule table (`ruleColumns` in
`views/repositories.tsx`) passes each `RuleResult.status` to the same
`StatusBadge`, but rule statuses are a different closed set
(`control.RuleStatus`: `pass`, `fail`, `error`, `unknown`,
`not_applicable`). `toneFor` maps anything it does not know to `mute`,
so `fail` and `error` render grey and `pass` renders grey instead of
green. The same badge renders `result_events.from_status`/`to_status`
on the Events tab, where a rule-level event (`rule_id` set) carries rule
statuses too.

No test catches it: `format.test.ts` exercises the control statuses
only, and the e2e seed does write `fail` rule results
(`ui/e2e/seed.sql`, `CASE cr.status WHEN 'compliant' THEN 'pass' ELSE
'fail' END`) but no spec asserts a badge's tone.

Fix: a separate `rule` field in the tone table, with `pass` → ok,
`fail` → bad, `error` → bad, `unknown` → warn, `not_applicable` →
mute, and a `RuleStatusBadge` the rule table and rule-level events use.
Keep the two sets apart rather than merging them into `status`: a
merged row would let a control status and a rule status share a key by
accident later. Add the rule rows to `format.test.ts` and one e2e
assertion that a failing rule's badge carries the `bad` tone.

### F2: the repository breadcrumb shows the id

`components/page/Breadcrumbs.tsx` labels an item crumb with the raw
route param (`item: "id"`). That reads well for `/controls/$id` and
`/orgs/$org`, whose params are names, but `/repositories/$id` is the
numeric `repositories.id`, so the crumb reads `Repositories / 1`.

Fix: let a view supply the item label (the repository page already has
`org/name` from `GET /repositories/{id}`) and fall back to the param
while it loads.

### F3: next due is always empty

The Repository card shows `next due —` for a managed repository that
is on a timer. `repositories.next_due_at` is selected by
`queries_controls/api_repositories.sql` but nothing on the controls
line writes it: `EvaluationWorkflow` keeps its next due time in
workflow state (`workflows.EvaluationWorkflowInput.NextDue`, carried through ContinueAsNew) and the
`RecordEvaluation` path does not persist it. The column is an rc
leftover that only the rc `RepoWorkflow` wrote.

Fix, either (a) persist the next due time from `RecordEvaluation`
(the workflow knows it when it records) or (b) drop the field from
the API and the card. (a) is the useful one: an operator reading the
page wants to know when the repository is looked at next.

### F4: remediable and Remediates read as a contradiction

The Repository card says `remediable no` (the Remediation App cannot
reach the repository or is suspended on it) while every rule row says
`Remediates yes` (the rule is *declared* remediable). Both are correct
and mean different things, but side by side they look like a bug.

Fix: rename the rule column to `Remediable rule` or show it only when
the repository is remediable, and give the card row a hint
("Remediation App has no access"). Remediation itself is IMPL-0030, so
until then every repository reads `remediable no`.

### F5: the documented provenance check fails on cosign v3

The chart README's SLSA check (`README.md.gotmpl` §SLSA provenance):

```bash
cosign verify-attestation --type slsaprovenance \
  --certificate-identity-regexp '^https://github.com/slsa-framework/slsa-github-generator/.+' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  ghcr.io/donaldgifford/charts/repo-guardian:2.0.0-rc.11
```

fails on cosign v3.1.3 for all three rc.11 artifacts with *"expected
SAN value to match regex ... got
`https://github.com/donaldgifford/repo-guardian/.github/workflows/ghcr.yml@refs/heads/v2`"*.
The message is misleading. cosign v3 reads only the new-bundle OCI
referrers by default, and the one referrer present is our own
`cosign sign` bundle (predicate `https://sigstore.dev/cosign/sign/v1`,
signed by `ghcr.yml`). The generator's provenance sits in the legacy
`sha256-<digest>.att` tag. With `--new-bundle-format=false` all three
verify: builder `generator_container_slsa3.yml@refs/tags/v2.1.0`,
source commit `7ad928f`.

Two smaller release notes from the same run:

- Image tags drop the `v`: the git tag is `v2.0.0-rc.11`, the images
  are `:2.0.0-rc.11` (`docker/metadata-action` semver). The chart tag
  never had one.
- `ghcr.yml` run 39026522802 published the image, the UI image and the
  chart, each with provenance; nothing failed.

Fix: add `--new-bundle-format=false` to the README command with a
sentence on why. Done in this branch.

### F6: a wall-clock test failed CI

`TestParseDocument_WideMappingIsLinear` failed PR #219's first CI run:
parsing a catalog file at the size cap took 5.7s under `-race` against
a 5s budget. It now compares the cost of 4x the annotations (about 4x
when linear, over 10x when quadratic; an injected O(n²) duplicate
check measured 22.8x), so runner speed cannot fail it. Fixed in
`eda8692`, shipped in rc.11. Recorded here so the next timing test is
written the same way.

### F7: gaps carried from PR 219

Listed in the PR body and not yet owned by a plan:

1. `STORE_DSN_EVALUATOR` / `STORE_DSN_REMEDIATOR` are mounted into the
   controls pods but no code reads them; the workers connect with
   `STORE_DSN`. The per-role database credentials (IMPL-0028 Phase 6)
   are therefore not yet the credentials in use.
2. `checksRetention` reaches the snapshot workflow but nothing prunes
   `checks` on the controls schema.
3. `docs/usage/img/ui/status.png` predates the controls status page and needs
   re-rendering in the pinned Playwright image
   (`make test-ui-e2e-update-snapshots`).

<!--docz:findings:end-->

<!--docz:conclusion:start-->
## Conclusion

The hypothesis holds. F1 to F4 are the findings-era UI meeting the
controls schema in places no assertion looks at, F5 is a cosign major
version changing a default under a documented command, F6 is already
fixed, and F7 is known work without a home.

<!--docz:conclusion:end-->

<!--docz:recommendation:start-->
## Recommendation

| Finding | Fix | Where |
|---------|-----|-------|
| F1 | `rule` tone row + `RuleStatusBadge`, unit + e2e assertion | IMPL-0032 Phase 1 (its own PR) |
| F2 | view-supplied item crumb label | IMPL-0032 Phase 1 |
| F3 | persist `next_due_at` from `RecordEvaluation` | IMPL-0030 Phase 2 |
| F4 | rename the rule column, hint on the card row | IMPL-0032 Phase 1 |
| F5 | `--new-bundle-format=false` in the README | this branch |
| F6 | none, fixed | rc.11 |
| F7.1 | read `STORE_DSN_<ROLE>` in the controls workers | IMPL-0030 Phase 1 |
| F7.2 | prune `checks` in `ControlsSnapshotWorkflow` | IMPL-0030 Phase 6 |
| F7.3 | re-render `status.png` | IMPL-0032 Phase 1 |

F1, F2, F4 and F7.3 are UI-only and ship first, as IMPL-0032 Phase 1
in its own pull request (#222, which fixes all four). F3, F7.1 and F7.2 are Go work and ride with
IMPL-0030 (decided 2026-10-18).

<!--docz:recommendation:end-->

<!--docz:references:start-->
## References

- PR #219 (IMPL-0029), `ghcr.yml` run 39026522802
- DESIGN-0036 §Badges (the tone table)
- DESIGN-0037 (the UI work these fixes ride with)
- `ui/web/src/lib/format.ts`, `ui/web/src/views/repositories.tsx`,
  `ui/web/src/components/page/Breadcrumbs.tsx`
- `internal/control/types.go` (`RuleStatus`)

<!--docz:references:end-->
