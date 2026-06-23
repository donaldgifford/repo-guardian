# Helm Chart Changelog

Changes to the `repo-guardian` Helm chart only. For application-level
changes, see the root [CHANGELOG.md](../../CHANGELOG.md).

## [1.0.0-rc.1] - 2026-06-23

First release candidate on the path to chart `1.0.0`. Ships the
memory-backend removal (IMPL-0016). Cut from
`feat/impl-0016-remove-memory-backend`; carries appVersion
`1.9.0`. Will be validated in homelab as `1.0.0-rc.1` (and any
follow-up `1.0.0-rc.N` tags from IMPL-0015) before promoting to
final `1.0.0`.

### Breaking changes

- `store.backend` enum now `["postgres"]` only. `memory` is
  rejected by `values.schema.json` at chart-render time.
- `queue.backend` enum now `["valkey"]` only. `memory` rejected.
- `scheduler.backend` enum now `["valkey"]` only. `ticker`
  rejected.
- Default `store.backend` / `queue.backend` / `scheduler.backend`
  flipped from `memory` / `memory` / `ticker` →
  `postgres` / `valkey` / `valkey`. A fresh `helm install` with
  no values overrides now brings up baked Postgres + baked
  Valkey StatefulSets out-of-the-box.
- The binary refuses to start when `STORE_BACKEND=memory` (or
  any other removed value) is set on the Deployment. Error
  message embeds the migration URL.

### Migration

See
[docs/operations/migrations.md#removing-memory-backend](../../docs/operations/migrations.md#removing-memory-backend)
for the three migration paths (baked / cnpg / external).

### Features

- *(chart)* New `values.schema.json` locks the three backend
  enums.
- *(chart)* Multi-replica deployment shapes (IMPL-0011 P6) ship
  as the only supported configurations.

### Documentation

- *(chart)* README deployment-shape table no longer lists the
  "Single-replica memory" row; new "Schema validation"
  subsection; "Upgrade notes (chart 1.0.0-rc.1) — breaking"
  block at the top of the release notes.

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

