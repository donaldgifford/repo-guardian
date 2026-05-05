# Helm Chart Changelog

Changes to the `repo-guardian` Helm chart only. For application-level
changes, see the root [CHANGELOG.md](../../CHANGELOG.md).
## [unreleased]

### Features

- *(chart)* [**breaking**] Generic templates.files map + templating.vars + STRICT_TEMPLATES (IMPL-0012 Phase 6)

### Documentation

- Examples + customizing-PR-text + migration guide (IMPL-0012 Phase 7)
- *(chart)* Homelab smoke runbook for IMPL-0012 Phase 7.4 acceptance

### Testing

- *(chart)* Bump deployment_test image-tag pattern to 1.5.0
- *(checker)* Lock IMPL-0012 homelab smoke chain in CI

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

## [1.3.8] - 2026-03-16

### Bug Fixes

- *(chart)* Add GITHUB_ORG env var to deployment template

## [repo-guardian-0.2.0] - 2026-03-15

### Bug Fixes

- *(chart)* Bump chart version to trigger chart-releaser

## [1.3.5] - 2026-03-15

### Features

- *(chart)* Add policy ConfigMap support for HCL config

## [1.3.4] - 2026-03-14

### Features

- *(helm)* Scaffold chart with core templates
- *(helm)* Add Tailscale sidecar and ServiceMonitor templates
- *(helm)* Add CI values, chart-testing config, and yamllint

### Bug Fixes

- *(chart)* Fix ct install CrashLoopBackOff with busybox stub
- *(chart)* Use 15m sleep instead of infinity for CI stub

### Documentation

- *(helm)* Generate chart README with helm-docs
- *(helm)* Add release instructions for GitHub Pages and OCI registry

### Testing

- *(helm)* Add helm-unittest test suite

