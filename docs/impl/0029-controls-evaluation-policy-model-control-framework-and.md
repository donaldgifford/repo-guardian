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

- [x] 1.1 Starting check: confirm the IMPL-0028 skeleton is on `v2`
  (`internal/control` compiles with the `Reader`, `PRObserver` and
  `Writer` interfaces and their value types; the `Writer` implementation
  sits in its own package; `PRConfig` and `PRTemplate` live in
  `internal/template`; the depguard edges of DESIGN-0031 D17 are in
  `.golangci.yml`). List anything missing in this task and finish it here
  before 1.2.
  Done 2026-10-16. Present: `Reader`/`PRObserver`/`Writer` and their
  value types in `internal/control` (`client.go`), the `Writer` in
  `internal/github/write`, the `writer` and `writer-evaluator-side`
  depguard rules. Missing and finished here: `PRConfig` and `PRTemplate`
  moved to `internal/template/pr.go` (aliases stay in the old
  `internal/policy` until Phase 7 deletes it); depguard rules
  `template-leaf`, `control-leaf`, `controls-types` and
  `controls-consumers` (lax mode, so the package's own allow wins over the
  module-wide deny), and `control`, `controls`, `policy` added to the
  `workflows` rule. Probe: an import of `store` in `control` and in a
  `controls/*` type, of `findings` in `template`, of a control type in
  `report`, and of `control` in `workflows` each failed lint; removed.
  (A blank-import probe does not trip depguard: use a named import.)
- [x] 1.2 Value types in `internal/control/types.go`, exactly as
  DESIGN-0031 § Supporting types: `Definition`, `BuildEnv`,
  `TemplateSource`, `ID` (with `String()` giving `slug@N` and `ParseID`),
  `Resource`, `RuleKind`, `RuleID`, `RuleKindSpec`, `ParamSpec`, `Rule`,
  `RuleStatus` and its five constants, `RuleResult`, `ReadState`,
  `ResourceRead`, `Evaluation`, `RepoContext`, `EvalInput`,
  `RemediationInput`, `FileChange`, `APIChange`, `ChangeSet`,
  `WorkflowApply`, and `Status` (`compliant`, `non_compliant`, `unknown`,
  `not_applicable`). One exported `ValidSlug` regexp
  (`^[a-z][a-z0-9]*([_-][a-z0-9]+)*$`) serves control slugs and rule ids.
  Done: also the `Type` and `Control` interfaces (Validate and the
  registry need them), `Resource.String`, the `Kind*` constants and
  `ChangeSet.IsRecommendation`; `FileChange` already existed in
  `client.go`. Status constants are `Status*` so they do not collide with
  the rule statuses. `template.Vars` is new: `Common` (`.Owner`, `.Repo`,
  `.DefaultBranch`) plus `.Org` and `.Catalog`, so the embedded templates
  render unchanged.
- [x] 1.3 `resource.go`: `Canonical(kind, key) (Resource, error)`. File
  keys go through `path.Clean` and are rejected when absolute, containing
  `..` or an empty segment, or starting with `./`, then compare byte for
  byte; `label:` keys are lower-cased; other kinds compare exactly;
  unknown kinds are an error. Table tests (`.github/./CODEOWNERS` equals
  `.github/CODEOWNERS`; `../x`, `/x`, `a//b` rejected; `Bug` equals
  `bug`) and `FuzzCanonicalFile` (canonical output is a fixed point, and
  never escapes the root).
  Done: `Canonical` and `MustCanonical` (for keys fixed in code); `.` is
  rejected as naming the root. `FuzzCanonicalFile` ran 15.8M inputs in
  20s with no failure.
- [x] 1.4 `validate.go`: `Validate(c, ev)` (one result per declared rule;
  a missing result becomes `error{reason=missing_result}`; a result for
  an undeclared id or a duplicate fails as a bug; closed status enum;
  evidence is scalars and arrays only, at most 16 KiB per rule, with
  `evidence_kind` and `evidence_version` present) and `StatusOf(ev)` in
  the table's order. Tests: every row of the status table, `unknown`
  included; each `Validate` failure mode, including nested and oversized
  evidence.
  Done: `Validate` returns results in declared rule order and wraps every
  bug in `ErrInvalidEvaluation`; "flat" evidence is scalars and arrays of
  scalars. `StatusOf` with no results is `unknown`, so an empty
  evaluation never reads as compliant. `FrameworkEvidence` writes the
  framework's own `missing_result`.
- [x] 1.5 `revision.go` and `fingerprint.go`: `Revision(t, def, env)` as
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
  Done, both in `revision.go`. Rule numbers and titles are display-only
  and excluded with the definition title and `pr {}`; type-level
  `Params` are included. A type whose definitions reference other store
  files implements `FileReferencer`, and those bytes are hashed too.
- [x] 1.6 `changeset.go`: `ValidateChangeSet(c, ev, cs)`: unique
  canonical paths; every file and API resource within `c.Owns()`; at most
  100 files and 1 MiB per file (or the limits the IMPL-0028 spike
  measured, as named constants citing it); regular files only, so a path
  the reader reported as a symlink or submodule must be in `Blocked`;
  every failing rule of `ev` in exactly one of `Fixed`, `Manual`,
  `Blocked`; every `Manual` and `Blocked` rule explained by a note.
  Table tests for each rejection.
  Done: `MaxChangeSetFiles` = 100 and `MaxFileBytes` = 1 MiB, citing
  spike 1. A symlink or submodule reaches the change set through the new
  `ErrNotRegularFile` sentinel, which the evaluation reader records as a
  read error with `Err = NotRegularFile`; writing that path is refused.
  Also refused: a rule named in two lists or undeclared, and a `Fixed`
  rule that is not failing. "Explained by a note" is at least one note per
  `Manual` and `Blocked` rule.
- [x] 1.7 `reader.go`: `NewEvalReader(base Reader, now func() time.Time)`
  wraps the per-repository adapter (already pinned to one commit by
  IMPL-0028's constructor), caches every read for the evaluation, and
  records a `ResourceRead` per resource (file digest = blob SHA, API
  digest = sha256 of canonical JSON, `ObservedAt` for API reads, absent
  reads recorded too). `Reads()` returns them sorted. Tests: two controls
  reading one file make one underlying call; an error read is recorded
  with `State = error`; a 403 on one endpoint surfaces as
  `ErrPermission` for the caller to map to `unknown{reason=permission}`.
  Done: failed reads are cached too, so every control sees one answer.
  Files key on `file:<canonical>`, listings on `tree:<path>` (`tree:.` for
  the root, absent when empty), settings on one `setting:<json name>` per
  field (nil = absent), and rulesets, properties and labels per item; a
  failed list is one `<kind>:*` error read. The org schema is cached but
  not recorded (org-level). `control.ErrPermission` is produced by the
  GitHub adapter (`classifyRead`: a 403 that is not a throttle), which
  also returns `ErrNotRegularFile` for a symlink or submodule.
- [x] 1.8 `file.go`: `FileSpec`, `LocateFile` (first location found
  wins; every location recorded, absent ones included), and the
  file-control adapter that turns a type's `Parse`, `Check`, `Fix` and
  `Render` into `Evaluate` and `Remediate` per DESIGN-0031's flowchart:
  absent → rules needing the file fail with `file_missing` and the
  template is rendered to `Canonical`; unparseable → `unknown{parse_error}`
  and every rule `Blocked` with a note; present → `Check` each rule, `Fix`
  the failing remediable ones at the found path, the rest `Manual`. A
  parser size cap is a required field of the adapter.
  Done: `NewFileControl[M](FileControlConfig[M])` over a generic
  `FileType[M]` (`Parse`, `Check`, `Fix` returning the ids it fixed,
  `Render`); it refuses a missing `MaxBytes`, an empty or non-canonical
  `FileSpec`, a `Canonical` outside `Locations`, and a remediable rule
  with no template. `LocateFile` reads every location; a read error at or
  above the found one is returned, one below it ignored. Beyond the
  flowchart: an oversized file is `unknown{file_too_large}`, a 403
  `unknown{permission}`, a symlink or submodule `unknown{not_regular_file}`
  and a transient read failure `error{read_error}`, all never written and
  every rule `Blocked`. A `*ParseError` puts its line in the evidence. A
  `Fix` that leaves the bytes unchanged fixes nothing (the rules go to
  `Manual`).
- [x] 1.9 `registry.go`: `Registry` with `Register`, `Type(name)` and
  `Build(def, env)`. `Build` fails on an unknown type, an unknown rule
  kind, a parameter not in `RuleKindSpec` or of the wrong type, a missing
  required parameter, a definition with no rules, `remediate = true` on a
  non-remediable kind, an `apply` value the type does not support
  (`workflow` without `WorkflowApply`), and a template whose render with
  the sample variables (org `example-org`, repository `example`) fails
  one of the control's remediable rules. Each error carries the
  definition's `slug@N` and rule id.
  Done: `NewRegistry`, `Register` (a duplicate name is an error), `Type`,
  `Build`. `Build` also refuses a rule id outside the slug grammar or
  declared twice, and an `apply` other than recommend/direct/workflow;
  an empty `Kind` defaults to the rule id. Parameter types accept what an
  HCL decode can produce (`[]any`/`[]string`, `map[string]any`/
  `map[string]string`, any numeric kind). The template check remediates
  an in-memory empty repository with `SampleVars`, applies the files and
  re-evaluates; it runs only when the definition names a template.
- [x] 1.10 `controltest`: `Run(t, typ, fixtures)` runs DESIGN-0031
  properties 2–6 (remediation property, idempotence, minimal edits,
  template passes its rules, two rules failing together) against a
  `FakeRepo` that implements `Reader` from a map of files and API values,
  and an `Apply(repo, cs)` helper that applies file changes and an API
  overlay. A notes-only change set passes the property and is asserted
  to be a recommendation.
  Done: `Fixture{Name, Def, Env, Repo, Vars, Want, WantFiles}`; `Run`
  builds through a `Registry`, so every fixture also passes `Build`'s
  checks. Property 4 is `WantFiles` byte-exact plus "no write that leaves
  a file unchanged"; property 6 fails the run when a type with two or more
  remediable kinds has no fixture failing two remediable rules at once.
  Idempotence means no writes and nothing in `Fixed` (a `Manual` rule
  keeps its note). `FakeRepo` serves `NotRegular` paths and per-read
  `Errors`; `Apply` overlays settings (by JSON name), properties, labels
  and rulesets. `control.OpSet`/`OpDelete` were added for `APIChange.Op`.
  Probe: a `Fix` that appends where it should prepend fails properties 2,
  3 and 4.
- [x] 1.11 `internal/controls/codeowners`: the type as DESIGN-0031's
  table (three locations, `exists` only, write the template to
  `.github/CODEOWNERS` when no location has a file, never edit an
  existing file), `Semantics() = 1`, the embedded fallback template
  sourced as the IMPL-0028 template spike decided, fixtures for no file,
  each location, a comment-only file and two files at once, the
  conformance suite, and a golden `Revision`.
  Done: the type looks its template up by name through
  `BuildEnv.Templates`, which Phase 2 builds as the policy root's
  `templates/` over the embedded `rules.TemplateStore` (spike 7); the
  catalogue definition names `codeowners` (the embedded store's name; 2.3's
  template source also accepts `codeowners.tmpl`). The test reads the shipped
  `internal/rules/templates/codeowners.tmpl` from disk (depguard keeps
  `rules` out of a control type), so the conformance suite and the golden
  cover the real fallback: editing it moves the golden, as D20 intends.
  Parser cap 3 MiB (GitHub ignores a larger CODEOWNERS). Fixtures: no
  file, each location, comment-only, two files with the first
  comment-only (fails: GitHub uses the first), two files both valid.
- [x] 1.12 Non-vacuous check: swap the `fail`/`unknown` order in
  `StatusOf`, drop the `Owns()` check from `ValidateChangeSet`, and make
  the reader skip its cache; confirm the matching tests fail; restore.
  Record the failing test names here.
  Done: `fail`/`unknown` swapped → `TestStatusOf/any_fail`,
  `TestStatusOf/fail_outranks_unknown`; `Owns()` check dropped →
  `TestValidateChangeSet/file_not_owned`; reader cache skipped →
  `TestEvalReader_SharedFileIsOneCall`,
  `TestEvalReader_AbsentAndErrorReadsAreRecorded`. All restored. Phase 1
  close-out: `internal/control` coverage 89.6%, `controltest` 72.4%,
  `codeowners` 86.2%; `go list` shows `internal/control`, `controltest`
  and `codeowners` import only `internal/control` and `internal/template`
  of this module.

#### Success Criteria

- `internal/control` imports only `internal/template` of this module,
  and depguard enforces every DESIGN-0031 D17 edge.
- `codeowners` passes the conformance suite and its revision golden.
- Coverage of `internal/control` is at least 85%; 1.12 shows the
  framework tests fail without the code they test.

### Phase 2: Policy model and resolution

#### Tasks

- [x] 2.1 Free the package path (OQ1): `git mv internal/policy
  internal/rulepolicy` and rewrite its importers' import paths, nothing
  else. `make ci` green. Phase 7 deletes `internal/rulepolicy`.
  Done: the package keeps its name `policy`, so importers carry an
  explicit `policy` alias (goimports adds it); comments and docs that
  name `internal/policy` are left alone.
- [x] 2.2 `internal/policy/load.go`: `Load(root string, reg
  *control.Registry, logger *slog.Logger) (*Snapshot, error)` over the
  DESIGN-0030 layout (`catalogue/*.hcl`, `templates/`, exactly one
  `enterprise.hcl`, `orgs/*.hcl`), using `hclparse.NewParser()`. Only
  literals and `file()` are allowed in expressions; every referenced file
  is resolved with `filepath.EvalSymlinks` and must stay under the root;
  each file is capped at 1 MiB. Errors carry `file:line`. No hot reload.
  Done: every file under the root is read once (walking `..data` when it
  exists and skipping `..*` names, per spike 7) and every one is
  containment-checked, so an escaping symlink fails load even when
  unreferenced. `file()` reads from that same in-memory set, so its bytes
  are hashed with the rest. "Literals and `file()` only" is enforced
  uniformly: every attribute of every HCL file is evaluated against an
  `EvalContext` holding `file` and no variables, before any decoder runs.
  Any HCL file outside `enterprise.hcl`, `catalogue/*.hcl` and
  `orgs/*.hcl` fails load.
- [x] 2.3 `catalogue.go`: decode each `control "<slug>"` block into a
  `control.Definition` (version, type, title, description, rules with
  `kind` defaulting to the id only when the id names a kind the type
  declares, template, `apply`, `pr {}`, type parameters), build it
  through the registry with a `BuildEnv` whose template source is the
  policy root's `templates/` plus the embedded fallbacks, and store its
  revision. Duplicate `slug@version` and slug or rule-id grammar
  violations fail load.
  Done: a `control` block's known attributes are `version`, `type`,
  `title`, `description`, `template`; any other attribute is a type
  parameter (`Definition.Params`), and in a `rule` block any attribute
  beyond `number`/`title`/`kind`/`remediate` is a rule parameter; unknown
  blocks fail. Values convert to the shapes `ParamSpec` checks (int64,
  float64, `[]any`, `map[string]any`). `control.Definition` gained a
  display-only `Description`. A template name resolves to
  `templates/<name>.tmpl`, then `templates/<name>`, then the embedded
  `rules.TemplateStore` entry, and the embedded names used are kept for
  2.9's version hash. `pr {}` titles and bodies are compiled at load.
  Registry errors are prefixed with the control block's `file:line`.
- [x] 2.4 `enterprise.go` and `org.go`: decode `enterprise.hcl` (`orgs`,
  `mode`, `controls`, `remediation { max_open_prs, reopen_after }`, and a
  load error naming the block if an exclusion appears) and each
  `orgs/<org>.hcl` (`mode`, `controls`, `replace "x@1" { with, reason }`,
  `exclude "x@1" { reason }`, `control "x@N" { mode }`,
  `exclude_repos { match, reason }`, labelled `repos` blocks,
  `remediation`). An org file for an org not in `enterprise.orgs` fails
  load.
  Done: org names compare case-insensitively (GitHub logins), so `ACME`
  and `acme` are one org: a second policy for it fails, naming the first.
  An org file may hold more than one `org` block. `controls`, `replace`
  and `exclude` are kept as one operation list per layer, sorted by
  source position so 2.7 can apply them in file order (a `controls` list
  keeps its own order). Values (`mode`, `max_open_prs`, `reopen_after`,
  reasons) are decoded raw and checked in 2.5. The enterprise policy
  names each forbidden block (`exclude`, `replace`, `exclude_repos`,
  `repos`, `control`) in its error.
- [x] 2.5 `validate.go`: every error and warning in DESIGN-0030's
  validation list, one table-test row each, asserting the message and the
  source location. Warnings go to the logger once at load.
  Done: errors are joined so one load reports all of them. A `replace` in
  a `repos` block is checked against the org layer's result plus every
  earlier block's additions, since which earlier blocks a repository
  meets is unknown at load. The ownership-collision rows are 2.6's
  (warnings, per this plan). "A `repos` block matches no known
  repository" needs discovery, so it is `(*Snapshot).UnmatchedReposBlocks`
  for the discovery path to log rather than a load-time warning.
- [x] 2.6 `ownership.go`: the conservative load-time pairwise `Owns()`
  enumeration over every pair of definitions any org could assign
  together; overlaps are warnings naming both definitions and the
  resource, never errors (the runtime check in 2.7 is the authority).
  Done: per org, the layers are actually applied (org alone, org plus
  each `repos` block, org plus each pair of blocks in file order), so a
  `replace` removes its source and never warns against its replacement;
  an onboarded org with no file is checked against the baseline. Each
  (org, pair, resource) warns once, naming the org, and only when the
  policy has no errors.
- [x] 2.7 `resolve.go`: `(*Snapshot).Resolve(repo Repository, access
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
  Done: `AppAccess{Evaluable, Remediable, RemediationSuspended}`: the
  third field picks `remediation_app_suspended` over `_no_access`, which
  two booleans cannot tell apart. The checks run in the diagram's order:
  unmanaged, then `exclude_repos`, then layers; a parked repository keeps
  its assignments, with `reason` the stored park reason (else `archived`
  or `fork`). `mode_source` is the layer's source string, `default` when
  nothing declares a mode. Excluded rows carry their would-be modes. A
  conflict error names every claimant (`a and b`, `a, b and c`). Org
  names and `match` globs compare case-insensitively.
- [x] 2.8 Resolution goldens: `internal/policy/testdata/resolution/<case>/`
  each holding a policy directory, a `repo.json` input and an
  `expected.json`, run by one table test with `-update`. Cases at least:
  baseline only; org addition; `replace` to `@2`; `exclude` with reason;
  `repos` blocks matching in file order; `exclude_repos`; mode by
  specificity (enterprise, org, `control` block, `repos` block);
  Remediation App absent and suspended; an ownership conflict; archived,
  fork and parked repositories; an org outside `enterprise.orgs`.
  Done: 23 cases under `testdata/resolution/`, the harness adding the
  shared test catalogue to each `policy/` directory; `repo.json` is
  `{repository, access}`. Beyond the list: both orders of the worked
  example, a cross-slug replace, exclude-then-re-add of an older version,
  a specific early block beating a later general one, and org-over-
  enterprise remediation settings. `control.ID` gained `MarshalText`/
  `UnmarshalText` so goldens read `"codeowners@2"`. `reopen_after` is in
  nanoseconds (`time.Duration`).
- [x] 2.9 `snapshot.go`: `Snapshot` with `Version()` (a hash over every
  loaded file, the template bytes and the embedded fallbacks actually
  used, so a template edit is a policy change), `Control(id)`,
  `Definition(id)`, `Controls()`, `WatchedPaths()` (the union of every
  definition's `file:` keys in `Owns() ∪ Reads()`, which ingest uses as
  its push filter), and `Summarize()` for `/policies` and
  `policy_versions`.
  Done: `Version()` is `controls:` + sha256 over the files, the embedded
  templates used and every definition's revision (so a type `Semantics`
  bump is also a policy change). `Definition(id)` returns `(def, ok)`,
  and `Revision(id)` was added for the store. `Summarize()` returns a
  policy-package `Summary` (enterprise, per-org adds/replaces/excludes
  with reasons and block labels, `exclude_repos`, repos blocks, catalogue
  IDs/titles/types/revisions), with no template bodies, PR text or
  params. Phase 8's `/policies` filters it.
- [x] 2.10 Example policies: write `examples/policy/{minimal,full,multi-org,renovate,enterprise}/`
  as directories covering what the five v1 examples covered, and rewrite
  `examples/examples_test.go` to load each and compare `Summarize()`
  against a committed golden. The v1 `guardian-*.hcl` files stay until
  Phase 7, because rule-engine tests still read them.
  Deferred to after 3.9: the five v1 examples cover dependency updates,
  settings, rulesets, labels and custom properties, whose types land in
  Phase 3, and only `codeowners` exists until then. Done right after
  Phase 3's types, before 3.10.
  Done (after 3.9): five roots under `examples/policy/` (`enterprise.hcl`,
  `catalogue/`, `orgs/`, and a `templates/` in `enterprise` for a
  policy-root template); `full` uses every built-in type once. The new
  `examples_test.go` loads each through `policy.Load` with
  `builtin.Registry()`, fails on any load warning, and compares
  `Summarize()` with `examples/policy/testdata/<name>.summary.json`
  (`-update` rewrites). The v1 tests moved to `examples_v1_test.go`
  with the `guardian-*.hcl` files until Phase 7. New
  `internal/controls/builtin` (`Types()`, `Registry()`) is the one
  package importing every type, exempt from `controls-types`;
  `controls-consumers` now admits `examples/*_test.go`. Probe: `apply =
  "workflow"` on the labels example fails the load.
- [x] 2.11 Non-vacuous check: reverse the specificity order and drop the
  `EvalSymlinks` containment check; confirm the goldens and the
  symlink-escape test fail; restore.
  Done: preferring org-level modes over repos blocks failed
  `mode-org-control` and `mode-repos-block`; dropping the containment
  check failed `TestLoad_SymlinkEscapeIsRefused`; both restored. Probing
  each step found two unpinned behaviours, now covered by goldens:
  exclude-beats-add within one layer (`exclude-beats-add-in-layer`) and
  a baseline slug re-added after a cross-slug replace
  (`baseline-readd-after-cross-slug-replace`). Each remaining step
  probe (no baseline set: 26 goldens; no replace lineage: 2; ignoring
  access: 2; no conflict marking: 1) fails at least one golden.

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

- [x] 3.1 `internal/controls/file`: parameters `path`, `template`,
  `mode` (`exists` or `exact`, never both); `exact` compares YAML
  semantically for `.yml`/`.yaml` and bytes otherwise; owns and reads
  `file:<path>`.
  Done: a definition holds exactly one rule, whose kind is the mode; a
  `mode` parameter, when set, must match it. `exact` always needs the
  template (it is the comparison), remediable or not. The type implements
  `control.Control` directly rather than through `NewFileControl`,
  because `exact` renders per repository and `FileType.Check` has no
  `Vars`. A present file that is not YAML fails `exact` and is rewritten;
  a template that renders to invalid YAML is `error`. controltest gained
  `OneRule`: a type with one rule per definition is exempt from property
  6, and every fixture must declare one rule.
- [x] 3.2 `internal/catalog`: keep the YAML node tree and source spans
  beside the parsed `Properties`, with a size cap, so a type can edit
  minimally; the existing `Parse` callers keep working until Phase 7.
  Done: `ParseDocument` (cap `MaxBytes` 1 MiB → `ErrTooLarge`; YAML
  errors and a non-mapping root → `*ParseError` with the line) returns a
  `Document` holding the source, the yaml.v3 node tree and the decoded
  `Entity`. `Lookup(path...)` returns a scalar with its `Span` (line,
  rune column, byte range, `Editable` for single-line plain and quoted
  scalars only), and `Replace(span, text)` edits the bytes in place.
  `Parse` is unchanged and shares `properties()`. depguard: control
  types may import `internal/catalog` (a new `catalog-leaf` rule keeps it
  importing nothing of ours). That edge is beyond DESIGN-0031 D17's
  diagram, which this task implies.
- [x] 3.3 `internal/controls/catalog_info`: two locations; rule kinds
  `exists` and `kind` (remediable) and `field_set` with `contains`,
  `annotation_set` and `no_placeholders` (`remediate = false`); an absent
  file gets the template with field rules in `Manual`; a present file is
  never edited (every failing rule `Manual`, `kind` included); an
  unparseable file puts every rule in `Blocked`.
  Done: `field_set{field, contains?}` checks a dotted path to a
  non-empty scalar; `annotation_set{annotation}` reads
  `metadata.annotations` (its keys hold dots); `no_placeholders{fields?,
  placeholders?}` checks the named fields, else every scalar. A value is
  a placeholder when it equals a token or starts with one followed by
  `:` or a space (default tokens TODO, TBD, FIXME, CHANGEME, CHANGE_ME,
  REPLACE_ME, XXX), or is wrapped in `<…>`. So the shipped fallback
  template passes `exists`, `kind` and `field_set` but fails
  `no_placeholders`, which is the point. Evidence lists offenders as
  parallel `placeholder_fields`/`placeholder_lines` arrays (evidence is
  scalars or arrays of scalars). `control` gained the `Param*` type
  constants.
- [x] 3.4 `internal/controls/dependency_updates`: `tool` and
  `renovate.extends`; the Dependabot and Renovate locations owned and
  read; `package.json` read only; rule kinds `configured`, `extends` and
  `exclusive`; a node-preserving JSON span editor that adds the preset to
  `extends`; JSON5 gets a note, not an edit; deleting the other tool's
  config is in the same change set. Fixtures include `configured` plus
  `extends` failing together, and Dependabot plus a JSON5 Renovate config
  (partial: `exclusive` fixed, `extends` manual).
  Done: implements `control.Control` directly (several files, two
  tools). The preset is `renovate = { extends = "..." }` and is a
  template, so one catalogue entry serves every org. Locations are in
  Renovate's own read order. A `package.json` `renovate` key satisfies
  `configured`/`extends` for Renovate, fails `exclusive` for Dependabot,
  and is never edited (Manual). `extends` is evaluated on JSON5 too, via
  a normaliser (comments, single quotes, bare keys, trailing commas); a
  JSON5 failure is Manual (D5). The JSON span editor appends to the
  top-level list, matching a multi-line list's indentation, or inserts
  the key before the first one; `extends` that is not a list is Manual.
  Unknown/error rules go to Blocked. `TestPartialRemediation` pins the
  D14 example.
- [x] 3.5 `internal/controls/repo_settings`: `setting:<property>` owned
  per declared property; rule kind `equals`; fix is
  `APIChange{setting:<property>, set, value}`. A 403 on the
  vulnerability-alerts read is `unknown{reason=permission}`.
  Done: properties are the scalar fields of `control.RepositorySettings`
  by JSON name plus `vulnerability_alerts`. `equals{property, value}`
  checks the value's kind against the property at Build (`ParamAny` is
  new); a property is pinned once per definition, and `default_branch`
  may be checked but not remediated. Vulnerability alerts are their own
  endpoint, so `control.Reader` gained `GetVulnerabilityAlerts` (recorded
  as `setting:vulnerability_alerts`; implemented by `RepoReader`, a 404 is
  false and a 403 is `ErrPermission`) and `control.Writer` gained
  `SetVulnerabilityAlerts` (PUT/DELETE). A field the response lacks is
  `unknown{not_returned}`. controltest property 5 now starts from
  `EmptyRepository()` (GitHub's defaults for a new repository) rather
  than a zero `FakeRepo`, so API types are proven against real
  settings. v1's `vulnerability_alerts_enabled` is
  `vulnerability_alerts` here.
- [x] 3.6 `internal/controls/branch_ruleset`: owns `ruleset:<name>`;
  rule kinds `exists` and `matches` (YAML-semantic comparison of rules);
  a same-named ruleset whose `Source` is the organization or enterprise
  makes both rules `unknown{reason=inherited}` with a note and is never
  written.
  Done: one definition declares one ruleset by `name` plus
  `ruleset = { enforcement, target, conditions, rules, bypass_actors }`
  in the REST shape (enforcement defaults to `active`, target to
  `branch`; `rules` required; unknown fields refused). The declaration is
  a definition parameter, not a template: the registry's template check
  applies files only, so an API type cannot prove a template there.
  `matches` compares by value after a JSON round trip (HCL ints vs API
  floats): declared object keys must match, extra API defaults are
  ignored, lists match element by element, and `rules` are sorted by
  `type` on both sides first. One upsert by id fixes both rules; an
  inherited org or enterprise ruleset blocks both with a note, even
  beside a repository one of the same name. A remediable `exists` with a
  non-active enforcement fails Build. Golden `4daeb7c1…`.
- [x] 3.7 `internal/controls/labels`: owns `label:<name>` for managed and
  retired labels, compared lower-cased; rule kinds `present` and
  `absent`; case handling on create and update as the IMPL-0028 label
  spike found.
  Done: one rule per label: `present{name, color, description}` and
  `absent{name}`, each owning `label:<lower-cased name>`; the same name
  in any case twice fails Build, and colours must be six hex digits
  (compared case-insensitively, written lower-cased). A case-only
  difference fails `present` and is fixed by a set carrying the declared
  name, which `write.UpsertLabel` turns into a PATCH (spike 10), never a
  create; a retired label is `APIChange{delete}`. An omitted description
  is not checked and an update keeps the label's own. Golden
  `885023bb…`.
- [x] 3.8 `internal/controls/custom_properties`: properties with
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
  Done: definition params `properties = { <name> = { source, normalize,
  scope, key, pattern } }` (declares a property or overrides a built-in's
  facts; `scope` is an org list, `key` the rendered GitHub name defaulting
  to `<name>`, `normalize` one of `none`/`entity_ref`/`lower`) and v1's
  `annotation_properties` shorthand (source `annotation:<key>`). Built-in
  `Owner` is `spec.owner` with `entity_ref` (so `group:default/platform`
  and `platform` agree) and `Component` is `metadata.name`. A sourceless
  property never enters the managed set; duplicate rendered keys (case-
  insensitive), an annotation onto a built-in, a bad key, pattern or
  normalize fail Build. Rules may narrow to one key with `property`. An
  empty derived value clears. `matches` evidence carries `properties`,
  `sources`, `differs`, `withheld` and `withheld_gates` (`value` |
  `schema`); a withheld property blocks the rule while its siblings are
  still written. A schema read error closes the schema gate (v1 failed
  open; the controls model never lets unknown open a write). Deviations:
  `control.RepoContext` gained `AccountType` (`AccountUser` makes every
  rule `not_applicable{account_type=user}`, D10) for the activity layer
  to fill, and depguard `controls-types` now allows the `internal/metrics`
  leaf so `CatalogParseFailedTotal{org}` increments in Evaluate (not
  Remediate, so once per evaluation). The workflow template lives with
  the type (`workflow.yml.tmpl`), runs on a push to the repository's
  default branch whatever its name, and clears with `-F ...=null`.
  Golden `ed80fe4e…`.
- [x] 3.9 `apply` on the four API types: `recommend` (default),
  `direct`, and `workflow` only on `custom_properties`; the registry
  rejects the rest (covered by 1.9's error table, extended here).
  Done: a marker interface `control.APIType` (`RemediatesByAPI()`) on
  `repo_settings`, `branch_ruleset`, `labels` and `custom_properties`.
  `checkApply` refuses an unknown value first, then any apply on a type
  that is not an `APIType` ("remediates by pull request and takes no
  apply value"), then `workflow` without `WorkflowApply`. 1.9's table
  gained recommend/direct on an API type, workflow on an API type
  without `WorkflowApply`, and all three values on a file type;
  `TestCatalogue_Apply` covers the same through the loader (the
  codeowners decode fixture lost its `remediation {}` block).
- [x] 3.10 Parser fuzzing: `Render(Parse(x)) == x` for the catalog-info
  YAML and the Renovate JSON editors, plus a test at each parser's size
  cap.
  Done: `FuzzParseDocument_RoundTrip` (bytes round-trip, and a
  `metadata.name` replace touches nothing outside its span),
  `FuzzAddExtends` (valid JSON out, preset present, one contiguous
  insertion) and `FuzzNormaliseJSON5` (valid JSON keeps its value). Two
  real bugs: `addExtends` edited truncated JSON (now refused by
  `json.Valid`; the corpus entry is kept as a seed), and yaml.v3's struct
  decode checks duplicate keys pairwise, so a 50k-key catalog-info took
  >20s — `ParseDocument` now walks the node tree linearly
  (`TestParseDocument_WideMappingIsLinear`). Size caps: catalog
  `MaxBytes` / `MaxBytes+1`, Renovate `TestExtends_SizeCap` (judged at the
  cap, `unknown{file_too_large}` one byte over). The `file` type's YAML
  exact compare and v1 `catalog.Parse` keep the quadratic decode; the
  former carries a FIX note, the latter goes in Phase 7.
- [x] 3.11 Control-type reference generator: `repo-guardian controls
  docs` renders every registered type's `RuleKinds()` and parameters to
  `docs/usage/controls-reference.md`; `make controls-docs` writes it and
  `make lint-controls-docs` diffs it, wired into `make lint` like
  `lint-monitoring`.
  Done: `builtin.WriteReference` renders each type's semantics, how it
  remediates (API with its `apply` values, or pull request) and a rule
  kind table; `repo-guardian controls docs [--out FILE]` calls it. `make
  lint` now depends on `lint-controls-docs`, which renders into `build/`
  and `diff`s, so CI's lint job runs it; appending a line to the
  committed reference fails the gate. The page is in the mkdocs nav.
- [x] 3.12 Non-vacuous check: in each type, remove one `Fix` branch or
  one location; confirm the conformance suite or fixture table fails;
  restore. Record one line per type.
  Done (each restored with `git checkout`):
  - `branch_ruleset`: drop the ruleset `OpSet` → `TestConformance/absent` fails.
  - `catalog_info`: drop the `catalog-info.yml` location → `TestConformance/the_.yml_location` fails.
  - `codeowners`: drop `docs/CODEOWNERS` → `TestConformance/docs/CODEOWNERS` and `TestExistingFileNeverEdited` fail.
  - `custom_properties`: drop the property `OpSet` → the two clearing source-state fixtures fail.
  - `dependency_updates`: drop the other-tool delete → the dependabot-present and JSON5-partial fixtures fail.
  - `file`: drop the file write (body kept in a note so it compiles) → `TestConformance` fails.
  - `labels`: drop the label change → `TestConformance` fails from `compliant/template` on.
  - `repo_settings`: drop the setting `OpSet` → `TestConformance` fails from `compliant/template` on.

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

- [x] 4.1 `00002_controls_policy.sql`: `repository_policy_state` and
  `control_assignments` with the DESIGN-0030 and DESIGN-0032 columns
  (`epoch`, `revision`, `requested_mode`, `effective_mode`,
  `mode_reason`, `ordinal`, `state` including `conflict`, `error`,
  `baseline_control`, `decision`, `source_block`; PK `(repository_id,
  control_id)`), an epoch sequence, the `assignment_changed`
  `repository_events.kind`, and `app_installations` and
  `app_repository_access` with their row-level security policies (unless
  IMPL-0028 already created them, in which case this task records that).
  Done: IMPL-0028 had created neither access table, so 00002 does. Epochs
  come from `assignment_epoch_seq` (USAGE to `rg_evaluator`). Each access
  table pairs a `FOR SELECT ... USING (true)` policy with one write policy
  per App; `TestControlsChain_AccessRowsAreFencedByApp` pins that both
  roles see both Apps' rows and a cross-App delete affects zero rows, and
  the role test gained resolution, slot and refusal cases.
  `ControlsSchemaVersion` is 2 and the dry-run test derives `1..N` from it.
- [x] 4.2 `00003_controls_results.sql` (OQ7): `control_results`,
  `rule_results`, `result_events`, `remediations`, `remediation_steps`,
  `remediation_events`, the `remediation_due` and
  `remediation_maintenance` views, the append-only revokes, and the full
  DESIGN-0032 grant matrix to `rg_evaluator` and `rg_remediator`,
  verbatim from the design.
  Done: identity keys instead of the sketch's BIGSERIAL (no sequence
  grants). `remediations` carries RLS so the remediator's DELETE reaches
  only `reserved` rows, each write policy paired with the shared SELECT
  policy. The revokes are explicit though nothing granted them. The role
  test covers a result record, a reservation, PR observation and the
  refusals (acknowledging a head as the evaluator, writing evaluator
  columns as the remediator, rewriting either append-only table).
  `ControlsSchemaVersion` is 3.
- [x] 4.3 `00004_compliance.sql`: `compliance_snapshots` keyed `(org,
  control_id, decision, snapshot_at)` with the posture buckets, the
  excluded count and `coverage`.
  Done: unmeasurable is split by reason into three columns, and
  `assigned`, `percent` and `coverage` are stored beside the buckets
  (`NUMERIC(4,1)`, NULL on an empty denominator). The README and the
  `ControlsSchemaVersion` comment now say 00004 is IMPL-0029's (the IMPL
  moved it here from IMPL-0030). `ControlsSchemaVersion` is 4.
- [x] 4.4 sqlc: a second block in `sqlc.yaml` reading the controls chain
  and `internal/store/postgres/queries_controls/` into package
  `controlsdb`; `make generate-sql` and `make lint-sql` cover both blocks.
  Done: the second block writes `controlsdb` from `migrations_controls` +
  `queries_controls/`, with the same overrides as the first. `make
  lint-sql` rewrites every block's paths with `.sql[] |= ...` and diffs
  both packages. `ControlsStore`'s two policy-activation statements moved
  into `queries_controls/policy.sql`, so the block has real queries from
  the start.
- [x] 4.5 `store.Controls` interface and its Postgres implementation:
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
  - activation runs in the migrate Job, never at worker startup
    (INV-0022 Phase-0 OQ1 (a)): `repo-guardian migrate` loads the
    policy root, computes the controls version and calls IMPL-0028's
    `ActivatePolicyVersion`; workers read `CurrentActivation` and start
    the rollout for it. The rc's per-pod `RecordPolicyVersion` start
    path is deleted with the rc services.
  Done: `store.Controls` (`internal/store/controls.go`) is implemented by
  `ControlsStore` on sqlc queries in `queries_controls/{repositories,
  resolution,evaluation}.sql`. Identity is matched by provider id, then by
  name. `Resolve` re-reads the row and both Apps' access, and
  `UpsertRepositories` calls it inline. The epoch compare covers version,
  revision (which fixes the write set), both modes, source and state.
  `intent_revision` is computed at record time, because only the record
  step knows the generation. `results_discarded_total{org,reason}` is
  registered now and counted after the record's work commits, so a retry
  cannot count twice. `repo-guardian migrate --chain controls [--policy
  DIR]` loads the root with `builtin.Registry()` and activates its
  version; `TestMigrateControls_ActivatesThePolicy` covers first-seen and
  re-sync. `TestControlsStore_EvaluationLifecycle` and
  `TestControlsStore_DiscoveryUnparks` run as `rg_evaluator`. The rc's
  `RecordPolicyVersion` start path goes with the rc services in Phase 7.
- [x] 4.6 Park with the result rule: `archived`, `fork`, `removed` and
  `installation_removed` clear results (with `result_events`);
  `access_denied` and `unknown` keep them; Evaluation App suspension is
  read from `app_installations.suspended_at` and is never a park. A table
  test per reason asserts rows kept or cleared.
  Done: `ControlsStore.Park` takes its reason from `parkClears`. A clearing
  reason withdraws every result through the same `withdraw` path as
  resolution (a `to_status = NULL` event), while assignments stay as
  desired state. `suspended` is refused, because suspension is access
  state, which `accessOf` reads from `app_installations.suspended_at`.
  `TestControlsStore_ParkResultRule` has one row per reason, plus the
  suspension refusal.
- [x] 4.7 Compliance queries in `queries_controls/compliance.sql`,
  starting from active assignments with a LEFT JOIN to results at the
  matching epoch and revision: buckets `compliant`, `non_compliant`,
  `pending`, `stale`, `unmeasurable` (by reason), `not_applicable`,
  `unknown`, plus excluded; percent `compliant/(compliant+non_compliant)`
  floored to one decimal and NULL on an empty denominator; coverage over
  assigned; baseline, org-added and exclusions variants.
  `store.CompliantPercent` keeps the Go mirror, and a test feeds the same
  counts to both and asserts they agree.
  Done: `queries_controls/compliance.sql` has `ControlCompliance` (one
  bucket per assignment, grouped by `(org, control_id, decision)`; lineage
  filter `all`/`baseline`/`org_added`; an excluded assignment counts under
  the lineage it would have had) and `ControlExclusions`. The percentage
  is one SQL function, `floor_percent`, added to 00004 and used for both
  percent and coverage. `TestCompliantPercent_SQLAgreesWithGo` feeds
  `api/testdata/compliant_percent.json` to it and to
  `store.CompliantPercent`. Repositories parked `unknown` count as
  unmeasurable access loss, alongside `access_denied` (D6's fail-safe).
  `TestControlsStore_ComplianceBuckets` covers every bucket in one fleet,
  and `TestControlsStore_EmptyFleetIsNoData` covers no rows / NULL.
- [x] 4.8 Grant tests (`pgtest`, as the roles, never the owner): every
  transaction in 4.5–4.7 succeeds as `rg_evaluator`; as `rg_remediator`,
  writing any evaluator-owned column of `control_results`,
  `control_assignments` or `repository_policy_state` (except
  `last_remediation_at`) fails; nobody can UPDATE or DELETE
  `result_events`.
  Done: `TestControlsChain_RecordIsNarrow` walks every evaluator-owned
  column of the three tables. As `rg_remediator` each one is refused, and
  as `rg_evaluator` it is allowed. The remediator's own columns
  (`last_remediation_at`, `remediated_generation`, `hold`, `hold_since`)
  succeed, so the refusals are not vacuous. UPDATE, DELETE and TRUNCATE on
  `result_events` are refused as evaluator, remediator and `rg_all`. The
  4.5–4.7 transactions run as `rg_evaluator` in the `TestControlsStore_*`
  tests.
- [x] 4.9 Integration tests: epoch fencing (an assignment deleted and
  recreated during a record retry ends at generation 1); a policy revert
  A → B → A resolves under A again; the posture case (100 assigned, one
  result: 99 `pending`, coverage 1%, percent from one row); an empty
  fleet reads NULL, never 100%.
  Done (`controls_compliance_integration_test.go`, as `rg_evaluator`):
  - `TestControlsStore_EpochFencingAcrossRecreate`: a result staged before
    a withdraw-and-re-add is discarded on the retry, and the recreated
    assignment starts at generation 1.
  - `TestControlsStore_PolicyRevertResolvesAgain`: A → B → A activation
    leaves A current, and re-resolving returns A's revision.
  - `TestControlsStore_PostureOfAMostlyPendingFleet`: 100 assigned, 99
    pending, coverage 1, percent from the single result.
  - The empty fleet reads no rows, and an all-pending fleet reads NULL
    (`TestControlsStore_EmptyFleetIsNoData`, from 4.7).
- [x] 4.10 `migrate --dry-run` applies `00001`–`00004` to an empty
  database in one rolled-back transaction; `SchemaVersion` is 4.
  Done: `ControlsSchemaVersion` is 4. `TestControlsChain_DryRunRollsBack`
  derives `1..4` from it and checks that no table is left behind.
  `TestMigrateControls_DryRun` runs `migrate --chain controls --dry-run`
  and asserts pending `[1 2 3 4]`, no activation and version 0. The chain
  is SQL only, so a new file is rehearsed simply by being in the directory.
- [x] 4.11 Non-vacuous check: drop the epoch predicate from the record
  step, make withdrawal skip the delete, and remove the NULL guard from
  the percent; confirm 4.6, 4.9 and the agreement test fail; restore.
  Done (each restored with `git checkout`):
  - Epoch predicate dropped from `fenced` → `TestControlsStore_EpochFencingAcrossRecreate` (4.9) and `TestControlsStore_EvaluationLifecycle` fail.
  - `withdraw` skips the delete → `TestControlsStore_ParkResultRule` (4.6) fails in every clearing row.
  - NULL guard removed from `floor_percent` → `TestCompliantPercent_SQLAgreesWithGo` fails (division by zero on the 0/0 golden).
#### Success Criteria

- The controls chain applies to an empty database and `make lint-sql`
  is green for both sqlc blocks.
- Every grant test runs as an application role.
- Posture never reads 100% on an empty or unmeasured fleet.

### Phase 5: Evaluation workflows and activities

#### Tasks

- [x] 5.1 Names and types in `internal/workflows`: `EvaluationWorkflowID`
  (`evaluation/<repository id>`), `ControlsDiscoveryWorkflowID`
  (`controls-discovery/installation/<id>/<delivery>`), schedule ids
  `controls-discovery` and `controls-snapshot`,
  `ControlsRolloutWorkflowID`
  (`controls-rollout/<version>/<activated_at unix seconds>`, so a revert
  or rollback rolls out again); signals
  `Recheck{Paths []string, Unknown bool, Priority}` and
  `PolicyChanged`; `PriorityManual Priority = 1`.
  Done: names in `names.go` (workflow and activity names, the two schedule
  ids, `EvaluationWorkflowID`, `ControlsDiscoveryWorkflowID`,
  `ControlsRolloutWorkflowID`); `Recheck` gained `Paths` and `Unknown`
  (omitempty, so the captured rc histories replay unchanged);
  `PolicyChanged` is reused as is; `PriorityManual = 1`;
  `TriggerPREvent`/`TriggerManual`. `TestControlsWorkflowIDs` pins the ids
  and that a re-activation gets a new rollout id.
- [x] 5.2 `EvaluationWorkflow` (`evaluation.go`), built from `repoLoop`:
  a jittered `EVAL_INTERVAL` timer, `recheck` and `policy_changed`
  signals, coalescing that unions changed paths with unknown dominating,
  the budget lease on `installation/eval/<installation id>`, deferral on
  throttle with nothing recorded, park on repository-level access loss,
  ContinueAsNew every 100 iterations carrying the coalesced set, and a
  drain of unread signals before ContinueAsNew. `repoLoop` stays for
  `RepoWorkflow` until Phase 7.
  Done: `internal/workflows/evaluation.go` (`EvaluationWorkflow`, `evalLoop`,
  `coalesce`) and `evaluation_types.go`. Deferrals reuse the check key and
  record nothing; a parked result runs `ParkRepository` and ends the run;
  the lease is on `installation/eval/<id>`. Tests in `evaluation_test.go`
  (schedule/record/park, deferral, in-flight signal coalescing, ContinueAsNew
  at 100, `coalesce` table). Not registered on a worker yet.
- [x] 5.3 `Evaluate` activity (`internal/activities/evaluate.go`): read
  policy state and active assignments, skip unless `managed`; skip
  `conflict` assignments (reported `unmeasurable{reason=conflict}`);
  classify throttle first, then repository access loss; pin the default
  branch head through `PRObserver.GetRef`; select controls by
  `Owns() ∪ Reads()` against the changed paths (all on unknown, the
  schedule or `policy_changed`; API resources only on the schedule);
  run each through one `NewEvalReader`, then `Validate`, `StatusOf` and
  `Fingerprint`; stage the payload. A control-local 403 is
  `unknown{reason=permission}` and parks nothing.
  Done: `internal/activities/evaluate.go` (`Evaluator`, `EvalStore`,
  `EvalPolicy`, `EvalRepo`, `EvalClients`) and `evalclients.go`
  (`GitHubEvalClients`). The pin reads the default branch over REST so a
  lost repository is a 403/404 `IsAccessDenied` sees (the settings read is
  GraphQL first); throttles inside a control's reads are caught by
  `throttleWatch` under the shared `EvalReader`, because types fold read
  errors into results. Each control's fingerprint takes only the reads of
  its own `Owns() ∪ Reads()` (plus a failed `*` listing of a kind it
  reads), so another control's file never moves it. An assignment whose
  revision differs from the loaded policy waits for re-resolution.
  Deviations: `ControlsStore.EvaluationTarget` + query added for the
  read; `app_installations.account_type` added to 00002 (unreleased) to
  fill `RepoContext.AccountType`, and `UpsertInstallation` now takes a
  pointer. Tests: `evaluate_test.go`,
  `TestControlsStore_EvaluationTarget`.
- [x] 5.4 Record and park activities: `RecordEvaluation` (4.5) and
  `ParkRepository` (4.6), with the `checks.trigger` values `pr_event`
  and `manual` accepted.
  Done: `Evaluator.RecordEvaluation` (counts recorded / changed / discarded,
  `AlreadyFinal` on a recorded key) and `Evaluator.ParkRepository` (store
  `Park`, which keeps results on access_denied; logs `CheckRepo`'s park
  line at the same levels and keys). `Evaluator.Register` names the three
  activities; nothing calls it yet. `pr_event`/`manual` were already in
  00001's trigger check: `TestControlsStore_StageAcceptsControlsTriggers`
  pins them.
- [x] 5.5 `ControlsDiscoveryWorkflow`: lists the Evaluation App's
  installations and repositories, refreshes `app = 'eval'` rows of
  `app_installations` and `app_repository_access`, and calls
  `UpsertRepositories`, which resolves inline between the upsert and the
  SignalWithStart of `evaluation/<id>`; parks missing repositories;
  records a `service_runs` row; sets `installation_info{app="eval"}`.
  Done: `ControlsDiscoveryWorkflow` (`internal/workflows/controls_service.go`,
  `DiscoveryWorkflow`'s shape on the `Controls*` activities, writing its
  run through the new `ControlsRecordServiceRun` activity) and
  `activities.ControlsServices` (`controls_services.go`). Listings keep
  archived and forked repositories (`workflows.DiscoveredRepo` carries the
  flags); resolution parks them with results cleared, so there is no
  discovery filter to keep in step. New or un-parked managed repositories
  get a policy_changed SignalWithStart of `evaluation/<id>` due at a random
  point in `EVAL_INTERVAL`. `ghclient.Installation` gained `Personal` and
  `RepositorySelection` for the eval installation row; the store gained
  `ParkMissing` and `RecordServiceRun`. Found by
  `TestControlsStore_ParkMissingAndServiceRun`: a nil `seen` slice is a
  NULL array and `id = ANY (NULL)` parks nothing, so the query coalesces
  it. Tests: `controls_service_test.go`, `controls_services_test.go`.
  Follow-up fix: `upsertOne` passed `Active: true` to resolution, so an
  archived or forked listing was un-parked and resolved managed; it now
  parks with that reason (results cleared) and never un-parks one
  (`TestControlsStore_DiscoveryParksArchivedAndForks`).
- [x] 5.6 Controls rollout: every worker reads `CurrentActivation` at
  startup and starts `controls-rollout/<version>/<activated_at>` with
  `REJECT_DUPLICATE` (one rollout per activation, however many pods
  start), which re-resolves every repository, then
  signals `policy_changed` spread over `POLICY_ROLLOUT_WINDOW`, and marks
  the rollout complete. The evaluator's bootstrap ensures only the
  `controls-discovery` and `controls-snapshot` schedules.
  Done: `ControlsRolloutWorkflow` (resolve pass, policy_changed signal pass
  by `now + window`, sleep the window, `ControlsCompleteRollout`, a
  `policy_rollout` service run); `ControlsServices.ResolveAll` /
  `SignalAll` / `CompleteRollout` over the new store reads
  `ActiveRepositories` and `CompletePolicyRollout`. `ResolveAll` refuses a
  rollout whose version is not the worker's loaded policy, so a retry lands
  on a pod that has it. `serviceStarter.startControlsRollout` reads
  `CurrentActivation` and starts `controls-rollout/<v>/<unix>` with
  `REJECT_DUPLICATE` only when the loaded policy is the activated one;
  `schedules(RoleEvaluator)` ensures exactly `controls-discovery` and
  `controls-snapshot` (snapshot takes `SnapshotInput`). The evaluator role
  calls neither until 7.1, per the phase's "nothing registered" criterion.
  The task names no straggler pass, so the rc's is not carried over.
- [x] 5.7 Webhook routing on `/webhooks/github/eval`: a default-branch
  push signals `recheck` at priority 2 with the changed paths filtered
  through `Snapshot.WatchedPaths()` (a push with no watched path signals
  nothing; 2048 commits, `forced`, or more than 256 filtered paths is
  unknown, DESIGN-0032 § history growth); `repository` created,
  renamed, transferred, archived, unarchived and deleted run discovery,
  resolution or park; `installation` and `installation_repositories`
  from either App re-resolve the affected repositories, because a
  Remediation App change moves the effective mode. This replaces the
  rc's drop of every Evaluation App delivery (IMPL-0028 task 9.1);
  delete `TestRouteWebhook_EvaluationAppDropped` with it.
  Done: `activities.ControlsRouter` (controls_route.go) takes every
  Evaluation App delivery through `Router.WithControls`; until 7.1 wires it
  the default `unwiredControls` logs and drops, so the rc binary is
  unchanged. Ingest puts the push change set on `WebhookInput.Paths`
  (watched, sorted) or `Unknown` (≥2048 commits, forced, >256 paths); a
  push touching no watched path is 204. Push rechecks a known active
  managed repository at priority 2, discovers an unknown one and never
  un-parks; repository events upsert with the payload's archived/fork
  flags (so discovery parks, not churns) and recheck in full, deleted and
  removed park `removed`, `installation.deleted` parks
  `installation_removed`. `RecordAppAccess` (Remediation App) re-resolves
  the named repositories, or the account's org, and signals
  `policy_changed` on a change. Known gap: the Remediation App's access
  rows are the remediator's to write (IMPL-0030), so re-resolution reads
  the access recorded so far. The watched set is still the rc's until
  7.1/7.8 pass `Snapshot.WatchedPaths()`. `TestRouteWebhook_EvaluationAppDropped`
  deleted; tests in controls_route_test.go, ingest_test.go and
  `TestControlsStore_RoutingReads`.
- [x] 5.8 Controls snapshot: `controls-snapshot` writes
  `compliance_snapshots` rows from 4.7's query.
  Done: `ControlsSnapshotWorkflow` runs the `ControlsSnapshot` activity
  (`ControlsServices.Snapshot`) stamped at the workflow's start, then
  records a `snapshot` service run with the row count. The controls line
  has no checks table, so `SnapshotInput.Retention` prunes nothing.
  `ControlsStore.InsertComplianceSnapshot` reads `ControlCompliance` (all
  lineages, every org) and inserts its rows in one transaction, so a
  snapshot never disagrees with the live query and a NULL percentage stays
  NULL. Tests: `TestControlsSnapshotWorkflow_WritesAndRecords`,
  `TestControlsServices_SnapshotStampsTheRun`, and
  `TestControlsStore_ComplianceSnapshot` (as rg_evaluator).
- [x] 5.9 `repo-guardian evaluate --repo <org>/<name> [--format json]`:
  loads the policy directory, authenticates as the Evaluation App,
  checks access, resolves, evaluates with no store, and prints a table
  or JSON carrying `schema_version` and the evaluated commit. Exit codes:
  0 compliant or nothing measured, 1 non-compliant, 2 usage or
  configuration error, 3 operational failure. Golden JSON for a fake
  repository; one test per exit code.
  Done: `cmd/repo-guardian/evaluate.go`. Loads the policy root (`--policy`,
  default `$GUARDIAN_CONFIG`) with the built-in registry, reads the
  Evaluation App's id and key path through the new
  `config.LoadAppCredentials`, finds the org's installation, checks access
  with `GetRepository` (which also supplies the archived/fork flags for
  resolution), then calls `activities.Evaluator.EvaluateResolved`. That
  method is new and store-free: the same selection and run as the
  scheduled evaluation, staging nothing, with epochs at zero. Output is a
  table, or JSON with `schema_version`, `policy_version`, `branch` and
  `commit`. Exit codes travel back through `exitError`, which `main`
  honours: 0 compliant or nothing measured, 1 non-compliant (silent),
  2 usage, policy or credentials error, 3 no access, throttled or failed.
  Tests: `TestEvaluate_ExitCodes` (one case per code, plus nothing-measured
  and a missing policy root), `TestEvaluate_GoldenJSON`
  (`testdata/evaluate.golden.json`, regenerate with `-update-evaluate`)
  and `TestEvaluate_Table`.
- [x] 5.10 Workflow tests (time-skipping environment): disjoint pushes
  coalesce to the union; one unknown signal selects every control; a
  parked repository is never read; a deferral writes no posture;
  ContinueAsNew history stays bounded under maximum-size signals (the
  bound the IMPL-0028 spike measured).
  Done: in `evaluation_test.go`.
  - `TestEvaluationWorkflow_SignalsDuringAnEvaluationCoalesce`: now sends disjoint `{b}` and `{a}` while an evaluation is in flight; they coalesce to `[a b]`.
  - `TestEvaluationWorkflow_UnknownSignalSelectsEveryControl`
  - `TestEvaluationWorkflow_ParkedRepositoryIsNeverReadAgain`: an already-parked result ends the loop with no further reads, records or park.
  - `TestEvaluationWorkflow_DeferralRecordsNothing` (from 5.2) covers the deferral case.
  - `TestEvaluationWorkflow_HistoryBoundedUnderMaxSignals`: a 256-path, 60-byte-path signal during every one of the 100 evaluations, and two disjoint ones during the last, to measure the INV-0022 spike 5(c) bound.

  Finding fixed: `coalesce` had no cap, so disjoint maximum signals grew the carried state without bound. A union past the new `workflows.MaxChangedPaths` (256, which ingest's filter now uses too) becomes unknown. The ContinueAsNew state stays under 1 KB; with the cap removed it read 32 KB and the test failed. A `TestCoalesce` row covers the cap.
- [x] 5.11 Change-detection tables (activity plus store): status change,
  file blob change, API value change with unchanged statuses, revision
  change and a first evaluation bump the generation; a cosmetic title
  edit and an unrelated push do not; unselected controls keep their
  `evaluated_sha`.
  Done. Activity table: `TestEvaluate_ChangeDetection` runs a file control and an API control (a label) through `Evaluator.Evaluate`.
  - These move the fingerprint (the store bumps the generation on exactly that): a status change, a file blob change, a label colour change with statuses unchanged, and a revision change.
  - An unrelated push moves neither.

  `TestPolicyRevision_CosmeticTitleEdit` loads two policy roots that differ only in titles and gets one revision.

  Store table, as rg_evaluator: `TestControlsStore_ChangeDetection`.
  - The first evaluation bumps both controls.
  - A repeated fingerprint bumps nothing.
  - A changed fingerprint bumps one.
  - The unselected `renovate` keeps its `evaluated_sha`.
- [x] 5.12 `TestIntegration_OneEvaluation` (`-tags integration`):
  `temporaltest`, the controls chain in `pgtest` and the GitHub fake run
  discovery → resolution → evaluation → recorded results for a fixture
  org, and capture an `EvaluationWorkflow` history into
  `internal/workflows/testdata/histories/evaluation_first_run.json`,
  replayed in CI with the existing fixtures.
  Done: `internal/activities/controls_integration_test.go`. `pgtest.ControlsDB`
  (store as `rg_evaluator`, policy activated by the owner), a loaded policy
  root assigning `codeowners@1`, a fixture `Lister` and an httptest GitHub
  serving the repository, its ref and no files. A versioned worker on the
  `repo-guardian-eval` deployment runs discovery → `EvaluationWorkflow`;
  a schedule re-check brings the first evaluation forward, which records
  `non_compliant` at the pinned head. The repository then 404s and a second
  re-check parks it and ends the workflow, so `evaluation_first_run.json`
  holds acquire → evaluate → record and evaluate → park → complete.
  `RegisterUnion` carries the not-yet-registered controls workflows
  (`unregisteredSet`; 7.1 moves them to `evaluatorSet`, the dedupe absorbs
  it) so the history replays now. Found a real bug: the store returns
  `AssignedControl.ControlID` as the bare slug (version in its own column)
  but `selectControls` parsed `slug@version`, so every stored assignment was
  skipped; the unit fixtures had used `name@1`. The evaluator now builds the
  id from slug + version, `EvaluateResolved` passes the slug, `evaluate`
  prints `slug@version`, and the fixtures match the store.
- [x] 5.13 Non-vacuous check: make coalescing keep only the last signal
  and make the push filter ignore `Reads()`; confirm the coalescing test
  and the `catalog-info.yaml` selects `custom_properties` test fail;
  restore.
  Done: the evaluator tests run fake controls, so the new
  `TestEvaluate_PushSelectsByReads` uses `custom_properties`' shape (owns
  API properties, reads `catalog-info.yaml`, `custom_properties.go`
  `Reads`): it is the only control a push of that path selects. Neutralized (`coalesce` drops `pending`; `selects`
  ranges over `Owns()` only): `TestCoalesce`,
  `TestEvaluationWorkflow_SignalsDuringAnEvaluationCoalesce`,
  `TestEvaluationWorkflow_HistoryBoundedUnderMaxSignals` and
  `TestEvaluate_PushSelectsByReads` failed; restored, all pass.
- [x] 5.14 KEDA on the homelab (human-run, moved from IMPL-0028 5.6 and
  5.7, because the evaluation queue first has work here). Prerequisites:
  install KEDA 2.21 (check its Kubernetes compatibility table against
  the Talos cluster), and set `keda.prometheus.serverAddress` to the
  in-cluster Prometheus that scrapes Temporal, with
  `keda.prometheus.namespaceLabel` left at `exported_namespace`
  (IMPL-0028 5.6 found the ServiceMonitor rename). Then, on dev in
  `topology: split`: (a) with `repo-guardian-eval` non-empty, compare
  the default query's value with `temporal task-queue describe`, and if
  they disagree fix the query and its helm-unittest and record why;
  (b) enable `evaluator.keda`, generate a backlog with a policy change
  or a discovery run, and watch the evaluator scale out and back. Record
  both in INV-0022.
  **Deferred - human required.**

#### Success Criteria

- Discovery, resolution, evaluation and recording run end to end in the
  integration test, against the controls chain, as `rg_evaluator`.
- The evaluation history replays in CI.
- Nothing is registered on a worker yet; the rc binary is unchanged.

### Phase 6: Posture, metrics, generated monitoring and the report

#### Tasks

- [x] 6.1 Metrics in `internal/metrics/metrics.go`: `evaluations_total{org,
  outcome}`, `evaluation_errors_total{org, control}`,
  `evaluation_changes_total{org, control}`, `results_discarded_total{org,
  reason}`, `evaluation_freshness_seconds{org}` (histogram of the
  previous `evaluated_at`'s age), `store_pool_wait_seconds{role}`, and
  the gauges `controls_status{org, control, bucket}` and
  `controls_coverage{org, control}`; producers wired in 5.3 and 5.4.
  `installation_info` gains `app`. No metric carries a repository label.
  Done: the seven series in `metrics.go` (`TestMetricNames_ExactSet`
  updated). `Evaluate` counts `evaluations_total` per attempt (deferred is
  not counted, parked is `skipped`) and `evaluation_errors_total` for a
  control returning an error or ending with an error rule
  (`TestEvaluate_CountsOutcomesAndControlErrors`). `RecordEvaluation`
  counts `evaluation_changes_total` per generation move and observes
  `evaluation_freshness_seconds` from the previous `evaluated_at`
  (`LockResult` reads it), both after commit
  (`TestControlsStore_ChangeDetection`). `store_pool_wait_seconds{role}`
  comes from `observability.InstrumentPostgresConfig(cfg, role)`, whose
  tracer wraps otelpgx and is the pool's `AcquireTracer`, wired into the
  controls workers' pools. `controls_status`/`controls_coverage` are
  published by 6.3's exporter; `installation_info` already carries `app`
  (IMPL-0028 3.6).
- [x] 6.2 `repo_guardian_deployment_info{topology, version}`, constant 1,
  set once at startup from the role configuration.
  Done: `deployment_info` gains `version`; `SetDeploymentInfo(topology,
  version)` runs once in `bringUp` with `temporal.BuildID()` (the module
  version of a tagged build, else the VCS revision, else `dev`).
- [x] 6.3 Posture exporter (OQ8) over 4.7's query: read before reset; on
  a read error keep the previous values; publish nothing when not
  configured to export. Tests for each, and for a vanished org dropping
  its series.
  Done: `api.PostureExporter` (`internal/api/posture.go`), per OQ8 the api
  role's job. `Export` reads `ControlCompliance(nil, LineageAll)`, sums the
  decisions per (org, control), then resets and republishes
  `controls_status{bucket}` (unmeasurable = access_denied + suspended +
  conflict) and `controls_coverage` (assigned). A read error returns and
  keeps the previous values; the reset drops a vanished org's series; a
  nil reader (not configured) publishes nothing and `Run` returns. Tests
  for each. Wiring it into the api role over the controls read-only store
  lands with the switch-over (Phase 7), when the api role gets that DSN.
- [x] 6.4 `monitoring.Derive(snap *policy.Snapshot, opts Options)`,
  keyed on control id; mechanisms derived from the snapshot (for
  example `custom_properties` with `apply = "workflow"`).
  Done: `Model{Orgs, Controls, Mechanisms, Source}` keyed on control id
  (`Control{ID, Type, Apply, Remediates, Orgs}`); `Derive` takes enterprise
  orgs ∪ org files ∪ `--org`, assigned orgs from baseline/added/replaced
  minus org-level excludes, and turns a mechanism on only for an assigned
  control (`custom_properties` + `apply = "workflow"` → the GHA mechanism).
  `monitoring generate --config` now takes a policy root (`policy.Load`
  with the built-in registry); no config = `Derive(nil, …)`, the static
  tier. Tests: `internal/monitoring/derive_test.go` (depguard lets
  monitoring tests import `controls/builtin`). Regeneration lands in 6.8.
- [x] 6.5 Dashboards (IMPL-0025 17.1, adapted): E1 and E2 rebuilt over
  `controls_status` and `controls_coverage` with `max by` inside and
  `sum` outside, and the "no data when nothing is assigned" guard; E3
  rebuilt around otelhttp, otelpgx, the Temporal SDK series,
  `evaluations_total`, the budget and rate series, discovery, and an API
  row; E4 trimmed and its matchers re-cut to log lines emitted by
  `internal/activities` and `internal/workflows`, with
  `TestLogLines_AreStillEmittedByTheBinary` passing and
  `TestSuite_PostureQueriesDedupeAcrossReplicas` re-pointed.
  Done: E1 over `controls_status`/`controls_coverage` (fleet compliance,
  assignments, unmeasurable, compliance by control, failing per control,
  assignments by status) with `max by (org, control[, bucket])` inside and
  `sum` outside; `compliance()` filters the denominator `> 0` and guards
  `and on (...) (coverage > 0)`, so nothing assigned reads "no data". E1
  service health is evaluation error rate, freshness p95, budget remaining
  and budget waits. E2 rows per declared org (compliance by control, buckets
  table, evaluation errors/changes) with the org variable on
  `controls_coverage`. E3 rows: HTTP, Temporal SDK, Evaluation (outcomes,
  errors by control, changes + stale-epoch discards, freshness), Budget and
  discovery, Store (query, pool wait by role, pgxpool), API, Runtime. E4
  keeps Errors and matches five activities/workflows lines (park, deferred
  and failed evaluations, awaiting re-resolution, partial discovery);
  `TestLogLines_AreStillEmittedByTheBinary` now walks only those two trees,
  and `TestSuite_PostureQueriesDedupeAcrossReplicas` requires every posture
  gauge occurrence to sit directly inside a `max by`.
- [x] 6.6 Alert catalogue (IMPL-0025 17.2, adapted): apply IMPL-0025's
  audit lists, drop alerts on metrics the switch-over deletes, add
  evaluation alerts (errors by control, stale-epoch discards, freshness).
  Before committing, confirm every Temporal metric name against the
  homelab's real `/metrics` output and record the check here. Update
  `TestCatalogue_RareEventAlertsCatchTheFirstIncrement`. — *deferred:
  human required (the metric-name check)*
  Done (code): IMPL-0025's lists applied to the controls line. Deleted the
  17 audit alerts plus HighErrorRate/AllChecksFailing (→
  `RepoGuardianEvaluationErrorRate`) and SlowChecks (its
  `check_duration_seconds` goes with the switch-over); StoreQueryErrors →
  `RepoGuardianStoreErrors`; NoWebhooks, WebhookRejectionsHigh and
  RateLimitNearExhaustion stay. New evaluation alerts:
  `ControlEvaluationErrors` (per org/control, `firstOrIncrease`),
  `EvaluationFreshness` (p95 > 48h) and `StaleEpochDiscards`. Temporal adds
  `WorkflowTaskFailures`, `NoWorkerPollers` and `TemporalUnreachable` beside
  the existing four. Nothing is mechanism-gated until IMPL-0030
  (`TestGenerate_NothingIsGatedYet`; gating stays covered by
  `skip_test.go`). The rare-event test now pins ControlEvaluationErrors and
  rejects the guard on the two threshold alerts. Found and fixed on the way
  (6e9be7b): nothing on the controls path fed `rate_limit_remaining`.
  Temporal names checked against the pinned SDK's
  `internal/common/metrics/constants.go` (v1.49.0): `workflow_task_execution_failed`,
  `num_pollers`, `long_request(_failure)`, `worker_task_slots_available`,
  `sticky_cache_total_forced_eviction`, both schedule-to-start latencies.
  **Live `/metrics` check against the homelab: deferred - human required.**
- [x] 6.7 Chart mirror (IMPL-0025 17.9): `prometheusrule.yaml` carries
  the catalogue; `make lint-alerts-chart` stays green; helm-unittest
  locks the selectors, not just the names.
  Done: `prometheusrule.yaml` carries the 15-alert catalogue — group
  `repo-guardian.rules` (the eight controls/service alerts) and
  `repo-guardian.temporal` (seven), each tunable and disableable under
  `prometheusRule.alerts.<name>`. `make lint-alerts-chart`: 15 rules,
  promtool SUCCESS. `tests/prometheusrule_test.yaml` locks the exact
  expressions of EvaluationErrorRate, ControlEvaluationErrors (both
  first-increment disjuncts), StaleEpochDiscards (`reason="stale_epoch"`),
  EvaluationFreshness, StoreErrors, NoWorkerPollers and TemporalUnreachable,
  plus a regex refusing any rc-era series; that assertion was confirmed to
  fail when pointed at a live series. 243/243 helm-unittest.
- [x] 6.8 `make monitoring-generate`; commit `contrib/generated/`;
  `make lint-monitoring` green.
  Done: `make monitoring-generate` rewrote the static tier (unconfigured:
  nil snapshot, discovered org rows, 15 alert rules); `make lint-monitoring`
  ✓ current; `make lint-alerts` green (15 chart rules, promtool SUCCESS).
- [x] 6.9 `internal/report` over control results: per-org sections keyed
  by control with buckets, coverage, baseline versus org-added, and
  exclusions; the shared percent definition with "no data"; goldens
  rewritten (no data, pending-heavy, stale, unmeasurable, a replaced
  version, exclusions, two orgs, escaped cells), and the findings
  remediation enums and the `foreign_pr` golden removed.
  Done: `report.Build(*Data)` over `ControlCompliance` (every lineage) and
  `ControlExclusions`: per org, a Baseline table (baseline and replaced) and
  an Org-added table keyed by control, each row carrying every bucket,
  compliance and coverage as the SQL computed them; an exclusions table
  (repository, control or "all controls", state, reason, excluded by); a
  headline from `Org.Totals()` (compliance via `store.CompliantPercent`,
  coverage via the floor mirror). An empty denominator renders "no data".
  Goldens rewritten: full, no_data, pending_heavy, stale, unmeasurable,
  replaced_version, exclusions_only, two orgs, escaped_cells; the findings
  reasons/remediation enums, trends, PR column and `foreign_pr` golden are
  gone. `repo-guardian report` reads the controls store
  (`RequireControlsSchema`), and drops `--with-pr-links`. The rc store's
  integration tests compare against the shared-query rows instead of the
  report. `docs/operations/compliance-reports.md` rewritten.
- [x] 6.10 Non-vacuous check: reverse `max by`/`sum` nesting in one E1
  panel, reset before read in the exporter, and remove the report's NULL
  guard; confirm the dedupe test, the exporter test and the no-data
  golden fail; restore.
  Done: (a) E1 "Repositories failing each control" rewritten as
  `max by (control) (sum by (org, control) (…))` → `TestSuite_PostureQueriesDedupeAcrossReplicas`
  failed ("outside a max-by dedupe (0 of 1 occurrences wrapped)"); (b) reset
  moved before the read in `PostureExporter.Export` →
  `TestPostureExporter_ReadsBeforeReset` and `…_ReadErrorKeepsPreviousValues`
  failed; (c) `renderPercent`'s nil guard removed → `TestRenderPercent` and
  the `no_data` golden failed ("0.0%" for "no data"). All restored; the
  packages pass.

#### Success Criteria

- `make lint-monitoring`, `make lint-alerts` and the monitoring tests are
  green, with Temporal metric names checked against live output.
- No posture query can read 100% on an empty fleet.

### Phase 7: Switch-over

The phase that deletes. It lands inside the plan's one PR (OQ6), so `v2` never holds a
half-switched binary.

#### Tasks

- [x] 7.1 The evaluator role registers `EvaluationWorkflow`,
  `ControlsDiscoveryWorkflow`, the controls rollout and snapshot
  workflows and their activities on `repo-guardian-eval`;
  `RepoWorkflow`, the old discovery, snapshot, rollout and bootstrap
  workflows and `CheckRepo` are unregistered and deleted, with
  `check_then_park.json`. The replay suite still holds
  `installation_grant_report.json` and `evaluation_first_run.json`.
  Done: `workflows.evaluatorSet` is Installation, Webhook, Evaluation,
  ControlsDiscovery, ControlsRollout and ControlsSnapshot; `rcSet`,
  `Register` and `unregisteredSet` are gone. `RepoWorkflow`, the old
  Discovery/Snapshot/PolicyRollout/Bootstrap workflows, their names,
  payload types and tests, `CheckRepo`/`RecordCheck`/`Park`, the rc
  `Services` and `check_then_park.json` are deleted. The shared service
  helpers moved to a slim `workflows/service.go`, and the deferral helpers
  moved to `activities/deferral.go`. `Router` is now a dispatcher: an
  Evaluation App delivery goes to `ControlsRouter`, and a Remediation App
  access change goes to the same router as `AppAccessRecorder`.
  `cmd/repo-guardian`:
  - the eval half loads `policy.Load(GUARDIAN_CONFIG, builtin.Registry())`
    and registers ControlsServices, Evaluator, Router and
    RemediationStarter (`registerEvaluation`);
  - after `w.Start()` it ensures the controls Schedules and starts the
    activated policy's rollout;
  - both controls workers require `ControlsSchemaVersion`.
  The rc `worker` role is gone: `RoleWorker`, `startV2Worker`,
  `--strict-templates`, the rule-engine loader, the single-App ingest
  route and the rc backlog probe. Ingest routes both Apps to the
  evaluation queue, with watched paths from `Snapshot.WatchedPaths()`, and
  requires `EVAL_`/`REMEDIATE_WEBHOOK_SECRET`. The chart's split topology
  renders ingest + controls roles + api (no worker Deployment or Service),
  and helm-unittest is 241/241.
  The replay suite holds `installation_grant_report.json`,
  `installation_dev_rc4.json` and `evaluation_first_run.json`, all
  replaying under `RegisterUnion`. `TestRegister_EachRoleRegistersOnlyItsSet`
  pins the exact sets, and `TestIntegration_OneEvaluation` passes.
- [x] 7.2 Retire the parity suite scenario by scenario: list every
  scenario in `internal/checker/parity_test.go` and
  `internal/store/postgres/parity_integration_test.go` in a table in
  this task, each with the test that replaces it (a resolution golden, a
  control fixture, a store integration test) or "retired: v1 behaviour,
  no replacement" with the reason. Delete only after the table is
  complete. `TestPRIdentity_IsFrozen` (checker and reconciler) is retired
  deliberately; `tools/rgctl` keeps its own literals.
  Done: every scenario has a fate (103 rows). Retired now:
  - `internal/store/postgres/parity_integration_test.go`;
  - both `TestPRIdentity_IsFrozen` files (`checker`, `reconciler`).

  The 101 checker goldens and `parity_test.go` go with `internal/checker`
  in 7.3, because every remaining engine test runs through
  `parityCheckRepo` until then.

  | Scenario | Replacement or reason |
  |---|---|
  | `checker` `TestBaseDrift_ExistingPR_BranchUpdatedBeforeSync` | retired: v1 behaviour, no replacement — the rc's shared `repo-guardian/add-missing-files` branch is gone; IMPL-0030 opens one PR per (repository, control); update-branch is pinned by `internal/github/write` `TestUpdateBranch` |
  | `checker` `TestBaseDrift_NoExistingPR_BranchNotUpdated` | retired: v1 behaviour, no replacement — the rc's shared `repo-guardian/add-missing-files` branch is gone; IMPL-0030 opens one PR per (repository, control) |
  | `checker` `TestBaseDrift_UpdateError_ReconcileContinues` | retired: v1 behaviour, no replacement — the rc's shared `repo-guardian/add-missing-files` branch is gone; IMPL-0030 opens one PR per (repository, control) |
  | `checker` `TestBranchProtection_BranchDoesNotExist` | retired: v1 behaviour, no replacement — a ruleset targets refs by pattern, so a missing branch is not a state the `branch_ruleset` type models |
  | `checker` `TestBranchProtection_DryRun` | retired: v1 behaviour, no replacement — there is no dry run: the evaluator holds a read-only App and never writes (DESIGN-0032) |
  | `checker` `TestBranchProtection_Matches_NoAction` | `branch_ruleset` `TestConformance` (passing case) |
  | `checker` `TestBranchProtection_Mismatch_NoRemediate` | `branch_ruleset` `TestConformance` (failing case) + resolution golden `mode-org-control` |
  | `checker` `TestBranchProtection_Mismatch_Remediate_Update` | `branch_ruleset` `TestRemediate_UpsertsByID` + write `TestUpsertRuleset` |
  | `checker` `TestBranchProtection_NoRuleset_Remediate_Create` | `branch_ruleset` `TestRemediate_UpsertsByID` + write `TestUpsertRuleset` |
  | `checker` `TestCheckRepoPolicy_DriftCounter_DoesNotIncrement_WhenActionable` | retired: v1 behaviour, no replacement — `pr_open_with_empty_actionable_total` was deleted (IMPL-0028 1.3) |
  | `checker` `TestCheckRepoPolicy_DriftCounter_DoesNotIncrement_WhenNoPROpen` | retired: v1 behaviour, no replacement — `pr_open_with_empty_actionable_total` was deleted (IMPL-0028 1.3) |
  | `checker` `TestCheckRepoPolicy_DriftCounter_IncrementsWhenPROpenAndNoActionable` | retired: v1 behaviour, no replacement — `pr_open_with_empty_actionable_total` was deleted (IMPL-0028 1.3) |
  | `checker` `TestCheckRepoPolicy_OpenPRsByRule_PopulatedWithActionableRules` | retired: v1 behaviour, no replacement — `open_prs_by_rule` was deleted (IMPL-0028 1.3); per-control PR posture returns with IMPL-0030 |
  | `checker` `TestCheckRepo_AllFilesExist` | `file`/`codeowners` `TestConformance` (passing) + `TestEvaluate_ScheduleStagesEveryActiveControl` |
  | `checker` `TestCheckRepo_DryRun` | retired: v1 behaviour, no replacement — there is no dry run: the evaluator holds a read-only App and never writes (DESIGN-0032) |
  | `checker` `TestCheckRepo_MissingFiles_ExistingPR` | retired: v1 behaviour, no replacement — control status never depends on a PR (PRObserver is remediation-only); PR handling returns with IMPL-0030 |
  | `checker` `TestCheckRepo_MissingFiles_NoPR` | `file` `TestConformance` + `TestFileControl_Absent_TemplateAtCanonical` |
  | `checker` `TestCheckRepo_MissingFiles_ThirdPartyPR` | retired: v1 behaviour, no replacement — `foreign_pr` suppression; control status never depends on a PR (PRObserver is remediation-only); PR handling returns with IMPL-0030 |
  | `checker` `TestCheckRepo_Skips__archived` | resolution golden `archived` + `TestControlsStore_DiscoveryParksArchivedAndForks` |
  | `checker` `TestCheckRepo_Skips__empty_repo_is_skipped_but_not_durable` | `TestEvaluate_Skips` |
  | `checker` `TestCheckRepo_Skips__fork` | resolution golden `fork` + `TestControlsStore_DiscoveryParksArchivedAndForks` |
  | `checker` `TestCheckRepo_StaleBranchCleanup` | retired: v1 behaviour, no replacement — the controls Writer has no branch delete (IMPL-0028 Phase 2) |
  | `checker` `TestConvergence_Bundle_AddAndRemove_RendersBothSections` | retired: v1 behaviour, no replacement — shared-branch convergence (orphans, auto-close, sticky log, restore) has no controls equivalent; IMPL-0030 owns per-control PRs |
  | `checker` `TestConvergence_DeleteFileError_IncrementsCounter_Continues` | retired: v1 behaviour, no replacement — shared-branch convergence (orphans, auto-close, sticky log, restore) has no controls equivalent; IMPL-0030 owns per-control PRs |
  | `checker` `TestConvergence_GenuineOrphanIsStillDeleted` | retired: v1 behaviour, no replacement — shared-branch convergence (orphans, auto-close, sticky log, restore) has no controls equivalent; IMPL-0030 owns per-control PRs |
  | `checker` `TestConvergence_GetContentsOnBranchError_FailsSafe` | retired: v1 behaviour, no replacement — shared-branch convergence (orphans, auto-close, sticky log, restore) has no controls equivalent; IMPL-0030 owns per-control PRs |
  | `checker` `TestConvergence_OrphanCleanupDisabled_NoDeletion` | retired: v1 behaviour, no replacement — shared-branch convergence (orphans, auto-close, sticky log, restore) has no controls equivalent; IMPL-0030 owns per-control PRs |
  | `checker` `TestConvergence_RenovateFirst_BothVariantsDeleted` | retired: v1 behaviour, no replacement — shared-branch convergence (orphans, auto-close, sticky log, restore) has no controls equivalent; IMPL-0030 owns per-control PRs |
  | `checker` `TestConvergence_RenovateFirst_GateClosesMidFlight_RestoresDependabot` | retired: v1 behaviour, no replacement — shared-branch convergence (orphans, auto-close, sticky log, restore) has no controls equivalent; IMPL-0030 owns per-control PRs |
  | `checker` `TestConvergence_RenovateFirst_HandDeletedDependabot_AutoCloses` | retired: v1 behaviour, no replacement — shared-branch convergence (orphans, auto-close, sticky log, restore) has no controls equivalent; IMPL-0030 owns per-control PRs |
  | `checker` `TestConvergence_RenovateFirst_IdempotentReSweep_ZeroMutations` | retired: v1 behaviour, no replacement — shared-branch convergence (orphans, auto-close, sticky log, restore) has no controls equivalent; IMPL-0030 owns per-control PRs |
  | `checker` `TestConvergence_RenovateFirst_RemovesDependabot` | retired: v1 behaviour, no replacement — shared-branch convergence (orphans, auto-close, sticky log, restore) has no controls equivalent; IMPL-0030 owns per-control PRs |
  | `checker` `TestConvergence_RenovateFirst_Sweep1_GateClosedSkipsDependabot` | `dependency_updates` `TestConformance`: one control owns the renovate-or-dependabot choice, so there is no gate |
  | `checker` `TestConvergence_RepairDoesNotFightAnActionableAbsentRule` | retired: v1 behaviour, no replacement — shared-branch convergence (orphans, auto-close, sticky log, restore) has no controls equivalent; IMPL-0030 owns per-control PRs |
  | `checker` `TestConvergence_RepairScopedToRuleTarget` | retired: v1 behaviour, no replacement — shared-branch convergence (orphans, auto-close, sticky log, restore) has no controls equivalent; IMPL-0030 owns per-control PRs |
  | `checker` `TestConvergence_RepairsBranchThatDeletedADefaultBranchFile` | retired: v1 behaviour, no replacement — shared-branch convergence (orphans, auto-close, sticky log, restore) has no controls equivalent; IMPL-0030 owns per-control PRs |
  | `checker` `TestConvergence_RestoreSkipsPathAnActionableRuleJustWrote` | retired: v1 behaviour, no replacement — shared-branch convergence (orphans, auto-close, sticky log, restore) has no controls equivalent; IMPL-0030 owns per-control PRs |
  | `checker` `TestConvergence_SatisfiedRuleNeverDeletesDefaultBranchFile` | `codeowners` `TestExistingFileNeverEdited` + `TestFileControl_Passing_EmptyChangeSet` |
  | `checker` `TestConvergence_Sweep1_OpensPR` | retired: v1 behaviour, no replacement — shared-branch convergence (orphans, auto-close, sticky log, restore) has no controls equivalent; IMPL-0030 owns per-control PRs |
  | `checker` `TestConvergence_Sweep2_OrphanCleanupAndBodyRefresh` | retired: v1 behaviour, no replacement — shared-branch convergence (orphans, auto-close, sticky log, restore) has no controls equivalent; IMPL-0030 owns per-control PRs |
  | `checker` `TestConvergence_Sweep3_AutoCloseDisabled_LeavesPROpen` | retired: v1 behaviour, no replacement — shared-branch convergence (orphans, auto-close, sticky log, restore) has no controls equivalent; IMPL-0030 owns per-control PRs |
  | `checker` `TestConvergence_Sweep3_AutoClosesPR_Default` | retired: v1 behaviour, no replacement — shared-branch convergence (orphans, auto-close, sticky log, restore) has no controls equivalent; IMPL-0030 owns per-control PRs |
  | `checker` `TestEmptyIgnoreList_NoReposSkipped` | resolution golden `baseline-only` |
  | `checker` `TestForeignPRForRule_CustomTitleDoesNotSelfSuppress` | retired: v1 behaviour, no replacement — `foreign_pr` suppression; control status never depends on a PR (PRObserver is remediation-only); PR handling returns with IMPL-0030 |
  | `checker` `TestGlobalIgnoreList_GlobPattern` | resolution golden `exclude-repos` |
  | `checker` `TestGlobalIgnoreList_NoMatchStillProcesses` | resolution golden `exclude-repos` (the unmatched repository keeps its assignments) |
  | `checker` `TestGlobalIgnoreList_SkipsAllRules` | resolution golden `exclude-with-reason` |
  | `checker` `TestIntegration_HCLConfigWithCustomPropertiesReconciler` | `custom_properties` `TestConformance` + `TestSourceStates_ExactWrites` |
  | `checker` `TestIntegration_IgnoreLists_SettingRules_BranchProtection` | resolution golden `exclude-repos` + `repo_settings`/`branch_ruleset` `TestConformance` |
  | `checker` `TestIntegration_LabelSyncReconciler_EndToEnd` | `labels` `TestConformance` + `TestRemediate_CaseOnlyIsOneUpdate` |
  | `checker` `TestIntegration_PolicyLoadAndEngineCreation` | policy `TestLoad_Layout` + `TestPolicyExamples_Summary` |
  | `checker` `TestIntegration_RenovateFileRules_AllPresent` | `dependency_updates` `TestConformance` (passing) |
  | `checker` `TestIntegration_RenovateFileRules_BothMissing` | `dependency_updates` `TestConformance` (absent) |
  | `checker` `TestIntegration_RenovateFileRules_ConfigInvalidPreset` | `dependency_updates` `TestConformance` + `TestAddExtends` |
  | `checker` `TestIntegration_RenovateFileRules_ConfigMissing` | `dependency_updates` `TestPartialRemediation` |
  | `checker` `TestIntegration_RenovateFileRules_WorkflowDrifted` | `file` `TestConformance` (exact mode, YAML-semantic mismatch) |
  | `checker` `TestIntegration_RenovateFileRules_WorkflowMissing` | `file` `TestConformance` (exact mode, file missing) |
  | `checker` `TestPRRefresh_CRLFBodyFromWebUI_SkipsPatch` | retired: v1 behaviour, no replacement — v1 PR body refresh; per-control PR text returns with IMPL-0030 |
  | `checker` `TestPRRefresh_UnchangedText_SkipsPatch` | retired: v1 behaviour, no replacement — v1 PR body refresh; per-control PR text returns with IMPL-0030 |
  | `checker` `TestPerRuleIgnoreList_ReconcilerAlsoSkipped` | resolution golden `exclude-beats-add-in-layer` (an excluded assignment evaluates nothing) |
  | `checker` `TestPerRuleIgnoreList_SkipsOnlyThatRule` | resolution golden `exclude-with-reason` |
  | `checker` `TestPolicyCheckRepo_AbsentMode_Phase1__forbidden_file_absent___rule_satisfied` | retired: v1 behaviour, no replacement — the `file` type has no absent mode (DESIGN-0031 D6); renovate-versus-dependabot is `dependency_updates` |
  | `checker` `TestPolicyCheckRepo_AbsentMode_Phase1__forbidden_file_present___dry-run_enumerates_only` | retired: v1 behaviour, no replacement — there is no dry run: the evaluator holds a read-only App and never writes (DESIGN-0032) |
  | `checker` `TestPolicyCheckRepo_BackwardCompatibility` | retired: v1 behaviour, no replacement — built-in defaults and env-injected rules are gone; the policy root is required (DESIGN-0030) |
  | `checker` `TestPolicyCheckRepo_ContainsMode_AssertionsFail` | retired: v1 behaviour, no replacement — the `file` type has no contains mode (DESIGN-0031 D6): anything that looks inside a file is a control type |
  | `checker` `TestPolicyCheckRepo_ContainsMode_AssertionsPass` | retired: v1 behaviour, no replacement — the `file` type has no contains mode (DESIGN-0031 D6) |
  | `checker` `TestPolicyCheckRepo_ContainsMode_FileMissing` | `file` `TestConformance` (exists, missing) |
  | `checker` `TestPolicyCheckRepo_ContainsMode_RenovateConfigInvalidAssertion` | `dependency_updates` `TestConformance` (renovate config failing) |
  | `checker` `TestPolicyCheckRepo_ContainsMode_RenovateConfigValidAssertion` | `dependency_updates` `TestConformance` (renovate config passing) |
  | `checker` `TestPolicyCheckRepo_ExactMode_FileDiffersFromTemplate` | `file` `TestConformance` (exact mismatch) |
  | `checker` `TestPolicyCheckRepo_ExactMode_FileMatchesTemplate` | `file` `TestConformance` (exact match) |
  | `checker` `TestPolicyCheckRepo_ExactMode_FileMissing` | `file` `TestConformance` (exact, missing) |
  | `checker` `TestPolicyCheckRepo_ExactMode_RenovateWorkflowDrifted` | `file` `TestConformance` (exact mismatch) |
  | `checker` `TestPolicyCheckRepo_ExactMode_RenovateWorkflowMatchesTemplate` | `file` `TestConformance` (exact match) |
  | `checker` `TestPolicyCheckRepo_ExactMode_YAMLSemanticComparison` | `file` `TestConformance` (YAML compared semantically) + `TestRevisionGolden` |
  | `checker` `TestPolicyCheckRepo_ExistsMode_FileMissing` | `file` `TestConformance` (exists, missing) |
  | `checker` `TestPolicyCheckRepo_ExistsMode_FilePresent` | `file` `TestConformance` (exists, present) |
  | `checker` `TestPolicyEngine_LegacyMode_NoScopeFiltering` | resolution golden `baseline-only` |
  | `checker` `TestPolicyEngine_StrictMode_InScopeButIgnored` | resolution golden `exclude-repos` |
  | `checker` `TestPolicyEngine_StrictMode_OutOfScopeCounterByLevel` | retired: v1 behaviour, no replacement — `out_of_scope_total` was a rule-engine counter; an unmanaged org has no assignments to count |
  | `checker` `TestPolicyEngine_StrictMode_OutOfScopeWinsOverIgnore` | resolution golden `unmanaged-org` |
  | `checker` `TestPolicyEngine_StrictMode_PolicyScopeRejectsRepo` | resolution golden `unmanaged-org` |
  | `checker` `TestPolicyEngine_StrictMode_RuleSubsetApplies` | resolution golden `org-addition` |
  | `checker` `TestPolicyEngine_StrictMode_RuleUniversalApplies` | resolution golden `baseline-only` |
  | `checker` `TestReconciler_DoesNotRunWhenAssertionsFail` | `custom_properties` `TestSourceStates_ExactWrites` (an unparseable catalog writes nothing) |
  | `checker` `TestReconciler_DoesNotRunWhenFileMissing` | `custom_properties` `TestSourceStates_ExactWrites` |
  | `checker` `TestReconciler_DryRunPropagated` | retired: v1 behaviour, no replacement — there is no dry run: the evaluator holds a read-only App and never writes (DESIGN-0032) |
  | `checker` `TestReconciler_ErrorLoggedNotFatal` | `TestEvaluate_CountsOutcomesAndControlErrors` (one control's error does not fail the evaluation) |
  | `checker` `TestReconciler_MultipleRunInOrder` | retired: v1 behaviour, no replacement — reconcilers are gone; each became a control type evaluated independently; controls have no order |
  | `checker` `TestReconciler_RunsOnAbsence_OnlyForCapableReconcilers` | `custom_properties` `TestSourceStates_ExactWrites` (an absent catalog clears the managed set) |
  | `checker` `TestReconciler_RunsWhenAssertionsPass_ContainsMode` | `custom_properties` `TestConformance` |
  | `checker` `TestReconciler_RunsWhenFilePresent_ExistsMode` | `custom_properties` `TestConformance` |
  | `checker` `TestSettingRule_DefaultBranch_Remediate` | `repo_settings` `TestConformance` + write `TestUpdateRepository_WritesOnlySetFields` |
  | `checker` `TestSettingRule_Disabled` | retired: v1 behaviour, no replacement — a disabled rule is now an unassigned control (resolution golden `baseline-only`) |
  | `checker` `TestSettingRule_MatchesExpected_NoAction` | `repo_settings` `TestConformance` (passing) |
  | `checker` `TestSettingRule_Mismatch_NoRemediate` | `repo_settings` `TestConformance` (failing) + resolution golden `mode-org-control` |
  | `checker` `TestSettingRule_Mismatch_Remediate` | `repo_settings` `TestConformance` + write `TestUpdateRepository_WritesOnlySetFields` |
  | `checker` `TestSettingRule_Mismatch_Remediate_DryRun` | retired: v1 behaviour, no replacement — there is no dry run: the evaluator holds a read-only App and never writes (DESIGN-0032) |
  | `checker` `TestSettingRule_PerRuleIgnore` | resolution golden `exclude-with-reason` |
  | `checker` `TestSettingRule_VulnerabilityAlerts_Remediate` | `repo_settings` `TestConformance` + write `TestSetVulnerabilityAlerts` |
  | `checker` `TestParity_GoldensExist` | retired: v1 behaviour, no replacement — it guarded the golden set retired here |
  | `postgres` `TestCompliance_ParityAcrossReportAPIAndSnapshot` | `TestControlsStore_ComplianceSnapshot` + `TestCompliantPercent_SQLAgreesWithGo` + report `TestRender_Golden`; API parity returns with Phase 8 |
  | `checker`/`reconciler` `TestPRIdentity_IsFrozen` | retired: v1 behaviour, no replacement — retired deliberately: v1 and v2 adopting each other's PRs ends here; `tools/rgctl` (IMPL-0030) keeps its own literals |
- [x] 7.3 Delete `internal/checker`, `internal/reconciler`,
  `internal/rulepolicy`, `internal/findings`, `internal/shadow`,
  `cmd/repo-guardian/verify_shadow.go`, and `internal/rules` except
  whatever embedded fallback templates the template spike left there.
  Done: deleted
  - `internal/checker` (with the 101 parity goldens and `parity_test.go`);
  - `internal/reconciler`, `internal/rulepolicy` and `internal/shadow`;
  - `cmd/repo-guardian/verify_shadow.go` (the `migrate verify-shadow`
    dispatch and usage line);
  - `examples/examples_v1_test.go` (`examplesDir` moved into
    `examples_test.go`).

  `internal/rules` keeps `registry.go` and the embedded fallback templates
  that the controls catalogue resolves (`policy/catalogue.go` reads them
  through `rules.NewTemplateStore`): codeowners, catalog-info, dependabot,
  renovate and renovate-workflow. `set-custom-properties.tmpl` and its
  tests are gone, because the `custom_properties` control renders its own
  `workflow.yml.tmpl`. depguard's `workflows` rule loses the
  checker/reconciler entries. `internal/findings` goes in 7.4 with its
  importers (the store, the store's postgres package and the API), so the
  tree compiles at every commit.
- [x] 7.4 Re-cut the findings importers: `internal/store` (`findings.go`,
  `api_views.go`, `util.go`, `v2.go`), `internal/store/postgres`
  (`v2store*.go`, `apireader*.go`, `report.go`), `internal/activities`
  and `internal/report` onto `store.Controls`.
  Done (after 7.6, see there):
  - **Deleted.** `internal/findings`; `internal/store` `findings.go` and
    `v2.go` (the Writer/Reader interfaces and their generated mocks, plus
    the `.mockery.yaml` entry); `internal/store/postgres` `v2store*.go`,
    `identity.go` and their tests, `report_v2_integration_test.go`, the
    rc revert spike (its fix is tested on the controls chain), and the rc
    API queries (`queries/api_*.sql`, `status.sql`).
  - **`internal/store` now** holds only what the controls line reads: the
    API views and status inputs, `ParkReason` (now with `suspended`), the
    controls `ServiceRunKind`s, and a self-contained `Truncate`.
    `RepositoryDetail` is repository plus last check. `Event` carries
    control result transitions (`result_events` with `rule_id IS NULL`)
    beside repository events, so the spec's `Event` swapped its finding
    fields for `control_id` and string statuses, and the
    `RuleKind`/`FindingStatus`/`Reason`/`Remediation` schemas went.
  - **`APIReader`** reads `controlsdb` over new
    `queries_controls/api_repositories.sql` and `status.sql`.
    `TestAPIQueries_AreScoped` scans `queries_controls/`. `/orgs` sums the
    controls compliance query (`controlCompliance`, shared with
    `ControlsStore.ControlCompliance`). Its `open_prs` counts open
    pull-request remediations. Status rollout follows `activated_at`
    (D14), and rate snapshots are empty until 8.5.
  - **Already on `store.Controls`:** `internal/activities` and
    `internal/report`; neither needed changes.
  - **Tests.** The API integration tests run on a new controls fixture
    (`api_fixture_integration_test.go`: rg_evaluator,
    `seedTwoOrgs`/`evaluate`/`park`/`openPR`), and the perf seed was
    rewritten for the controls schema.
  - **Perf.** `TestAPIPerf` at 20k repositories and 200k assignments:
    every repository read is p99 < 2ms. `/orgs` measured p99 1.1s, mostly
    JIT, caused by the inlined posture CTE re-evaluating its bucket per
    FILTER. With `posture AS MATERIALIZED` it is p50 198ms / p99 319ms,
    still over the 100ms target. Phase 8's `/orgs` rebuild (8.1–8.3)
    owns closing that gap.
- [x] 7.5 Remove the rc schema code: `migrations_v2/`, the rc goose
  wiring, `adopt_v1.go`, `backfill_v1.go`, `dry_run.go`, `CheckV1Idle`,
  `pgtest/v1sql`, `migrations/` (golang-migrate), `queries/` and
  `sqlcdb/`, and the rc block of `sqlc.yaml`. `repo-guardian migrate`
  applies only the controls chain.
  Done: deleted `migrations_v2/`, `migrations/` (golang-migrate, now
  gone from go.mod), `queries/`, `sqlcdb/`, `pgtest/v1sql`, `goose.go`,
  `adopt_v1.go` (with `CheckV1Idle`), `backfill_v1.go`, `dry_run.go`,
  `migrate.go`, `migrate_context.go`, `export_test.go`, the rc
  `SchemaVersion`/`RequireSchema`, `pgtest.SeedV1`/`AppRole` and the
  adoption, backfill, rollback and goose tests. Also gone: the rc block
  of `sqlc.yaml` and the `sqlcdb` half of `make lint-sql`.
  - **What survived the move.** `OpenDB` lives in `controls_migrate.go`.
    `schema.go` holds only `RequireControlsSchema`, which the api role's
    readiness now uses too.
  - **`repo-guardian migrate`** applies only the controls chain. `--chain`
    defaults to `controls` and refuses `v2` with a message naming the
    removal; the flag stays because the chart still passes
    `--chain controls`. `--freshness`, `--force-running` and the backfill
    report are gone.
  - **Left for 7.8/7.9.** The chart's `args: ["migrate"]` branch (without
    `store.controls.enabled`) now also applies the controls chain, and
    `RECONCILE_FRESHNESS` on the Job is ignored.
- [x] 7.6 API: remove `/rules`, `/rules/{kind}/{name}`, `/findings`,
  `/policy`, `/installations`, `/orgs/{org}`, `/compliance/history`,
  `/summary` and the `Repository` fields `installation_id`,
  `last_check_outcome` and `policy_version` from `api/openapi.yaml`, with
  their handlers and queries; `/me`, `/status` and `/openapi.yaml` stay.
  `make generate-api`. Phase 8 adds the D32 replacements.
  Done (ahead of 7.4 and 7.5, which cannot compile while handlers for
  the removed endpoints still read the findings tables): the eight paths
  are gone from `api/openapi.yaml`, along with the three `Repository`
  fields and every component they alone referenced (Summary, the rule,
  finding and evidence schemas, Installation, History, Policy, the
  RuleKind/RuleName/StaleAfter parameters). `RepositoryDetail` is now
  `repository` plus `last_check`: its `installation` and `findings` read
  tables that 7.4 deletes, and 8.1 rebuilds the view with `/controls`.
  `make generate-api` regenerated `internal/api/gen`; with fewer schemas
  the rollout enum constants lost their prefix (`gen.None`/`InProgress`/`Complete`).
  Removed the handlers, the `Reader` methods, `Options.StaleAfter`, the
  reader methods in `postgres.APIReader`, and the queries `api_findings`,
  `api_history`, `api_installations`, `api_rules` and `api_summary`. The
  two org queries that `/orgs` still uses moved to `queries/api_orgs.sql`
  until 7.4 re-cuts `/orgs` onto controls compliance. `PR_STALE_AFTER`
  stays in config with no reader until IMPL-0030's PR views. The unit
  scoping tests now drive `/repositories`, and the integration tests for
  the removed endpoints are deleted. The generated UI types go stale here;
  7.7 removes those views.
- [x] 7.7 UI: remove the Findings, Rules, Orgs, History, Policy and
  Repository views and the code only they use; the Status view and the
  shell stay; `make generate-ui-api`, `make lint-ui`, `make test-ui` and
  the e2e suite (reduced to status and auth) green.
  Done: the Findings, Rules, Orgs, History, Policy and Repository views
  went, and so did Fleet, which read `/summary`, `/rules` and history.
  Also removed: the ⌘K palette (it searched rules, orgs and repositories,
  and navigated to the deleted routes), the sidebar's Findings count and
  policy footer, and the code only those views used (`filters/`,
  `OrgScope`, `RangeTabs`, `timeline`, and `lib/{csv,pr,range,timeline,evidence}`
  with their tests). `queries.ts` keeps `useUIConfig`, `useMe` and
  `useStatus`; `viewmodel.ts` keeps `initials`, `hasNoAccess` and
  `heatOpacity`. The domain-free `components/ui/` primitives (and
  `lib/search`, which `tabs` uses) stay for the controls views.
  `/` inside the shell renders the Status view (nav: Overview, Status page).
  e2e: `seed.sql` was rewritten for the controls schema, and
  `e2e/roles.sql` creates `rg_evaluator`/`rg_remediator` before
  `migrate --policy=`, because the controls chain grants to them and never
  creates them. The suite is down to sign-in, session, public status,
  sign-out, no-Docs and axe (status, signed-in overview). The four removed
  reference screenshots were deleted. Results: `make lint-ui` green,
  `make test-ui` 239 pass, `make test-ui-e2e` 7 pass and 1 skipped.
  `status.png` was not re-rendered: the visual spec runs only in the
  pinned image (`make test-ui-e2e-update-snapshots`) and the seed changed,
  so it needs a refresh there.
- [x] 7.8 Configuration: `GUARDIAN_CONFIG` names the policy directory;
  `EVAL_INTERVAL` replaces `CHECK_INTERVAL`; audit every env var in
  `_helpers.tpl`'s list that only the rule engine read (at least
  `DRY_RUN`, `SKIP_FORKS`, `SKIP_ARCHIVED`, `AUTO_CLOSE_PR`,
  `ORPHAN_CLEANUP`, `TEMPLATE_DIR`, `RECONCILE_FRESHNESS`,
  `PR_STALE_AFTER`) and give each a fate in this task: kept, renamed, or
  removed with IMPL-0028's warn-and-ignore pattern.
  Done. Fates:
  - **Kept:** `GUARDIAN_CONFIG`, which now names the policy directory (doc
    comment; the evaluator and ingest already `policy.Load` it).
  - **Renamed:** `CHECK_INTERVAL` → `EVAL_INTERVAL` (`Config.EvalInterval`).
  - **Removed** (field and parse dropped, added to `removedEnvVars` under a
    new `runbookControls` anchor):
    - `GITHUB_APP_ID`, `GITHUB_PRIVATE_KEY_PATH`, `GITHUB_PRIVATE_KEY` and
      `GITHUB_WEBHOOK_SECRET`. Ingest's refusal of the rc key went with
      them; it still refuses `EVAL_`/`REMEDIATE_` keys.
    - `TEMPLATE_DIR`, `SKIP_FORKS`, `SKIP_ARCHIVED` and `DRY_RUN`.
    - `AUTO_CLOSE_PR`, `ORPHAN_CLEANUP`, `STRICT_TEMPLATES`,
      `RECONCILE_FRESHNESS` and `PR_STALE_AFTER` (with its 1h–8760h
      validation).
    - `STORE_POSTGRES_MAX_CONNS` (replaced by `*_DB_POOL_SIZE`).
    - `WORKER_ACTIVITY_CONCURRENCY` (replaced by `*_CONCURRENCY`).
      `temporal.WorkerConfigFromEnv` now reads only `TEMPORAL_BUILD_ID`
      and returns no error.
  The fates table is the new `docs/operations/v2-migration.md#controls-environment-variables`.
  `TestLoadRole_IgnoresRemovedVars` sets each removed var to an
  unparseable value. The chart still emits several of these until 7.9, so
  they only warn.
- [x] 7.9 Chart (OQ11): `policy.files` renders the policy directory into
  one ConfigMap mounted at `GUARDIAN_CONFIG` on the ingest and evaluator
  roles (ingest needs `WatchedPaths()`); `policy.existingConfigMap` stays;
  `templates.*`, `policy.config` and the rule-engine knobs are rejected
  by name through `values.schema.json` and `validateRemovedValues` with
  the migration URL; helm-unittest for rendering, mounts and each removed
  key; namespace check passes.
  Done:
  - **Policy ConfigMap.** Per OQ11 (a), `policy.files` is a nested map
    by directory: a string is a file and a map is a directory.
    `repo-guardian.policyWalk` flattens it into one ConfigMap
    (`policy-configmap.yaml`), keyed by the path joined with dots
    (`catalogue.codeowners.hcl`). The `repo-guardian.policyVolume` items
    put each file back at its path, so `catalogue/` and `orgs/` survive
    the kubelet `..data` mount. The walk refuses an empty name, `.`,
    `..` and any name holding `/`. `validatePolicyFiles` refuses two
    paths that fold to one key, and `files` beside `existingConfigMap`.
  - **Mounts.** `GUARDIAN_CONFIG=/etc/repo-guardian/policy` is set on
    ingest, the evaluator, the migrate Job, and also the remediator,
    because `config.validateControlsWorker` requires `GUARDIAN_CONFIG` for
    both workers. `existingConfigMap` stays and is mounted whole.
  - **Removed.** `templates.*` (its ConfigMap went), `secrets.*` (the rc
    Secret and every `GITHUB_*` env went), `policy.config`,
    `policy.autoClosePR`, `policy.orphanCleanup`, `config.appId`,
    `config.dryRun`, `config.skipForks`, `config.skipArchived`,
    `templating.strict`, `worker.replicas`, `worker.concurrency`,
    `worker.resources`, `checkInterval` (now `evalInterval`),
    `store.postgres.maxConns`, `migrate.freshness` and `api.prStaleAfter`.
    Each is rejected by a schema `const` and by `validateRemovedValues`,
    with `v2-migration.md#controls-chart-values`.
  - **Schedule env bug fixed.** The schedule env (`EVAL_INTERVAL`,
    discovery, retention, snapshot) was gated on the deleted `worker` role,
    so a split evaluator got none of it. It now rides on
    `roleHasEvalKey`.
  - **Other chart edits.** `reservedEnvVars` was rebuilt. `NOTES.txt`
    names the two webhook routes. The Makefile helm targets and
    `ci-values.yaml` use `policy.files`. The README template was updated
    (Quick Start, shapes, editing the policy, upgrade notes).
  - **Tests.** The `secret` and `configmap` suites went. The `policy`
    suite was rewritten (11 tests). `values_guard` gained one test per
    removed key. helm-unittest: 241 pass; `helm lint` and
    `make lint-alerts-chart` (15 rules) green. The namespace check passes
    for every kind.
- [x] 7.10 Delete `examples/guardian-*.hcl` and the `guardian-multi-org/`
  directory; `examples/values-*.yaml` move to the policy map.
  Done: the nine `guardian-*.hcl` files and `guardian-multi-org/` are gone. `values-with-policy.yaml` and `values-multi-org.yaml` are chart values on `github.eval` with a nested `policy.files` map mirroring `examples/policy/full` and `multi-org` byte for byte, pinned by `TestValuesExamples_MatchPolicyRoots` (verified non-vacuous: a one-character edit fails it). `examples/README.md` lost its v1 sections; live references in README.md, docs/index.md and the multi-org root now point at `examples/policy/`. Both values files render.
- [x] 7.11 Non-vacuous check: re-register `RepoWorkflow` and confirm the
  replay and registration tests notice; set a removed chart key and
  confirm render fails; restore.
  Done: registering an `EvaluationWorkflow` under the name `RepoWorkflow` in `evaluatorSet` fails `TestRegister_EachRoleRegistersOnlyItsSet`; restoring the rc `check_then_park.json` history fails `TestReplay_CapturedHistories` ("unable to find workflow type: RepoWorkflow"). `--set config.dryRun=true` fails render at the schema (`const` naming `v2-migration.md#controls-chart-values`) and, with `--skip-schema-validation`, at `validateRemovedValues`. All restored; the workflows package and a plain render pass.
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

- [x] 8.1 Spec: add `/controls`, `/controls/{id}`, `/policies`,
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
  Done: `api/openapi.yaml` 2.0.0 adds `/summary`, `/controls`, `/controls/{id}`, `/policies`, `/orgs/{org}`, `/repositories/{id}/controls`, `/repositories/{id}/evaluations` (was `/checks`), `/compliance/history` (keyed by control) and `POST /repositories/{id}/evaluate` (202, 404, `409` with problem `type` `workflow_missing`/`parked`, `501`). `Repository` gains `policy_state`, `policy_reason`, `evaluable`, `remediable` and `last_evaluated_at`. Every compliance object is one `Posture` schema: the ten buckets, `assigned`, and nullable `percent` and `coverage`. `ControlOrg` carries `versions[] {version, revision, repositories}`. `Evidence` is a `oneOf` of `FrameworkEvidence` (`evidence_kind` const `framework`) and `ControlEvidence`. `evidence_kind` uses the shape the control types actually write, `<type>.<kind>` with a dot, not the slash in this task's text. There is no assignment-event table, so the timeline gains `rule_id` and shows a withdrawal as a result event with no `to_status`. `/remediations` and the remediation fields are left to IMPL-0030. `StatusCounts` is gone (`store.ControlPosture.Plus` sums posture rows with the new `store.FloorPercent`). New handlers answer 501 until 8.3. `make lint-api` and `make lint-ui` are green.
- [x] 8.2 Queries in `queries_controls/api_*.sql`, each with the scope
  predicate; `TestAPIQueries_AreScoped` scans the new directory.
  Done: `queries_controls/api_controls.sql` adds `APIControlVersions`, `APIRepositoryControls` (its bucket is `ControlCompliance`'s CASE), `APIRepositoryRuleResults`, `APIComplianceHistory` (each UTC day's last snapshot per org, control and lineage) and `APISummaryEvaluations`. `api_repositories.sql` gains `policy_state`/`policy_reason`/`evaluable`/`remediable` and a `policy_state` filter. `last_evaluated_at` is the newest successful `checks` row, because `last_checked_at` is never written on the controls line. `/checks` became `APIRepositoryEvaluations`, and the events query adds rule-level transitions with `rule_id`. All are scoped, and `TestAPIQueries_AreScoped` already scanned `queries_controls/api_*.sql`. `ActivePolicy` is global, in `policy.sql`. Reader methods are in `apireader_controls.go` and the `api.Reader` interface carries them.

  Found a real bug: the read policies on `app_installations`, `app_repository_access` and `remediations` named only `rg_evaluator, rg_remediator`. The api's read-only role (`pg_read_all_data` does not bypass RLS) therefore saw no rows, so `evaluable`/`remediable`, the suspension bucket and open-PR counts all read empty through the API. The policies are now `FOR SELECT USING (true)` (PUBLIC), and table grants still decide who reads. 00002/00003 are unreleased, so they were edited in place. `pgtest` gains `ReadOnlyRole` (`rg_api_ro`, granted SELECT on every table after the chain, as the chart's hook does), and every API integration test now reads as it. It was reading as `rg_evaluator`, which is what hid this.
- [x] 8.3 Handlers in `internal/api` (`controls.go`, `policies.go`, and
  rewrites of `resources.go` and `compliance.go`). `/policies` filters
  the stored `Summarize()` output in Go to the principal's visible orgs,
  with enterprise-wide fields only for a principal that sees every org;
  `TestPoliciesSummary_IsScoped` asserts a single-org principal sees no
  other org.
  Done: `internal/api/controls.go` serves `/summary`, `/controls` (grouped by slug, totals summed with `ControlPosture.Plus`, title and type from the newest catalogue version), `/controls/{id}` (per-org rows by lineage, each with its `versions`), `/orgs/{org}` (baseline is baseline plus replaced, `org_added` the rest; up to five measured controls with the lowest percent; exclusions), `/compliance/history` (1–365 days, slug validated) and `/repositories/{id}/controls` (stored evidence unmarshals into the `Evidence` union). `policies.go` filters the stored `Summarize()` output in Go: the visible orgs, the catalogue entries their layers refer to, and `enterprise` only for an all-orgs scope. `resources.go` gains the `policy_state` filter (bound to the cursor) and `compliance.go` sums posture. `TestPoliciesSummary_IsScoped` asserts a team-web principal sees only acme-web, no enterprise block, only `labels@1`, and no byte of acme-pay's policy. `/policies` answers 404 before activation.
- [x] 8.4 `POST /repositories/{id}/evaluate`: visibility through
  `APIScope` first; `recheck` at priority 1 to `evaluation/<id>`; `202`
  with no body; `409 workflow_missing`, `409 parked`; `501` without
  `TEMPORAL_ADDRESS`; dedupe per OQ12. The UI BFF proxies this one
  `POST` with the Origin check it applies to logout
  (`ui/server/proxy.ts`).
  Done: `internal/api/evaluate.go`. Visibility comes first through `Reader.Repository` (404). Then: `501` without an `Options.Rechecker`; `409` problem type `parked` for an inactive repository; and a per-replica one-minute dedupe (OQ12 (a)) whose suppressed requests answer `202` without signalling. A failed signal releases its claim, so a retry is not swallowed; `ErrWorkflowMissing` becomes `409 workflow_missing`. Problems gained a `type` (`typedError`). In cmd, `evaluationRechecker` signals `recheck` `{manual, PriorityHuman, Unknown: true}` to `evaluation/<id>` through a one-method `signaler` and maps `serviceerror.NotFound` to `ErrWorkflowMissing`. The api role dials Temporal only when `TEMPORAL_ADDRESS` is set, and then without a `temporal` readiness check, so an outage never takes the read API out of rotation. The chart gains `api.temporal` (default off; `roleDialsTemporal` includes api only with it). The BFF proxies exactly `POST /api/v1/repositories/<n>/evaluate` under the logout's Origin check (403 cross-origin or no Origin); every other non-GET stays 405. Tests: `TestEvaluate_*` (501 after 404, once per minute, both 409s, claim release, 500), `TestEvaluationRechecker_*`, two proxy tests, and a secret-scoping chart test.
- [x] 8.5 `/status`: both queues' backlog through `DescribeTaskQueue`,
  each App's installations from `app_installations`, `topology`,
  evaluation freshness and coverage.
  Done: `Status` gains `topology` (split|all), `apps[]` (`StatusApp`: eval|remediate with installation, suspended and reachable-repository counts; an unknown App is dropped, never echoed), `compliance.coverage`, and `operations.evaluated_24h`/`overdue` (managed repositories with no success in 2× `EVAL_INTERVAL`). New `StatusApps`/`StatusOverdue` queries; `StatusInputs(ctx, now, freshness)` sums the posture with `ControlPosture.Plus`. The dead `github_budget` component, `RateSnapshot` and `RateReserve` are gone (the controls schema stores no rate snapshots), as is the rc `backlog` component. The api role with a client probes the queue of each configured App; the chart gives it the App id alone under `api.temporal` (secret_scoping test). Privacy guard allowlists the two new enums.
- [x] 8.6 `apitest` with kin-openapi response validation for every
  endpoint; `make generate-api` and `make lint-api` green.
  Done: `TestEndpoints_ValidateAgainstTheSpec` now drives every operation (16, including `POST /evaluate` → the spec's 501 without a Temporal client, `/status`, `/openapi.yaml`) against Postgres through `apitest`'s kin-openapi validation, and fails when the spec gains an operation with no case. It caught a fixture writing evidence without `evidence_kind`/`evidence_version` (which `control.Validate` requires of real evidence); fixed. `make generate-api` and `make lint-api` green.
- [x] 8.7 UI views: Controls, Control (per-org breakdown with versions),
  Orgs and Org (baseline versus org, coverage), Fleet linking to
  controls, Repository with the controls tab, evidence and the
  evaluation log, History by control, Policy on `/policies`, Status with
  queues, Apps and topology; the re-evaluate button hidden on `501`; a
  null percent renders "no data". `make generate-ui-api`, `make lint-ui`,
  `make test-ui`.
  Done: routes `/` (Fleet: summary KPIs, assignment distribution, controls and orgs linking through), `/controls` + `/controls/$id` (per-org/lineage breakdown with versions and revisions), `/orgs` + `/orgs/$org` (baseline vs org-added percent and coverage, controls, worst five, exclusions), `/repositories` (org/name/policy-state filter in the URL) + `/repositories/$id` (tabs: controls with rule results and text-only evidence, evaluation log, events; facts card), `/history` (fleet or one control, 7/30/90d, snapshots summed per day with the SQL floor math), `/policy` (version, enterprise for all-org callers, each org layer, catalogue). Status gains the Deployment card (topology + each App's counts), coverage and overdue; the queues are its `backlog_*` components. Re-evaluate hides once the POST answers 501 (cached for the session). A null percent renders "no data" everywhere (`formatPercent`, meters). `lib/posture.ts` and `lib/evidence.ts` with tests; nav, breadcrumbs (item crumbs) and the test harness paths updated. `make generate-ui-api`, `lint-ui`, `test-ui` (254 pass) green.
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
