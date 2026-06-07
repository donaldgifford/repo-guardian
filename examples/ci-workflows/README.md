# CI workflow examples — mise-manual tool install

The production workflows under `.github/workflows/` use a handful of
third-party actions to manage the tooling chain:

| Action | Purpose |
|---|---|
| `jdx/mise-action` | Install pinned tools from `mise.toml` |
| `sigstore/cosign-installer@v3` | Install cosign |
| `orhun/git-cliff-action@v4` | Run git-cliff to regenerate the chart CHANGELOG |
| `slsa-framework/slsa-github-generator/.github/workflows/generator_container_slsa3.yml@v2.0.0` | Generate + sign SLSA Level 3 provenance |

These examples show alternatives that depend **only** on `actions/checkout`,
`docker/login-action`, `docker/setup-{qemu,buildx}-action`, and
`docker/bake-action` — every other tool is installed manually via the
`mise.run` install script, then driven from `mise.toml`'s pinned versions.

Use these when an organization-level policy restricts which third-party
actions can run, or when you want every tool version reproducible from a
single `mise.toml` source of truth.

## What changed vs. the production workflows

### mise install (everywhere)

Where the production workflow would do `uses: jdx/mise-action@v2`, the
example does:

```yaml
- name: Install mise
  run: |
    set -eo pipefail
    curl https://mise.run | sh
    {
      echo "$HOME/.local/bin"
      echo "$HOME/.local/share/mise/shims"
    } >> "$GITHUB_PATH"

- name: Install pinned tools
  run: mise install
```

After this, every binary pinned in `mise.toml` is on `PATH` via the
shim directory — including `cosign`, `git-cliff`, `helm`, `yq`,
`jq`, etc. No further per-tool install steps needed.

### cosign

Production:

```yaml
- uses: sigstore/cosign-installer@v3
```

Example: nothing. `cosign` is pinned in `mise.toml` and was installed
by the `mise install` step above. Call `cosign sign ...` directly.

### git-cliff

Production:

```yaml
- uses: orhun/git-cliff-action@v4
  with:
    config: charts/repo-guardian/cliff.toml
    args: --include-path "charts/**" --output charts/repo-guardian/CHANGELOG.md
```

Example:

```yaml
- name: Regenerate chart CHANGELOG via git-cliff
  run: |
    git-cliff \
      --config charts/repo-guardian/cliff.toml \
      --include-path 'charts/**' \
      --output charts/repo-guardian/CHANGELOG.md
```

### SLSA provenance

This is the lossy swap. The `slsa-framework/slsa-github-generator`
reusable workflow runs on a trusted reusable workflow runner and
produces a SLSA Level 3 provenance attestation — the L3 guarantee is
that the build environment is isolated from the provenance generator,
which the reusable workflow gets by construction.

The manual replacement builds a SLSA v1.0 provenance JSON in-line
from `github.*` context vars and signs it with `cosign attest`. This
gets a signed in-toto attestation attached to the image in the
registry, but the build and the attestation both run in the same job
on the same runner, so the L3 isolation property is lost. Effectively
this is **SLSA Level 2** (signed provenance, but build and signer are
the same workload).

If you need full SLSA L3 and cannot use the reusable workflow, the
real fix is to run the provenance step on a separate isolated runner
or in a separate signed reusable workflow you control — out of scope
for these examples.

The example pattern:

```yaml
- name: Generate SLSA provenance
  run: |
    cat > provenance.json <<EOF
    {
      "_type": "https://in-toto.io/Statement/v1",
      "subject": [
        {
          "name": "${REGISTRY}/${IMAGE_REPO}",
          "digest": { "sha256": "${DIGEST_HEX}" }
        }
      ],
      "predicateType": "https://slsa.dev/provenance/v1",
      "predicate": {
        "buildDefinition": {
          "buildType": "https://github.com/Attestations/GitHubActionsWorkflow@v1",
          "externalParameters": {
            "workflow": {
              "ref": "${GITHUB_REF}",
              "repository": "https://github.com/${GITHUB_REPOSITORY}",
              "path": "${GITHUB_WORKFLOW_REF}"
            }
          },
          "resolvedDependencies": [
            {
              "uri": "git+https://github.com/${GITHUB_REPOSITORY}@${GITHUB_REF}",
              "digest": { "sha1": "${GITHUB_SHA}" }
            }
          ]
        },
        "runDetails": {
          "builder": {
            "id": "https://github.com/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}"
          },
          "metadata": {
            "invocationId": "${GITHUB_RUN_ID}",
            "startedOn": "${BUILD_STARTED_AT}"
          }
        }
      }
    }
    EOF

- name: Sign + attest
  run: |
    cosign attest --yes \
      --predicate provenance.json \
      --type slsaprovenance1 \
      "${REGISTRY}/${IMAGE_REPO}@sha256:${DIGEST_HEX}"
```

Note that `DIGEST_HEX` is the digest **without** the `sha256:` prefix
(the JSON schema requires the hex value alone). `cosign attest` writes
the attestation as a sibling OCI artifact in the same registry as the
image.

## Files

- `ghcr.yml` — image + chart publish to GHCR
- `ecr.yml` — image + chart publish to ECR (OIDC auth)

The orchestrator (`release.yml`) doesn't use any of the swapped
third-party actions, so its production version is reusable as-is —
just point `uses: ./.github/workflows/<file>.yml` at whichever copy of
`ghcr.yml`/`ecr.yml` you ship.

The files here are illustrative — copy the patterns you need, don't
drop them in `.github/workflows/` verbatim or you'll have duplicate
jobs running against the same registry.
