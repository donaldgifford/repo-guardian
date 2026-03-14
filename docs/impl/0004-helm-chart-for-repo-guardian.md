---
id: IMPL-0004
title: "Helm Chart for repo-guardian"
status: Draft
author: Donald Gifford
created: 2026-03-14
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0004: Helm Chart for repo-guardian

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-03-14

## Objective

Replace the Kustomize-based deployment with a Helm chart that packages all
repo-guardian Kubernetes resources, supports dev/prod/Tailscale scenarios via
values, and integrates chart testing and release tooling.

**Implements:** DESIGN-0005

## Scope

### In Scope

- Helm chart with all templates (deployment, service, configmap, secret, etc.)
- Tailscale sidecar toggle via values
- helm-unittest test suite
- Makefile targets for helm operations
- CI workflows for chart linting, testing, and release
- OCI registry push support
- helm-docs generated README

### Out of Scope

- Removing Kustomize overlays (future work after validation)
- Helm Operator / Flux HelmRelease CRDs
- Third-party subcharts (Prometheus, Grafana)

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its tasks
are checked off and its success criteria are met.

---

### Phase 1: Chart Scaffold and Core Templates

Create the chart directory structure, Chart.yaml, values.yaml, and the core
Kubernetes resource templates.

#### Tasks

- [ ] Create `charts/repo-guardian/Chart.yaml` (apiVersion v2, type application)
- [ ] Create `charts/repo-guardian/.helmignore`
- [ ] Create `charts/repo-guardian/values.yaml` with helm-docs annotations (include `config.scheduleInterval`, `config.skipForks`, `config.skipArchived`, `extraEnv`, `extraVolumes`, `extraVolumeMounts`)
- [ ] Create `charts/repo-guardian/templates/_helpers.tpl` (name, fullname, labels, selectorLabels, serviceAccountName, secretName)
- [ ] Create `charts/repo-guardian/templates/deployment.yaml` (app container, env vars from config/secrets, volume mounts, probes, extraEnv/extraVolumes/extraVolumeMounts)
- [ ] Create `charts/repo-guardian/templates/service.yaml` (ClusterIP, http + metrics ports)
- [ ] Create `charts/repo-guardian/templates/serviceaccount.yaml` (conditional on `serviceAccount.create`)
- [ ] Create `charts/repo-guardian/templates/configmap.yaml` (template file overrides)
- [ ] Create `charts/repo-guardian/templates/secret.yaml` (conditional on `secrets.create`, supports file mount and env var modes)
- [ ] Create `charts/repo-guardian/templates/NOTES.txt` (post-install instructions)
- [ ] Verify `helm lint charts/repo-guardian` passes
- [ ] Verify `helm template charts/repo-guardian` renders correctly

#### Success Criteria

- `helm lint charts/repo-guardian` passes with no errors
- `helm template repo-guardian charts/repo-guardian` renders all resources
- Default values match current Kustomize base configuration
- Private key file mount and env var modes both render correctly

---

### Phase 2: Tailscale Sidecar and ServiceMonitor

Add optional Tailscale Funnel sidecar injection and Prometheus ServiceMonitor.

#### Tasks

- [ ] Add Tailscale sidecar container to `deployment.yaml` (conditional on `tailscale.enabled`)
- [ ] Create Tailscale Funnel serve config in `configmap.yaml` or separate template (conditional)
- [ ] Add Tailscale state emptyDir volume
- [ ] Add Tailscale RBAC (Role + RoleBinding) template (conditional on `tailscale.rbac.create`)
- [ ] Set `TRUST_PROXY_HEADERS=true` when Tailscale is enabled
- [ ] Create `charts/repo-guardian/templates/servicemonitor.yaml` (conditional on `serviceMonitor.enabled`)
- [ ] Verify Tailscale sidecar renders with `helm template --set tailscale.enabled=true`
- [ ] Verify ServiceMonitor renders with `helm template --set serviceMonitor.enabled=true`

#### Success Criteria

- Tailscale sidecar only appears when `tailscale.enabled=true`
- RBAC resources only appear when `tailscale.rbac.create=true`
- ServiceMonitor only appears when `serviceMonitor.enabled=true`
- `helm lint` still passes with Tailscale and ServiceMonitor enabled

---

### Phase 3: helm-unittest Test Suite

Write unit tests for all templates to validate rendering logic.

#### Tasks

- [ ] Create `charts/repo-guardian/tests/deployment_test.yaml` (replica count, image tag, env vars, volume mounts, Tailscale sidecar, probes)
- [ ] Create `charts/repo-guardian/tests/service_test.yaml` (port mappings, service type)
- [ ] Create `charts/repo-guardian/tests/configmap_test.yaml` (template content, custom overrides)
- [ ] Create `charts/repo-guardian/tests/secret_test.yaml` (create vs existing, file mount vs env var)
- [ ] Create `charts/repo-guardian/tests/serviceaccount_test.yaml` (conditional creation, annotations)
- [ ] Create `charts/repo-guardian/tests/servicemonitor_test.yaml` (conditional creation, interval, labels)
- [ ] Run `helm unittest charts/repo-guardian` and verify all tests pass

#### Success Criteria

- All helm-unittest tests pass
- Tests cover conditional rendering (enabled/disabled toggles)
- Tests cover both private key modes (file mount and env var)
- Tests cover Tailscale sidecar injection

---

### Phase 4: CI Values and chart-testing Config

Set up chart-testing configuration and CI values for kind cluster installs.

#### Tasks

- [ ] Create `charts/repo-guardian/ci/ci-values.yaml` (busybox image, disabled probes, test secrets)
- [ ] Create `ct.yaml` (chart-dirs, target-branch, lint-conf)
- [ ] Create `charts/.yamllint.yml` (chart-specific yamllint config)
- [ ] Verify `ct lint --config ct.yaml --all` passes

#### Success Criteria

- `ct lint --config ct.yaml --all` passes
- `ci-values.yaml` allows chart installation without real GitHub credentials

---

### Phase 5: Makefile Targets

Add helm-related targets to the Makefile.

#### Tasks

- [ ] Add `helm-lint` target
- [ ] Add `helm-template` target (default values)
- [ ] Add `helm-template-ci` target (CI values)
- [ ] Add `helm-package` target
- [ ] Add `helm-unittest` target
- [ ] Add `helm-test` target (lint + unittest)
- [ ] Add `helm-ct-lint` target
- [ ] Add `helm-ct-list-changed` target
- [ ] Add `helm-ct-install` target
- [ ] Add `helm-docs` target
- [ ] Add `helm-diff-check` target
- [ ] Add `helm-cr-package` target
- [ ] Add `helm-push` target (OCI registry)
- [ ] Verify all targets work locally

#### Success Criteria

- All Makefile targets execute without errors
- `make helm-test` runs lint and unittest end-to-end

---

### Phase 6: helm-docs README Generation

Generate the chart README automatically from values annotations.

#### Tasks

- [ ] Create `charts/repo-guardian/README.md.gotmpl` (optional, for custom README structure)
- [ ] Run `helm-docs` and verify generated README
- [ ] Verify all values are documented in the generated table

#### Success Criteria

- `helm-docs` generates a complete README with all values documented
- README includes chart description, usage instructions, and values table

---

### Phase 7: CI Workflows

Add GitHub Actions workflows for chart linting, testing, and release.

#### Tasks

- [ ] Add helm-unittest job to `.github/workflows/ci.yml`
- [ ] Add helm chart-testing job to `.github/workflows/ci.yml` (ct lint + ct install)
- [ ] Add helm lint step to existing lint job in CI
- [ ] Create `.github/workflows/chart-release.yml` (chart-releaser to GitHub Pages)
- [ ] Add OCI registry push job to chart-release workflow (conditional on `HELM_OCI_REGISTRY` var)

#### Success Criteria

- CI runs helm-unittest on every push
- CI runs ct lint on chart changes
- Chart release workflow publishes to GitHub Pages
- OCI push job is skipped when `HELM_OCI_REGISTRY` is not set

---

### Phase 8: Documentation and Cleanup

Update project documentation and verify everything is consistent.

#### Tasks

- [ ] Update `README.md` with Helm deployment instructions
- [ ] Mark `deploy/` Kustomize overlays as deprecated in docs
- [ ] Update `CLAUDE.md` with Helm chart architecture notes
- [ ] Verify `make ci` still passes (existing Go CI unaffected)
- [ ] Verify `make helm-test` passes

#### Success Criteria

- README includes Helm install/upgrade instructions
- Kustomize deprecation is documented
- All CI checks pass (Go + Helm)

---

## Open Questions

*All resolved.*

1. ~~**Schedule interval in values.yaml**~~: **Resolved.** First-class field.
   Add `config.scheduleInterval` to `values.yaml` (default `168h`). It's one of
   the most common per-environment overrides.

2. ~~**Tailscale state persistence**~~: **Resolved.** Keep `emptyDir` only for
   now. Defer `kubeSecret`-backed persistent state to a future iteration.

3. ~~**Extra env vars and volumes**~~: **Resolved.** Yes, include `extraEnv`,
   `extraVolumes`, and `extraVolumeMounts` escape hatches. Easier to add now
   than retrofit later.

4. ~~**Namespace**~~: **Resolved.** Follow Helm convention. No default namespace
   in values or templates. Users pass `--namespace` at install time.

5. ~~**`appVersion` tracking**~~: **Resolved.** Manual bump. Chart version and
   appVersion are independent release artifacts. Bump `appVersion` in
   `Chart.yaml` when referencing a new app release.

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `charts/repo-guardian/Chart.yaml` | Create | Chart metadata |
| `charts/repo-guardian/.helmignore` | Create | Helm ignore patterns |
| `charts/repo-guardian/values.yaml` | Create | Default values with helm-docs annotations |
| `charts/repo-guardian/templates/_helpers.tpl` | Create | Template helpers |
| `charts/repo-guardian/templates/deployment.yaml` | Create | Deployment with optional Tailscale sidecar |
| `charts/repo-guardian/templates/service.yaml` | Create | ClusterIP service |
| `charts/repo-guardian/templates/serviceaccount.yaml` | Create | Optional ServiceAccount |
| `charts/repo-guardian/templates/configmap.yaml` | Create | Template file overrides |
| `charts/repo-guardian/templates/secret.yaml` | Create | GitHub App credentials |
| `charts/repo-guardian/templates/servicemonitor.yaml` | Create | Optional Prometheus ServiceMonitor |
| `charts/repo-guardian/templates/NOTES.txt` | Create | Post-install notes |
| `charts/repo-guardian/tests/*.yaml` | Create | helm-unittest test cases |
| `charts/repo-guardian/ci/ci-values.yaml` | Create | CI install values |
| `ct.yaml` | Create | chart-testing config |
| `charts/.yamllint.yml` | Create | Chart yamllint config |
| `Makefile` | Modify | Add helm targets |
| `.github/workflows/ci.yml` | Modify | Add helm-unittest and ct jobs |
| `.github/workflows/chart-release.yml` | Create | Chart release workflow |
| `README.md` | Modify | Add Helm deployment docs |
| `CLAUDE.md` | Modify | Add Helm chart notes |

## Testing Plan

- [ ] helm-unittest for all templates (conditional rendering, values overrides)
- [ ] `ct lint` for YAML and Chart.yaml validation
- [ ] `ct install` in kind cluster with CI values
- [ ] `helm template` with default and custom values
- [ ] `helm diff` for upgrade preview validation

## Dependencies

- Helm 3.19+ (managed via mise)
- helm-unittest plugin
- helm-cr, helm-ct, helm-diff, helm-docs (managed via mise)
- GitHub Pages branch (`gh-pages`) for chart-releaser
- ECR repository for OCI push (optional, production only)

## References

- [DESIGN-0005: Helm Chart for repo-guardian](../design/0005-helm-chart-for-repo-guardian.md)
- [server-price-tracker Helm chart](https://github.com/donaldgifford/server-price-tracker)
- [helm-unittest documentation](https://github.com/helm-unittest/helm-unittest)
- [chart-releaser-action](https://github.com/helm/chart-releaser-action)
