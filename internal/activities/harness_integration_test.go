//go:build integration

package activities_test

import (
	"context"
	"flag"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/temporalproto"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/temporal/temporaltest"
)

const installationID int64 = 7

// updateHistories rewrites the replay suite's captured histories
// (internal/workflows/testdata/histories) from this run.
var updateHistories = flag.Bool("update-histories", false, "capture workflow histories for the replay suite")

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// factory hands out the base-URL client for every installation.
type factory struct{ c *ghclient.GitHubClient }

func (f factory) CreateInstallationClient(context.Context, int64) (ghclient.Client, error) {
	return f.c, nil
}

func eventually(t *testing.T, within time.Duration, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %v", within)
		}

		time.Sleep(200 * time.Millisecond)
	}
}

// captureHistory writes a workflow's history for the replay suite when
// -update-histories is set.
func captureHistory(t *testing.T, ts *temporaltest.Server, workflowID, runID, name string) {
	t.Helper()

	if !*updateHistories {
		return
	}

	hist := &historypb.History{}

	iter := ts.Client.GetWorkflowHistory(t.Context(), workflowID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		ev, err := iter.Next()
		if err != nil {
			t.Fatalf("history: %v", err)
		}

		hist.Events = append(hist.Events, ev)
	}

	b, err := temporalproto.CustomJSONMarshalOptions{Indent: "  "}.Marshal(hist)
	if err != nil {
		t.Fatalf("marshal history: %v", err)
	}

	path := filepath.Join("..", "workflows", "testdata", "histories", name+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		t.Fatalf("write history: %v", err)
	}
}
