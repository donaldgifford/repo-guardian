---
id: INV-0004
title: "Forge interface and package refactor for Forgejo backend"
status: Resolved
author: Donald Gifford
created: 2026-05-03
---
<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0004: Forge interface and package refactor for Forgejo backend

**Status:** Resolved
**Author:** Donald Gifford
**Date:** 2026-05-03
**Resolved:** 2026-05-03 (PR #71 review walkthrough)

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [Observation 1: Method classification (preliminary)](#observation-1-method-classification-preliminary)
  - [Observation 2: Auth abstraction](#observation-2-auth-abstraction)
  - [Observation 3: Feature-flag wiring (sketch)](#observation-3-feature-flag-wiring-sketch)
  - [Observation 4: Package layout proposal](#observation-4-package-layout-proposal)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!--toc:end-->

## Question

INV-0002 established that Forgejo backend support is feasible at the
feature level. This investigation goes one level deeper: **what is
the minimal package + interface refactor in `internal/github/`
needed to admit a second SCM backend without scattering
vendor-conditional code through the engine?**

Specifically:

1. Which methods on the existing `Client` interface are vendor-neutral
   (file CRUD, branches, PRs, labels) versus GitHub-specific (custom
   properties, rulesets-style branch protection, GitHub App
   installations)?
2. What's the right package layout — rename `internal/github` to
   `internal/scm` with provider sub-packages, or keep `internal/github`
   and add a parallel `internal/forgejo` with a shared abstraction
   above both?
3. How do we surface "this reconciler doesn't work on this backend"
   to the engine without runtime errors mid-reconcile?
4. What does the auth model look like when the binary supports both
   GitHub-App-installation auth and Forgejo PAT auth simultaneously?

## Hypothesis

Promote `internal/github.Client` to `internal/scm.Forge`. The bulk
of the methods (file CRUD, branches, PRs, labels, repo metadata)
already have direct Forgejo equivalents and stay on the interface
unchanged. The vendor-specific methods (custom properties, rulesets,
GitHub App installations) move to provider-specific extension
interfaces (`GitHubFeatures`, `ForgejoFeatures`) that the engine
queries via type assertion. Reconcilers declare `RequiredFeatures()`
at registration time; the engine refuses to wire a reconciler whose
required features the configured backend doesn't expose. This is
loud at startup, silent at runtime.

## Context

INV-0002 (April 2026) answered "is Forgejo support feasible?" — yes,
with caveats — but stopped at the feature comparison level. Since
then:

- DESIGN-0010 + IMPL-0009 closed the per-org scope question that
  was bundled with the Forgejo work in INV-0002.
- INV-0003 + PR #69 made `CreateOrUpdateFile` idempotent against
  pre-existing branches (a foundation any second backend will share).
- DESIGN-0012 (Draft) established the **interface-first pattern**
  for `Store` / `Queue` / `Scheduler`. Promoting `Client` to a `Forge`
  interface follows the same pattern and reuses the same testing
  conventions (mockery v2 for the new interface, in-memory fake
  for tests).

Without this refactor, adding Forgejo means either (a) duplicating
the engine's GitHub-shaped code path or (b) pushing GitHub-vs-Forgejo
conditionals into every reconciler — both of which scale poorly.

**Triggered by:** Operator interest in Forgejo as a self-hosted
target backend; INV-0002 conclusion that the gap is interface-shaped,
not feature-shaped.

## Approach

1. **Audit `internal/github.Client`** method by method. Tag each
   one as `core` (every backend implements it), `optional` (only
   some backends), or `vendor-only` (GitHub-specific, lives on a
   feature interface).
2. **Map the auth surface.** Compare GitHub App installation tokens
   (JWT-minted, per-installation, short-lived) to Forgejo PATs
   (long-lived, bot-user-scoped). Identify what the wrapping
   abstraction needs to expose.
3. **Sketch the `Forge` interface** with proposed Go signatures.
   Compile-check against the existing reconcilers to ensure they
   compile against the abstracted interface.
4. **Sketch the feature-flag mechanism**: `RequiredFeatures()` on
   reconcilers, `Features() []string` (or capability interfaces)
   on the forge. Fail-loud at startup wiring, not at reconcile
   time.
5. **HCL grammar sketch** for declaring multiple guardians, each
   bound to a forge type:

   ```hcl
   guardian "github_corp" {
     forge { type = "github"; app_id = ...; ... }
     scope { orgs = ["corp"] }
   }
   guardian "forgejo_internal" {
     forge { type = "forgejo"; base_url = "https://git.internal"; token_env = "FORGEJO_PAT" }
     scope { orgs = ["platform"] }
   }
   ```

6. **Migration cost estimate** for the existing
   `internal/github/client.go` and its tests — rename, move, or
   leave in place.
7. **Identify the smallest first IMPL** — probably "rename
   `internal/github` → `internal/scm/github`, define the `Forge`
   interface, move feature methods to a `GitHubFeatures` extension,
   ship with NO Forgejo implementation yet" — then a separate IMPL
   adds Forgejo.

## Environment

| Component | Version / Value |
|---|---|
| Forgejo target | v15.0 LTS (April 2026) — established in INV-0002 |
| Forgejo Go SDK | `code.forgejo.org/forgejo/go-sdk` |
| GitHub Go SDK | `github.com/google/go-github/v68` |
| Existing interface | `internal/github.Client` (~30 methods) |
| Reconcilers in scope | `custom_properties`, `label_sync`, `branch_protection`, `workflow_sync` |

## Findings

### Observation 1: Method classification (preliminary)

Audit of the 30 methods on `internal/github.Client`:

**Core (every SCM backend will have these):**

- `GetContents`, `GetFileContent` — file existence + content
- `CreateOrUpdateFile` — file write on a branch
- `GetBranchSHA`, `CreateBranch`, `DeleteBranch` — branch ops
- `CreatePullRequest`, `ListOpenPullRequests` — PR ops
- `GetRepository` — repo metadata (default branch, archived, fork)
- `ListLabels`, `CreateLabel`, `UpdateLabel`, `DeleteLabel` — labels

**Optional (Forgejo has different / weaker analog):**

- `GetRepoSettings`, `UpdateRepository` — Forgejo's `PATCH /repos/{o}/{r}`
  exposes a different field set; needs a backend-specific mapping
  layer for the `setting` rule type.
- `GetVulnerabilityAlertsEnabled`, `EnableVulnerabilityAlerts`,
  `DisableVulnerabilityAlerts` — Forgejo has scanning integrations
  but no GitHub Dependabot equivalent. Probably unportable.

**Vendor-only (GitHub-specific):**

- `GetCustomPropertyValues`, `SetCustomPropertyValues` — no Forgejo
  equivalent.
- `ListRepositoryRulesets`, `GetRepositoryRuleset`,
  `CreateRepositoryRuleset`, `UpdateRepositoryRuleset` — Forgejo
  has only legacy `branch_protections`, not rulesets.
- `ListInstallations`, `ListInstallationRepos`,
  `CreateInstallationClient` — App-installation model is
  GitHub-only.

**Roughly 15 of 30 methods are core; ~5 optional; ~10 vendor-only.**
The interface refactor moves the 10 vendor-only methods to feature
extensions; the 5 optional methods get a per-backend mapping layer.

### Observation 2: Auth abstraction

The current code uses `ghinstallation.AppsTransport` for App auth
and a per-installation token cache. Forgejo PAT auth is just a
bearer token. Proposed abstraction:

```go
package scm

type Authenticator interface {
    // RoundTripper returns the HTTP transport with credentials applied.
    RoundTripper(ctx context.Context) (http.RoundTripper, error)
}
```

Implementations:

- `auth/github.AppAuth` — wraps `ghinstallation.AppsTransport`.
  `RoundTripper` mints installation tokens on demand.
- `auth/github.PATAuth` — for testing or single-org PAT-mode GitHub.
- `auth/forgejo.PATAuth` — bearer token from env or secret.

The `Forge` factory takes an `Authenticator` and constructs the
SCM client. Per-installation factories (`CreateInstallationClient`)
become a vendor-only feature on `GitHubFeatures`.

### Observation 3: Feature-flag wiring (sketch)

```go
package scm

type Forge interface {
    // Core methods (file ops, branches, PRs, labels, repo metadata)
    // ... ~15 methods ...
}

type GitHubFeatures interface {
    GetCustomPropertyValues(...) (...)
    SetCustomPropertyValues(...) (...)
    ListRepositoryRulesets(...) (...)
    // ... all 10 GitHub-only methods ...
}

type ForgejoFeatures interface {
    // Forgejo-specific methods, e.g., topic management with
    // weaker semantics than custom properties
}
```

Reconciler registration declares features:

```go
type Reconciler interface {
    Name() string
    RequiredFeatures() []string  // e.g., ["github.custom_properties"]
    Reconcile(ctx, *ReconcileParams) error
}
```

At engine startup, for each `(guardian, reconciler)` pair, the
engine type-asserts the guardian's `Forge` against the reconciler's
required feature interfaces. Mismatch → fail startup with a clear
error: `reconciler "custom_properties" requires feature
"github.custom_properties" not provided by forge type "forgejo"`.

### Observation 4: Package layout proposal

```
internal/
  scm/
    forge.go              ← Forge interface + Authenticator
    github/
      client.go           ← was internal/github/client.go
      client_test.go
      features.go         ← GitHubFeatures impl
      auth.go             ← AppAuth + PATAuth
    forgejo/
      client.go           ← new
      client_test.go
      features.go         ← ForgejoFeatures impl
      auth.go             ← PATAuth
    mocks/                ← mockery output for Forge + features
```

`internal/github` becomes a deprecated alias for
`internal/scm/github` for one minor release, then removed.

## Conclusion

**Answer: Yes — promote `internal/github.Client` to a `Provider`
interface in `internal/scm/`, refactor the existing GitHub code
behind it, and treat Forgejo as empirical validation rather than
upfront design.**

The interface refactor is straightforward — ~15 core methods, ~10
GitHub-only methods on a feature extension interface, ~5 with
per-backend mapping (preliminary classification in Observation 1).
The reconciler `RequiredFeatures()` mechanism cleanly solves the
"which reconciler works where" problem. Auth is naturally
pluggable behind an `Authenticator` interface.

What we **deliberately don't decide here**: the exact Forgejo
implementation shape, the `code.forgejo.org/forgejo/go-sdk`
maturity story, the Forgejo PR-flow gotchas beyond what INV-0002
flagged, and the multi-guardian HCL grammar. Those are best
answered by writing a Forgejo provider against the interface
*after* the abstraction is in code, not designed in advance for a
backend we haven't built. Designing the abstraction up front for a
hypothetical second backend is the over-design failure mode.

## Recommendation

Pragmatic two-step path:

1. **IMPL: Provider interface refactor** (next in queue, separate
   from PR #71). Rename `internal/github` →
   `internal/scm/github`. Define `internal/scm.Provider` interface
   (the ~15 core methods). Extract GitHub-only methods to
   `internal/scm/github.GitHubFeatures` extension interface. Wire
   reconciler `RequiredFeatures()` capability declarations. Move
   the existing `Authenticator` shape (App installation transport
   + token cache) behind a small interface. **No Forgejo yet.**
   The existing GitHub backend is the only `Provider` implementation.
   CI stays green throughout. This IMPL is mostly mechanical —
   rename, move, extract.

2. **Future work** (no doc needed yet): add a Forgejo provider
   against the same interface. Whatever falls out of that —
   missing methods, wrong abstraction shape, auth model surprise —
   surfaces empirically and informs whatever follow-up DESIGN, INV,
   or IMPL it needs. Multi-guardian HCL grammar (the "multi-app"
   question from INV-0002) gets its own design only if the Forgejo
   implementation creates a real need for it.

This INV is closed by step 1 happening; step 2 is open-ended and
gets new artifacts only when work begins on it.

## References

- INV-0002 (multi-org and Forgejo support — feasibility findings,
  this INV builds on its conclusions)
- DESIGN-0012 (interface-first pattern this refactor follows)
- INV-0003 (idempotent CreateOrUpdateFile — foundation reused for
  any second backend)
- DESIGN-0010 (per-org scope — closes the multi-installation
  question that was bundled in INV-0002)
- Forgejo Go SDK: https://code.forgejo.org/forgejo/go-sdk
- Forgejo API docs: https://docs.codeberg.org/api/
- go-github v68: https://github.com/google/go-github
