package observability_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	promclient "github.com/prometheus/client_golang/prometheus"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/observability"
)

// TestNew_DefaultRegistryIsTheProductionPath exercises the wiring
// main.go actually uses: Options.Registerer left nil, so the bridge
// lands in prometheus.DefaultRegisterer — the registry promauto writes
// to and promhttp.Handler() serves.
//
// Every other test in this package injects a private registry, which is
// right for isolation but means the nil-Registerer branch, and the
// coexistence claim in its real setting, would otherwise never run. The
// domain metrics here are not synthesised: internal/metrics registers
// all of them into the default registry at init, so this is the genuine
// article sharing a registry with the bridge.
//
// Registration is process-global and one-shot, so this deliberately
// lives alone in its own file and must not be duplicated — a second
// call would fail on duplicate registration.
func TestNew_DefaultRegistryIsTheProductionPath(t *testing.T) {
	// Touch a domain metric so it has a sample to render.
	metrics.ReposCheckedTotal.WithLabelValues("scheduler", "acme").Inc()

	provider, err := observability.New(observability.Options{Logger: slog.Default()})
	if err != nil {
		t.Fatalf("New() with a nil Registerer = %v, want nil", err)
	}

	counter, err := provider.MeterProvider.Meter("test/default").Int64Counter("http.server.requests")
	if err != nil {
		t.Fatalf("Int64Counter() = %v, want nil", err)
	}

	counter.Add(context.Background(), 1)

	gathered, err := promclient.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("Gather() on the default registry = %v, want nil; "+
			"a name collision here 500s /metrics for every series at once", err)
	}

	var sawDomain, sawSemconv bool

	for _, mf := range gathered {
		switch {
		case strings.HasPrefix(mf.GetName(), "repo_guardian_"):
			sawDomain = true
		case strings.HasPrefix(mf.GetName(), "http_server_"):
			sawSemconv = true
		}
	}

	if !sawDomain {
		t.Error("no repo_guardian_ series in the default registry")
	}

	if !sawSemconv {
		t.Error("no semconv series in the default registry; the bridge did not default to DefaultRegisterer")
	}
}
