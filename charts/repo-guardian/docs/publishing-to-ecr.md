# Publishing the chart to Amazon ECR

The default `chart-release.yml` workflow publishes only to GHCR. If
you need ECR (or another OCI-compliant registry), this recipe shows
the auth-and-push flow. It's intentionally NOT wired into this repo's
CI to keep the workflow surface lean — adapt it to your own pipeline
if needed.

## One-time ECR setup

Create the repository in your AWS account:

```bash
aws ecr create-repository \
  --repository-name helm-charts/repo-guardian \
  --region us-east-1
```

Configure an OIDC role that GitHub Actions can assume. Trust policy
should match your fork's OIDC subject (e.g.,
`repo:donaldgifford/repo-guardian:ref:refs/heads/main`). Permissions
needed: `ecr:GetAuthorizationToken`, `ecr:BatchCheckLayerAvailability`,
`ecr:CompleteLayerUpload`, `ecr:InitiateLayerUpload`,
`ecr:PutImage`, `ecr:UploadLayerPart`, `ecr:DescribeRepositories`,
and `ecr:DescribeImages` against the chart repo ARN.

## Auth-and-push step

Add this step to your downstream workflow (or run locally with
appropriate AWS credentials):

```yaml
- uses: aws-actions/configure-aws-credentials@v6
  with:
    role-to-assume: arn:aws:iam::123456789012:role/github-actions-helm
    aws-region: us-east-1

- name: Login to ECR
  run: |
    aws ecr get-login-password --region us-east-1 | \
      helm registry login \
        --username AWS \
        --password-stdin \
        123456789012.dkr.ecr.us-east-1.amazonaws.com

- name: Push chart to ECR
  run: |
    chart_version=$(yq '.version' charts/repo-guardian/Chart.yaml)
    helm pull oci://ghcr.io/donaldgifford/charts/repo-guardian \
      --version "${chart_version}" --destination /tmp
    helm push "/tmp/repo-guardian-${chart_version}.tgz" \
      "oci://123456789012.dkr.ecr.us-east-1.amazonaws.com/helm-charts"
```

The example pulls from GHCR (the canonical source) and re-pushes to
ECR. Alternatively, package from source if you want a fresh build:

```bash
helm package charts/repo-guardian
helm push repo-guardian-*.tgz \
  oci://123456789012.dkr.ecr.us-east-1.amazonaws.com/helm-charts
```

## Cosign signatures and SLSA in ECR

The cosign signature and SLSA provenance attestation produced by the
GHCR publish workflow live alongside the chart at GHCR — they do
**not** automatically follow the chart to ECR. If you need
verifiable signatures in your ECR push, sign the ECR-pushed digest
separately:

```bash
cosign sign --yes \
  "123456789012.dkr.ecr.us-east-1.amazonaws.com/helm-charts/repo-guardian@<digest>"
```

This requires `id-token: write` on the workflow that does the ECR
push.

## Local push (no CI)

```bash
make helm-push HELM_REGISTRY=123456789012.dkr.ecr.us-east-1.amazonaws.com/helm-charts
```

The `helm-push` make target packages the chart and pushes to the OCI
registry specified by `HELM_REGISTRY`. Auth must be handled
separately (e.g., `aws ecr get-login-password | helm registry
login ...` before invocation).
