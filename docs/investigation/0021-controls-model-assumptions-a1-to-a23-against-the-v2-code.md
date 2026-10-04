---
id: INV-0021
title: "Controls model assumptions A1 to A23 against the v2 code"
status: Open
author: Donald Gifford
created: 2026-10-04
---

<!-- markdownlint-disable-file MD025 MD041 -->

# INV-0021: Controls model assumptions A1 to A23 against the v2 code

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [A1 — RepoWorkflow hosts the evaluation loop](#a1--repoworkflow-hosts-the-evaluation-loop)
  - [A2 — Budget keyed by installation id alone](#a2--budget-keyed-by-installation-id-alone)
  - [A3 — Discovery, the un-parker and the subset invariant carry over](#a3--discovery-the-un-parker-and-the-subset-invariant-carry-over)
  - [A4 — The repositories table and identity matching are reusable](#a4--the-repositories-table-and-identity-matching-are-reusable)
  - [A5 — Findings tables are rule-keyed and are replaced](#a5--findings-tables-are-rule-keyed-and-are-replaced)
  - [A6 — The checks table becomes the evaluation log](#a6--the-checks-table-becomes-the-evaluation-log)
  - [A7 — Policy versions, VersionV2 and rollout re-pointed at the catalogue](#a7--policy-versions-versionv2-and-rollout-re-pointed-at-the-catalogue)
  - [A8 — The checker engine is removed, not adapted](#a8--the-checker-engine-is-removed-not-adapted)
  - [A9 — The HCL loader is replaced; small pieces survive](#a9--the-hcl-loader-is-replaced-small-pieces-survive)
  - [A10 — The template package is reused](#a10--the-template-package-is-reused)
  - [A11 — The catalog parser is the core of the catalog_info type](#a11--the-catalog-parser-is-the-core-of-the-cataloginfo-type)
  - [A12 — The client layer survives; the interface splits](#a12--the-client-layer-survives-the-interface-splits)
  - [A13 — Ingest and WebhookWorkflow survive; pull_request is added](#a13--ingest-and-webhookworkflow-survive-pullrequest-is-added)
  - [A14 — API structure and UI shell survive](#a14--api-structure-and-ui-shell-survive)
  - [A15 — Roles and chart secret scoping extend to evaluator and remediator](#a15--roles-and-chart-secret-scoping-extend-to-evaluator-and-remediator)
  - [A16 — No production histories; new workflow type names need no gates](#a16--no-production-histories-new-workflow-type-names-need-no-gates)
  - [A17 — Frozen PR identity ends; cutover closes v1's PRs](#a17--frozen-pr-identity-ends-cutover-closes-v1s-prs)
  - [A18 — TaskPriority applies unchanged](#a18--taskpriority-applies-unchanged)
  - [A19 — Posture export and dashboards regenerate from control results](#a19--posture-export-and-dashboards-regenerate-from-control-results)
  - [A20 — Shadow verification and the v1 backfill are not carried over](#a20--shadow-verification-and-the-v1-backfill-are-not-carried-over)
  - [A21 — GitHub reads CODEOWNERS from .github/, then root, then docs/](#a21--github-reads-codeowners-from-github-then-root-then-docs)
  - [A22 — The CODEOWNERS errors endpoint is readable with read permissions](#a22--the-codeowners-errors-endpoint-is-readable-with-read-permissions)
  - [A23 — The Evaluation App's read set covers every built-in read](#a23--the-evaluation-apps-read-set-covers-every-built-in-read)
  - [Keep, adapt, remove](#keep-adapt-remove)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
  - [OQ1: How is installation suspension modelled under the controls model?](#oq1-how-is-installation-suspension-modelled-under-the-controls-model)
  - [OQ2: Does repositories.installationid stay once apprepository_access exists?](#oq2-does-repositoriesinstallationid-stay-once-apprepositoryaccess-exists)
  - [OQ3: How are live RepoWorkflow executions retired when the build stops registering the type?](#oq3-how-are-live-repoworkflow-executions-retired-when-the-build-stops-registering-the-type)
- [References](#references)
<!--toc:end-->

## Question

Do the twenty-three assumptions that DESIGN-0029 makes about the v2 code (A1 to A23, section "Assumptions about the current v2 code") hold against the code on this branch? Each assumption decides whether a v2 package is kept, adapted or removed under the controls model, so a wrong one changes the implementation plan. This investigation checks every assumption against the actual files, names the test that pins each behaviour where one exists, and records the correction DESIGN-0029 to DESIGN-0032 need where the code disagrees.

## Hypothesis

Most assumptions hold, because DESIGN-0029 was written against the IMPL-0025 summary in `CLAUDE.md`, which the code follows closely. The ones most likely to need a caveat are the cross-cutting ones: the single `github.Client` interface (A12), the single-App shape of discovery, ingest and the chart (A3, A13, A15), and the posture export (A19), which on the v2 line may not run at all. The three GitHub-behaviour assumptions (A21 to A23) cannot be settled from code and will need a homelab probe.

## Context

DESIGN-0029 replaces v1's rule model with controls and splits evaluation from remediation across two GitHub Apps. Its register of assumptions is marked unverified, and the design says a follow-up investigation checks each one before the implementation plan is written. The keep-versus-replace split in DESIGN-0029 ("What happens to v1 concepts") and the overview's keep-versus-replace material rest on the same register. The code under test is the `v2` line at chart `2.0.0-rc.4`, which still runs v1's rule engine inside the `CheckRepo` activity (IMPL-0025 Phase 4).

**Triggered by:** DESIGN-0029, DESIGN-0030, DESIGN-0031, DESIGN-0032

## Approach

1. Read the current assumptions table in DESIGN-0029 and, where an assumption's purpose was unclear, the section of DESIGN-0030, DESIGN-0031 or DESIGN-0032 that depends on it.
2. For each assumption, locate the code it describes with `grep` and read the relevant functions, schema and tests. Record file paths with line numbers and the name of the test that pins the behaviour.
3. Give a verdict per assumption: holds, holds with caveat, does not hold, or not verifiable from code. Where the code disagrees with the design text, quote the assumption's own "If wrong" column and state the correction.
4. Derive a keep, adapt, remove table for every package under `internal/`, `cmd/`, `ui/` and `charts/` from the verdicts.
5. Raise the decisions that are the maintainer's, not the investigation's, as open questions.

The design documents were being edited while this investigation ran. Verdicts are against the code; corrections are against the design text as it read on 2026-10-04 and are phrased so that a correction already applied reads as confirmed.

## Environment

| Component | Version / Value |
| --------- | --------------- |
| Branch | `docs/controls-and-policies` on the `v2` line, commit `660e8ea` |
| Chart / appVersion | `2.0.0-rc.4` (`charts/repo-guardian/Chart.yaml:7-8`) |
| Go | `1.26.6` (`go.mod:3`) |
| go-github | `v68.0.0` (`go.mod:16`) |
| Temporal SDK | `go.temporal.io/sdk v1.49.0` (`go.mod:39`) |
| goose | `v3.28.0` (`go.mod:21`), v2 `SchemaVersion = 3` (`internal/store/postgres/schema.go:13`) |
| HCL | `hashicorp/hcl/v2 v2.24.0` (`go.mod:18`) |

## Findings

Verdict counts: 10 hold, 10 hold with caveat, 0 do not hold, 3 are not verifiable from code (A21, A22, A23). No assumption is wrong in a way that changes the model. Six caveats change the implementation plan: discovery and ingest are single-App today (A3, A13), the client split adds methods rather than partitioning existing ones (A12), the chart and config model one App (A15), the posture exporter does not run on v2 at all (A19), and the replay suite fails closed when its fixtures go (A16).

### A1 — RepoWorkflow hosts the evaluation loop

**Verdict:** holds with caveat.

- `internal/workflows/repo.go:25-54` is the loop: `wait` (timer plus signals, `repo.go:91-118`), `drain` for coalescing (`repo.go:140-151`), `onRecheck` keeps the lowest priority key (`repo.go:155-159`), `onPolicyChanged` spreads the next due time across the window (`repo.go:163-176`), ContinueAsNew at `maxIterationsPerRun = 100` or on suggestion (`repo.go:15`, `repo.go:29-37`).
- The check activity is reached by name only: `CheckRepoActivity` (`internal/workflows/names.go:27`) executed at `repo.go:214`. Swapping in an `Evaluate` activity is a constant and a payload change. The depguard rule that keeps `internal/workflows` free of engine imports (`.golangci.yml`, the `internal/checker` deny entry) still holds for the new activity.
- Pinned by `TestRepoWorkflow_TwentySignalsAtMostTwoChecks`, `TestRepoWorkflow_HighestPriorityWins`, `TestRepoWorkflow_PolicyChangedPullsNextDueIn`, `TestRepoWorkflow_ContinueAsNewCarriesState` and `TestRepoWorkflow_DeferredConsumesNoRetryAttempt` (`internal/workflows/repo_test.go:52-200`).
- Caveat: the loop body is more than a timer and signals. One iteration also acquires and reports a budget lease (`repo.go:207`, `repo.go:225-230`), sleeps on a durable timer for a `CheckDeferred` result (`repo.go:233-240`) and parks (`repo.go:241-242`). `ContinueAsNew` names `RepoWorkflowName` literally (`repo.go:36`), so a workflow under a new type name (A16) must pass its own name.

**Consequence:** keep the `repoLoop` pattern and its tests; register it under the new `EvaluationWorkflow` name with an `Evaluate` activity and a parametrised ContinueAsNew target. The remediation workflow in DESIGN-0032 needs the budget, defer and park steps too, so the loop should become a shared helper rather than being copied twice. The "If wrong" column (copied rather than reused) does not apply.

### A2 — Budget keyed by installation id alone

**Verdict:** holds.

- `InstallationWorkflowID(installationID)` returns `installation/<id>` (`internal/workflows/names.go:80-82`). `InstallationWorkflowInput` carries `InstallationID`, `Threshold`, `LeaseTTL`, `MaxHandled` (`internal/workflows/installation.go:65-77`); `BudgetState` has no App field (`installation.go:43-56`).
- Every producer and consumer keys by id alone: the report signal (`repo.go:302`), the `AcquireBudget` activity (`internal/activities/budget.go`), the suspend signal from webhook routing (`internal/activities/route.go:224`), and the load tester (`cmd/rg-burst`, `InstallationWorkflowID(*installation)` at line 76).
- The `installations` table is keyed by `installation_id` with no App column (`internal/store/postgres/migrations_v2/00002_v2_schema.sql:8-20`) and carries the persisted rate snapshot (`rate_limit`, `rate_remaining`, `rate_reset_at`, `rate_observed_at`).

**Consequence:** the design's change is the one it expects: the id gains an App segment, the state records the App, and five call sites change (`names.go`, `repo.go:302`, `activities/budget.go`, `route.go:224`, `cmd/rg-burst`). GitHub installation ids are unique across Apps, so the App segment is for routing and readability, not collision avoidance. The `installations` table is superseded by `app_installations` per DESIGN-0030 AR-0030-04; DESIGN-0032's migration list still described `00004_controls_policy` as adding `installations.app` when read, and should name `app_installations` and `app_repository_access` instead.

### A3 — Discovery, the un-parker and the subset invariant carry over

**Verdict:** holds with caveat.

- `DiscoveryWorkflow` lists installations, then per installation lists, upserts in batches of 100 and parks the unseen (`internal/workflows/service.go:59-137`). A failed listing parks nothing (`service.go:109-114`, `TestDiscoveryWorkflow_FailedListingParksNothing`).
- `UpsertDiscovered` is the only un-parker: `ReactivateRepository` is "the only statement that sets active = true" (`internal/store/postgres/queries/repositories.sql:29-33`), called from `internal/store/postgres/v2store_ops.go:176-180` with an `unparked` event.
- The subset invariant is explicit: `Services.skip` filters on the same `SkipArchived` / `SkipForks` fields the check path parks on (`internal/activities/services.go:39-44`, `services.go:126-129`), and `TestListRepositories_SubsetInvariant` drives the real engine to prove it (`internal/activities/services_test.go:93`).
- The nil-versus-empty parking rule survives as a boolean: `classify` sets `ClearFindings = true` only for durable skips and leaves access-denied findings in place (`internal/activities/check.go:140-157`); `Park(reason, clearFindings)` (`v2store_ops.go:78`); pinned by `TestPark_AccessDeniedKeepsFindings` and `TestPark_ArchivedClearsFindingsWithEvents` (`internal/store/postgres/v2store_contract_integration_test.go:63-123`).
- Caveat 1, resolution placement: `UpsertRepositories` both upserts and starts the `RepoWorkflow` with `policy_changed{By: due}` for every new or reactivated row, inside the same activity (`services.go:135-170`). A resolution step "after upsert" must sit between the upsert and the start inside that activity, or the first evaluation runs with no assignments.
- Caveat 2, one App: `ListInstallations` lists the installations of the one `GitHub` client the `Services` hold (`services.go:80-103`), and `DiscoveryInput` has only `InstallationID` (`internal/workflows/service_types.go:15-18`). The Remediation App's listing-only discovery from DESIGN-0030 AR-0030-04 needs an `App` field on the input, a second client, and a worker on the remediate queue.
- Caveat 3, `suspended` parks: DESIGN-0029 A3 names a `suspended` park that keeps results. Today suspension is a budget signal, not a park: `installation.suspend` upserts `SuspendedAt` and signals the `InstallationWorkflow` (`route.go:71`, `route.go:224-247`), and `park_reason` has no `suspended` value (`00002_v2_schema.sql:31-32`).

**Consequence:** keep the workflow, the un-parker and the invariant test. Adapt `UpsertRepositories` to resolve before starting, add the App dimension to `DiscoveryInput` and `Services`, and decide how suspension is modelled (OQ1). The "If wrong" column (resolution may need its own workflow) does not apply; it fits inside the existing activity.

### A4 — The repositories table and identity matching are reusable

**Verdict:** holds with caveat.

- `matchRepository` locks by provider id first, then by name among rows without a conflicting id, and returns `ErrIdentityConflict` on a reused name (`internal/store/postgres/identity.go:29-65`). `applyIdentity` writes `transferred` on an org change, `renamed` on a name change, nothing on a case-only change, and never touches `active` (`identity.go:72-118`).
- Pinned by `TestIdentity_NullIDFilledOnFirstMatch`, `TestIdentity_RenameKeepsIDAndHistory`, `TestIdentity_TransferUpdatesOrgAndInstallation`, `TestIdentity_CaseOnlyRenameWritesNoEvent`, `TestIdentity_NameHeldByAnotherIDConflicts`, `TestRecordCheck_NeverSetsActive` (`internal/store/postgres/v2store_identity_integration_test.go:52-214`).
- Caveat 1: `repositories.installation_id` is `NOT NULL REFERENCES installations` (`00002_v2_schema.sql:29`) and `applyIdentity` rewrites it on transfer (`identity.go:98-99`). Under per-App `app_repository_access` the column is either the Evaluation App's installation or redundant; the designs do not say which (OQ2).
- Caveat 2: three columns are rule-engine posture that DESIGN-0032 drops at cutover (`last_check_outcome`, `policy_version`, `catalog_parse_ok`, DESIGN-0032 "Data Model"). They are written today by `updateRepositoryAfterCheck` and threaded through `internal/activities/check.go:83`, `check.go:101`, `internal/activities/record.go:27`, so those writes go with the engine.
- Caveat 3: the `repository_events.kind` CHECK (`00002_v2_schema.sql:118-119`) lacks `assignment_changed`; DESIGN-0030 already notes this.

**Consequence:** keep `identity.go` and its tests unchanged. The "If wrong" column (identity redone) does not apply. Settle the `installation_id` column in DESIGN-0030 or DESIGN-0032 before migration `00004` is written.

### A5 — Findings tables are rule-keyed and are replaced

**Verdict:** holds with caveat.

- `findings` is keyed `(repository_id, rule_kind, rule_name)` with `rule_kind IN ('file', 'setting', 'branch_protection')` (`00002_v2_schema.sql:52-67`). `finding_events` (`00002_v2_schema.sql:94-109`) and `compliance_snapshots` (`00002_v2_schema.sql:146-156`) carry `rule_kind` / `rule_name` too.
- Caveat: the `rule_kind` CHECK is on `findings` only. `finding_events.rule_kind` and `compliance_snapshots.rule_kind` are unconstrained `TEXT`. The Go side mirrors the key: `findings.RuleKind` (`internal/findings/findings.go:11-19`) and `store.ComplianceCount.Kind` (`internal/store/findings.go:234-236`).
- The replacement must carry two properties the current tables have: the append-only grant on `finding_events` (`00002_v2_schema.sql:163-168`, pinned by `TestFindingEvents_AppendOnlyUnderApplicationRole`, `v2store_contract_integration_test.go:124`) and the "evidence-only change writes no event" rule (`TestRecordCheck_EvidenceOnlyChangeWritesNoEvent`, `v2store_integration_test.go:220`). DESIGN-0032's `00005_controls_results` already names the revokes.
- Everything above the tables is rule-shaped and goes with them: the `/rules`, `/rules/{kind}/{name}` and `/findings` endpoints (`api/openapi.yaml:75-225`), `queries/api_rules.sql`, `queries/api_findings.sql`, the UI views `rules.tsx` and `findings.tsx`, and the per-reason evidence renderer `ui/web/src/lib/evidence.ts`.

**Consequence:** replace, as assumed. The "If wrong" column (a migration keeping findings rows) does not apply because A20 starts from a fresh evaluation. State in DESIGN-0032 that the new tables constrain their kind or control-id columns at every level, not only on the current-state table.

### A6 — The checks table becomes the evaluation log

**Verdict:** holds.

- `checks` has `check_key TEXT NOT NULL UNIQUE`, `pending_result JSONB` for the staged outcome set and `finished_at` (`00002_v2_schema.sql:72-91`). `StageCheck` upserts the staged payload on conflict; `FinalizeCheck` clears it (`internal/store/postgres/queries/checks.sql:6-25`).
- `V2Store.StageCheck` and `RecordCheck` implement the handoff; a retry with a final key returns the stored transitions with `AlreadyFinal` (`internal/store/postgres/v2store.go:109-215`, `resolveOutcomes`).
- Pinned by `TestRecordCheck_SameKeyTwiceIsIdempotent`, `TestRecordCheck_StagedPayloadIsClearedOnCommit`, `TestRecordCheck_IdenticalOutcomesWriteNoEvents` (`v2store_integration_test.go:143-285`) and `TestCheckRepo_StageThenRecordHandoff` (`internal/activities/activities_test.go:259`).
- Two engine-shaped details to carry: the `trigger` CHECK list (`00002_v2_schema.sql:78-79`, mirrored by `workflows.Trigger*` at `internal/workflows/types.go:21-28`) gains remediation triggers, and the `pending_result` payload is `[]outcomeRow` keyed by rule kind and name (`v2store.go`, `toOutcomeRows`), which becomes control results. DESIGN-0032 already lists the CHECK extension under `00005`.

**Consequence:** keep the table, the staging contract and the `/repositories/{id}/checks` endpoint (`api/openapi.yaml:321`). The "If wrong" column does not apply.

### A7 — Policy versions, VersionV2 and rollout re-pointed at the catalogue

**Verdict:** holds with caveat.

- `policy_versions(version, first_seen_at, rollout_completed_at, summary)` (`00002_v2_schema.sql:126-133`). `RecordPolicyVersion` returns `firstSeen` (`v2store_ops.go:273`), and the worker starts `policy-rollout/<v>` only on first sight (`cmd/repo-guardian/services.go:72-86`).
- `PolicyRolloutWorkflow` signals every active repository with `policy_changed{By: start + window}`, sleeps the window, re-signals `DriftedOnly` stragglers once, then completes (`internal/workflows/service.go:174-200`, `TestPolicyRolloutWorkflow_SignalsEveryPageThenStragglersAtWindowEnd`).
- Caveat 1, the hash input is wholly v1-shaped: `VersionV2` hashes `versionInput`, an explicit copy of guardian knobs, ignore, scope, defaults PR, file rules, setting rules, branch-protection rules and template bodies (`internal/policy/version_v2.go:26`, `version_v2.go:43-133`). Nothing in it can be "pointed" at controls; it is rewritten. The classification guard `TestVersionV2_EveryFieldClassified` (`internal/policy/version_v2_test.go:131`) and the golden (`testdata/version_v2/`) are the pattern to keep, with the catalogue's control revisions (D9) as the hashed fields.
- Caveat 2, stragglers read a column that goes: `SignalRepositories` with `DriftedOnly` compares `repositories.policy_version` (`internal/activities/services.go:196`), which DESIGN-0032 drops at cutover. Stragglers must come from `repository_policy_state.policy_version`.
- Caveat 3, rollout does not re-resolve: `SignalRepositories` only signals (`services.go:196-233`). DESIGN-0030 "When resolution runs" expects a policy change to re-resolve every repository before re-evaluating where assignments changed. That is a new step in the rollout workflow or the signal handler.

**Consequence:** keep the table, the first-seen gate, the window spread and the straggler pass. Rewrite the hash input and `policy.Summarize` for the catalogue and policies. Add re-resolution to the rollout. The "If wrong" column (rollout rebuilt, window kept) half applies: the window mechanics stay, the hash is new.

### A8 — The checker engine is removed, not adapted

**Verdict:** holds with caveat.

- `internal/checker` is 11,983 lines including tests (`wc`), with 100 parity goldens under `internal/checker/testdata/parity/` and the recorder in `internal/checker/parity_test.go:59-100`. `internal/reconciler` adds the four reconcilers. The only runtime consumer on the v2 path is `a.engine.CheckRepo` in `internal/activities/check.go:60`; the v1 role in `cmd/repo-guardian/main.go` is the other.
- Caveat: five pieces are not rule-engine logic and must be re-homed rather than deleted. (1) `SkippedError`, `AsSkipped` and the durable-skip table in `internal/checker/skip.go:19-62`, consumed by `classify` (`check.go:150`). (2) `CheckResult.Repository` identity capture (`internal/checker/result.go:85-93`), which feeds the `RecordCheck` identity refresh with zero extra API calls. (3) The typed comparators the control types need: `settingMatches` and `getSettingValue` (`internal/checker/engine_settings.go:130-165`), `compareBranchProtection` and `buildDesiredRuleset` (`internal/checker/engine_branch_protection.go:189-236`), `labelsEqual` and `normalizeColor` (`internal/reconciler/label_sync.go:217-226`), `rulesetMatches` (`internal/reconciler/branch_protection.go:186`). (4) `foreignPRForRule`'s loose third-party PR match (`internal/checker/engine_pr.go:47-85`) if DESIGN-0032 keeps a "yield to a human PR" rule. (5) Two log lines are locked by tests outside the package: `TestLogLines_AreStillEmittedByTheBinary` (`internal/monitoring/dashboard/logline_test.go:29`) walks `internal/` for the E4 matchers in `dashboard/e4.go:17-23`, and `internal/reconciler/log_contract_test.go` locks the catalog-parse line verbatim.

**Consequence:** remove the engine, PR assembly, drift, orphan and reconcile-log code and the parity suite per scenario (D8). Lift the five pieces above into the evaluate activity and the control types, and re-point the E4 matchers in the same change. The "If wrong" column (engine code constrains the control interface) does not apply: none of the surviving pieces constrains `control.Control`.

### A9 — The HCL loader is replaced; small pieces survive

**Verdict:** holds.

- `hclparse.NewParser()` is the entry point for files and directories (`internal/policy/loader.go:17`, `loader.go:174`, `loader.go:219`).
- Strict decode through `Content()` with a `BodySchema` covers the top level (`loader.go:263`), `guardian` (`loader.go:398-416`), `rule` (`loader.go:511`), `when` (`loader.go:538`), `reconcile` (`loader.go:690`), `defaults` (`loader.go:894`), `setting` (`loader.go:953`) and `branch_protection` (`loader.go:1020`). Five blocks still decode loosely with `JustAttributes()`: `locals` (`loader.go:296`), `ignore` (`loader.go:468`), `scope` (`loader.go:491`), `pr` (`loader.go:826`) and `assertion` (`loader.go:919`).
- Glob matching is stdlib `path.Match`, lower-cased (`internal/policy/ignore.go:32`); `ScopeConfig.Matches` (`internal/policy/scope.go:21`).
- PR template compilation and inheritance: `compilePolicyTemplates`, `compilePR`, `asTemplate`, `ResolveRulePR`, `mergePR` (`internal/policy/pr.go:27-187`); strict validation `ValidatePRTemplates` (`internal/policy/strict.go:26`).
- Also present and relevant: `ExtractWatchedPaths` (`internal/policy/watch.go:13`), which feeds ingest and dies with the watched-path set; `applyEnvOverrides` (`loader.go:1189`), which gives env the last word over `guardian {}`.

**Consequence:** replace the loader. Carry hclparse, the `Content()` pattern (and apply it to every block this time), `path.Match` for `exclude_repos`, and the PR compile and merge code, which moves to `internal/template` with `PRConfig` / `PRTemplate` per DESIGN-0031 AR-0031-08. Decide deliberately whether env-overrides-last survives for the new operational knobs. The "If wrong" column (more reusable than expected) is mildly true: the inheritance resolver is reusable as-is.

### A10 — The template package is reused

**Verdict:** holds.

- `Renderer.Parse` compiles with the curated `FuncMap` and `missingkey=error`; `Compiled.Render` wraps errors with the template name and refuses a nil receiver (`internal/template/template.go:49-110`). `ValidateZero[T]` is generic over the context type (`internal/template/strict.go:17`).
- Helpers: `env`, `default`, `join`, `lower`, `upper`, `title`, `propenv`, `yamlq` (`internal/template/helpers.go:16-25`). `propenv` and `yamlq` are the IMPL-0020 injection guards for the generated workflow and must stay for the `workflow` apply mode of `custom_properties`.
- The embedded templates and the operator template store live in `internal/rules/registry.go` (`TemplateStore.Load`, `Get`, `Raw`, `AsMap`, lines 35-150) over `internal/rules/templates/*.tmpl` (codeowners, dependabot, renovate, renovate-workflow, catalog-info, set-custom-properties).
- Contexts are v1-shaped: `FileVars`, `PRVars`, `Rule`, `CatalogInfo` (`internal/template/contexts.go:6-80`). DESIGN-0031 introduces `template.Vars`; `ValidateZero` works unchanged with any new context type.

**Consequence:** keep the package and `rules.TemplateStore` (renamed or moved as the control template store; `AsMap` keeps feeding the version hash). Add `Vars`, retire `FileVars` and `Rule` with the engine. The "If wrong" column does not apply.

### A11 — The catalog parser is the core of the catalog_info type

**Verdict:** holds with caveat.

- `Entity`, `Metadata`, `Spec` model the Backstage shape (`internal/catalog/catalog.go:27-46`); `Parse` enforces `apiVersion` and `kind == Component` and returns `ErrNotComponent` otherwise (`catalog.go:75-83`, `catalog.go:23`). The three-outcome contract from IMPL-0020 (parse error, not a Component, Properties) is pinned by `TestParse` (`internal/catalog/catalog_test.go:8`).
- Caveat: `Parse` projects the entity into `Properties{Owner, Component, Extra}` (`catalog.go:50-57`, `catalog.go:85-113`) and discards the rest. A `catalog_info` control with rules on `spec.lifecycle`, `spec.type` or `spec.system`, and the "keep the YAML node tree for minimal edits" requirement in DESIGN-0031, both need the entity or the node, not the projection. The only importer today is `internal/reconciler/custom_properties.go`.

**Consequence:** keep the package and the entity structs. Split `Parse` into an entity (or node) parse plus a separate properties projection for the `custom_properties` type. The "If wrong" column (parser rewritten) does not apply; this is an added return shape.

### A12 — The client layer survives; the interface splits

**Verdict:** holds with caveat.

- Transport chain: every constructor funnels through `instrumentedClient` with otelhttp outermost over `newRateLimitTransport` over ghinstallation (`internal/github/client.go:71-120`, `client.go:1090-1096`); `countingTransport` sits under the rate-limit transport (`internal/github/usage.go:119`). Pinned by `TestTransportOrder_ThrottledRequestIsStillMeasured` (`internal/github/transport_order_test.go:43`).
- `AsThrottled` normalises the transport's `*ThrottledError`, go-github's `*RateLimitError` pre-check and `*AbuseRateLimitError` (`internal/github/ratelimit.go:54-95`; `TestAsThrottled_SecondaryRateLimit`, `internal/github/access_test.go:78`; `TestCheckRepo_ThrottledErrorSurvivesWrapChain`, `internal/checker/ratelimit_chain_test.go:43`). `WithUsage` (`usage.go:36`) and `IsAccessDenied` (`internal/github/access.go:22`) are consumed by `classify` and `CheckRepo` (`check.go:57-60`, `check.go:139-150`).
- Caveat, the split is not a partition: `Client` is one 40-method interface (`internal/github/github.go:136-288`) mixing App-level methods (`ListInstallations`, `CreateInstallationClient`, `RateLimitRemaining`), 17 reads and 19 writes. The mock is generated by mockery (`.mockery.yaml`), so partitioning is mechanical, but the `Reader` DESIGN-0031 specifies needs methods that do not exist: content reads pinned to a ref (`GetContents` and `GetFileContent` are default-branch only, `client.go:130-142`, `client.go:473-494`; `GetContentsOnBranch` takes a ref but returns only sha and existence, `client.go:884`), directory or tree listing, `CodeownersErrors`, and commit authors for `PRObserver`. `Writer.Commit` through the git-data API (blobs, trees, commits) does not exist either; the only `Git.*` calls are `CreateRef` and `DeleteRef` (`client.go:234`, `client.go:244`), and files go through the Contents API (`CreateOrUpdateFile`, `client.go:258`).

**Consequence:** keep the transport chain, `AsThrottled`, `WithUsage`, `IsAccessDenied` and deferral unchanged. Implement `control.Reader`, `control.PRObserver` and `control.Writer` on `GitHubClient` as new methods; then delete the 40-method `Client` with the engine. The "If wrong" column (a wrapper rather than an interface change) does not apply, but the design should say "new interfaces with mostly new methods", not "splits".

### A13 — Ingest and WebhookWorkflow survive; pull_request is added

**Verdict:** holds with caveat.

- Ingest validates the HMAC with `gh.ValidatePayload`, drops unhandled events with 204, starts `WebhookWorkflow` with `REJECT_DUPLICATE` and treats already-started as success (`internal/ingest/ingest.go:59-118`). The event table is `repository{created,deleted,archived,unarchived,renamed,transferred}`, `installation{created,deleted,suspend,unsuspend}`, `installation_repositories{added,removed}` plus default-branch pushes touching a watched path (`ingest.go:125-131`, `ingest.go:183-213`). Routing lives in `RouteWebhook` (`internal/activities/route.go:49-82`). There is no `pull_request` case anywhere.
- Caveat 1, one secret: `New(secret string, ...)` holds a single secret (`ingest.go:51-53`). DESIGN-0032 now has both Apps deliver lifecycle events to one endpoint, each signed with its own secret, chosen by the target App id header. `ValidatePayload` consumes the body once, so the header must be read before it and the handler must hold a map of App id to secret.
- Caveat 2, the push filter: `touchesWatched` consults `policy.ExtractWatchedPaths` (`ingest.go:201-213`). Under controls the set is every assigned control's `Owns() and Reads()`. Ingest already loads the policy (`roleReadsPolicy` includes `ingest`, `charts/repo-guardian/templates/_helpers.tpl:304`), so it can load the catalogue, but the resource paths must be computable without the store, which the `ingest` role refuses to hold (`internal/config/role.go:40-47`).
- Caveat 3: `pull_request` is a new case in three places: `ingest.route`, `RouteWebhook`, and `workflows/webhook.go`.

**Consequence:** keep the role and the workflow; the "If wrong" column (ingest reshaped) partly applies through the two-secret validation and the resources-based push filter. State both in DESIGN-0032's ingest paragraph.

### A14 — API structure and UI shell survive

**Verdict:** holds.

- `api.New` layers otelhttp, request id and access log, recover, authn outside the generated wrapper, then authz; public routes are derived from the spec's `security: []` (`internal/api/server.go:80-130`, `publicRoutes` at `server.go:134`); `requireVisibleOrgs` is keyed on operation id (`server.go:181`).
- `apitest.New` validates every response against `api/openapi.yaml` with kin-openapi and `IncludeResponseStatus` (`internal/api/apitest/apitest.go:33`, `apitest.go:105-119`); depguard forbids `net/http/httptest` in API tests and `internal/github` or `store/postgres` in the API (`.golangci.yml`, the `internal/api` entries).
- `TestAPIQueries_AreScoped` enforces the scope predicate over every `queries/api_*.sql` (`internal/store/postgres/apiscope_test.go:14-45`). `NewReadOnlyPool` (`internal/store/postgres/ropool.go:22`) and `APIReader` (`internal/store/postgres/apireader.go:23`) are the read path. `TestStatus_PrivacyGuard` (`internal/api/status_http_test.go:117`) and `TestCompliance_ParityAcrossReportAPIAndSnapshot` (`internal/store/postgres/parity_integration_test.go:22`) pin the status and percent contracts.
- UI: the BFF (`ui/server/`), the router with `/`, `/rules`, `/rules/$kind/$name`, `/orgs`, `/orgs/$org`, `/findings`, `/repos/$id`, `/history`, `/policy`, `/status` (`ui/web/src/router.tsx:43-150`) and the views under `ui/web/src/views/`. `schema.gen.ts` is generated from the spec and drift-gated by `lint-ui`.
- Rule-shaped surfaces to replace: `/rules`, `/rules/{kind}/{name}`, `/findings` (`api/openapi.yaml:75-225`), `rules.tsx`, `findings.tsx`, `lib/evidence.ts`, `lib/viewmodel.ts`. One invariant changes by design: the api role holds no Temporal client today (`dialTemporal` returns early for `api`, pinned by `TestDialTemporal_APIRoleNeedsNoTemporal`), and `POST /repositories/{id}/evaluate` (DESIGN-0032 D9) adds a signal-only client, so that test is retired on purpose.

**Consequence:** keep the framework and the guards; replace the rule-shaped resources and views. The "If wrong" column (API and UI work is larger) does not apply beyond the known replacements.

### A15 — Roles and chart secret scoping extend to evaluator and remediator

**Verdict:** holds with caveat.

- Roles render from one list (`repo-guardian.roles`: `all`, or `ingest worker [api]`, `charts/repo-guardian/templates/_helpers.tpl:264-270`); `deployment.yaml` ranges over it (`templates/deployment.yaml:8`). Secret scoping is by one-line list-membership helpers: `roleHasAppKey`, `roleHasWebhookSecret`, `roleDialsTemporal`, `roleHasStore`, `roleServesAPI`, `roleReadsPolicy` (`_helpers.tpl:299-304`), consumed at `deployment.yaml:100-110`, `deployment.yaml:175`, `deployment.yaml:230`. `tests/secret_scoping_test.yaml` asserts each absence by env name. `validateRoles` (`_helpers.tpl:489-501`) and `validateRemovedValues` (`_helpers.tpl:195`) are the guard pattern.
- The binary mirrors it: `Role` is a bitmask of `RoleIngest | RoleWorker | RoleAPI` (`internal/config/role.go:11-15`); `ValidateRole` refuses the App key and `STORE_DSN` on `ingest` alone and requires them on `worker` (`role.go:28-76`).
- Caveat: every layer models one App and one queue. Values: `config.appId`, `secrets.privateKey`, `secrets.existingSecret`, `secrets.webhookSecret` (`charts/repo-guardian/values.yaml:83`, `values.yaml:309-315`). Env: `GITHUB_APP_ID`, `GITHUB_PRIVATE_KEY_PATH`, `GITHUB_WEBHOOK_SECRET`, one `TEMPORAL_TASK_QUEUE`, one `STORE_DSN` via `storeDSNEnv` (`_helpers.tpl:229`). DESIGN-0032 wants two keys, two webhook secrets, two task queues and two DSNs (`rg_evaluator`, `rg_remediator`).

**Consequence:** the helper pattern extends (add `evaluator` and `remediator` to the lists, add per-App key helpers), so the "If wrong" column (roles redesigned) does not apply. The values schema and env names are new, which is a values-breaking change on the rc line; use the existing `const` plus `validateRemovedValues` pattern for the single-App keys.

### A16 — No production histories; new workflow type names need no gates

**Verdict:** holds with caveat; the operational half is not verifiable from code.

- The only version gate is `budget-v1` (`internal/workflows/repo.go:19`, `repo.go:253`). The worker uses deployment versioning with `AutoUpgrade` (`internal/temporal/worker.go:83-89`).
- The replay suite replays every file under `internal/workflows/testdata/histories/` and fails when the directory is empty (`internal/workflows/replay_test.go:23-33`). Two fixtures exist, `check_then_park.json` (a `RepoWorkflow`) and `installation_grant_report.json` (an `InstallationWorkflow`), captured with `-update-histories` (`internal/activities/integration_test.go:50`).
- Whether any namespace other than the homelab holds histories is an operational fact the code cannot settle.
- Caveat 1: the fixtures are coupled to the type names. Removing `RepoWorkflow` from `Register` makes `check_then_park.json` fail to replay, and deleting the file alone trips the emptiness guard. The change that introduces `EvaluationWorkflow` must capture its history in the same PR.
- Caveat 2: `AutoUpgrade` moves in-flight executions to the newest build. A deployed build that no longer registers `RepoWorkflow` makes every live `repo/<id>` execution fail its next workflow task with an unregistered type. The terminate step in the cutover runbook (D8) must run before the deploy, or the build must keep a registration that completes immediately.

**Consequence:** no `GetVersion` gates, as assumed. Add the two caveats to DESIGN-0032's cutover section. The "If wrong" column (gates or a migration workflow) does not apply.

### A17 — Frozen PR identity ends; cutover closes v1's PRs

**Verdict:** holds.

- `TestPRIdentity_IsFrozen` pins `repo-guardian/add-missing-files`, the title, the reconcile-log marker and the hash tag (`internal/checker/identity_test.go:13-38`), and the reconciler branches `repo-guardian/set-custom-properties` and `repo-guardian/add-catalog-info` with their titles (`internal/reconciler/identity_test.go:9-18`).
- "Adopt each other's PRs" is literal: v2 runs the same engine (`check.go:60`), which finds its own PR by `pr.Head == BranchName` (`internal/checker/engine_pr.go:57`), so v1 and v2 converge on one PR because they are one code path.
- DESIGN-0032 D4 closes v1's PR with a pointer comment at cutover and names only `add-missing-files`. The two reconciler branches are also frozen identities with PRs in the wild.

**Consequence:** retire both tests deliberately at cutover. Correction: D4's close step should name all three branches (`add-missing-files`, `set-custom-properties`, `add-catalog-info`). The "If wrong" column (a transitional adopter) does not apply.

### A18 — TaskPriority applies unchanged

**Verdict:** holds.

- `TaskPriority(p, installationID)` returns `{PriorityKey: p, FairnessKey: installation id}` (`internal/workflows/options.go:28-30`). Priorities are `webhook 2`, `schedule 3`, `rollout 4` (`internal/workflows/types.go:13-17`); pushes use `PriorityWebhook` (`internal/activities/route.go:162-168`). The budget halves its reserve for priority 2 and below (`internal/workflows/installation.go:247-257`).
- Under two Apps each role has its own queue and each App its own installation ids, so fairness by installation partitions naturally; a remediation run keys on the Remediation App's installation.

**Consequence:** keep. Remediation runs triggered by an evaluation change need a priority value in DESIGN-0032's table (3 fits "machine-triggered, not a rollout"). The "If wrong" column does not apply.

### A19 — Posture export and dashboards regenerate from control results

**Verdict:** holds with caveat; the exporter does not exist on the v2 path.

- `PostureExporter.Export` publishes `repos_actionable{rule_name, org}`, `repos_tracked{org}` and `repos_unmeasurable{org, reason}` after reading, then `ResetPosture` (`internal/checker/posture.go:48-125`). It reads v1's `rule_state` joined to `repo_state` (`internal/store/postgres/postgres.go:470-520`, `store.Store.Posture`).
- It is wired only in the v1 `run()` path (`cmd/repo-guardian/main.go:353`). No v2 role exports posture: `cmd/repo-guardian/roles.go`, `cmd/repo-guardian/services.go` and `internal/activities` contain no posture code. The E1 and E2 panels query `repo_guardian_repos_actionable` and `repo_guardian_repos_tracked` with `max by` (`internal/monitoring/dashboard/e1.go:33-34`, `dashboard/e2.go:79-156`), so on a v2 deployment those panels read "no data" today.
- The generator derives its model from `*policy.PolicyConfig` and keys every posture series on `rule_name`, rejecting duplicate names across kinds (`internal/monitoring/derive.go:55`, `derive.go:226-240`). The leader-only pattern relies on Valkey SETNX, which v2 roles do not have.

**Consequence:** the `max by` and "no data" patterns and the catalogue-as-data alert model carry over, but the exporter is new work on v2 regardless of controls: an exporter over control results, run from one place (a Temporal schedule or the api role, which is the only singleton), with `Derive` fed by the catalogue and policy snapshot and keyed on control id. The "If wrong" column (monitoring generation rewritten) applies to the model derivation and the exporter, not to the panel library or emitters. DESIGN-0029's `installation_info{topology}` addition lands in the same change.

### A20 — Shadow verification and the v1 backfill are not carried over

**Verdict:** holds.

- `internal/shadow` compares v1 `rule_state` with v2 findings per `(repository, rule)` and classifies divergences (`internal/shadow/shadow.go:1-60`, `Compare` at line 107); the subcommand is `migrate verify-shadow` (`cmd/repo-guardian/verify_shadow.go:17`).
- The backfill is goose migration `00003` (`internal/store/postgres/backfill_v1.go:53`), no-op unless adopted, writing findings with `migrated_from_v1` (`backfill_v1.go:240-275`) and setting `v2_meta.bootstrap_pending`, which `BootstrapWorkflow` clears (`internal/workflows/service.go:205-218`). `00001_adopt_v1` creates `v2_meta` and checks the v1 schema (`internal/store/postgres/adopt_v1.go:30-60`); both Go migrations are in `goMigrations` (`internal/store/postgres/goose.go:67-72`). `migrate --dry-run` hand-wires the `00002` replay (`internal/store/postgres/dry_run.go`).
- Without a rule-to-control mapping the shadow comparison has no key to join on, which is exactly why A20 chooses a fresh evaluation.

**Consequence:** remove `internal/shadow`, `verify-shadow`, `ReasonMigratedFromV1`, `BootstrapWorkflow` and the bootstrap store methods. Keep `00001` (the v1-idle check still protects a v1 database). Make `00003` a versioned no-op rather than deleting it, so `SchemaVersion` and existing rc databases keep a contiguous chain; its only product is dropped by `00007`. The "If wrong" column (a v1 to controls mapping) does not apply.

### A21 — GitHub reads CODEOWNERS from .github/, then root, then docs/

**Verdict:** not verifiable from code.

- Nothing in the repository encodes GitHub's precedence. v1's default rule lists `CODEOWNERS`, `.github/CODEOWNERS`, `docs/CODEOWNERS` in that order with `Target = .github/CODEOWNERS` (`internal/policy/defaults.go:112-126`), and the operator docs repeat root-first (`docs/usage/getting-started.md:199`). v1's `exists` check passes when any path exists, so the order never mattered to v1 and is not evidence either way.
- DESIGN-0031's `codeowners` table lists `.github/CODEOWNERS`, `CODEOWNERS`, `docs/CODEOWNERS` as "the first found is the one GitHub uses", matching GitHub's published documentation at the time of writing.

**Consequence:** do not copy v1's order. Pin the precedence in a table test of `LocateFile` and confirm it once in the homelab with two CODEOWNERS files and the errors endpoint, which reports the file GitHub actually used. If the probe disagrees, the "If wrong" column holds: only the location precedence changes.

### A22 — The CODEOWNERS errors endpoint is readable with read permissions

**Verdict:** not verifiable from code.

- go-github v68 exposes the endpoint with a `Ref` option, so binding validation to the evaluated commit (DESIGN-0031 AR-0031-05) is implementable: `RepositoriesService.GetCodeownersErrors(ctx, owner, repo, &GetCodeownersErrorsOptions{Ref})` (`$GOMODCACHE/github.com/google/go-github/v68@v68.0.0/github/repos_codeowners.go:15-19`, `repos_codeowners.go:42`). Nothing in the repository calls it.
- Neither permission table in the repository mentions the endpoint (`docs/operations/ent-setup.md:128-145`, `docs/operations/v2-onboarding.md:122-131`).

**Consequence:** the fallback in the "If wrong" column is already the design (`unknown{reason=permission}`, never pass on parser evidence). Verify with the Evaluation App's token in the homelab when DESIGN-0034 is taken up; A22 is in that design's scope now.

### A23 — The Evaluation App's read set covers every built-in read

**Verdict:** not verifiable from code; one correction is visible from the code.

- The reads the code performs today: `Repositories.GetContents` (`internal/github/client.go:131`, `client.go:474`), `PullRequests.List` (`client.go:155`), `Repositories.Get` (`client.go:184`, `client.go:595`), `Git.GetRef` (`client.go:213`), `Repositories.GetAllCustomPropertyValues` (`client.go:500`), `Organizations.GetAllCustomProperties` (`client.go:550`), `Repositories.GetVulnerabilityAlerts` (`client.go:565`), `Repositories.GetAllRulesets` and `GetRuleset` (`client.go:653`, `client.go:668`), `Issues.ListLabels` (`client.go:813`), `Issues.ListComments` (`client.go:1012`). The new reads in DESIGN-0031 (tree listing, commits, CODEOWNERS errors) are Contents and Metadata reads.
- The repository's own permission docs distinguish two Custom properties permissions: repository-level for values and organisation-level for the schema (`docs/operations/ent-setup.md:140-144`, `docs/operations/v2-onboarding.md:128-131`, `docs/operations/annotation-properties-migration.md:85`). The design's list says "Custom properties:read" once. `GetOrgPropertySchema` (`client.go:549`), which DESIGN-0031 keeps on `Reader` as `OrgPropertySchema`, needs the organisation permission.
- Which permission each endpoint needs is GitHub's rule, not the code's.

**Consequence:** amend A23 and DESIGN-0032's permission table to list "Custom properties (repository): read" and "Custom properties (organization): read" separately. Verify the full set once by registering the Evaluation App with exactly that set and running one evaluation per built-in control type in the homelab. If a read fails, the "If wrong" column applies to that control type only.

### Keep, adapt, remove

| Package or component | Decision | Basis | What changes |
| -------------------- | -------- | ----- | ------------ |
| `internal/workflows` | adapt | A1, A2, A3, A7, A13, A16, A18 | `repoLoop` becomes the shared loop under `EvaluationWorkflow`; budget id gains the App; discovery input gains the App and a resolution step; rollout re-resolves; webhook gains `pull_request`; new type names in `names.go` |
| `internal/activities` | adapt | A1, A3, A6, A8, A13 | `CheckRepo` becomes `Evaluate`; `classify`, identity capture and `StageCheck` stay; router gains `pull_request` and per-App installation upserts; `UpsertRepositories` resolves before starting |
| `internal/temporal` | keep | A16 | none |
| `internal/store/postgres` (v2 store, identity, goose, sqlc) | adapt | A4, A5, A6, A7, A20 | migrations `00004` to `00007`; `identity.go` and `checks` unchanged; findings queries replaced by control-result queries; `00003` becomes a no-op |
| `internal/store/postgres` v1 `Store` (`postgres.go`, `compliance.go`, `report.go`) | remove with the `v1` role | A19, A20 | `rule_state` / `repo_state` access goes with v1 |
| `internal/store` (`v2.go`, `api.go`, `findings.go`) | adapt | A5, A6 | `Outcome`, `Finding`, `ComplianceCount` re-keyed to controls |
| `internal/findings` | adapt | A5 | `Status` reused for control status; `Reason` and `Evidence` catalogue re-cut per control type |
| `internal/checker` | remove | A8, A17 | lift `skip.go`, `RepositoryIdentity`, the comparators and the E4 log lines first |
| `internal/reconciler` | remove | A8, A17 | lift label and ruleset diff logic into `labels` and `branch_ruleset` |
| `internal/policy` | replace | A7, A9 | keep hclparse, `Content()` decode, `path.Match`, the PR compile and merge code (moving to `template`), the `VersionV2` classification-test pattern |
| `internal/template`, `internal/rules` (TemplateStore) | keep | A10 | add `Vars`; `PRConfig` / `PRTemplate` move in; retire `FileVars` / `Rule` |
| `internal/catalog` | adapt | A11 | expose the entity or node; keep the properties projection for `custom_properties` |
| `internal/github` | adapt | A12 | transport chain, `AsThrottled`, `WithUsage`, `IsAccessDenied` unchanged; implement `control.Reader` / `PRObserver` / `Writer` with new methods; delete `Client`; regenerate mocks |
| `internal/ingest` | adapt | A13 | two secrets by target App id; `pull_request`; push filter over `Owns()` and `Reads()` |
| `internal/api`, `ui/` | adapt | A14 | framework and guards kept; `/rules`, `/findings` and their views replaced; signal-only Temporal client for the evaluate POST |
| `internal/config` | adapt | A15 | role bitmask gains `evaluator` / `remediator`; two credential sets; per-role refusals extended |
| `internal/metrics` | adapt | A19 | posture gauges re-keyed to control; v1 queue, worker and write-back metrics removed with v1 |
| `internal/monitoring`, `contrib/generated/` | adapt | A8, A19 | `Derive` from the catalogue and policy snapshot; posture series by control; E4 matchers re-pointed; a v2 exporter to feed them |
| `internal/observability` | keep | A12 | none |
| `internal/report` | adapt | A5 | shared compliance query over control results; percent rule kept |
| `internal/shadow`, `backfill_v1.go` body, `BootstrapWorkflow`, `migrate verify-shadow` | remove | A20 | `00003` kept as a versioned no-op |
| `internal/worker`, `internal/webhook`, `internal/scheduler`, `internal/queue`, the `v1` subcommand | remove at cutover | A19, A20 | the v1 runtime; already absent from v2 roles |
| `cmd/rg-burst` | adapt | A2 | installation id becomes App plus id |
| `charts/repo-guardian` | adapt | A15 | roles list and helpers extended; two credential blocks; two queues; removed-values guard for the single-App keys |

## Conclusion

**Answer:** Yes, with caveats. All twenty-three assumptions either hold (A1, A2, A6, A9, A10, A14, A17, A18, A20 and, for its code half, A16), hold with a caveat that changes the plan but not the model (A3, A4, A5, A7, A8, A11, A12, A13, A15, A19), or are GitHub facts the code cannot settle (A21, A22, A23). None is wrong in the sense its "If wrong" column anticipates. The keep-versus-replace split in DESIGN-0029 stands: the Temporal control plane, the store's identity and check-log tables, the template layer, the client transport chain, the API framework and the chart's role pattern are kept or adapted; the rule engine, the reconcilers, the findings tables, the HCL loader and the v1 bridge are removed or replaced.

The six findings that change the implementation plan are: resolution must run inside `UpsertRepositories` before the workflow starts (A3); discovery, ingest, config and the chart are single-App and need an App dimension rather than a redesign (A3, A13, A15); the client split adds methods instead of partitioning existing ones (A12); the posture exporter does not run on v2 and has to be built, not re-pointed (A19); the replay suite fails closed when its fixtures lose their workflow type (A16); and five non-engine pieces inside `internal/checker` must be lifted before the package goes (A8).

## Recommendation

1. **DESIGN-0029, assumptions table.** Mark A1 to A20 verified with the verdicts above; mark A21 to A23 "homelab probe" with the probe described in each finding. Reword A12 from "splits into" to "is replaced by `control.Reader`, `PRObserver`, `Writer`, implemented as new methods on `GitHubClient`".
2. **DESIGN-0029 "What happens to v1 concepts" and the overview.** Add a line that the five surviving checker pieces (durable-skip classification, repository identity capture, the setting, ruleset and label comparators, the foreign-PR match, the E4 log lines) are lifted into the evaluate activity and the control types, so "removed, not adapted" reads correctly (A8).
3. **DESIGN-0030 "When resolution runs".** State that resolution runs inside `UpsertRepositories` between the upsert and the SignalWithStart, and inside the rollout before re-evaluation (A3, A7).
4. **DESIGN-0032 "Two Apps" and "Migration".** Replace any remaining `installations.app` wording in `00004_controls_policy` with `app_installations` and `app_repository_access` (A2). Decide the fate of `repositories.installation_id` (OQ2) and record it under Data Model (A4).
5. **DESIGN-0032 ingest paragraph.** Name the two concrete reshapes: secret selection by the target App id header before `ValidatePayload`, and a push filter over `Owns()` and `Reads()` computed without store access (A13).
6. **DESIGN-0032 D4.** Extend the cutover close step to `repo-guardian/set-custom-properties` and `repo-guardian/add-catalog-info` (A17).
7. **DESIGN-0032 cutover (D8).** Add: capture `EvaluationWorkflow` and `RemediationWorkflow` histories in the PR that removes `RepoWorkflow`, because the replay suite fails on an empty directory; terminate live `repo/<id>` executions before deploying a build that no longer registers `RepoWorkflow`, because the worker runs `AutoUpgrade` (A16, OQ3).
8. **DESIGN-0032 permission table and A23.** List repository-level and organisation-level Custom properties: read separately (A23).
9. **DESIGN-0032 "Temporal mapping" or the IMPL.** Add a v2 posture exporter over control results as its own phase; today nothing feeds the E1 and E2 panels on v2 (A19). Re-point `Derive` from `PolicyConfig` to the catalogue and policy snapshot in the same phase.
10. **DESIGN-0032 priorities table.** Give change-triggered remediation runs a priority value (A18).
11. **IMPL plan.** Order the phases so `control.Reader` and its new client methods (ref-pinned reads, tree listing, CODEOWNERS errors, git-data commit) land before any control type, since every type depends on them (A12). Keep goose `00003` as a versioned no-op and delete only its body (A20). Apply `Content()` decode to every block in the new loader (A9).

### OQ1: How is installation suspension modelled under the controls model?

- (a) ✅ recommended: as `app_installations.suspended_at`, from which `remediable` and `unmeasurable` are derived; not a park. Today suspension is only a budget gate (`route.go:224-247`) and `park_reason` has no `suspended` value; keeping it out of parking preserves "discovery is the only un-parker" and needs no un-park path on `installation.unsuspend`.
- (b) add `suspended` to `park_reason`, park on `installation.suspend` keeping results, and un-park on `installation.unsuspend`, which makes `unsuspend` a second un-parker.
- other:

### OQ2: Does `repositories.installation_id` stay once `app_repository_access` exists?

- (a) ✅ recommended: keep it as the Evaluation App's installation, `NOT NULL`, refreshed by `applyIdentity` on transfer as today (`identity.go:98-99`), with `app_repository_access` as the authority for `evaluable` and `remediable`. Discovery inserts stay one statement and the identity tests stay green.
- (b) drop it in `00004` and make `matchRepository` / `applyIdentity` installation-free, reading the Evaluation App installation from `app_repository_access` wherever `CheckRepo` and `TaskPriority` need it.
- other:

### OQ3: How are live `RepoWorkflow` executions retired when the build stops registering the type?

- (a) ✅ recommended: the cutover runbook terminates every `repo/<id>` and `installation/<id>` execution before the new build is promoted, and the PR that removes `RepoWorkflow` replaces the two replay fixtures with captures of the new workflows.
- (b) the new build keeps a tombstone `RepoWorkflow` that completes immediately, so executions drain on their own under `AutoUpgrade`, and the fixtures are kept until the tombstone is removed one release later.
- other:

## References

- DESIGN-0029: `docs/design/0029-controls-and-policies-opinionated-compliance-with-separate.md` (assumptions table, "What happens to v1 concepts", Data Model)
- DESIGN-0030: `docs/design/0030-policy-model-enterprise-and-org-policies-and-control-assignment.md` ("When resolution runs", AR-0030-04)
- DESIGN-0031: `docs/design/0031-control-framework-controls-control-rules-and-file-controls.md` (D7, AR-0031-05, AR-0031-08, `codeowners` locations)
- DESIGN-0032: `docs/design/0032-evaluation-and-remediation-workflows-change-driven-remediation.md` ("Two Apps", "Roles", D4, D8, "Migration")
- IMPL-0025: `docs/impl/0025-*.md` (Phases 3 to 18, the code under test)
- INV-0015 (parking and the subset invariant), IMPL-0020 (catalog parse contract), IMPL-0023 (posture-state contract)
- `CLAUDE.md`, "The v2 branch (IMPL-0025)"
- go-github v68 `repos_codeowners.go` (`GetCodeownersErrors`, `GetCodeownersErrorsOptions.Ref`)
