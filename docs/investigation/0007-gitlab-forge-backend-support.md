---
id: INV-0007
title: "GitLab provider backend support"
status: Open
author: Donald Gifford
created: 2026-05-29
---
<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0007: GitLab provider backend support

**Status:** Open
**Author:** Donald Gifford
**Date:** 2026-05-29 (extended 2026-06-08 with Finding 8 + Implementation taxonomy)

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Findings](#findings)
  - [Finding 1 — Go SDK](#finding-1--go-sdk)
  - [Finding 2 — CODEOWNERS support](#finding-2--codeowners-support)
  - [Finding 3 — Custom-properties equivalent](#finding-3--custom-properties-equivalent)
  - [Finding 4 — Webhook authentication](#finding-4--webhook-authentication)
  - [Finding 5 — Rate-limit model](#finding-5--rate-limit-model)
  - [Finding 6 — Wiz consumption (does custom_properties even matter on GitLab?)](#finding-6--wiz-consumption-does-customproperties-even-matter-on-gitlab)
  - [Finding 7 — Auth model](#finding-7--auth-model)
  - [Finding 8 — Groups as the scope unit](#finding-8--groups-as-the-scope-unit)
- [Cross-provider feature matrix](#cross-provider-feature-matrix)
  - [File rules](#file-rules)
  - [Setting rules](#setting-rules)
  - [Reconcilers](#reconcilers)
  - [Platform plumbing](#platform-plumbing)
- [Implementation taxonomy: config-only vs code changes](#implementation-taxonomy-config-only-vs-code-changes)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [Open questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Question

INV-0004 established a `Provider` interface seam for Forgejo. Can we
add **GitLab** as a third provider backend alongside GitHub and
Forgejo, and what are the GitLab-specific differences in:

1. **API surface + Go SDK** — what's the supported Go client in
   2026 (the popular `xanzy/go-gitlab` was archived in late 2024)?
2. **Authentication** — GitHub uses installation-scoped App tokens;
   Forgejo uses PATs. What does GitLab look like?
3. **Webhook auth + payload shape** — does GitLab use HMAC like
   GitHub or something else, and how does that map onto the
   existing middleware?
4. **Rate-limit model** — does the sweeper's
   `RateLimitRemaining` reserve gate translate?
5. **Feature gaps** — what GitHub-specific concepts (custom
   properties, rulesets, App installation auth) have no GitLab
   equivalent and must surface as "reconciler unavailable on this
   backend" the same way INV-0004 resolved for Forgejo?
6. **CODEOWNERS portability** — does the existing `codeowners`
   file rule's path strategy work unchanged on GitLab and Forgejo?

## Hypothesis

GitLab's API is rich enough to support the core file-CRUD + MR +
labels path with minimal vendor-conditional code, slotting into the
existing `Provider` interface from INV-0004. The likely friction
points:

- **No GitHub-App equivalent.** Auth becomes per-instance +
  per-project/group, more like Forgejo than GitHub.
- **Branch-protection model is different enough** that the
  `branch_protection` reconciler likely needs a GitLab-specific
  implementation path.
- **Custom properties have no first-class equivalent.** GitLab's
  Custom Attributes API exists but is admin-only.
- **Self-hosted instances are the common case** for GitLab — base
  URL is per-instance config, like Forgejo.

I expect the work to look more like INV-0004's Forgejo refactor
than a from-scratch effort, because the interface seam already
exists.

## Context

**Triggered by:** Operator review during INV-0006 follow-up
discussion (2026-05-29). The same operator runs 20+ GitHub orgs
plus a few self-hosted GitLab instances. INV-0006 clarified that
GitLab support is orthogonal to per-org-app-credentials — it sits
behind the existing `Provider` abstraction (INV-0004).

This investigation captures the GitLab-specific shape so that when
the operator is ready to onboard the GitLab instances, the work
isn't blocked on a clean-slate analysis.

## Approach

1. Confirm the `Provider` interface seam from INV-0004 covers the
   GitLab method set.
2. Identify the supported Go SDK for GitLab in 2026.
3. Cross-reference each `Provider` method against the GitLab REST
   API v4 and map 1:1 / requires-translation / no-equivalent.
4. Audit GitLab's auth model — PAT, Project Access Token, Group
   Access Token, OAuth app — and decide which fits the
   reconciler's long-lived-background-job persona.
5. Compare webhook authentication and payload shape against
   GitHub's existing handler.
6. Inspect GitLab's rate-limit headers and map them onto the
   sweeper's `RateLimitRemaining` gate.
7. Build a cross-provider feature matrix covering every rule,
   setting, and reconciler in repo-guardian today, so the gaps
   are explicit before design begins.
8. Confirm whether Wiz (the primary downstream consumer of the
   `custom_properties` reconciler) actually reads GitLab Topics
   or Custom Attributes — informs whether the GitLab analog is
   even necessary.

## Findings

### Finding 1 — Go SDK

**Use `gitlab.com/gitlab-org/api/client-go`** for any new Go
integration in 2026.

- `github.com/xanzy/go-gitlab` was archived **2024-12-10**. The
  final release was a deprecation pointer to the new home. It
  still resolves via the Go module proxy but receives no updates.
- The replacement lives at `gitlab.com/gitlab-org/api/client-go`,
  reached v1.0 with a backwards-compatibility guarantee, and is
  at **v1.8.1** as of 2026.
- **Important caveat:** the module is hosted under `gitlab-org/`
  but is community-maintained. It is **not** covered by GitLab
  paid-subscription support.
- API surface is largely a drop-in for `xanzy/go-gitlab`;
  migration is mostly an import-path swap.
- A community fork lives at
  `gitlab.com/gitlab-community/gitlab-org/api/client-go` if the
  primary maintainer cadence slips.

### Finding 2 — CODEOWNERS support

**GitLab: Premium/Ultimate only. Different paths from GitHub.
Different syntax.**

| Axis | GitHub | GitLab | Forgejo |
|---|---|---|---|
| Tier | Free+ | Premium / Ultimate only | Free |
| Paths | `.github/CODEOWNERS`, root, `docs/` | root, `docs/`, `.gitlab/CODEOWNERS` | `.forgejo/`, `.gitea/`, root, `docs/` |
| Reads `.github/CODEOWNERS`? | Yes | **No** | **No** |
| Syntax | gitignore globs | `File::fnmatch` (shell-glob subset) + sections + role-based `@@Developer`/`@@Maintainer`/`@@Owner` | Go regex (NOT globs) |
| Enforcement | Free | Premium + protected branch | **Advisory only** (any write-access user can approve) |

**Implications for the `codeowners` file rule:**

- Path strategy must be provider-aware. GitHub-migrated repos
  carrying `.github/CODEOWNERS` are silently uncovered on both
  GitLab and Forgejo. The `FileRule` registry needs provider-specific
  `Paths` resolution at check time.
- The current rule body (stamp a default CODEOWNERS pointing at
  the default reviewer team) is the same shape on every provider —
  only the path changes.
- Document clearly that Forgejo CODEOWNERS is advisory-only —
  setting up the file does NOT block merges without owner approval.

### Finding 3 — Custom-properties equivalent

**Two GitLab candidates. Topics is the realistic target; Custom
Attributes is admin-only and impractical.**

| Surface | Scope | Permission needed | Verdict |
|---|---|---|---|
| **Topics** (`/projects/:id`, `topics` field; Topics API) | String list per project, widely consumed by external tools (Backstage, etc.) | Project Maintainer | **Recommended target.** Pragmatic, works for non-admin tokens, ecosystem-friendly. |
| **Custom Attributes** (`GET/PUT /projects/:id/custom_attributes/:key`) | Arbitrary key/value, true GitHub-Custom-Properties analog | **Instance admin only** | Impractical. Cannot set from a normal user / Project Access Token. Terraform's `gitlab_project_custom_attribute` exists but requires admin token. |
| CI/CD variables (`/projects/:id/variables`) | Key/value, but secrets-shaped | Maintainer | Wrong shape. Not metadata. |
| Description | Single text blob | Maintainer | Not key/value. |

**Recommendation:** if a GitLab `custom_properties` reconciler is
built, sync `catalog-info.yaml` values into **Topics**. Drop the
Custom Attributes path entirely — its admin-only requirement
would force operators to grant the bot's PAT
instance-administrator rights, which violates least-privilege.

### Finding 4 — Webhook authentication

**Two mechanisms — pick the modern one.**

- **Legacy** (`X-Gitlab-Token` header, plaintext static secret,
  constant-time compare): still default for GitLab.com webhooks
  configured before 19.1. Marked "not recommended for new webhooks"
  in current docs. Secret is on the wire every request.
- **Modern** (Standard Webhooks signing, GitLab 19.1+, 2026):
  headers `webhook-id`, `webhook-timestamp`, `webhook-signature`.
  Signature format `v1,{base64_signature}` where HMAC-SHA256 is
  over `{webhook-id}.{webhook-timestamp}.{body}`. Same scheme
  Svix, Resend, Polar.sh use.

**Payload shapes diverge significantly from GitHub.** GitLab uses
event-type-specific top-level keys (`object_kind: "push" |
"merge_request" | ...`), nested `project` and `user` objects, and
entirely different field names. **Not drop-in compatible** —
`internal/webhook/` needs per-provider schema dispatch (route by
header, then dispatch to a per-provider parser).

### Finding 5 — Rate-limit model

**Per-user (authenticated) or per-IP (unauthenticated),
operator-configurable on self-hosted.**

- Headers (`RateLimit-Limit`, `RateLimit-Remaining`,
  `RateLimit-Reset`) follow the IETF draft standard since GitLab
  13.8. `Retry-After` on 429.
- **No per-installation/per-app bucket** equivalent to GitHub's
  5k/15k installation buckets. The closest model is per-token —
  one bot token consumed by repo-guardian counts as one user.
- **GitLab.com:** limits are fixed.
- **Self-hosted:** fully operator-configurable. Setting `0`
  disables a limit.
- **Caveat:** only the most-restrictive Rack::Attack rate limit
  is reflected in headers. Application-level limits (issue
  creation, project export, etc.) can still 429 you without
  warning in headers.

**Implication:** `Client.RateLimitRemaining(installationID)`
should be polymorphic over provider — the parameter changes semantic
meaning per backend. On GitLab, the "installation ID" maps to the
token identity (or the project group). The reserve-gate logic in
`internal/checker/sweep.go` itself is reusable; only the lookup
key changes.

### Finding 6 — Wiz consumption (does custom_properties even matter on GitLab?)

**Likely not.** Wiz's public docs do not call out GitLab Topics or
Custom Attributes as a primary categorization signal.

What Wiz **does** appear to consume from a connected GitLab
instance:

- Repo metadata (name, default branch, description, languages).
- VCS structure: groups, subgroups, projects, branches, teams,
  user roles.
- CI/CD config (`.gitlab-ci.yml`).
- Code content scans (vulnerabilities, secrets, misconfigurations,
  IaC) on default branch and every MR.
- Benchmarks: GitLab CIS, OpenSSF SCM Best Practices, OWASP Top 10
  CI/CD risks.

**Implication:** if Wiz consumption is the primary driver for the
existing `custom_properties` reconciler on GitHub, the GitLab
equivalent may be **unnecessary for parity**. Before designing a
GitLab `custom_properties` reconciler, **confirm with the Wiz
team or a discovery call** what categorization signal Wiz reads
from GitLab. As a safe default — syncing `catalog-info.yaml`
values into GitLab **Topics** gives broad ecosystem value
(Backstage and most other tools consume Topics) even if Wiz
itself doesn't read them.

### Finding 7 — Auth model

**GitLab has no GitHub-App-equivalent installation-token mode.**

| Auth shape | Lifecycle | Scope | Best for |
|---|---|---|---|
| Personal Access Token (PAT) | User-bound; revoked when user offboarded | User-wide | Single-operator pilots only |
| Group Access Token | Group-bound (bot user backing) | Group-wide | **Recommended** for repo-guardian operating across many projects in one group |
| Project Access Token | Project-bound | Single project | Per-repo overrides if needed |
| OAuth App | User-delegated | Per-user OAuth grant | Wrong shape for background daemon |
| Service Account (GitLab 16.1+, Ultimate) | Admin-managed bot user | Configurable | Closest to GitHub-App spirit; Ultimate only |

**No JWT-bearer / auto-rotating installation tokens.** The
`ghinstallation` token-caching pattern doesn't apply on GitLab —
tokens are long-lived (configurable expiry, no auto-rotation).

**Recommendation:** default to a **Group Access Token** for the
reconciler, scoped to the group containing the managed projects.
Cache the token in the existing config surface (or a `provider.gitlab.token`
HCL block). Document the expiry/rotation responsibility on the
operator.

### Finding 8 — Groups as the scope unit

DESIGN-0010 introduced `scope { orgs = [...] }` for per-org rule
scoping on GitHub. The vocabulary needs to generalize for GitLab
and Forgejo without breaking existing configs.

**Org-like concepts per provider:**

| Provider | Scope unit | Shape |
|---|---|---|
| GitHub | Organization | Flat — one level |
| GitLab | Top-level group | **Hierarchical** — top-level group contains subgroups, projects, more subgroups (arbitrary nesting) |
| Forgejo | Organization | Flat — Gitea-derived model |

**Three options for the scope vocabulary:**

| Option | Shape | Trade-off |
|---|---|---|
| **A.** Keep `scope { orgs = [...] }` | Universal name; values are provider-specific slugs (GitLab top-level group, Forgejo org, GitHub org) | Backward-compatible. Term is GitHub-leaning but operators learn quickly. **Recommended.** |
| **B.** Rename to `scope { namespaces = [...] }` | Provider-agnostic name | Breaks every existing HCL config; requires migration recipe. Cosmetic win only. |
| **C.** Polymorphic: `scope { github_orgs, gitlab_groups, forgejo_orgs }` | Per-provider typed fields | Most precise but verbose; loader needs provider-aware dispatch; every operator with > 1 provider writes more YAML. |

**Recommendation: Option A.** `orgs` reads as "logical owners";
document in the HCL reference that the values map to whatever each
provider calls its top-level container. No breaking change.

**Subgroup semantics on GitLab.** Top-level groups can contain
arbitrary subgroup nesting (`myorg/sub/team/project`). The scope
matcher needs explicit semantics:

| Semantics | Behavior | Verdict |
|---|---|---|
| **Top-level only** | `orgs = ["myorg"]` matches `myorg/*` projects but not `myorg/sub/*` | Surprising; operator must explicitly enumerate subgroups. Rejected. |
| **Recursive (default)** | `orgs = ["myorg"]` matches every project owned by `myorg` at any depth | Matches GitHub flat-org expectations. **Recommended.** |
| **Explicit only** | `orgs = ["myorg", "myorg/sub"]` — no recursion, no shortcuts | Over-precise; operators write more config; combinatoric for deep hierarchies. Rejected. |

**Recommended default: recursive.** Operators who need narrower
targeting use the existing glob-pattern support in DESIGN-0010 to
match specific paths (`orgs = ["myorg/prod-*"]`). This keeps the
single-org GitHub idiom unchanged while supporting GitLab's
nesting cleanly.

**Cross-provider scope evaluation:** when multiple provider backends are
configured, `orgs = [...]` matches against the **fully-qualified
project owner** for each backend. Two distinct GitLab instances
both having a top-level group named `platform` are NOT
ambiguous — they're distinguished by the `provider { ... }` instance
name, not by the org slug.

## Cross-provider feature matrix

The repo-guardian feature surface, mapped across all three providers.
Legend: **✓** = directly supported, **~** = supported with
caveats, **✗** = not supported, **N/A** = concept doesn't apply.

### File rules

| Rule | GitHub (today) | GitLab | Forgejo |
|---|---|---|---|
| `codeowners` | ✓ paths: `.github/CODEOWNERS`, root, `docs/` | ~ paths differ: root, `docs/`, `.gitlab/`; Premium/Ultimate for enforcement | ~ paths differ: `.forgejo/`, `.gitea/`, root, `docs/`; advisory-only |
| `dependabot` | ✓ `.github/dependabot.yml` | ✗ Dependabot is GitHub-only; GitLab uses native Dependency Scanning (CI-driven) | ✗ no Dependabot |
| `renovate_config` | ✓ `renovate.json*` (disabled by default) | ✓ same file; Renovate self-hosts against GitLab | ✓ same file |
| `renovate_workflow` | ✓ `.github/workflows/renovate.yml` (disabled by default) | ~ translate to `.gitlab-ci.yml` job | ~ translate to `.forgejo/workflows/renovate.yml` |
| `catalog_info` | ✓ `catalog-info.yaml`, paired with `custom_properties` reconciler | ✓ same file; reconciler retargets to **Topics** instead of Custom Properties | ✓ same file; reconciler retargets to **Topics** |

### Setting rules

| Setting | GitHub (today) | GitLab | Forgejo |
|---|---|---|---|
| `default_branch` | ✓ `Repositories.Edit.DefaultBranch` | ✓ `projects.default_branch` | ✓ same |
| `has_issues` | ✓ | ✓ `issues_enabled` | ✓ |
| `has_wiki` | ✓ | ✓ `wiki_enabled` | ✓ |
| `delete_branch_on_merge` | ✓ | ✓ `remove_source_branch_after_merge` | ✓ |
| `allow_merge_commit` | ✓ | ~ part of `merge_method` enum (`merge`, `rebase_merge`, `ff`) | ~ similar |
| `allow_squash_merge` | ✓ | ~ separate flag `squash_option` | ~ similar |
| `allow_rebase_merge` | ✓ | ~ encoded in `merge_method` | ~ similar |
| `vulnerability_alerts_enabled` | ✓ (Dependabot Alerts GraphQL) | ~ GitLab uses Dependency Scanning + Container Scanning CI jobs; no on/off toggle in the same shape | ✗ |

GitLab merge-strategy settings need an HCL-level translation:
GitLab's `merge_method` is a single enum, while GitHub exposes
three booleans. The setting-rule schema may need a provider-aware
remediator.

### Reconcilers

| Reconciler | GitHub (today) | GitLab | Forgejo |
|---|---|---|---|
| `custom_properties` | ✓ Custom Properties API | ~ retarget to **Topics** (Custom Attributes is admin-only, impractical); **may be unnecessary if Wiz is the only consumer** | ~ retarget to Topics |
| `label_sync` | ✓ `Issues.ListLabels`/`Create`/`Edit`/`Delete` | ✓ same shape, project-scoped | ✓ same shape |
| `branch_protection` | ✓ Repository Rulesets API | ~ split across Protected Branches, Push Rules (Premium), MR Approval Settings — three separate concepts. Needs a GitLab-specific reconciler. | ~ Forgejo has Branch Protection but not Rulesets-shape |
| `workflow_sync` | ✓ observational on `.github/workflows/` | ✓ retarget to `.gitlab-ci.yml` (single file, not a directory) | ✓ retarget to `.forgejo/workflows/` |

### Platform plumbing

| Concern | GitHub (today) | GitLab | Forgejo |
|---|---|---|---|
| Go SDK | `google/go-github` v68 + `ghinstallation` v2 | `gitlab.com/gitlab-org/api/client-go` v1.8.1 (community, not paid-support-covered) | `code.gitea.io/sdk/gitea` (Forgejo is Gitea-API-compatible) |
| Auth model | GitHub App + installation tokens (JWT, auto-rotating) | PAT / **Group Access Token** (recommended) / Project Access Token / Service Account (Ultimate); long-lived, no auto-rotate | PAT |
| Webhook auth | HMAC-SHA256 in `X-Hub-Signature-256` | Legacy: `X-Gitlab-Token` plaintext. Modern (19.1+): Standard Webhooks (`webhook-signature`) | HMAC-SHA256 in `X-Forgejo-Signature` |
| Webhook payload | GitHub event types | `object_kind`-keyed; nested `project`/`user`; entirely different field names | Gitea-compatible payload |
| Rate-limit gate | Per-installation bucket (5k/15k/hr) | Per-user (authed) / per-IP (unauthed); headers `RateLimit-*`; operator-tunable on self-hosted | Operator-configured |
| Multi-org/instance model | Multi-installation under one App (or per-org Apps per INV-0006) | Multi-instance (each GitLab is its own URL + token) | Multi-instance |
| Custom-properties consumer | Wiz uses GitHub Custom Properties | Wiz does NOT appear to consume GitLab Topics/Custom Attributes (confirm w/ vendor) | N/A |

## Implementation taxonomy: config-only vs code changes

Adding a new provider backend splits cleanly into three buckets. The
distinction matters for scoping the work and for setting operator
expectations about what a future backend toggle costs.

| Bucket | Where | Examples |
|---|---|---|
| **Pure HCL config** | Operator sets values in `guardian.hcl`; no code change. | `provider.gitlab.url`, `provider.gitlab.token`, per-provider `scope { orgs }` lists, per-provider `ignore { }` blocks, all rule definitions (paths, templates, assertions), PR template scopes |
| **Code — provider backend** | New Go code in `internal/provider/<name>/` implementing the `Provider` interface from INV-0004. Built once into the binary. | GitLab API client + auth + rate-limit gate, per-provider webhook payload parser, retry/backoff per provider, Standard Webhooks signing verifier |
| **Code — provider-aware reconciler logic** | Existing reconciler / rule has different semantics per provider. Branches inside the reconciler. | `branch_protection` reconciler (3-way split on GitLab into Protected Branches + Push Rules + MR Approvals), `codeowners` path strategy, `custom_properties` retargeting to Topics, `dependabot` rule disabled outside GitHub |

**No reconciler or rule is currently "pure config" for a brand-new
backend.** Every reconciler in the codebase today contains
GitHub-specific code paths (custom-properties API calls, rulesets
JSON shape, etc.). Adding a provider means *either* writing the
provider-side code for each reconciler you want to support, *or*
having the reconciler return "unavailable on this backend" the
same way INV-0004 resolved for Forgejo gaps.

**Config knobs a new provider introduces:**

```hcl
provider "gitlab" "instance-1" {
  url   = "https://gitlab.example.com"  # required, no default
  token = env("GITLAB_TOKEN")            # Group Access Token recommended
  # rate_limit_threshold inherits from guardian {} defaults
}

provider "github" "default" {
  # existing GitHub App config; backward-compatible
  app_id              = env("GITHUB_APP_ID")
  private_key         = env("GITHUB_PRIVATE_KEY")
  webhook_secret      = env("WEBHOOK_SECRET")
}
```

`provider { }` is keyed by `<type> <name>` so an operator can run
repo-guardian against multiple GitLab instances (different
self-hosted servers, or `gitlab.com` + on-prem) from one deployment.
Each instance is an independent `Provider` instance with its own
client, rate-limit bucket, and scope.

**Order of work for a fresh backend.** This is the rough sequence
once a backend gets promoted from INV to DESIGN to IMPL:

1. **Interface compatibility check** — confirm `Provider` covers every
   method needed, add new methods if not (e.g.,
   `ManageTopics(ctx, project, []string)` for GitLab).
2. **Backend package** — `internal/provider/<name>/` implementing the
   interface, with its own tests.
3. **Auth + webhook plumbing** — config block, secret loading,
   webhook handler dispatch (route or header-based).
4. **Reconciler audit** — for each existing reconciler, add the
   provider code path OR flag it explicitly unavailable.
5. **Path / setting strategy adjustments** — `codeowners` paths,
   merge-strategy enum translation for GitLab, etc.
6. **Chart values + docs** — `provider.gitlab.*` values, ECR/GHCR
   parity, operator runbook.

Skipping any one of these surfaces as a runtime failure or as
silent feature unavailability the operator only discovers from a
log line.

## Conclusion

**Answer:** _Inconclusive pending execution_ — but the preliminary
research is strong enough to scope the work:

- **Feasible.** The `Provider` interface seam from INV-0004 absorbs
  GitLab cleanly. The community Go SDK is stable, well-maintained,
  and at v1.8.1.
- **Largest gaps:** `custom_properties` (Topics is the only
  realistic target, and may not even matter if Wiz doesn't read
  it) and `branch_protection` (GitLab splits the GitHub Rulesets
  concept into three separate APIs — needs a GitLab-specific
  reconciler implementation).
- **Smallest gaps:** file CRUD, MR (PR) operations, labels,
  comments. These map 1:1 with renames only.
- **Path strategy gap:** `codeowners` file rule's hard-coded
  `.github/CODEOWNERS` default needs provider-aware path resolution
  — not just GitLab, but Forgejo too.
- **Webhook handler refactor required.** Standard Webhooks
  signing dispatch + per-provider payload parser. Not trivial; needs
  its own DESIGN doc.

## Recommendation

**Promote to DESIGN when operator is ready to onboard GitLab
instances.** Specifically, the DESIGN doc should cover:

1. The `Provider` interface contract — confirm INV-0004's seam,
   identify any new methods needed (e.g.,
   `ManageTopics(ctx, project, []string)`).
2. Auth strategy — Group Access Token as default, with
   per-instance config block.
3. Webhook router — Standard Webhooks signing dispatch, per-provider
   payload parser, route prefix or header-based dispatch.
4. The `branch_protection` reconciler split for GitLab (Protected
   Branches API + MR Approval Settings + Push Rules).
5. Topics-vs-Custom-Properties decision for `custom_properties`,
   pending the Wiz-consumption confirmation.
6. Helm chart auth surface (`provider.gitlab.token` block,
   `existingSecret` plumbing).
7. Migration recipe — how an operator running repo-guardian
   against GitHub today adds a GitLab instance without disturbing
   the existing deployment.

## Open questions

These are blocking design decisions, not blocking the
investigation itself:

- **(a)** Confirm with Wiz (or the platform team that owns the
  Wiz integration) what categorization signal Wiz reads from
  GitLab. If "none / not first-class," drop GitLab
  `custom_properties` from MVP scope.
- **(b)** Does the operator's deployment use Premium/Ultimate
  GitLab? CODEOWNERS enforcement and Push Rules require it. If
  Free-tier, scope down the GitLab reconciler set accordingly.
- **(c)** GitLab Service Accounts (Ultimate only, 16.1+) are the
  closest analog to GitHub Apps. If the deployment has Ultimate,
  prefer Service Accounts over Group Access Tokens. Confirm
  edition.
- **(d)** Webhook URL routing — single `/webhook` with header-based
  dispatch (recommended), or per-provider route prefix
  (`/webhook/github`, `/webhook/gitlab`, `/webhook/forgejo`)?
  Single-route + header dispatch reuses existing IP-allowlist
  middleware patterns more cleanly.

## References

- [INV-0004](0004-forge-interface-and-package-refactor-for-forgejo-backend.md)
  — established the `Provider` interface seam this backend slots
  into. Required reading before executing this investigation.
- [INV-0002](0002-multi-org-and-forgejo-support-for-repo-guardian.md)
  — earlier multi-provider thinking; predates the current
  `Provider`-abstraction layout.
- [INV-0006](0006-per-org-github-app-credentials.md) — the
  conversation that surfaced this question. INV-0006 clarified
  that GitLab support is orthogonal to per-org-app-credentials.
- [`gitlab.com/gitlab-org/api/client-go`](https://gitlab.com/gitlab-org/api/client-go)
  — recommended Go SDK.
- [GitLab Docs: Code Owners](https://docs.gitlab.com/user/project/codeowners/)
- [GitLab Docs: CODEOWNERS reference](https://docs.gitlab.com/user/project/codeowners/reference/)
- [GitLab Docs: Custom Attributes API](https://docs.gitlab.com/api/custom_attributes/)
- [GitLab Docs: Topics API](https://docs.gitlab.com/api/topics/)
- [GitLab Docs: Webhooks](https://docs.gitlab.com/user/project/integrations/webhooks/)
- [GitLab Docs: User and IP rate limits](https://docs.gitlab.com/administration/settings/user_and_ip_rate_limits/)
- [GitLab Docs: Rate limits overview](https://docs.gitlab.com/security/rate_limits/)
- [Forgejo Docs: Webhooks](https://forgejo.org/docs/next/user/webhooks/)
- [`code.gitea.io/sdk/gitea`](https://pkg.go.dev/code.gitea.io/sdk/gitea)
  — Forgejo-compatible Go SDK.
- [Wiz Blog: Unify security across GitHub, GitLab, Azure Repos](https://www.wiz.io/blog/wiz-code-unify-security-across-github-gitlab-and-azure-repos)
  — Wiz consumption story.
- [Backstage GitLab integration](https://backstage.io/docs/integrations/gitlab/discovery/)
  — Topics consumption example.
