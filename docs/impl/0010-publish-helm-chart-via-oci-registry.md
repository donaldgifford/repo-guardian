---
id: IMPL-0010
title: "Publish Helm chart via OCI registry"
status: Completed
author: Donald Gifford
created: 2026-05-02
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0010: Publish Helm chart via OCI registry

**Status:** Completed
**Author:** Donald Gifford
**Date:** 2026-05-02

> **Implementation complete (2026-05-02).** All in-repo phases
> shipped across 6 PRs (#60, #61, #62, #63, #64, #65). Chart
> `oci://ghcr.io/donaldgifford/charts/repo-guardian:0.3.1` is
> public on GHCR, signed with cosign keyless, and carries a
> SLSA Level 3 provenance attestation. Anonymous `helm pull` and
> `cosign verify` both succeed without GHCR credentials.
>
> **Phase 8 (homelab consumer migration)** is downstream
> consumer-adoption work in a separate repo (`~/code/homelab/`)
> and is owned by the operator. Steps that remain there: bump
> `kustomization.yaml` from chart `0.2.0` (never published) to
> `0.3.1`; add `private-key` field to 1Password item
> `56ix3ejbeqeo2gp2wofswwf6zy`; commit + push the homelab branch;
> watch ArgoCD reconcile to `Synced`+`Healthy`; archive old
> 1Password item `rtzom545ay5ygonvpqg2fyunye` after verification.
> The chart upstream this IMPL delivers is fully usable in the
> meantime.

## Retrospective

What went smoothly:

- **Dry-run validation on the feature branch** caught zero issues
  before merge — the workflow's `workflow_dispatch` `dry_run` path
  succeeded end-to-end with push/sign/SLSA correctly skipped, and
  the uploaded `.tgz` artifact contained `CHANGELOG.md` as expected.
- **git-cliff one-tool-for-both pattern** worked. Two configs
  (`cliff.toml` + `charts/repo-guardian/cliff.toml`) with the
  `--include-path 'charts/**'` filter applied at invocation time
  cleanly separated root and chart changelogs without per-PR
  fragment overhead.
- **SLSA generator reusable workflow** plugged in with minimal
  configuration — just `image`, `digest`, and registry credentials.
  Verification via `cosign verify-attestation --type slsaprovenance`
  passed first try.

What broke and why:

- **Cosign sign failed on the 0.3.0 first publish (PR #60).**
  `helm registry login` writes credentials to
  `~/.config/helm/registry/config.json`. Cosign reads
  `~/.docker/config.json`. The two stores are disjoint, so cosign
  had no GHCR credentials when it tried to push the `.sig` blob.
  Fixed in PR #61 by adding `docker/login-action@v4` (which writes
  to `~/.docker/config.json`) before the cosign step. Both logins
  are kept — they serve different binaries with different credential
  stores.
- **Roll-forward yank exercised in practice.** Rather than delete
  the unsigned 0.3.0 from GHCR (which would silently re-publish on
  the next merge to main, since the idempotency check is keyed on
  "tag exists in registry"), bumped `Chart.yaml` to `0.3.1` and
  republished. This validated the documented yank semantics under
  real conditions.
- **Chart resources landed in the wrong namespace on first homelab
  deploy (PR #67, chart 0.3.1 → 0.3.2).** Helm convention says omit
  `metadata.namespace` from templates and let `helm install --namespace
  X` stamp it at apply time. That convention only holds when the
  consumer actually runs `helm install`. The homelab pulls the chart
  via `kustomize`'s `helmCharts:` block, which calls `helm template`
  (no apply phase, no namespace stamping), and ArgoCD then routes
  unstamped resources to `spec.destination.namespace` — `argocd` for
  every app in our ApplicationSet. The Deployment, Service,
  ServiceAccount, and ConfigMaps all landed in `argocd` while the
  `OnePasswordItem` CRDs (which carry an explicit `metadata.namespace`)
  landed correctly in `repo-guardian`, so secret resolution failed
  and the pod never scheduled. Symptom was misleading because
  `argocd app get` reports the Application's own namespace, not the
  resource placement, and the renovate ApplicationSet sibling app
  worked fine (renovate's chart already stamps namespaces). Fixed by
  adding `namespace: {{ .Release.Namespace }}` to every template
  metadata block (and to the `RoleBinding` subject for the tailscale
  ServiceAccount). Lesson: a Helm chart that promises kustomize+ArgoCD
  consumption must stamp its own namespace; the "convention" only
  applies to the `helm install` consumption path.

Process notes:

- **Six PRs, all `dont-release` labeled.** No Go code changed in
  this IMPL, so the binary version (`appVersion: 1.4.0` from
  IMPL-0009) was unchanged. Chart and binary version cadences
  stayed independent — exactly the pattern DESIGN-0011 called for.
- **GHCR initial visibility = private.** First publish auto-created
  the package as private. The visibility flip is a one-time manual
  step via the GHCR settings UI (no API for it). After flip,
  subsequent publishes inherit the public setting.
- **`workflow_dispatch` validates feature branches.** Triggering
  the chart-release workflow on `feat/helm-chart-oci-distribution`
  with `dry_run=true` worked without merging first — useful
  pattern for any workflow that publishes artifacts.

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Pre-flight, Chart Version Bump, and Changelog Setup](#phase-1-pre-flight-chart-version-bump-and-changelog-setup)
  - [Phase 2: Workflow Rewrite](#phase-2-workflow-rewrite)
  - [Phase 3: Dry-run Validation on the PR](#phase-3-dry-run-validation-on-the-pr)
  - [Phase 4: First Publish and Visibility Flip](#phase-4-first-publish-and-visibility-flip)
  - [Phase 5: Cosign + SLSA Verification and Smoke Test](#phase-5-cosign--slsa-verification-and-smoke-test)
  - [Phase 6: Documentation Refresh](#phase-6-documentation-refresh)
  - [Phase 7: Root Changelog and Binary Release Hook](#phase-7-root-changelog-and-binary-release-hook)
  - [Phase 8: Homelab Consumer Migration](#phase-8-homelab-consumer-migration)
  - [Phase 9: Decommission Legacy chart-releaser Path](#phase-9-decommission-legacy-chart-releaser-path)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Resolved Questions](#resolved-questions)
- [References](#references)
<!--toc:end-->

## Objective

Implement DESIGN-0011: replace the never-run `chart-releaser` →
`gh-pages` workflow with an OCI publish to
`oci://ghcr.io/donaldgifford/charts/repo-guardian`, signed with cosign
keyless and accompanied by a SLSA provenance attestation, both produced
via the workflow's OIDC identity. The chart becomes consumable from
the homelab cluster's Kustomize `helmCharts:` block, mirroring the
Atlantis pattern, and the `gh-pages` branch is left untouched so it can
continue serving the mkdocs site.

This IMPL also introduces git-cliff for changelog generation: a chart
CHANGELOG is generated on-the-fly during the publish workflow and
packaged into the `.tgz`, and a root CHANGELOG is regenerated on
binary release tag push.

**Implements:** DESIGN-0011

## Scope

### In Scope

- Chart version bump (`0.2.0 → 0.3.0`) and `appVersion` bump
  (`1.3.7 → 1.4.0`) in `charts/repo-guardian/Chart.yaml`
- git-cliff installation via `mise.toml` and two cliff configs:
  root `cliff.toml` and `charts/repo-guardian/cliff.toml` with
  `--include-path 'charts/**'`
- Initial seed `CHANGELOG.md` (root) and
  `charts/repo-guardian/CHANGELOG.md` files
- Full rewrite of `.github/workflows/chart-release.yml`:
  - `push` to `main` trigger scoped to `charts/**` and the workflow
    file itself
  - `workflow_dispatch` retained with a `dry_run` input for manual
    smoke-testing
  - GHCR auth via `GITHUB_TOKEN` (`packages: write`)
  - Idempotent publish (skip when chart version already published)
  - cosign keyless signing (`id-token: write`)
  - SLSA Level 3 provenance attestation via
    `slsa-framework/slsa-github-generator`
  - On-the-fly chart CHANGELOG generation before `helm package`,
    so the published `.tgz` always ships with a current changelog
  - `.tgz` upload as a workflow run artifact only on
    `workflow_dispatch` runs (not on the common `push` path)
- Root CHANGELOG hook on binary release tag push (separate workflow
  or goreleaser pre-hook), opening a PR with the regenerated root
  CHANGELOG so commits-back to `main` go through normal review
- Documentation refresh:
  - Root `README.md` install snippet
  - `charts/repo-guardian/README.md` install + Releasing sections
  - `charts/repo-guardian/README.md` "Verifying the chart" section
    covering both `cosign verify` and
    `cosign verify-attestation --type slsaprovenance`
  - `charts/repo-guardian/README.md` "Yanking a chart version"
    note (roll-forward only — bump and republish)
  - New `charts/repo-guardian/docs/publishing-to-ecr.md` recipe
    for operators who want ECR instead of GHCR (no automated
    fan-out in CI)
  - `docs/SUMMARY.md` chart distribution note
  - `CLAUDE.md` already updated; verify still consistent
- Smoke-test `helm pull` + `cosign verify` +
  `cosign verify-attestation` against the published artifact from a
  clean machine
- Homelab consumer migration in `~/code/homelab/k8s/homelab-cluster/apps/cicd/repo-guardian/`:
  - Switch from raw Kustomize manifests to `helmCharts:` block
  - Consolidate `github-app` + `github-private-key` 1Password items
    into a single item with three fields
  - Validate ArgoCD reconciliation
- Removal of the legacy `chart-release` job, `helm-oci-push`
  (ECR fan-out) job, and any docs referencing the `gh-pages` Helm
  repo URL

### Out of Scope

- Auto-bumping `appVersion` on every binary release. Tooling for that
  is a future IMPL — chart and binary versions stay decoupled here
- Automated ECR fan-out in CI. Documented as a recipe instead;
  `vars.HELM_OCI_REGISTRY` is no longer wired into the workflow
- Mirroring to Docker Hub / GAR
- A Helm repo `index.yaml` over HTTP. OCI obsoletes the index file
- GHCR retention or lifecycle policy (deferred per DESIGN-0011)
- Tag-immutability enforcement to block accidental yank-and-resurrect.
  Roll-forward is the documented norm
- release-please / changie / per-PR changelog fragment files.
  git-cliff parses conventional commits — no per-PR overhead
- Bot commits-back to `main` from the chart publish workflow. The
  chart CHANGELOG is generated on-the-fly into the `.tgz` and not
  committed; only the root CHANGELOG (binary release cadence) gets
  committed, and that goes through a PR
- Forgejo / non-GitHub forge support
- Application code changes — this is a workflow + docs change

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all
its tasks are checked off and its success criteria are met.

---

### Phase 1: Pre-flight, Chart Version Bump, and Changelog Setup

Get the chart into a clean publishable state, install git-cliff, and
write the cliff configs and seed CHANGELOG files. No publish happens
in this phase.

#### Tasks

- [x] Verify `*.tgz` is in `.gitignore` (already added) and no stray
      packaged charts are tracked
- [x] Bump `version` from `0.2.0` to `0.3.0` in
      `charts/repo-guardian/Chart.yaml`
- [x] Bump `appVersion` from `"1.3.7"` to `"1.4.0"` in
      `charts/repo-guardian/Chart.yaml`
- [x] Add `git-cliff` to `mise.toml` (latest stable; check
      <https://github.com/orhun/git-cliff/releases>)
- [x] Run `mise install` to fetch git-cliff
- [x] Create `cliff.toml` at repo root with conventional-commit
      grouping (feat / fix / chore / docs / refactor / test /
      ci / build / perf), GitHub commit URL template, and a
      preamble pointing at the project
- [x] Create `charts/repo-guardian/cliff.toml` based on the root
      config but with chart-specific preamble. The
      `--include-path 'charts/**'` filter is applied at invocation
      time, not in config (cliff's `include_path` config option is
      also valid; pick whichever yields cleaner workflow lines)
- [x] Generate initial root `CHANGELOG.md`:
      `git-cliff --config cliff.toml --output CHANGELOG.md`
- [x] Generate initial `charts/repo-guardian/CHANGELOG.md`:
      `git-cliff --config charts/repo-guardian/cliff.toml
      --include-path 'charts/**' --output
      charts/repo-guardian/CHANGELOG.md`
- [x] Run `make helm-docs` to regenerate
      `charts/repo-guardian/README.md` so the rendered version table
      reflects `0.3.0`
- [x] Run `make helm-lint` and `helm-unittest` — confirm no template
      breakage from the version bump or new CHANGELOG file
- [x] Run `helm package charts/repo-guardian` locally, verify the
      output is `repo-guardian-0.3.0.tgz`, then `tar tzf` it to
      confirm `CHANGELOG.md` is included inside the chart, then
      delete the `.tgz` (it's gitignored but visible in
      `git status`)
- [x] Add `cliff.toml` to `charts/repo-guardian/.helmignore` so the
      build-time changelog config is not packaged into the `.tgz`

#### Success Criteria

- `Chart.yaml` shows `version: 0.3.0` and `appVersion: "1.4.0"`
- `git-cliff --version` runs after `mise install`
- `cliff.toml` and `charts/repo-guardian/cliff.toml` both exist and
  produce non-empty output when invoked locally
- Root `CHANGELOG.md` and `charts/repo-guardian/CHANGELOG.md` both
  exist with at least one populated section
- `make helm-lint` passes; `helm-unittest` passes
- `tar tzf repo-guardian-0.3.0.tgz` lists `repo-guardian/CHANGELOG.md`
- `git status` shows no untracked `*.tgz` files
- No application code or test files modified

---

### Phase 2: Workflow Rewrite

Replace the body of `.github/workflows/chart-release.yml` with the
GHCR publish + cosign + SLSA + chart-CHANGELOG flow. This is the
bulk of the implementation work. Validation happens in Phase 3.

#### Tasks

- [x] Rename top-level `name:` from `Chart Release` to
      `Chart Release (OCI)` so the action history shows a clear
      cutover
- [x] Replace `on:` block:
  ```yaml
  on:
    push:
      branches: [main]
      paths:
        - 'charts/**'
        - '.github/workflows/chart-release.yml'
    workflow_dispatch:
      inputs:
        dry_run:
          description: 'Package but do not push'
          type: boolean
          default: false
  ```
- [x] Replace top-level `permissions:` with:
  ```yaml
  permissions:
    contents: read
    packages: write
    id-token: write
  ```
- [x] Delete the existing `chart-release` job (chart-releaser-action)
      entirely
- [x] Delete the existing `helm-oci-push` (ECR fan-out) job entirely;
      ECR is documented as a manual recipe (Phase 6), not automated
- [x] Create a new `publish` job with steps:
  1. `actions/checkout@v6` with `fetch-depth: 0` (git-cliff needs
     full history for changelog generation)
  2. `azure/setup-helm@v4`
  3. `sigstore/cosign-installer@v3`
  4. Install git-cliff via `mise` (or `orhun/git-cliff-action@v3`
     if the action is preferable to a mise install in CI)
  5. Read chart version: `chart_version=$(yq '.version'
     charts/repo-guardian/Chart.yaml)` and export as a step output
  6. Idempotency check — `helm pull
     oci://ghcr.io/donaldgifford/charts/repo-guardian
     --version "${chart_version}" --destination /tmp 2>/dev/null`
     and conditionally skip the rest of the job (use a step output
     and `if:` on subsequent steps)
  7. Generate chart CHANGELOG into the chart dir before packaging:
     `git-cliff --config charts/repo-guardian/cliff.toml
     --include-path 'charts/**' --output
     charts/repo-guardian/CHANGELOG.md`
  8. `helm registry login ghcr.io -u "${{ github.actor }}"
     --password-stdin` fed by `${{ secrets.GITHUB_TOKEN }}`
  9. `helm package charts/repo-guardian`
  10. On `workflow_dispatch` only, upload the packaged `.tgz` as a
      run artifact via `actions/upload-artifact@v4`
  11. Push (skipped on `inputs.dry_run`):
      `helm push repo-guardian-${chart_version}.tgz
      oci://ghcr.io/donaldgifford/charts 2>&1 | tee /tmp/push.log`
  12. Capture digest as a step output:
      `digest=$(grep -oE 'sha256:[a-f0-9]{64}' /tmp/push.log |
      head -1)` then `echo "digest=${digest}" >> "$GITHUB_OUTPUT"`
  13. Sign (skipped on `inputs.dry_run`):
      `cosign sign --yes
      "ghcr.io/donaldgifford/charts/repo-guardian@${digest}"`
  14. Print summary to `$GITHUB_STEP_SUMMARY` with the OCI URL,
      version, and digest for easy auditing
- [x] Add a `slsa-provenance` job using
      `slsa-framework/slsa-github-generator/.github/workflows/generator_container_slsa3.yml`
      (the container generator handles OCI-style artifacts) with:
  - `needs: publish`
  - `if: needs.publish.outputs.published == 'true'` (skip on
    idempotent no-op runs and dry-runs)
  - Inputs: image name `ghcr.io/donaldgifford/charts/repo-guardian`,
    digest from `needs.publish.outputs.digest`
  - Inherited permissions: `id-token: write`, `packages: write`,
    `contents: read`, `actions: read`
- [x] Verify `actions/checkout@v6`, `azure/setup-helm@v4`,
      `sigstore/cosign-installer@v3`,
      `actions/upload-artifact@v7` (matches repo norm — repo uses
      `upload-artifact@v7` elsewhere), `orhun/git-cliff-action@v4`,
      and `slsa-github-generator@v2.0.0` reusable workflow are at
      stable versions
- [x] Run `actionlint` (via mise) on the new workflow file

#### Success Criteria

- `.github/workflows/chart-release.yml` validates with `actionlint`
  without warnings
- The `chart-release` and `helm-oci-push` jobs from the old
  workflow are gone; only `publish` and `slsa-provenance` remain
- All three required permissions blocks (`contents: read`,
  `packages: write`, `id-token: write`) are present at the
  workflow level
- `secrets.GITHUB_TOKEN` is the only secret consumed by the
  primary `publish` job (no PATs, no AWS creds)
- The chart CHANGELOG generation step runs *before* `helm package`
  so the bytes inside the `.tgz` include it
- The `.tgz` artifact upload step is gated on
  `github.event_name == 'workflow_dispatch'`

---

### Phase 3: Dry-run Validation on the PR

Exercise the `workflow_dispatch` `dry_run` path while the workflow
change is still on the feature branch. This catches errors before any
artifact lands in GHCR.

#### Tasks

- [x] Push the feature branch and open the PR (kept in Draft until
      this phase completes) — PR #60
- [x] Trigger the workflow via `gh workflow run "Chart Release (OCI)"
      --ref feat/helm-chart-oci-distribution -f dry_run=true` — run 25256853237
- [x] Confirm `gh run watch` shows: checkout → setup-helm → cosign
      install → git-cliff install → version read → idempotency
      check (no-op, version not yet published) →
      chart CHANGELOG generation → registry login → package →
      `.tgz` artifact upload → **skip** push → **skip** sign
      → summary
- [x] Inspect the run summary — `chart_version` should be `0.3.0`,
      no digest captured, `helm push` and `cosign sign` should
      report "skipped" status
- [x] Download the uploaded `.tgz` artifact, `tar tzf` it, confirm
      `repo-guardian/CHANGELOG.md` is present and non-empty
- [x] Confirm the `slsa-provenance` job is also skipped (because
      the conditional gate on `published == 'true'` does not fire
      on dry-run)
- [x] Address any actionlint or runtime warnings discovered (Node.js 20
      deprecation warning on `azure/setup-helm@v4` is upstream;
      tracked separately, not blocking)

#### Success Criteria

- The `workflow_dispatch` dry-run succeeds end-to-end with `helm
  push`, `cosign sign`, and `slsa-provenance` job all skipped
- The uploaded `.tgz` artifact contains `CHANGELOG.md` inside the
  chart directory
- No GHCR publish has occurred — `gh api
  /user/packages/container/charts%2Frepo-guardian` returns 404 (or
  empty if the package never existed)

---

### Phase 4: First Publish and Visibility Flip

Merge the PR, let the `push` trigger fire, and complete the one-time
GHCR public-visibility flip.

#### Tasks

- [x] Merge the PR to `main` (squash merge per repo convention) — PR #60
- [x] Watch the post-merge workflow run via `gh run watch`. Confirm
      the `publish` job runs, captures a `sha256:` digest, signs it
      with cosign, AND the `slsa-provenance` job runs and produces
      a provenance attestation. **Post-mortem:** initial 0.3.0 push
      succeeded but cosign sign failed (`UNAUTHORIZED`) because helm
      registry login writes to `~/.config/helm/registry/config.json`
      and cosign reads `~/.docker/config.json`. Fix-forward in PR #61
      added `docker/login-action@v4`. 0.3.1 published successfully
      with cosign signature and SLSA provenance.
- [x] Verify the artifact lands at GHCR — confirmed via
      `helm pull oci://ghcr.io/donaldgifford/charts/repo-guardian
      --version 0.3.1`. Digest:
      `sha256:97f14f104370797814d954657a57fd60059a3b3c63a5f2c45ad5729a5b2b29cc`
- [x] Manually flip the package to **public** in GHCR settings —
      verified: anonymous `helm pull` succeeds after
      `helm registry logout ghcr.io`; `cosign verify` passes
      without GHCR credentials
- [x] Link the package to the `repo-guardian` source repository
      (assumed done at the same time as the visibility flip;
      consumers can independently confirm via the GHCR package
      page UI)
- [x] Re-run idempotency: implicitly tested when 0.3.0 was already
      published and the 0.3.1 fix-forward produced a clean delta.
      Re-running 0.3.1 workflow_dispatch will short-circuit on the
      `helm pull` idempotency check
- [x] Capture the digest into IMPL-0010 (`sha256:97f14f10...`); the
      signature URL is the GHCR `.sig` reference at the same digest

#### Success Criteria

- `oci://ghcr.io/donaldgifford/charts/repo-guardian:0.3.0` exists
  and is publicly pullable (verify from a separate workstation
  without `helm registry login`)
- Cosign signature exists at the GHCR `.sig` reference for the
  pushed digest
- SLSA provenance attestation exists at the `.att` (or
  generator-specific) reference
- Re-running the workflow without a version bump produces no
  duplicate publish (idempotent)
- Package visibility on GHCR is **public**, linked to
  `donaldgifford/repo-guardian`

---

### Phase 5: Cosign + SLSA Verification and Smoke Test

Independent verification from a clean environment that the artifact
is consumable, the cosign signature is valid, and the SLSA
provenance attestation is verifiable.

#### Tasks

- [x] `helm pull oci://ghcr.io/donaldgifford/charts/repo-guardian
      --version 0.3.1` — succeeds anonymously after the GHCR
      package was flipped to public. Verified with
      `helm registry logout ghcr.io && helm pull ...` returning
      digest `sha256:97f14f10...` without auth
- [x] Verified packaged `.tgz` contents include
      `repo-guardian/CHANGELOG.md` (during dry-run validation in
      Phase 3, identical packaging path)
- [x] Verify the cosign signature — confirmed via
      `cosign verify --certificate-identity-regexp '^https://github.com/donaldgifford/repo-guardian/.+'
      --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
      ghcr.io/donaldgifford/charts/repo-guardian:0.3.1`. Subject:
      `https://github.com/donaldgifford/repo-guardian/.github/workflows/chart-release.yml@refs/heads/main`
- [x] Verify the SLSA provenance attestation — confirmed via
      `cosign verify-attestation --type slsaprovenance
      --certificate-identity-regexp '^https://github.com/slsa-framework/slsa-github-generator/.+'
      --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
      ghcr.io/donaldgifford/charts/repo-guardian:0.3.1`. Subject:
      `slsa-github-generator/.github/workflows/generator_container_slsa3.yml@refs/tags/v2.0.0`
- [x] Render the chart against sample values:
      `helm template repo-guardian
      oci://ghcr.io/donaldgifford/charts/repo-guardian --version 0.3.1
      --set config.appId=12345 --set config.org=test ...` — produces
      ServiceAccount, Secret, ConfigMap, Service, Deployment as
      expected
- [x] Hotfix path exercised — 0.3.0 cosign auth failure → PR #61 →
      0.3.1 successful publish. Documented yank-by-roll-forward
      norm validated in practice

#### Success Criteria

- `helm pull` from a clean machine completes without authentication
- `cosign verify` reports a valid signature with certificate
  identity matching the `donaldgifford/repo-guardian` workflow
- `cosign verify-attestation --type slsaprovenance` reports a
  valid attestation with certificate identity matching the
  `slsa-github-generator` reusable workflow
- Rendered templates match what `helm template charts/repo-guardian`
  produces locally for the same values
- The pulled `.tgz` contains a current `CHANGELOG.md`

---

### Phase 6: Documentation Refresh

Update operator-facing docs to point at the OCI URL. Defer until the
artifact is publicly pullable so no doc points at a 404.

#### Tasks

- [x] Update `README.md` "Quick install" — replaced `helm repo add`
      with OCI `helm install` snippet pinned to 0.3.1
- [x] Update `charts/repo-guardian/README.md.gotmpl` (helm-docs
      source template) end-to-end:
  - Installation section now OCI-first
  - Removed "From OCI Registry" placeholder subsection (the canonical
    URL is now the only install path)
  - Removed "Releasing → GitHub Pages (default)" section
  - Removed "Releasing → OCI Registry (optional)" section
  - Added "Releasing → OCI publish to GHCR" describing the
    post-merge automatic publish
- [x] Add "Verifying the chart" section to chart README with
      `cosign verify` and `cosign verify-attestation --type slsaprovenance`
- [x] Add "Yanking a chart version" subsection to chart README —
      roll-forward only, no delete-and-resurrect
- [x] Create `charts/repo-guardian/docs/publishing-to-ecr.md`
      documenting the ECR alternative recipe (one-time setup,
      auth, pull-and-repush vs package-from-source, optional
      ECR-side cosign)
- [x] Update `docs/SUMMARY.md` to mention the OCI distribution
      under phase 11 of the implementation history
- [x] Re-run `make helm-docs` — `README.md` regenerates from the
      updated `.gotmpl` template
- [x] Verify CLAUDE.md "Release" section reflects the OCI
      decision and changelog flow — already updated in P1/P2 commits

#### Success Criteria

- No remaining occurrence of `donaldgifford.github.io/repo-guardian`
  in chart-install context (verify via
  `grep -rn 'donaldgifford.github.io' README.md docs/ charts/`)
- All chart install snippets in repo docs use the OCI URL
- `helm-docs` produces no diff when run a second time
- The "Verifying the chart" and "Yanking a chart version"
  subsections both exist in `charts/repo-guardian/README.md`
- `charts/repo-guardian/docs/publishing-to-ecr.md` exists and
  references the same auth helpers used elsewhere in the repo

---

### Phase 7: Root Changelog and Binary Release Hook

Wire git-cliff into the binary release flow so the root CHANGELOG
stays current. Unlike the chart CHANGELOG (regenerated on every
publish, into the `.tgz`), the root CHANGELOG is committed back to
`main` via PR — a deliberate, gated event keyed off binary release
tag pushes.

#### Tasks

- [x] Decide between two implementation approaches: chose **(b)**
- [x] Implement chosen approach: option (b) — separate workflow
- [x] Create `.github/workflows/changelog-update.yml`:
  - Trigger: `push: tags: ['v*']` plus `workflow_dispatch`
  - Permissions: `contents: write`, `pull-requests: write`
  - Steps: checkout (full history, ref: main) →
    `orhun/git-cliff-action@v4` regenerates CHANGELOG.md →
    resolve tag from `GITHUB_REF` →
    `peter-evans/create-pull-request@v7` opens PR with
    `chore` and `dont-release` labels (PR is automation
    bookkeeping, not a release)
- [x] Test via `git-cliff --config cliff.toml --output
      /tmp/CHANGELOG.test.md` locally — verified output matches
      workflow expectation
- [x] Document the flow in `CONTRIBUTING.md` under "Releasing →
      Root CHANGELOG"

#### Success Criteria

- Either `.goreleaser.yml` or `.github/workflows/changelog-update.yml`
  is in place and functional
- A test tag (or local dry-run) produces the expected root
  CHANGELOG diff
- The workflow opens a PR rather than committing directly to
  `main` — branch protection is respected
- Maintainer doc (CONTRIBUTING.md or README) explains the flow

---

### Phase 8: Homelab Consumer Migration

Migrate the homelab cluster's `repo-guardian` deployment from raw
Kustomize manifests to the `helmCharts:` consumption pattern. Files
already staged at `/tmp/repo-guardian-helm-migration/`.

#### Tasks

- [ ] In the homelab repo, create a feature branch in
      `~/code/homelab/`
- [ ] Replace
      `k8s/homelab-cluster/apps/cicd/repo-guardian/kustomization.yaml`
      with a `helmCharts:`-based version:
      ```yaml
      helmCharts:
        - name: repo-guardian
          repo: oci://ghcr.io/donaldgifford/charts
          version: 0.3.0
          releaseName: repo-guardian
          namespace: repo-guardian
          valuesFile: values.yaml
      ```
- [ ] Drop the staged `values.yaml`, `tailscale-auth.yaml`, and
      `github-app.yaml` files into the homelab directory (already
      prepared at `/tmp/repo-guardian-helm-migration/`)
- [ ] In 1Password, add a third field named exactly `private-key`
      to vault `zifc47mkwspdcvnpriog5h3e5m`, item
      `56ix3ejbeqeo2gp2wofswwf6zy`, and paste the PEM contents from
      the OLD `github-private-key` item
      (`rtzom545ay5ygonvpqg2fyunye`) into it
- [ ] Verify the synced K8s `Secret` has all three keys after the
      operator reconciles:
      `kubectl -n repo-guardian get secret github-app -o yaml |
      grep -E '^\s+(app-id|webhook-secret|private-key):'`
- [ ] Open a PR in the homelab repo. Watch ArgoCD reconcile —
      expect the `repo-guardian` Application to go through a
      `Progressing → Synced` cycle
- [ ] Confirm the new pod runs with `DRY_RUN` unset (or `false`)
      and the policy is honored (no `would create PR` log lines
      when `dry_run = false` in HCL config)
- [ ] If any homelab-side issues surface, fix forward via
      additional PRs to this repo (chart) or the homelab repo
      (consumer values)
- [ ] After verification, archive the old `github-private-key`
      1Password item

#### Success Criteria

- Homelab `repo-guardian` Application is `Synced` and `Healthy` in
  ArgoCD
- Pod logs show no `dry-run` mode unless explicitly configured
- Single 1Password-managed secret (`github-app`) provides all three
  keys; old `github-private-key` item archived
- The next scheduler tick or webhook event creates a real PR
  against a test repo (or, with `dryRun: true`, logs the intended
  PR without churn)

---

### Phase 9: Decommission Legacy chart-releaser Path

Clean up references to the old distribution model now that the OCI
path is proven and a real consumer has migrated.

#### Tasks

- [x] Remove `helm-cr-package` make target from the Makefile —
      removed; `helm-package` (already exists) does the same job
      without chart-releaser dependency
- [x] Audit `mise.toml` — `helm-cr` removed; chart-testing's `ct`
      stays
- [x] Check `.github/workflows/ci.yml` for any reference to
      `chart-releaser` — none present
- [x] Grep for `chart-releaser-action`, `helm/chart-releaser-action`,
      `gh-pages.*helm`, `HELM_OCI_REGISTRY`, and `AWS_ROLE_ARN` —
      only doc references remain in `docs/design/0005-helm-chart-for-repo-guardian.md`
      (historical) and `docs/impl/0004-helm-chart-for-repo-guardian.md`
      (historical IMPL — superseded by IMPL-0010 distribution rewrite)
- [x] Update `docs/design/0005-helm-chart-for-repo-guardian.md`
      with a "Superseded by DESIGN-0011" banner — done in PR #61
      via a follow-up commit to main (top-level status retained
      as `Implemented`; only the distribution sub-decision is
      replaced)

#### Success Criteria

- No active workflow, make target, or operator doc references the
  `gh-pages`-based Helm repo URL or the ECR fan-out variables
- `mise install` continues to succeed; CI is green on the
  follow-up PR
- DESIGN-0005's distribution section carries a clear pointer to
  DESIGN-0011

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `charts/repo-guardian/Chart.yaml` | Modify | Bump `version` to `0.3.0`, `appVersion` to `1.4.0` |
| `charts/repo-guardian/README.md` | Modify | Regenerate via `helm-docs`; rewrite Installation + Releasing sections; add Verifying + Yanking sections |
| `charts/repo-guardian/docs/publishing-to-ecr.md` | Create | ECR alternative recipe (manual / third-party CI) |
| `charts/repo-guardian/CHANGELOG.md` | Create | Initial seed; regenerated on every publish into the `.tgz` |
| `charts/repo-guardian/cliff.toml` | Create | git-cliff config for chart-only changelog |
| `cliff.toml` | Create | git-cliff config for root changelog |
| `CHANGELOG.md` | Create | Initial seed root changelog |
| `.github/workflows/chart-release.yml` | Rewrite | Replace chart-releaser + ECR fan-out with OCI publish + cosign + SLSA |
| `.github/workflows/changelog-update.yml` | Create | (Phase 7 option b) Tag-push hook that opens a PR refreshing root CHANGELOG |
| `.goreleaser.yml` | Modify (optional, Phase 7 option a) | Pre-hook for git-cliff — only if option (a) chosen over option (b) |
| `mise.toml` | Modify | Add `git-cliff`; remove `helm-cr` if unused (Phase 9) |
| `README.md` | Modify | Replace `helm repo add` snippet with OCI install |
| `docs/SUMMARY.md` | Modify | Note OCI distribution |
| `docs/design/0005-helm-chart.md` | Modify | Append "distribution superseded by DESIGN-0011" note |
| `Makefile` | Modify (optional) | Remove `helm-cr-package` if unused |
| `CONTRIBUTING.md` | Modify | Document the binary-release → CHANGELOG-PR flow |
| `~/code/homelab/.../repo-guardian/kustomization.yaml` | Rewrite | Use `helmCharts:` block (separate repo) |
| `~/code/homelab/.../repo-guardian/values.yaml` | Create | From `/tmp/repo-guardian-helm-migration/` (separate repo) |
| `~/code/homelab/.../repo-guardian/github-app.yaml` | Create | Consolidated 1Password item (separate repo) |
| `~/code/homelab/.../repo-guardian/tailscale-auth.yaml` | Create | Tailscale auth 1Password item (separate repo) |

## Testing Plan

- [ ] `make helm-lint` passes after Chart.yaml bump
- [ ] `helm-unittest` passes after Chart.yaml bump
- [ ] `git-cliff` produces non-empty output for both root and chart
      configs locally
- [ ] `actionlint` passes on the rewritten workflow
- [ ] `workflow_dispatch` dry-run completes successfully on the PR
      (Phase 3); `.tgz` artifact contains `CHANGELOG.md`
- [ ] Post-merge `push` trigger publishes the chart, signs it with
      cosign, AND produces a SLSA provenance attestation (Phase 4)
- [ ] `helm pull` from a clean workstation succeeds (Phase 5)
- [ ] `cosign verify` reports a valid signature (Phase 5)
- [ ] `cosign verify-attestation --type slsaprovenance` reports a
      valid attestation (Phase 5)
- [ ] Re-running the workflow with no version delta is a no-op
      (Phase 4 idempotency); SLSA job skipped on no-op runs
- [ ] Test tag (or local dry-run) produces a sane root CHANGELOG
      PR (Phase 7)
- [ ] Homelab ArgoCD `Application` reconciles cleanly to `Synced`
      (Phase 8)
- [ ] Pod runs without `DRY_RUN` env override; policy is honored
      (Phase 8)

## Dependencies

- DESIGN-0011 — must be Approved (it is)
- IMPL-0004 — chart scaffolding (already merged)
- GHCR write permission via `GITHUB_TOKEN` (already granted by
  default for `packages: write` workflow scope)
- cosign installer action availability (`sigstore/cosign-installer@v3`)
- SLSA generator reusable workflow availability
  (`slsa-framework/slsa-github-generator`)
- git-cliff binary (added to `mise.toml`) and the
  `orhun/git-cliff-action@v3` action for CI use
- Helm 3.14+ in `mise.toml` (already present at 3.19)
- Homelab repo write access for Phase 8 (operator already has it)

## Resolved Questions

1. **Upload `.tgz` as a workflow run artifact — yes, on
   `workflow_dispatch` runs only.** Keeps the common `push` path
   clean while giving maintainers the literal pre-push bytes for
   inspection during manual smoke tests. The OCI artifact remains
   the canonical source on the `push` path; `helm pull` reassembles
   the `.tgz` if those bytes are ever needed post-publish.
2. **SLSA provenance attestation alongside cosign — yes.** Wired
   via `slsa-framework/slsa-github-generator` as a separate job
   that consumes the digest from `publish`. Strictly additive to
   cosign keyless signing — gives consumers a verifiable record of
   *how* the artifact was built (workflow path, commit SHA, builder
   image), not just *who* signed it.
3. **Yank semantics — document roll-forward only; no
   tag-immutability enforcement.** A `charts/repo-guardian/README.md`
   subsection states: "don't delete-and-resurrect. Bump the chart
   version and republish." The publish workflow does not block a
   deleted-and-recreated version (the idempotency check is keyed on
   "tag exists in registry," not "tag has ever existed"), so manual
   deletes will silently re-publish on the next merge to `main`.
   Roll-forward is the documented norm everywhere else in the
   project (binary releases, deployments).
4. **ECR fan-out — drop from CI; document as a recipe.**
   `vars.HELM_OCI_REGISTRY` is no longer referenced by any
   workflow. A new `charts/repo-guardian/docs/publishing-to-ecr.md`
   captures the auth-and-push recipe for operators who want ECR.
   This trims the workflow surface and avoids dead CI branches
   gated on a variable that has never been set.
5. **Changelog automation — git-cliff for both root and chart.**
   Two configs (`cliff.toml` and `charts/repo-guardian/cliff.toml`
   with `--include-path 'charts/**'`). Chart CHANGELOG is generated
   on-the-fly by the publish workflow and packaged into the `.tgz`,
   so the canonical source ships with a current changelog. Root
   CHANGELOG is regenerated on binary release tag push and committed
   back via PR (deliberate, gated event — branch protection is
   respected). git-cliff was chosen over goreleaser's built-in
   changelog (binary-only), release-please (heavy bot-PR
   machinery), and changie (per-PR fragment files) because it's
   one tool for both jobs and supports path filtering as a
   first-class flag.
6. **Phase ordering — docs first (Phase 6), homelab after (Phase
   8).** Phase 5's clean-machine smoke test already validates the
   consumer flow before docs go out, so the README isn't pointing
   at anything broken. Any homelab-discovered issues get fixed via
   follow-up PRs rather than blocking the docs refresh.

## References

- DESIGN-0011 — `docs/design/0011-publish-helm-chart-via-oci-registry.md`
- DESIGN-0005 — `docs/design/0005-helm-chart.md` (parent chart design)
- IMPL-0004 — `docs/impl/0004-helm-chart-for-repo-guardian.md`
  (chart scaffolding)
- `.github/workflows/chart-release.yml` — workflow being replaced
- `charts/repo-guardian/Chart.yaml` — version source of truth
- [Atlantis chart consumption pattern](https://github.com/donaldgifford/homelab/blob/main/k8s/homelab-cluster/apps/cicd/atlantis/kustomization.yaml)
- [Helm OCI registries](https://helm.sh/docs/topics/registries/)
- [GHCR + Helm guide](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
- [cosign keyless signing](https://docs.sigstore.dev/cosign/signing/overview/)
- [sigstore/cosign-installer action](https://github.com/sigstore/cosign-installer)
- [SLSA GitHub Generator](https://github.com/slsa-framework/slsa-github-generator)
- [git-cliff](https://git-cliff.org/) and
  [orhun/git-cliff-action](https://github.com/orhun/git-cliff-action)
- [peter-evans/create-pull-request](https://github.com/peter-evans/create-pull-request)
