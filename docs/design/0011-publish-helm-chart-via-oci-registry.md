---
id: DESIGN-0011
title: "Publish Helm chart via OCI registry"
status: Approved
author: Donald Gifford
created: 2026-05-02
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0011: Publish Helm chart via OCI registry

**Status:** Approved
**Author:** Donald Gifford
**Date:** 2026-05-02

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [Distribution target](#distribution-target)
  - [Workflow trigger](#workflow-trigger)
  - [Authentication](#authentication)
  - [Versioning and tagging](#versioning-and-tagging)
  - [chart-releaser deprecation](#chart-releaser-deprecation)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Resolved Questions](#resolved-questions)
- [References](#references)
<!--toc:end-->

## Overview

Publish the `repo-guardian` Helm chart to GitHub Container Registry
(GHCR) as an OCI artifact at `oci://ghcr.io/donaldgifford/charts`,
mirroring the distribution model used by the Atlantis chart in the
homelab cluster. This replaces the existing (un-run) chart-releaser
gh-pages workflow, which conflicts with the docs site already hosted
on the `gh-pages` branch.

The Atlantis chart in `homelab/k8s/homelab-cluster/apps/cicd/atlantis/`
is consumed via `kustomize` `helmCharts:` referencing
`oci://ghcr.io/runatlantis/charts`. Adopting the same pattern lets the
homelab repo's GitOps flow consume `repo-guardian` without bespoke
bootstrap.

## Goals and Non-Goals

### Goals

- Publish chart artifacts to `oci://ghcr.io/donaldgifford/charts/repo-guardian`
  on every chart-version bump merged to `main`.
- Sign chart artifacts with **cosign** (Sigstore keyless signing via the
  workflow's OIDC identity), enabling `cosign verify` for provenance.
- Make chart releases idempotent: re-running the workflow for an
  unchanged version is a no-op, not an error.
- Keep the `gh-pages` branch reserved for the mkdocs documentation site.
- Document the consumption pattern (`helmCharts:` in kustomize, ArgoCD
  Application with `chart:`) so the homelab migration in
  IMPL-0010 has a working upstream.

### Non-Goals

- Mirror to additional registries (Docker Hub, ECR, GAR). The existing
  optional `helm-oci-push` job in `chart-release.yml` already handles
  ECR if `vars.HELM_OCI_REGISTRY` is set; that escape hatch stays.
- Publish a Helm repo index over HTTP (`https://...index.yaml`). OCI
  obsoletes the index file model.
- Auto-bump the chart version on every binary release. Chart and binary
  versions stay decoupled — IMPL-0010 will introduce explicit
  bump tooling.
- GHCR retention or lifecycle policy. Out of scope; covered by GitHub's
  default container retention.

## Background

The chart was scaffolded as part of IMPL-0004. The release workflow at
`.github/workflows/chart-release.yml` was added with two jobs:

1. `chart-release` — uses `helm/chart-releaser-action@v1` to push to
   the `gh-pages` branch.
2. `helm-oci-push` — pushes to ECR, gated on `vars.HELM_OCI_REGISTRY`.

Neither job has ever run. The workflow is `workflow_dispatch` only and
the `gh-pages` branch is now serving the mkdocs-material docs site
(see PR #51, #52). Running `chart-releaser` against `gh-pages` would
clobber the docs.

The cluster ApplicationSet at
`homelab/k8s/homelab-cluster/apps/cicd/application-set.yaml` scans
`apps/cicd/*/` and creates ArgoCD Applications. The Atlantis directory
demonstrates the working pattern:

```yaml
# atlantis/kustomization.yaml
helmCharts:
  - name: atlantis
    repo: oci://ghcr.io/runatlantis/charts
    version: 6.2.0
    releaseName: atlantis
    namespace: atlantis
    valuesFile: values.yaml
```

For the in-flight repo-guardian Kustomize → Helm migration to land,
the chart needs a published OCI URL.

## Detailed Design

### Distribution target

Publish to `oci://ghcr.io/donaldgifford/charts` with chart name
`repo-guardian`. The full reference for a release is:

```text
oci://ghcr.io/donaldgifford/charts/repo-guardian:<chart-version>
```

GHCR is chosen over alternatives because:

- Already hosting the binary container image at
  `ghcr.io/donaldgifford/repo-guardian` — same auth model, same
  retention defaults, same visibility controls.
- Free for public artifacts.
- Helm 3.8+ has stable OCI support; no helm-cm or experimental flags.
- Atlantis precedent already in the cluster proves the consumption
  path works under Talos + ArgoCD.

The package will be marked **public** under the user's GHCR settings,
matching the binary image visibility. No secrets are baked into chart
templates; values are operator-supplied at install time.

### Workflow trigger

Replace the `workflow_dispatch`-only trigger with two paths:

1. **`push` to `main`** scoped to `charts/**` and
   `.github/workflows/chart-release.yml`. Skips when the chart version
   in `Chart.yaml` matches a tag already present in the registry
   (idempotent re-runs).
2. **`workflow_dispatch`** retained for manual republish (e.g., after
   a bad delete or registry outage).

Pull requests do NOT publish — only post-merge pushes to `main`. PR
validation continues to run via `helm-ct` and `helm-unittest` in
`ci.yml`.

### Authentication

Use `GITHUB_TOKEN` with the workflow-scoped `packages: write`
permission for the push, plus `id-token: write` for cosign keyless
signing. No long-lived PAT or AWS role required:

```yaml
permissions:
  contents: read
  packages: write
  id-token: write   # for cosign keyless signing via OIDC

steps:
  - run: |
      echo "${{ secrets.GITHUB_TOKEN }}" | \
        helm registry login ghcr.io \
          -u "${{ github.actor }}" \
          --password-stdin
      helm package charts/repo-guardian
      helm push repo-guardian-*.tgz oci://ghcr.io/donaldgifford/charts \
        2>&1 | tee /tmp/push.log
      digest=$(grep -oE 'sha256:[a-f0-9]{64}' /tmp/push.log | head -1)
      cosign sign --yes \
        "ghcr.io/donaldgifford/charts/repo-guardian@${digest}"
```

Consumers verify provenance with:

```bash
cosign verify \
  --certificate-identity-regexp '^https://github.com/donaldgifford/repo-guardian/.+' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  ghcr.io/donaldgifford/charts/repo-guardian:<chart-version>
```

The first push from a workflow auto-creates the OCI repository with
**private** visibility. A one-time manual step in GHCR settings flips
it to **public** and links it to the `repo-guardian` source repo so
the package inherits the repo's badges and discovery.

### Versioning and tagging

The chart's `version` in `Chart.yaml` is the source of truth for the
OCI tag. Idempotency is enforced by checking whether the tag already
exists before push:

```bash
chart_version=$(yq '.version' charts/repo-guardian/Chart.yaml)
if helm pull "oci://ghcr.io/donaldgifford/charts/repo-guardian" \
     --version "${chart_version}" --destination /tmp 2>/dev/null; then
  echo "chart ${chart_version} already published, skipping"
  exit 0
fi
```

`appVersion` is independent and tracks the binary release tag (e.g.,
`v1.4.0` after this session). A separate IMPL doc will introduce
tooling for keeping `appVersion` in sync with binary releases without
auto-bumping the chart version.

### chart-releaser deprecation

The `chart-release` job in the existing workflow is removed entirely.
The `helm-oci-push` job is rewritten to target GHCR by default, with
ECR remaining as an optional secondary push gated on
`vars.HELM_OCI_REGISTRY`.

The `gh-pages` branch is left untouched — it continues to serve the
mkdocs docs site.

## API / Interface Changes

No application API changes. The following user-facing surface changes:

- **Chart consumption URL** — the README's "Quick install" instruction
  changes from a future `helm repo add ...` to:

  ```bash
  helm install repo-guardian \
    oci://ghcr.io/donaldgifford/charts/repo-guardian \
    --version <chart-version> \
    -n repo-guardian --create-namespace \
    -f values.yaml
  ```

- **Workflow file** — `.github/workflows/chart-release.yml` is
  rewritten. Anyone with a fork that referenced the old job name
  (`chart-release`) needs to update their references.

## Data Model

N/A — no persistent data, no schema changes. The OCI artifact is the
only output, and its layout follows the
[Helm OCI artifact spec](https://helm.sh/docs/topics/registries/).

## Testing Strategy

- **CI gate (existing, unchanged)** — `ci.yml` continues to run
  `helm lint`, `helm-ct lint`, `helm-unittest`, and `helm template` on
  every PR and push.
- **Workflow dry-run** — the new chart-release workflow gets a
  `if: github.event_name == 'workflow_dispatch' && inputs.dry_run`
  branch that runs `helm package` and skips `helm push`. Lets us
  smoke-test the workflow without polluting the registry.
- **Post-publish smoke test** — after the first real push, run
  `helm pull oci://ghcr.io/donaldgifford/charts/repo-guardian
  --version <v>` from a clean workstation and verify the manifest.
- **Consumer integration** — once published, update the homelab repo's
  `apps/cicd/repo-guardian/kustomization.yaml` to use the OCI URL and
  watch ArgoCD reconcile.

No new Go tests; the change is workflow-only.

## Migration / Rollout Plan

Sequenced to avoid leaving the cluster in a half-migrated state:

1. **Land this design** (DESIGN-0011) and the IMPL doc that derives
   from it (IMPL-0010, separate PR).
2. **Bump chart version** to `0.3.0` and `appVersion` to `1.4.0` to
   match the latest binary. Commit on the same branch as the workflow
   change so the first publish has a clean version delta.
3. **Rewrite `chart-release.yml`** per the IMPL doc. Verify on the PR
   via the dry-run path.
4. **Merge to main** — the workflow runs and publishes
   `oci://ghcr.io/donaldgifford/charts/repo-guardian:0.3.0`.
5. **Flip the OCI package to public** via GHCR settings (one-time).
6. **Update README** with the new install command.
7. **Land the homelab Kustomize → Helm migration** (separate PR in the
   homelab repo) referencing the published OCI URL. Validate the
   `repo-guardian` ArgoCD Application reconciles cleanly with the new
   chart and the existing 1Password-managed secrets.
8. **Archive the old `github-private-key` 1Password item** once the
   merged `github-app` secret is confirmed working.

Rollback for steps 1–4 is `git revert` on this repo. Rollback for
steps 5–7 is reverting the homelab repo to its prior kustomize-only
manifest set; the in-cluster Deployment is recreated from the
unmodified base.

## Resolved Questions

1. **Signing — cosign keyless via Sigstore.** Chart artifacts are
   signed in the publish workflow using the workflow's OIDC identity
   (`id-token: write` permission). No long-lived signing key to
   manage; provenance is verifiable against the
   `https://github.com/donaldgifford/repo-guardian/...` certificate
   identity. GPG signing of the binary releases is unaffected.
2. **Visibility — public.** Matches the binary container image at
   `ghcr.io/donaldgifford/repo-guardian` and the project's
   open-source posture. The chart contains no secrets; values are
   operator-supplied at install time.
3. **Retention policy — defer.** GHCR's default is "keep all
   versions forever," which is fine until storage cost or noise
   becomes a concern. A future ADR can revisit.

## References

- [Atlantis chart consumption pattern](https://github.com/donaldgifford/homelab/blob/main/k8s/homelab-cluster/apps/cicd/atlantis/kustomization.yaml)
- [Helm OCI registries documentation](https://helm.sh/docs/topics/registries/)
- [GHCR + Helm integration guide](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
- [DESIGN-0005](0005-helm-chart.md) — original chart design (parent)
- [IMPL-0004](../impl/0004-helm-chart-for-repo-guardian.md) — chart implementation
- `.github/workflows/chart-release.yml` — existing workflow being replaced
- `charts/repo-guardian/Chart.yaml` — `version` and `appVersion`
  source of truth
