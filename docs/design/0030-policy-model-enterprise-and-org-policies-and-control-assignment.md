---
id: DESIGN-0030
title: "Policy model: enterprise and org policies and control assignment"
status: Draft
author: Donald Gifford
created: 2026-10-02
---

<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN-0030: Policy model: enterprise and org policies and control assignment

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [Files](#files)
  - [The control catalogue](#the-control-catalogue)
  - [The enterprise policy](#the-enterprise-policy)
  - [Org policies](#org-policies)
  - [Resolution](#resolution)
  - [Resource ownership](#resource-ownership)
  - [Modes](#modes)
  - [When resolution runs](#when-resolution-runs)
  - [Validation at load](#validation-at-load)
  - [Compliance queries](#compliance-queries)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Decisions](#decisions)
- [Open Questions](#open-questions)
  - [OQ1: Is there a separate repository policy type?](#oq1-is-there-a-separate-repository-policy-type)
  - [OQ2: Can an org policy change a control's parameters (e.g. different owner teams)?](#oq2-can-an-org-policy-change-a-controls-parameters-eg-different-owner-teams)
  - [OQ3: At what granularity is the mode set?](#oq3-at-what-granularity-is-the-mode-set)
<!--toc:end-->

## Overview

A policy is the *who* and the *what*: which orgs and repositories, and which controls apply to them. This document defines:

- the **control catalogue**, where controls are defined once;
- the **enterprise policy**, the baseline for every listed org;
- **org policies**, which add, replace or exclude controls for one org or for specific repositories in it, and set the mode;
- **resolution**, the deterministic function that turns those, together with the repository's stored state and the two Apps' installation status, into a repository's **assignments**: the controls that apply, the policy each came from, and the mode (`evaluate` or `remediate`);
- how assignments are stored and queried for compliance at repository, org, enterprise and policy level.

Vocabulary and the overall architecture are in DESIGN-0029. What a control *is* in code is in DESIGN-0031. How assignments drive evaluation and remediation is in DESIGN-0032.

## Goals and Non-Goals

### Goals

- **One baseline, local variation.** The enterprise policy says what applies everywhere. Org policies are only needed for differences.
- **Trial a control on one org.** An org policy can enable a new control, or a new version of one, without touching the enterprise policy.
- **Exclusions are explicit and explained.** Every exclusion names its scope and a reason, and is visible per repository in the UI.
- **Every assignment has provenance.** For each repository and control, the database records which policy assigned it, which excluded it, and why.
- **Deterministic resolution with no API calls.** The same policy snapshot, the same repository row and the same installation status always give the same assignments. Resolution reads stored state only, so it is table-testable.
- **One owner per resource holds per repository.** Resolution never assigns two active controls that own the same resource — the "one owner per resource" goal of DESIGN-0029.

### Non-Goals

- **A policy language with arbitrary conditions.** Selection is by org and repository name (with globs). Selection by repository facts is a later addition (D4). It is not a general expression language.
- **Per-team or per-topic policies.** These could come later through repository facts.
- **Editing policy through the UI.** Policies are files under version control. The UI is read-only (DESIGN-0027 stance).

## Background

v1 and v2 today use one policy file:

- a top-level `scope { orgs }` that engages strict mode;
- per-rule `scope` and `ignore` blocks;
- global `ignore { repos }` globs.

Every rule must restate its scope, and an exception for one org means editing a shared file. That creates three problems:

- there is no way to trial a rule on one org without scoping tricks;
- nothing records *why* a rule does not apply to a repository;
- "how compliant is org X with the enterprise baseline" is not a question the data can answer.

A fourth problem is structural rather than about scoping: rules are evaluated in isolation, so two rules that name the same file have no shared view of the result and can undo each other. The policy model's answer is resource ownership (below); the control model's answer is in DESIGN-0031.

## Detailed Design

### Files

```text
policy/
  catalogue/
    codeowners.hcl        # control definitions, any number of files
    catalog_info.hcl
    dependency_updates.hcl
  enterprise.hcl          # exactly one
  orgs/
    donaldgifford.hcl     # zero or one per org
    test-org.hcl
```

The directory is mounted the way `GUARDIAN_CONFIG` is today (a ConfigMap from chart values or an existing ConfigMap) (D3). Everything in it is loaded, validated and hashed together into one **policy version**, so every repository is resolved against a consistent snapshot.

### The control catalogue

A control is defined once and referenced by `name@version`, where the version is an integer and the reference matches exactly (DESIGN-0029 D2 and D3):

```hcl
control "codeowners" {
  version = 2
  title   = "CODEOWNERS"
  type    = "codeowners"              # the Go control type (DESIGN-0031)

  rule "exists" {
    number = "1.1"
    title  = "A valid CODEOWNERS file exists in the standard location"
    remediate = true                  # this rule is remediable
  }

  rule "wiz-owners" {
    number  = "1.2"
    title   = ".wiz is owned by security champions and application security"
    kind    = "owners"                # a rule kind the codeowners type provides
    pattern = ".wiz"
    owners  = ["@{{ .Org }}/security_champions", "@{{ .Org }}/application_security"]
    remediate = true
  }

  template = "codeowners"             # full-file template for an absent file

  pr {
    title = "chore: CODEOWNERS ({{ .Control.Title }} v{{ .Control.Version }})"
  }
}

control "repo_settings" {
  version = 1
  title   = "Repository settings"
  type    = "repo_settings"           # an API-remediated type

  rule "no-wiki" {
    number    = "1.1"
    title     = "The wiki is disabled"
    kind      = "setting"
    property  = "has_wiki"
    value     = false
    remediate = true
  }

  remediation {
    apply = "pr"                      # "pr" (default) or "direct" (DESIGN-0032 OQ4)
  }
}
```

Rule ids (`exists`, `wiz-owners`) are bare and unique within their control. Rule **numbers** (`1.1`, `1.2`) are display-only labels for reports and the UI; they are a different axis from the control **version** (`2`), which is what policies pin. A new version of a control is a new catalogue definition under the same name, not a new Go type.

Which rule kinds and parameters a control can use is defined by its control type, and validated at load (DESIGN-0031). The catalogue holds definitions only. A control in the catalogue that no policy references is inert.

### The enterprise policy

```hcl
enterprise {
  orgs = ["donaldgifford", "test-org", "platform"]
  mode = "evaluate"                    # default for everything; see Modes

  controls = [
    "codeowners@1",
    "catalog_info@1",
    "dependency_updates@1",
  ]

  remediation {                        # defaults; an org policy may override
    max_open_prs = 3                   # per repository (DESIGN-0032 OQ2)
    reopen_after = "336h"              # after a user closes a PR (DESIGN-0032 OQ3)
  }
}
```

The enterprise policy has **no exclusions**: it is the baseline. Its org list is also the onboarding gate. An org not listed is not managed, even if an App is installed there or an org policy file exists (D1).

### Org policies

```hcl
org "test-org" {
  mode = "remediate"                   # overrides the enterprise default for this org

  # Additional controls for this org only.
  controls = ["branch_ruleset@1"]

  # Trial a new version of an enterprise control in this org.
  replace "codeowners@1" {
    with = "codeowners@2"
  }

  # Exclude a control for the whole org.
  exclude "dependency_updates@1" {
    reason = "test-org uses an in-house updater"
  }

  # Keep evaluating one control while the rest of the org remediates.
  control "codeowners@2" {
    mode = "evaluate"
  }

  # Exclude repositories from repo-guardian entirely.
  exclude_repos {
    match  = ["sandbox-*", "archive-*"]  # globs on the repository name
    reason = "throwaway repositories"
  }

  # Repository-level variation (OQ1: no separate repository policy type).
  repos {
    match = ["legacy-api", "legacy-web"]

    exclude "catalog_info@1" {
      reason = "being decommissioned, no Backstage entry"
    }

    mode = "evaluate"                  # never remediate these
  }

  repos {
    match    = ["payments-*"]
    controls = ["secret_scanning@1"]   # (hypothetical control)

    control "secret_scanning@1" {
      mode = "remediate"
    }
  }

  remediation {
    max_open_prs = 5
  }
}
```

Block labels are quoted strings; lists live in attributes. `repos` and `exclude_repos` both select repositories with `match`, a list of globs on the repository name.

An org policy can:

- **add** controls (`controls`), org-wide or in a `repos` block;
- **replace** a control with another that owns the same resource, usually a newer version (`replace`), org-wide or in a `repos` block;
- **exclude** a control, org-wide or in a `repos` block, with a required `reason` (D2);
- **exclude repositories** entirely (`exclude_repos`), with a required `reason`;
- **set the mode** org-wide, in a `repos` block, or for one control with a `control "name@N" { mode }` override inside either (OQ3);
- **tune remediation** (`remediation { max_open_prs, reopen_after }`) for the org.

### Resolution

Resolution makes no API calls. It reads the policy snapshot and two pieces of stored state:

```text
resolve(policySnapshot,
        repository{org, name, active, archived, fork},
        installations{org → evaluation App installed?, remediation App installed?})
  → RepositoryPolicyState{state, reason}, []Assignment
```

`installations{org → …}` is read from `installations WHERE account_login = org AND suspended_at IS NULL AND removed_at IS NULL`, one row per App (`app IN ('eval', 'remediate')`, DESIGN-0032); a suspended or removed installation counts as not installed.

The repository's `active` flag is the parking mechanism carried over from today: archived repositories, forks, removed repositories and repositories the Evaluation App cannot read are parked, and discovery (`UpsertDiscovered`) is the only thing that un-parks. A parked repository resolves to no assignments and is never scanned.

```mermaid
flowchart TD
    A["repository row<br/>(org, name, active, archived, fork)"] --> P{"active?"}
    P -- no --> PK["state = parked<br/>(park reason kept, no assignments)"]
    P -- yes --> B{"org in enterprise.orgs?"}
    B -- no --> U["state = unmanaged<br/>(no assignments)"]
    B -- yes --> C{"matched by org exclude_repos?"}
    C -- yes --> X["state = excluded<br/>(reason recorded, no assignments)"]
    C -- no --> L1["layer 1: enterprise<br/>controls, default mode"]
    L1 --> L2["layer 2: org<br/>replace, add, exclude, mode, control overrides"]
    L2 --> L3["layers 3 to n: each matching repos block in file order<br/>replace, add, exclude, mode, control overrides"]
    L3 --> M["mode per control: most specific wins,<br/>then remediation App not installed forces evaluate"]
    M --> I{"two active controls<br/>own one resource?"}
    I -- yes --> ERR["resolution_error on the affected assignments<br/>(caught at policy load where possible)"]
    I -- no --> OUT["state = managed<br/>assignments with provenance"]
```

**Layers.** Resolution walks the layers in order — enterprise, then the org policy, then every `repos` block whose `match` fits the repository, in file order. **Each layer is one step** that applies its `replace`, its additions, its exclusions and its mode settings together, on top of the set the previous layer produced. Within one layer an exclusion beats an addition of the same control, because "exclude" is the explicit opt-out. A later layer may add back a control an earlier layer excluded; that is how "exclude org-wide except these repositories" is written. The assignment's provenance records the layer that made the final decision.

**Precedence**, from weakest to strongest:

| Layer | Can add | Can replace | Can exclude | Can set mode |
| ----- | ------- | ----------- | ----------- | ------------ |
| enterprise | ✓ baseline | — | — | default |
| org | ✓ | ✓ | ✓ | ✓ |
| org `repos` block | ✓ | ✓ | ✓ | ✓ |
| `control "name@N" {}` override, inside the org or a `repos` block | — | — | — | ✓ for that control |

**Mode** resolves per (repository, control), most specific wins: a `control {}` override in a matching `repos` block, then that block's `mode`, then the org's `control {}` override, then the org's `mode`, then the enterprise `mode`. After that, if the Remediation App is not installed for the org, the mode is forced to `evaluate` with `mode_reason = remediation_app_not_installed` (see Modes).

**Worked example** — two overlapping `repos` blocks:

```hcl
org "test-org" {
  exclude "catalog_info@1" {
    reason = "no Backstage in test-org yet"
  }

  repos {
    match    = ["backstage-*"]
    controls = ["catalog_info@1"]      # re-activates it for these repositories
  }

  repos {
    match = ["backstage-legacy"]
    exclude "catalog_info@1" {
      reason = "being decommissioned"
    }
  }
}
```

For `backstage-api`: the enterprise assigns `catalog_info@1`, the org excludes it, the first block adds it back. Final: active, `source = org:test-org/repos[backstage-*]`. For `backstage-legacy`: the same, then the second block excludes it again. Final: excluded, `excluded_by = org:test-org/repos[backstage-legacy]`. Swapping the two blocks leaves `backstage-legacy` active, which is why blocks apply in file order and the validator warns when a later block re-adds what an earlier one excluded for an overlapping match.

**The output, per repository:**

| Field | Example |
| ----- | ------- |
| control | `codeowners@2` |
| state | `active` or `excluded` |
| source | `enterprise`, `org:test-org`, `org:test-org/repos[legacy-*]` |
| replaced | `codeowners@1` (when a `replace` applied) |
| excluded_by, reason | `org:test-org`, "being decommissioned" |
| mode | `evaluate` or `remediate` |
| mode_source | which layer set the mode |
| mode_reason | `NULL` (from policy) or `remediation_app_not_installed` |

Alongside the assignments, resolution writes the repository's effective remediation settings, `max_open_prs` and `reopen_after` (enterprise default, org override), onto its `repository_policy_state` row (D7). DESIGN-0032's `remediation_due` definition and the remediator read them from that row, never from a settings table or a re-parse of policy.

Excluded assignments are stored, not dropped. "Excluded by policy, with reason" is posture information the UI shows, and it is distinct from "not applicable" (a control that ran and decided it does not apply). On an excluded row, `mode` holds the mode that would have applied had the control been active; it is informational.

### Resource ownership

Each control type declares the resources it owns (DESIGN-0031):

| Control type | Owns |
| ------------ | ---- |
| `codeowners` | `file:CODEOWNERS`, covering `.github/CODEOWNERS`, `CODEOWNERS` and `docs/CODEOWNERS` |
| `catalog_info` | `file:catalog-info.yaml` and `.yml` |
| `dependency_updates` | `file:renovate` (every Renovate config location), `file:dependabot` (every Dependabot config location) |
| `file` | `file:<its path>` |
| `repo_settings` | `setting:<property>` for each property it sets |
| `branch_ruleset` | `ruleset:<name>` for each ruleset it manages |
| `labels` | `label:<name>` for each label it manages |
| `custom_properties` | `property:<name>` for each property it manages |

**At load**, the validator resolves the ownership check on the **final active set** of every combination it can enumerate: each org's layer alone, each org plus each of its `repos` blocks, and each org plus each pair of its `repos` blocks (globs can overlap, so two blocks may both match one repository). It rejects any combination where two active controls own the same resource and no `replace` connects them. Adding `codeowners@2` without replacing `codeowners@1` is a load error that names both controls and the org. Three or more overlapping blocks are not enumerated; a collision that only appears there is caught at discovery-time resolution, which records the repository as `managed` with `resolution_error` on the affected assignments rather than assigning either control.

Resolution re-checks at runtime as a backstop. A collision there is a bug, not a policy state.

### Modes

- `evaluate` (default): evaluate and record. A remediation is never started for this assignment.
- `remediate`: evaluate and record. When the control is non-compliant, has a remediable failing rule and the evaluation changed, start remediation (DESIGN-0032).

Mode resolves per (repository, control) through the precedence above. "Remediate the whole org except one new control while we watch it" is therefore a `control "name@N" { mode = "evaluate" }` override inside a `remediate` org (OQ3).

Remediation additionally requires the Remediation App to be installed on the org. Installation status is an input to resolution: where the Remediation App is absent, every assignment that policy would have set to `remediate` resolves to `mode = evaluate` with `mode_reason = remediation_app_not_installed`, persisted on the assignment, which the UI and an alert surface. It is not an error. Installing the App re-resolves the org (see below) and the overrides disappear.

### When resolution runs

| Trigger | Scope |
| ------- | ----- |
| repository discovered, renamed, transferred, parked or un-parked | that repository |
| policy version changes (any file in `policy/`) | every repository: re-resolve, and re-evaluate where assignments changed, spread over the rollout window (assumption A7) |
| either App installed, suspended or unsuspended | that org's repositories |

Resolution is cheap and makes no API calls, so it runs inline in the activity that needs it. Its result is persisted, so the API and the evaluation workflow read assignments rather than recomputing them.

### Validation at load

Load fails, with the file, line and names in the message, on:

- an unknown control name or version;
- a parameter the control type rejects (DESIGN-0031);
- an org policy for an org not in `enterprise.orgs` (D1);
- a `replace` whose two controls do not own the same resource;
- a resource ownership collision (see above);
- an exclusion without a `reason` (D2);
- a `control {}` override naming a control that no layer assigns;
- `remediation.max_open_prs` below 1, or `reopen_after` that is not a duration of at least `1h`;
- a duplicate org policy, or a duplicate control name at one version.

Load warns on:

- an exclusion of a control that is not assigned at that layer, which is a no-op;
- a `repos` block that re-adds a control an earlier block excluded for an overlapping `match`;
- a `repos` block that matches no known repository, which is checked after discovery.

### Compliance queries

Because each assignment records its source, posture can be cut by policy, not only by repository. Status is not stored on the assignment: these queries join `control_assignments` to DESIGN-0032's `control_results` on `(repository_id, control_id)`. Neither key includes the control version, on purpose — a repository has at most one version of a control assigned, and a result row follows the assignment through a version bump.

- **Enterprise baseline compliance for org X:** active assignments with `source = enterprise` (or `replaced` from an enterprise control) in org X, compliant ÷ (compliant + non_compliant).
- **Org-specific compliance:** active assignments with `source = org:X…`.
- **Worst controls in org X:** non-compliant count per control.
- **Exclusions report:** excluded assignments and excluded repositories with their reasons, per org (D5).

The percentage rule (integer floor, computed once in SQL, shared by report, API and snapshots) carries over from IMPL-0025 Phase 8.

## API / Interface Changes

- New policy directory and HCL schema (above); `GUARDIAN_CONFIG` points at the directory (D3).
- `policy.Snapshot`, with `Resolve(repo RepositoryRow, installs InstallationStatus) Resolution` (deterministic, no I/O), `Version() string`, `Control(id control.ID) control.Control` and `Definition(id control.ID) control.Definition`. `Resolution{State, Reason, Assignments, MaxOpenPRs, ReopenAfter}` carries the repository policy state, the assignments and the effective remediation settings (D7). `Assignment` carries `Control, State, Source, Replaced, ExcludedBy, Reason, Mode, ModeSource, ModeReason`, one per `control_assignments` row.
- API: `/policies` (the loaded snapshot, summarised), `/controls` (catalogue), `/repositories/{id}/controls` (assignments with provenance and the latest result), and `/orgs/{org}` gaining baseline-versus-org compliance (DESIGN-0032 lists the full set).

## Data Model

```sql
-- Why a repository has, or has not, assignments; rewritten on resolution.
CREATE TABLE repository_policy_state (
    repository_id   BIGINT PRIMARY KEY REFERENCES repositories(id),
    state           TEXT   NOT NULL CHECK (state IN ('managed', 'excluded', 'unmanaged', 'parked')),
    reason          TEXT,                      -- exclusion reason or park reason
    max_open_prs    INT    NOT NULL,           -- effective remediation settings (D7):
    reopen_after    INTERVAL NOT NULL,         --   enterprise default, org override
    policy_version  TEXT   NOT NULL REFERENCES policy_versions(version),
    resolved_at     TIMESTAMPTZ NOT NULL
);

-- The resolved assignments; rewritten per repository on resolution.
CREATE TABLE control_assignments (
    repository_id   BIGINT NOT NULL REFERENCES repositories(id),
    control_id      TEXT   NOT NULL,           -- 'codeowners'
    control_version INT    NOT NULL,           -- 2
    state           TEXT   NOT NULL CHECK (state IN ('active', 'excluded')),
    source          TEXT   NOT NULL,           -- 'enterprise' | 'org:<org>' | 'org:<org>/repos[<glob>]'
    replaced        TEXT,                      -- 'codeowners@1'
    excluded_by     TEXT,
    reason          TEXT,
    mode            TEXT   NOT NULL CHECK (mode IN ('evaluate', 'remediate')),
    mode_source     TEXT   NOT NULL,
    mode_reason     TEXT,                      -- NULL = from policy; 'remediation_app_not_installed'
    policy_version  TEXT   NOT NULL REFERENCES policy_versions(version),
    resolved_at     TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (repository_id, control_id)
);
```

There is one row per (repository, control name). That is the resource-ownership invariant, restated as a key: only one version of a control can be assigned to a repository. A repository in state `excluded`, `unmanaged` or `parked` has a `repository_policy_state` row and no `control_assignments` rows, so "no assignments" is always explained. Assignment and state changes write a `repository_events` row (`assignment_changed`) so the timeline shows when a control started or stopped applying; the `kind` CHECK on `repository_events` must be extended for it (assumption A4 covers the identity tables, not the event kinds).

Results follow assignments (D6). When resolution flips an assignment to `excluded` or removes it (the control dropped from policy, the org removed from `enterprise.orgs`, the repository excluded or parked), the same transaction deletes that control's `control_results` and `rule_results` rows (DESIGN-0032) and writes a `result_events` row with `rule_id = NULL`, `from_status` set to the last status and `to_status = NULL`, so the timeline shows when posture stopped being tracked. `remediations` rows are kept; an open remediation PR for a control that is no longer assigned is closed by the next remediation run as `closed_withdrawn` (DESIGN-0032). The consequence is that compliance queries, the posture gauges and `/repositories/{id}/controls` never see a result without an active assignment: "no assignment" and "no result" are the same fact.

## Testing Strategy

- **Resolution table tests:** every precedence row, replace, exclude, `exclude_repos`, overlapping `repos` blocks (including the worked example in both block orders), the five mode layers, the `remediation_app_not_installed` override, and each `repository_policy_state` outcome (parked, unmanaged, excluded, managed). Each case asserts the full assignment, including provenance.
- **Load validation tests:** one per error and warning above, asserting the message names the file and the controls.
- **Ownership-collision tests:** the validator enumerates combinations. A fuzz test generates random catalogues and org policies, then asserts that load-valid policies never resolve to a collision for any single `repos` block or pair of blocks, and that a deeper collision resolves to `resolution_error` rather than an assignment.
- **Golden snapshot of the shipped example policies,** resolved against a fixed repository list, so a resolution change shows up as a diff.

## Migration / Rollout Plan

- There is no v1 policy compatibility (DESIGN-0029 D1). The example policies are rewritten as `policy/` directories, so they double as documentation.
- The chart's `policy` values move from one HCL string to a map of file names to contents, rendered into one ConfigMap. `existingConfigMap` keeps working.

## Decisions

Former open questions settled in this document; the body text above states each as fact.

- **D1 Org onboarding** — the enterprise `orgs` list is the only onboarding gate; an org policy for an org not in it is a load error. Onboarding stays one reviewed change in one file, and a stray org file cannot silently bring an org under management.
- **D2 Exclusion reasons** — every `exclude` and `exclude_repos` carries a required, non-empty `reason`, stored with the assignment or the repository state and shown in the UI. An exception without a reason is the one an auditor asks about.
- **D3 Policy source** — the policy directory is a mounted ConfigMap, as `GUARDIAN_CONFIG` is today. No new moving parts; changes go through the same values or Argo review path. A git-sync sidecar can be added later without changing the loader.
- **D4 Fact selectors** — selection is by org and repository name globs only. Facts (visibility, topics, catalog-info `spec.type`) need an evaluation to have run first, which would make resolution two-pass; names cover the stated needs, and a `facts` selector can be added later.
- **D5 Counting exclusions** — excluded controls and excluded, unmanaged or parked repositories are recorded (`control_assignments.state`, `repository_policy_state`) and reported separately, never in the compliance denominator. "92% compliant, 14 exclusions" is honest; folding exclusions into either side of the percentage is not.
- **D6 Results are cleared when an assignment stops being active** — a result row with no active assignment is posture nobody asked for: it would count in compliance, in the gauges and in the repository view until something noticed. Deleting it in the resolution transaction and recording the transition in `result_events` keeps "no assignment" and "no result" the same fact, and the history stays queryable.
- **D7 Remediation settings are persisted per repository** — `max_open_prs` and `reopen_after` are resolved like mode (enterprise default, org override) and written to `repository_policy_state`, so the sweep query and the remediator read one row instead of re-deriving policy; a policy change re-resolves and rewrites them like every other assignment field.

## Open Questions

### OQ1: Is there a separate repository policy type?

- (a) ✅ recommended: **no.** Repository-level variation lives in `repos { match = [...] }` blocks inside the org policy. Every exception for an org is in one file the org's owners review, and resolution has two layers plus blocks rather than three file types. `match` globs also cover "these five repos", which a per-repo file would not.
- (b) Yes: `repo "org/name" {}` files under `policy/repos/`. Repository owners could own their file, but exceptions scatter, and precedence gains a third file type.
- (c) A repository-owned file inside the repository (`.github/repo-guardian.hcl`). Self-service, but a repository could exclude itself from controls, which defeats the point of a policy.
- other:

### OQ2: Can an org policy change a control's parameters (e.g. different owner teams)?

- (a) ✅ recommended: **no parameter overrides; parameters are templated with repository context** (`{{ .Org }}`), and a genuinely different requirement is a different control or version, used through `replace`. Results stay comparable across orgs: "CODEOWNERS 1.2" means the same thing everywhere. The cost is real: an org whose team slugs do not follow the templated convention (`@{{ .Org }}/security_champions`) has `replace` as its only escape, which means a second catalogue entry for that org.
- (b) `override "codeowners@1" { rule "wiz-owners" { owners = [...] } }` in org policies. Flexible, but the same control id then means different things per org, and compliance numbers stop being comparable.
- other:

### OQ3: At what granularity is the mode set?

- (a) ✅ recommended: **enterprise default, org, `repos` block, and a per-control `control "name@N" { mode }` override inside the org or a `repos` block**, resolved most-specific-wins by the precedence table. This enables "remediate everything except the control we are trialling", which is the safe way to roll out a new remediation.
- (b) Org and enterprise only. Simpler, but a new control in a `remediate` org starts writing PRs the moment it is added.
- (c) Per control only, in the catalogue. That is global, so it cannot express "remediate in the test org, evaluate everywhere else".
- other:
