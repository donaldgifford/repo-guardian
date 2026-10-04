---
id: DESIGN-0029
title: "Controls and policies: opinionated compliance with separate evaluation and remediation"
status: Draft
author: Donald Gifford
created: 2026-10-02
---

<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN-0029: Controls and policies: opinionated compliance with separate evaluation and remediation

<!--toc:start-->
- [Overview](#overview)
  - [Document set](#document-set)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
  - [Why the rule model conflicts](#why-the-rule-model-conflicts)
  - [Why now](#why-now)
- [Detailed Design](#detailed-design)
  - [Vocabulary](#vocabulary)
  - [Architecture](#architecture)
  - [Lifecycle of one repository](#lifecycle-of-one-repository)
  - [What happens to v1 concepts](#what-happens-to-v1-concepts)
  - [Assumptions about the current v2 code](#assumptions-about-the-current-v2-code)
  - [Risks](#risks)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Decisions](#decisions)
- [Adversarial Review](#adversarial-review)
  - [AR-0029-01 (high): Access loss must not erase known non-compliance](#ar-0029-01-high-access-loss-must-not-erase-known-non-compliance)
  - [AR-0029-02 (high): The all topology is an exception to the credential boundary](#ar-0029-02-high-the-all-topology-is-an-exception-to-the-credential-boundary)
  - [AR-0029-03 (high): Pre-release status does not make persisted workflows disposable](#ar-0029-03-high-pre-release-status-does-not-make-persisted-workflows-disposable)
  - [AR-0029-04 (high): name@version does not freeze the meaning of a control](#ar-0029-04-high-nameversion-does-not-freeze-the-meaning-of-a-control)
  - [AR-0029-05 (medium): Separate installations do not prove independent capacity](#ar-0029-05-medium-separate-installations-do-not-prove-independent-capacity)
- [Open Questions](#open-questions)
  - [OQ1: When does the controls model land relative to v2.0.0?](#oq1-when-does-the-controls-model-land-relative-to-v200)
  - [OQ2: Where does this work happen?](#oq2-where-does-this-work-happen)
<!--toc:end-->

## Overview

repo-guardian today is a generic rules engine. A rule is a file path, a template and some assertions, and many rules can point at the same file. Rules that share a file delete, overwrite or loop against each other, because the engine cannot know that "CODEOWNERS with a `.wiz` line" is one file with two requirements.

This design set replaces the rule model with an **opinionated controls model**, and splits **evaluation** from **remediation**:

- A **policy** says *who* and *what*: which orgs and repositories, and which controls apply to them.
- A **control** (for example `codeowners@1`, displayed as "CODEOWNERS 1.0") is the expected state of one resource, defined by its **control rules** ("1.1 a CODEOWNERS file exists in a standard location"; a later version may add "1.2 `.wiz` is owned by security champions and appsec", the ownership control deferred to DESIGN-0034). A control is implemented in Go by a package that understands its resource, so a control owns its resource outright.
- **Evaluation** is read-only and idempotent. It always runs against the default branch, and records which controls and control rules pass or fail. It is the default mode, so an enterprise can measure its posture before anything writes to a repository.
- **Remediation** is a separate, opt-in, write-capable workflow. It runs only when an evaluation *changed*, or when a remediation PR was edited or closed. It opens **one PR per control**, containing every fix that control needs.

The two halves run as two GitHub Apps (DESIGN-0032 D1): a read-only **Evaluation App** and a write-capable **Remediation App**. Each is installed per org with its own installation id, so each has its own rate budget (`installation/<app>/<installation id>`), its own Temporal task queue (`repo-guardian-eval`, `repo-guardian-remediate`) and its own worker role (`evaluator`, `remediator`). The two scale independently on what the Apps isolate — the GitHub primary budget, the credentials, the task queue and the worker role — while Temporal, Postgres, egress and GitHub's secondary limits stay shared and are sized explicitly (D10). Repository webhooks go to the Evaluation App only; each App receives its own installation lifecycle events (DESIGN-0030, DESIGN-0032).

The original brief is kept verbatim in `notes/2026-10-02-controls-and-policies-brief.md`.

### Document set

| Doc | Covers | Open questions |
| --- | ------ | -------------- |
| **DESIGN-0029** (this) | Vocabulary, architecture, end-to-end lifecycle, assumptions about v2 code, cross-cutting decisions | 0 (both resolved 2026-10-04) |
| **DESIGN-0030** | Policy model: enterprise and org policies, the control catalogue, resolving which controls apply to a repository, modes | 0 (all resolved 2026-10-04) |
| **DESIGN-0031** | Control framework: the `Control` interface, control rules, results, file controls, the built-in control types | 0 (OQ2 resolved, OQ1 deferred to DESIGN-0034, 2026-10-04) |
| **DESIGN-0032** | Evaluation and remediation workflows: the two GitHub Apps, change detection, per-control PRs and their lifecycle, data model, Temporal mapping | 0 (all resolved 2026-10-04) |
| **DESIGN-0033** | Companion: fwsync as inspiration — concept mapping, the conventions adopted from it, every reuse option evaluated and rejected; no shared code, schema or definition | 0 (all resolved 2026-10-04) |
| **DESIGN-0034** | CODEOWNERS ownership control: `owners` and `default_owner` rules, effective ownership under last-match-wins, the errors endpoint. **Deferred** and not decided; this version's `codeowners` is existence plus template (DESIGN-0031 D11) | 1 (deferred) |

Each doc also carries a **Decisions** section: choices that were weighed and settled, with a one-line rationale, so the open questions are only the ones that need the maintainer.

## Goals and Non-Goals

### Goals

- **One owner per resource.** A control owns its resource (a file, a setting, a ruleset, a label, a property), so two parts of a policy can never fight over it. Every shared-file conflict in the v1 engine becomes impossible by construction rather than being patched case by case.
- **Opinionated control types.** Each resource kind gets a Go package that can find, read, parse, evaluate and fix it. The policy author picks controls and parameters, not paths and regexes.
- **Evaluation is the product; remediation is an add-on.** Posture is measured and stored on every evaluation, whether or not anything is ever fixed.
- **Least privilege by construction, in `topology: split`.** A split evaluator never holds write credentials — neither the Remediation App key nor the remediator database role — and an org in evaluate mode never needs the write App installed. This is the product's security claim and it is proven by the chart and grant tests, not assumed. `all` is a combined-trust topology for single-operator installs: both App keys in one process and a database role that is a member of both writers (D7).
- **Change-driven remediation.** No PR traffic unless something changed: the default branch, the evaluation result, or a human's edit to a remediation PR.
- **Explainable posture.** For every repository the database records which controls apply, which policy assigned each one, every control rule's result and evidence, and the remediation PR's state and age.
- **Compliance at every level.** Repository, org, enterprise, per control, and per policy source, from one consistent set of tables. Every percentage is reported beside its coverage (measured over assigned), so an unevaluated, stale or unmeasurable repository shrinks coverage and never improves the number (D6).

### Non-Goals

- **Compatibility with v1 policies.** The HCL schema is new, and there is no automatic translation (D1); a migration guide maps v1 rules to control types.
- **Keeping the v1 engine alive inside v2.** `internal/checker`'s rule evaluation, PR building, orphan cleanup and reconciler plumbing are removed (A8). Five pieces are lifted into the new packages rather than deleted: durable-skip classification, repository identity capture, the setting, ruleset and label comparators, foreign-PR matching, and the log lines the E4 dashboard locks (INV-0021).
- **Generic file merging.** A file that needs requirement-level checks gets a control type; the generic file control is one path rendered from one template.
- **Non-GitHub providers.** The control interface should not preclude them, but GitLab stays out of scope (INV-0007).

## Background

### Why the rule model conflicts

A v1 rule is `paths + target + template + assertions + scope`. Rules are independent, and nothing ties two rules on the same file together. v1 evaluates each rule in isolation against the files it names, so rules that target the same file have no shared view of the result: one rule's cleanup deleted a CODEOWNERS file another rule still required; two rules could write one file in turn, each sweep undoing the other; a rule's own fix could fail to satisfy it because `paths` (read) and `target` (write) are separate settings that can disagree; a removed rule's change stayed in the open PR because nobody owned the PR's contents; and a "must exist" / "must not exist" pair on the same path looped forever.

Every one of these is the same gap: the engine has no concept of a resource with an owner. The fix is structural — one owner per resource, and every check on a file inside one opinionated control — not another patch to the engine. The catalog-info rule has never had this problem, but only because one rule targets that file and a filled-in `catalog-info.yaml` is the norm.

### Why now

v2 is pre-release (`2.0.0-rc.N`) and already replaces v1's runtime with a Temporal control plane, a findings store, an API and a UI. It still runs v1's rule engine inside its check activity (IMPL-0025 Phase 4, "the parity suite is the swap guarantee"). The controls model is the piece that makes the rest of v2 coherent. It belongs before the v2 data model is frozen by a GA release (OQ1).

## Detailed Design

### Vocabulary

| Term | Meaning |
| ---- | ------- |
| **Enterprise** | The whole fleet repo-guardian manages: every org listed in the enterprise policy. |
| **Policy** | Who and what. The **enterprise policy** lists orgs and the baseline controls. An **org policy** adds, replaces or excludes controls, excludes repositories, and sets the mode for one org and its repositories (DESIGN-0030). There is no repository-level policy type; repository variation lives in an org policy's `repos` blocks (DESIGN-0030 OQ1). |
| **Control catalogue** | The set of control definitions that policies refer to by id and version, for example `codeowners@2` (DESIGN-0030). |
| **Control** | A named, versioned expected state of one resource, for example `codeowners@1`, displayed as "CODEOWNERS 1.0". It is an instance of a control type with parameters and rules. The version is an integer (D3); the identity results bind to is its revision (D9). |
| **Control revision** | A sha256 over a control definition's canonical content: id, version, type name, the type's declared `Semantics` integer, the rules with their parameters, `apply`, the template bytes and the bytes of any other file the definition references. Title, description and PR title and body are display-only and excluded, so a cosmetic edit changes nothing. Stored on assignments and results, included in the fingerprint; a revision change is a policy-version change (D9, DESIGN-0031). |
| **Control type** | The Go implementation behind a control: `codeowners`, `catalog_info`, `dependency_updates`, `file`, `repo_settings`, … (DESIGN-0031). One Go type serves every version of a control. |
| **Control rule** | One verifiable requirement inside a control, for example `Repository settings 1.1: the wiki is disabled`. Its id is bare and unique within the control (`no-wiki`), qualified as `repo_settings@1/no-wiki` only in logs and the UI (D2). |
| **Rule kind** | The typed check a control rule performs, implemented by the control type: `exists`, `owners`, `field_set`, … A rule names its kind and parameters (DESIGN-0031). |
| **Rule number** | The display numbering of a rule inside its control (`1.1`, `1.2`). Display only, and a separate axis from the control version. |
| **Remediable** | A control rule with `remediate = true`: its control type can produce a fix for it. A rule without it only reports. |
| **Resource** | What a control owns: one file (in all its locations), one setting, one ruleset, one label, one custom property. |
| **Assignment** | The resolved pairing of one repository with one control: the control version and revision, the requested and effective mode, the policy layer that decided it, and whether it is active, excluded or in conflict (DESIGN-0030). An assignment is desired state, derived from the policy snapshot and the repository's identity alone; parking never removes it (D6). |
| **Evaluation** | Running every assigned control's rules against a repository's default branch, read-only. |
| **Rule result** | `pass`, `fail`, `error` or `not_applicable`, with evidence. |
| **Control status** | Derived from its rule results: `compliant`, `non_compliant`, `unknown` or `not_applicable` (DESIGN-0031). |
| **Mode** | `evaluate` (the default) or `remediate`, resolved per repository and control from policy. A repository the Remediation App cannot reach — not in its installation's selection, or the installation suspended — resolves to `evaluate` whatever its policy says; the requested mode, the effective mode and the reason are all stored (DESIGN-0030). |
| **Remediation** | Producing and proposing the change that makes a non-compliant control compliant, as a PR, or as a direct API change where a control allows it (DESIGN-0032). |
| **Fingerprint** | A hash of the control revision, an evaluation's rule results and the digests of the resources it read. An evaluation *changed* iff its fingerprint changed (DESIGN-0032). |
| **Eval generation** | A per-control counter bumped whenever the fingerprint changes; the first evaluation is generation 1. Remediation compares it with the generation it last acted on (DESIGN-0032). |

### Architecture

```mermaid
flowchart LR
    subgraph GitHub
        GH[("Repositories")]
        EA[["Evaluation App<br/>read-only"]]
        RA[["Remediation App<br/>read and write"]]
    end

    subgraph Policy
        CAT["Control catalogue"]
        ENT["Enterprise policy"]
        ORG["Org policies"]
    end

    subgraph Temporal["Temporal control plane"]
        DISC["Discovery"]
        RES["Resolve assignments<br/>policy + repository identity<br/>mode from per-App repository access"]
        EVAL["Evaluation workflow per repository<br/>evaluator role, queue repo-guardian-eval"]
        REM["Remediation workflow per repository and control<br/>remediator role, queue repo-guardian-remediate"]
        BE["rate budget<br/>installation/eval/{id}"]
        BR["rate budget<br/>installation/remediate/{id}"]
    end

    DB[("Postgres<br/>assignments · results ·<br/>generations · remediations")]
    API["API + UI"]

    GH -- "repository webhooks and its own installation events" --> EA
    GH -- "its own installation events" --> RA
    EA --> DISC
    RA -- "lists repositories only" --> DISC
    EA --> EVAL
    CAT --> RES
    ENT --> RES
    ORG --> RES
    DISC --> RES --> DB
    EVAL --> BE
    EVAL -- "read via Evaluation App" --> GH
    EVAL --> DB
    EVAL -- "changed and mode = remediate" --> REM
    REM --> BR
    REM -- "write via Remediation App" --> GH
    REM --> DB
    DB --> API
```

The key properties:

- **Two Apps, two of what the Apps can isolate.** Each App is installed per org with its own installation id, so each has its own `InstallationWorkflow` and rate budget (`installation/<app>/<installation id>`), its own credentials, its own task queue and its own worker role. Evaluation load never competes with remediation load for the GitHub primary budget or for worker slots (DESIGN-0032 D1). What the two share is named here so nobody reads more into the split than it gives: Temporal, Postgres, egress and GitHub's secondary limits. Those are sized by explicit knobs in DESIGN-0032 — per-role activity concurrency (`EVALUATOR_CONCURRENCY`, `REMEDIATOR_CONCURRENCY`), rollout fan-out through `POLICY_ROLLOUT_WINDOW`, batch sizes for the sweep and for lifecycle maintenance, and per-role pool sizes — and secondary-limit throttles defer under IMPL-0022's one-mechanism rule rather than through a new gate. Capacity targets are operational SLOs set per deployment and watched as metrics, not numbers this design commits to (D10).
- **In `topology: split`, evaluation never holds the write App's key.** The remediator workers are the only processes with write credentials, which extends v2's role-based secret scoping (IMPL-0025 Phase 17). The guarantee is stated per topology: `all` runs both halves in one process with both keys and a database role that is a member of both writers, and is documented as combined trust. A deployment reports which boundary it has through the `topology` label on `installation_info` and on the status page (D7).
- **Resolution reads stored state only.** Which controls apply to a repository is a function of the policy snapshot and the repository's identity alone; which mode is effective adds each App's per-repository access (`evaluable`, `remediable`); none of it makes an API call. Assignments are desired state and survive parking — parking decides whether a repository is evaluated, not what is expected of it — and discovery stays the only un-parker, so an archived repository is never scanned (DESIGN-0030). v2's nil-vs-empty rule is kept, not reversed: a park for `archived`, `fork` or `removed` is definitive non-applicability and clears the repository's results; a park for `access_denied` or an installation suspension learned nothing new, keeps the last results, and reports the repository `unmeasurable{reason=access_denied|suspended}`. The only other way a result disappears is an operator's decision — the policy withdraws or excludes the control. Access loss can therefore never improve a posture number (D6).
- **The database is the meeting point.** Evaluation writes facts, remediation reads them and records what it did, and the API reads both.
- **Remediation is triggered by change, not by schedule.** An evaluation that changed signals the remediation workflow. A periodic sweep exists only as a backstop (DESIGN-0032).

### Lifecycle of one repository

```mermaid
sequenceDiagram
    autonumber
    participant D as Discovery
    participant E as Evaluation
    participant DB as Postgres
    participant R as Remediation
    participant GH as GitHub

    D->>DB: repository discovered and active, controls resolved from policy and repository identity, mode from per-App repository access
    D->>E: start evaluation
    E->>GH: read default branch (Evaluation App)
    E->>DB: CODEOWNERS 1.0 non_compliant (1.1 fail), first evaluation inserts generation 1
    E->>R: changed, mode = remediate
    R->>GH: branch repo-guardian/codeowners, full template, open PR (Remediation App)
    R->>DB: PR #12 open, remediated generation 1
    Note over E: next scheduled evaluation
    E->>GH: read default branch
    E->>DB: same fingerprint, evaluated_at only, generation stays 1
    Note over R: not signalled, nothing changed
    GH-->>E: PR #12 merged (webhook to the Evaluation App)
    E->>GH: read default branch
    E->>DB: CODEOWNERS 1.0 compliant, generation 2, PR #12 merged
```

The other PR paths (a human edits the PR, closes it, or the default branch becomes compliant on its own) are specified in DESIGN-0032.

### What happens to v1 concepts

| v1 concept | Controls model |
| ---------- | -------------- |
| `rule "file"` with `exists` / `exact` | the generic `file` control type: one path, one template |
| `rule "file"` with `contains` + assertions | a dedicated control type (`codeowners`, `catalog_info`, `dependency_updates`, …), whose rules are typed checks |
| `check = "absent"` + `when` gates | absorbed into the control type that owns the opinion; for example `dependency_updates` owns both Renovate and Dependabot files |
| `rule "setting"` | the `repo_settings` control type, remediated through the API |
| `rule "branch_protection"` and the `branch_protection` reconciler | one `branch_ruleset` control type |
| `custom_properties` reconciler | a `custom_properties` control type reading catalog-info (DESIGN-0031 D4) |
| `label_sync` reconciler | a `labels` control type |
| `workflow_sync` reconciler | absorbed: its only job was feeding the push-watched path set. Every control declares what it writes (`Owns()`) and what invalidates it (`Reads()`), and a push re-evaluates only the controls whose owned or read resources it touched (DESIGN-0031, DESIGN-0032). No control type is needed. |
| global and per-rule `scope` / `ignore` | the enterprise org list, plus org-policy `exclude` and `exclude_repos` (DESIGN-0030) |
| `dry_run` | evaluate mode |
| single PR branch `repo-guardian/add-missing-files` | one branch and PR per control, `repo-guardian/<control slug>` (DESIGN-0032 D4) |
| `rule_state` / findings per rule | control and control-rule results per repository |

### Assumptions about the current v2 code

These shaped the design and were unverified when written. INV-0021 (2026-10-04) checked each one against the `v2` branch and records, per assumption, the code evidence and whether the code is kept, adapted or removed: ten hold, ten hold with a caveat, three (A21–A23) are facts about GitHub rather than about this code and are not verifiable from it, and none fails. The verdict column summarises INV-0021; the caveats change the implementation plan, not the model.

| # | Assumption | If wrong | INV-0021 verdict |
| - | ---------- | -------- | ---------------- |
| A1 | The `RepoWorkflow` pattern (one long-running workflow per repository, timer plus `recheck` / `policy_changed` / `park` signals, coalescing, ContinueAsNew) can host the evaluation loop with a new activity. | Evaluation needs a new workflow type; the signal and coalescing design is copied rather than reused. | holds with caveat: the loop also acquires budget, defers and parks; the new activity inherits those steps |
| A2 | `InstallationWorkflow` and its budget state are keyed by installation id alone (`installation/<id>`). The design adds an app dimension: the workflow id becomes `installation/<app>/<installation id>` and the state records which App it meters, so each App's installations get their own budget. | The key already carries an app dimension and the change is smaller. | holds |
| A3 | `DiscoveryWorkflow`, `UpsertDiscovered` (the only un-parker) and the subset invariant carry over unchanged, and policy resolution can run as a step after upsert. v2's nil-vs-empty parking rule is kept, not reversed: `archived`, `fork` and `removed` parks clear results (definitive non-applicability), `access_denied` and `suspended` parks keep them and mark the repository unmeasurable; parking never removes an assignment (D6). | Discovery needs reshaping; resolution may need its own workflow. | holds with caveat: `UpsertRepositories` both upserts and starts the workflow, so resolution slots between them; no `suspended` park exists today (INV-0021 OQ1) |
| A4 | The `repositories` table and its identity matching (provider id first, rename and transfer events) are reusable as-is. | The identity work of IMPL-0025 Phase 5 is redone. | holds with caveat: the fate of `repositories.installation_id` under `app_repository_access` is open (INV-0021 OQ2) |
| A5 | `findings`, `finding_events` and `compliance_snapshots` are keyed by `(rule_kind, rule_name)`, with a `rule_kind` CHECK of `file`/`setting`/`branch_protection`. They are replaced by control and control-rule tables, not extended. | A migration path that keeps findings rows is needed. | holds with caveat: the `rule_kind` CHECK is on `findings` only |
| A6 | The `checks` table with its idempotent `check_key` and staging contract can become the evaluation log. | A new log table is needed (cheap). | holds |
| A7 | `policy_versions`, `VersionV2` hashing and `PolicyRolloutWorkflow` (spread re-checks over a window) can be pointed at the catalogue plus policies. The hash covers every control revision (D9), so a changed rule parameter, template or evaluator `Semantics` under an unchanged `name@version` is a policy-version change that re-evaluates and re-remediates even an acknowledged generation. | Rollout is rebuilt; the window idea is kept. | holds with caveat: `versionInput` is wholly v1-shaped and is rewritten, and the rollout today signals without re-resolving |
| A8 | `internal/checker`'s rule evaluation, PR assembly, drift and orphan handling, reconcile log and the parity suite are removed, not adapted. The parity suite goes per scenario, and only once the IMPL names the replacement test that covers that scenario (D8). | Some engine code survives and constrains the control interface. | holds with caveat: five checker pieces are lifted, not deleted (durable-skip classification, identity capture, the setting/ruleset/label comparators, foreign-PR matching, the E4-locked log lines) |
| A9 | `internal/policy`'s HCL loader is replaced. Small pieces survive: hclparse usage, glob matching, PR template compilation, strict decode. | More of the loader is reusable than expected (good). | holds |
| A10 | `internal/template` (renderer, helpers, `ValidateZero`) is reused for control templates and PR text. | Control types need their own rendering. | holds |
| A11 | `internal/catalog` (the Backstage parser) is the core of the `catalog_info` control type. | The parser is rewritten. | holds with caveat: the parser projects to `Properties` and discards the entity, which the control type needs |
| A12 | The GitHub client layer survives: the transport chain (otelhttp → rate limit → ghinstallation), `AsThrottled`, `WithUsage` and deferral. The consumer-side `control.Reader`, `control.PRObserver` and `control.Writer` are implemented by `internal/github`, as a split of today's `Client` plus the new methods the control types need. | The split needs a wrapper rather than an interface change. | holds with caveat: `Reader`/`PRObserver`/`Writer` need methods the client lacks today (ref-pinned reads, tree listing, CODEOWNERS errors, git-data commit) |
| A13 | The `ingest` role and `WebhookWorkflow` survive; the event table gains `pull_request` events for remediation branches. | Ingest is reshaped. | holds with caveat: ingest holds one webhook secret and filters pushes on watched paths; both are reshaped |
| A14 | The API's structure (OpenAPI-first, `apitest`, the scope predicate on every query, the read-only pool) survives with new resources. The UI keeps its shell, auth and status page, with new views. | API and UI work is larger. | holds |
| A15 | Roles and chart secret scoping (`roleHasAppKey` and friends) extend to an evaluator / remediator split. | Chart roles are redesigned. | holds with caveat: values, env and config model one App, one queue and one DSN |
| A16 | v2 has no production Temporal histories that must replay, and replay compatibility with the Phase 10–13 histories is not attempted: the controls workflows get new workflow type names (`EvaluationWorkflow`, `RemediationWorkflow`, `ControlsDiscoveryWorkflow`) and new workflow IDs, so no old history can replay into new code and no `GetVersion` gate is needed (the one existing gate, `budget-v1` in `RepoWorkflow`, guards histories that only the homelab holds). Live rc executions are terminated by the cutover runbook, not drained (D8). | Version gates or a migration workflow are needed. | holds with caveat: the code half holds (one gate, replay suite fails on an empty directory, `AutoUpgrade`); the operational half is the runbook (INV-0021 OQ3) |
| A17 | v1 and v2 currently adopt each other's PRs through frozen identity (`TestPRIdentity_IsFrozen`). Per-control branches end that, so cutover closes v1's PRs instead, and that test's literals are retired deliberately at cutover (DESIGN-0032 D4). | A transitional adopter is needed. | holds |
| A18 | `TaskPriority` (priority plus fairness by installation) applies unchanged to evaluation and remediation activities. | Priorities need rework. | holds |
| A19 | The posture export and dashboards (IMPL-0023) can be regenerated from control results with the same leader-only, `max by` patterns. | Monitoring generation is rewritten. | holds with caveat: no v2 role exports posture today, so the exporter over control results is new work |
| A20 | `internal/shadow` (verify-shadow) and the v1 backfill are not carried into the controls model; cutover starts from a fresh evaluation. | A v1 → controls mapping is needed. | holds |
| A21 | GitHub reads CODEOWNERS from `.github/`, then the repository root, then `docs/`, and uses the first it finds. | The `codeowners` type's location precedence changes; nothing else does. | not verifiable from code (GitHub fact, homelab probe described in INV-0021) |
| A22 | GitHub's REST endpoint listing CODEOWNERS errors is readable with the Evaluation App's read permissions. | The `valid` rule, now DESIGN-0034's scope with the rest of the ownership control, is `unknown{reason=permission}` for that installation; it never passes on parser evidence alone (DESIGN-0031 AR-0031-05). | not verifiable from code (GitHub fact, homelab probe described in INV-0021) |
| A23 | The Evaluation App's read-only permission set (Contents, Metadata, Administration:read, Custom properties:read, Pull requests:read, Issues:read) covers every read the built-in control types need, including rulesets, settings and labels. | The Evaluation App needs more permissions, or some control types move reads to the Remediation App. | not verifiable from code (GitHub fact, homelab probe described in INV-0021); repository-level and organization-level Custom properties reads are separate permissions |

### Risks

- **Code volume.** Removal is large (the rule engine, reconcilers, parity suite). Each control type also adds a package with a parser, rules, templates and tests. This is a trade of generic code for specific code, and the net size is unknown until the follow-up investigation.
- **PR volume.** One PR per control means onboarding a repository can open several PRs at once (DESIGN-0032 OQ2).
- **Two Apps to operate.** Two installations per org and two keys, though one repository-webhook configuration (repository events reach the Evaluation App only; each App delivers its own installation lifecycle events to the same ingest endpoint). Accepted for least privilege, separate budgets and independent scaling (DESIGN-0032 D1).
- **Schema churn during the rc line.** rc users (the homelab) re-evaluate from scratch, and the cutover is a reset — terminate the old executions, restore-and-redeploy to roll back — rather than a replay-compatible upgrade (D8).
- **`all` is combined trust.** The credential boundary holds only in `topology: split`. An `all` deployment has both App keys in one process and a database role that is a member of both writers; an evaluator-side compromise there reaches write credentials. Accepted for single-operator installs, made visible through `installation_info{topology}` and the status page (D7).
- **Shared capacity.** The two Apps do not isolate Temporal, Postgres, egress or GitHub's secondary limits. A fleet-wide rollout together with a remediation backlog can saturate them; the knobs in DESIGN-0032 bound the work, and the SLOs (evaluation freshness p95, cleanup latency, Postgres pool wait) are per deployment, not promised here (D10).
- **"Signal-only" is a property of the api role's code, not of Temporal.** Self-hosted Temporal authorizes per namespace, so the server cannot restrict the api role to `SignalWorkflow`. The api role builds a client that reaches only that call, under a dedicated mTLS identity, and checks repository visibility through `APIScope` before signalling (DESIGN-0032 AR-0032-12). A compromise of that process could issue any call its namespace identity allows.

## API / Interface Changes

Summarised here; specified in the per-area docs.

- **Policy files:** new HCL for the catalogue, the enterprise policy and org policies (DESIGN-0030).
- **Go:** `control.Control` and the control-type registry; the consumer-side `control.Reader`, `control.PRObserver` and `control.Writer` interfaces, implemented by `internal/github` so the package graph has no cycle (DESIGN-0031 D7, AR-0031-08).
- **Roles:** `evaluator` and `remediator` worker roles, each on its own task queue (`repo-guardian-eval`, `repo-guardian-remediate`) and holding only its own App's key (DESIGN-0032 D2).
- **Env:** `EVAL_INTERVAL` replaces `CHECK_INTERVAL`. Both Apps' credentials are mounted as files, scoped per role. Per-role capacity knobs — `EVALUATOR_CONCURRENCY`, `REMEDIATOR_CONCURRENCY`, per-role pool sizes, and batch sizes for the sweep and for lifecycle maintenance — are specified in DESIGN-0032 (D10).
- **API:** new resources for controls, policies, `/repositories/{id}/controls` (assignments with provenance and the latest result) and remediations, replacing `/rules` and `/findings`; one write, `POST /repositories/{id}/evaluate`, backed by a signal-only Temporal client in the api role (DESIGN-0032 D9). "Signal-only" is enforced by the api role's code and a dedicated mTLS identity, not by Temporal (Risks).
- **Metrics and status:** `installation_info` gains a `topology` label (`split` or `all`) and the status page shows the value, so an operator can see which credential boundary a deployment actually has (D7).
- **Chart:** credentials for both Apps, scoped per role (in `topology: split` the evaluator mounts neither the Remediation App key nor the remediator DSN, asserted by helm-unittest); `EVAL_INTERVAL` in place of `CHECK_INTERVAL`; the mode per org comes from policy rather than chart values.

## Data Model

Owned by the per-area docs:

- assignments and per-repository policy state (managed, excluded, unmanaged, parked): DESIGN-0030;
- results, events, generations, remediations: DESIGN-0032.

Cross-cutting shape, decided here and stored there:

- an assignment is desired state keyed by `(repository_id, control_id)` and survives parking; results are cleared only on policy withdrawal or exclusion and on `archived`, `fork` and `removed` parks, and are kept on `access_denied` and `suspended` parks, where the repository reads `unmeasurable` (D6);
- `control_assignments.revision` and `control_results.revision` store the control revision, so a historical result names the definition that produced it (D9);
- posture queries start from active assignments with a LEFT JOIN to results at the matching epoch and revision, so coverage is measured over assigned, and an empty denominator is NULL ("no data"), never 100% (D6, DESIGN-0030).

`repositories` stays. Installation facts are per App and per repository rather than per org: `app_installations` (one row per App and account, with suspension and repository selection) and `app_repository_access` (which App can reach which repository), each refreshed by that App's own discovery and lifecycle events (DESIGN-0030 AR-0030-04).

## Testing Strategy

- **Control types** are tested in isolation against repository state fixtures. Three properties are required for every control type (DESIGN-0031):
  - remediating then re-evaluating passes every remediable rule;
  - remediating twice changes nothing;
  - the default template passes every rule with `remediate = true`.

  With one owner per resource there is no rule-overlap matrix to test; two controls claiming one resource is caught by the policy validator at load where it can be, and failed closed per repository by resolution at runtime, where the conflicted assignment is persisted and visible rather than silently dropped (DESIGN-0030 AR-0030-01).
- **Control revision:** a golden digest test per built-in type pins its revision, so an accidental semantic change fails CI. Changing a rule parameter, the template bytes and the evaluator's `Semantics` separately, each under an unchanged `name@version`, must invalidate stale posture and remediation; a title-only or description-only edit must change nothing (D9).
- **Policy resolution** is table-tested (DESIGN-0030).
- **Access loss:** start with a failing repository, revoke the Evaluation App's access, rediscover the repository and restore access. At no point may the displayed posture improve, and its last-known failures are never discarded; the repository reads `unmeasurable` while unreadable (D6).
- **Topology:** helm-unittest asserts, by env and secret name, that a `topology: split` evaluator pod carries neither the Remediation App key nor the remediator DSN, extending `secret_scoping_test.yaml`; the database grant tests run as the evaluator and remediator roles through `pgtest.AppRole`, never as the owner, because an owner cannot fail on a missing grant (D7, DESIGN-0032 AR-0032-09).
- **Workflows** are tested in the time-skipping environment, with replay histories captured for the new workflow types (DESIGN-0032).
- **Cutover:** cut over with a check in flight, an open PR with human edits, and a GitHub write whose database record failed. The check re-runs after the new roles start (idempotent on its check key), the human-edited PR is adopted under DESIGN-0032 AR-0032-02's rule rather than recreated, and the unrecorded write is found by exact identity. Rollback is restore-and-redeploy, not a live roll back. No duplicate writer, lost intent or recreated v1 PR (D8).
- **End to end:** the e2e stack (Postgres, API, BFF) gains seeded control results. A Temporal plus GitHub-fake integration test drives onboarding → PR → merge → compliant.
- **Load:** fleet onboarding and a policy rollout together with exhausted GitHub budgets and a remediation backlog is an IMPL soak task, measured against the operational metrics (evaluation freshness p95, cleanup latency, Postgres pool wait) rather than against design-time numbers (D10).

## Migration / Rollout Plan

1. **Accept DESIGN-0029 to DESIGN-0032**, then run the follow-up investigation on assumptions A1–A23.
2. **IMPL plan**, roughly in this order:
   - the control interface and the `codeowners` type;
   - policy resolution and assignments;
   - the evaluation workflow, the results tables and API reads;
   - the UI views;
   - the remediation workflow and the second App;
   - the remaining control types;
   - removing the rule engine, retiring the parity suite per scenario as the IMPL names each scenario's replacement test (D8).
3. **Evaluate-only first.** The first rc with controls ships evaluation alone. Remediation follows, so posture numbers can be compared against v1 before anything writes.
4. **v1** keeps its rule engine, with bug fixes only, until cutover. Cutover is a reset, not a replay (D8), and follows the ordered runbook in DESIGN-0032's Migration / Rollout Plan: (1) scale the v2 `worker` role to zero and confirm no v1 pods run; (2) terminate the `repo/*`, `installation/*` and `policy-rollout/*` executions and delete the `discovery` and `snapshot` schedules; (3) reconcile external writes by listing every open repo-guardian PR from GitHub and recording it before anything closes it; (4) run migrations 00004–00007; (5) deploy the new roles and bootstrap; (6) only then close v1's PRs with the pointer comment (A17). Rollback is a Postgres restore plus the previous chart. A check in flight at step 1 is re-run after step 5, since checks are idempotent on their check key; a GitHub write whose record failed is found at step 3 by exact identity rather than recreated. Every current install is an rc with no GA users, so this reset is the only supported rc path.

## Decisions

- **D1 No v1 policy translation** — there is no automatic translation; a migration guide maps v1 rules to control types. The models differ in kind (paths and regexes versus typed rules), so a mechanical translation would produce generic `file` controls and miss the point, and the known fleet policies are small.
- **D2 Identifiers** — a control's id is a stable slug (`codeowners`); rule ids are bare and unique within the control (`exists`, `no-wiki`), stored under `(repository_id, control_id, rule_id)` and qualified as `codeowners@1/exists` only in logs and the UI; rule numbers (`1.1`, `1.2`) are display only. Slugs survive renumbering and keep database keys and URLs stable; the control is the namespace, so rule ids stay short in HCL. Control and rule ids follow one grammar, `^[a-z][a-z0-9]*([_-][a-z0-9]+)*$` (DESIGN-0033 D8).
- **D3 Versions** — a control's version is an integer in the catalogue (`version = 2`), referenced exactly as `codeowners@2`. A bump is catalogue data served by the same Go type, and an org trials `@2` through `replace` (DESIGN-0030). The version is the reference humans and policies use; it does not by itself identify behaviour — the control revision (D9) does, and results record both. "Did the rules change" needs no semantic-version semantics (amended 2026-10-04, AR-0029-04).
- **D4 Timing** — the controls model lands before v2.0.0 on the rc line, replacing the rule engine (OQ1, resolved 2026-10-04). v2 is pre-release and its data model is not frozen; shipping v2.0.0 with rule-keyed findings would mean migrating a GA schema later.
- **D5 Branch** — the work happens on `v2`, phase by phase like IMPL-0025; `main` receives bug fixes only and the `internal/checker` divergence between the branches is accepted (OQ2, resolved 2026-10-04).
- **D6 Parking keeps results** — assignments are desired state, derived from the policy snapshot and the repository's identity, and parking never removes them. Results are cleared in exactly two cases: a policy withdrawal or exclusion, which is an operator's decision, and a park for `archived`, `fork` or `removed`, which is definitive non-applicability. An `access_denied` or `suspended` park learned nothing new, so it keeps the last results and reports the repository `unmeasurable{reason=…}`; coverage, measured over assigned, is reported beside every percentage, and an empty denominator is "no data", never 100%. This is v2's nil-vs-empty rule kept, not reversed: access loss can never improve a posture number (AR-0029-01; storage in DESIGN-0030 D6, outstanding-PR lifecycle in DESIGN-0032).
- **D7 Least privilege is a property of `topology: split`** — the credential boundary (a split evaluator holds neither the Remediation App key nor the remediator DSN) is the product's security claim and holds only in the split topology. `all` is a combined-trust topology for single-operator installs, with both App keys in one process and a database role that is a member of both writers. A deployment reports which boundary it has through `installation_info{topology}` and the status page, and the chart and grant tests prove the split — helm-unittest by env and secret name, grants as the two roles rather than the owner — instead of assuming it (AR-0029-02).
- **D8 Cutover is a reset, not a replay** — the controls workflows get new workflow type names (`EvaluationWorkflow`, `RemediationWorkflow`, `ControlsDiscoveryWorkflow`) and new workflow IDs, so no Phase 10–13 history replays into new code and no `GetVersion` gate is needed. Cutover follows DESIGN-0032's ordered runbook: stop the writers, terminate the old executions and schedules, record every open repo-guardian PR, migrate, deploy and bootstrap, then close v1's PRs. Rollback is a Postgres restore plus the previous chart. The parity suite is retired per scenario, only once the IMPL names that scenario's replacement test. Every current install is an rc with no GA users, so this is the only supported rc path (AR-0029-03).
- **D9 Control revision** — each control definition has a `revision`: a sha256 over its canonical definition, which is the id, version, type name, the type's declared `Semantics` integer, the rules with their parameters, `apply`, the template bytes and the bytes of any other file the definition references. Title, description and PR title and body are display-only and excluded, so a cosmetic edit changes nothing. Assignments and results store the revision, the fingerprint includes it, and a revision change is a policy-version change that re-evaluates and re-remediates even an acknowledged generation. A Go evaluator whose meaning changes bumps its `Semantics` constant, and a golden digest test per built-in type pins the value. `name@version` stays the reference humans and policies use; the revision is the identity results bind to (AR-0029-04; definition in DESIGN-0031, storage in DESIGN-0030).
- **D10 What the two Apps isolate** — the GitHub primary budget, the App credentials, the task queue and the worker role, and nothing more. Temporal, Postgres, egress and GitHub's secondary limits are shared, and are bounded by explicit knobs in DESIGN-0032: `EVALUATOR_CONCURRENCY`, `REMEDIATOR_CONCURRENCY`, rollout fan-out through `POLICY_ROLLOUT_WINDOW`, batch sizes for the sweep and for lifecycle maintenance, and per-role pool sizes. Secondary-limit throttles already defer under IMPL-0022's one-mechanism rule, so no new gate is added. Capacity targets are operational SLOs recorded as metrics (evaluation freshness p95, cleanup latency, Postgres pool wait) and set per deployment, not numbers this design commits to (AR-0029-05).

## Adversarial Review

Reviewed 2026-10-03 against DESIGN-0029–0033 as one design set. **Disposition: changes required before implementation of the affected contracts.** Findings below are unresolved review findings, not accepted decisions. `critical` denotes a counterexample to a core safety guarantee, `high` a correctness or operational blocker, and `medium` a contract gap that needs an explicit decision. Each finding identifies the challenged section, a failure scenario, a proposed correction and a verification case.

**Responses (2026-10-03):** 4 accepted, 1 accepted with changes. Each finding below carries a **Response** giving the disposition and the concrete change. The accepted changes were applied to the body and the Decisions ledger on 2026-10-04 (D3 amended, D6–D10 added); the body and the Decisions ledger are now authoritative, and each finding's **Applied** line names where its change landed.

### AR-0029-01 (high): Access loss must not erase known non-compliance

**Basis:** Architecture's parking behavior, A3, and DESIGN-0030 D6. An Evaluation App 403 parks a repository, removes every assignment and deletes every current result. A repository with known failures therefore disappears from measured posture precisely when it becomes unreadable. This reverses v2's access-denied contract: preserve findings when nothing new was learned; clear them only for definitive non-applicability such as archived/fork filtering.

**Proposed correction:** Distinguish desired assignments, eligibility to evaluate, and measurement availability. Keep last-known results for access-denied/suspended installations, mark them stale or unmeasurable, and expose coverage beside compliance. Reserve result removal for intentional withdrawal or confirmed non-applicability.

**Verification:** Start with a failing repository, revoke access, rediscover it and restore access. At no point may access loss improve the displayed posture or silently discard its last-known failures.

**Response:** **Accepted.** The access-denied contract v1 and v2 already hold stands: a repository that becomes unreadable keeps its last results. Assignments become pure desired state, derived from the policy snapshot and the repository's identity, and parking never removes them; the "Resolution reads stored state only" property in Architecture and assumption A3 are rewritten to say that v2's nil-vs-empty rule is kept, not reversed. Results are cleared in exactly two cases: a policy withdrawal or exclusion, which is an operator's decision, and parking for `archived`, `fork` or `removed`, which is definitive non-applicability (v1's empty-result rule). On access loss or installation suspension the results stay and the repository is reported as `unmeasurable{reason=access_denied|suspended}`. Compliance queries start from active assignments and report coverage, measured over assigned, beside the percentage. The storage model lands in DESIGN-0030, whose D6 is amended (AR-0030-05); the lifecycle rule for outstanding PRs lands in DESIGN-0032 (AR-0032-04). Verification: adopted.

**Applied 2026-10-04:** Architecture ("Resolution reads stored state only"), assumption A3, Vocabulary (Assignment, Mode), Goals ("Compliance at every level"), Data Model (cross-cutting shape) and Testing Strategy ("Access loss"); recorded as D6.

### AR-0029-02 (high): The `all` topology is an exception to the credential boundary

**Basis:** Goals' least-privilege guarantee and DESIGN-0032 Roles/D2/D8. `all` runs evaluation and remediation in one process with both private keys and a database role belonging to both writers. An evaluator dependency or compromise in that process can reach write credentials even if the `Evaluate` method accepts only a reader. Interface separation does not enforce the process-level guarantee stated here.

**Proposed correction:** State the guarantee per deployment topology. Either require split roles for the credential-isolated product or explicitly document `all` as a weaker combined-trust topology. Extend secret-scoping and database-grant checks to prove the split topology, rather than treating `all` as equivalent.

**Verification:** Render and boot both topologies. A split evaluator must lack the remediation key and remediation database capabilities; documentation and UI/operator configuration must identify the combined topology's different boundary.

**Response:** **Accepted.** The least-privilege goal is restated per topology. It holds for `topology: split`, which is the product's security claim; `all` is documented as a combined-trust topology for single-operator installs, with both App keys in one process and a database role that is a member of both writers. The `installation_info` gauge gains a `topology` label and the status page shows the value, so an operator can see which boundary a deployment actually has. helm-unittest asserts by env and secret name that a split evaluator pod carries neither the Remediation App key nor the remediator DSN, extending the existing `secret_scoping_test.yaml` pattern, and the database grant tests run as the two roles rather than as the owner (AR-0032-09). Verification: adopted.

**Applied 2026-10-04:** Goals ("Least privilege by construction, in `topology: split`"), Architecture (the credential property), API / Interface Changes ("Metrics and status", "Chart"), Risks ("`all` is combined trust") and Testing Strategy ("Topology"); recorded as D7.

### AR-0029-03 (high): Pre-release status does not make persisted workflows disposable

**Basis:** A16–A20 and Migration / Rollout Plan. A deployment can have live rc workflows, staged checks and GitHub PRs even without GA users. Reusing `repo/<id>` and discovery workflow IDs with changed commands/task queues can break replay. Closing v1 PRs while its workers still run allows v1 to reopen them; resetting Temporal without reconciling Postgres can lose in-flight intent. The migration sequence does not define writer quiescence, recovery or rollback.

**Proposed correction:** Specify a concrete cutover: stop old writers, drain or terminate named executions, reconcile completed external writes, activate the new snapshot/schema, and only then start the new workflows. Choose replay-compatible rollout or an explicitly scoped reset, with a recovery path for rc installations. Retire parity tests only after their relevant safety scenarios have replacement coverage.

**Verification:** Cut over with a running check, an open PR with human edits, and a GitHub write whose database record failed. Restart or roll back and demonstrate no duplicate writer, lost intent or recreated v1 PR.

**Response:** **Accepted.** Replay compatibility with the Phase 10–13 histories is not attempted. The controls workflows get new workflow type names (`EvaluationWorkflow`, `RemediationWorkflow`, `ControlsDiscoveryWorkflow`) and new workflow IDs, so no old history can replay into new code and no `GetVersion` gate is needed; A16 is kept with that qualification. The cutover becomes an ordered runbook in DESIGN-0032's Migration / Rollout Plan: (1) scale the v2 `worker` role to zero and confirm no v1 pods run; (2) terminate `repo/*`, `installation/*` and `policy-rollout/*` executions and delete the `discovery` and `snapshot` schedules; (3) reconcile external writes by listing every open repo-guardian PR from GitHub and recording it before anything closes it; (4) run migrations 00004–00007; (5) deploy the new roles and bootstrap; (6) only then close v1's PRs with the pointer comment (A17). Because every current install is an rc with no GA users, this reset is the only supported rc path.

Rollback is a Postgres restore plus the previous chart. A check in flight at step 1 is simply re-run after step 5, since checks are idempotent on their check key, and a GitHub write whose record failed is found in step 3 by exact identity rather than recreated. Parity tests are retired per scenario, and only when the IMPL names the replacement test for that scenario. Verification: amended — the rollback case is "restore and redeploy", not a live roll back, and the human-edited open PR is adopted (AR-0032-02), not recreated.

**Applied 2026-10-04:** assumptions A8 and A16, Migration / Rollout Plan (step 2's last bullet and the step 4 runbook), Risks ("Schema churn") and Testing Strategy ("Cutover"); recorded as D8.

### AR-0029-04 (high): `name@version` does not freeze the meaning of a control

**Basis:** D3, A7 and DESIGN-0032's fingerprint definition. A catalogue definition, template or Go evaluator can change while retaining `codeowners@1`. If rule IDs/statuses and read blob SHAs stay the same, the fingerprint does not change even when the required owners or remediation template change. Re-evaluating on a policy rollout is insufficient: an already-acknowledged generation remains settled. Historical results also cannot identify which definition or implementation produced them.

**Proposed correction:** Define immutable control revisions or a content digest covering effective parameters, templates, referenced schema bytes and evaluator semantics. Bind assignments, results and remediation intent to that revision. Specify which display-only edits are excluded and how a binary semantic change invalidates evaluations.

**Verification:** Change required owners, template bytes and evaluator behavior separately without changing the integer version. Each semantic change must invalidate stale posture/remediation; title-only changes must follow the declared cosmetic-change policy.

**Response:** **Accepted.** `name@version` stays the reference humans and policies use, and a content digest becomes the identity results bind to. Each control definition gets a `revision`: a sha256 over the canonical definition, which is the id, version, type name, the type's declared `Semantics` integer, the rules with their parameters, `apply`, the template bytes and any referenced schema bytes. Title, description and PR title and body are excluded as display-only, so a cosmetic edit changes nothing. `control_assignments.revision` and `control_results.revision` store it, the fingerprint includes it, and a revision change is a policy-version change that re-evaluates and re-remediates even an acknowledged generation. A Go evaluator whose meaning changes bumps its `Semantics` constant, and a golden digest test per built-in type pins the value so an accidental semantic change fails CI. The digest lands in DESIGN-0031 and its storage in DESIGN-0030, AR-0033-03 is closed the same way, and D3 is kept without its claim that the integer alone identifies behaviour. Verification: adopted.

**Applied 2026-10-04:** Vocabulary (Control, new Control revision row, Fingerprint), assumption A7, Data Model and Testing Strategy ("Control revision"); D3 amended and D9 added. Two later decisions narrow this response: the "required owners" verification case is phrased as a rule-parameter change because the ownership rules are deferred to DESIGN-0034, and the closure of AR-0033-03 through this digest fell away when DESIGN-0033 D6 dropped the shared schema file.

### AR-0029-05 (medium): Separate installations do not prove independent capacity

**Basis:** Architecture's statement that evaluation never competes with remediation. Separate primary GitHub budgets/task queues are useful isolation, but both paths still share Temporal, Postgres, network capacity and potentially GitHub secondary limits. A fleet-wide evaluation rollout or thousands of due remediation workflows can overwhelm those shared services; a per-repository PR cap does not bound fleet-wide work.

**Proposed correction:** Narrow the independence claim to the resources actually isolated. Specify activity concurrency, rollout fan-out, sweep batch limits, shared-capacity targets and throttle/hold retry cadence. Define how queue scaling leaves capacity for discovery, resolution and lifecycle cleanup.

**Verification:** Exercise fleet onboarding and policy rollout together with exhausted GitHub budgets and a remediation backlog. Measure evaluation freshness, cleanup latency and database load against explicit targets.

**Response:** **Accepted with changes.** The independence claim in Architecture is narrowed to what the two Apps actually isolate: the GitHub primary budget, the App credentials, the task queue and the worker role. The shared capacity is named next to it: Temporal, Postgres, egress and GitHub secondary limits. Explicit knobs land in DESIGN-0032: per-role activity concurrency (`EVALUATOR_CONCURRENCY`, `REMEDIATOR_CONCURRENCY`), rollout fan-out through the existing `POLICY_ROLLOUT_WINDOW`, batch sizes for the sweep and for lifecycle maintenance, and per-role pool sizes. Secondary-limit throttles already defer under IMPL-0022's one-mechanism rule, so no new gate is added. The rejected part is design-time capacity targets: they are operational SLOs, recorded as metrics (evaluation freshness p95, cleanup latency, Postgres pool wait) and set per deployment, not numbers this document commits to. Verification: amended — the combined load exercise is an IMPL soak task measured against those metrics rather than against design-time numbers.

**Applied 2026-10-04:** Overview (second paragraph), Architecture ("Two Apps, two of what the Apps can isolate"), API / Interface Changes ("Env"), Risks ("Shared capacity") and Testing Strategy ("Load"); recorded as D10.

## Open Questions

### OQ1: When does the controls model land relative to v2.0.0?

**Resolved 2026-10-04: (a).**

- (a) ✅ recommended: **before v2.0.0, replacing the rule engine on the v2 line.** v2 is pre-release, its data model is not frozen, and shipping v2.0.0 with rule-keyed findings would mean migrating a GA schema later. The rc line absorbs the churn.
- (b) After v2.0.0, as v3. v2.0.0 ships sooner on the current engine, but users get two breaking migrations in a row, and the shared-file conflicts ship in GA.
- (c) On v2.0.0 behind a flag, with both engines. It doubles the surface the team must test, for a pre-release product.
- other:

### OQ2: Where does this work happen?

**Resolved 2026-10-04: (a).**

- (a) ✅ recommended: **on the `v2` branch, phase by phase like IMPL-0025.** `main` (v1) gets bug fixes only. The `internal/checker` divergence between branches is accepted, because v1 stops receiving engine features.
- (b) A new long-lived `v2-controls` branch merged into `v2` when evaluation works end to end. It isolates churn from the v2 rc line, at the cost of a second integration branch to keep current.
- other:
