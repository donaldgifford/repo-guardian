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
