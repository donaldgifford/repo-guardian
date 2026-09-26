// Package temporaltest starts a Temporal dev server for tests. It is
// imported only from tests.
//
// The server is the Temporal CLI's start-dev, downloaded once per
// machine at a pinned version (DESIGN-0026 OQ10) so a test run never
// silently moves to a new server. The same version is pinned in
// docker-compose.dev.yaml; bump both together.
package temporaltest

import (
	"io"
	"log/slog"
	"testing"

	"go.temporal.io/sdk/client"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"

	"github.com/donaldgifford/repo-guardian/internal/temporal"
)

// CLIVersion is the pinned Temporal CLI, and so dev server, version.
const CLIVersion = "v1.9.1"

// Server is a running dev server.
type Server struct {
	// Client is connected to the repo-guardian namespace.
	Client client.Client
	// Config reaches the server the way the application would.
	Config temporal.Config
}

// Start runs a dev server with the repo-guardian namespace registered
// and stops it when tb finishes. The first run on a machine downloads
// the CLI into the user cache directory.
func Start(tb testing.TB) *Server {
	tb.Helper()

	quiet := tlog.NewStructuredLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))

	srv, err := testsuite.StartDevServer(tb.Context(), testsuite.DevServerOptions{
		CachedDownload: testsuite.CachedDownload{Version: CLIVersion},
		ClientOptions:  &client.Options{Namespace: temporal.DefaultNamespace, Logger: quiet},
		LogLevel:       "error",
	})
	if err != nil {
		tb.Fatalf("temporaltest: start dev server %s: %v", CLIVersion, err)
	}

	tb.Cleanup(func() {
		if err := srv.Stop(); err != nil {
			tb.Logf("temporaltest: stop dev server: %v", err)
		}
	})

	return &Server{
		Client: srv.Client(),
		Config: temporal.Config{
			Address:   srv.FrontendHostPort(),
			Namespace: temporal.DefaultNamespace,
			TaskQueue: temporal.DefaultTaskQueue,
		},
	}
}
