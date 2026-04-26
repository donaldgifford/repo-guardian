---
id: INV-0002
title: "Multi-org and Forgejo support for repo-guardian"
status: Open
author: Donald Gifford
created: 2026-04-25
---
<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0002: Multi-org and Forgejo support for repo-guardian

**Status:** Open
**Author:** Donald Gifford
**Date:** 2026-04-25

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [Multi-installation of one GitHub App: already supported](#multi-installation-of-one-github-app-already-supported)
  - [Multi-App: not supported, but moderate refactor](#multi-app-not-supported-but-moderate-refactor)
  - [Per-org rule scoping: not implemented](#per-org-rule-scoping-not-implemented)
  - [Forgejo: feasible for core flow with caveats](#forgejo-feasible-for-core-flow-with-caveats)
  - [Reconciler portability](#reconciler-portability)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!--toc:end-->

## Question

Two related questions about extending repo-guardian beyond its current
single-GitHub-App, single-policy scope:

1. **Multi-org / multi-app:** Can repo-guardian be configured with multiple
   "guardians" — i.e., multiple GitHub Apps connected to different
   organizations — with shared rules across all, per-org rule overrides, or
   some combination?
2. **Forgejo:** Could repo-guardian connect to Forgejo (Gitea fork, v15.x)
   to offer the same file-PR / compliance workflow there?

For each, what would need to change in the current architecture?

## Hypothesis

- Multi-org via **multiple installations of one GitHub App is already
  supported** (the scheduler iterates installations, `RepoJob` carries an
  `InstallationID`, and there's a `CreateInstallationClient` factory). What's
  missing is per-org rule scoping in the HCL policy.
- Multi-org via **multiple distinct Apps** is *not* supported because config,
  webhook secret, and HMAC validation are single-tenant.
- Forgejo support is **technically feasible** for the file-PR core flow, but
  two reconcilers (`branch_protection` via rulesets, `custom_properties`)
  have no Forgejo equivalent and would need either alternate implementations
  or feature flags that disable them per backend.

## Context

repo-guardian was scoped initially to a single GitHub App enforcing one
policy across one org's repos. The HCL policy engine, reconciler pattern,
and per-installation client factory are all in place. Two real-world
pressures motivate this investigation:

- **Multi-org:** Operating multiple GitHub orgs (e.g., production org +
  experimental org, or post-acquisition consolidation) where each may need
  shared baseline rules plus org-specific overrides.
- **Forgejo:** Some teams prefer self-hosted Git forges. Forgejo v15 (April
  2026, LTS) is mature enough that the same compliance workflow could
  meaningfully run there.

**Triggered by:** ad-hoc product question — "what would it take to support
multiple guardians?"

## Approach

1. Map current code seams that assume "one GitHub App, one org" via static
   exploration (file paths + line numbers).
2. Identify clean abstraction boundaries that already exist (interfaces,
   factories) versus hard-coupled GitHub-specific code paths.
3. For Forgejo: catalog what API features it offers in v15.x and compare
   feature-by-feature to repo-guardian's GitHub usage.
4. Propose phased work that delivers value at each step rather than a
   rewrite.

## Environment

| Component | Version / Value |
|-----------|-----------------|
| repo-guardian | main @ 2026-04-25 |
| go-github | v68 |
| ghinstallation | v2 |
| Forgejo (target) | v15.0 LTS (2026-04-16) |
| Forgejo Go SDK | `code.forgejo.org/forgejo/go-sdk` |

## Findings

### Multi-installation of one GitHub App: already supported

The architecture already iterates multiple installations of a single App.
This naturally covers the "one App installed across multiple orgs" case —
nothing new is needed for that flavor of multi-org.

| Concern | Where | Status |
|---------|-------|--------|
| Per-installation client factory | `internal/github/client.go:294` (`CreateInstallationClient`) | Caches `installClients map[int64]*gh.Client` |
| Job carries installation ID | `internal/checker/queue.go:35` (`RepoJob.InstallationID`) | Used at line 166 to scope client |
| Scheduler enumerates installations | `internal/scheduler/scheduler.go:71-80` | Calls `ListInstallations()` then loops `ListInstallationRepos(install.ID)` |
| Webhook extracts installation ID | `internal/webhook/handler.go:86,103,121,166` | `e.GetInstallation().GetID()` |

What's *not* there:

- Metrics carry no `installation_id` / `org` label — `internal/metrics/metrics.go`
  uses `trigger`, `event_type`, `operation`, `reason`, `scope`, `rule_name`
  but nothing identifying which org work happened on. Hard to slice MTTR or
  error rates per org.

### Multi-App: not supported, but moderate refactor

A *second* GitHub App (different `APP_ID` + private key, different webhook
secret) would require changes in three places:

1. **Config** (`internal/config/config.go:79-172`): single `GITHUB_APP_ID`,
   single `GITHUB_PRIVATE_KEY[_PATH]`, single `GITHUB_WEBHOOK_SECRET`. Would
   need to become a list (e.g., HCL `app "<name>" { app_id = ..., ... }`
   blocks) or numbered env vars.
2. **Webhook routing** (`internal/webhook/handler.go:41`): `gh.ValidatePayload`
   takes one secret. Either route per-app to distinct endpoints
   (`/webhooks/github/app-a`, `/webhooks/github/app-b`) or try-each-secret
   on a single endpoint. The latter is uglier but preserves a single GitHub
   App webhook URL per App registration.
3. **Client construction** (`internal/github/client.go:31-63`): `NewClient`
   creates one `ghinstallation.AppsTransport`. Would become a registry of
   App transports keyed by App ID, with `CreateInstallationClient` taking
   `(appID, installationID)`.

The `Client` interface (`internal/github/github.go:109-194`) is the clean
seam — callers already use it, so swapping in a multi-app-aware constructor
doesn't ripple downstream.

### Per-org rule scoping: not implemented

The HCL policy engine treats rules as global. There is a `GuardianConfig.Org`
field (`internal/policy/types.go:34`) populated from the `GITHUB_ORG` env var
or the `guardian { org = "..." }` block, but **it's used only inside
assertion patterns the user writes** — there is no runtime gate that limits
rules to specific orgs.

To support "shared rules + per-org overrides," the most natural extension is
the existing `ignore {}` pattern in reverse — a `scope {}` block that
opts a rule into a set of orgs:

```hcl
rule "file" "renovate_workflow" {
  # ...
  scope {
    orgs = ["myorg-prod", "myorg-staging"]
  }
}
```

The matching logic would slot in next to `matchesIgnoreList` with
near-identical structure (glob matching, lowercase normalize). No engine
restructure needed.

### Forgejo: feasible for core flow with caveats

Forgejo v15.0 (LTS, 2026-04-16) provides the API primitives needed for
repo-guardian's *file-PR* path. The auth model is materially different and
two reconcilers can't be ported.

| GitHub feature | Forgejo equivalent | Repo-guardian impact |
|---|---|---|
| GitHub App + installation tokens | **No equivalent.** Closest analog: repo-scoped PATs (v15) on a bot user account. | Replace `ghinstallation.AppsTransport` with PAT auth. Lose multi-tenant installation isolation. |
| Webhook HMAC | **Yes** — `X-Forgejo-Signature` (HMAC-SHA256). | Direct port. |
| Repo contents read/write API | **Yes** — `/repos/{o}/{r}/contents/{path}`. | Direct port. |
| Branch / PR creation | **Yes** with subtle divergences (the popular `peter-evans/create-pull-request` action doesn't work — there's a Forgejo-specific fork). | Integration testing required. |
| Repository labels | **Yes** — `/repos/{o}/{r}/labels`. | Direct port. |
| Repository settings | **Partial** — `PATCH /repos/{o}/{r}` exposes some attributes but the field set differs from GitHub's `delete_branch_on_merge` etc. | Setting rule semantics need per-backend mapping. |
| Branch protection | **Legacy only** — `/repos/{o}/{r}/branch_protections`. **No rulesets API.** | `branch_protection` reconciler needs a Forgejo-specific implementation. |
| Custom properties | **No equivalent.** Closest analog: repo topics (much weaker semantics). | `custom_properties` reconciler is unportable; would skip on Forgejo. |
| Go SDK | **`code.forgejo.org/forgejo/go-sdk`** (active fork from Gitea SDK). | Add as dependency; introduce `Forge` interface; keep `go-github` for GitHub. |

The auth simplification (PAT vs JWT-minted installation token) is actually
operationally easier — but it loses the per-installation security boundary
that GitHub Apps give you. A bot user with a PAT has the union of all
permissions across all orgs it's a member of.

### Reconciler portability

| Reconciler | GitHub | Forgejo | Notes |
|------------|--------|---------|-------|
| `workflow_sync` | ✓ | ✓ | Pure file watching; portable. |
| `label_sync` | ✓ | ✓ | Labels API exists in Forgejo. |
| `custom_properties` | ✓ | ✗ | No equivalent. Disable per backend. |
| `branch_protection` | ✓ (rulesets) | ⚠ (legacy branch protection only) | Needs a second implementation that targets the legacy API. |

This argues for a `Reconciler` capability flag (`SupportedForges() []string`)
that the engine consults before registering it for a given guardian. Cleaner
than runtime errors mid-reconciliation.

## Conclusion

**Answer: Both are feasible, but they're three distinct chunks of work
with very different sizes.**

1. **Multi-org via single GitHub App across multiple orgs:** *Already
   supported.* The only gap is observability (no `org` / `installation_id`
   label on metrics). Zero-to-low effort.
2. **Per-org rule scoping in HCL:** Small. Add a `scope { orgs = [...] }`
   block mirroring the existing `ignore { repos = [...] }` pattern. No
   engine restructure.
3. **Multi-App (multiple distinct GitHub Apps in one repo-guardian):**
   Moderate. Config schema changes + webhook routing + client registry.
   The `Client` interface boundary keeps the blast radius contained.
4. **Forgejo support:** Large. Requires a `Forge` interface abstraction over
   `internal/github`, a Forgejo client implementation, per-forge config
   schema, reconciler capability flags, and accepting that two existing
   reconcilers (`custom_properties`, `branch_protection`) either don't run
   or need a second implementation. This is essentially "make repo-guardian
   forge-agnostic" — a major effort that would benefit from being preceded
   by the multi-app refactor (which forces the same abstraction work).

## Recommendation

Three phased designs, each independently shippable:

1. **DESIGN: Per-org rule scoping** *(small, high value)*
   Add `scope { orgs = [...] }` to file/setting/branch-protection rule
   blocks, plus `org` / `installation_id` labels on Prometheus metrics.
   Unlocks "shared rules + per-org overrides" using only what's already
   in the codebase.
2. **DESIGN: Multi-App support** *(medium, conditional on demand)*
   Move single-App env vars to an HCL `app "<name>" {}` block, build an
   App-keyed transport registry, decide on per-app webhook endpoints. Worth
   doing only if there's a concrete use case; otherwise teams should run
   one `repo-guardian` per App, which is simpler and already works.
3. **RFC: Forge abstraction for Forgejo (and beyond)** *(large, exploratory)*
   Treat this as an RFC first because the API and trade-offs (auth model
   loss, reconciler capability gating, dual SDK dependencies) need
   discussion before committing. The forge-abstraction work overlaps
   substantially with multi-app — sequence them so multi-app forces the
   interface boundary, then Forgejo plugs in behind it.

Operationally: start with (1). It's worth doing regardless of where the
other two land, and it surfaces the per-org dimension everywhere (metrics,
config, runtime behavior) that the larger work will need anyway.

## References

- [DESIGN-0006](../design/0006-hcl-policy-configuration-and-rule-engine.md) — HCL policy engine
- [DESIGN-0007](../design/0007-reconciler-interface-and-push-event-handler.md) — Reconciler interface
- [DESIGN-0008](../design/0008-additional-rule-types-and-ignore-lists.md) — Ignore lists pattern (template for `scope {}`)
- [Forgejo v15.0 release announcement](https://forgejo.org/2026-04-release-v15-0/)
- [Forgejo API usage docs](https://forgejo.org/docs/next/user/api-usage/)
- [Forgejo branch protection docs](https://forgejo.org/docs/latest/user/protection/)
- [Forgejo Go SDK](https://code.forgejo.org/forgejo/go-sdk)
- [Forgejo issue #3571 — granular Action token permissions](https://codeberg.org/forgejo/forgejo/issues/3571)
