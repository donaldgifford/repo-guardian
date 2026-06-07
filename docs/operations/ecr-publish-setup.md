# ECR publish setup (maintainer-side)

The `.github/workflows/ecr.yml` workflow pushes the repo-guardian
container image and Helm chart to an AWS ECR account in parallel with
the GHCR publish. This document describes the one-time AWS-side prep
required before the workflow can run successfully.

> **Audience:** project maintainers. Chart consumers don't need any
> of this — they just `helm pull` from the public registry.

## Required prep (one-time)

### 1. IAM OIDC trust for GitHub Actions

Create (or extend) an IAM Identity Provider for GitHub Actions if
your account doesn't already have one:

```bash
aws iam create-open-id-connect-provider \
  --url https://token.actions.githubusercontent.com \
  --client-id-list sts.amazonaws.com \
  --thumbprint-list 6938fd4d98bab03faadb97b34396831e3780aea1
```

(Thumbprint is the well-known GitHub OIDC root cert SHA-1. AWS rotates
behind the scenes for the actual TLS, so this thumbprint is fixed by
GitHub.)

### 2. IAM role with ECR push permissions

Create a role with this trust policy (replace `<AWS_ACCOUNT_ID>`):

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Federated": "arn:aws:iam::<AWS_ACCOUNT_ID>:oidc-provider/token.actions.githubusercontent.com"
      },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "token.actions.githubusercontent.com:aud": "sts.amazonaws.com"
        },
        "StringLike": {
          "token.actions.githubusercontent.com:sub": "repo:donaldgifford/repo-guardian:*"
        }
      }
    }
  ]
}
```

And this permissions policy:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ecr:GetAuthorizationToken"
      ],
      "Resource": "*"
    },
    {
      "Effect": "Allow",
      "Action": [
        "ecr:BatchCheckLayerAvailability",
        "ecr:CompleteLayerUpload",
        "ecr:GetDownloadUrlForLayer",
        "ecr:InitiateLayerUpload",
        "ecr:PutImage",
        "ecr:UploadLayerPart",
        "ecr:BatchGetImage",
        "ecr:ListImages",
        "ecr:DescribeImages",
        "ecr:DescribeRepositories"
      ],
      "Resource": [
        "arn:aws:ecr:<AWS_REGION>:<AWS_ACCOUNT_ID>:repository/repo-guardian",
        "arn:aws:ecr:<AWS_REGION>:<AWS_ACCOUNT_ID>:repository/repo-guardian-chart"
      ]
    }
  ]
}
```

The trust policy's `sub` claim restricts which workflows can assume
the role. `repo:donaldgifford/repo-guardian:*` allows any ref in this
repo to assume the role; tighten to specific branches/tags if you
want stricter scoping.

### 3. ECR repositories

```bash
aws ecr create-repository --repository-name repo-guardian       --region <AWS_REGION>
aws ecr create-repository --repository-name repo-guardian-chart --region <AWS_REGION>
```

ECR doesn't have a namespace concept like GHCR — both image and chart
sit at the account root with flat names. The chart repo uses
`-chart` suffix to disambiguate.

### 4. GitHub repo secrets

In **Settings → Secrets and variables → Actions → Secrets**, set:

| Secret name | Value |
|---|---|
| `ECR_AWS_ACCOUNT_ID` | 12-digit AWS account ID (e.g. `123456789012`) |
| `ECR_REGION` | AWS region (e.g. `us-east-1`) |
| `ECR_ROLE_ARN` | `arn:aws:iam::<account>:role/<role-name>` from step 2 |

## Verification

Run the workflow with `dry_run: true` from the Actions tab → "Publish
to ECR" → "Run workflow" — this packages the chart and runs through
auth without pushing. A clean run confirms the IAM trust policy + role
permissions are correct.

## What gets published

| Artifact | Registry path |
|---|---|
| Container image | `<account>.dkr.ecr.<region>.amazonaws.com/repo-guardian:<tag>` |
| Helm chart | `oci://<account>.dkr.ecr.<region>.amazonaws.com/repo-guardian-chart:<version>` |
| Cosign signatures | sibling OCI artifacts (`.sig` tag suffix) |
| SLSA Level 3 provenance | sibling OCI artifacts (`.att` tag suffix) |

## Consumer-side install

```bash
aws ecr get-login-password --region <region> | \
  helm registry login <account>.dkr.ecr.<region>.amazonaws.com \
    --username AWS --password-stdin

helm install repo-guardian \
  oci://<account>.dkr.ecr.<region>.amazonaws.com/repo-guardian-chart \
  --version <chart-version> \
  -f values.yaml
```

ECR repositories default to private; granting read access to other
AWS accounts is a separate IAM/ECR repository policy step, not
covered here.
