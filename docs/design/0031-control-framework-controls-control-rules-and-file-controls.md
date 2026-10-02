---
id: DESIGN-0031
title: "Control framework: controls, control rules and file controls"
status: Draft
author: Donald Gifford
created: 2026-10-02
---

<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN-0031: Control framework: controls, control rules and file controls

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [Package layout](#package-layout)
  - [The interface](#the-interface)
  - [Rules and results](#rules-and-results)
  - [File controls](#file-controls)
  - [Built-in control types](#built-in-control-types)
    - [CODEOWNERS](#codeowners)
    - [Catalog info](#catalog-info)
    - [Dependency updates](#dependency-updates)
    - [Generic file (the escape hatch)](#generic-file-the-escape-hatch)
    - [API-remediated types](#api-remediated-types)
  - [The registry](#the-registry)
  - [Shared evaluation reads](#shared-evaluation-reads)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
  - [OQ1: Evaluate + Remediate, or one Run(mode)?](#oq1-evaluate--remediate-or-one-runmode)
  - [OQ2: How is "the template passes every rule" enforced?](#oq2-how-is-the-template-passes-every-rule-enforced)
  - [OQ3: What does "pattern is owned by owners" mean for CODEOWNERS?](#oq3-what-does-pattern-is-owned-by-owners-mean-for-codeowners)
  - [OQ4: Does codeowners.exists require GitHub to consider the file valid?](#oq4-does-codeownersexists-require-github-to-consider-the-file-valid)
  - [OQ5: Can catalog-info remediation edit fields in an existing file?](#oq5-can-catalog-info-remediation-edit-fields-in-an-existing-file)
  - [OQ6: May one control read another control's parsed resource?](#oq6-may-one-control-read-another-controls-parsed-resource)
  - [OQ7: Do JSON5 Renovate configs get minimal edits?](#oq7-do-json5-renovate-configs-get-minimal-edits)
  - [OQ8: Is the generic file control limited to exists and exact?](#oq8-is-the-generic-file-control-limited-to-exists-and-exact)
<!--toc:end-->

## Overview

A control is the expected state of one resource, and a control rule is one verifiable requirement of it. This document defines how controls are built in Go:

- the **`Control` interface**, with separate `Evaluate` (read-only) and `Remediate` (produces a change, writes nothing) methods;
- **results**: how rule results combine into a control status;
- **file controls**: the shared machinery to find, read, parse, render and edit a file, so each file kind's package contains only what is specific to that file;
- the **built-in control types**, starting with `codeowners`, and the generic `file` escape hatch.

Policies (who gets which control) are in DESIGN-0030. Running controls and applying their changes is in DESIGN-0032.

## Goals and Non-Goals

### Goals

- **All knowledge of a resource lives in one package.** For example, `internal/controls/codeowners` knows where GitHub looks for CODEOWNERS, how to parse it, what "`.wiz` is owned by X" means, and how to fix it.
- **Remediation is whole-control.** An absent file gets the full template, which satisfies every rule at once. A present file gets the edits for exactly the failing rules, all in one change.
- **Evaluation cannot write.** `Evaluate` receives a reader with no write methods, enforced by the type system and backed by the read-only App (DESIGN-0032).
- **Remediation is a value.** `Remediate` returns a change set, and the workflow applies it. Controls stay testable without a GitHub fake that records writes.
- **Built-in guarantees, tested per control type:**
  - remediating and re-evaluating passes every fixable rule;
  - remediating twice changes nothing;
  - the default template passes every rule.

### Non-Goals

- **User-defined control types at runtime** (plugins, scripting). A control type is Go code and ships in a release.
- **Arbitrary assertions on arbitrary files.** The generic `file` control renders one template to one path. Anything requirement-level is a control type.
- **Applying changes.** Branches, commits and PRs belong to the remediation workflow (DESIGN-0032).

## Background

v1 evaluates rules with a generic mechanism (`exists` / `contains` / `exact` / `absent` plus regex and `yaml_path` assertions) and remediates by writing a rendered template to a target path. The engine knows nothing about the file it writes, which is the root cause in INV-0021. The catalog-info parser (`internal/catalog`) is the one place that understands a file's format, but only the `custom_properties` reconciler uses it.

## Detailed Design

### Package layout

```text
internal/control/            # the framework: interfaces, results, registry, file helpers
internal/controls/codeowners/
internal/controls/catalog_info/
internal/controls/dependency_updates/
internal/controls/file/      # the generic escape hatch
internal/controls/repo_settings/
internal/controls/branch_ruleset/
...
```

### The interface

```go
package control

// Type is a control type: Go code that knows one kind of resource.
// It validates a catalogue definition and builds a Control from it.
type Type interface {
    Name() string                                     // "codeowners"
    RuleKinds() []RuleKind                            // what rules a definition may declare
    Build(def Definition) (Control, error)            // validates parameters at policy load
}

// Control is one catalogue definition, ready to run.
type Control interface {
    ID() ID                                           // {Slug: "codeowners", Version: "1.0"}
    Resources() []Resource                            // what it owns (DESIGN-0030 ownership)
    Rules() []Rule                                    // declared rules, in display order

    // Evaluate reads, never writes. The reader is pinned to one commit
    // of the default branch, or of a remediation PR's head (DESIGN-0032).
    Evaluate(ctx context.Context, in EvalInput) (Evaluation, error)

    // Remediate returns the change that makes the failing, fixable rules
    // pass, computed against the same pinned state. It writes nothing.
    Remediate(ctx context.Context, in RemediationInput) (ChangeSet, error)
}

type EvalInput struct {
    Repo   RepoContext   // org, name, default branch, facts
    Reader github.Reader // contents at a pinned SHA, settings, rulesets: read-only
    Vars   template.Vars // {{ .Org }}, {{ .Repo }}, …
}

type RemediationInput struct {
    EvalInput
    Evaluation Evaluation // the evaluation of the same pinned state
}

// ChangeSet is what a remediation proposes.
type ChangeSet struct {
    Files []FileChange // write or delete, applied as one commit (DESIGN-0032)
    API   []APIChange  // direct changes: settings, rulesets, labels, properties
    Notes []string     // manual steps for failing rules with no remediation
}
```

Two methods rather than one `Run(mode)` (OQ1), because the methods need different capabilities. `Evaluate` must be callable by a process that holds no write credentials. `Remediate` needs no credentials at all, because it returns a value.

### Rules and results

A rule is declared in the catalogue (DESIGN-0030) using a **rule kind** its control type provides, with parameters the type validates at load:

| Field | Meaning |
| ----- | ------- |
| `id` | stable slug, `wiz-owners` |
| `number`, `title` | display: `1.2`, ".wiz is owned by …" |
| `kind` | a kind the type offers: `exists`, `owners`, … |
| parameters | kind-specific: `pattern`, `owners`, … |
| `remediate` | whether this rule's failures are fixed by `Remediate` |

Evaluating a rule gives a **rule result**:

| Status | Meaning |
| ------ | ------- |
| `pass` | the requirement holds |
| `fail` | the requirement does not hold |
| `error` | could not be determined (API error, unparseable input the rule needs) |
| `not_applicable` | the rule does not apply to this repository (for example a rule about a language the repository does not use) |

Each result carries **evidence**: typed, versioned, and text-only, the same contract the v2 UI already renders (DESIGN-0027).

The **control status** is derived from the rule results, in this order:

| Rule results | Control status |
| ------------ | -------------- |
| any `fail` | `non_compliant` |
| no `fail`, any `error` | `unknown` |
| all `not_applicable` | `not_applicable` |
| otherwise (every rule `pass` or `not_applicable`) | `compliant` |

`fail` outranks `error` deliberately. A control with one definite failure is non-compliant whatever its other rules could not determine, and that is what remediation acts on. An `error` never triggers remediation (DESIGN-0032).

### File controls

Most controls own a file. The framework provides the shared parts, so a control type writes only what is specific to its file:

```go
package control

// FileSpec describes the file a file control owns.
type FileSpec struct {
    Locations []string // in the precedence GitHub (or the tool) uses; first found wins
    Canonical string   // where a new file is created
}

// LocateFile finds the file a tool would actually use.
func LocateFile(ctx context.Context, r github.Reader, spec FileSpec) (path string, content []byte, found bool, err error)
```

A file control type implements three things:

1. **Parse(content) → model.** For example, CODEOWNERS entries with their line numbers and comments preserved.
2. **Check(model, rule) → result.** One rule kind, evaluated against the parsed model.
3. **Fix(model, failing rules) → model, and Render(model) → content.** Edits that leave unrelated lines byte-identical.

The framework turns those into `Evaluate` and `Remediate`:

```mermaid
flowchart TD
    S[pinned state] --> L[LocateFile: first location found]
    L --> F{found?}

    F -- no --> EA["Evaluate: every rule that needs the file fails<br/>(evidence: file_missing)"]
    EA --> RA["Remediate: render the default template to Canonical<br/>one FileChange"]

    F -- yes --> P[Parse]
    P --> PE{parse ok?}
    PE -- no --> EP["Evaluate: rules needing the model → error<br/>(evidence: parse_error, line)"]
    EP --> RP["Remediate: none<br/>(Notes: fix the syntax by hand)"]
    PE -- yes --> C[Check each rule against the model]
    C --> EC[results + evidence]
    EC --> RC["Remediate: Fix(model, failing ∩ remediate)<br/>Render → one FileChange at the found path"]
```

The framework's rules for file remediation:

- **An absent file gets the template.** The template must pass every rule of the control; that is tested at build time (see Testing), so one write fixes everything. Templates are rendered with the existing `internal/template` renderer (assumption A10).
- **A present file is edited at the path where it was found,** never moved to `Canonical`. Moving a working file is not a remediation anyone asked for.
- **An unparseable file is never overwritten.** Its rules report `error`, and remediation adds a manual note. Replacing a broken file with a template would discard whatever the team meant to write (the INV-0011 A1 principle).
- **Edits are minimal.** `Fix` changes only what failing rules require, and `Render` must round-trip untouched content byte-for-byte. A property test enforces this per type.

### Built-in control types

#### CODEOWNERS

| Aspect | Behaviour |
| ------ | --------- |
| Locations | `.github/CODEOWNERS`, `CODEOWNERS`, `docs/CODEOWNERS`; the first found is the one GitHub uses (assumption A21) |
| Model | ordered entries `{pattern, owners, line}`, with comments and blank lines preserved |
| Rule kind `exists` | a CODEOWNERS file is found and parses; optionally "valid" through GitHub's CODEOWNERS errors endpoint (OQ4, assumption A22) |
| Rule kind `owners` | `pattern` is owned by at least `owners` (OQ3 defines "owned") |
| Rule kind `default_owner` | `*` has an owner |
| Fix for `owners` | append `pattern owner…` at the end of the file. CODEOWNERS uses last-match-wins, so an appended line takes effect over earlier lines for the same pattern. |
| Template | the operator's `codeowners` template, which must satisfy every rule (tested) |

The wiz example under this type:

- **No CODEOWNERS:** the template is written, and it already contains the `.wiz` line, because the template must pass `wiz-owners`.
- **A team's CODEOWNERS without `.wiz`:** one line is appended, and the team's file is otherwise untouched.
- **An org that does not get the control:** it is never evaluated there (DESIGN-0030), so it cannot touch the file.

#### Catalog info

- **Locations:** `catalog-info.yaml`, `catalog-info.yml`.
- **Model:** `internal/catalog`'s parse (assumption A11), extended to keep the YAML node tree for minimal edits.
- **Rule kinds:**
  - `exists`;
  - `kind` (the entity is a Component);
  - `field_set` (`spec.owner`, `spec.system`, …), optionally with a `contains`;
  - `annotation_set`;
  - `no_placeholders`.
- **Remediation:**
  - an absent file gets the template;
  - a present file gets no automatic remediation by default, because a filled-in catalog-info is owned by its team, and a failing field needs a human value. Failing rules produce `Notes` in the PR.
  - Whether field-level fixes are allowed is OQ5.

#### Dependency updates

One control for one opinion, which replaces v1's `renovate_config` + `dependabot` + `no_dependabot` + `when` gate (the INV-0021 R2 loop):

- **Parameters:** `tool = "renovate" | "dependabot"`, and `renovate.extends = "github>org/preset"`.
- **Rule kinds:**
  - `configured` (the chosen tool's config exists);
  - `extends` (the Renovate config extends the preset);
  - `exclusive` (the other tool's config is absent).
- **Remediation:** write the chosen tool's config from its template, add the preset to `extends` in an existing Renovate config (JSON or JSON5, OQ7), and delete the other tool's config. All of it is one change set: one PR, with no loop possible.

#### Generic file (the escape hatch)

- **Parameters:** `path`, `template`, `mode = "exists" | "exact"`.
- **Rules:**
  - `exists`: the file is present;
  - `exact`: the file equals the rendered template (YAML-semantic comparison for `.yml`/`.yaml`, byte comparison otherwise, as v1).
- **Remediation:** write the template.
- **Owns `file:<path>`.** That is one path and one owner, so a requirement-level check on that file means writing a control type.

#### API-remediated types

`repo_settings`, `branch_ruleset`, `labels` and `custom_properties` remediate through `ChangeSet.API`, not files. Applying a direct change has no review step, so DESIGN-0032 OQ8 decides whether these may apply directly in `remediate` mode, or only propose through a PR or issue.

### The registry

```go
control.Register(codeowners.Type{})
```

At policy load, every catalogue definition is passed to its type's `Build`, which validates rule kinds and parameters and compiles templates. Load fails on unknown types, unknown rule kinds, bad parameters, or a template that fails its own rules. That last check runs only when templates are static enough; see OQ2.

### Shared evaluation reads

Several controls read the same data. For example, `catalog_info` and `custom_properties` both read `catalog-info.yaml`. The evaluation workflow gives every control in one evaluation the same `github.Reader`, which is pinned to one SHA and caches reads. Each file is fetched once per evaluation, whatever the number of controls. Controls stay independent: none reads another control's *results* (OQ6).

## API / Interface Changes

- New packages `internal/control` and `internal/controls/*`.
- `github.Reader` (contents at a ref, list directory, settings, rulesets, custom properties, CODEOWNERS errors) and `github.Writer` (the remediation workflow's apply operations). This replaces the single `github.Client` for control code (assumption A12).
- Catalogue HCL as in DESIGN-0030; rule kinds and parameters per type, documented from the types' own metadata (`RuleKinds()`), so the docs cannot drift from the code.

## Data Model

Controls are stateless. Results and evidence are persisted by the evaluation workflow (DESIGN-0032). Evidence is versioned JSON per `(control type, rule kind)`, and the UI renders only versions it knows (the v2 evidence contract).

## Testing Strategy

Per control type, in its own package:

1. **Fixture tables:** repository states (no file, each location, each rule passing or failing, unparseable, multiple locations) × rules → expected results and evidence.
2. **Remediation property:** for every fixture, the following must hold for every fixable rule:

   ```text
   evaluate(apply(state, remediate(state))) passes every fixable rule
   ```

3. **Idempotence:** `remediate(apply(state, remediate(state)))` is an empty change set.
4. **Minimal edits:** untouched lines are byte-identical after `Fix`/`Render`. Round-trip fuzzing of the parser (`Render(Parse(x)) == x`) covers this for every input the parser accepts.
5. **Template passes its rules:** the default template (and any operator template in tests) evaluated with sample variables passes every rule of the control.
6. **Framework tests:** `LocateFile` precedence, control-status derivation (every row of the table), and registry validation errors.

A shared conformance suite (`control/controltest`) runs properties 2–5 against any type given its fixtures. A new control type gets them by adding fixtures.

## Migration / Rollout Plan

The order in which types are built:

1. `codeowners`: the motivating case, and the reference implementation of a file control.
2. `file`.
3. `catalog_info`.
4. `dependency_updates`.
5. The API types.

The first evaluate-only rc needs only the types that v1's built-in defaults cover.

## Open Questions

### OQ1: `Evaluate` + `Remediate`, or one `Run(mode)`?

- (a) ✅ recommended: **two methods, with `Remediate` returning a `ChangeSet`.** The type system then enforces the capability split: evaluation workers call a method whose inputs contain no writer. Remediation is a pure function of state, testable without fakes.
- (b) `Run(ctx, mode, client)`. A single entry point, extensible to other modes. But it needs a client capable of both, so the read-only guarantee becomes a runtime convention.
- (c) Two methods, with `Remediate` writing directly through a `github.Writer`. Each control decides how to commit, which re-creates per-control commit logic and INV-0021's partial-apply problems.
- other:

### OQ2: How is "the template passes every rule" enforced?

- (a) ✅ recommended: **at load, by rendering the template with sample variables and evaluating the control against it.** Load fails if any rule fails. Sample variables come from the type (org `example-org`, repo `example`). A template whose output depends on real repository data is rare, and the runtime self-check below still catches it.
- (b) Runtime only: after `Remediate`, evaluate the changed content, and refuse to propose a change that does not pass. That is always correct, but a broken template is discovered on the first repository, not at deploy.
- (c) Both (a) and (b). The safest option, and only slightly more code.
- other:

### OQ3: What does "`pattern` is owned by `owners`" mean for CODEOWNERS?

- (a) ✅ recommended: **effective ownership.** Using last-match-wins over the whole file, the owners GitHub would request for a path matching `pattern` include every listed owner. That is what the requirement means in practice, and the append-at-end fix satisfies it by construction.
- (b) A literal line: there is a line whose pattern is exactly `.wiz` with exactly those owners. Simple to check, but a broader later line (for example `* @org/other`) silently overrides it and the check still passes.
- (c) A literal line *and* effective ownership. Strict, but it fails files that are correct under (a) and written differently.
- other:

### OQ4: Does `codeowners.exists` require GitHub to consider the file valid?

- (a) ✅ recommended: **yes, when the CODEOWNERS errors endpoint is available, as a separate `valid` rule kind.** Unknown owners and bad patterns are the common real-world CODEOWNERS failure, and GitHub reports them itself. Keeping it a separate rule leaves `exists` cheap and lets a policy choose.
- (b) Parse-only validation. No extra API call, but a file that references a deleted team passes.
- other:

### OQ5: Can catalog-info remediation edit fields in an existing file?

- (a) ✅ recommended: **no, only create from the template when absent; failing fields become manual notes in the PR or report.** Field values (owner, system) need human knowledge, and inventing them produces plausible-looking wrong data.
- (b) Fill missing fields with placeholders. Every rule then passes structurally, but `no_placeholders` fails forever. It recreates a loop in a new form.
- (c) Per-rule opt-in (`remediate = true` on a field rule with a `value` parameter). Only for fields with an org-wide constant value.
- other:

### OQ6: May one control read another control's parsed resource?

- (a) ✅ recommended: **controls share reads (one cached reader per evaluation) but never results.** `custom_properties` parses `catalog-info.yaml` itself, through the shared parser package. Evaluation order then never matters, and a failure in one control cannot cascade into another.
- (b) Explicit dependencies (`inputs = ["catalog_info"]`), with the framework evaluating in dependency order and passing models. Less duplicated parsing, but it introduces a graph and order-dependent results.
- other:

### OQ7: Do JSON5 Renovate configs get minimal edits?

- (a) ✅ recommended: **edit JSON with a node-preserving encoder; for JSON5, report `extends` failures as manual notes instead of rewriting.** Rewriting JSON5 through a JSON encoder drops comments and formatting a team chose.
- (b) Normalise JSON5 to JSON on edit. Automatic, but it rewrites a team's whole file to add one preset.
- other:

### OQ8: Is the generic `file` control limited to `exists` and `exact`?

- (a) ✅ recommended: **yes.** One path, one template, present or identical. Anything that inspects inside a file is, by definition, a control type. This is the line that keeps the INV-0021 class of conflict out.
- (b) Also allow a single regex `contains` rule. Convenient for quick checks, but it is the first step back to generic assertions, and it has no safe remediation other than overwriting.
- (c) Also allow `absent`. "This path must not exist" is occasionally useful (a committed `.env`). It is safe as one path with one owner, but each such case is better served by a type that knows why the file is forbidden.
- other:
