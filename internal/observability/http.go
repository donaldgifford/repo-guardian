package observability

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Handler wraps an HTTP handler with semconv server metrics.
//
// route names the operation. It is the span name only — spans go
// nowhere in this build — and does NOT set the http.route metric
// attribute: as of otelhttp v0.70.0 that comes from http.Request.Pattern,
// which net/http fills in when a ServeMux with Go 1.22 patterns routes
// the request. (The older otelhttp.WithRouteTag no longer exists.)
// Passing the registration pattern here keeps the two agreeing.
//
// Route rather than path is what keeps these metrics bounded, and it is
// the default: the emitted attribute set is method, route, status,
// protocol, server.address and scheme — no url.path, which on an
// endpoint anyone on the internet can POST to would be an unbounded
// label.
//
// Wrap OUTSIDE any rejection middleware. On the webhook route the IP
// allowlist and the HMAC check both reject before the handler proper
// runs, and those rejections are the interesting part: HMAC 401s are
// counted nowhere else in the system today (webhook_rejected_total
// lives only in the allowlist middleware, and only counts 403s). Wrap
// inside them and the failures vanish from the histogram, leaving a
// panel that shows only requests that already succeeded.
//
// Deliberately NOT applied to the metrics/health server: it would
// mostly measure Prometheus scraping itself, and kubelet probes would
// swamp the request count with traffic nobody is asking questions
// about.
//
// The meter provider is the global one, so this is a no-op wrapper
// when OTEL_SDK_DISABLED is set — call sites never branch.
func Handler(h http.Handler, route string) http.Handler {
	return otelhttp.NewHandler(h, route)
}

// Transport wraps an HTTP transport with semconv client metrics.
//
// It must be the OUTERMOST layer of the GitHub transport chain:
//
//	otelhttp                 ← here
//	  └─ rate-limit          ← IMPL-0022, the ThrottledError source
//	       └─ ghinstallation ← token minting
//
// The ordering is not cosmetic. The rate-limit transport refuses to
// send once the remaining budget is under the reserve, returning a
// *github.ThrottledError instead of a response. Outermost, that refusal
// is still an attempt otelhttp measures, carrying error.type and no
// status code — which is the residual throttle signal replacing the
// retired github_rate_limit_wait_* pair. Put otelhttp underneath and
// the request never reaches it: deferred calls disappear from the
// client metrics entirely, and the throttled period reads as a traffic
// lull rather than as backpressure.
//
// The meter provider is the global one, so this is a no-op wrapper when
// OTEL_SDK_DISABLED is set — call sites never branch.
func Transport(next http.RoundTripper) http.RoundTripper {
	return otelhttp.NewTransport(next)
}
