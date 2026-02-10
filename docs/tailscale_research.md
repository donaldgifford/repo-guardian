# Tailscale Integration Research

Research into using Tailscale for exposing repo-guardian's webhook endpoint publicly during local/dev testing, and potentially in production.

## Two Approaches

### 1. Sidecar Container (Current Setup)

The `compose.yaml` already has a Tailscale sidecar behind the `funnel` profile. It uses `network_mode: service:repo-guardian` to share the network namespace and proxies HTTPS traffic from Funnel to `127.0.0.1:8080`.

**Pros:**
- Zero code changes to the app
- Works with any container image
- Independent lifecycle (can restart Tailscale without restarting the app)
- Familiar Docker Compose pattern

**Cons:**
- Requires `CAP_NET_ADMIN` and `/dev/net/tun`
- Two containers to manage
- Serve config needs `${TS_CERT_DOMAIN}` variable expansion

### 2. Go SDK (`tsnet`) — Embedded Tailscale

`tsnet` (`tailscale.com/tsnet`) embeds the full Tailscale networking stack directly into a Go program. The app **becomes** a Tailscale node — no sidecar, no daemon, no kernel modules.

It uses a **userspace TCP/IP stack** (gVisor netstack), so it requires no root, no `CAP_NET_ADMIN`, and no TUN device.

#### Core API

```go
srv := &tsnet.Server{
    Hostname:  "repo-guardian",
    Dir:       "./tsnet-state",       // persists node identity/keys
    AuthKey:   os.Getenv("TS_AUTHKEY"),
    Ephemeral: true,                  // auto-remove from tailnet on shutdown
}
defer srv.Close()

// ListenFunnel exposes on the public internet with automatic TLS.
// Only ports 443, 8443, and 10000 are supported.
ln, err := srv.ListenFunnel("tcp", ":443")
if err != nil {
    log.Fatal(err)
}

// The public URL is https://repo-guardian.<tailnet-name>.ts.net
fmt.Printf("Listening on https://%s\n", srv.CertDomains()[0])

log.Fatal(http.Serve(ln, myHandler))
```

#### How It Would Work in repo-guardian

The webhook server (currently on `:8080`) could optionally also listen via Funnel on `:443`. The metrics server (`:9090`) stays local-only. This could be gated behind a config flag (e.g., `TAILSCALE_FUNNEL=true`).

```go
// Pseudocode for the integration pattern:
if cfg.TailscaleFunnel {
    tsSrv := &tsnet.Server{
        Hostname: "repo-guardian",
        AuthKey:  os.Getenv("TS_AUTHKEY"),
    }
    ln, _ := tsSrv.ListenFunnel("tcp", ":443")
    go http.Serve(ln, webhookHandler)
}
```

#### Trade-off Comparison

| Factor | `tsnet` (Embedded) | Sidecar Container |
|---|---|---|
| Code changes | Must integrate into app | None |
| Privileges | No root, no CAP_NET_ADMIN | Needs CAP_NET_ADMIN + /dev/net/tun |
| Container count | Single container | Two containers |
| Binary size impact | +15-30 MB (~3x current) | No impact |
| Dependency tree | ~650+ transitive Go deps | No impact |
| Networking stack | Userspace (gVisor) | Kernel WireGuard |
| Lifecycle coupling | Tailscale = app lifecycle | Independent |
| Debugging | Harder to isolate | Separate container |

## Funnel Constraints

Applies to both approaches:

- **Ports:** Only 443, 8443, and 10000 are supported. No other ports work.
- **HTTPS only:** TLS termination is automatic. No plain HTTP over Funnel.
- **ACL policy:** Funnel must be explicitly enabled in the tailnet ACL policy.
- **Auth keys:** Regular keys expire after 90 days max. Use **OAuth client credentials** (`ClientID`/`ClientSecret`) for long-lived deployments.

## Gotchas

### State directory persistence
Even with `Ephemeral: true`, tsnet requires a writable `Dir` for state. In read-only container filesystems, set `Dir` to a writable path or mount an `emptyDir` volume. Tracked in [tailscale/tailscale#16556](https://github.com/tailscale/tailscale/issues/16556).

### Dependency weight
The `tailscale.com` module pulls in ~100 direct dependencies including the full gVisor netstack, wireguard-go, and AWS SDK v2. The `depaware.txt` manifest is 657 lines. This would significantly increase build times and `go.sum` size.

### Kubernetes considerations
- Without persistent state, the node re-authenticates on every pod restart.
- Use `Ephemeral: true` with a reusable auth key, or mount a PersistentVolume for `Dir`.
- Multiple replicas cannot share the same state (each needs its own identity).

### Userspace networking performance
The gVisor netstack has historically had ~20-30% overhead vs kernel WireGuard due to GC pressure. Recent buffer pooling improvements have reduced this. **Irrelevant for webhook workloads** — only matters for high-throughput data transfer.

## Recommendation

For **local dev/testing**, the sidecar container approach is simpler — no code changes, no dependency bloat, and it's already wired up in compose.yaml.

For **production on Kubernetes**, the sidecar is also the more standard pattern. It aligns with Tailscale's own [Kubernetes operator](https://tailscale.com/kb/1185/kubernetes) which uses sidecar proxies.

The `tsnet` approach would only be worth it if we wanted to:
- Eliminate the sidecar entirely in production
- Remove the `CAP_NET_ADMIN` requirement
- Have programmatic control over Tailscale (e.g., dynamic cert provisioning, identity-aware routing)

None of these are pressing needs for repo-guardian today. The sidecar is the right call for now.

## References

- [tsnet — Tailscale Docs](https://tailscale.com/kb/1244/tsnet)
- [tsnet Go Package Reference](https://pkg.go.dev/tailscale.com/tsnet)
- [The Subtle Magic of tsnet — Tailscale Blog](https://tailscale.com/blog/tsup-tsnet)
- [Create Virtual Private Services with tsnet — Tailscale Blog](https://tailscale.com/blog/tsnet-virtual-private-services)
- [Official tsnet-funnel Example](https://github.com/tailscale/tailscale/blob/main/tsnet/example/tsnet-funnel/tsnet-funnel.go)
- [Docker + Tailscale Guide](https://tailscale.com/blog/docker-tailscale-guide)
- [Tailscale on Kubernetes](https://tailscale.com/kb/1185/kubernetes)
- [GitHub Issue #16556: State dir required even for ephemeral nodes](https://github.com/tailscale/tailscale/issues/16556)
