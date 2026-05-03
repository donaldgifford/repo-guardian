# Changelog

All notable changes to this project are documented here. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
this project adheres to [Semantic Versioning](https://semver.org/).
## [unreleased]

### Features

- *(chart)* Publish to OCI registry with cosign + SLSA (DESIGN-0011 / IMPL-0010) ([#60](https://github.com/donaldgifford/repo-guardian/issues/60))

### Bug Fixes

- *(chart)* Authenticate cosign to GHCR via docker login (post-mortem 0.3.0) ([#61](https://github.com/donaldgifford/repo-guardian/issues/61))

### Documentation

- *(chart)* Refresh install + verify + yank docs (IMPL-0010 P6) ([#62](https://github.com/donaldgifford/repo-guardian/issues/62))
- *(impl)* Mark IMPL-0010 In Progress with phase summary ([#64](https://github.com/donaldgifford/repo-guardian/issues/64))
- *(impl)* IMPL-0010 Phase 4 complete — GHCR public verified ([#65](https://github.com/donaldgifford/repo-guardian/issues/65))
- IMPL-0010 retrospective + DESIGN-0011 Implemented ([#66](https://github.com/donaldgifford/repo-guardian/issues/66))

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

