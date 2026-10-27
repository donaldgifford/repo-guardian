---
id: IMPL-0034
title: "v2.0.0 GA cutover with security tier 1 (DESIGN-0038 Part A)"
status: In Progress
author: Donald Gifford
created: 2026-10-25
---

<!-- markdownlint-disable-file MD024 MD025 MD041 -->

# IMPL-0034: v2.0.0 GA cutover with security tier 1 (DESIGN-0038 Part A)

<!--toc:start-->
<!-- markdownlint-capture -->
<!-- markdownlint-disable MD013 -->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Pre-implementation audit](#pre-implementation-audit)
- [How the phases are ordered](#how-the-phases-are-ordered)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Sync main into v2](#phase-1-sync-main-into-v2)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: The public docs site, interim](#phase-2-the-public-docs-site-interim)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: The documentation rewrite](#phase-3-the-documentation-rewrite)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: Changelogs, branch entries and CLAUDE.md](#phase-4-changelogs-branch-entries-and-claudemd)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: Security tier 1](#phase-5-security-tier-1)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: The GA-prep pull request](#phase-6-the-ga-prep-pull-request)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
  - [Phase 7: Soak and homelab](#phase-7-soak-and-homelab)
    - [Tasks](#tasks-6)
    - [Success Criteria](#success-criteria-6)
  - [Phase 8: The merge and the release](#phase-8-the-merge-and-the-release)
    - [Tasks](#tasks-7)
    - [Success Criteria](#success-criteria-7)
  - [Phase 9: After GA](#phase-9-after-ga)
    - [Tasks](#tasks-8)
    - [Success Criteria](#success-criteria-8)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [References](#references)
<!-- markdownlint-restore -->
<!--toc:end-->

<!--docz:objective:start-->
## Objective

Take `v2` to `main` and ship `v2.0.0` through the existing release
pipeline: converge the branches with merge commits, make the tree
GA-shaped before the final merge (docs, changelogs, branch-entry
reverts, chart `2.0.0`), land the first tier of supply-chain security
(SBOMs, CodeQL, rulesets, pins, pod hardening, a verification page), run
the soak IMPL-0030 set, then merge with the `major` label so one push
produces the binaries, both registries, the UI image, the chart and
provenance.

**Implements:** [DESIGN-0038](../design/0038-v200-ga-cutover-and-identity-provider-portability.md)
Part A (A1–A9), Part D tier 1 (D4), decisions D1–D3, D10–D12, OQ1–OQ3,
OQ10–OQ12, OQ16–OQ19. Supersedes IMPL-0030 OQ12 and carries IMPL-0030
Phase 10.

<!--docz:objective:end-->

<!--docz:scope:start-->
## Scope

<!--docz:in-scope:start-->
### In Scope

- M1 (`main` → `v2`) and M2 (`v2` → `main`), both merge commits through
  pull requests; M2 labelled `major`.
- `mkdocs-ghpages.yml` inheriting `mkdocs.yml`, a strict docs build in
  CI, the nav reorganised, mermaid rendering in the interim (OQ1).
- The documentation rewrite inventory from DESIGN-0038 A4, the
  `v2-migration.md` contradictions, the historical admonitions.
- `changelog-update.yml` prepending; the `[main, v2]` trigger reverts;
  `ct.yaml`; `prerelease.yml` and `changelog-update.yml` wording;
  CLAUDE.md § The v2 branch and rule 6; CONTRIBUTING.
- Security tier 1: SBOM generation and attestation for binaries, both
  images and the chart; CodeQL default setup; required status checks
  and a `v*` tag ruleset; `@latest` pins; Dependabot/Renovate
  clean-up; chart security-context parity; `verifying-releases.md`;
  `SECURITY.md`; `concurrency` and `release.yml` permission narrowing.
- Chart `2.0.0`, the `2.0.0` UI changelog entry, regenerated READMEs.
- The soak gate and the homelab tasks gathered from IMPL-0029/0030/
  0031/0032, ticked here and there.
- Post-GA bookkeeping: docz statuses, issues, memory, the `v2` branch.

<!--docz:in-scope:end-->

<!--docz:out-of-scope:start-->
### Out of Scope

- Security tiers 2–3 (IMPL-0036) and the Astro docs site (IMPL-0035).
- Any runtime behaviour change. Every code edit in this IMPL is to
  workflows, chart defaults, docs or metadata.
- An in-place upgrade from 1.x (DESIGN-0032 D27).
- IMPL-0026, DESIGN-0034, the Go module path (OQ11 decided).

<!--docz:out-of-scope:end-->
<!--docz:scope:end-->

## Pre-implementation audit

Verified 2026-10-24/25 (DESIGN-0038 § Background):

- `main` = 1a825c0 (v1.14.1) has five commits `v2` lacks: #203
  (`tools/rgctl`), #227, #225 (`docs/operations/rgctl.md`), #228
  (`release.yml` `base-branch: true`), #229. `v2` = 517f0cd (rc.16)
  after #239. Merge-base 5bd96bb. The `main` ruleset (12545694) has
  rules `deletion`, `non_fast_forward`, `pull_request` and no bypass
  actors; no required checks; no tag ruleset. No `v2.0.0` tag or
  release exists.
- `release.yml`: `bump-version` (`jefflinse/pr-semver-bump@v1.7.4`,
  labels `major|minor|patch`, noop `dont-release`) → `release`
  (goreleaser, GPG) → `publish-ghcr`/`publish-ecr` (reusable, `tag`
  from the bump; ECR gated on `vars.ECR_PUBLISH_ENABLED`). The action
  parses all tags as semver (loose), sorts, and with `base-branch`
  takes the newest reachable from the base branch; node-semver
  `inc("2.0.0-rc.16", major|minor|patch)` = `2.0.0`.
- `prerelease.yml` matches `v*-*`; `changelog-update.yml` runs
  `git-cliff --output CHANGELOG.md` (full regeneration) on `v*` pushes
  without a dash; `cliff.toml` has a header and `render_always`.
- `gh-pages.yml` deploys `mkdocs-ghpages.yml` (nav up to DESIGN-0009,
  no Operations, no v2 pages; `theme: material` is its only non-nav
  difference from `mkdocs.yml`, which has `exclude_docs` and `plugins:
  techdocs-core`) with an unpinned `pip install mkdocs-material`;
  neither config enables `pymdownx.superfences`, so the 39 mermaid
  blocks render as code.
- Landing docs still describe v1: `README.md`, `docs/index.md`,
  `docs/usage/getting-started.md`, `docs/ADDING_RULES.md`,
  `docs/operations/{aws,scaling,ent-setup}.md`, chart README intro.
  `v2-migration.md` contradicts itself at the `RECONCILE_FRESHNESS` and
  `migrate.freshness` rows.
- Security: no `sboms:` in `.goreleaser.yml`; no `attest` in
  `docker-bake.hcl`; CodeQL default setup `not-configured`; 6 of 111
  external action refs SHA-pinned; govulncheck/go-licenses installed
  `@latest`, `mkdocs-material` unpinned, `mise.toml` `latest` for
  trivy, syft, cosign, goreleaser, git-cliff, govulncheck, go-licenses;
  `.github/dependabot.yml` docker entry at `directory: cicd` (does not
  exist) and actions managed by both Dependabot and Renovate;
  `renovate.json5` lacks the `slsa-github-generator` pin exception;
  `values.yaml` `podSecurityContext: {}` and a container context
  without `allowPrivilegeEscalation`/`capabilities`/seccomp while
  `templates/ui.yaml` has all of them plus
  `automountServiceAccountToken: false`; no role imports a Kubernetes
  client (`k8s.io/client-go` absent from `go.mod`); `SECURITY.md`
  § Reporting Vulnerabilities is three lines; the chart README is the
  only verification doc; no `concurrency:` anywhere; `release.yml`
  grants `contents/packages/id-token: write` workflow-wide.
- `ci.yml` gates jobs on `dorny/paths-filter` outputs (`go`, `docker`,
  `helm`, `workflows`, `alerts`, `monitoring`, `sql`, `temporal`,
  `api`, `ui`); skipped jobs satisfy required checks (CLAUDE.md § CI
  paths-filter convention). `pr-labels.yml` requires exactly one semver
  label.

## How the phases are ordered

Phase 1 first because every later edit must land on a tree that is a
superset of `main`. Phases 2–5 are independent of one another and of
the soak; they are separate pull requests against `v2`, each reviewable
alone and none changing runtime behaviour. Phase 6 is the last pull
request before M2 and makes the tree GA-shaped. Phase 7 runs in the
homelab in parallel with 2–6 and is the clock. Phase 8 is M2 and the
release; Phase 9 the clean-up. IMPL-0033 (rc.17) merges before Phase 6
so the GA docs name knobs that exist.

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its
tasks are checked off and its success criteria are met.

---

<!--docz:phase:start-->
### Phase 1: Sync main into v2

DESIGN-0038 A1 (M1), D1.

<!--docz:tasks:start-->
#### Tasks

- [x] 1.1 **Branch.** `chore/sync-main-into-v2` from `v2`; `git merge
  --no-ff origin/main`. Expected conflicts: `.github/workflows/release.yml`
  (keep `base-branch: true` and `v2`'s surrounding text), `go.mod`/
  `go.sum` (keep `v2`'s versions; both lines have 1.26.10). Anything
  else conflicting is noted on this task. *Done on
  `feat/impl-0034-ga-cutover` (one PR for the IMPL), not a separate
  branch. Also conflicted: `ci.yml` (rgctl coverage step kept in
  test-go, before v2's replay/integration jobs), `.gitignore` (both),
  `.golangci.yml` (`rgctl-boundary` joined v2's depguard rules),
  `Makefile` (`lint: lint-controls-docs lint-rgctl`), `mkdocs.yml`
  and the two docz READMEs (v2's side, then 1.3).*
- [x] 1.2 **Verify the superset.** `git merge-base --is-ancestor
  origin/main HEAD` exits 0; `git ls-files tools/rgctl | wc -l` > 0;
  `docs/operations/rgctl.md` exists; `grep -c 'base-branch: true'
  .github/workflows/release.yml` = 1; `.codecov.yml` carries the rgctl
  flag; `go build ./... && go vet ./tools/...` (rgctl's module layout
  per IMPL-0027) pass.
- [x] 1.3 **Nav.** `mkdocs.yml` gains `docs/operations/rgctl.md` under
  Operations and DESIGN-0035 / IMPL-0027 under their sections (docz did
  not see them on `v2`); `docz update design` and `docz update impl`
  regenerate the READMEs; revert unrelated TOC churn.
- [ ] 1.4 (`deferred - human required`: the maintainer opens and merges the PR) **PR.** `dont-release`, **merge commit** (pick "Create a merge
  commit" by hand; the default squash would flatten `main`'s history
  into `v2`). CI green, including `lint` (rgctl is in the module set).
  *This IMPL's single PR carries the sync, so that PR is the one to
  merge with "Create a merge commit".*

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- `git log origin/v2..origin/main` is empty after the merge.
- `rgctl` builds and its tests pass on `v2`; the `v2-migration.md`
  links to `rgctl.md` resolve.
- `release.yml` on `v2` carries `base-branch: true`.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 2: The public docs site, interim

DESIGN-0038 A3, D2, OQ3 (decided (a)); IMPL-0034 OQ1, OQ2.

<!--docz:tasks:start-->
#### Tasks

- [x] 2.1 **Inherit.** `mkdocs-ghpages.yml` becomes `INHERIT: mkdocs.yml`
  plus `theme: {name: material}`; `plugins` is overridden to `[search]`
  because `techdocs-core` is not installed on Pages. Per OQ1 (b) no
  mermaid extension is added: the 39 diagrams keep rendering as code
  on the interim site until IMPL-0035 (which renders them client-side).
- [x] 2.2 **Pin.** `gh-pages.yml` installs `mkdocs-material==<current>`
  with a `# renovate: datasource=pypi depName=mkdocs-material` comment
  so Renovate's mise/pypi managers bump it.
- [x] 2.3 **Nav.** `mkdocs.yml`: Usage order (Getting Started, Policy
  reference, Control-type reference, The business UI, The read-only
  API, Identity providers); Operations gains v2 overview, onboarding,
  migration, rgctl, verifying releases (Phase 5) at the top; a
  "Historical (1.x)" group holds the pages Phase 3 marks historical.
  docz keeps appending new documents to their sections. *The
  verifying-releases entry lands with its page in 5.9 (strict mode
  refuses a nav entry with no file).*
- [x] 2.4 **Strict build in CI.** `ci.yml`: a `docs` paths filter
  (`docs/**`, `mkdocs.yml`, `mkdocs-ghpages.yml`) and a `docs` job (OQ2)
  that installs the pinned `mkdocs-material` and runs `mkdocs build
  --strict --config-file mkdocs-ghpages.yml`; `make lint-docs` wraps the
  same command for local use. A dead nav entry or a broken inherit now
  fails the pull request, not the deploy.
- [x] 2.5 **Verify locally.** `make lint-docs` renders into a throwaway
  directory (`mkdocs build -d build/mkdocs`; MkDocs's default `site_dir`
  is `site/`, which IMPL-0035 claims for the Astro source, so set
  `site_dir: build/mkdocs` in `mkdocs.yml` now and narrow `.gitignore`'s
  `site/` entry to `build/`); open one Usage page and confirm the nav;
  note the result on the task. *2026-10-27: `make lint-docs` exits 0
  with no warnings (mkdocs-material 9.8.0, via `uvx`), output in
  `build/mkdocs`, no `site/`; `usage/api/index.html` carries the Usage
  group with Identity providers, the v2 Operations entries and
  Historical (1.x).*

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- `mkdocs build --strict --config-file mkdocs-ghpages.yml` passes on
  `v2` with every page in the nav and no warnings, writing under
  `build/`, never `site/`.
- The two mkdocs configs have no nav of their own to drift: `grep -c
  '^nav:' mkdocs-ghpages.yml` = 0.
- The `docs` CI job runs on a docs-only pull request and skips on a
  Go-only one.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 3: The documentation rewrite

DESIGN-0038 A4 (the disposition table), OQ10, OQ12.

<!--docz:tasks:start-->
#### Tasks

- [x] 3.1 **`README.md`.** Rewrite: what repo-guardian is on the
  controls line (two Apps, policy root, controls, evaluate/remediate,
  Temporal + Postgres, UI/API); architecture diagram adapted from
  `docs/operations/v2-overview.md`; install via the chart at `2.0.0`
  pointing at `v2-onboarding.md`; the 1.14 line's README reachable by
  tag; the `latest` note from A9; the release-verification pointer
  (Phase 5). No `guardian.hcl`, `watch = true`, Valkey, `SCHEDULE_INTERVAL`,
  `STORE_BACKEND`.
- [x] 3.2 **`docs/index.md`.** Mirror 3.1's opening; link v2 overview,
  onboarding, UI, API, identity providers, verifying releases.
- [x] 3.3 **`docs/usage/getting-started.md`.** Rewrite as the fresh-install
  walk: create the two Apps (policy-derived permissions from
  `registerRemediation`'s log line), write a minimal policy root, chart
  values, first evaluation, reading the UI, turning on remediation for
  one org. Cross-check every value name against `values.yaml` and every
  env var against `config/`.
- [x] 3.4 **`docs/ADDING_RULES.md` → "Adding a control type".** New
  content: `controls/builtin` registration, `Semantics()`,
  `TestRevisionGolden`, `Owns()`/`Reads()`, `repo-guardian controls
  docs` and `make lint-controls-docs`; the old text moves to
  `docs/historical/adding-rules-1x.md` with the admonition (3.7).
  `SECURITY.md` and `mkdocs.yml` links updated.
- [x] 3.5 **Operations pages.** `aws.md` (drop ElastiCache/Valkey; RDS for
  the controls database with the two roles; Temporal is the
  operator's), `scaling.md` (drop `staleSweep.*`/`queue.valkey.*`; KEDA
  per queue, `evaluator.*`/`remediator.*`, pool sizing ≥ concurrency;
  keep the posture/`max by` section), `ent-setup.md` (two Apps,
  permissions per App, two webhook URLs).
- [x] 3.6 **`v2-migration.md`.** Fix the two contradictions:
  `RECONCILE_FRESHNESS` → "none", `staleSweep.freshness` → `EVAL_INTERVAL`
  with `migrate.freshness` removed as a replacement; re-read the whole
  page against the chart values once.
- [x] 3.7 **Historical admonitions.** One admonition at the top of each
  1.x-only page (`absent-rules-migration`, `annotation-properties-migration`,
  `chart-0.5.0-migration`, `pr-convergence-migration`,
  `template-migration`, `custom-properties-security-fix`,
  `delayed-requeue-runbook`, `property-name-charset`,
  `compliance-reports`): "This page describes repo-guardian 1.x. v2 has
  no equivalent" with the v2 pointer where one exists. GFM alert syntax
  (`> [!WARNING]` as the first line, then `> **1.x only.** ...`), never
  an MkDocs `!!!` admonition: Starlight renders a GFM alert as an aside
  and MkDocs/TechDocs/GitHub degrade it to a blockquote, so one source
  serves every renderer (claudelint's DESIGN-0003 convention).
- [x] 3.8 **Chart README.** `README.md.gotmpl` introduction rewritten for
  the controls line; `make helm-docs`.
- [x] 3.9 **Tombstone.** `git rm deploy/MIGRATED.md` (OQ10) and remove
  its mentions (CLAUDE.md IMPL-0014 entry gets "deleted at GA").
- [x] 3.10 **Sweep.** `grep -rn 'guardian\.hcl\|watch = true\|Valkey\|
  staleSweep\|STORE_BACKEND' README.md docs/index.md docs/usage
  docs/operations/{aws,scaling,ent-setup,v2-*}.md
  charts/repo-guardian/README.md.gotmpl` returns only lines inside
  historical admonitions or explicit "was removed" sentences; paste the
  output on the task. *2026-10-27, every hit is a removal sentence, a
  v1→v2 comparison or under a history admonition:*

  ```text
  docs/usage/policy-reference.md:418:## From `guardian.hcl`
  docs/operations/scaling.md:12:> `staleSweep.*` and `queue.valkey.*` were removed in v2, as were
  docs/operations/v2-overview.md:4,28,82,256,260,470 (guardian.hcl → policy directory comparison)
  docs/operations/v2-overview.md:364,371,475 (v1 Valkey job system vs Temporal)
  docs/operations/v2-migration.md:223,236,237,249,276 (removed-value tables)
  docs/operations/aws.md:21:> Valkey (and ElastiCache with it) was removed in v2.
  README.md:9,132 (fresh-install note; rgctl reads a 1.x guardian.hcl)
  docs/operations/v2-onboarding.md:519:`guardian.hcl` is gone. The policy is a **directory**:
  charts/repo-guardian/README.md.gotmpl:252-363 (rc/1.x upgrade notes under the History admonition)
  ```

  *The chart README's upgrade notes gained an `## Upgrade notes` heading
  with a GFM history admonition, since several name values that were
  later removed. `docs/README.md`'s quick links were rewritten too.
  Follow-ups found on the way: `rds-terraform-variables.md` is still
  v1 in places, and `store.controls.enabled` still defaults to `false`
  with an "off until IMPL-0029" comment.*

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- Every page in `mkdocs.yml`'s Usage and Operations groups (outside
  Historical) describes v2; the 3.10 grep is clean.
- `make lint-docs` (Phase 2) passes; `make lint-controls-docs` passes
  (3.4 touches no generated file).
- A reader following `getting-started.md` from an empty cluster reaches
  the first evaluation using only values that exist in `values.yaml`
  (checked by the implementer in a kind cluster or by review against
  the schema).

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 4: Changelogs, branch entries and CLAUDE.md

DESIGN-0038 A5, A6, D3.

<!--docz:tasks:start-->
#### Tasks

- [x] 4.1 **Prepend.** `changelog-update.yml`: `git-cliff --latest
  --strip all --prepend CHANGELOG.md` (header and footer already live
  in the file; `--latest` renders the newest tag's section only).
  Verify locally against a scratch copy: run it for `v2.0.0-rc.16` and
  confirm the rc.11 section and the 1.x sections below are unchanged
  byte for byte. Default the `ref` input to `main` only; reword the
  comment about rc tags. *Done with `--latest --prepend` and no
  `--strip`: tested on a scratch copy (git-cliff 2.14.2, rc.18 as the
  newest tag), `--strip all` writes the section above `# Changelog`,
  because git-cliff only swaps the file's header out when it renders
  one. Without it the header stays on top, exactly one section is
  added and every older section is byte-identical. `cliff.toml`'s
  header gained a trailing blank line so the new section is separated
  from it.*
- [x] 4.2 **Triggers.** `ci.yml`, `license-check.yml`, `security.yml`:
  `branches: [main]` (keep `security.yml`'s `pull_request` trigger);
  `ct.yaml` `target-branch: main`. These land on `v2` and are correct
  the moment M2 merges; until then `v2` pushes run no CI, so this is
  the **last** pull request before Phase 6 and is merged together with
  it (OQ3). *Single-PR note: this IMPL ships as one PR against `v2`, and
  a `pull_request: branches: [main]` trigger stops CI on that PR, so
  4.2 is the branch's last commit, pushed after CI is green on
  everything else.* *Done as the branch's last commit; the remaining
  `v2` strings in the workflows are the rc version in `release.yml`'s
  comment and action/tool versions.*
- [x] 4.3 **`prerelease.yml`.** Comment: hand-cut `vX.Y.Z-rc.N` tags on
  `main`, built here because `release.yml`'s bump cannot cut
  pre-releases; dispatch is possible once on `main`, so add
  `workflow_dispatch` with a `tag` input for re-runs.
- [x] 4.4 **CLAUDE.md.** Replace § "The v2 branch (IMPL-0025)" with a
  short § "Release line": GA releases by semver label through
  `release.yml`, never a hand tag; rc tags by hand with `dont-release`
  and `prerelease.yml` + `ghcr.yml` dispatch; the merge-commit rule for
  line merges; the changelog prepend. Rule 6 gains "a GA release is cut
  by its semver label". Keep the historical bullets that still explain
  code (UI image, e2e stack, DESIGN-0037 contracts) under a new
  heading "The UI" so nothing load-bearing is lost.
- [x] 4.5 **CONTRIBUTING.md.** The release section describes the label
  flow for GA and the rc flow; the chart-bump changelog step stays.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- The scratch prepend leaves every existing `CHANGELOG.md` section
  intact and adds exactly one.
- `grep -rn 'v2' .github/workflows/*.yml ct.yaml` returns only
  `prerelease.yml`'s and `changelog-update.yml`'s reworded comments.
- CLAUDE.md has no instruction that assumes a `v2` branch exists.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 5: Security tier 1

DESIGN-0038 D2, D3, D4 tier 1; D10–D12; OQ16–OQ19. GitHub-settings
tasks carry the exact command and are run per OQ4.

<!--docz:tasks:start-->
#### Tasks

- [x] 5.1 **SBOMs for binaries.** `.goreleaser.yml` `sboms: [{artifacts:
  archive}]` (syft, SPDX JSON, `*.sbom.json` beside each archive;
  `checksums.txt` covers them). `make release-snapshot` (goreleaser
  `--snapshot`) locally shows the SBOM files; syft pinned in
  `mise.toml` (5.6). *`make release-local` (goreleaser `--snapshot`)
  wrote four `*.tar.gz.sbom.json` files, all four in `checksums.txt`.
  `release.yml` and `prerelease.yml` install syft
  (`anchore/sbom-action/download-syft`) before goreleaser, which needs
  it on the runner.*
- [x] 5.2 **SBOMs for images.** `docker-bake.hcl` `_common`: `attest =
  ["type=provenance,mode=max", "type=sbom"]` (provenance made explicit,
  SBOM added; `BUILDX_METADATA_PROVENANCE` untouched, the digest still
  comes from the expression layer). In `ghcr.yml` and `ecr.yml`, after
  each `cosign sign`: `syft <image@digest> -o spdx-json > sbom.json`
  and `cosign attest --yes --type spdxjson --predicate sbom.json
  <image@digest>`, for the image, the UI image and the chart (the
  chart's SBOM is syft over the `.tgz`), then `cosign verify-attestation
  --type spdxjson --certificate-identity-regexp ... <ref@digest>` as the
  in-workflow verify (the SBOM-presence check from the design's testing
  strategy). Digests from `steps.digest.outputs.digest`, never a shell
  blob. *`dev`/`ui-dev` (local `type=docker` loads) override `attest`
  with both types `disabled=true`, since an empty list does not
  override an inherited one and the docker exporter cannot carry
  attestations. syft over the chart `.tgz` gives an SPDX-2.3 document.
  The verify regexp is the workflow file (`ghcr\.yml@`/`ecr\.yml@`),
  matching both the `release.yml` call (`@refs/heads/main`) and an rc
  dispatch (`@refs/tags/...`).*
- [x] 5.3 **CodeQL.** Default setup for `go`, `javascript-typescript`,
  `actions` (OQ18): `gh api -X PATCH
  repos/donaldgifford/repo-guardian/code-scanning/default-setup -f
  state=configured -f query_suite=default -f
  'languages[]=go' -f 'languages[]=javascript-typescript' -f
  'languages[]=actions'`; confirm the first analysis lands under tool
  `CodeQL` and that `security.yml`'s govulncheck upload still appears
  beside it. *(Enabled by the maintainer 2026-10-27 through Settings, Default
  suite; GitHub also picked up `python`. The first run passed, with
  analyses for go, javascript-typescript, actions and python beside
  govulncheck. One alert, #17: highlight.js from cdnjs without SRI in
  `docs/design/controls-redesign.html`. It is fixed by hashing all three
  CDN loads and pinning mermaid to 11.17.2.)*
- [ ] 5.4 (`deferred - human required`, OQ4 (a)) **Rulesets.** (a) `main`: add `required_status_checks` with the
  set OQ3 chooses (the aggregate `ci-ok` job added in 5.5, `Check
  Required Labels`, `govulncheck`, `Check Dependency Licenses`), strict
  off (merge commits), `pull_request` with 0 approvals as today. (b) A
  tag ruleset on `refs/tags/v*` with `deletion` and `non_fast_forward`
  rules only (OQ5) so `release.yml`'s bump can still create tags. Both
  via `gh api` with the JSON written in the task note first; `gh api
  repos/.../rulesets/<id>` output pasted after. *Commands, for after
  M2 (the checks must exist on `main` first):*

  ```bash
  # (a) main: add the required checks to ruleset 12545694, keeping its rules.
  gh api repos/donaldgifford/repo-guardian/rulesets/12545694 \
    | jq '{name, target, enforcement, conditions, bypass_actors,
           rules: (.rules + [{type: "required_status_checks", parameters: {
             strict_required_status_checks_policy: false,
             required_status_checks: [
               {context: "CI OK"},
               {context: "Check Required Labels"},
               {context: "Check for vulnerabilities using `govulncheck`"},
               {context: "Check Dependency Licenses"}]}}])}' \
    > /tmp/main-ruleset.json
  gh api -X PUT repos/donaldgifford/repo-guardian/rulesets/12545694 \
    --input /tmp/main-ruleset.json

  # (b) v* tags: no deletion, no move; creation stays open for release.yml.
  gh api -X POST repos/donaldgifford/repo-guardian/rulesets --input - <<'JSON'
  {"name": "release tags", "target": "tag", "enforcement": "active",
   "conditions": {"ref_name": {"include": ["refs/tags/v*"], "exclude": []}},
   "rules": [{"type": "deletion"}, {"type": "non_fast_forward"}]}
  JSON
  ```
- [x] 5.5 **`ci-ok` aggregate.** `ci.yml`: a final job `ci-ok` with
  `needs:` every job, `if: always()`, failing when any needed job's
  `result` is `failure` or `cancelled` (skipped counts as pass, which is
  what the paths filters need). It is the one required check for CI.
  *Job name `CI OK`, needs all 18 jobs.*
- [x] 5.6 **Pins.** `mise.toml`: exact versions for `trivy`, `syft`,
  `cosign`, `goreleaser`, `git-cliff`, `govulncheck`, `go-licenses`,
  `golines`, `jq`, `checkmake`, `promtool`, helm plugins, each with its
  `# renovate:` comment. `ci.yml` Security Scan, `security.yml` and
  `license-check.yml` install through `jdx/mise-action` instead of
  `go install ...@latest`. `ci.yml` `helm plugin install` pinned to a
  version (keep `--verify=false`, documented in CLAUDE.md § Helm CLI
  semantics; a verified install is IMPL-0036's). *Every `latest` in
  `mise.toml` is pinned to the installed version (plus `actionlint`,
  for 5.12); govulncheck and go-licenses install through
  `jdx/mise-action` with `install_args` naming only that tool;
  helm-unittest is `v1.2.1` with a renovate comment.*
- [x] 5.7 **Dependabot and Renovate.** `.github/dependabot.yml`: remove
  the `github-actions` and `docker` version-update entries (the docker
  one points at a nonexistent `cicd/`); keep `gomod` security-only and
  add `npm` (`directory: /ui`) and `github-actions` security-only
  entries (`open-pull-requests-limit: 0`). `renovate.json5`: a
  `packageRules` entry `matchPackageNames: ["slsa-framework/slsa-github-generator"],
  pinDigests: false` with a comment pointing at CLAUDE.md's reason.
  Renovate's dashboard (#213) shows no duplicate of a Dependabot PR
  after one cycle (verified in Phase 9). *The UI entry is
  `package-ecosystem: "bun"`, not `npm`: the lockfile is `bun.lock`,
  and Dependabot's npm ecosystem needs a `package-lock.json`.*
- [x] 5.8 **Pod hardening.** `values.yaml`: `podSecurityContext:
  {runAsNonRoot: true, runAsUser: 65534, runAsGroup: 65534, seccompProfile:
  {type: RuntimeDefault}}`, `securityContext: {readOnlyRootFilesystem:
  true, allowPrivilegeEscalation: false, capabilities: {drop: [ALL]}}`
  (move `runAsNonRoot`/`runAsUser` up to the pod level, matching
  `ui.yaml`); `automountServiceAccountToken: false` on every role's
  Deployment and on the hook Jobs (no role or Job calls the Kubernetes
  API: `k8s.io/client-go` is not in `go.mod`, the Jobs run `migrate`
  and `psql`). `templates/deployment.yaml`, `migrate-job.yaml` and the
  two controls-roles/read-only Jobs render them; helm-unittest asserts
  each field per role in `tests/security_context_test.yaml`; `ci-values.yaml`
  (busybox) still renders. *The two psql Jobs keep the baked Postgres
  pod context (uid 999) and get a container context with no privilege
  escalation, `drop: [ALL]` and RuntimeDefault seccomp, but not a
  read-only root (the image's shell may spool the heredoc to `/tmp`).
  `make helm-unittest` 257/257; default values render every Deployment
  and the migrate Job with `automountServiceAccountToken: false`,
  RuntimeDefault and no privilege escalation.*
- [x] 5.9 **Verifying releases.** `docs/operations/verifying-releases.md`:
  for `repo-guardian`, `repo-guardian-ui` and the chart, the `cosign
  verify` and `cosign verify-attestation --type slsaprovenance` and
  `--type spdxjson` commands with the certificate identity
  `https://github.com/donaldgifford/repo-guardian/.github/workflows/ghcr.yml@refs/tags/<tag>`
  and the OIDC issuer; for binaries, `checksums.txt` + its GPG
  signature today (the key published in the page and as a release
  asset, OQ20 says cosign replaces it in IMPL-0036) and
  `slsa-verifier verify-image` for the images; retrieving an SBOM with
  `cosign download attestation`. The chart README's verification
  section becomes a link. *Written with Phase 3 (the landing pages
  link it). Certificate identity is given as a regexp over
  `ghcr|ecr.yml@refs/(heads/main|tags/v.+)`, because a GA publish runs
  `ghcr.yml` as `release.yml`'s reusable workflow, whose identity is
  `@refs/heads/main`, not the tag. The release key's fingerprint
  (C47D…8D59, from `checksums.txt.sig`) is on the page; **publishing
  the public key as a release asset is `deferred - human required`**.*
- [x] 5.10 **`SECURITY.md`.** § Reporting Vulnerabilities: GitHub private
  vulnerability reporting (enabled) as the channel, acknowledgement
  within 3 business days and a fix or mitigation plan within 30 for
  high/critical, supported versions (2.x; 1.14.x for 90 days after GA),
  the verifying-releases link, and the supply-chain controls list from
  DESIGN-0038 D1 in two paragraphs. The three remaining
  `docs/ADDING_RULES.md` links move to the new page from 3.4. *The v1
  "Scope-constrained rule evaluation" and key/filesystem sections were
  rewritten for v2 (two Apps, pod hardening, scoped policy), which is
  where the one remaining link now points at the new page.*
- [x] 5.11 **Workflow hygiene.** `concurrency: {group: release-${{
  github.ref }}, cancel-in-progress: false}` on `release.yml`,
  `ghcr.yml`, `ecr.yml` and `prerelease.yml`; `release.yml` job-level
  permissions (`bump-version`: `contents: write, pull-requests: read,
  issues: read`; `release`: `contents: write`; the publish callers keep
  their documented ceilings) and the workflow-level block reduced to
  `contents: read`. Re-run the June post-mortem check: the first
  push-triggered run after this change **creates jobs**. *`ghcr.yml`
  and `ecr.yml` use `release-ghcr-`/`release-ecr-${{ github.ref }}`,
  not `release-${{ github.ref }}`: called from `release.yml`, a
  reusable workflow sees the caller's `github.ref`, and one waiting on
  its caller's group is cancelled as a deadlock. The post-merge
  "creates jobs" check is `deferred - human required` (8.3).*
- [x] 5.12 **Lint the workflows once.** Run `actionlint` (mise) and
  `zizmor` locally on the edited workflows before the PR; the CI job
  for them is IMPL-0036's, but the edits here should not introduce
  findings. *actionlint 1.7.12 clean on every workflow (one
  pre-existing SC2086 in `gh-pages.yml` fixed). zizmor 1.31.1
  (`--offline`) against the base commit, `unpinned-uses` aside (that is
  IMPL-0036's SHA pinning): `excessive-permissions` 6 → 3, everything
  else unchanged after giving the new `docs` checkout
  `persist-credentials: false`.*

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- A `workflow_dispatch` of `ghcr.yml` with `dry_run=false` for the
  current rc tag (re-publish is idempotent) attaches an SPDX attestation
  to all three artefacts and the verify step passes; `cosign download
  attestation` returns it.
- `gh api repos/.../code-scanning/default-setup` reports `configured`
  with the three languages; the first CodeQL analysis is visible.
- A test pull request with one deliberately failing job cannot be
  merged (`ci-ok` red, required); a `v*` tag cannot be deleted or moved
  by the maintainer without a bypass.
- `grep -rn '@latest' .github/workflows mise.toml` is empty; a
  `helm template` of default values shows every role's pod with
  seccomp, no privilege escalation, dropped capabilities and no
  mounted service-account token.
- The push-triggered `release.yml` run after 5.11 shows jobs.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 6: The GA-prep pull request

DESIGN-0038 A1 (the tree before M2), A5.

<!--docz:tasks:start-->
#### Tasks

- [x] 6.1 **Chart.** `Chart.yaml` `version: 2.0.0`, `appVersion:
  "2.0.0"`; `make helm-docs`.
- [x] 6.2 **UI changelog.** `ui/CHANGELOG.yaml` `2.0.0` entry (title "v2:
  controls, remediation and the UI"; summary written so no Playwright
  substring locator matches it; tags `new`, `breaking`, `fleet`,
  `findings`, `policy`, `ui`, `api`, `chart`); `bun run gen:changelog`;
  `make lint-ui` green.
- [x] 6.3 **Preflight script.** `scripts/ga-preflight.sh` (OQ6), run by
  the maintainer on the M2 head before merging: `git merge-base
  --is-ancestor origin/main HEAD`; `git tag --merged HEAD | grep -x
  v2.0.0-rc.17`; no `v2.0.0` tag or release; `grep -c 'base-branch:
  true' .github/workflows/release.yml` = 1; `Chart.yaml` both fields
  `2.0.0`; `make lint-ui lint-docs`; the 3.10 grep; the PR has exactly
  the `major` label (`gh pr view --json labels`). Exit non-zero on any
  failure with the failing check named. *`--pr N` or `--skip-label`.
  The 3.10 check allowlists the reviewed files, so a new hit elsewhere
  fails (proven by appending `Valkey` to `docs/index.md`). Passes on
  `feat/impl-0034-ga-cutover` with `--skip-label`, 2026-10-27.*
- [x] 6.4 **Release notes.** Draft `docs/releases/2.0.0.md` (or the GitHub
  release body, OQ7): what 2.0 is, fresh install only, `latest` moves,
  the identity-provider page, verification. goreleaser's release header
  links it. *The header links it only when `.Version` is 2.0.0, and
  links verifying-releases on every release; `goreleaser check` passes.
  The page is in the nav under Release notes and linked from the
  README.*
- [ ] 6.5 (`deferred - human required`: the maintainer opens or approves
  the pull request; this IMPL ships as one PR, which needs "Create a
  merge commit" because it carries Phase 1) **PR.** `dont-release` against `v2`, together with Phase 4.2's
  trigger reverts; merge commit not required (this is a normal squash
  PR onto `v2`).

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- `scripts/ga-preflight.sh` passes on `v2`'s head after merge (with the
  label check skipped via a flag, since there is no M2 PR yet).
- `v2`'s tree is what `main` will be: a `diff` between `v2` and the
  eventual M2 merge commit is empty but for the merge itself.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 7: Soak and homelab

DESIGN-0038 A7; IMPL-0030 Phase 9 and OQ7/OQ8; IMPL-0029 9.7, 9.8;
IMPL-0031 8.3; IMPL-0032 9.2, 9.3. Every task here is
`deferred - human required`; the implementer ticks the box when the
maintainer reports it, in this document and in the origin document.

<!--docz:tasks:start-->
#### Tasks

- [x] 7.1 *(Skipped 2026-10-27 by maintainer decision: roll forward,
  no soak.)* IMPL-0030 9.2–9.7 as written there (Apps, stop the old writers,
  delete the old schedules, `rgctl prs list` per old App, fresh
  install, verify evaluation and KEDA). Several are already true of
  the running homelab (rc.16 is the fresh install); tick what is true
  with the date.
- [x] 7.2 *(Skipped 2026-10-27 by maintainer decision: roll forward,
  no soak.)* IMPL-0030 9.8: `donaldgifford` in `remediate`; 7 days with
  every hold explained and no PR over the cap (OQ7 window 1). Record
  the start date here.
- [x] 7.3 *(Skipped 2026-10-27 by maintainer decision: roll forward,
  no soak.)* IMPL-0030 9.9: the remaining orgs; `rgctl prs close` per OQ6.
  Window 2 starts.
- [x] 7.4 *(Skipped 2026-10-27 by maintainer decision: roll forward,
  no soak.)* IMPL-0029 9.7/9.8 and IMPL-0032 9.2: the fresh evaluate-only
  install check and the UI walk (Findings, orgs tree, fleet panels,
  Remediations, status Changelog card) on rc.17.
- [x] 7.5 *(Skipped 2026-10-27 by maintainer decision: roll forward,
  no soak.)* IMPL-0029 8.9, IMPL-0031 8.3, IMPL-0032 9.3: the group-split
  check by the `identity-provider.md` procedure (IMPL-0033 4.7); one
  item, three origin boxes.
- [x] 7.6 *(Skipped 2026-10-27 by maintainer decision: roll forward,
  no soak.)* Window 2 complete: 7 more days with no unexplained `conflict`,
  `foreign_branch` or `permission` hold (`remediation_holds` gauge and
  the Remediations page). GA clock satisfied; date recorded.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- IMPL-0030 Phase 9's success criteria hold and its boxes are ticked.
- Both OQ7 windows have elapsed with the holds explained in the task
  notes.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 8: The merge and the release

DESIGN-0038 A1 (M2), A2, A9; D1; OQ2.

<!--docz:tasks:start-->
#### Tasks

- [x] 8.1 **M2 PR.** From `v2` into `main`, title "release: v2.0.0", body
  from 6.4, label **`major`** and nothing else from the semver set;
  `scripts/ga-preflight.sh` output pasted into the PR. *(PR #245,
  2026-10-27. The label check was too strict: it refused the labeler's
  path labels. Fixed afterwards to check the semver set only, which is
  what this task asks for.)*
- [x] 8.2 **Merge** with "Create a merge commit" (`deferred - human
  required`: the maintainer merges). Immediately open the Actions tab
  and confirm the `Release` run **has jobs** (a `startup_failure` has
  none and shows only on the web page). *(Merge commit 8350f11; Release
  run 40514249680 created every job and went green.)*
- [x] 8.3 **Bump.** `bump-version` log says "Only considering tags on
  branch main" and tags `v2.0.0`; `gh release view v2.0.0` exists with
  goreleaser's archives, `checksums.txt`, its signature, the SBOMs and
  `openapi.yaml`. *(Tagged `v2.0.0` on 8350f11; all four archives, their
  SBOMs, `checksums.txt` + `.sig` (GPG verified) and `openapi.yaml`.)*
- [x] 8.4 **Registries.** `publish-ghcr` (and `publish-ecr` if enabled)
  green with every SLSA job; `cosign verify` on
  `ghcr.io/donaldgifford/repo-guardian:2.0.0` and `repo-guardian-ui:2.0.0`;
  `cosign verify-attestation --type spdxjson` on both and on the chart;
  `helm pull oci://ghcr.io/donaldgifford/charts/repo-guardian --version
  2.0.0`; `latest` and `2.0` resolve to the 2.0.0 digests. *(All pass;
  ECR is not enabled. The verification page's SBOM read command was
  wrong for cosign v3, which writes a sigstore bundle; fixed to read
  `.dsseEnvelope.payload`. `slsa-verifier` was not run.)*
- [x] 8.5 **Docs site.** The `gh-pages.yml` run deploys the inherited
  config; the public site's nav shows Usage and Operations as in Phase
  2 and a design page renders its diagram. *(The deploy ran; the live nav
  shows Getting started, Historical (1.x), Release notes, Verifying
  releases. The diagram was not checked in a browser.)*
- [x] 8.6 **Changelog PR.** `changelog-update.yml` opens the prepend PR
  for `v2.0.0`; review that the 1.x sections are intact; merge
  (`dont-release`). *(It never fired: `bump-version` pushes the tag with
  GITHUB_TOKEN, which starts no workflow. Dispatched by hand, it pushed
  its branch but could not open the PR (the repository does not let
  Actions create PRs), and `--latest` started at `v2.0.0-rc.18`. The
  workflow now runs from the previous GA tag with rc tags ignored and
  takes the tag as a dispatch input. The 2.0.0 section (v1.14.1..v2.0.0,
  66 lines added, none removed) went in with the post-GA follow-up PR.)*
- [x] 8.7 **Alerts.** Dependabot alerts #15 and #16 close on their own
  (the patched versions are now on `main`); confirm. *(Both `fixed`.)*

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- One push produced the GitHub release, both images, the UI image, the
  chart and provenance; every artefact verifies with the commands in
  `verifying-releases.md` exactly as written.
- `latest` points at 2.0.0; the 1.14.1 tags still pull.
- The public site is the v2 nav.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 9: After GA

DESIGN-0038 A8; IMPL-0030 10.3–10.5.

<!--docz:tasks:start-->
#### Tasks

- [ ] 9.1 IMPL-0030 10.3 (`deferred - human required`): upgrade the
  homelab from rc.17 to 2.0.0 in place; OQ8's 14-day database window
  starts.
- [x] 9.2 **docz statuses.** DESIGN-0029, 0030, 0031, 0032, 0036 →
  Implemented; IMPL-0028, 0029, 0030, 0031, 0032 → Completed (fix
  IMPL-0025's body status line); DESIGN-0034, IMPL-0026 stay Draft;
  `docz update design`, `docz update impl`; revert TOC churn.
- [ ] 9.3 (`deferred - human required`: after GA) **Issues.** Close #232 and #235 (fixed, verified in the
  homelab), #199, #200, #73 ("1.x only; component removed in 2.0").
  Check #192 against the `monitoring generate` of 2.0.0 and close or
  re-scope.
- [ ] 9.4 (`deferred - human required`: after GA) **`v2` branch.** Delete `origin/v2` and its ruleset (24046649)
  once 9.2 is merged; update the memory files that name the branch
  ([[project-v2-branch-docz-ids]] becomes history).
- [x] 9.5 **CLAUDE.md and memory.** Record the ship date in CLAUDE.md
  § Release line and in memory; note anything the fresh install taught
  (IMPL-0030 10.5). *(Ship date 2026-10-27 recorded. No fresh-install
  notes, because Phase 7 was skipped.)*
- [ ] 9.6 (`deferred - human required`: after GA) **The `file.go` `FIX:`.** `internal/controls/file/file.go`:
  replace the yaml.v3 struct `Decode` duplicate-key compare with the
  linear node walk `internal/catalog` already uses; benchmark against
  the 50k-key fixture noted in the comment; ships as 2.0.1 by label.
- [ ] 9.7 (`deferred - human required`: after GA) **Close out.** IMPL-0034 → Completed; DESIGN-0038 stays In
  Review until IMPL-0035 and IMPL-0036 complete.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- Every docz index shows the statuses above; no open issue refers to a
  removed 1.x component without the "1.x only" closing comment.
- The homelab runs 2.0.0 and the old databases are archived and dropped
  at the end of OQ8's window (date recorded).

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:file-changes:start-->
## File Changes

| File | Action | Description |
| ---- | ------ | ----------- |
| `mkdocs-ghpages.yml`, `mkdocs.yml`, `gh-pages.yml`, `Makefile` (`lint-docs`), `ci.yml` (`docs` filter + job, `ci-ok`) | Modify | interim site, strict build, aggregate check |
| `README.md`, `docs/index.md`, `docs/usage/getting-started.md`, `docs/ADDING_RULES.md` → new content, `docs/historical/adding-rules-1x.md`, `docs/operations/{aws,scaling,ent-setup,v2-migration}.md`, nine historical pages, `charts/repo-guardian/README.md.gotmpl` | Modify/Create | the rewrite |
| `deploy/MIGRATED.md` | Delete | OQ10 |
| `.github/workflows/{changelog-update,ci,license-check,security,prerelease,release,ghcr,ecr}.yml`, `ct.yaml` | Modify | prepend, triggers, concurrency, permissions, SBOM attest + verify |
| `.goreleaser.yml`, `docker-bake.hcl` | Modify | `sboms:`, `attest` |
| `mise.toml`, `.github/dependabot.yml`, `renovate.json5` | Modify | pins; security-only Dependabot; SLSA exception |
| `charts/repo-guardian/values.yaml`, `templates/{deployment,migrate-job,*job*}.yaml`, `tests/security_context_test.yaml` | Modify/Create | pod hardening |
| `docs/operations/verifying-releases.md`, `SECURITY.md` | Create/Modify | verification and disclosure |
| `CLAUDE.md`, `CONTRIBUTING.md` | Modify | release line |
| `charts/repo-guardian/Chart.yaml`, `ui/CHANGELOG.yaml`, `ui/web/src/generated/changelog.json`, `scripts/ga-preflight.sh`, `docs/releases/2.0.0.md` | Modify/Create | GA prep |
| `internal/controls/file/file.go` | Modify | 9.6 |

<!--docz:file-changes:end-->

<!--docz:testing:start-->
## Testing Plan

- [ ] Phase 2 (local strict build passes; CI and the in-browser
  mermaid `<svg>` check are `deferred - human required`): `mkdocs build --strict` locally and in CI; a mermaid
  page renders an `<svg>`.
- [x] Phase 3: the 3.10 grep; `make lint-docs`; review of
  `getting-started.md` against `values.schema.json`.
- [x] Phase 4: scratch prepend byte-compare; `actionlint` on every edited
  workflow.
- [ ] Phase 5 (`helm unittest` passes, 257/257; the rest is
  `deferred - human required`): idempotent re-publish of the current rc with attestation
  and verify; the blocked-merge test for required checks; a tag
  move/delete refused; `helm unittest` for the security context; the
  jobs-created check after the permission change.
- [x] Phase 6: `scripts/ga-preflight.sh` on `v2`. *(Passed with
  `--skip-label` on the branch head; rerun on `v2` after merge.)*
- [x] Phase 8: the verification page's commands run verbatim against the
  published 2.0.0 artefacts.

<!--docz:testing:end-->

<!--docz:dependencies:start-->
## Dependencies

- DESIGN-0038 (PR #240) and IMPL-0033 (rc.17) merged before Phase 6.
- The soak (Phase 7) is the maintainer's and gates Phase 8.
- `jefflinse/pr-semver-bump@v1.7.4` behaviour as audited (tag parsing
  with `semver.parse(..., {loose: true})`, `base-branch` commit walk);
  re-verify against the pinned version's `version.js` before Phase 8 if
  Renovate bumps it.
- GitHub: the maintainer's admin rights for CodeQL, rulesets and the
  Pages settings.

<!--docz:dependencies:end-->

## Open Questions

**OQ1. Mermaid on the interim MkDocs site.**

- (a) ✅ recommended: **enable `pymdownx.superfences` with the mermaid
  fence in `mkdocs-ghpages.yml`**; it is four lines, Material renders
  mermaid natively with it, and the GA site stops showing 39 diagrams
  as code while IMPL-0035 is built.
- (b) leave it to Starlight; the GA site keeps today's rendering for a
  few more weeks.
- other:

**Decision (2026-10-25):** (b).

**OQ2. Where the strict docs build runs.**

- (a) ✅ recommended: **a new `docs` CI job** on a `docs` paths filter,
  installing the pinned `mkdocs-material` with pip; it later becomes
  IMPL-0035's Starlight build job under the same name and filter.
- (b) inside the existing `lint` job; fewer jobs, but `lint` would then
  run on docs-only pull requests and needs Python.
- other:

**Decision (2026-10-25):** (a).

**OQ3. The required status checks.**

- (a) ✅ recommended: **one aggregate `ci-ok` job** (needs every
  `ci.yml` job, `if: always()`, red on any failure or cancellation)
  plus `Check Required Labels`, `govulncheck` and `Check Dependency
  Licenses` from their own workflows; adding a CI job never needs a
  ruleset edit, and skipped (paths-filtered) jobs pass.
- (b) list every job name in the ruleset; explicit, but every new or
  renamed job silently stops being required until someone edits the
  ruleset.
- other:

**Decision (2026-10-25):** (a).

**OQ4. Who runs the GitHub-settings changes (CodeQL, rulesets, Pages).**

- (a) ✅ recommended: **the maintainer runs the `gh api` commands
  written in the task**, after reviewing the JSON in the task note;
  they are outward-facing, hard to review after the fact, and need
  admin rights.
- (b) the implementer runs them with the maintainer's explicit go-ahead
  per command; faster, same commands.
- other:

**Decision (2026-10-25):** (a).

**OQ5. The `v*` tag ruleset's rules.**

- (a) ✅ recommended: **`deletion` and `non_fast_forward` only**;
  existing release tags cannot be moved or deleted, and `release.yml`'s
  `GITHUB_TOKEN` can still create the next one. The ruleset has no
  bypass actors, like `main`'s.
- (b) also restrict `creation` with a bypass actor for the GitHub
  Actions integration; stops a maintainer's accidental hand tag, but
  the bypass must name the right integration id and breaks the
  pipeline if it drifts.
- other:

**Decision (2026-10-25):** (a).

**OQ6. The GA preflight.**

- (a) ✅ recommended: **`scripts/ga-preflight.sh`**, run by the
  maintainer on the M2 head and pasted into the PR; one file, no CI
  wiring, and the checks are release-time facts (tags, labels) CI
  cannot see on a pull request.
- (b) a `make ga-preflight` target wrapping the same script; one more
  Makefile entry for a once-per-major action.
- other:

**Decision (2026-10-25):** (a).

**OQ7. Where the 2.0.0 release notes live.**

- (a) ✅ recommended: **`docs/releases/2.0.0.md`**, linked from
  goreleaser's release header and from the README; the notes outlive
  the GitHub release page and render on the docs site.
- (b) only the GitHub release body (goreleaser `header`); nothing to
  keep in the tree, not on the docs site.
- other:

**Decision (2026-10-25):** (a).

<!--docz:references:start-->
## References

- [DESIGN-0038](../design/0038-v200-ga-cutover-and-identity-provider-portability.md) Parts A and D.
- [IMPL-0030](0030-controls-remediation-per-control-prs-remediation-workflow-and.md) Phases 9–10, OQ6–OQ8, OQ12.
- [IMPL-0033](0033-identity-provider-portability-for-the-api-and-ui-design-0038.md) (rc.17, before Phase 6).
- [IMPL-0035](0035-public-docs-site-on-astro-starlight-design-0038-part-c.md), [IMPL-0036](0036-supply-chain-hardening-design-0038-part-d-tiers-2-3.md) (after GA).
- IMPL-0029 8.9, 9.7, 9.8; IMPL-0031 8.3; IMPL-0032 9.2, 9.3 (origin boxes for Phase 7).
- `.github/workflows/*.yml`, `ct.yaml`, `cliff.toml`, `.goreleaser.yml`,
  `docker-bake.hcl`, `mise.toml`, `renovate.json5`,
  `.github/dependabot.yml`, `mkdocs.yml`, `mkdocs-ghpages.yml`,
  `charts/repo-guardian/values.yaml`, `templates/ui.yaml`.
- `jefflinse/pr-semver-bump` `version.js`; node-semver `inc`.
- CLAUDE.md § Release (the pipeline post-mortems), § CI paths-filter
  convention, § The v2 branch, rule 6.
- GitHub rulesets 12545694 (`main`), 24046649 (`v2`); Dependabot alerts
  #15, #16; issues #192, #199, #200, #213, #232, #235, #73.

<!--docz:references:end-->
