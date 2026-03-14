---
id: INV-0001
title: "Tailscale Funnel for Webhook Testing"
status: Concluded
author: Donald Gifford
created: 2026-03-14
---
<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0001: Tailscale Funnel for Webhook Testing

**Status:** Open
**Author:** Donald Gifford
**Date:** 2026-03-14

## Question

Can we use Tailscale Funnel to expose the repo-guardian webhook endpoint
running in Kubernetes so that GitHub can deliver webhooks to it? We will test
on a local Talos k8s cluster first, with EKS as the eventual production
target. A previous attempt failed for unknown reasons -- this investigation
aims to identify and resolve those issues.

## Hypothesis

Tailscale Funnel should work as a webhook ingress for the EKS-deployed app.
The likely failure points from the previous attempt are:

1. Funnel not enabled in the tailnet ACL policy.
2. Port mismatch -- Funnel only supports ports 443, 8443, and 10000, while the
   app listens on 8080. The sidecar needs to proxy correctly.
3. Tailscale serve config not routing traffic to the app container.
4. Kubernetes networking between the sidecar and app containers.
5. Auth key expiry or misconfiguration in the pod.

## Context

repo-guardian runs in Kubernetes (local Talos cluster for dev/testing, EKS for
production). GitHub needs a publicly reachable HTTPS endpoint to deliver
webhooks. Currently we use ngrok for this, but the free tier generates a new
URL on every restart, requiring a manual update of the GitHub App webhook URL
each time.

Tailscale Funnel provides a stable `*.ts.net` URL that persists across pod
restarts (assuming the same Tailscale node identity), which would eliminate
this friction. We previously attempted Tailscale Funnel but it failed -- the
reason was not documented.

**Triggered by:** [DESIGN-0003: Tailscale Integration Research](../design/0003-tailscale-integration-research.md)

## Approach

### Phase 1: Verify Tailscale Funnel prerequisites

1. Check tailnet ACL policy allows Funnel (`"nodeAttrs"` with `"funnel"` cap).
2. Confirm we have a reusable auth key or OAuth client credentials for the
   Tailscale node in k8s.
3. Determine the Funnel hostname (e.g., `repo-guardian.<tailnet>.ts.net`).

### Phase 2: Sidecar container design for Kubernetes

1. Add a Tailscale sidecar container to the repo-guardian Deployment.
2. Use the official `tailscale/tailscale` Docker image.
3. Sidecar shares the pod network namespace (default in k8s -- all containers
   in a pod share `localhost`).
4. Configure Tailscale serve to proxy Funnel HTTPS (port 443) to
   `http://127.0.0.1:8080` (the app's webhook port).
5. Sidecar needs:
   - `TS_AUTHKEY` or OAuth credentials via a k8s Secret.
   - `TS_EXTRA_ARGS` or serve config for Funnel setup.
   - Writable state directory (emptyDir or PV for persistent identity).
   - `CAP_NET_ADMIN` and `/dev/net/tun` (unless using userspace networking).

### Phase 3: Deploy and test on Talos cluster

1. Deploy the updated Deployment with the Tailscale sidecar to the local Talos
   cluster.
2. Confirm the Funnel URL is reachable from the public internet.
3. Update the GitHub App webhook URL to the Funnel `*.ts.net` URL.
4. Trigger a webhook event (create a test repo or push to an existing one).
5. Verify the webhook is received, HMAC-validated, and processed by the app.

### Phase 4: Persistence and reliability

1. Restart the pod -- confirm the Funnel URL remains stable.
2. Verify auth key renewal or OAuth token refresh works across restarts.
3. Confirm no webhook deliveries are lost during pod restarts.

## Environment

| Component | Version / Value |
|-----------|----------------|
| Tailscale | 1.94.1 (local); TBD (sidecar image tag) |
| Kubernetes | Talos (local dev/test), EKS (production target) |
| repo-guardian | current main branch |
| GitHub App | dev app instance |

## Funnel Constraints (from DESIGN-0003)

- **Ports:** Only 443, 8443, and 10000 are supported for Funnel.
- **HTTPS only:** TLS termination is automatic (handled by Tailscale).
- **ACL policy:** Funnel must be explicitly enabled in the tailnet ACL policy.
- **Auth keys:** Regular keys expire after 90 days max. Use OAuth client
  credentials for long-lived deployments.

## Kubernetes Sidecar Considerations

- All containers in a pod share `localhost`, so the sidecar can reach the app
  on `127.0.0.1:8080` without any special networking.
- Tailscale sidecar needs a writable directory for state. Options:
  - `emptyDir` -- simple but re-authenticates on every pod restart (new node
    identity each time).
  - PersistentVolume -- preserves node identity across restarts, keeps the same
    Funnel hostname.
- If using `emptyDir` with `Ephemeral: true`, the node auto-removes from the
  tailnet on shutdown, but the Funnel URL remains stable as long as the same
  `TS_HOSTNAME` is used with a reusable auth key.
- The Tailscale Kubernetes operator is another option but may be heavier than
  needed for a single-pod sidecar.

## Findings

### ACL Policy

The tailnet ACL already had the required configuration:

- `tag:container` defined in `tagOwners` with `autogroup:admin` as owner.
- `nodeAttrs` grants `funnel` capability to `tag:container`.

No ACL changes were needed.

### Auth Key

Generated a reusable, ephemeral auth key tagged with `tag:container`. The
sidecar authenticates on pod start and auto-removes from the tailnet on
shutdown. With `TS_HOSTNAME=repo-guardian` and a reusable key, the same
Funnel hostname is reclaimed on restart.

### Sidecar Configuration

The working configuration uses:

- **Image:** `ghcr.io/tailscale/tailscale:latest`
- **Userspace networking** (`TS_USERSPACE=true`) -- avoids `CAP_NET_ADMIN`
  and `/dev/net/tun`. The sidecar runs as non-root (UID 1000).
- **Serve config via ConfigMap** (`TS_SERVE_CONFIG=/config/serve-config.json`)
  -- proxies Funnel HTTPS 443 to `http://127.0.0.1:8080`.
- **State directory** on `emptyDir` (`TS_STATE_DIR=/var/lib/tailscale`).

### Serve Config

```json
{
  "TCP": { "443": { "HTTPS": true } },
  "Web": {
    "${TS_CERT_DOMAIN}:443": {
      "Handlers": { "/": { "Proxy": "http://127.0.0.1:8080" } }
    }
  },
  "AllowFunnel": { "${TS_CERT_DOMAIN}:443": true }
}
```

`${TS_CERT_DOMAIN}` is automatically expanded by Tailscale to the node's
FQDN (e.g., `repo-guardian.wyrm-ule.ts.net`).

### Funnel URL

Public endpoint: `https://repo-guardian.wyrm-ule.ts.net`

- TLS via Let's Encrypt (auto-provisioned by Tailscale).
- HTTP/2 supported.
- Health check (`/healthz`) returns 200.

### Webhook Delivery

Tested by adding a repo to the GitHub App installation:

1. GitHub sent `installation_repositories` webhook to
   `https://repo-guardian.wyrm-ule.ts.net/webhooks/github`.
2. Funnel proxied to the app on `127.0.0.1:8080`.
3. HMAC signature validated successfully.
4. App processed the event, checked the repo for missing files, and logged
   the dry-run result.

Full end-to-end flow confirmed working: GitHub -> Tailscale Funnel -> k8s
pod -> repo-guardian webhook handler -> checker engine.

### Previous Failure (Likely Cause)

While the exact previous failure was not documented, the most likely cause
was one of:

- Funnel not enabled in the ACL policy (it requires explicit `nodeAttrs`).
- Not using `TS_SERVE_CONFIG` with a proper serve config JSON (Funnel won't
  activate without `AllowFunnel` in the serve config).
- Missing userspace mode causing permission issues without `CAP_NET_ADMIN`.

## Conclusion

**Answer:** Yes. Tailscale Funnel works as a webhook tunnel for repo-guardian
running in Kubernetes. The sidecar approach requires no code changes, provides
a stable public URL with auto TLS, and runs without elevated privileges using
userspace networking.

## Recommendation

1. **Use the `deploy/overlays/tailscale/` overlay** for dev/test deployments
   that need GitHub webhook delivery.
2. **Keep ngrok in `compose.yaml`** as a fallback for quick local testing
   outside of k8s.
3. **For production (EKS)**, use a proper ALB Ingress instead of Funnel.
   Funnel is a dev/test solution -- it routes through Tailscale's DERP relays
   which adds latency and is not designed for production webhook traffic.
4. **Auth key rotation**: The current reusable key expires after 90 days.
   Consider switching to OAuth client credentials for longer-lived
   deployments, or automate key rotation.
5. **Webhook IP allowlisting**: This investigation did not cover restricting
   access to the Funnel endpoint. Tailscale Funnel exposes the webhook URL to
   the public internet with no IP-level filtering. While HMAC validation
   rejects unauthorized payloads, an IP allowlist middleware would add
   defense-in-depth. See
   [DESIGN-0004: GitHub Webhook IP Allowlist Middleware](../design/0004-github-webhook-ip-allowlist-middleware.md)
   for the proposed solution.

## References

- [DESIGN-0003: Tailscale Integration Research](../design/0003-tailscale-integration-research.md)
- [Tailscale Funnel Docs](https://tailscale.com/kb/1223/funnel)
- [Tailscale on Kubernetes](https://tailscale.com/kb/1185/kubernetes)
- [Tailscale Docker/Kubernetes Sidecar Guide](https://tailscale.com/blog/docker-tailscale-guide)
- [Tailscale Serve Docs](https://tailscale.com/kb/1242/tailscale-serve)
