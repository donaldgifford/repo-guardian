---
id: INV-0007
title: "GitLab forge backend support"
status: Open
author: Donald Gifford
created: 2026-05-29
---
<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0007: GitLab forge backend support

**Status:** Open
**Author:** Donald Gifford
**Date:** 2026-05-29

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Findings](#findings)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!--toc:end-->

## Question

INV-0004 established a `Forge` interface seam for Forgejo. Can we
add **GitLab** as a third forge backend alongside GitHub and
Forgejo, and what are the GitLab-specific differences in:

1. **API surface** — file CRUD, branches, PRs (merge requests in
   GitLab terminology), labels, repo metadata.
2. **Authentication** — GitHub uses installation-scoped App tokens;
   Forgejo uses PATs. What does GitLab look like (PATs, OAuth apps,
   project access tokens, group access tokens)?
3. **Webhook auth + payload shape** — GitHub uses HMAC-SHA256 over
   the body keyed on a per-app secret; what does GitLab use, and how
   does that map onto the existing IP-allowlist + HMAC middleware?
4. **Rate limit model** — GitHub gates per-installation
   (5k/15k req/hr); GitLab is per-user / per-IP / per-project
   depending on edition. How does that change the
   `RateLimitRemaining` gate the sweeper relies on?
5. **Feature gaps** — what GitHub-specific things (custom
   properties, rulesets-style branch protection, GitHub Apps
   themselves) have no GitLab equivalent and must surface as
   "reconciler unavailable on this backend" the same way INV-0004
   resolved for Forgejo?

## Hypothesis

GitLab's API is feature-rich enough to support the core
file-CRUD + MR + labels path with minimal vendor-conditional code,
slotting into the existing `Forge` interface from INV-0004. The
likely friction points:

- **No GitHub-App equivalent** — GitLab has Project Access Tokens
  and Group Access Tokens, but no installation-scoped App model.
  Auth becomes per-instance + per-project/group, more like Forgejo
  than GitHub.
- **Branch protection model is different enough** that the
  `branch_protection` reconciler likely needs a GitLab-specific
  implementation path (push rules, protected branches, MR
  approvals are three separate concepts in GitLab vs GitHub
  rulesets' unified model).
- **Custom properties have no equivalent**; the
  `custom_properties` reconciler is GitHub-only and stays
  GitHub-only.
- **Self-hosted instances are the common case** for GitLab — base
  URL is a per-instance config, like Forgejo.

I expect the work to look more like INV-0004's Forgejo refactor
than a from-scratch effort, because the interface seam already
exists.

## Context

**Triggered by:** Operator review during INV-0006 follow-up
discussion (2026-05-29). The same operator runs **20+ GitHub orgs
plus a few self-hosted GitLab instances** and asked whether the
multi-org expansion requires the per-org-app-credentials work in
INV-0006. INV-0006's conclusion (no, org count alone isn't a
trigger) clarified that GitLab support is orthogonal —
it sits behind the existing `Forge` abstraction (INV-0004), not
behind credential-fanout (INV-0006).

This investigation captures the GitLab-specific shape so that when
the operator is ready to onboard the GitLab instances, the work
isn't blocked on a clean-slate analysis.

**Triggered by:** Operator question following INV-0006 review (2026-05-29).

## Approach

1. Read INV-0004 and confirm the `Forge` interface seam covers
   the GitLab method set (file CRUD, branches, MRs, labels, repo
   metadata, comments).
2. Cross-reference each `Forge` method against the GitLab REST API
   v4 reference (https://docs.gitlab.com/ee/api/). Note which
   methods map 1:1 and which require translation
   (e.g., `pulls` → `merge_requests`).
3. Audit GitLab's auth model — PAT, Project Access Token, Group
   Access Token, OAuth app — and decide which fits the
   reconciler's "long-lived background job" persona. Likely
   group-scoped PAT for org-wide reconcile + project-scoped
   tokens for per-repo overrides.
4. Compare webhook authentication: GitLab signs webhook payloads
   with `X-Gitlab-Token` (a static secret, NOT HMAC) — confirm
   whether this can co-exist with the existing GitHub HMAC
   middleware or if it needs a separate route prefix.
5. Inspect GitLab's rate limit headers (`RateLimit-Limit`,
   `RateLimit-Remaining`, `RateLimit-Reset`) and map them onto
   the sweeper's `RateLimitRemaining` gate.
6. Enumerate the reconcilers that should surface as
   "unavailable on GitLab":
   - `custom_properties` — no GitLab equivalent.
   - `branch_protection` — has a GitLab translation but the
     rulesets schema doesn't map cleanly; likely a separate
     `gitlab_branch_protection` reconciler.
   - `label_sync`, `workflow_sync` — should work; GitLab has
     labels and CI files.
7. Sketch the package layout: does `internal/forge/gitlab/`
   parallel `internal/forge/forgejo/`, or does GitLab sit deeper
   in the dispatch tree?

## Findings

_Pending investigation. To be filled in when the operator's
GitLab onboarding work begins._

## Conclusion

_Pending._

**Answer:** _Pending — investigation not yet executed._

## Recommendation

_Pending._

When this investigation is executed, the follow-up artifacts will
likely be:

- A **DESIGN** doc covering the GitLab-specific surface
  (parallel to whatever DESIGN doc covers Forgejo today).
- An **IMPL** plan for the GitLab backend phases.
- Updates to the chart's auth surface to admit GitLab PAT secrets.

## References

- [INV-0004](0004-forge-interface-and-package-refactor-for-forgejo-backend.md)
  — established the `Forge` interface seam this backend would slot
  into. Required reading before executing this investigation.
- [INV-0002](0002-multi-org-and-forgejo-support-for-repo-guardian.md)
  — earlier multi-forge thinking; predates the current
  `Forge`-abstraction layout.
- [INV-0006](0006-per-org-github-app-credentials.md) — the
  conversation that surfaced this question. INV-0006 clarified
  that GitLab support is orthogonal to per-org-app-credentials.
- GitLab REST API v4 reference:
  https://docs.gitlab.com/ee/api/
- GitLab webhook authentication:
  https://docs.gitlab.com/ee/user/project/integrations/webhooks.html#validate-payloads-by-using-a-secret-token
