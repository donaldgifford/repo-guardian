# Examples

Example configuration files for repo-guardian.

## Controls policy roots

Each directory under [`policy/`](policy/) is a whole policy root in the
controls model (DESIGN-0030): `enterprise.hcl`, a `catalogue/` of
control definitions, optional `orgs/<org>.hcl` files and optional
`templates/`. `examples_test.go` loads each through the strict loader
with every built-in control type and pins its summary in
`policy/testdata/`.

| Root | Covers |
|------|--------|
| [`policy/minimal/`](policy/minimal/) | CODEOWNERS and Dependabot on every repository of one org. |
| [`policy/renovate/`](policy/renovate/) | Renovate extending the org preset, the exact Renovate workflow, no Dependabot left behind. |
| [`policy/full/`](policy/full/) | Every built-in type once: catalog-info, custom properties (`direct`), labels, repository settings (`recommend`), a default-branch ruleset, with org-level repository exclusions and a `repos` carve-out. |
| [`policy/multi-org/`](policy/multi-org/) | A shared baseline with per-org additions and an evaluate-only org. |
| [`policy/enterprise/`](policy/enterprise/) | An enterprise list larger than the orgs with files, evaluate by default with org opt-in, `replace` and `exclude` with reasons, a policy-root template. |

### Usage

`GUARDIAN_CONFIG` names the policy root directory. Evaluate one
repository against a root without a store (as the Evaluation App):

```bash
repo-guardian evaluate --repo acme/api --policy examples/policy/full
```

**With the Helm chart (inline):** `policy.files` is the root as a nested
map by directory: a string is a file, a map is a directory. The chart
mounts it at `/etc/repo-guardian/policy` with every file at its path.

```yaml
policy:
  files:
    enterprise.hcl: |
      enterprise {
        orgs = ["acme"]
      }
    catalogue:
      codeowners.hcl: |
        control "codeowners" {
          # ...
        }
```

**With the Helm chart (external ConfigMap):** the ConfigMap's items must
put each file back at its path, so prefer `policy.files` unless you
generate the ConfigMap with matching `items`.

```yaml
policy:
  existingConfigMap: guardian-policy
```

## Helm Values

Each values file's `policy.files` mirrors a policy root byte for byte;
`examples_test.go` fails if the two drift.

| File | Policy root |
|------|-------------|
| [`values-with-policy.yaml`](values-with-policy.yaml) | [`policy/full/`](policy/full/) — one org, every built-in control type. |
| [`values-multi-org.yaml`](values-multi-org.yaml) | [`policy/multi-org/`](policy/multi-org/) — a shared baseline across orgs with per-org additions. |

## Other

| File | Description |
|------|-------------|
| [`catalog-info.yaml`](catalog-info.yaml) | Example Backstage catalog-info.yaml for repo-guardian itself. |
