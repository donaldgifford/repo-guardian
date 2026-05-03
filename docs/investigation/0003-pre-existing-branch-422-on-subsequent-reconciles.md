---
id: INV-0003
title: "Pre-existing branch 422 on subsequent reconciles"
status: Open
author: Donald Gifford
created: 2026-05-02
---
<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0003: Pre-existing branch 422 on subsequent reconciles

**Status:** Open
**Author:** Donald Gifford
**Date:** 2026-05-02

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [Observation 1](#observation-1)
  - [Observation 2](#observation-2)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!--toc:end-->

## Question

When a repo already has an open repo-guardian PR (and therefore a
pre-existing `repo-guardian/add-missing-files` branch with the templated
files committed to it), the next reconcile against that repo fails with
`422 Invalid request "sha" wasn't supplied`. What is the minimal engine
change to make subsequent reconciles idempotent on the already-published
branch?

## Hypothesis

The file-creation path in the GitHub client wrapper unconditionally calls
`PUT /repos/{owner}/{repo}/contents/{path}` (the "create file" call)
without first checking whether the file exists on the target branch. On
the first reconcile this works because the branch is fresh. On the
second reconcile the branch and file already exist on the branch, and
GitHub's contents API requires the existing blob's `sha` for an UPDATE.
The fix is a small change in the wrapper: GET the file on the branch
first; if it exists with the same content, no-op; if it exists with
different content, send the existing `sha` along with the PUT to make
it an UPDATE.

## Context

Surfaced 2026-05-02 during the chart 0.3.2 homelab smoke test. After
chart 0.3.2 deployed cleanly (PR #67), the engine successfully created
PR #1 on `donaldgifford/logpush` (CODEOWNERS) and PR #2 on
`donaldgifford/repo-guardian-test-repo` (CODEOWNERS, dependabot,
renovate). On the next reconcile tick the same two repos failed with
422 because the engine tried to re-PUT files that were already on the
`repo-guardian/add-missing-files` branch from the open PRs.

The bug only manifests when an open repo-guardian PR exists and the
weekly scheduler (or a webhook trigger) re-checks the same repo before
the PR is merged. In a steady state where PRs merge quickly, the bug
is invisible. In our homelab where PRs are intentionally left open for
review, every subsequent reconcile against those repos fails.

**Triggered by:** Chart 0.3.2 homelab smoke test (post-PR #67),
recorded as a Known Issue in `CLAUDE.md`.

## Approach

1. Reproduce locally against `repo-guardian-test-repo` with the engine
   in a loop: first reconcile creates PR, second reconcile must succeed
   without 422.
2. Trace the exact call path from `internal/checker/engine_policy.go`
   through `internal/github/client.go` to the go-github contents API
   call. Identify whether the create-or-update logic lives in the
   wrapper or higher up in the engine.
3. Inspect what `client.GetContents` (or equivalent) returns when the
   file exists on the branch — confirm we get a usable `sha` back.
4. Sketch the minimal patch: branch detection + file existence check +
   sha forwarding. Confirm it doesn't break the first-reconcile path.
5. Decide between (a) one-PR fix (small, scoped to client wrapper) and
   (b) IMPL doc (if the fix touches multiple packages or needs new
   tests beyond a unit test on the wrapper).

## Environment

| Component | Version / Value |
|-----------|----------------|
| Chart | 0.3.2 (production-deployed in homelab) |
| Binary | appVersion 1.4.0 |
| go-github | v68 |
| Target repos (homelab) | `donaldgifford/logpush`, `donaldgifford/repo-guardian-test-repo` |
| Stale branch | `repo-guardian/add-missing-files` |

## Findings

### Observation 1: Failure log from production

From the homelab pod after the second reconcile tick (2026-05-02):

```
level=ERROR msg="failed to create file"
  repo=donaldgifford/logpush
  path=CODEOWNERS
  branch=repo-guardian/add-missing-files
  err="PUT https://api.github.com/repos/donaldgifford/logpush/contents/CODEOWNERS:
       422 Invalid request. \"sha\" wasn't supplied. []"
```

GitHub's contents API distinguishes create (no `sha`) from update
(must include the existing blob `sha`). The current engine path always
treats the call as a create.

### Observation 2: Workaround confirms the diagnosis

Manually deleting the stale branch
(`gh api -X DELETE repos/<owner>/<repo>/git/refs/heads/repo-guardian/add-missing-files`)
allows the next reconcile to succeed, because the engine then re-creates
the branch fresh and the file genuinely does not exist on it. This
confirms the failure mode is "branch + file pre-existence" and not, for
example, a stale token or a permissions regression.

### Observation 3: Pending — code path

To complete the investigation, identify the exact function in
`internal/github/` (or `internal/checker/`) that issues the PUT and
confirm whether the wrapper already has a GetContents helper we can
call before the PUT.

## Conclusion

**Answer:** Pending — investigation is in progress.

Preliminary: hypothesis is consistent with all observed evidence
(production log + workaround). Remaining work is to read the client
wrapper and confirm the smallest patch shape.

## Recommendation

**Pending — to be filled after Observation 3.**

Likely shape:
- If the fix is `<50` lines in `internal/github/client.go` plus a
  unit test, ship a single PR labeled `fix` with `patch` semver.
- If the fix touches engine-level logic (e.g., the engine needs to
  decide between "skip the file because content is identical" vs.
  "update the file because content differs"), promote to an IMPL doc.

The engine should also surface a metric (`files_skipped_total{reason="already_present"}`?)
for the no-op-on-identical-content case, so the homelab dashboards
distinguish "engine did nothing" from "engine had nothing to do."

## References

- Known Issue entry in `CLAUDE.md`
- Auto-memory pattern: "Pre-existing-branch 422 engine bug"
- IMPL-0010 (chart 0.3.2 deployed the engine version where this surfaced)
- GitHub contents API:
  https://docs.github.com/en/rest/repos/contents#create-or-update-file-contents
