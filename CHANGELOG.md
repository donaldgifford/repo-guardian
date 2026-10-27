# Changelog

All notable changes to this project are documented here. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
this project adheres to [Semantic Versioning](https://semver.org/).

## [2.0.0] - 2026-10-27

### Features

- *(security)* SBOMs, ci-ok, pins and pod hardening (IMPL-0034 Phase 5)

### Bug Fixes

- *(ci)* Install the pinned go beside mise go: tools (IMPL-0034 5.6)

### Documentation

- *(impl)* Tick IMPL-0033 5.3, rc.17 published
- *(site)* Pages inherits mkdocs.yml, strict build in CI (IMPL-0034 Phase 2)
- *(release)* Changelog prepend, rc dispatch, release line (IMPL-0034 Phase 4)
- Rewrite the landing and operations docs for v2 (IMPL-0034 Phase 3)
- *(ui)* 2.0.0 changelog entry (IMPL-0034 6.2)
- *(release)* 2.0.0 release notes (IMPL-0034 6.4)
- *(impl)* IMPL-0034 progress, Phases 7-9 deferred to the maintainer
- *(impl)* IMPL-0034 testing plan status, 1.4 deferred

### Miscellaneous Tasks

- Sync main into v2 (IMPL-0034 Phase 1)
- *(chart)* 2.0.0 (IMPL-0034 6.1)
- *(release)* GA preflight script (IMPL-0034 6.3)
- Run CI on main only (IMPL-0034 4.2)

## [1.14.1] - 2026-10-21

### Features

- *(rgctl)* Standalone CLI to find and close repo-guardian PRs (DESIGN-0035, IMPL-0027) ([#203](https://github.com/donaldgifford/repo-guardian/issues/203))

### Bug Fixes

- *(deps)* Go 1.26.9 and golang.org/x/text v0.42.0 (GO-2026-6617, GO-2026-6629) ([#227](https://github.com/donaldgifford/repo-guardian/issues/227))
- *(ci)* Bump only from tags reachable from main ([#228](https://github.com/donaldgifford/repo-guardian/issues/228))
- *(deps)* Go 1.26.10 (GO-2026-6809) ([#229](https://github.com/donaldgifford/repo-guardian/issues/229))

### Documentation

- *(rgctl)* The v2 cutover's two rgctl steps (IMPL-0030 task 8.6) ([#225](https://github.com/donaldgifford/repo-guardian/issues/225))

## [2.0.0-rc.11] - 2026-10-16

### Features

- *(control)* Framework value types and template.Vars (IMPL-0029 1.2)
- *(control)* Canonical resource keys (IMPL-0029 1.3)
- *(control)* Validate and StatusOf (IMPL-0029 1.4)
- *(control)* Revision and Fingerprint (IMPL-0029 1.5)
- *(control)* ValidateChangeSet (IMPL-0029 1.6)
- *(control)* Evaluation reader (IMPL-0029 1.7)
- *(control)* File-control adapter and LocateFile (IMPL-0029 1.8)
- *(control)* Type registry and definition validation (IMPL-0029 1.9)
- *(control)* Controltest conformance suite (IMPL-0029 1.10)
- *(controls)* Codeowners reference type (IMPL-0029 1.11)
- *(policy)* Policy-root loader (IMPL-0029 2.2)
- *(policy)* Control catalogue decoding (IMPL-0029 2.3)
- *(policy)* Enterprise and org policy decoding (IMPL-0029 2.4)
- *(policy)* Load-time validation (IMPL-0029 2.5)
- *(policy)* Load-time ownership overlap warnings (IMPL-0029 2.6)
- *(policy)* Repository resolution (IMPL-0029 2.7)
- *(policy)* Snapshot version, accessors and summary (IMPL-0029 2.9)
- *(controls)* Generic file control type (IMPL-0029 3.1)
- *(catalog)* Document parse with node tree and source spans (IMPL-0029 3.2)
- *(controls)* Catalog_info control type (IMPL-0029 3.3)
- *(controls)* Dependency_updates control type (IMPL-0029 3.4)
- *(controls)* Repo_settings control type and vulnerability-alerts reads (IMPL-0029 3.5)
- *(controls)* Branch_ruleset control type (IMPL-0029 3.6)
- *(controls)* Labels control type (IMPL-0029 3.7)
- *(controls)* Custom_properties control type with workflow apply (IMPL-0029 3.8)
- *(control)* Apply is for API types only (IMPL-0029 3.9)
- *(examples)* Controls-model example policy roots with summary goldens (IMPL-0029 2.10)
- *(controls)* Generated control-type reference (IMPL-0029 3.11)
- *(store)* Controls policy schema (IMPL-0029 4.1)
- *(store)* Controls results and remediation schema (IMPL-0029 4.2)
- *(store)* Controls compliance snapshots (IMPL-0029 4.3)
- *(store)* Evaluator controls store (IMPL-0029 4.5)
- *(store)* Park with the result rule (IMPL-0029 4.6)
- *(store)* Controls compliance queries (IMPL-0029 4.7)
- *(workflows)* Controls evaluation names and signals (IMPL-0029 5.1)
- *(workflows)* EvaluationWorkflow (IMPL-0029 5.2)
- *(activities)* Controls Evaluate activity (IMPL-0029 5.3)
- *(activities)* RecordEvaluation and ParkRepository (IMPL-0029 5.4)
- *(workflows)* ControlsDiscoveryWorkflow (IMPL-0029 5.5)
- *(workflows)* Controls rollout and evaluator schedules (IMPL-0029 5.6)
- *(activities)* Controls webhook routing (IMPL-0029 5.7)
- *(workflows)* Controls compliance snapshot (IMPL-0029 5.8)
- *(cmd)* Repo-guardian evaluate (IMPL-0029 5.9)
- *(metrics)* Controls evaluation metrics (IMPL-0029 6.1)
- *(metrics)* Deployment_info carries the binary version (IMPL-0029 6.2)
- *(api)* Controls posture exporter (IMPL-0029 6.3)
- *(monitoring)* Derive the model from the controls policy snapshot (IMPL-0029 6.4)
- *(monitoring)* Rebuild the dashboards over controls posture (IMPL-0029 6.5)
- *(monitoring)* Controls alert catalogue (IMPL-0029 6.6)
- *(chart)* Mirror the controls alert catalogue (IMPL-0029 6.7)
- *(report)* Render compliance from control results (IMPL-0029 6.9)
- *(workflows)* The evaluator runs the controls workflows (IMPL-0029 7.1)
- *(api)* Remove the findings-era endpoints (IMPL-0029 7.6)
- *(ui)* Remove the findings-era views (IMPL-0029 7.7)
- *(chart)* Mount the controls policy directory (IMPL-0029 7.9)
- *(api)* Specify the controls endpoints (IMPL-0029 8.1)
- *(store)* Add the controls API queries (IMPL-0029 8.2)
- *(api)* Serve the controls views (IMPL-0029 8.3)
- *(api)* Re-evaluate a repository on request (IMPL-0029 8.4)
- *(api)* Status page for the controls line (IMPL-0029 8.5)
- *(ui)* The controls views (IMPL-0029 8.7)

### Bug Fixes

- *(store)* Discovery parks archived and forked repositories (IMPL-0029 5.5)
- *(activities)* The evaluator feeds rate_limit_remaining (IMPL-0029 6.6)
- *(chart)* Take policy.files as a nested map by directory (IMPL-0029 7.9, OQ11)

### Refactor

- *(template)* Move PRConfig/PRTemplate to template; D17 depguard edges (IMPL-0029 1.1)
- *(policy)* Move internal/policy to internal/rulepolicy (IMPL-0029 2.1)
- Delete the rule engine (IMPL-0029 7.3)
- *(store)* Re-cut the API reader onto the controls schema (IMPL-0029 7.4)
- *(store)* Remove the rc schema code (IMPL-0029 7.5)
- *(config)* Give every rule-engine env var a fate (IMPL-0029 7.8)

### Documentation

- *(impl)* Rc.8 released and confirmed on dev (IMPL-0028 10.4)
- *(control)* Non-vacuous probes and Phase 1 close-out (IMPL-0029 1.12)
- *(impl)* Record per-type non-vacuous probes (IMPL-0029 3.12)
- CLAUDE.md architecture for IMPL-0029 Phase 3
- *(impl)* Tick IMPL-0029 4.1
- *(store)* Fix ControlsSchemaVersion comment
- *(store)* Restore ControlsSchemaVersion doc line
- *(impl)* Record Phase 4 non-vacuous probes (IMPL-0029 4.11)
- CLAUDE.md store notes for IMPL-0029 Phase 4
- CLAUDE.md for IMPL-0029 Phase 5
- CLAUDE.md and monitoring docs for IMPL-0029 Phase 6
- Record the controls switch-over in CLAUDE.md (IMPL-0029 Phase 7)
- Record the controls API and UI (IMPL-0029 Phase 8)
- Record the controls framework invariants in CLAUDE.md (IMPL-0029 9.4)
- *(impl)* IMPL-0031 8.1 and 8.2 done on rc.9/rc.10 ([#218](https://github.com/donaldgifford/repo-guardian/issues/218))

### Testing

- *(policy)* Resolution golden cases (IMPL-0029 2.8)
- *(policy)* Non-vacuous probes and two more goldens (IMPL-0029 2.11)
- *(controls)* Parser fuzzing and size caps (IMPL-0029 3.10)
- *(store)* Controls grant boundary as the roles (IMPL-0029 4.8)
- *(store)* Epoch fencing, policy revert and posture scenarios (IMPL-0029 4.9)
- *(migrate)* Controls dry run covers 00001-00004 (IMPL-0029 4.10)
- *(workflows)* EvaluationWorkflow coalescing, parking and history bound (IMPL-0029 5.10)
- Controls change-detection tables (IMPL-0029 5.11)
- *(activities)* TestIntegration_OneEvaluation and evaluation history (IMPL-0029 5.12)
- *(activities)* Push selection by Reads and the Phase 5 non-vacuous check (IMPL-0029 5.13)
- *(monitoring)* Phase 6 non-vacuous checks (IMPL-0029 6.10)
- Retire the parity suite scenario by scenario (IMPL-0029 7.2)
- Confirm the rc workflow and chart guards fire (IMPL-0029 7.11)
- *(api)* Validate every endpoint against the spec (IMPL-0029 8.6)
- *(ui)* E2e over the controls views (IMPL-0029 8.8)
- *(api)* Prove the scope tests are non-vacuous (IMPL-0029 8.10)

### Miscellaneous Tasks

- *(policy)* Policy-leaf depguard rule and Phase 2 docs (IMPL-0029)
- *(store)* Sqlc block for the controls chain (IMPL-0029 4.4)
- *(monitoring)* Regenerate the static tier for controls (IMPL-0029 6.8)
- *(examples)* Replace the guardian.hcl examples with the policy map (IMPL-0029 7.10)

## [1.9.0] - 2026-06-23

Bundled with chart `1.0.0-rc.1`. IMPL-0016 (memory backend
removal). Validated via the `1.0.0-rc.N` series before cutting
final `1.0.0`.

### Removed (BREAKING)

- *(store)* Delete `internal/store/memory` — Postgres is the only
  supported `Store` backend ([IMPL-0016](docs/impl/0016-deprecate-memory-backend.md)).
- *(queue)* Delete `internal/queue/memory` — Valkey is the only
  supported `Queue` backend.
- *(scheduler)* Delete `internal/scheduler/ticker` — Valkey is
  the only supported `Scheduler` backend.
- *(config)* `STORE_BACKEND=memory`, `QUEUE_BACKEND=memory`, and
  `SCHEDULER_BACKEND=ticker` are now hard errors at startup. The
  error includes a migration URL pointing at
  `docs/operations/migrations.md#removing-memory-backend`.

### Features

- *(dev)* `docker-compose.dev.yaml` at the repo root + new
  `make dev-services` / `make dev-stop` targets. `make run-local`
  now depends on `dev-services` and wires the DSN env vars
  automatically, so a fresh checkout can `make run-local`
  end-to-end with no manual Postgres / Valkey provisioning.

### Documentation

- *(ops)* New `docs/operations/migrations.md#removing-memory-backend`
  section with baked / cnpg / external migration recipes.
- *(ops)* Superseded-by banners on `chart-0.5.0-migration.md` and
  `cnpg-homelab-cutover.md`.
- *(chart)* Chart README's deployment-shape table dropped the
  "Single-replica memory" row; new "Schema validation"
  subsection describes the `values.schema.json` rejection.

See [DESIGN-0018](docs/design/0018-deprecate-memory-backend.md)
for design rationale.

## [1.8.0] - 2026-05-31

### Features

- *(policy)* Add non_empty assertion type for yaml_path ([#92](https://github.com/donaldgifford/repo-guardian/issues/92))

### Miscellaneous Tasks

- *(chart)* Add revisionHistoryLimit, bump PG 16.4 → 17.4, CNPG cutover runbook ([#91](https://github.com/donaldgifford/repo-guardian/issues/91))

## [1.7.0] - 2026-05-31

### Bug Fixes

- *(ci)* Use azure/setup-helm + direct plugin install for helm-unittest ([#76](https://github.com/donaldgifford/repo-guardian/issues/76))

### Refactor

- *(checker)* IMPL-0014 Phase 1 collapse legacy engine path ([#85](https://github.com/donaldgifford/repo-guardian/issues/85))

### Documentation

- *(ops)* Chart 0.5.0 migration runbook (IMPL-0011) ([#75](https://github.com/donaldgifford/repo-guardian/issues/75))
- *(claude)* Capture CI paths-filter convention + helm CLI gotchas ([#78](https://github.com/donaldgifford/repo-guardian/issues/78))
- *(inv)* INV-0005 stale PRs when file rules become satisfied on main ([#80](https://github.com/donaldgifford/repo-guardian/issues/80))
- *(inv)* INV-0006 per-org GitHub App credentials (deferred) ([#81](https://github.com/donaldgifford/repo-guardian/issues/81))
- *(impl)* IMPL-0013 reconcile open PRs when file rules become satisfied ([#82](https://github.com/donaldgifford/repo-guardian/issues/82))
- *(inv)* INV-0007 GitLab forge backend + INV-0006 scale clarification ([#84](https://github.com/donaldgifford/repo-guardian/issues/84))
- DESIGN-0014 + IMPL-0014 — remove legacy engine path and deprecated overlays ([#83](https://github.com/donaldgifford/repo-guardian/issues/83))
- IMPL-0014 Phase 3 purge legacy markdown + mkdocs nav block ([#87](https://github.com/donaldgifford/repo-guardian/issues/87))
- *(0014)* Mark DESIGN-0014 + IMPL-0014 as Implemented ([#88](https://github.com/donaldgifford/repo-guardian/issues/88))
- *(0014)* Check off final cross-phase verification ([#89](https://github.com/donaldgifford/repo-guardian/issues/89))

### Miscellaneous Tasks

- *(ci)* Per-job paths filter via dorny/paths-filter ([#77](https://github.com/donaldgifford/repo-guardian/issues/77))
- *(deploy)* IMPL-0014 Phase 2 remove Kustomize tree ([#86](https://github.com/donaldgifford/repo-guardian/issues/86))
- *(release)* Cut v1.7.0 rollup (chart 0.6.1, appVersion 1.7.0) ([#90](https://github.com/donaldgifford/repo-guardian/issues/90))

## [1.6.0] - 2026-05-06

### Features

- Persistent reconcile state + multi-replica coordination (IMPL-0011) ([#74](https://github.com/donaldgifford/repo-guardian/issues/74))

## [1.5.0] - 2026-05-05

### Features

- Customizable PR templates + extensible template ConfigMap (IMPL-0012) ([#72](https://github.com/donaldgifford/repo-guardian/issues/72))

### Miscellaneous Tasks

- *(chart)* Bump 0.3.2 → 0.3.3 (carry appVersion 1.4.1) ([#70](https://github.com/donaldgifford/repo-guardian/issues/70))

## [1.4.1] - 2026-05-03

### Features

- *(chart)* Publish to OCI registry with cosign + SLSA (DESIGN-0011 / IMPL-0010) ([#60](https://github.com/donaldgifford/repo-guardian/issues/60))

### Bug Fixes

- *(chart)* Authenticate cosign to GHCR via docker login (post-mortem 0.3.0) ([#61](https://github.com/donaldgifford/repo-guardian/issues/61))
- *(chart)* Stamp namespace into every template (0.3.1 → 0.3.2) ([#67](https://github.com/donaldgifford/repo-guardian/issues/67))
- *(github)* Idempotent CreateOrUpdateFile (INV-0003) + appVersion 1.4.1 ([#69](https://github.com/donaldgifford/repo-guardian/issues/69))

### Documentation

- *(chart)* Refresh install + verify + yank docs (IMPL-0010 P6) ([#62](https://github.com/donaldgifford/repo-guardian/issues/62))
- *(impl)* Mark IMPL-0010 In Progress with phase summary ([#64](https://github.com/donaldgifford/repo-guardian/issues/64))
- *(impl)* IMPL-0010 Phase 4 complete — GHCR public verified ([#65](https://github.com/donaldgifford/repo-guardian/issues/65))
- IMPL-0010 retrospective + DESIGN-0011 Implemented ([#66](https://github.com/donaldgifford/repo-guardian/issues/66))
- Capture chart 0.3.2 namespace-stamping post-mortem ([#68](https://github.com/donaldgifford/repo-guardian/issues/68))

### Miscellaneous Tasks

- *(tooling)* Remove helm-cr leftovers (IMPL-0010 P9) ([#63](https://github.com/donaldgifford/repo-guardian/issues/63))

## [1.4.0] - 2026-04-26

### Bug Fixes

- Create public ghpages mkdocs file ([#52](https://github.com/donaldgifford/repo-guardian/issues/52))

### Miscellaneous Tasks

- Adding gh-pages for docs ([#51](https://github.com/donaldgifford/repo-guardian/issues/51))
- Updated ci action versions ([#53](https://github.com/donaldgifford/repo-guardian/issues/53))

## [1.3.9] - 2026-03-16

### Miscellaneous Tasks

- Add HCL policy examples and fix org field decoding

## [1.3.8] - 2026-03-16

### Features

- *(templates)* Add renovate workflow template and update config template
- *(policy)* Add Renovate file rules and org config to policy defaults

### Bug Fixes

- *(chart)* Add GITHUB_ORG env var to deployment template

### Documentation

- *(design)* Add DESIGN-0009 distributed Renovate via per-repo GitHub Actions
- *(impl)* Add IMPL-0008 for distributed Renovate file management
- Add Renovate HCL examples and update design status
- *(readme)* Add Renovate rules and GITHUB_ORG to README

### Testing

- *(policy)* Add assertion and engine tests for Renovate rules
- *(checker)* Add integration tests for Renovate file rules

### Miscellaneous Tasks

- Mark IMPL-0008 as Completed

## [repo-guardian-0.2.0] - 2026-03-15

### Bug Fixes

- *(chart)* Bump chart version to trigger chart-releaser

## [1.3.7] - 2026-03-15

### Features

- *(policy)* Add global and per-rule ignore lists with glob matching
- *(github)* Add client methods for settings, rulesets, and labels
- *(policy)* Implement setting rules with check and remediation
- *(policy)* Implement branch protection rules with rulesets API
- *(reconciler)* Implement label_sync reconciler
- *(reconciler)* Implement branch_protection reconciler
- *(reconciler)* Add workflow sync reconciler

### Documentation

- Update statuses, indexes, and architecture for IMPL-0007

### Testing

- Add integration tests for IMPL-0007 Phase 8

## [1.3.6] - 2026-03-15

### Features

- *(reconciler)* Add Reconciler interface and registry
- *(reconciler)* Add custom_properties reconciler
- *(checker)* Integrate reconcilers into CheckRepo flow
- *(policy)* Add backward compatibility for CUSTOM_PROPERTIES_MODE env var
- *(webhook)* Add push event handler for watched file changes
- *(policy)* Add ExtractWatchedPaths utility for push event handling

### Bug Fixes

- *(reconciler)* Fix golines formatting in custom_properties.go

### Refactor

- *(checker)* Remove legacy custom properties code path

### Documentation

- Update statuses and regenerate docz indexes
- Update statuses, CLAUDE.md, and legacy docs for IMPL-0006

## [1.3.5] - 2026-03-15

### Features

- *(policy)* Add Go types and HCL schema for policy configuration
- *(policy)* Add built-in defaults matching current hardcoded behavior
- *(policy)* Add HCL parser and config loader
- *(policy)* Add config validation with clear error messages
- *(policy)* Add minimal YAML path evaluator
- *(policy)* Add content assertion evaluation engine
- *(checker)* Add policy-based rule engine with three check modes
- *(main)* Wire policy config into application startup
- *(chart)* Add policy ConfigMap support for HCL config

### Bug Fixes

- *(policy)* Preallocate error slices in validation

### Documentation

- *(design)* Add DESIGN-0006 push event handler for catalog-info changes
- *(rfc)* Add RFC-0002 HCL-driven policy engine for repo-guardian
- *(rfc)* Resolve all open questions in RFC-0002
- *(design)* Add DESIGN-0006, 0007, 0008 for HCL policy engine
- *(design)* Resolve all open questions in DESIGN-0006, 0007, 0008
- *(impl)* Add implementation plans for HCL policy engine
- *(impl)* Resolve open questions in IMPL-0005/0006/0007
- Add policy engine documentation to README and CLAUDE.md

## [1.3.4] - 2026-03-14

### Features

- *(helm)* Scaffold chart with core templates
- *(helm)* Add Tailscale sidecar and ServiceMonitor templates
- *(helm)* Add CI values, chart-testing config, and yamllint

### Bug Fixes

- *(chart)* Fix ct install CrashLoopBackOff with busybox stub
- *(chart)* Use 15m sleep instead of infinity for CI stub

### Documentation

- Update README with security, metrics, and Tailscale docs
- *(design)* Resolve open questions and add OCI registry support
- *(impl)* Add IMPL-0004 for Helm chart implementation plan
- *(impl)* Add open questions and refine values coverage
- *(impl)* Resolve all open questions in IMPL-0004
- Align IMPL-0004 tasks with DESIGN-0005 requirements
- *(helm)* Generate chart README with helm-docs
- Add Helm deployment instructions and mark Kustomize deprecated
- *(helm)* Add release instructions for GitHub Pages and OCI registry

### Testing

- *(helm)* Add helm-unittest test suite

### Miscellaneous Tasks

- *(helm)* Add Makefile targets for helm operations
- *(helm)* Add CI workflows for chart testing and release

## [1.3.3] - 2026-03-14

### Features

- *(config)* Add webhook IP allowlist configuration fields
- *(metrics)* Add webhook rejected counter for IP allowlist
- *(webhook)* Add GitHub IP allowlist with middleware
- *(main)* Wire IP allowlist middleware on webhook route

### Documentation

- Initialize docz and migrate existing docs to structured format
- Add Tailscale Funnel investigation and webhook IP allowlist design
- Add IP allowlist impl plan and Helm chart design doc
- Update documentation for webhook IP allowlist
- Add SECURITY.md with webhook protection details
- Close Tailscale Funnel IP investigation (Phase 7)

### Testing

- *(webhook)* Add comprehensive allowlist test coverage

### Miscellaneous Tasks

- *(webhook)* Add temporary debug logging for Funnel IP investigation

## [1.3.2] - 2026-03-14

### Bug Fixes

- Add secret manifest and support private key from env var

## [1.3.1] - 2026-03-14

### Documentation

- Expand summary and one-pager with custom properties coverage

## [1.3.0] - 2026-02-13

### Features

- Add catalog-info.yaml parser package
- Extend GitHub client with file content and custom properties methods
- Add CUSTOM_PROPERTIES_MODE configuration
- Add custom properties Prometheus metrics
- Add custom properties and catalog-info templates
- Implement custom properties checker with two-mode support
- Wire custom properties into engine and main

### Documentation

- Add executive summary and Amazon-style one-pager
- Add contrib examples and rule walkthrough guide
- Add custom properties feature plan
- Revise custom properties plan per review feedback
- Add two-mode architecture to custom properties plan
- Add detailed implementation plan for custom properties
- Update documentation for custom properties feature

### Testing

- Add comprehensive custom properties test coverage

## [1.2.2] - 2026-02-10

### Bug Fixes

- Resolve remaining code review issues (#15, #16, #18)

## [1.2.1] - 2026-02-10

### Bug Fixes

- Resolve 5 code review issues and expand documentation

## [1.2.0] - 2026-02-10

### Features

- *(metrics)* Add rate limit wait metrics
- *(config)* Add RateLimitThreshold config field
- *(github)* Add rate limit transport with pre-emptive throttling
- Add local dev setup with Docker Compose and ngrok tunnel

### Bug Fixes

- *(ci)* Prevent bake from auto-discovering compose.yaml

### Miscellaneous Tasks

- Add RATE_LIMIT_THRESHOLD to .env.example

## [1.1.3] - 2026-02-09

### Other

- Docker CI and image build

## [1.1.2] - 2026-02-09

### Bug Fixes

- Use local source for bake-action and add metadata-action target

## [1.1.0] - 2026-02-09

### Features

- Define GitHub client interface and types
- Add FileRule registry and template store
- Implement GitHub client with go-github and ghinstallation
- Add configuration package with env var parsing
- Implement checker engine with idempotent PR creation
- Add work queue with buffered channel and worker pool
- Add webhook handler with signature validation
- Add reconciliation scheduler with startup run
- Add Prometheus metrics and instrument all packages
- Wire main entrypoint with HTTP servers and graceful shutdown
- Add multi-stage Dockerfile with distroless runtime
- Add Kubernetes manifests with Kustomize base and overlays
- Add Docker Bake for multi-arch image builds and CI/CD publishing

### Miscellaneous Tasks

- Update CLAUDE.md and implementation plan with completed status

## [0.0.1] - 2026-02-06

