---
id: DESIGN-0033
title: "fwsync as inspiration: shared HCL concepts and whether to reuse it"
status: Draft
author: Donald Gifford
created: 2026-10-02
---

<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN-0033: fwsync as inspiration: shared HCL concepts and whether to reuse it

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [Two pipelines side by side](#two-pipelines-side-by-side)
  - [Concept mapping](#concept-mapping)
  - [What transfers, and where it lands](#what-transfers-and-where-it-lands)
  - [What does not transfer](#what-does-not-transfer)
  - [Reuse options evaluated](#reuse-options-evaluated)
  - [The one real seam: the governed tag schema](#the-one-real-seam-the-governed-tag-schema)
  - [Assumptions about fwsync](#assumptions-about-fwsync)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Decisions](#decisions)
- [Open Questions](#open-questions)
  - [OQ1: Who owns GitHub repository custom properties?](#oq1-who-owns-github-repository-custom-properties)
  - [OQ2: How does repo-guardian read the governed tag definitions?](#oq2-how-does-repo-guardian-read-the-governed-tag-definitions)
  - [OQ3: What are the GitHub property keys called?](#oq3-what-are-the-github-property-keys-called)
  - [OQ4: Should repo-guardian publish repository compliance into Wiz?](#oq4-should-repo-guardian-publish-repository-compliance-into-wiz)
- [References](#references)
<!--toc:end-->

## Overview

[fwsync](https://github.com/donaldgifford/fwsync) is the maintainer's other HCL-schema-driven governance tool: Wiz configuration (security frameworks, cloud configuration rules, scan policies, projects) kept as HCL documents in git and diff-applied to the Wiz API, with a generator that compiles a compact domain schema into those documents. It was designed a few weeks before the controls model in DESIGN-0029 to DESIGN-0032, by the same author, against the same Backstage-centred platform, and its vocabulary — framework, subcategory, rule, policy, scope, audit-first ratchet — reads like ours.

This document does two things. It maps fwsync's concepts onto the controls model so the overlap and the real differences are explicit, and it names what transfers (seven conventions and a test kit, each landing in a specific place in DESIGN-0030 to DESIGN-0032). Then it evaluates every way repo-guardian could *use* fwsync — to manage the policy, to manage the controls, to consume its output, to feed it, or as a library — and recommends against all of them except one: the governed **tag schema** that fwsync owns (`component`, `owner`, their value grammar and per-platform rendering) is the single artifact both tools should read, because repo-guardian's `custom_properties` control and fwsync's DESIGN-0003 GitHub carrier otherwise govern the same repository properties from two definitions.

## Goals and Non-Goals

### Goals

- A concept-by-concept mapping between fwsync's document model and the controls model, naming what each concept solves in each tool.
- A concrete list of fwsync conventions to adopt, each with the DESIGN-0030/0031/0032 section it amends.
- A decision on each reuse option: fwsync managing the policy, managing the catalogue, its output as our input, our output as its input, fwsync as a Go library.
- A recorded boundary for the one shared concern: GitHub repository custom properties.

### Non-Goals

- Changing fwsync. Where a seam needs a change on the fwsync side (promoting `tagschema` to a public module), this document records the ask; the design belongs in fwsync's docs.
- Re-litigating DESIGN-0029 to DESIGN-0032. This document amends them with specific conventions; it does not reopen their decisions.
- Pushing repo-guardian compliance into Wiz. Evaluated and deferred (OQ4), not designed.
- Rego anywhere in repo-guardian. Controls are typed Go (DESIGN-0031 D1); fwsync's rego conventions are noted for the *shape* they share, not adopted.

## Background

fwsync (RFC-0001, DESIGN-0001, ADR-0001 to ADR-0007, IMPL-0001; all at commit `14fbbef`) exists because the cloud estate carries two ungoverned tagging conventions, and the fix was one schema as the source of truth enforced through Wiz at PR time, at Kubernetes admission and on the deployed estate. Its two layers:

- **An applier core.** HCL documents of four kinds (`framework`, `rule`, `scan_policy`, `project`; `control` designed and deferred) decoded into a typed model, validated (slug grammar, reference resolution, dead subcategories, duplicate slugs), planned against live Wiz objects fetched by GUID through a committed lockfile, and applied with staged concurrency. Pruning is lock-bounded: fwsync deletes only what it created.
- **A generator layer.** `fwsync gen tags` compiles `schemas/tags.hcl` — tags with value grammars, facets, enforcement levels, an attribute map of where tags live per native type — into the committed `wiz/` tree: documents plus rego whose only varying part is a marked data region.

Its three Draft designs extend this: DESIGN-0002 adds an IaC governance framework where "policies gate, rules detect, controls verify" under one requirement identity, and DESIGN-0003 carries the governed tags to Docker labels, Kubernetes labels and **GitHub repository custom properties** (`component`, `owner`, kebab-case), with the note that for GitHub "the Backstage scaffolder sets repo custom properties at creation; the Backstage↔Wiz sync reconciles drift for existing repos".

repo-guardian's controls model (DESIGN-0029 to DESIGN-0032) is the other side of that platform: it evaluates GitHub repositories against opinionated controls assigned by enterprise and org policies and remediates by PR. Its `custom_properties` control (DESIGN-0031) already writes `Owner` and `Component` to repositories from `catalog-info.yaml` — it is, in practice, the reconciliation fwsync's DESIGN-0003 assumes someone else performs.

## Detailed Design

### Two pipelines side by side

```mermaid
flowchart LR
    subgraph FW["fwsync (desired state into one control plane)"]
        FS["schemas/tags.hcl<br/>domain schema"] -- "gen tags" --> FD["wiz/ documents + rego<br/>committed, header-owned"]
        FD -- "validate, plan" --> FP["plan: create / update / delete<br/>by lockfile GUID"]
        FP -- "apply, --prune" --> WIZ[("Wiz API")]
        FL["fwsync.lock.json<br/>slug → GUID"] --- FP
    end

    subgraph RG["repo-guardian (evaluate many repositories, propose by PR)"]
        RC["catalogue + enterprise/org policies<br/>hand-authored HCL"] -- "resolve" --> RA["assignments<br/>per repository and control"]
        RA -- "evaluate (read-only App)" --> RR["control results<br/>fingerprint, generation"]
        RR -- "changed and mode = remediate" --> RP["remediation PR<br/>per control, adopt-only-own"]
        RP --> GH[("GitHub")]
        RDB[("Postgres<br/>results, remediations")] --- RR
    end

    BS["Backstage catalog<br/>component, owner identity"]
    BS -. "catalog-info.yaml" .-> RR
    BS -. "entity names validated downstream" .-> WIZ
    FS -. "governed tag definitions<br/>(the shared seam)" .-> RC
```

The shapes rhyme — schema in, validate, plan, act, record what you own — and the directions differ. fwsync owns its target: Wiz holds exactly what the documents say, and a plan is a diff of desired against observed. repo-guardian never owns a repository: evaluation observes and classifies, remediation *proposes*, and a human merges. That difference decides most of what follows.

### Concept mapping

| fwsync | repo-guardian (DESIGN-0029 to 0032) | Same idea | Different because |
| ------ | ----------------------------------- | --------- | ----------------- |
| `framework` with `category` / `subcategory` (a Wiz object; subcategories are surface-agnostic requirements) | control catalogue; a `control "name@N"` is the requirement (DESIGN-0030) | one requirement identity that every surface reports into; the rollup answers "compliant?" once | a framework is data pushed to Wiz; a control is Go behaviour registered in the binary |
| `rule` (Wiz cloud configuration rule: rego matcher, native types, `subcategories = [...]`) | control rule with a `kind` implemented in Go (DESIGN-0031) | a rule reports into exactly one requirement; "detect" is separate from "verify" | rego evaluated by Wiz over exported objects vs typed Go evaluated by us over repository content and settings |
| `scan_policy` (CI/CD gate: fail builds over a severity budget) | `mode = evaluate \| remediate` per enterprise, org, `repos` block, control (DESIGN-0030) | the gate is written separately from the check, and ratchets per tier | fwsync's gate blocks a build; ours gates whether a PR is opened |
| `project` (scope by account tags) and DESIGN-0002 `tier` facets | `enterprise.orgs`, org policy, `repos { match }` globs (DESIGN-0030) | scoping lives in policy, not in rules | fwsync scopes by facts (tag values, facets); we scope by org and repository name only (DESIGN-0030 D4) |
| domain schema → `gen` → committed documents | hand-authored catalogue; no generator | documents are data, reviewed as diffs | our catalogue is a dozen controls with no per-tier expansion; a generator earns nothing yet (D4 here) |
| `file("rego/<slug>/rule.rego")` — content is a referenced file, never inlined | templates referenced by name from the template store; rule `params` | content lives beside the document and is reviewed as a file diff | our templates are Go `text/template` programs, not data (see "What does not transfer") |
| lockfile `slug → {id, type}`, written per completed create; lock-bounded prune | `remediations` row per (repository, control) with PR number, URL, head SHA; branch adopted only when every commit is the Remediation App's (DESIGN-0032) | act only on what you created, recorded at the moment you created it | fwsync commits a file to git; we record in Postgres, because our "lock" changes on every evaluation |
| `plan` = diff desired vs observed by GUID; `--detailed-exitcode` 2 on changes | evaluation = observe at a pinned SHA, classify, fingerprint; `repo-guardian evaluate` preview (DESIGN-0032 D7) | a plan is data with reasons, rendered separately | we never "apply" a repository; the plan becomes a PR |
| `gen --check` drift: `missing`, `stale`, `orphan` by generated header | `exact` file control; the template-passes-rules load check (DESIGN-0031 D2) | ownership is declared in the artifact and checked mechanically | our ownership is `Resources()` on the control, not a header in the file |
| `validate`: slug grammar, dangling reference = error, dead subcategory = warning, cross-file duplicates | policy load validation: ownership collisions, unknown control refs, required reasons (DESIGN-0030) | validate the whole tree before anything acts | — |
| audit-first ratchet (RFC-0001 Phases 1 to 3) | evaluate is the default mode; remediate per org, then per control (DESIGN-0030 modes) | nothing enforces until the numbers have been looked at | — |
| ADR-0007 attribute map: where a tag lives per native type; rego dispatches on the value's actual shape, so a wrong entry is "path unused", never a false positive | `FileSpec.Locations` per file control, `dependency_updates` location list, `catalog_info` annotation map (DESIGN-0031) | "where the fact lives" is data; a wrong row fails safe | — |

### What transfers, and where it lands

Each item below is an amendment to a sibling design, applied in the IMPL plan, not a new mechanism.

1. **Documents are data (ADR-0002) for the catalogue and policies.** The policy decoder evaluates HCL with no variables and no functions except a `file()`-style reference that returns a cleaned, in-root path and never contents; a syntactic check rejects expressions where a literal is required. *Lands in* DESIGN-0030 "Validation". Today's loader uses `hclparse` + `Content()` with a fixed schema, which already rejects unknown attributes; the addition is the empty `EvalContext` and the path function.
2. **Slug grammar and the reference chain.** Control ids and rule ids follow fwsync's slug grammar (`^[a-z0-9]+(-[a-z0-9]+)*$`), references resolve or load fails, and a catalogue control assigned by no policy is a load **warning** (fwsync's "dead subcategory"). *Lands in* DESIGN-0029 D2 and DESIGN-0030 "Validation".
3. **Plan as data with reasons.** `repo-guardian evaluate --repo org/name --format json` (DESIGN-0032 D7) emits the control results and, in remediate preview, the `ChangeSet` with a `Reason` per change (`missing`, `rule_failed:<id>`, `orphan`), and exits 2 under `--detailed-exitcode` when any control is non-compliant — fwsync's exit-code contract (0 ok, 1 error, 2 usage or diff). fwsync has no JSON mode; we add one from the start. *Lands in* DESIGN-0032 "API / Interface Changes".
4. **Lock-bounded action, stated as a principle.** repo-guardian closes, updates or deletes only PRs and branches recorded in `remediations`, and adopts a branch only when every commit is the Remediation App's. DESIGN-0032 already does both; this names the invariant so later features (API remediations, label sync) inherit it. *Lands in* DESIGN-0032 "Failure semantics" as the first row.
5. **Ownership drift with three verdicts.** The `exact` file control's evidence distinguishes `missing`, `stale` (present, differs) and — for the generic `file` control only — `orphan` (a file we wrote that no control claims any more), which is DESIGN-0032's orphan cleanup made explicit in the evidence. *Lands in* DESIGN-0031 "Generic file".
6. **Schema-owned addressing with fail-safe lookup (ADR-0007).** Every "where does this fact live" list (`FileSpec.Locations`, `dependency_updates` locations, the annotation map) is data on the control definition with a conformance test that a wrong entry yields `not found`, never a false `pass`. *Lands in* DESIGN-0031 "Conformance suite".
7. **The test kit.** Golden tree (`gen.Check` over the committed tree inside `go test`), seeded-change tests (one schema edit touches exactly the expected files), one `testdata/<defect>/` directory per failure class with a table test on a diagnostic substring, a stateful fake target that records call order and injects failures, and a recorded `RoundTripper` for the external API. repo-guardian already has the golden-tree pattern in `make lint-monitoring` and the stateful fake in the checker tests; the defect-directory and seeded-change patterns are new. *Lands in* DESIGN-0030 and DESIGN-0031 "Testing Strategy".

Also worth a look during the IMPL, not a design decision: fwsync decodes through `hclkit`, which gives every declaration its own source range (`def_range`, `label_range`, `attr_value_range` tags on the model) so diagnostics point at the exact line. Our loader reports file-level errors. If `hclkit` is importable it replaces hand-rolled range plumbing.

### What does not transfer

- **Committed generation (ADR-0003) for the catalogue.** fwsync generates because one tag expands into N rules × M surfaces × tiers. Our catalogue has no such product; hand-authored documents are the whole thing. repo-guardian already uses committed generation where it earns its keep (dashboards and alerts, `make monitoring-generate` + drift gate).
- **Data-region rego and sibling byte-identity (ADR-0005, ADR-0006).** No rego here. The *shape* survives as "a control type is one Go implementation, versions differ only in catalogue data" (DESIGN-0029 D3).
- **Templates as data.** fwsync forbids logic in documents; our file templates are `text/template` programs with curated helpers and `ValidateZero`. That stays — a CODEOWNERS template genuinely needs the org and team names interpolated — but the controls model narrows where it matters: the template is rendered once, by `Remediate`, and must pass every remediable rule at load (DESIGN-0031 D2). The program is contained, not eliminated.
- **The lockfile as a git artifact.** Our ownership record changes on every evaluation and lives beside the results; committing it would be noise.
- **Facts as a scoping dimension.** fwsync's facets and `project.scope.account_tags` select by what a thing *is*. DESIGN-0030 D4 chose name globs only. Not reopened; noted because the first request for "apply this control to every repository with `type: service`" will have fwsync's facets as the obvious shape.

### Reuse options evaluated

| Option | What it would mean | Verdict | Why |
| ------ | ------------------ | ------- | --- |
| A. fwsync manages repo-guardian's **policy** | enterprise/org policies as fwsync documents, synced to repo-guardian | **No** | fwsync syncs into Wiz only: its target interfaces are typed on `wizclient` structs, its four kinds are hardcoded in plan ordering, apply switches and lock validation, and nothing is importable (everything is `internal/`). repo-guardian's policy is a mounted ConfigMap loaded at start (DESIGN-0030 D3); git plus the chart is already its delivery, and the policy-rollout workflow is its "apply" |
| B. fwsync manages the **control catalogue** | same, for `control` documents | **No** | same reasons; and a generator layer in front of the catalogue earns nothing (D4 here) |
| C. repo-guardian **consumes fwsync output** | read the committed `wiz/` tree, the lockfile or `plan` text | **No, except the tag schema** | fwsync's outputs are Wiz rules, rego and a slug→GUID lock; none is an input repo-guardian evaluates. The exception is the *input* side, `schemas/tags.hcl`: it defines the governed tags whose GitHub rendering repo-guardian writes (next section) |
| D. fwsync **consumes repo-guardian output** | repository compliance rolled into a Wiz framework so the heatmap shows repo governance next to cloud posture | **Deferred (OQ4)** | Wiz frameworks roll up Wiz *findings*; there is no external-findings input (assumption F9). fwsync DESIGN-0003's own open question — can repo-scan findings attach to custom subcategories at all — is the gate. repo-guardian's `/rules`, `/orgs` and compliance snapshots are the data if it opens |
| E. fwsync as a **Go library** (plan/apply engine, decoder, lockfile) | import or fork `internal/plan`, `internal/apply`, `internal/decode` | **No** | not importable, Wiz-typed, builds only with the private `wiz-go-gen` SDK and `WIZ_*` credentials, and the direction is wrong: repo-guardian never applies a repository, it proposes a PR. Patterns transfer (previous section); code does not |
| F. **Shared decode layer** (`hclkit`) | both tools decode HCL with the same range-aware kit | **IMPL question** | depends on whether `hclkit` is published; no design impact |

### The one real seam: the governed tag schema

fwsync's `schemas/tags.hcl` is the source of truth for *what the platform tags things with*: `component` (Backstage component entity name, pattern `^[a-z0-9]([-_.a-z0-9]*[a-z0-9])?$`), `owner` (Backstage group), `account-class` (accounts only), each with meaning, remediation text, scope and per-platform key rendering (`platform_key_style`, per-tag `platform "k8s" { key = ... }` overrides). DESIGN-0003 extends scope to `repo` and renders GitHub custom properties kebab-case: `component`, `owner`.

repo-guardian's `custom_properties` control writes `Owner` and `Component` (PascalCase, the reserved names since IMPL-0017) from `catalog-info.yaml`'s `spec.owner` and `metadata.name`, plus any annotation mapped by `annotation_properties`. Two definitions of the same two properties, in two repositories, by the same author, already disagree on casing.

```mermaid
flowchart TD
    TS["fwsync schemas/tags.hcl<br/>component, owner: meaning, pattern, platform keys"]
    TS -- "gen tags" --> WR["Wiz rules: detect missing or invalid<br/>(DESIGN-0003 GitHub carrier, when Wiz Code can see properties)"]
    TS -- "shared module or fixture (OQ2)" --> CP["repo-guardian custom_properties control<br/>managed names and value grammar"]
    CI["catalog-info.yaml<br/>spec.owner, metadata.name"] --> CP
    CP -- "Remediation App, PATCH properties" --> GHP["GitHub repository<br/>custom properties"]
    GHP -. "VCS resource export" .-> WR
```

The boundary this document proposes (OQ1): **repo-guardian writes, Wiz detects.** repo-guardian's control is the "Backstage↔Wiz sync reconciles drift" that DESIGN-0003 assumes, because it already runs per repository with write credentials and the catalog file in hand; Wiz's rules, if the carrier ships, are the independent audit of the same properties. For that to be one requirement rather than two, the property names and value grammar must come from one place (OQ2) and the casing must be settled (OQ3).

What repo-guardian would take from the tag schema, concretely: the set of governed tags with `repo` in scope, each tag's rendered GitHub key, and each tag's value pattern — three facts per tag. The `custom_properties` control's managed set becomes `{rendered key for every repo-scoped tag} ∪ annotation_properties values`, and a `value_pattern` rule kind checks well-formedness before writing (today a malformed `spec.owner` is written as-is). Catalog *existence* stays downstream (fwsync's own rule: "value validation is well-formedness only").

### Assumptions about fwsync

Verified against commit `14fbbef` unless marked.

| # | Assumption | If wrong |
| - | ---------- | -------- |
| F1 | Every fwsync package is under `internal/`; there is no `pkg/` and no importable API | option E and the `tagschema` module (OQ2a) get cheaper |
| F2 | fwsync has no machine-readable output; `plan` and `apply` render human text only | option C gains a surface, still not one we need |
| F3 | The four document kinds are hardcoded in plan ordering, apply dispatch and lock validation; there is no kind or surface registry | options A/B remain wrong in direction even if they become possible |
| F4 | fwsync has not been applied to a live tenant (no `wiz/fwsync.lock.json` in the repo); IMPL-0001's integration suite is deferred | none for us; it bounds how much to lean on its conventions as proven |
| F5 | The `control` kind is designed (DESIGN-0002) and rejected by the decoder today | none |
| F6 | `schemas/tags.hcl` has no `platform "github"` block yet; DESIGN-0003's GitHub carrier is a sketch, and `tagschema` decodes `platform` blocks only for `k8s` | OQ2's shared module has to grow the GitHub platform before repo-guardian can read rendered keys from it |
| F7 | fwsync builds only with the private `wiz-go-gen` module (`GOPRIVATE`, PAT) | reinforces E = no |
| F8 | fwsync's decoder is `hclkit` (external); whether it is importable by repo-guardian is unknown | option F |
| F9 | *Unverified.* Wiz has no API for ingesting findings produced outside Wiz; framework rollups come only from Wiz rules and controls | if wrong, option D becomes a design of its own, not a deferral |
| F10 | DESIGN-0003's GitHub carrier depends on an open question in fwsync (can repo-scan findings bind to custom subcategories; do VCS native types expose custom properties) | if it never ships, repo-guardian is the only enforcement of repository properties, which strengthens OQ1a |

## API / Interface Changes

No new surface in repo-guardian from this document alone. The conventions above amend:

- `repo-guardian evaluate --repo <org>/<name> [--format json] [--detailed-exitcode]` (DESIGN-0032 D7) gains the exit-code contract and the `Reason` field on preview changes.
- `repo-guardian policy validate` reports slug-grammar failures, unresolved references and unassigned catalogue controls (warning), with exit codes 0 / 1 / 2 as fwsync's `validate`.
- The `custom_properties` catalogue definition gains, under OQ2a, a `tag_schema = file("...")` reference (or an import of the shared module's embedded defaults) from which managed names and value patterns derive; `annotation_properties` stays for the non-governed extras.

On the fwsync side, the ask recorded for OQ2a: promote `internal/tagschema` to a public Go module exposing the decoded schema (tags, scopes, platform key rendering) with no Wiz dependency.

## Data Model

None. The tag schema, if shared, is read at policy load like any other catalogue input; nothing about it is stored. `custom_properties` evidence gains `source = tag_schema | annotation_properties` per managed name so the UI can show where a property came from.

## Testing Strategy

- **Agreement test (any OQ2 answer).** A copy of fwsync's `schemas/tags.hcl` as a repo-guardian test fixture, and a test that the `custom_properties` control's managed names and patterns equal the schema's repo-scoped tags. Under OQ2a the fixture is replaced by the module; under OQ2c the test is the only thing stopping silent divergence.
- **Defect directories** for the policy loader: `testdata/bad-slug/`, `dangling-control/`, `unassigned-control/` (warning), `expression-in-document/`, `escape-path/`, each asserting a diagnostic substring — fwsync's `internal/decode` and `internal/validate` layout.
- **Seeded-change tests** on resolution: one policy edit changes exactly the expected assignments and nothing else (fwsync's `gen` seeded-change pattern applied to `resolve`).
- **Fail-safe lookup** in the conformance suite: for every file control, a fixture where the file exists at a path *not* in `Locations` yields `not found`, never a pass or a parse of the wrong file.

## Migration / Rollout Plan

1. Fold items 1 to 7 of "What transfers" into DESIGN-0030/0031/0032 as one amendment PR, then into the IMPL plan as tasks.
2. Resolve OQ1 to OQ3 before the `custom_properties` control is rewritten under DESIGN-0031; casing (OQ3) is a breaking change for any org whose property schema already carries `Owner`/`Component`, and needs both keys during a migration window.
3. If OQ2a: open the `tagschema` promotion in fwsync; until it lands, ship with the fixture + agreement test.
4. Revisit option D when fwsync's DESIGN-0003 binding question is answered.

## Decisions

- **D1 No runtime or library dependency on fwsync** — nothing in repo-guardian imports, forks, shells out to or reads the output of fwsync. Its packages are `internal/`, its target is Wiz, and its direction (apply into a control plane you own) is not ours (propose by PR into repositories you do not).
- **D2 Borrow conventions, not code** — the seven items in "What transfers" are adopted as amendments to DESIGN-0030/0031/0032 and tasks in the IMPL plan; this document is their provenance.
- **D3 One definition of the governed tags** — repo-guardian's `custom_properties` control derives its governed property names and value patterns from the platform tag schema rather than declaring them itself; how it reads that schema is OQ2 and what the keys are called is OQ3.
- **D4 No generator layer in front of the catalogue** — the catalogue is hand-authored data. A `repo-guardian gen` compiling a compact schema into controls is reconsidered only if per-org or per-tier expansion appears.
- **D5 `evaluate` preview emits JSON and Terraform-style exit codes from the first release** — fwsync's missing JSON mode is the one gap in an otherwise sound CLI contract; we do not repeat it.

## Open Questions

### OQ1: Who owns GitHub repository custom properties?

- (a) ✅ recommended: **repo-guardian writes, Wiz detects.** repo-guardian's `custom_properties` control is the reconciliation DESIGN-0003 assumes exists (it has the catalog file and write credentials per repository); fwsync's GitHub carrier, if it ships, is an independent audit of the same properties. Both read one tag definition (OQ2).
- (b) The Backstage↔Wiz sync writes properties; repo-guardian drops the `custom_properties` control and only evaluates presence. Loses the per-repository catalog-info read that makes the values correct.
- (c) Both write. Last writer wins and nobody can explain a value.
- other:

### OQ2: How does repo-guardian read the governed tag definitions?

- (a) ✅ recommended: **a public `tagschema` Go module** promoted out of fwsync (decode only, no Wiz dependency), imported by both tools; repo-guardian's control derives managed names and patterns from it. Until it exists, a copied fixture with an agreement test.
- (b) repo-guardian reads `schemas/tags.hcl` by path at policy load (`tag_schema = file(...)`), with its own minimal decoder. Two decoders of one grammar drift.
- (c) Duplicate the three facts per tag by hand in the catalogue, guarded only by the agreement test. Cheapest now; the test is the whole safety net.
- other:

### OQ3: What are the GitHub property keys called?

- (a) ✅ recommended: **kebab-case `component` / `owner`, as fwsync DESIGN-0003 renders them**, and repo-guardian migrates off `Owner` / `Component`. One rendering rule across every carrier (`platform_key_style.github = "kebab-case"`); the org property schema carries both keys for a migration window and repo-guardian writes both until the old keys are removed.
- (b) PascalCase `Component` / `Owner` everywhere on GitHub; fwsync sets `platform_key_style.github = "PascalCase"`. No repo-guardian migration; GitHub becomes the one carrier that does not follow the kebab-case convention.
- (c) Keep both tools as they are. Two keys for one fact on every repository.
- other:

### OQ4: Should repo-guardian publish repository compliance into Wiz?

- (a) ✅ recommended: **not in v2.** Revisit when fwsync's DESIGN-0003 binding question is answered and if Wiz exposes an external-findings input (assumption F9). repo-guardian's `/rules`, `/orgs` endpoints and compliance snapshots are the data source either way.
- (b) Design a Wiz exporter now, assuming an ingestion path exists.
- (c) Never; Backstage is the shared hub and repository compliance surfaces there (a separate design).
- other:

## References

- fwsync at commit `14fbbef`: README; RFC-0001 Tag Governance Enforcement; DESIGN-0001 Wiz desired-state management and tag-compliance generation; DESIGN-0002 IaC governance framework; DESIGN-0003 Tag compliance extension (containers, Kubernetes, GitHub); ADR-0001 to ADR-0007; IMPL-0001; `schemas/tags.hcl`; `internal/model`, `internal/decode`, `internal/plan`, `internal/apply`, `internal/lockfile`, `internal/gen`.
- DESIGN-0029 Controls and policies (overview); DESIGN-0030 Policy model; DESIGN-0031 Control framework; DESIGN-0032 Evaluation and remediation workflows.
- DESIGN-0019 / IMPL-0017 — the current `custom_properties` reconciler (managed set `Owner`, `Component`, `annotation_properties`).
- `docs/operations/monitoring-generation.md` — repo-guardian's existing committed-generation pattern.
