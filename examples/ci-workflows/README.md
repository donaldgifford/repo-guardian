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

This is the lossy swap. Both Level 2 and Level 3 produce signed
in-toto provenance attestations stating "this digest was built from
this source by this workflow." What changes is whether the build can
forge its own claims.

| Aspect | L2 | L3 |
|---|---|---|
| Provenance signed? | Yes | Yes |
| Build platform hosted? | Yes (e.g. GHA runners) | Yes |
| Who generates provenance? | The build job itself | A separately running, trusted workflow the build can't modify |
| Who holds the signing key? | The build's identity (OIDC token in the build job) | A control plane outside the build's reach |
| Can the build lie about itself? | **Yes** | **No** |

**Concretely in GitHub Actions:**

- **L2** (this example — `cosign attest` in the same job that built
  the image): the build job constructs the provenance JSON, signs it
  with its own OIDC identity, and pushes. Anything with RCE inside
  the build — a malicious `npm install`, a compromised base image, a
  goofy `Makefile` — can override what gets written. The signature is
  valid; the contents can be fiction. The verifier proves "someone
  with this repo's OIDC identity signed this attestation." It cannot
  prove the attestation describes reality.

- **L3** (`slsa-framework/slsa-github-generator`): the build job
  emits the image digest as an output. A separate reusable workflow
  in a different repo, signed by the SLSA project, takes that digest
  as input, queries GitHub's API for run metadata, generates the
  provenance, signs with its own ephemeral OIDC identity, attests.
  The build job has no way to tamper with the provenance generator's
  logic, environment, or signing key — they share only the digest
  string crossing the workflow boundary. The verifier proves "this
  digest was built by workflow X in repo Y at commit Z." High-
  confidence reality claim.

**Threat model L3 defends against that L2 doesn't:** an attacker who
compromises one of your build dependencies (typosquat, malicious PR
merge, supply-chain attack on a base image) but doesn't get any
further access. With L2 they can quietly emit "everything's fine"
provenance signed with your OIDC identity. With L3 they can't,
because the provenance step isn't running their code.

**When L2 is actually fine:**

- You're the only person who can push to the repo
- You audit the build steps yourself
- Consumers aren't running `slsa-verifier` in their install pipeline
- Compliance documents don't require L3 specifically

**When you need L3:**

- You're publishing artifacts that downstream consumers verify
  automatically against a specific provenance shape
- Your security review demands non-forgeable build claims
- You're publishing to ecosystems (e.g. distroless, Kubernetes core)
  where L3 is table stakes

If you need full L3 and cannot use the SLSA reusable workflow, the
real fix is to host an equivalent **trusted control plane** yourself
— a separately signed reusable workflow you control that takes a
digest as input and emits provenance from an isolated environment.
Out of scope for these examples; the manual `cosign attest` pattern
below is the L2 fallback.

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
