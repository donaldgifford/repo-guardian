# Examples

Example configuration files for repo-guardian.

## HCL Policy Files

repo-guardian uses an optional HCL policy file to define compliance rules.
Point to it with the `GUARDIAN_CONFIG` environment variable, or inline it in
your Helm values under `policy.config`.

| File | Description |
|------|-------------|
| [`guardian-minimal.hcl`](guardian-minimal.hcl) | Mirrors the built-in defaults (CODEOWNERS + Dependabot). Good starting point. |
| [`guardian-renovate.hcl`](guardian-renovate.hcl) | Adds Renovate workflow and config management on top of the defaults. |
| [`guardian-full.hcl`](guardian-full.hcl) | Kitchen sink — all rule types, assertions, reconcilers, ignore lists, settings, branch protection. |
| [`guardian-multi-org.hcl`](guardian-multi-org.hcl) | Strict-mode multi-org example in a single file. Top-level `scope { }` + per-rule scopes (universal `["*"]` and per-org subsets). |
| [`guardian-multi-org/`](guardian-multi-org/) | Same as above, split across `scope.hcl`, `shared.hcl`, `prod-only.hcl`, `staging-only.hcl`. Point `GUARDIAN_CONFIG` at the directory. |

### Usage

**Standalone:**

```bash
export GUARDIAN_CONFIG=path/to/guardian.hcl
make run-local
```

**With Docker:**

```bash
docker run -v $(pwd)/guardian.hcl:/etc/repo-guardian/policy/guardian.hcl \
  -e GUARDIAN_CONFIG=/etc/repo-guardian/policy/guardian.hcl \
  repo-guardian:latest
```

**With Helm chart (inline):**

```yaml
# values.yaml
policy:
  config: |
    rule "file" "codeowners" {
      # ...
    }
```

**With Helm chart (external ConfigMap):**

```bash
kubectl create configmap guardian-policy --from-file=guardian.hcl=guardian-renovate.hcl
```

```yaml
# values.yaml
policy:
  existingConfigMap: guardian-policy
```

## Helm Values

| File | Description |
|------|-------------|
| [`values-with-policy.yaml`](values-with-policy.yaml) | Single-org production-ready Helm values with inline HCL policy covering Renovate, catalog-info, settings, and branch protection. |
| [`values-multi-org.yaml`](values-multi-org.yaml) | Multi-org Helm values using strict-mode `scope { }`. Shared file rules with `scope { orgs = ["*"] }`, plus prod-only and staging-only rules. |

## Other

| File | Description |
|------|-------------|
| [`catalog-info.yaml`](catalog-info.yaml) | Example Backstage catalog-info.yaml for repo-guardian itself. |

## Key Concepts

- **When HCL is provided, built-in defaults are replaced entirely.** You must
  define all the rules you want — there's no merging with defaults.
- **Renovate rules are disabled by default** in built-in defaults. Set
  `enabled = true` to activate them, or define them in your HCL config.
- **Templates are embedded** in the binary. The HCL `template` field references
  the template name (e.g., `"codeowners"`, `"renovate-workflow"`), not a file
  path. Override templates by mounting files in `TEMPLATE_DIR`.
- **Two scope modes:**
  - **Legacy** (no top-level `scope { }`): every rule applies to every repo
    the GitHub App sees. The simpler default for single-org users.
  - **Strict** (top-level `scope { }` declared): every rule must declare its
    own `scope { orgs = [...] }` sub-block. Use the literal `["*"]` to apply
    to every in-scope org, or a subset to target specific orgs. See
    `guardian-multi-org.hcl` for the canonical example.
