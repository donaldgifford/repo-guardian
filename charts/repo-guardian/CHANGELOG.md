# Helm Chart Changelog

Changes to the `repo-guardian` Helm chart only. For application-level
changes, see the root [CHANGELOG.md](../../CHANGELOG.md).
## [unreleased]

### Miscellaneous Tasks

- *(chart)* Bump chart 0.3.0 / appVersion 1.4.0 (IMPL-0010 P1.1-3)

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

