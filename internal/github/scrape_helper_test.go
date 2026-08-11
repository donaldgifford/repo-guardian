package github_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// scrapeRegistry renders reg as Prometheus exposition text.
func scrapeRegistry(t *testing.T, reg *promclient.Registry) string {
	t.Helper()

	srv := httptest.NewServer(promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatalf("build scrape request: %v", err)
	}

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scrape body: %v", err)
	}

	return string(body)
}

// sumSamples adds up every sample of the named metric in exposition
// text, across all label sets.
//
// Summing rather than matching one label set is deliberate: the point
// of the caller is to count how many requests were measured in total,
// and a successful request and a refused one carry different
// attributes by design. Keying on either would make the assertion
// depend on the thing being measured.
func sumSamples(t *testing.T, exposition, metric string) float64 {
	t.Helper()

	var total float64

	for _, line := range strings.Split(exposition, "\n") {
		if !strings.HasPrefix(line, metric+"{") && line != metric {
			continue
		}

		idx := strings.LastIndex(line, " ")
		if idx < 0 {
			continue
		}

		value, err := strconv.ParseFloat(strings.TrimSpace(line[idx+1:]), 64)
		if err != nil {
			t.Fatalf("parse sample %q: %v", line, err)
		}

		total += value
	}

	return total
}
