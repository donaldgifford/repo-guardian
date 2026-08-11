package github_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	promclient "github.com/prometheus/client_golang/prometheus"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/observability"
)

// TestTransportOrder_ThrottledRequestIsStillMeasured is the shared
// ordering contract between DESIGN-0021 (rate-limit deferral) and
// DESIGN-0022 (OTEL client metrics). IMPL-0023 task 3.3: whichever of
// the two lands second owns this test, and OTEL landed second.
//
// The chain must be:
//
//	otelhttp
//	  └─ rate-limit
//	       └─ ghinstallation
//
// The rate-limit transport does not send once remaining is under the
// reserve — it returns a *ThrottledError in place of a response. With
// otelhttp on top, that refusal is an attempt: it lands in the client
// histogram carrying error.type and no status code, which is the
// residual throttle signal that replaced github_rate_limit_wait_*.
// Underneath, the request never reaches otelhttp at all, so deferred
// calls vanish from the metrics and a throttled installation looks
// like an idle one — the exact failure the deferral work exists to
// make visible.
//
// So the discriminator is the SAMPLE COUNT, not the attributes: two
// requests are issued and both must be measured, even though only one
// was sent.
func TestTransportOrder_ThrottledRequestIsStillMeasured(t *testing.T) {
	reg := promclient.NewRegistry()

	if _, err := observability.New(observability.Options{
		Logger:     slog.Default(),
		Registerer: reg,
	}); err != nil {
		t.Fatalf("observability.New() = %v, want nil", err)
	}

	resetAt := time.Now().Add(50 * time.Minute)

	// Remaining sits above zero but under the reserve threshold, so our
	// own transport raises ThrottledError on the NEXT call. Zero would
	// instead trip go-github's internal pre-check, which short-circuits
	// above our transport and above otelhttp — a different path, and
	// not the one this test is about.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/o/r", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "20")
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"r","default_branch":"main"}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Threshold 0.10 of 5000 = 500; remaining 20 is well under it.
	client, err := ghclient.NewClientForBaseURL(srv.URL+"/api/v3", nil, slog.Default(), 0.10)
	if err != nil {
		t.Fatalf("NewClientForBaseURL() = %v, want nil", err)
	}

	ctx := context.Background()

	// Request 1: sent, succeeds, primes the rate-limit cache.
	if _, err := client.GetRepository(ctx, "o", "r"); err != nil {
		t.Fatalf("first GetRepository() = %v, want nil", err)
	}

	// Request 2: refused by the rate-limit transport before it is sent.
	_, err = client.GetRepository(ctx, "o", "r")
	if err == nil {
		t.Fatal("second GetRepository() = nil error, want a throttle deferral; the fixture no longer trips the reserve")
	}

	if _, ok := ghclient.AsThrottled(err); !ok {
		t.Fatalf("second GetRepository() error = %v, want a throttle signal", err)
	}

	body := scrapeRegistry(t, reg)

	const metricName = "http_client_request_duration_seconds_count"

	total := sumSamples(t, body, metricName)
	if total != 2 {
		t.Errorf("%s total = %v, want 2 (one sent, one refused); "+
			"a count of 1 means otelhttp sits BELOW the rate-limit transport and never saw the deferred request",
			metricName, total)
	}

	if !strings.Contains(body, "error_type=") {
		t.Errorf("client metrics carry no error.type attribute, so the deferral is indistinguishable from a success\n%s", body)
	}
}
