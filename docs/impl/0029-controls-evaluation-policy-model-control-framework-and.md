---
id: IMPL-0029
title: "Controls evaluation: policy model, control framework and evaluation workflow"
status: Draft
author: Donald Gifford
created: 2026-10-07
---

<!-- markdownlint-disable-file MD024 MD025 MD041 -->

# IMPL-0029: Controls evaluation: policy model, control framework and evaluation workflow

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Starting point (2026-10-07)](#starting-point-2026-10-07)
- [How the phases are ordered](#how-the-phases-are-ordered)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Control framework and the codeowners reference type](#phase-1-control-framework-and-the-codeowners-reference-type)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Policy model and resolution](#phase-2-policy-model-and-resolution)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: The remaining built-in control types](#phase-3-the-remaining-built-in-control-types)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: Controls schema and store](#phase-4-controls-schema-and-store)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: Evaluation workflows and activities](#phase-5-evaluation-workflows-and-activities)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: Posture, metrics, generated monitoring and the report](#phase-6-posture-metrics-generated-monitoring-and-the-report)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
  - [Phase 7: Switch-over](#phase-7-switch-over)
    - [Tasks](#tasks-6)
    - [Success Criteria](#success-criteria-6)
  - [Phase 8: API and UI (D32)](#phase-8-api-and-ui-d32)
    - [Tasks](#tasks-7)
    - [Success Criteria](#success-criteria-7)
  - [Phase 9: Documentation, the evaluate-only rc and the homelab run](#phase-9-documentation-the-evaluate-only-rc-and-the-homelab-run)
    - [Tasks](#tasks-8)
    - [Verification results](#verification-results)
    - [Success Criteria](#success-criteria-8)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
  - [OQ1: Where does the new policy package live while the rule engine still builds?](#oq1-where-does-the-new-policy-package-live-while-the-rule-engine-still-builds)
  - [OQ2: Do the control types implement Remediate in this plan?](#oq2-do-the-control-types-implement-remediate-in-this-plan)
  - [OQ3: Which control types must exist before the first controls rc?](#oq3-which-control-types-must-exist-before-the-first-controls-rc)
  - [OQ4: Does this plan ship an evaluate-only rc, and where does it run?](#oq4-does-this-plan-ship-an-evaluate-only-rc-and-where-does-it-run)
  - [OQ5: What does the evaluate-only rc do with mode = "remediate"?](#oq5-what-does-the-evaluate-only-rc-do-with-mode--remediate)
  - [OQ6: How is the work split into pull requests?](#oq6-how-is-the-work-split-into-pull-requests)
  - [OQ7: Does 00003controlsresults land whole in this plan?](#oq7-does-00003controlsresults-land-whole-in-this-plan)
  - [OQ8: Which process exports the posture gauges?](#oq8-which-process-exports-the-posture-gauges)
  - [OQ9: How much of the UI does this plan rebuild?](#oq9-how-much-of-the-ui-does-this-plan-rebuild)
  - [OQ10: When is the monitoring tier regenerated?](#oq10-when-is-the-monitoring-tier-regenerated)
  - [OQ11: How does the chart express a policy directory in a ConfigMap?](#oq11-how-does-the-chart-express-a-policy-directory-in-a-configmap)
  - [OQ12: Where does POST /evaluate keep its one-per-minute dedupe?](#oq12-where-does-post-evaluate-keep-its-one-per-minute-dedupe)
- [References](#references)
<!--toc:end-->

## Objective

Build the evaluation half of the controls model on the `v2` branch and
switch the binary onto it, so that the next v2 rc evaluates controls in
place of rules:

- `internal/control` holds the framework and `internal/controls/*` holds
  the eight built-in control types (DESIGN-0031);
- `internal/policy` loads a policy directory (catalogue, enterprise and
  org policies) and resolves each repository into assignments
  (DESIGN-0030);
- `EvaluationWorkflow`, the `Evaluate` activity and
  `ControlsDiscoveryWorkflow` run on `repo-guardian-eval` and record
  control results fenced on the assignment epoch (DESIGN-0032, the
  evaluation half);
- the rule engine, `internal/findings` and the rc schema code are
  deleted, and the binary runs on the controls goose chain;
- the API, UI, posture gauges, generated monitoring and the report are
  re-keyed from rules to controls (DESIGN-0032 D32);
- an evaluate-only rc ships and runs in the homelab beside v1, so
  posture numbers can be compared before anything writes (DESIGN-0029
  rollout step 3).

**Implements:** DESIGN-0030 (whole), DESIGN-0031 (whole, except applying
change sets), DESIGN-0032 (evaluation, results, API, metrics and the
evaluator's half of the data model), the evaluation items of
DESIGN-0029. It takes over IMPL-0025 tasks 17.1, 17.2, 17.9 and 19.2.

## Scope

### In Scope

- `internal/control`: value types, `Validate`, `StatusOf`, `Fingerprint`,
  `Revision`, `ValidateChangeSet`, resource canonicalization, the
  pinned, caching, recording reader wrapper, `FileSpec`/`LocateFile` and
  the file-control adapter, the registry; `internal/control/controltest`.
- `internal/controls/{codeowners,file,catalog_info,dependency_updates,repo_settings,branch_ruleset,labels,custom_properties}`,
  each with `Evaluate` and (per OQ2) `Remediate`.
- `internal/policy`: the directory loader, catalogue, enterprise and org
  policy decoding, load validation, `Resolve`, `Snapshot`, resolution
  golden cases, the example policies as directories.
- Schema `00002_controls_policy`, `00003_controls_results` (per OQ7) and
  `00004_compliance`; the controls sqlc package; the evaluator's store
  methods, compliance queries and grants.
- `EvaluationWorkflow`, `Evaluate`, the record and park activities,
  `ControlsDiscoveryWorkflow`, controls rollout and snapshot, webhook
  routing for push, repository and both Apps' installation events;
  `repo-guardian evaluate`.
- Metrics, the posture exporter, the deployment info series, the
  monitoring generator over the snapshot, the alert catalogue and its
  chart mirror, the report.
- The switch-over: deleting `internal/checker`, `internal/reconciler`,
  `internal/findings`, `internal/shadow`, the old policy loader and the
  rc schema code; the chart's policy map; the removed knobs.
- The API per D32 except `/remediations`, and the UI views it affects.
- `docs/operations/v2-overview.md` and `v2-onboarding.md` rewrites, the
  policy and control-type reference, CLAUDE.md, the rc and its homelab
  run.

### Out of Scope

- Everything IMPL-0028 (Foundations) delivers: the v1 runtime deletion,
  the `control.Reader`/`PRObserver`/`Writer` adapters, the GraphQL
  commit, throttle classification per rate-limit bucket, the two Apps
  and their webhook paths, the two task queues and worker deployments,
  the `evaluator`/`remediator` roles, KEDA per queue, the database roles
  and `pgtest` roles, `00001_core`, and the phase-0 spikes.
- The permission printer, which IMPL-0028 Phase 3 builds and which
  derives each App's set from the registered control types.
- Everything IMPL-0030 (Remediation) delivers: `RemediationWorkflow`,
  the signal-with-start activity, PR observation and `pull_request`
  routing, the `remediation_due` truth table, writes, caps, holds,
  lifecycle maintenance, `/remediations` and its view, the remediation
  metrics and alerts, the
  `v2-migration.md` rewrite, and the v2.0.0 fresh install.
- The remediation preview in evaluate mode (DESIGN-0032 D7, later).
- CODEOWNERS ownership rules (DESIGN-0034, deferred).

## Starting point (2026-10-07)

Facts about the `v2` branch this plan depends on, checked before writing
the phases:

| # | Fact | Where | Effect on this plan |
| --- | --- | --- | --- |
| S1 | `internal/findings` is imported by `policy`, `api`, `report`, `store`, `store/postgres`, `activities`, `checker`, `shadow` and tests in `worker` | `grep 'internal/findings"'` | Phase 7 re-cuts every importer in one PR |
| S2 | The old policy loader owns the `internal/policy` path, and `monitoring.Derive` takes `*policy.PolicyConfig` | `internal/policy/loader.go`, `internal/monitoring/derive.go:55` | OQ1; Phase 6 re-points `Derive` |
| S3 | The compliance percent is floored to one decimal in SQL, with a Go copy for summed totals | `queries/compliance.sql:21`, `store.CompliantPercent` (`internal/store/api_views.go:22`) | Phase 4 keeps both and a test that they agree |
| S4 | `TaskPriority` defines 2, 3 and 4 only | `internal/workflows/types.go:14-16` | Phase 5 adds `PriorityManual = 1` |
| S5 | `RepoWorkflow`'s selector, coalescing and priority keys live on `repoLoop` | `internal/workflows/repo.go:57-180` | `EvaluationWorkflow` reuses the loop under a new type and id |
| S6 | Of nine E4 matchers, seven are emitted only by `internal/worker`, `checker/sweep.go` and `reconciler/custom_properties.go` | `internal/monitoring/dashboard/e4.go`, `logline_test.go:29` | Phase 6 re-cuts them to v2 emitters |
| S7 | `installation_info` is set from `activities/check.go:49` and `services.go:91`, per installation, no `app` label | `internal/metrics/metrics.go:398` | Phase 6 adds `app`; `topology` goes on a new deployment series |
| S8 | sqlc has one schema directory (`migrations_v2`) and one package (`sqlcdb`) | `sqlc.yaml` | Phase 4 adds a block for the controls chain; Phase 7 removes the rc block |
| S9 | The chart's `policy.config` is one HCL string; templates come from `templates.files` at `TEMPLATE_DIR` | `values.yaml:336,548`, `deployment.yaml:128-146` | Phase 7 replaces both with a policy directory (OQ11) |
| S10 | The parity suite is `internal/checker/parity_test.go` and `internal/store/postgres/parity_integration_test.go` | | Phase 7 retires it scenario by scenario |
| S11 | `TestAPIQueries_AreScoped` scans only `api_*.sql` | `internal/store/postgres/apiscope_test.go:21` | `/policies` gets a Go-level test |

## How the phases are ordered

New code lands alongside the rule engine first and is exercised by tests
against the controls chain, not by the running rc binary. One phase then
switches the binary over and deletes what it replaces, and the phases
after it add what is new on top of the switched binary:

1. **Build** (Phases 1–6): framework, policy, types, schema, workflows,
   then posture and monitoring. Each phase keeps `make ci` green. Nothing
   is registered on a worker or served by the API yet.
2. **Switch** (Phase 7): register the controls workflows, delete the
   rule engine, the findings model, the rc schema code and the API
   endpoints and UI views that read them, and move the chart onto a
   policy directory.
3. **Add** (Phases 8–9): the D32 API and UI views, docs, the rc and the
   homelab run.

The branch is not tagged between the start of Phase 1 and Phase 9; the
last tagged rc is IMPL-0028's (or IMPL-0026's) and stays usable. Each
phase ends with `make lint` and `make test` green plus the phase's own
gates, one commit per numbered task, and its tests shown to be
non-vacuous (neutralize the code, watch the test fail, restore).

The type-building order follows DESIGN-0031: `codeowners` first as the
reference, then `file`, `catalog_info`, `dependency_updates`, then the
API types. Policy resolution comes after the framework because the loader
calls `Type.Build`.

## Implementation Phases

### Phase 1: Control framework and the codeowners reference type

#### Tasks

- [ ] 1.1 Starting check: confirm the IMPL-0028 skeleton is on `v2`
  (`internal/control` compiles with the `Reader`, `PRObserver` and
  `Writer` interfaces and their value types; the `Writer` implementation
  sits in its own package; `PRConfig` and `PRTemplate` live in
  `internal/template`; the depguard edges of DESIGN-0031 D17 are in
  `.golangci.yml`). List anything missing in this task and finish it here
  before 1.2.
- [ ] 1.2 Value types in `internal/control/types.go`, exactly as
  DESIGN-0031 § Supporting types: `Definition`, `BuildEnv`,
  `TemplateSource`, `ID` (with `String()` giving `slug@N` and `ParseID`),
  `Resource`, `RuleKind`, `RuleID`, `RuleKindSpec`, `ParamSpec`, `Rule`,
  `RuleStatus` and its five constants, `RuleResult`, `ReadState`,
  `ResourceRead`, `Evaluation`, `RepoContext`, `EvalInput`,
  `RemediationInput`, `FileChange`, `APIChange`, `ChangeSet`,
  `WorkflowApply`, and `Status` (`compliant`, `non_compliant`, `unknown`,
  `not_applicable`). One exported `ValidSlug` regexp
  (`^[a-z][a-z0-9]*([_-][a-z0-9]+)*$`) serves control slugs and rule ids.
- [ ] 1.3 `resource.go`: `Canonical(kind, key) (Resource, error)`. File
  keys go through `path.Clean` and are rejected when absolute, containing
  `..` or an empty segment, or starting with `./`, then compare byte for
  byte; `label:` keys are lower-cased; other kinds compare exactly;
  unknown kinds are an error. Table tests (`.github/./CODEOWNERS` equals
  `.github/CODEOWNERS`; `../x`, `/x`, `a//b` rejected; `Bug` equals
  `bug`) and `FuzzCanonicalFile` (canonical output is a fixed point, and
  never escapes the root).
- [ ] 1.4 `validate.go`: `Validate(c, ev)` (one result per declared rule;
  a missing result becomes `error{reason=missing_result}`; a result for
  an undeclared id or a duplicate fails as a bug; closed status enum;
  evidence is scalars and arrays only, at most 16 KiB per rule, with
  `evidence_kind` and `evidence_version` present) and `StatusOf(ev)` in
  the table's order. Tests: every row of the status table, `unknown`
  included; each `Validate` failure mode, including nested and oversized
  evidence.
- [ ] 1.5 `revision.go` and `fingerprint.go`: `Revision(t, def, env)` as
  sha256 over a canonical JSON encoding of id, version, type name,
  `Semantics()`, rules with parameters, `apply`, the template bytes and
  every referenced file's bytes, excluding title, description and
  `pr {}`; `Fingerprint(revision, ev)` over sorted (rule id, status) and
  sorted (resource, state, digest). Tests: title, description and
  `pr {}` edits leave the revision unchanged; a parameter, template byte,
  `apply` or `Semantics` change moves it; an API value moving between two
  wrong values moves the fingerprint with statuses unchanged; an absent
  property and a present null differ; evidence wording and `ObservedAt`
  do not move it.
- [ ] 1.6 `changeset.go`: `ValidateChangeSet(c, ev, cs)`: unique
  canonical paths; every file and API resource within `c.Owns()`; at most
  100 files and 1 MiB per file (or the limits the IMPL-0028 spike
  measured, as named constants citing it); regular files only, so a path
  the reader reported as a symlink or submodule must be in `Blocked`;
  every failing rule of `ev` in exactly one of `Fixed`, `Manual`,
  `Blocked`; every `Manual` and `Blocked` rule explained by a note.
  Table tests for each rejection.
- [ ] 1.7 `reader.go`: `NewEvalReader(base Reader, now func() time.Time)`
  wraps the per-repository adapter (already pinned to one commit by
  IMPL-0028's constructor), caches every read for the evaluation, and
  records a `ResourceRead` per resource (file digest = blob SHA, API
  digest = sha256 of canonical JSON, `ObservedAt` for API reads, absent
  reads recorded too). `Reads()` returns them sorted. Tests: two controls
  reading one file make one underlying call; an error read is recorded
  with `State = error`; a 403 on one endpoint surfaces as
  `ErrPermission` for the caller to map to `unknown{reason=permission}`.
- [ ] 1.8 `file.go`: `FileSpec`, `LocateFile` (first location found
  wins; every location recorded, absent ones included), and the
  file-control adapter that turns a type's `Parse`, `Check`, `Fix` and
  `Render` into `Evaluate` and `Remediate` per DESIGN-0031's flowchart:
  absent → rules needing the file fail with `file_missing` and the
  template is rendered to `Canonical`; unparseable → `unknown{parse_error}`
  and every rule `Blocked` with a note; present → `Check` each rule, `Fix`
  the failing remediable ones at the found path, the rest `Manual`. A
  parser size cap is a required field of the adapter.
- [ ] 1.9 `registry.go`: `Registry` with `Register`, `Type(name)` and
  `Build(def, env)`. `Build` fails on an unknown type, an unknown rule
  kind, a parameter not in `RuleKindSpec` or of the wrong type, a missing
  required parameter, a definition with no rules, `remediate = true` on a
  non-remediable kind, an `apply` value the type does not support
  (`workflow` without `WorkflowApply`), and a template whose render with
  the sample variables (org `example-org`, repository `example`) fails
  one of the control's remediable rules. Each error carries the
  definition's `slug@N` and rule id.
- [ ] 1.10 `controltest`: `Run(t, typ, fixtures)` runs DESIGN-0031
  properties 2–6 (remediation property, idempotence, minimal edits,
  template passes its rules, two rules failing together) against a
  `FakeRepo` that implements `Reader` from a map of files and API values,
  and an `Apply(repo, cs)` helper that applies file changes and an API
  overlay. A notes-only change set passes the property and is asserted
  to be a recommendation.
- [ ] 1.11 `internal/controls/codeowners`: the type as DESIGN-0031's
  table (three locations, `exists` only, write the template to
  `.github/CODEOWNERS` when no location has a file, never edit an
  existing file), `Semantics() = 1`, the embedded fallback template
  sourced as the IMPL-0028 template spike decided, fixtures for no file,
  each location, a comment-only file and two files at once, the
  conformance suite, and a golden `Revision`.
- [ ] 1.12 Non-vacuous check: swap the `fail`/`unknown` order in
  `StatusOf`, drop the `Owns()` check from `ValidateChangeSet`, and make
  the reader skip its cache; confirm the matching tests fail; restore.
  Record the failing test names here.

#### Success Criteria

- `internal/control` imports only `internal/template` of this module,
  and depguard enforces every DESIGN-0031 D17 edge.
- `codeowners` passes the conformance suite and its revision golden.
- Coverage of `internal/control` is at least 85%; 1.12 shows the
  framework tests fail without the code they test.

### Phase 2: Policy model and resolution

#### Tasks

- [ ] 2.1 Free the package path (OQ1): `git mv internal/policy
  internal/rulepolicy` and rewrite its importers' import paths, nothing
  else. `make ci` green. Phase 7 deletes `internal/rulepolicy`.
- [ ] 2.2 `internal/policy/load.go`: `Load(root string, reg
  *control.Registry, logger *slog.Logger) (*Snapshot, error)` over the
  DESIGN-0030 layout (`catalogue/*.hcl`, `templates/`, exactly one
  `enterprise.hcl`, `orgs/*.hcl`), using `hclparse.NewParser()`. Only
  literals and `file()` are allowed in expressions; every referenced file
  is resolved with `filepath.EvalSymlinks` and must stay under the root;
  each file is capped at 1 MiB. Errors carry `file:line`. No hot reload.
- [ ] 2.3 `catalogue.go`: decode each `control "<slug>"` block into a
  `control.Definition` (version, type, title, description, rules with
  `kind` defaulting to the id only when the id names a kind the type
  declares, template, `apply`, `pr {}`, type parameters), build it
  through the registry with a `BuildEnv` whose template source is the
  policy root's `templates/` plus the embedded fallbacks, and store its
  revision. Duplicate `slug@version` and slug or rule-id grammar
  violations fail load.
- [ ] 2.4 `enterprise.go` and `org.go`: decode `enterprise.hcl` (`orgs`,
  `mode`, `controls`, `remediation { max_open_prs, reopen_after }`, and a
  load error naming the block if an exclusion appears) and each
  `orgs/<org>.hcl` (`mode`, `controls`, `replace "x@1" { with, reason }`,
  `exclude "x@1" { reason }`, `control "x@N" { mode }`,
  `exclude_repos { match, reason }`, labelled `repos` blocks,
  `remediation`). An org file for an org not in `enterprise.orgs` fails
  load.
- [ ] 2.5 `validate.go`: every error and warning in DESIGN-0030's
  validation list, one table-test row each, asserting the message and the
  source location. Warnings go to the logger once at load.
- [ ] 2.6 `ownership.go`: the conservative load-time pairwise `Owns()`
  enumeration over every pair of definitions any org could assign
  together; overlaps are warnings naming both definitions and the
  resource, never errors (the runtime check in 2.7 is the authority).
- [ ] 2.7 `resolve.go`: `(*Snapshot).Resolve(repo Repository, access
  AppAccess) Resolution` implementing DESIGN-0030 steps 1–7, pure and
  with no API calls: policy state `managed`/`excluded`/`unmanaged`/
  `parked` with reason; baseline, then the org file in file order, then
  each matching `repos` block in file order; one row per slug; requested
  mode by specificity; effective mode `evaluate` with `mode_reason`
  `remediation_app_no_access` or `remediation_app_suspended` when the
  Remediation App cannot write; the write set as the union of `Owns()`,
  with any overlap marking both assignments `conflict` with an `error`;
  `ordinal`, `decision`, `baseline_control` and `source_block` on each
  assignment; resolved `MaxOpenPRs` and `ReopenAfter`. Epochs are not
  assigned here; the store assigns them (4.5).
- [ ] 2.8 Resolution goldens: `internal/policy/testdata/resolution/<case>/`
  each holding a policy directory, a `repo.json` input and an
  `expected.json`, run by one table test with `-update`. Cases at least:
  baseline only; org addition; `replace` to `@2`; `exclude` with reason;
  `repos` blocks matching in file order; `exclude_repos`; mode by
  specificity (enterprise, org, `control` block, `repos` block);
  Remediation App absent and suspended; an ownership conflict; archived,
  fork and parked repositories; an org outside `enterprise.orgs`.
- [ ] 2.9 `snapshot.go`: `Snapshot` with `Version()` (a hash over every
  loaded file, the template bytes and the embedded fallbacks actually
  used, so a template edit is a policy change), `Control(id)`,
  `Definition(id)`, `Controls()`, `WatchedPaths()` (the union of every
  definition's `file:` keys in `Owns() ∪ Reads()`, which ingest uses as
  its push filter), and `Summarize()` for `/policies` and
  `policy_versions`.
- [ ] 2.10 Example policies: write `examples/policy/{minimal,full,multi-org,renovate,enterprise}/`
  as directories covering what the five v1 examples covered, and rewrite
  `examples/examples_test.go` to load each and compare `Summarize()`
  against a committed golden. The v1 `guardian-*.hcl` files stay until
  Phase 7, because rule-engine tests still read them.
- [ ] 2.11 Non-vacuous check: reverse the specificity order and drop the
  `EvalSymlinks` containment check; confirm the goldens and the
  symlink-escape test fail; restore.

#### Success Criteria

- Every resolution golden passes, and a deliberate change to any of
  steps 1–7 fails at least one golden.
- Every DESIGN-0030 validation rule has a test with its message and
  location.
- `Resolve` has no dependency on `internal/github` or the store
  (depguard).

### Phase 3: The remaining built-in control types

Each type ships with its fixture table, the conformance suite, a golden
revision and a `Semantics()` constant of 1.

#### Tasks

- [ ] 3.1 `internal/controls/file`: parameters `path`, `template`,
  `mode` (`exists` or `exact`, never both); `exact` compares YAML
  semantically for `.yml`/`.yaml` and bytes otherwise; owns and reads
  `file:<path>`.
- [ ] 3.2 `internal/catalog`: keep the YAML node tree and source spans
  beside the parsed `Properties`, with a size cap, so a type can edit
  minimally; the existing `Parse` callers keep working until Phase 7.
- [ ] 3.3 `internal/controls/catalog_info`: two locations; rule kinds
  `exists` and `kind` (remediable) and `field_set` with `contains`,
  `annotation_set` and `no_placeholders` (`remediate = false`); an absent
  file gets the template with field rules in `Manual`; a present file is
  never edited (every failing rule `Manual`, `kind` included); an
  unparseable file puts every rule in `Blocked`.
- [ ] 3.4 `internal/controls/dependency_updates`: `tool` and
  `renovate.extends`; the Dependabot and Renovate locations owned and
  read; `package.json` read only; rule kinds `configured`, `extends` and
  `exclusive`; a node-preserving JSON span editor that adds the preset to
  `extends`; JSON5 gets a note, not an edit; deleting the other tool's
  config is in the same change set. Fixtures include `configured` plus
  `extends` failing together, and Dependabot plus a JSON5 Renovate config
  (partial: `exclusive` fixed, `extends` manual).
- [ ] 3.5 `internal/controls/repo_settings`: `setting:<property>` owned
  per declared property; rule kind `equals`; fix is
  `APIChange{setting:<property>, set, value}`. A 403 on the
  vulnerability-alerts read is `unknown{reason=permission}`.
- [ ] 3.6 `internal/controls/branch_ruleset`: owns `ruleset:<name>`;
  rule kinds `exists` and `matches` (YAML-semantic comparison of rules);
  a same-named ruleset whose `Source` is the organization or enterprise
  makes both rules `unknown{reason=inherited}` with a note and is never
  written.
- [ ] 3.7 `internal/controls/labels`: owns `label:<name>` for managed and
  retired labels, compared lower-cased; rule kinds `present` and
  `absent`; case handling on create and update as the IMPL-0028 label
  spike found.
- [ ] 3.8 `internal/controls/custom_properties`: properties with
  `source`, `normalize`, scope, rendered key and value pattern; the
  built-in `Owner` and `Component`; reads the two catalog-info paths;
  rule kinds `matches`, `value_pattern` and `defined`; the four source
  states of DESIGN-0031 D16 as fixtures with their exact per-property
  writes or retention; `CatalogParseFailedTotal` on an unparseable file;
  `defined` is `not_applicable` on a user-owned repository; each write is
  gated on its own property. `WorkflowApply` renders
  `.github/workflows/repo-guardian-custom_properties.yml` through the
  IMPL-0020 A2 env-indirection helpers (`yamlq`, `propenv`), with the
  injection regression test carried over from `internal/rules`.
- [ ] 3.9 `apply` on the four API types: `recommend` (default),
  `direct`, and `workflow` only on `custom_properties`; the registry
  rejects the rest (covered by 1.9's error table, extended here).
- [ ] 3.10 Parser fuzzing: `Render(Parse(x)) == x` for the catalog-info
  YAML and the Renovate JSON editors, plus a test at each parser's size
  cap.
- [ ] 3.11 Control-type reference generator: `repo-guardian controls
  docs` renders every registered type's `RuleKinds()` and parameters to
  `docs/usage/controls-reference.md`; `make controls-docs` writes it and
  `make lint-controls-docs` diffs it, wired into `make lint` like
  `lint-monitoring`.
- [ ] 3.12 Non-vacuous check: in each type, remove one `Fix` branch or
  one location; confirm the conformance suite or fixture table fails;
  restore. Record one line per type.

#### Success Criteria

- All eight types pass `controltest` and their revision goldens.
- Every multi-rule file type has a two-rules-failing fixture that passes.
- `make lint-controls-docs` is green and fails on a stale reference.

### Phase 4: Controls schema and store

IMPL-0028 provides the controls chain directory, its embed and goose
wiring, `00001_core`, and `pgtest` with an owner role plus `rg_evaluator`
and `rg_remediator`. This phase adds the rest of the chain and the
evaluator's store.

#### Tasks

- [ ] 4.1 `00002_controls_policy.sql`: `repository_policy_state` and
  `control_assignments` with the DESIGN-0030 and DESIGN-0032 columns
  (`epoch`, `revision`, `requested_mode`, `effective_mode`,
  `mode_reason`, `ordinal`, `state` including `conflict`, `error`,
  `baseline_control`, `decision`, `source_block`; PK `(repository_id,
  control_id)`), an epoch sequence, the `assignment_changed`
  `repository_events.kind`, and `app_installations` and
  `app_repository_access` with their row-level security policies (unless
  IMPL-0028 already created them, in which case this task records that).
- [ ] 4.2 `00003_controls_results.sql` (OQ7): `control_results`,
  `rule_results`, `result_events`, `remediations`, `remediation_steps`,
  `remediation_events`, the `remediation_due` and
  `remediation_maintenance` views, the append-only revokes, and the full
  DESIGN-0032 grant matrix to `rg_evaluator` and `rg_remediator`,
  verbatim from the design.
- [ ] 4.3 `00004_compliance.sql`: `compliance_snapshots` keyed `(org,
  control_id, decision, snapshot_at)` with the posture buckets, the
  excluded count and `coverage`.
- [ ] 4.4 sqlc: a second block in `sqlc.yaml` reading the controls chain
  and `internal/store/postgres/queries_controls/` into package
  `controlsdb`; `make generate-sql` and `make lint-sql` cover both blocks.
- [ ] 4.5 `store.Controls` interface and its Postgres implementation:
  - `UpsertRepositories` writes the repository row and its `app = 'eval'`
    access row in one transaction, then applies the resolution (below);
  - `ApplyResolution(repoID, Resolution)` upserts policy state and
    assignments, keeps the epoch of an unchanged active assignment and
    takes a new one from the sequence for a new or re-activated one,
    deletes the `control_results` row of a withdrawn or excluded
    assignment (cascading to `rule_results`) with a `result_events` row
    whose `to_status` is NULL, and writes `assignment_changed` events;
  - `StageEvaluation` / `RecordEvaluation` stage per-control payloads in
    `checks.pending_result` and commit them in one transaction fenced on
    the assignment epoch: a stale epoch discards the control's result and
    counts `results_discarded_total{reason="stale_epoch"}`; a first row
    inserts `eval_generation = 1`; a fingerprint change bumps the
    generation and writes `result_events`; `evaluated_sha`,
    `evaluated_at` and evidence refresh only for selected controls;
    recording is idempotent on the check key;
  - `RecordPolicyVersion` upserts with `ON CONFLICT (version) DO UPDATE
    SET activated_at = now()`, and the rollout reads the version with the
    latest `activated_at`.
- [ ] 4.6 Park with the result rule: `archived`, `fork`, `removed` and
  `installation_removed` clear results (with `result_events`);
  `access_denied` and `unknown` keep them; Evaluation App suspension is
  read from `app_installations.suspended_at` and is never a park. A table
  test per reason asserts rows kept or cleared.
- [ ] 4.7 Compliance queries in `queries_controls/compliance.sql`,
  starting from active assignments with a LEFT JOIN to results at the
  matching epoch and revision: buckets `compliant`, `non_compliant`,
  `pending`, `stale`, `unmeasurable` (by reason), `not_applicable`,
  `unknown`, plus excluded; percent `compliant/(compliant+non_compliant)`
  floored to one decimal and NULL on an empty denominator; coverage over
  assigned; baseline, org-added and exclusions variants.
  `store.CompliantPercent` keeps the Go mirror, and a test feeds the same
  counts to both and asserts they agree.
- [ ] 4.8 Grant tests (`pgtest`, as the roles, never the owner): every
  transaction in 4.5–4.7 succeeds as `rg_evaluator`; as `rg_remediator`,
  writing any evaluator-owned column of `control_results`,
  `control_assignments` or `repository_policy_state` (except
  `last_remediation_at`) fails; nobody can UPDATE or DELETE
  `result_events`.
- [ ] 4.9 Integration tests: epoch fencing (an assignment deleted and
  recreated during a record retry ends at generation 1); a policy revert
  A → B → A resolves under A again; the posture case (100 assigned, one
  result: 99 `pending`, coverage 1%, percent from one row); an empty
  fleet reads NULL, never 100%.
- [ ] 4.10 `migrate --dry-run` applies `00001`–`00004` to an empty
  database in one rolled-back transaction; `SchemaVersion` is 4.
- [ ] 4.11 Non-vacuous check: drop the epoch predicate from the record
  step, make withdrawal skip the delete, and remove the NULL guard from
  the percent; confirm 4.6, 4.9 and the agreement test fail; restore.

#### Success Criteria

- The controls chain applies to an empty database and `make lint-sql`
  is green for both sqlc blocks.
- Every grant test runs as an application role.
- Posture never reads 100% on an empty or unmeasured fleet.

### Phase 5: Evaluation workflows and activities

#### Tasks

- [ ] 5.1 Names and types in `internal/workflows`: `EvaluationWorkflowID`
  (`evaluation/<repository id>`), `ControlsDiscoveryWorkflowID`
  (`controls-discovery/installation/<id>/<delivery>`), schedule ids
  `controls-discovery` and `controls-snapshot`,
  `ControlsRolloutWorkflowID` (`controls-rollout/<version>`); signals
  `Recheck{Paths []string, Unknown bool, Priority}` and
  `PolicyChanged`; `PriorityManual Priority = 1`.
- [ ] 5.2 `EvaluationWorkflow` (`evaluation.go`), built from `repoLoop`:
  a jittered `EVAL_INTERVAL` timer, `recheck` and `policy_changed`
  signals, coalescing that unions changed paths with unknown dominating,
  the budget lease on `installation/eval/<installation id>`, deferral on
  throttle with nothing recorded, park on repository-level access loss,
  ContinueAsNew every 100 iterations carrying the coalesced set, and a
  drain of unread signals before ContinueAsNew. `repoLoop` stays for
  `RepoWorkflow` until Phase 7.
- [ ] 5.3 `Evaluate` activity (`internal/activities/evaluate.go`): read
  policy state and active assignments, skip unless `managed`; skip
  `conflict` assignments (reported `unmeasurable{reason=conflict}`);
  classify throttle first, then repository access loss; pin the default
  branch head through `PRObserver.GetRef`; select controls by
  `Owns() ∪ Reads()` against the changed paths (all on unknown, the
  schedule or `policy_changed`; API resources only on the schedule);
  run each through one `NewEvalReader`, then `Validate`, `StatusOf` and
  `Fingerprint`; stage the payload. A control-local 403 is
  `unknown{reason=permission}` and parks nothing.
- [ ] 5.4 Record and park activities: `RecordEvaluation` (4.5) and
  `ParkRepository` (4.6), with the `checks.trigger` values `pr_event`
  and `manual` accepted.
- [ ] 5.5 `ControlsDiscoveryWorkflow`: lists the Evaluation App's
  installations and repositories, refreshes `app = 'eval'` rows of
  `app_installations` and `app_repository_access`, and calls
  `UpsertRepositories`, which resolves inline between the upsert and the
  SignalWithStart of `evaluation/<id>`; parks missing repositories;
  records a `service_runs` row; sets `installation_info{app="eval"}`.
- [ ] 5.6 Controls rollout: on a new or re-activated policy version,
  `controls-rollout/<version>` re-resolves every repository, then
  signals `policy_changed` spread over `POLICY_ROLLOUT_WINDOW`, and marks
  the rollout complete. The evaluator's bootstrap ensures only the
  `controls-discovery` and `controls-snapshot` schedules.
- [ ] 5.7 Webhook routing on `/webhooks/github/eval`: a default-branch
  push signals `recheck` at priority 2 with the changed paths filtered
  through `Snapshot.WatchedPaths()` (a push with no watched path signals
  nothing; 2048 commits, `forced`, or more than 256 filtered paths is
  unknown, DESIGN-0032 § history growth); `repository` created,
  renamed, transferred, archived, unarchived and deleted run discovery,
  resolution or park; `installation` and `installation_repositories`
  from either App re-resolve the affected repositories, because a
  Remediation App change moves the effective mode.
- [ ] 5.8 Controls snapshot: `controls-snapshot` writes
  `compliance_snapshots` rows from 4.7's query.
- [ ] 5.9 `repo-guardian evaluate --repo <org>/<name> [--format json]`:
  loads the policy directory, authenticates as the Evaluation App,
  checks access, resolves, evaluates with no store, and prints a table
  or JSON carrying `schema_version` and the evaluated commit. Exit codes:
  0 compliant or nothing measured, 1 non-compliant, 2 usage or
  configuration error, 3 operational failure. Golden JSON for a fake
  repository; one test per exit code.
- [ ] 5.10 Workflow tests (time-skipping environment): disjoint pushes
  coalesce to the union; one unknown signal selects every control; a
  parked repository is never read; a deferral writes no posture;
  ContinueAsNew history stays bounded under maximum-size signals (the
  bound the IMPL-0028 spike measured).
- [ ] 5.11 Change-detection tables (activity plus store): status change,
  file blob change, API value change with unchanged statuses, revision
  change and a first evaluation bump the generation; a cosmetic title
  edit and an unrelated push do not; unselected controls keep their
  `evaluated_sha`.
- [ ] 5.12 `TestIntegration_OneEvaluation` (`-tags integration`):
  `temporaltest`, the controls chain in `pgtest` and the GitHub fake run
  discovery → resolution → evaluation → recorded results for a fixture
  org, and capture an `EvaluationWorkflow` history into
  `internal/workflows/testdata/histories/evaluation_first_run.json`,
  replayed in CI with the existing fixtures.
- [ ] 5.13 Non-vacuous check: make coalescing keep only the last signal
  and make the push filter ignore `Reads()`; confirm the coalescing test
  and the `catalog-info.yaml` selects `custom_properties` test fail;
  restore.

#### Success Criteria

- Discovery, resolution, evaluation and recording run end to end in the
  integration test, against the controls chain, as `rg_evaluator`.
- The evaluation history replays in CI.
- Nothing is registered on a worker yet; the rc binary is unchanged.

### Phase 6: Posture, metrics, generated monitoring and the report

#### Tasks

- [ ] 6.1 Metrics in `internal/metrics/metrics.go`: `evaluations_total{org,
  outcome}`, `evaluation_errors_total{org, control}`,
  `evaluation_changes_total{org, control}`, `results_discarded_total{org,
  reason}`, `evaluation_freshness_seconds{org}` (histogram of the
  previous `evaluated_at`'s age), `store_pool_wait_seconds{role}`, and
  the gauges `controls_status{org, control, bucket}` and
  `controls_coverage{org, control}`; producers wired in 5.3 and 5.4.
  `installation_info` gains `app`. No metric carries a repository label.
- [ ] 6.2 `repo_guardian_deployment_info{topology, version}`, constant 1,
  set once at startup from the role configuration.
- [ ] 6.3 Posture exporter (OQ8) over 4.7's query: read before reset; on
  a read error keep the previous values; publish nothing when not
  configured to export. Tests for each, and for a vanished org dropping
  its series.
- [ ] 6.4 `monitoring.Derive(snap *policy.Snapshot, opts Options)`,
  keyed on control id; mechanisms derived from the snapshot (for
  example `custom_properties` with `apply = "workflow"`).
- [ ] 6.5 Dashboards (IMPL-0025 17.1, adapted): E1 and E2 rebuilt over
  `controls_status` and `controls_coverage` with `max by` inside and
  `sum` outside, and the "no data when nothing is assigned" guard; E3
  rebuilt around otelhttp, otelpgx, the Temporal SDK series,
  `evaluations_total`, the budget and rate series, discovery, and an API
  row; E4 trimmed and its matchers re-cut to log lines emitted by
  `internal/activities` and `internal/workflows`, with
  `TestLogLines_AreStillEmittedByTheBinary` passing and
  `TestSuite_PostureQueriesDedupeAcrossReplicas` re-pointed.
- [ ] 6.6 Alert catalogue (IMPL-0025 17.2, adapted): apply IMPL-0025's
  audit lists, drop alerts on metrics the switch-over deletes, add
  evaluation alerts (errors by control, stale-epoch discards, freshness).
  Before committing, confirm every Temporal metric name against the
  homelab's real `/metrics` output and record the check here. Update
  `TestCatalogue_RareEventAlertsCatchTheFirstIncrement`. — *deferred:
  human required (the metric-name check)*
- [ ] 6.7 Chart mirror (IMPL-0025 17.9): `prometheusrule.yaml` carries
  the catalogue; `make lint-alerts-chart` stays green; helm-unittest
  locks the selectors, not just the names.
- [ ] 6.8 `make monitoring-generate`; commit `contrib/generated/`;
  `make lint-monitoring` green.
- [ ] 6.9 `internal/report` over control results: per-org sections keyed
  by control with buckets, coverage, baseline versus org-added, and
  exclusions; the shared percent definition with "no data"; goldens
  rewritten (no data, pending-heavy, stale, unmeasurable, a replaced
  version, exclusions, two orgs, escaped cells), and the findings
  remediation enums and the `foreign_pr` golden removed.
- [ ] 6.10 Non-vacuous check: reverse `max by`/`sum` nesting in one E1
  panel, reset before read in the exporter, and remove the report's NULL
  guard; confirm the dedupe test, the exporter test and the no-data
  golden fail; restore.

#### Success Criteria

- `make lint-monitoring`, `make lint-alerts` and the monitoring tests are
  green, with Temporal metric names checked against live output.
- No posture query can read 100% on an empty fleet.

### Phase 7: Switch-over

The phase that deletes. It lands inside the plan's one PR (OQ6), so `v2` never holds a
half-switched binary.

#### Tasks

- [ ] 7.1 The evaluator role registers `EvaluationWorkflow`,
  `ControlsDiscoveryWorkflow`, the controls rollout and snapshot
  workflows and their activities on `repo-guardian-eval`;
  `RepoWorkflow`, the old discovery, snapshot, rollout and bootstrap
  workflows and `CheckRepo` are unregistered and deleted, with
  `check_then_park.json`. The replay suite still holds
  `installation_grant_report.json` and `evaluation_first_run.json`.
- [ ] 7.2 Retire the parity suite scenario by scenario: list every
  scenario in `internal/checker/parity_test.go` and
  `internal/store/postgres/parity_integration_test.go` in a table in
  this task, each with the test that replaces it (a resolution golden, a
  control fixture, a store integration test) or "retired: v1 behaviour,
  no replacement" with the reason. Delete only after the table is
  complete. `TestPRIdentity_IsFrozen` (checker and reconciler) is retired
  deliberately; `tools/rgctl` keeps its own literals.
- [ ] 7.3 Delete `internal/checker`, `internal/reconciler`,
  `internal/rulepolicy`, `internal/findings`, `internal/shadow`,
  `cmd/repo-guardian/verify_shadow.go`, and `internal/rules` except
  whatever embedded fallback templates the template spike left there.
- [ ] 7.4 Re-cut the findings importers: `internal/store` (`findings.go`,
  `api_views.go`, `util.go`, `v2.go`), `internal/store/postgres`
  (`v2store*.go`, `apireader*.go`, `report.go`), `internal/activities`
  and `internal/report` onto `store.Controls`.
- [ ] 7.5 Remove the rc schema code: `migrations_v2/`, the rc goose
  wiring, `adopt_v1.go`, `backfill_v1.go`, `dry_run.go`, `CheckV1Idle`,
  `pgtest/v1sql`, `migrations/` (golang-migrate), `queries/` and
  `sqlcdb/`, and the rc block of `sqlc.yaml`. `repo-guardian migrate`
  applies only the controls chain.
- [ ] 7.6 API: remove `/rules`, `/rules/{kind}/{name}`, `/findings`,
  `/policy`, `/installations`, `/orgs/{org}`, `/compliance/history`,
  `/summary` and the `Repository` fields `installation_id`,
  `last_check_outcome` and `policy_version` from `api/openapi.yaml`, with
  their handlers and queries; `/me`, `/status` and `/openapi.yaml` stay.
  `make generate-api`. Phase 8 adds the D32 replacements.
- [ ] 7.7 UI: remove the Findings, Rules, Orgs, History, Policy and
  Repository views and the code only they use; the Status view and the
  shell stay; `make generate-ui-api`, `make lint-ui`, `make test-ui` and
  the e2e suite (reduced to status and auth) green.
- [ ] 7.8 Configuration: `GUARDIAN_CONFIG` names the policy directory;
  `EVAL_INTERVAL` replaces `CHECK_INTERVAL`; audit every env var in
  `_helpers.tpl`'s list that only the rule engine read (at least
  `DRY_RUN`, `SKIP_FORKS`, `SKIP_ARCHIVED`, `AUTO_CLOSE_PR`,
  `ORPHAN_CLEANUP`, `TEMPLATE_DIR`, `RECONCILE_FRESHNESS`,
  `PR_STALE_AFTER`) and give each a fate in this task: kept, renamed, or
  removed with IMPL-0028's warn-and-ignore pattern.
- [ ] 7.9 Chart (OQ11): `policy.files` renders the policy directory into
  one ConfigMap mounted at `GUARDIAN_CONFIG` on the ingest and evaluator
  roles (ingest needs `WatchedPaths()`); `policy.existingConfigMap` stays;
  `templates.*`, `policy.config` and the rule-engine knobs are rejected
  by name through `values.schema.json` and `validateRemovedValues` with
  the migration URL; helm-unittest for rendering, mounts and each removed
  key; namespace check passes.
- [ ] 7.10 Delete `examples/guardian-*.hcl` and the `guardian-multi-org/`
  directory; `examples/values-*.yaml` move to the policy map.
- [ ] 7.11 Non-vacuous check: re-register `RepoWorkflow` and confirm the
  replay and registration tests notice; set a removed chart key and
  confirm render fails; restore.

#### Success Criteria

- `make ci`, `make helm-test` and `make lint-ui` are green with no
  reference to `findings`, `checker`, `reconciler` or `rulepolicy` left
  (`grep -r` in this task's PR description).
- `repo-guardian migrate` on an empty database applies the controls
  chain, and the evaluator role evaluates the homelab-shaped fixture org
  in the integration test.
- Every parity scenario has a named replacement or a recorded reason.

### Phase 8: API and UI (D32)

#### Tasks

- [ ] 8.1 Spec: add `/controls`, `/controls/{id}`, `/policies`,
  `/orgs`, `/orgs/{org}` (baseline versus org with coverage, worst
  controls, exclusions), `/repositories` (with `policy_state`,
  `policy_reason`, `evaluable`, `remediable`), `/repositories/{id}`,
  `/repositories/{id}/controls`, `/repositories/{id}/events` (assignment
  and result events; remediation events join in IMPL-0030),
  `/repositories/{id}/evaluations`, `/compliance/history` keyed by
  control, `/summary`, `POST /repositories/{id}/evaluate`. Every
  compliance object carries the buckets, `coverage` and a nullable
  percent; evidence is a `oneOf` discriminated by `evidence_kind`
  (`<type>/<kind>`) and `evidence_version`; per-org breakdowns carry
  `version` and `revision`.
- [ ] 8.2 Queries in `queries_controls/api_*.sql`, each with the scope
  predicate; `TestAPIQueries_AreScoped` scans the new directory.
- [ ] 8.3 Handlers in `internal/api` (`controls.go`, `policies.go`, and
  rewrites of `resources.go` and `compliance.go`). `/policies` filters
  the stored `Summarize()` output in Go to the principal's visible orgs,
  with enterprise-wide fields only for a principal that sees every org;
  `TestPoliciesSummary_IsScoped` asserts a single-org principal sees no
  other org.
- [ ] 8.4 `POST /repositories/{id}/evaluate`: visibility through
  `APIScope` first; `recheck` at priority 1 to `evaluation/<id>`; `202`
  with no body; `409 workflow_missing`, `409 parked`; `501` without
  `TEMPORAL_ADDRESS`; dedupe per OQ12. The UI BFF proxies this one
  `POST` with the Origin check it applies to logout
  (`ui/server/proxy.ts`).
- [ ] 8.5 `/status`: both queues' backlog through `DescribeTaskQueue`,
  each App's installations from `app_installations`, `topology`,
  evaluation freshness and coverage.
- [ ] 8.6 `apitest` with kin-openapi response validation for every
  endpoint; `make generate-api` and `make lint-api` green.
- [ ] 8.7 UI views: Controls, Control (per-org breakdown with versions),
  Orgs and Org (baseline versus org, coverage), Fleet linking to
  controls, Repository with the controls tab, evidence and the
  evaluation log, History by control, Policy on `/policies`, Status with
  queues, Apps and topology; the re-evaluate button hidden on `501`; a
  null percent renders "no data". `make generate-ui-api`, `make lint-ui`,
  `make test-ui`.
- [ ] 8.8 E2E: `ui/e2e/seed.sql` rewritten for the controls schema; a
  Playwright spec per view; `make test-ui-e2e` green.
- [ ] 8.9 Homelab Keycloak (IMPL-0025 19.2): register the UI client and
  the `repo-guardian-api` audience, add a groups mapper, create two
  groups mapped to different orgs; verify each group sees different orgs
  in `/controls`, `/orgs` and `/policies`, a machine client can read
  `/controls`, and `/status` works anonymously. — *deferred: human
  required*
- [ ] 8.10 Non-vacuous check: drop the scope predicate from one query and
  the Go filter from `/policies`; confirm both scope tests fail; restore.

#### Success Criteria

- Every D32 row is implemented or explicitly left to IMPL-0030
  (`/remediations`, remediation fields).
- Both scope tests pass and fail without the predicate.
- 8.9 is checked off before this plan closes.

### Phase 9: Documentation, the evaluate-only rc and the homelab run

#### Tasks

- [ ] 9.1 Rewrite `docs/operations/v2-overview.md`: policy compatibility
  claims, one PR per control (`repo-guardian/<slug>`, `max_open_prs`),
  `search_terms` and foreign-PR detection dropped, check modes replaced
  by typed controls, `setting` → `repo_settings`, `branch_protection` →
  `branch_ruleset`, reconcilers → controls, the status model, workflow
  names, `CHECK_INTERVAL` → `EVAL_INTERVAL`, roles.
- [ ] 9.2 Rewrite `docs/operations/v2-onboarding.md`: the Evaluation and
  Remediation Apps (permissions and events tables), `worker` →
  `evaluator`/`remediator`, `guardian.hcl` → the policy directory and the
  chart's policy map, `dryRun` → evaluate/remediate mode, scope →
  `enterprise.orgs` and `exclude`, workflow and schedule names
  (`evaluation/<id>`, `controls-*`), the API endpoint replacements,
  `EVAL_INTERVAL`. Keep IMPL-0028's sections where they already landed.
- [ ] 9.3 Policy reference: `docs/usage/policy-reference.md` rewritten
  for the directory layout, resolution and validation, with a "From
  `guardian.hcl`" section mapping each v1 rule kind to its control type
  (DESIGN-0029 D1); link the generated `controls-reference.md`;
  `mkdocs.yml` nav.
- [ ] 9.4 CLAUDE.md (`v2` branch): the framework invariants (`Validate`
  before `StatusOf`, `Owns` versus `Reads`, revision goldens and
  `Semantics`), the epoch fence, the park-reason result rule, the
  posture `max by` rule for `controls_status`, and the switch-over facts
  a later edit must not break.
- [ ] 9.5 `Chart.yaml` `2.0.0-rc.N` and appVersion `2.0.0-rc.N` (the
  next free rc), helm-unittest pins, both CHANGELOGs through git-cliff,
  `README.md.gotmpl` then `make helm-docs`.
- [ ] 9.6 PR to `v2` with `dont-release` (Rule 6); after merge, tag
  `v2.0.0-rc.N`, dispatch `ghcr.yml`, and verify assets, cosign
  signatures, SLSA provenance, and that `latest` did not move.
- [ ] 9.7 Homelab (OQ4): install the rc fresh into the dev namespace
  against a new database, Evaluation App only, every org in
  `enterprise.orgs`, every assignment `evaluate`; confirm both worker
  deployments behave as IMPL-0028 set them up and the evaluator promotes
  at first start. — *deferred: human required*
- [ ] 9.8 Homelab checks: compare per-org posture with prod v1 through
  the 9.3 mapping and record each difference with its cause; edit a
  `catalog-info.yaml` and see `catalog_info` and `custom_properties`
  re-evaluated; press re-evaluate; archive a repository and see its
  results clear; deploy policy A, B, then A again and see resolution
  follow A. — *deferred: human required*
- [ ] 9.9 docz: `docz update` for the statuses (DESIGN-0030 and
  DESIGN-0031 Implemented; DESIGN-0029 and DESIGN-0032 stay until
  IMPL-0030; IMPL-0029 Completed once every deferred task is checked).

#### Verification results

<!-- Filled in by 6.6, 9.7 and 9.8. -->

#### Success Criteria

- The rc is published like rc.4 (assets, cosign, provenance, `latest`
  unchanged).
- The homelab rc evaluates every org beside prod v1 with no GitHub write
  (the Evaluation App has no write permission), and every posture
  difference from v1 is explained.
- Every deferred task is checked off.

## File Changes

| File | Change |
| --- | --- |
| `internal/control/*.go` (new) | types, resource, validate, revision, fingerprint, changeset, reader, file, registry |
| `internal/control/controltest/` (new) | conformance suite, `FakeRepo`, `Apply` |
| `internal/controls/{codeowners,file,catalog_info,dependency_updates,repo_settings,branch_ruleset,labels,custom_properties}/` (new) | the eight built-in types with fixtures and goldens |
| `internal/catalog/` | node tree and spans |
| `internal/policy/` (new, after the 2.1 rename) | load, catalogue, enterprise, org, validate, ownership, resolve, snapshot, `testdata/resolution/` |
| `internal/rulepolicy/` | 2.1 rename; deleted in 7.3 |
| `internal/store/store.go`, `controls.go` (new), `api_views.go` | `store.Controls`, `CompliantPercent` |
| `internal/store/postgres/migrations_controls/0000{2,3,4}_*.sql` (new) | schema |
| `internal/store/postgres/queries_controls/` (new), `controlsdb/` (generated) | queries |
| `internal/store/postgres/controls*.go` (new) | the store implementation |
| `internal/store/postgres/{migrations_v2,migrations,queries,sqlcdb,pgtest/v1sql}/`, `adopt_v1.go`, `backfill_v1.go`, `dry_run.go`, `v2store*.go` | deleted or re-cut in 7.4–7.5 |
| `sqlc.yaml` | controls block (4.4); rc block removed (7.5) |
| `internal/workflows/{evaluation,controls_discovery,controls_service}.go` (new), `names.go`, `types.go` | workflows, ids, signals, `PriorityManual` |
| `internal/workflows/{repo,service}.go`, `testdata/histories/check_then_park.json` | deleted in 7.1 |
| `internal/workflows/testdata/histories/evaluation_first_run.json` (new) | replay fixture |
| `internal/activities/{evaluate,controls_record,controls_services}.go` (new), `check.go`, `record.go`, `route.go`, `services.go` | activities and routing |
| `internal/metrics/metrics.go` | evaluation metrics, posture gauges, `deployment_info`, `installation_info{app}` |
| `internal/monitoring/{derive,model,mechanism}.go`, `alert/alert.go`, `dashboard/e{1,2,3,4}.go` and tests | re-pointed generator |
| `contrib/generated/` | regenerated |
| `internal/report/` | over control results; goldens |
| `internal/{checker,reconciler,findings,shadow}/`, `internal/rules/` (most) | deleted in 7.3 |
| `cmd/repo-guardian/{roles,migrate,report,monitoring,services}.go`, `evaluate.go` (new), `controls_docs.go` (new), `verify_shadow.go` (deleted) | wiring and CLI |
| `api/openapi.yaml`, `internal/api/*`, `internal/api/gen/api.gen.go` | D32 |
| `ui/web/src/views/*`, `ui/web/src/lib/{evidence,viewmodel}.ts`, `ui/web/src/api/*`, `ui/server/proxy.ts`, `ui/e2e/*` | D32 views, POST proxy, e2e |
| `charts/repo-guardian/values.yaml`, `values.schema.json`, `templates/_helpers.tpl`, `templates/deployment.yaml`, policy ConfigMap template, `templates/prometheusrule.yaml`, `tests/*`, `README.md.gotmpl`, `README.md`, `Chart.yaml`, `CHANGELOG.md` | policy map, removed keys, alerts, rc |
| `examples/policy/*/` (new), `examples/examples_test.go`, `examples/guardian-*.hcl` (deleted) | examples |
| `Makefile` | `controls-docs`, `lint-controls-docs`; `lint-sql` over both blocks until 7.5 |
| `.golangci.yml` | depguard edges for `internal/policy` and `internal/controls/*` |
| `docs/operations/v2-overview.md`, `v2-onboarding.md`, `docs/usage/policy-reference.md`, `docs/usage/controls-reference.md` (generated), `mkdocs.yml` | docs |
| `CLAUDE.md` | v2-branch entries |

## Testing Plan

| Layer | What | Where |
| --- | --- | --- |
| Framework | status derivation, `Validate`, `ValidateChangeSet`, canonicalization (fuzz), revision, fingerprint, reader caching | `internal/control` (Phase 1) |
| Conformance | properties 2–6 for every type | `controltest` (1.10, Phases 1 and 3) |
| Per type | fixture tables, revision goldens, two rules failing together, parser round-trip fuzz | `internal/controls/*` (Phase 3) |
| Policy | resolution goldens, validation rows, symlink escape, example snapshots | `internal/policy`, `examples` (Phase 2) |
| Store | epoch fence, park result rule, revert, posture buckets, SQL/Go percent agreement | `internal/store/postgres` integration (Phase 4) |
| Grants | evaluator transactions as `rg_evaluator`; cross-writes fail as `rg_remediator` | `pgtest` (4.8) |
| Workflow | coalescing, unknown dominates, parked skip, deferral writes nothing, ContinueAsNew bound | time-skipping env (5.10) |
| Change detection | bump and no-bump table | 5.11 |
| End to end | discovery → resolution → evaluation → record | `TestIntegration_OneEvaluation` (5.12) |
| Replay | `evaluation_first_run.json`, `installation_grant_report.json` | CI replay suite |
| Monitoring | dedupe, rare-event guards, log-line contract, drift | monitoring tests, `make lint-monitoring`, `make lint-alerts` (Phase 6) |
| Report | goldens incl. no data | `internal/report` (6.9) |
| API | kin-openapi validation, `TestAPIQueries_AreScoped`, `TestPoliciesSummary_IsScoped`, POST status codes | `apitest` (Phase 8) |
| UI | unit, typecheck, Playwright | `make test-ui`, `make lint-ui`, `make test-ui-e2e` |
| Chart | policy map render, mounts, removed keys, alert selectors | helm-unittest, `make lint-alerts-chart` |
| Non-vacuous | one neutralization per phase | x.11–x.13 tasks |
| Live | posture beside v1, push selection, re-evaluate, park, revert, Keycloak scoping | homelab (8.9, 9.7, 9.8) |

## Dependencies

- **IMPL-0028 (Foundations) complete**, in particular: the v1 runtime
  deleted; `control.Reader`, `PRObserver` and `Writer` adapters, with the
  `Writer` in its own package; throttle classification per rate-limit
  bucket; both Apps and their webhook paths; `repo-guardian-eval` and
  the `evaluator` role on its own worker deployment; the budget workflow
  keyed `installation/<app>/<id>`; `00001_core`, the controls chain
  wiring and `pgtest` with the two application roles; and the phase-0
  spike results this plan cites (template sourcing, label case, history
  size, commit limits, Evaluation App permissions, policy revert).
- DESIGN-0029 to DESIGN-0032 as amended 2026-10-06 (INV-0022); every
  design question is decided.
- Phases 1 → 2 → 3 are sequential (the loader builds types; types reuse
  the framework). Phase 4 can start with Phase 2. Phase 5 needs 2–4.
  Phase 6 needs 5's metric producers. Phase 7 needs 1–6. Phase 8 needs
  7. Phase 9 needs all.
- IMPL-0030 starts after Phase 7 (it needs the switched binary and the
  `00003` tables) and can overlap Phases 8–9.
- Operator-owned for the homelab run: the Evaluation App installed on
  every org, a fresh Postgres database for the dev namespace, Keycloak.

## Open Questions

### OQ1: Where does the new policy package live while the rule engine still builds?

**Resolved 2026-10-07: (a).**

The old loader owns `internal/policy` until Phase 7, and the new one must
end up there (DESIGN-0031 D17 names it).

- (a) ✅ recommended: **rename the old package to `internal/rulepolicy`
  at the start of Phase 2** (one mechanical commit), build the new one in
  place, delete `rulepolicy` in Phase 7. No rename at the end, and
  depguard rules name the final path from day one.
- (b) Build the new one as `internal/controlpolicy` and rename it in
  Phase 7. The old code is untouched, but every new importer and depguard
  rule changes at switch-over.
- (c) Delete the rule engine first and build on an empty path. Simplest
  code, but the branch cannot run anything until Phase 7.
- other:

### OQ2: Do the control types implement Remediate in this plan?

**Resolved 2026-10-07: (a).**

`Remediate` writes nothing (it returns a `ChangeSet`), and the
conformance suite's properties 2–6 test it.

- (a) ✅ recommended: **yes, each type is built complete here**, so one
  package and one conformance run cover a type, and the load-time
  template check and `ValidateChangeSet` are exercised before IMPL-0030
  starts applying change sets. IMPL-0030 only applies them.
- (b) Build `Evaluate` here and stub `Remediate` with an error;
  IMPL-0030 fills it in. This plan is smaller, but every type is
  reopened and properties 2–6 wait.
- other:

### OQ3: Which control types must exist before the first controls rc?

**Resolved 2026-10-07: (a).**

DESIGN-0031 says the first evaluate-only rc needs only the types v1's
built-in defaults cover.

- (a) ✅ recommended: **all eight**, because the homelab comparison with
  prod v1 (9.8) covers the settings, branch protection, labels and
  custom properties v1 manages there, and a policy naming an unbuilt
  type fails load.
- (b) The four file types (`codeowners`, `file`, `catalog_info`,
  `dependency_updates`) gate the rc; the API types follow in a second rc
  from this plan.
- (c) The four file types here; the API types move to IMPL-0030.
- other:

### OQ4: Does this plan ship an evaluate-only rc, and where does it run?

**Resolved 2026-10-07: (a).**

- (a) ✅ recommended: **yes, and it replaces the homelab dev install**:
  a fresh install in the dev namespace against a new database, with the
  Evaluation App only, beside prod v1. It cannot write, so it can watch
  every org, and its numbers are compared with v1 before IMPL-0030
  turns anything on. The IMPL-0030 fresh install later replaces both.
- (b) No rc from this plan: evaluation and remediation ship together at
  the end of IMPL-0030. One fewer release, but posture is first compared
  with v1 only when writes are possible.
- (c) Tag an rc but do not deploy it.
- other:

### OQ5: What does the evaluate-only rc do with mode = "remediate"?

**Resolved 2026-10-07: (a).**

The rc has no remediator. Usually the Remediation App is absent too, so
resolution already sets `effective_mode = evaluate` with
`remediation_app_no_access`.

- (a) ✅ recommended: **load and resolve honestly; nothing consumes
  `remediation_due`.** Evaluation does not start remediations until
  IMPL-0030 adds the activity, and `/repositories/{id}/controls` shows
  requested and effective mode as resolved.
- (b) Fail policy load on `mode = "remediate"` in this release, and
  remove the check in IMPL-0030.
- other:

### OQ6: How is the work split into pull requests?

**Resolved 2026-10-07: (a).** The maintainer's direction: one PR per plan, so the agent does every task it can without waiting on a merge between phases. A task that needs the maintainer (homelab runs, GitHub App registration, approving a large `git rm`, release tags) is marked `deferred - human required` and done by the maintainer, during review or after the merge.

- (a) ✅ recommended: **one PR for the whole plan** into `v2`, with
  `dont-release`, as IMPL-0027 did. Phase 7 deletes and re-points inside
  it, so `v2` never holds a half-switched binary; the rc tag and the
  homelab runs in Phase 9 are `deferred - human required`.
- (b) One PR per phase, with Phase 7 as a single PR. Smaller reviews, but
  every phase waits on a merge.
- (c) Two PRs: build (Phases 1–6), then switch, API and release (7–9).
- other:

### OQ7: Does 00003_controls_results land whole in this plan?

**Resolved 2026-10-07: (a).**

DESIGN-0032 fixes the file split up front; `00003` holds the evaluation
tables and the remediation tables, views and grants.

- (a) ✅ recommended: **yes, the whole file verbatim from the design**,
  so the rc's schema is final, the grant matrix is tested once, and
  IMPL-0030 adds behaviour without a schema change. IMPL-0030 owns the
  `remediation_due` truth table and the remediator's grant tests.
- (b) Split it: this plan writes `00003_controls_results` with the
  evaluation tables; IMPL-0030 adds `00005_remediation`. That breaks the
  design's numbering and leaves evaluation's `remediations` column
  grants for later.
- other:

### OQ8: Which process exports the posture gauges?

**Resolved 2026-10-07: (a).**

v2 has no SETNX leader; the v1 exporter is deleted with the v1 runtime.

- (a) ✅ recommended: **every `api` replica, on a ticker, from its
  read-only DSN.** Every replica reads the same rows, so `max by`
  collapses them exactly; the api role runs continuously (the evaluator
  may scale to zero under KEDA). The gauges are absent when the api role
  is disabled, which the chart notes.
- (b) A Temporal schedule activity on whichever evaluator runs it. One
  pod holds fresh values and the others hold stale ones, which `max by`
  cannot tell apart.
- (c) Every evaluator pod on a ticker. Correct under `max by`, but
  nothing is published while the evaluator is scaled to zero.
- other:

### OQ9: How much of the UI does this plan rebuild?

**Resolved 2026-10-07: (a).**

- (a) ✅ recommended: **every view D32 affects** (8.7), leaving the
  Remediations view and the remediation fields of the Repository view to
  IMPL-0030. The homelab comparison (9.8) needs the controls views.
- (b) The API here and every UI view in IMPL-0030. Smaller plan, but the
  rc ships with only a Status view.
- (c) Controls, Repository and Status here; Orgs, History and Policy in
  IMPL-0030.
- other:

### OQ10: When is the monitoring tier regenerated?

**Resolved 2026-10-07: (a).**

- (a) ✅ recommended: **in Phase 6 of this plan, over evaluation**, so
  the rc ships with working E1–E4 and alerts and `lint-monitoring` stays
  meaningful; IMPL-0030 adds the remediation panels and alerts.
- (b) Once, in IMPL-0030, for both halves. Less churn in the generator,
  but the rc ships with E1/E2 removed and `Derive` stubbed.
- other:

### OQ11: How does the chart express a policy directory in a ConfigMap?

**Resolved 2026-10-07: (a).**

ConfigMap keys cannot contain `/`, and the policy root has
`catalogue/`, `orgs/` and `templates/`.

- (a) ✅ recommended: **`policy.files` is a nested map by directory**
  (`enterprise.hcl`, `catalogue: {codeowners.hcl: ...}`,
  `orgs: {...}`, `templates: {...}`); the template flattens keys
  (`catalogue.codeowners.hcl`) and mounts them back with `items[].path`.
  Values read like the directory, and the flattening is invisible.
- (b) Flat keys with a separator in values (`catalogue__codeowners.hcl`),
  mapped back with `items[].path`.
- (c) One ConfigMap per directory, or `existingConfigMap` only.
- other:

### OQ12: Where does POST /evaluate keep its one-per-minute dedupe?

**Resolved 2026-10-07: (a).**

The api role has a read-only DSN and no shared cache.

- (a) ✅ recommended: **in memory, per api replica**, a TTL map keyed by
  repository id. With N replicas a repository can be signalled at most N
  times a minute, and `EvaluationWorkflow` coalesces signals anyway.
- (b) In the workflow: the workflow ignores a manual `recheck` within a
  minute of the last one. Exact across replicas, but every request still
  reaches Temporal.
- (c) A dedupe table the api role may write. Exact, but it breaks the
  read-only DSN.
- other:

## References

- [DESIGN-0029](../design/0029-controls-and-policies-opinionated-compliance-with-separate.md) — controls and policies (D6, D7, D8, D9, D11)
- [DESIGN-0030](../design/0030-policy-model-enterprise-and-org-policies-and-control-assignment.md) — policy model and control assignment
- [DESIGN-0031](../design/0031-control-framework-controls-control-rules-and-file-controls.md) — control framework, rules and file controls
- [DESIGN-0032](../design/0032-evaluation-and-remediation-workflows-change-driven-remediation.md) — evaluation and remediation workflows (D27–D32)
- [INV-0021](../investigation/0021-controls-model-assumptions-a1-to-a23-against-the-v2-code.md) and [INV-0022](../investigation/0022-controls-designs-against-the-v2-code-github-and-temporal.md) — the design audits and the phase-0 spikes
- [IMPL-0025](0025-v2-findings-model-temporal-control-plane-read-only-api-and-ui.md) — tasks 17.1, 17.2, 17.9 and 19.2, moved here
- [IMPL-0026](0026-temporal-client-auth-credential-reload-keda-prometheus-trigger.md) — structure this plan follows
- [IMPL-0028](0028-controls-foundations-v1-runtime-removal-client-split-two-apps.md) — foundations, prerequisite
- [IMPL-0030](0030-controls-remediation-per-control-prs-remediation-workflow-and.md) — remediation, follows
- `docs/operations/v2-overview.md`, `v2-onboarding.md` — rewritten in Phase 9
