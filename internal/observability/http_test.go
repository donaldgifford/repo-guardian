package observability_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	promclient "github.com/prometheus/client_golang/prometheus"

	"github.com/donaldgifford/repo-guardian/internal/observability"
	"github.com/donaldgifford/repo-guardian/internal/webhook"
)

// serveThrough routes one request through a ServeMux and returns the
// response status.
//
// Going through a mux is the point: http.Request.Pattern is what
// otelhttp reads for the http.route attribute, and only a mux fills it
// in. Registering the wrapped handler straight onto an httptest.Server
// would leave Pattern empty, http.route with it, and both tests below
// quietly measuring something other than production.
func serveThrough(t *testing.T, route string, h http.Handler, req *http.Request) int {
	t.Helper()

	mux := http.NewServeMux()
	mux.Handle(route, observability.Handler(h, route))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(srv.URL, "http://")
	req.RequestURI = ""

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	return resp.StatusCode
}

// TestHandler_MeasuresRejectedWebhooks is the point of instrumenting
// inbound HTTP at all (DESIGN-0022 Finding G).
//
// A webhook that fails HMAC validation is counted NOWHERE in this
// system today: webhook_rejected_total lives in the IP-allowlist
// middleware and only counts 403s, and the handler's own counters
// increment after validation passes. So an App with a rotated-but-not-
// redeployed secret rejects every delivery from GitHub and every
// existing dashboard shows a quiet, healthy service.
//
// This asserts the 401 lands in the semconv server histogram with its
// status code attached, using the real webhook handler rather than a
// stub — a stub would only prove otelhttp works, which is upstream's
// test, not ours.
func TestHandler_MeasuresRejectedWebhooks(t *testing.T) {
	reg := promclient.NewRegistry()
	newProvider(t, reg)

	handler := webhook.NewHandler("the-real-secret", nil, slog.Default(), nil, nil, "", 0)

	req, err := http.NewRequestWithContext(
		t.Context(), http.MethodPost, "/webhooks/github", strings.NewReader(`{"action":"created"}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	req.Header.Set("X-GitHub-Event", "ping")
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")

	if status := serveThrough(t, "POST /webhooks/github", handler, req); status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; the test is no longer exercising a rejected webhook", status)
	}

	body := scrape(t, reg)

	for _, want := range []string{
		"http_server_request_duration_seconds",
		`http_response_status_code="401"`,
		`http_route="/webhooks/github"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape is missing %q\n%s", want, body)
		}
	}
}

// TestHandler_KeysOnRouteNotPath pins the attribute that decides
// whether server metrics are one series or one series per request.
//
// The webhook endpoint is reachable by anyone who can find the
// hostname, so anything derived from the request URL is
// attacker-controlled. otelhttp keys on http.route — the ServeMux
// pattern, taken from http.Request.Pattern — so three requests to
// three different paths under one pattern collapse to one series.
//
// This does NOT claim the whole attribute set is bounded, and it is
// not: server.address and server.port are derived from the request
// Host header, which is also client-controlled. Closing that is the
// task 3.6 cardinality audit's job (it needs a view, not a wrapper
// change); this test covers the part the wrapper is responsible for.
//
// Non-vacuity: assert on url.path instead of http.route and the three
// paths reappear as three series.
func TestHandler_KeysOnRouteNotPath(t *testing.T) {
	reg := promclient.NewRegistry()
	newProvider(t, reg)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for _, path := range []string{"/things/aaa", "/things/bbb", "/things/ccc"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}

		if status := serveThrough(t, "GET /things/{id}", handler, req); status != http.StatusOK {
			t.Fatalf("status = %d, want 200", status)
		}
	}

	body := scrape(t, reg)

	if strings.Contains(body, "url_path") {
		t.Errorf("server metrics carry url.path, which is unbounded on a public endpoint\n%s", body)
	}

	for _, unwanted := range []string{"/things/aaa", "/things/bbb", "/things/ccc"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("scrape contains the raw path %q; one series per request URL is a cardinality incident\n%s", unwanted, body)
		}
	}

	if want := `http_route="/things/{id}"`; !strings.Contains(body, want) {
		t.Errorf("scrape is missing %q; requests are not being keyed by route\n%s", want, body)
	}
}

// TestHandler_SpoofedHostDoesNotMintSeries closes the finding the
// task 3.2 tests deliberately left open (IMPL-0023 task 3.6).
//
// server.address and server.port default to the request's Host header.
// /webhooks/github is reachable from the internet, so a caller can send
// a different Host on every request and mint a series per value across
// three histograms — in the same registry that serves every
// repo_guardian_ metric, where one bad series does not degrade the
// endpoint but 500s it entirely.
//
// Non-vacuity: drop WithServerName from Handler and this fails on all
// three spoofed values.
func TestHandler_SpoofedHostDoesNotMintSeries(t *testing.T) {
	reg := promclient.NewRegistry()
	newProvider(t, reg)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	spoofed := []string{"evil.example.com", "also-evil.example.com", "third.example.com"}

	for _, host := range spoofed {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/hook", http.NoBody)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}

		req.Host = host

		if status := serveThrough(t, "GET /hook", handler, req); status != http.StatusOK {
			t.Fatalf("status = %d, want 200", status)
		}
	}

	body := scrape(t, reg)

	for _, host := range spoofed {
		if strings.Contains(body, host) {
			t.Errorf("a spoofed Host header reached the metrics as %q; "+
				"every distinct value is a new series on a publicly reachable endpoint\n%s", host, body)
		}
	}

	if want := `server_address="repo-guardian"`; !strings.Contains(body, want) {
		t.Errorf("scrape is missing %q; server.address is not pinned\n%s", want, body)
	}

	if strings.Contains(body, "server_port=") {
		t.Errorf("server_port is still present; WithServerName should suppress it\n%s", body)
	}
}

// TestBridge_NoScopeLabels pins the dependency-churn fix.
//
// otel_scope_version carries the INSTRUMENTATION LIBRARY's version, so
// leaving scope labels on means every renovate bump of otelhttp,
// redisotel or otelpgx changes a label value on every series that
// library emits: the old series goes stale, the new one starts at zero,
// and rate() across the deploy sees a counter reset. Phase 6 bakes
// PromQL into generated dashboards behind a fail-on-diff gate, so this
// would be a recurring breakage rather than a cosmetic one.
func TestBridge_NoScopeLabels(t *testing.T) {
	reg := promclient.NewRegistry()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	newProvider(t, reg)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/hook", http.NoBody)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if status := serveThrough(t, "GET /hook", handler, req); status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	body := scrape(t, reg)

	for _, label := range []string{"otel_scope_name", "otel_scope_version", "otel_scope_schema_url"} {
		if strings.Contains(body, label) {
			t.Errorf("%s is present; it changes value on every dependency bump and resets the series\n%s", label, body)
		}
	}

	// The suffixes the naming strategy is pinned for. Losing them would
	// break every alert and generated panel at once.
	if want := "http_server_request_duration_seconds_bucket"; !strings.Contains(body, want) {
		t.Errorf("scrape is missing %q; the name-translation strategy is not producing suffixed names\n%s", want, body)
	}
}
