# repo-guardian docs

Design and planning documentation for repo-guardian.

## Contents

| Document | Description |
|----------|-------------|
| [RFC.md](RFC.md) | Canonical design document covering architecture, rules engine, deployment, and observability |
| [IMPLEMENTATION_PLAN.md](IMPLEMENTATION_PLAN.md) | Phased build plan with acceptance criteria (derived from RFC) |
| [api_backoff.md](api_backoff.md) | GitHub API rate limit and backoff strategy research |
| [tailscale_research.md](tailscale_research.md) | Tailscale networking research for webhook delivery |

## Quick Links

- [Configuration reference](../README.md#configuration) -- all environment variables
- [Kubernetes deployment](../README.md#kubernetes-deployment) -- Kustomize manifests and setup
- [Local development](../README.md#quick-start-local-development) -- Docker Compose quick start
- [Observability](../README.md#observability) -- Prometheus metrics reference
