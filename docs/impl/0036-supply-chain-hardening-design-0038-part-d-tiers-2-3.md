---
id: IMPL-0036
title: "Supply-chain hardening (DESIGN-0038 Part D tiers 2-3)"
status: Draft
author: Donald Gifford
created: 2026-10-25
---

<!-- markdownlint-disable-file MD024 MD025 MD041 -->

# IMPL-0036: Supply-chain hardening (DESIGN-0038 Part D tiers 2-3)

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
  - [Phase 1: Action pinning](#phase-1-action-pinning)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Image, config and dependency scanning](#phase-2-image-config-and-dependency-scanning)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Workflow linting and Scorecard](#phase-3-workflow-linting-and-scorecard)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: Publish-path hygiene](#phase-4-publish-path-hygiene)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: Signed binaries](#phase-5-signed-binaries)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: Runtime and repository hardening](#phase-6-runtime-and-repository-hardening)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [References](#references)
<!-- markdownlint-restore -->
<!--toc:end-->

<!--docz:objective:start-->
## Objective

Finish the supply-chain work DESIGN-0038 Part D ranked below tier 1:
pin every action to a commit, scan the images and the chart, lint and
score the workflows, take secrets out of shell text and verify what we
sign, then sign the binaries with cosign and SLSA generic provenance,
add a NetworkPolicy, and revisit required signatures. Each phase is a
pull request that leaves the pipeline publishing exactly as before.

Workflow linting, security auditing and action pinning are one tool,
`jdx/jactionlint` (reviewed 2026-10-25): it carries actionlint's checks
(`correctness` profile), a rule per zizmor audit under the same IDs
(`default` profile = zizmor's `regular` persona), and `unpinned-uses`
whose `--online --fix` pins tags to commits with a version comment, so
`pinact`, `actionlint` and `zizmor` are not adopted separately.

**Implements:** [DESIGN-0038](../design/0038-v200-ga-cutover-and-identity-provider-portability.md)
Part D tiers 2 and 3 (D4), D9, OQ20. Follows IMPL-0034 Phase 5 (tier
1).

<!--docz:objective:end-->

<!--docz:scope:start-->
## Scope

<!--docz:in-scope:start-->
### In Scope

- SHA pinning of all external action references (`jactionlint --online
  --fix`) with Renovate keeping them current; the `slsa-github-generator`
  tag exception (tier 1) as an `unpinned-uses` policy.
- Trivy image and config scans (SARIF to code scanning), `bun audit` and
  a UI licence check, `jactionlint` (actionlint + zizmor coverage, SARIF),
  `dependency-review`, OpenSSF Scorecard.
- Verify-after-sign on every publish; registry credentials through
  `env:`/stdin only; secret-scanning settings review.
- Cosign keyless signatures and SLSA generic provenance for the
  goreleaser archives; GPG retirement plan.
- A chart `NetworkPolicy` template (default off), `harden-runner` in
  audit mode, Renovate `lockFileMaintenance`, the required-signatures
  decision.

<!--docz:in-scope:end-->

<!--docz:out-of-scope:start-->
### Out of Scope

- Tier 1 (IMPL-0034 Phase 5): SBOMs, CodeQL, rulesets, `@latest` pins,
  pod security context, `verifying-releases.md`, `SECURITY.md`.
- Changing the builders (goreleaser, bake) or the registries.
- Runtime code. A finding from a scanner that needs a Go or TypeScript
  fix is filed as an issue and fixed on its own.

<!--docz:out-of-scope:end-->
<!--docz:scope:end-->

## Pre-implementation audit

Verified 2026-10-25 (DESIGN-0038 § Background, D4):

- 6 of 111 external action references are SHA-pinned (the
  `sigstore/cosign-installer` uses with a `# v4.1.2` comment); the rest
  are tags. Renovate's shared presets set `pinDigests` for
  `github-actions` but nothing has been pinned because the manager only
  pins references it has already seen pinned once (`pinDigests` applies
  on update, not retroactively).
- `trivy` and `syft` are in `mise.toml` at `latest` (IMPL-0034 5.6 pins
  them) and unused by any workflow; `cosign` likewise local-only
  except through `cosign-installer`.
- No `actionlint`, `zizmor`, `dependency-review-action` or Scorecard
  workflow; `.github/actionlint.yml` exists (self-hosted labels `[]`) but
  nothing runs it.
- **`jdx/jactionlint` v2.3.0** (pushed 2026-10-25, 285 stars; a fork of
  actionlint by mise's author): reads actionlint's config; one rule per
  zizmor 1.30.1 audit with the same IDs and `# zizmor: ignore[...]`
  comments honoured; profiles `correctness` (actionlint + bug
  detectors), `default` (+ security and policy: `unpinned-uses`,
  `missing-permissions`, `missing-timeout`, `concurrency-limits`,
  `artipacked`, `template-injection`), `pedantic`; `--baseline-write`/
  `--baseline` for gradual adoption; `--fix` (safe) and `--fix=unsafe`;
  `--online` adds `impostor-commit`, `known-vulnerable-actions`,
  `ref-version-mismatch`, `archived-uses`, `repojacking`, and makes
  `unpinned-uses` fixable (tag → commit + `# vX.Y.Z` comment, most
  specific tag, annotated tags followed, branches and unknown tags left
  alone); checks `dependabot.yml` too; installs through mise with
  `mise.lock` checksum + provenance verification; the `jdx/jactionlint`
  action uploads SARIF with `advanced-security: true`; shellcheck is
  used when on `PATH` (present on `ubuntu-24.04`). The `unpinned-uses`
  rule takes zizmor-style `policies`, which is where the
  `slsa-github-generator` `ref-pin` exception goes. Risk: a young fork;
  the three-tool fallback is noted under Dependencies.
- `ghcr.yml` signs with `cosign sign --yes` and never runs `cosign
  verify` after; `helm registry login` takes `--password-stdin` in
  `ecr.yml` (piped from `aws ecr get-login-password`) and the GHCR login
  reads `secrets.GITHUB_TOKEN` through `docker/login-action`, so the
  remaining inlined secret is `ecr.yml:73`'s shell variable assignment
  and the `${{ secrets.ECR_REGION }}` interpolations in `run:` blocks.
- Repository security settings: Dependabot security updates enabled;
  secret scanning enabled with push protection; non-provider patterns
  and validity checks disabled.
- `.goreleaser.yml` signs `checksums.txt` with GPG only; no binary
  provenance. `slsa-framework/slsa-github-generator` ships a
  `generator_generic_slsa3.yml` reusable workflow that takes a
  `base64-subjects` input (sha256sum of the artefacts) and uploads
  `multiple.intoto.jsonl` to the release.
- The chart has no `NetworkPolicy` template; the role Deployments
  reach GitHub, Temporal, Postgres and the OIDC issuer (api) and are
  reached by ingress (ingest webhook, api, ui) and Prometheus
  (`/metrics`).
- `release.yml` runs `bump-version` on `ubuntu-latest`; no
  `step-security/harden-runner` anywhere.

## How the phases are ordered

Phase 1 first because every later workflow edit is then written against
pinned references and Renovate maintains them. Phases 2 and 3 add
scanners and are independent; both report before they gate. Phase 4
touches the publish workflows and must land before Phase 5 adds another
signing step there. Phase 5 is the tier-3 signing change. Phase 6
collects the remaining tier-3 items, each a small pull request.

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its
tasks are checked off and its success criteria are met.

---

<!--docz:phase:start-->
### Phase 1: Action pinning

DESIGN-0038 D4 tier 2 (pinning); IMPL-0036 OQ1.

<!--docz:tasks:start-->
#### Tasks

- [ ] 1.1 **Pin once.** `jactionlint` added to `mise.toml` (exact
  version, `# renovate: datasource=github-releases depName=jdx/jactionlint`)
  and `mise lock` run so `mise.lock` records its checksum.
  `.github/jactionlint.yaml` created with `rules.unpinned-uses.policies`
  mapping `slsa-framework/slsa-github-generator` to `ref-pin` (the
  CLAUDE.md reason in a comment) and `.github/actionlint.yml` folded
  into it. Then `jactionlint --online --select unpinned-uses --diff`
  reviewed and `--fix` applied (OQ1): every tag becomes
  `owner/repo@<sha> # vX.Y.Z`. Review the diff: the six existing pins
  unchanged; the SLSA generator references **left at `@v2.1.0`**;
  reusable-workflow `uses:` of our own files
  (`./.github/workflows/ghcr.yml`) untouched; anything jactionlint left
  with `online-required`/`needs-judgment` listed on this task and
  pinned by hand (`gh api repos/<o>/<r>/git/ref/tags/<tag>`).
- [ ] 1.2 **Renovate.** Confirm the shared preset's `github-actions`
  `pinDigests: true` and `helpers:pinGitHubActionDigests`
  (`renovate.json5` extends `github>donaldgifford/renovate-config:ci`;
  read the preset) will keep the `# vX.Y.Z` comment updated; add
  `"extends": ["helpers:pinGitHubActionDigestsToSemver"]` if the
  comment form is not already configured. The IMPL-0034 5.7 exception
  for the SLSA generator is in place.
- [ ] 1.3 **Verify the pipeline.** Run `ghcr.yml` by `workflow_dispatch`
  with `dry_run=true` for the current tag; every job runs on the pinned
  actions. The next Renovate run proposes digest updates with the
  version comment (check #213's dashboard).
- [ ] 1.4 **Guard.** `make lint-actions` runs `jactionlint --profile
  correctness` plus `--select unpinned-uses` (offline; both exit 1 on a
  finding) until Phase 3 widens it to the `default` profile; Phase 3's
  workflow job runs the same target in CI so a new `uses: foo/bar@v1`
  fails the pull request.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- `grep -rhoE 'uses: [^./][^@]+@[^ ]+' .github | grep -vE '@[0-9a-f]{40}'`
  returns only the `slsa-github-generator` references, and
  `jactionlint --select unpinned-uses` exits 0.
- A dispatched publish dry run is green on the pinned tree.
- Renovate's next digest PR carries `# vX.Y.Z` comments.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 2: Image, config and dependency scanning

DESIGN-0038 D4 tier 2 (scanning); IMPL-0036 OQ2, OQ4, OQ5.

<!--docz:tasks:start-->
#### Tasks

- [ ] 2.1 **Trivy image scan.** `security.yml`: a `trivy-image` job that
  builds the `release` and `ui-release` bake targets locally (no push,
  `load: true`, single platform) and runs `aquasecurity/trivy-action`
  (pinned) with `format: sarif`, `severity: HIGH,CRITICAL`,
  `ignore-unfixed: true`, uploading to code scanning under category
  `trivy-image-<target>`. Per OQ2 the job **reports** for two weeks
  (`exit-code: 0`) and then flips to `exit-code: 1` in a follow-up
  commit noted on this task.
- [ ] 2.2 **Trivy config scan.** The same job runs `trivy config` over
  `charts/repo-guardian` (`helm template` output with default values and
  `ci-values.yaml`) and `Dockerfile`/`ui/Dockerfile`, SARIF category
  `trivy-config`; the IMPL-0034 5.8 hardening should leave it clean,
  and any remaining finding is either fixed or `.trivyignore`d with a
  reason line.
- [ ] 2.3 **`bun audit`.** `ci.yml` `ui` job gains `bun audit
  --audit-level=high` after install (reports to the log; OQ2's window
  applies, then fails on high); `site/` gets the same in the `docs` job
  once IMPL-0035 lands.
- [ ] 2.4 **UI licences.** `license-check.yml` gains a `ui` job running
  `bunx license-checker-rseidelsohn --production --onlyAllow
  "<allowlist>"` (OQ5) over `ui/`; the allowlist matches the Go job's
  (`MIT;Apache-2.0;BSD-2-Clause;BSD-3-Clause;ISC;0BSD;MPL-2.0`) plus
  `CC0-1.0`/`Unlicense`; the first run's exceptions are recorded in
  `ui/.license-allowlist.json` with a reason each.
- [ ] 2.5 **Dependency review.** `.github/workflows/dependency-review.yml`
  on `pull_request`: `actions/dependency-review-action` (pinned) with
  `fail-on-severity: high`, `deny-licenses` mirroring the allowlists'
  complement (OQ4), `comment-summary-in-pr: on-failure`; required via
  `ci-ok`'s sibling list (IMPL-0034 5.4) once it has run green on three
  pull requests.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- Code scanning shows `CodeQL`, `govulncheck`, `trivy-image-release`,
  `trivy-image-ui-release` and `trivy-config` tools with results from
  `main`.
- A pull request adding a dependency with a GPL licence or a known
  high-severity advisory is blocked by `dependency-review`.
- `bun audit` and the UI licence check run on every UI pull request;
  after OQ2's window both fail on a high finding.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 3: Workflow linting and Scorecard

DESIGN-0038 D4 tier 2 (workflow hygiene); IMPL-0036 OQ3, OQ6. One
linter, `jactionlint`, in place of `actionlint` + `zizmor` + `pinact`.

<!--docz:tasks:start-->
#### Tasks

- [ ] 3.1 **Correctness first.** `jactionlint --profile correctness`
  (actionlint's checks + shellcheck on `run:` blocks, `shellcheck`
  pinned in `mise.toml` for laptops) over `.github/`; fix every finding
  (these are mistakes, not debt). `make lint-actions` becomes
  `jactionlint` at the `default` profile with `--baseline` (3.2);
  `ci.yml` gets a `workflows` job on the existing `workflows` paths
  filter: `jdx/mise-action` (jactionlint from `mise.lock`), `make
  lint-actions`, and `jactionlint --format sarif` uploaded with
  `github/codeql-action/upload-sarif` (tool `jactionlint`) so findings
  land in code scanning beside CodeQL and trivy.
- [ ] 3.2 **Default profile.** Run `jactionlint` (the `default` profile
  = zizmor's `regular` persona, OQ3) and triage: `template-injection`
  findings take the safe `--fix` (the expression moves to `env:`, the
  CLAUDE.md bake-metadata rule generalised) and the `needs-judgment`
  ones are edited by hand; `missing-permissions`/
  `excessive-permissions` are covered by IMPL-0034 5.11's job-level
  blocks; `artipacked` takes the safe fix (`persist-credentials: false`)
  on every `actions/checkout` that does not push (every one but the
  `changelog-update` and `bump-version` checkouts); `missing-timeout`
  gets `rules.missing-timeout.default-minutes: 30` in
  `.github/jactionlint.yaml` and the fix applied; `concurrency-limits`
  is satisfied by IMPL-0034 5.11 on the publish workflows and added to
  `ci.yml` (`cancel-in-progress: true` on pull requests). What is left
  goes into `jactionlint --baseline-write` so the build fails only on
  new findings, and the baseline file is burned down to empty within
  this phase (list the entries on this task).
- [ ] 3.3 **Online checks, scheduled.** A weekly `jactionlint --online`
  run in the `workflows` job (`schedule` trigger, `JACTIONLINT_ONLINE`
  with `GITHUB_TOKEN`, `contents: read`) for `impostor-commit`,
  `known-vulnerable-actions`, `ref-version-mismatch`, `archived-uses`,
  `repojacking`; findings annotate the run and upload as SARIF. Not on
  pull requests (network, rate limits).
- [ ] 3.4 **Scorecard.** `.github/workflows/scorecard.yml`:
  `ossf/scorecard-action` (pinned) on `push` to `main` and weekly, with
  `publish_results: true` (OQ6), SARIF to code scanning. Record the
  first score and the per-check breakdown on this task; the README gets
  the badge. Checks expected to lag: `Branch-Protection` (no required
  reviews; single maintainer), `Signed-Releases` (until Phase 5),
  `Token-Permissions` (fixed by 5.11 + 3.2), `Pinned-Dependencies`
  (Dockerfiles: pin `apk`/`apt` packages or mark them).
- [ ] 3.5 **Dockerfile pins.** For `Pinned-Dependencies`: the Go
  `Dockerfile` and `ui/Dockerfile` `FROM` lines pinned to digests with a
  `# renovate:` comment (Renovate's docker manager updates them); any
  package installs pinned or removed.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- `make lint-actions` (`jactionlint`, `default` profile, empty
  baseline) passes locally and in the `workflows` CI job; introducing
  `uses: actions/checkout@v4` or `run: echo ${{
  github.event.pull_request.title }}` fails it (verified once each).
- Code scanning shows the `jactionlint` tool; the weekly online run
  reports no `impostor-commit` or `known-vulnerable-actions`.
- Scorecard publishes a result for `main`; `Token-Permissions` and
  `Pinned-Dependencies` are at 10 after 3.2–3.5.
- Every `actions/checkout` outside the two pushing workflows has
  `persist-credentials: false`.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 4: Publish-path hygiene

DESIGN-0038 D4 tier 2 (verify-after-sign, secrets via env).

<!--docz:tasks:start-->
#### Tasks

- [ ] 4.1 **Verify after sign.** In `ghcr.yml` and `ecr.yml`, directly
  after each `cosign sign`: `cosign verify --certificate-identity
  "https://github.com/${GITHUB_REPOSITORY}/.github/workflows/<file>@${GITHUB_REF}"
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
  <ref@digest>` for the image, the UI image and the chart; after each
  SLSA job, `slsa-verifier verify-image <ref@digest> --source-uri
  github.com/donaldgifford/repo-guardian --source-tag <tag>` in a
  `verify` job that needs both the sign and SLSA jobs. The published
  artefact is unchanged; a verify failure fails the run loudly, which is
  what the v1.8.2/v1.11.0 post-mortems lacked.
- [ ] 4.2 **Secrets through env.** Every `run:` block that interpolates
  `${{ secrets.* }}` or a token output moves it to `env:` and reads the
  variable (`ecr.yml` token step, `helm registry login` password via
  `--password-stdin <<< "$TOKEN"`, any `gh` call); `jactionlint`'s
  `template-injection` and shellcheck confirm none remain. The
  `::add-mask::` on the ECR token stays.
- [ ] 4.3 **Secret scanning settings.** `deferred - human required`:
  enable `secret_scanning_non_provider_patterns` and
  `secret_scanning_validity_checks` (`gh api -X PATCH repos/... -f
  'security_and_analysis[secret_scanning_non_provider_patterns][status]=enabled'
  ...`); push protection is already on. Record the result.
- [ ] 4.4 **Diff the twins.** `ghcr.yml` and `ecr.yml` are re-diffed
  step by step after 4.1/4.2 (the CLAUDE.md "copy-paste divergence"
  rule) and the remaining intentional differences listed in a comment
  at the top of `ecr.yml`.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- A dispatched `ghcr.yml` dry run shows the verify steps passing for
  all three artefacts; breaking the identity regexp once fails the
  step.
- `grep -n 'secrets\.' .github/workflows/*.yml` matches only `env:`,
  `with:` and `secrets:` lines, never a `run:` body.
- The two registry workflows differ only in the lines the `ecr.yml`
  header comment lists.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 5: Signed binaries

DESIGN-0038 D4 tier 3 (binaries), D9, OQ20 (decided (a)); IMPL-0036
OQ7.

<!--docz:tasks:start-->
#### Tasks

- [ ] 5.1 **Cosign in goreleaser.** `.goreleaser.yml` `signs:` gains a
  keyless cosign entry over `artifacts: checksum` (`cosign sign-blob
  --yes --output-signature ${signature} --output-certificate
  ${certificate} ${artifact}`, `.sig` + `.pem` uploaded); the GPG entry
  stays for one release (OQ7). `release.yml`'s `release` job gets
  `id-token: write` and `cosign-installer` (pinned).
- [ ] 5.2 **SLSA generic provenance.** `release.yml`: after `release`, a
  `provenance` job calling
  `slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@v2.1.0`
  (tag, per the CLAUDE.md rule) with `base64-subjects` from
  `checksums.txt` (`base64 -w0 < dist/checksums.txt`, exposed as a job
  output by `release`), `upload-assets: true`, `upload-tag-name: <tag>`;
  the caller's `permissions` ceiling grants `actions: read, id-token:
  write, contents: write` (the June post-mortem rule). The first
  push-triggered run after this change must **create jobs**.
- [ ] 5.3 **Verify in the workflow.** A `verify-release` job downloads
  the release assets and runs `slsa-verifier verify-artifact
  --provenance-path multiple.intoto.jsonl --source-uri
  github.com/donaldgifford/repo-guardian --source-tag <tag>
  <archives>` and `cosign verify-blob --certificate checksums.txt.pem
  --signature checksums.txt.sig --certificate-identity-regexp ...
  checksums.txt`.
- [ ] 5.4 **Docs.** `docs/operations/verifying-releases.md` binaries
  section: the cosign and slsa-verifier commands replace the GPG
  commands as primary; GPG listed as "also available until <version>"
  with the retirement date from OQ7. `SECURITY.md`'s controls list
  updated.
- [ ] 5.5 **Dry run.** `make release-snapshot` locally shows the cosign
  signing step (keyless needs an OIDC token: use `COSIGN_EXPERIMENTAL`'s
  interactive flow once, or skip signing in snapshot with `--skip=sign`
  and rely on the next rc tag's `prerelease.yml` run as the test;
  `prerelease.yml` gets the same `signs:` and provenance job).
- [ ] 5.6 **Retire GPG.** One release after 5.1 ships (OQ7): remove the
  GPG `signs:` entry, the `GPG_PRIVATE_KEY`/`PASSPHRASE` secrets'
  consumers, the `crazy-max/ghaction-import-gpg` step; the public key
  stays published for verifying older releases; `deferred - human
  required` for deleting the repository secrets.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- The next GA release carries `checksums.txt.sig`, `checksums.txt.pem`,
  `multiple.intoto.jsonl`; `verify-release` passes in the workflow and
  the `verifying-releases.md` commands pass on a laptop.
- Scorecard `Signed-Releases` is at 10.
- After 5.6, `grep -rn 'gpg\|GPG' .github .goreleaser.yml` returns only
  the verifying-releases historical note's pointer.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 6: Runtime and repository hardening

DESIGN-0038 D4 tier 3 (the rest); IMPL-0036 OQ8, OQ9, OQ10.

<!--docz:tasks:start-->
#### Tasks

- [ ] 6.1 **NetworkPolicy.** `templates/networkpolicy.yaml` behind
  `networkPolicy.enabled` (default `false`, OQ8): one policy per role.
  Ingress: ingest from `networkPolicy.ingress.webhookFrom` (namespace/
  pod selectors; empty = any), api and ui from `ingress.apiFrom`, every
  role's `/metrics` port from `networkPolicy.ingress.metricsFrom`
  (default the `monitoring` namespace selector); the baked Postgres and
  Valkey StatefulSets only from the roles. Egress: DNS; `egress.github`
  (TCP 443, CIDR list or `0.0.0.0/0` default with a comment); Temporal
  (`temporal.address` host:port → a `namespaceSelector`/`podSelector`
  under `egress.temporal` or a CIDR); Postgres (baked: pod selector;
  cnpg: the cluster's pods; external: `egress.postgres` CIDR); the OIDC
  issuer for api/ui (`egress.oidc`). helm-unittest renders each role's
  policy with the selectors; `ci-values.yaml` enables it so `helm-test`
  proves the pods still come up under kind (which enforces nothing, so
  also a `kubectl exec` curl smoke in the `helm-test` job is **not**
  attempted; the policy's correctness is reviewed, and the homelab
  (Cilium) is the enforcement test, `deferred - human required`).
- [ ] 6.2 **`harden-runner`.** `step-security/harden-runner` (pinned) as
  the first step of every job in `release.yml`, `ghcr.yml`, `ecr.yml`,
  `prerelease.yml` with `egress-policy: audit` (OQ9); after two
  releases, read the reported endpoints and switch the publish jobs to
  `block` with the `allowed-endpoints` list in a follow-up noted here.
- [ ] 6.3 **Lock-file maintenance.** `renovate.json5`
  `lockFileMaintenance: {enabled: true, schedule: ["before 6am on
  monday"]}` so `go.sum`, `ui/bun.lock` and `site/bun.lock` are refreshed
  weekly even when no direct dependency moved.
- [ ] 6.4 **Required signatures.** Decide per OQ10 after Phase 1–5:
  if Renovate is configured for `platformCommit: enabled` (GitHub
  App-signed commits) and Dependabot's commits are verified, add
  `required_signatures` to the `main` ruleset via `gh api`
  (`deferred - human required`); otherwise record the reason to wait in
  DESIGN-0038 D4 and close the item.
- [ ] 6.5 **Close out.** Scorecard re-read and recorded; `SECURITY.md`'s
  controls list final; IMPL-0036 → Completed; DESIGN-0038 → Implemented
  once IMPL-0033/0034/0035 are also complete.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- `helm template --set networkPolicy.enabled=true` renders one policy
  per rendered role plus the baked stores, and helm-unittest pins the
  selectors; default values render none.
- Every publish job starts with `harden-runner`; the audit report
  lists only expected endpoints (GitHub, the registries, Sigstore, AWS
  when ECR is on).
- Renovate opens a lock-file maintenance PR in the first week.
- The required-signatures decision is recorded in DESIGN-0038 with its
  date.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:file-changes:start-->
## File Changes

| File | Action | Description |
| ---- | ------ | ----------- |
| `.github/workflows/*.yml`, `.github/actions/**` | Modify | SHA pins; `persist-credentials: false`; secrets via env; verify steps; harden-runner |
| `.github/jactionlint.yaml` (replaces `.github/actionlint.yml`), `.github/jactionlint-baseline.json` (burned down to empty), `mise.toml` + `mise.lock` (`jactionlint`, `shellcheck`, `slsa-verifier`) | Create/Modify | lint tooling |
| `Makefile` (`lint-actions`) | Modify | local parity |
| `.github/workflows/security.yml` (`trivy-image`, `trivy-config`), `.trivyignore` | Modify/Create | scanning |
| `.github/workflows/ci.yml` (`workflows` job: jactionlint + SARIF + weekly `--online`, `bun audit`, `concurrency`), `license-check.yml` (`ui` job), `ui/.license-allowlist.json` | Modify/Create | workflow lint and UI dependency checks |
| `.github/workflows/dependency-review.yml`, `scorecard.yml` | Create | tier 2 |
| `Dockerfile`, `ui/Dockerfile` | Modify | digest-pinned `FROM` |
| `.goreleaser.yml`, `.github/workflows/release.yml`, `prerelease.yml` | Modify | cosign signs, SLSA generic provenance, verify-release |
| `docs/operations/verifying-releases.md`, `SECURITY.md`, `README.md` (badge) | Modify | binaries verification; Scorecard |
| `charts/repo-guardian/templates/networkpolicy.yaml`, `values.yaml`, `values.schema.json`, `tests/networkpolicy_test.yaml`, `ci-values.yaml`, `README.md.gotmpl` | Create/Modify | NetworkPolicy |
| `renovate.json5` | Modify | digest comment preset, lockFileMaintenance, platformCommit |

<!--docz:file-changes:end-->

<!--docz:testing:start-->
## Testing Plan

- [ ] Phase 1: dispatched publish dry run on pinned actions; the
  `jactionlint --select unpinned-uses` negative case.
- [ ] Phase 2: code-scanning tool list; a deliberately bad dependency on
  a throwaway pull request blocked by `dependency-review`.
- [ ] Phase 3: `make lint-actions` negative cases (unpinned `uses:`,
  template injection); Scorecard result recorded.
- [ ] Phase 4: verify steps pass; one broken-identity negative run;
  `grep` for `secrets.` in `run:` bodies.
- [ ] Phase 5: the rc that follows 5.1 has `.sig`/`.pem`/`.intoto.jsonl`
  and `verify-release` is green; laptop verification per the docs page.
- [ ] Phase 6: helm-unittest for the policy; harden-runner audit
  reports; the Renovate lock-file PR.

<!--docz:testing:end-->

<!--docz:dependencies:start-->
## Dependencies

- IMPL-0034 Phase 5 merged (the SLSA Renovate exception, `ci-ok`, pinned
  `mise.toml`, job-level permissions, `verifying-releases.md` exist).
- IMPL-0035 for the `site/` `bun audit` line (2.3) only.
- `donaldgifford/renovate-config` presets: read the `ci` preset for the
  digest-pinning shape before 1.2; a change there is a separate PR in
  that repository.
- The maintainer for the repository settings (4.3, 6.4) and for the
  homelab NetworkPolicy enforcement test (6.1).
- `jdx/jactionlint` staying maintained. It is a young fork (v2.3.0,
  active daily as of 2026-10-25). If it stalls, the fallback is the
  three tools it replaces (`actionlint`, `zizmor --persona regular`,
  `pinact run --check`), each a mise pin and a line in `lint-actions`;
  the `# zizmor: ignore[...]` comment form keeps both directions
  portable.

<!--docz:dependencies:end-->

## Open Questions

**OQ1. How to get to SHA-pinned actions.**

- (a) ✅ recommended: **`pinact run` once, Renovate maintains**; one
  reviewable diff pins all 105 remaining references with version
  comments, and Renovate's digest updates keep them current with the
  comment form.
- (b) let Renovate pin them over time; it only pins references it is
  updating, so most would stay unpinned for months.
- (c) Dependabot `github-actions` version updates re-enabled with
  pinning; Dependabot pins on its first update too, and we just made it
  security-only to stop the duplicate PRs.
- other:

**Decision (2026-10-25):** (a). Tool: `jactionlint --online --fix` on `unpinned-uses` in place of `pinact run`; same shape, one diff, Renovate maintains.

**OQ2. When the trivy image scan and `bun audit` start failing the run.**

- (a) ✅ recommended: **report-only for two weeks, then fail on
  HIGH/CRITICAL with `--ignore-unfixed`**; the window shows the
  baseline and fixes without blocking the first merges, and unfixed
  upstream CVEs in distroless cannot be acted on anyway.
- (b) fail from the first run; cleaner signal, but the first green
  requires every current finding resolved or ignored in one PR.
- (c) report-only indefinitely; it is then a dashboard nobody reads.
- other:

**Decision (2026-10-25):** (a).

**OQ3. The `zizmor` persona.**

- (a) ✅ recommended: **`regular`** (the default); `auditor` adds
  low-confidence findings (every `uses:` without a pin even when the
  pin is ours, unpinned `runs-on`) that `pinact` and Phase 1 already
  cover.
- (b) `auditor` from the start, with findings ignored inline; more
  noise, marginal gain.
- (c) `pedantic`; not for CI gating.
- other:

**Decision (2026-10-25):** (a). In jactionlint terms: the `default` profile, which is zizmor's `regular` persona; `pedantic` covers `auditor`/`pedantic`.

**OQ4. `dependency-review` failure threshold and licences.**

- (a) ✅ recommended: **`fail-on-severity: high` and `deny-licenses`
  for the copyleft set** (`GPL-2.0`, `GPL-3.0`, `AGPL-3.0`, `LGPL-*`,
  `SSPL-1.0`, `EUPL-*`); matches `license-check.yml`'s allowlist by
  complement and blocks the two things a dependency bump can bring in
  that a scanner on `main` would only report after the merge.
- (b) `fail-on-severity: moderate`; stricter, more Renovate PRs blocked
  by advisories with no fix.
- (c) severity only, no licence rule; licences then rely on the
  post-merge `license-check.yml`.
- other:

**Decision (2026-10-25):** (a).

**OQ5. The UI licence tool.**

- (a) ✅ recommended: **`license-checker-rseidelsohn`** (the maintained
  fork of `license-checker`, MIT) run with `bunx` at a pinned version;
  reads `node_modules`, supports `--onlyAllow`, JSON output for the
  exceptions file.
- (b) `licensee` or `fossa` SaaS; heavier, needs tokens.
- (c) `bun pm licenses` if bun ships it by then; check at
  implementation time and prefer it if it exists with an allowlist.
- other:

**Decision (2026-10-25):** (a).

**OQ6. Scorecard publishing.**

- (a) ✅ recommended: **`publish_results: true` and the README badge**;
  public repository, public score, and the badge is the point.
- (b) run privately (SARIF only); keeps a low early score off the
  README, but there is nothing to hide and the score lifts quickly.
- other:

**Decision (2026-10-25):** (a).

**OQ7. GPG retirement timing.**

- (a) ✅ recommended: **keep GPG for one release after cosign ships**,
  so one release carries both and the verification page can show both
  passing; then remove it (5.6).
- (b) remove GPG in the same release cosign appears; cleaner, no
  overlap release for operators scripting `gpg --verify`.
- (c) keep both indefinitely; two signing paths to maintain.
- other:

**Decision (2026-10-25):** (a).

**OQ8. NetworkPolicy shape.**

- (a) ✅ recommended: **a chart template, default off, one policy per
  role**, selectors from values with permissive egress defaults for
  GitHub (any 443) and explicit selectors for Temporal, Postgres, OIDC
  and metrics; operators turn it on where their CNI enforces.
- (b) default on; breaks every install whose Temporal or Postgres is
  outside the selectors the defaults guess.
- (c) ship an example in `contrib/` only; nothing templated, nothing
  tested.
- other:

**Decision (2026-10-25):** (a).

**OQ9. `harden-runner` mode.**

- (a) ✅ recommended: **`audit` on the publish workflows first, `block`
  on them after two releases** with the observed endpoint list; the
  publish jobs are the ones with credentials, and audit shows what to
  allow before blocking something cosign or SLSA needs.
- (b) `block` immediately with a guessed list; a missed Sigstore or
  registry endpoint breaks a release.
- (c) audit on every workflow including CI; more reports, little extra
  protection where there are no secrets.
- other:

**Decision (2026-10-25):** (a).

**OQ10. Required signatures on `main`.**

- (a) ✅ recommended: **decide at Phase 6 with the evidence**: enable
  only when Renovate (`platformCommit`) and Dependabot both produce
  verified commits and the maintainer's signing key is set up on every
  machine; otherwise record "not yet" in DESIGN-0038 D4 and revisit.
- (b) enable now; unverified bot commits then block merges until each
  bot is reconfigured.
- (c) never; signing is already policy by habit (`git commit -S`), but
  a ruleset is what makes it a control.
- other:

**Decision (2026-10-25):** (a).

<!--docz:references:start-->
## References

- [DESIGN-0038](../design/0038-v200-ga-cutover-and-identity-provider-portability.md) Part D, D4, D9, OQ16–OQ20.
- [IMPL-0034](0034-v200-ga-cutover-with-security-tier-1-design-0038-part-a.md) Phase 5 (tier 1).
- [IMPL-0035](0035-public-docs-site-on-astro-starlight-design-0038-part-c.md) (the `site/` audit line).
- `.github/workflows/{release,prerelease,ghcr,ecr,ci,security,license-check}.yml`,
  `.goreleaser.yml`, `docker-bake.hcl`, `Dockerfile`, `ui/Dockerfile`,
  `mise.toml`, `renovate.json5`, `.github/actionlint.yml`,
  `charts/repo-guardian/templates/`.
- CLAUDE.md § Release: the nested-permission ceilings, bake-metadata
  and wrong-step-id post-mortems; the `slsa-github-generator` tag rule;
  "diff the twins".
- `jdx/jactionlint` (README, `docs/fix.md`, `docs/online.md`,
  `docs/ci.md`, `docs/zizmor-parity.md`, read 2026-10-25),
  `aquasecurity/trivy-action`, `actions/dependency-review-action`,
  `ossf/scorecard-action`, `step-security/harden-runner`,
  `slsa-framework/slsa-github-generator`
  (`generator_generic_slsa3.yml`), `slsa-verifier`, cosign `sign-blob`.
- GitHub repository security settings (`security_and_analysis`) as read
  2026-10-25; issue #213 (Renovate dashboard).

<!--docz:references:end-->
