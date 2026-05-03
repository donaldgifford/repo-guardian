---
id: INV-0003
title: "Pre-existing branch 422 on subsequent reconciles"
status: Resolved
author: Donald Gifford
created: 2026-05-02
---
<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0003: Pre-existing branch 422 on subsequent reconciles

**Status:** Resolved
**Author:** Donald Gifford
**Date:** 2026-05-02

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [Observation 1: Failure log from production](#observation-1-failure-log-from-production)
  - [Observation 2: Workaround confirms the diagnosis](#observation-2-workaround-confirms-the-diagnosis)
  - [Observation 3: Code path located in client wrapper](#observation-3-code-path-located-in-client-wrapper)
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

### Observation 3: Code path located in client wrapper

`internal/checker/engine_policy.go:553` and
`internal/checker/engine.go:267` both call
`client.CreateOrUpdateFile`. The wrapper at
`internal/github/client.go:184-201` was misnamed — it called only
`Repositories.CreateFile`, never `UpdateFile`, and never read the
existing blob first. On a fresh branch this works; on a re-used
`repo-guardian/add-missing-files` branch with the file already
present, the call hits 422.

The package already had a `GetContents` helper, but it took no `Ref`
parameter and so always queried the default branch — useless for the
"does the file exist on the target branch?" question we needed to
answer. The fix inlines `Repositories.GetContents` with
`RepositoryContentGetOptions{Ref: branch}` directly inside
`CreateOrUpdateFile`, since this is the only caller that needs
branch-scoped existence checks.

## Conclusion

**Answer:** Yes — fix is small (≈35 lines in
`internal/github/client.go` plus three unit tests) and lives entirely
in the GitHub client wrapper.

`CreateOrUpdateFile` now performs:

1. `GetContents(..., Ref: branch)` to check the target branch.
2. If 404 → `CreateFile` (unchanged path).
3. If 200 with byte-identical decoded content → return nil (idempotent
   skip; no commit, no API write).
4. If 200 with differing content → `UpdateFile` with the existing
   blob's `sha`.

This makes the function name truthful (it now actually creates *or*
updates) and makes engine reconciles idempotent on the
`repo-guardian/add-missing-files` branch.

## Recommendation

Shipping in this PR. Decisions made:

- **One-PR fix, not an IMPL.** All changes are in
  `internal/github/client.go` and `internal/github/client_test.go`.
  No engine logic changed, no public API changed, signature of
  `CreateOrUpdateFile` is preserved so callers in `engine.go` and
  `engine_policy.go` are unaffected.
- **No new metric.** The "skipped because identical" case is silent.
  Adding a `files_skipped_total{reason}` metric would be useful for
  homelab dashboards, but it expands scope beyond the bug fix and
  belongs in a separate PR if/when the dashboard need surfaces.
  Logging a debug-level `"file unchanged on branch"` line was
  considered and rejected for the same scope-creep reason.
- **Bumped chart `appVersion` to 1.4.1.** Real bug fix, real binary
  change → patch semver bump on the binary. Chart `version` stays
  at 0.3.2 (no chart-level changes).
- **Test coverage added.** Three tests cover all three branches:
  `TestCreateOrUpdateFile_FileMissing_Creates`,
  `TestCreateOrUpdateFile_FileExistsIdentical_Skips`,
  `TestCreateOrUpdateFile_FileExistsDifferent_Updates`.

## References

- Known Issue entry in `CLAUDE.md`
- Auto-memory pattern: "Pre-existing-branch 422 engine bug"
- IMPL-0010 (chart 0.3.2 deployed the engine version where this surfaced)
- GitHub contents API:
  https://docs.github.com/en/rest/repos/contents#create-or-update-file-contents
