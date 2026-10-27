//go:build integration

package temporal_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/client"
	temporalsdk "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"github.com/donaldgifford/repo-guardian/internal/temporal/temporaltest"
)

// NOTE: IMPL-0028 task 0.10 (INV-0022 spike 5, F4). These tests measure
// Temporal behaviour the controls workflows depend on; results are
// recorded in INV-0022's Phase-0 addendum. They use toy workflows, not
// the controls types, which do not exist yet.

const spikeSignal = "recheck"

// spikeRemediation is RemediationWorkflow's end-of-run shape: started by
// signal-with-start, one slow activity, then (when drain is set) a
// non-blocking drain of the signal channel before completing. It returns
// how many signals it consumed.
func spikeRemediation(ctx workflow.Context, drain bool) (int, error) {
	ch := workflow.GetSignalChannel(ctx, spikeSignal)

	var v int

	ch.Receive(ctx, &v)
	n := 1

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
	if err := workflow.ExecuteActivity(ctx, spikeWork).Get(ctx, nil); err != nil {
		return n, err
	}

	if drain {
		for ch.ReceiveAsync(&v) {
			n++
		}
	}

	return n, nil
}

func spikeWork(context.Context) error {
	time.Sleep(30 * time.Millisecond)
	return nil
}

// TestSpike_SignalWithStartIntoCompletingWorkflow sends signals by
// signal-with-start at a workflow that keeps completing, and counts what
// the runs consumed. With the drain, nothing may be lost; without it the
// spike records how many are.
func TestSpike_SignalWithStartIntoCompletingWorkflow(t *testing.T) {
	srv := temporaltest.Start(t)

	const queue = "spike-sws"

	w := worker.New(srv.Client, queue, worker.Options{})
	w.RegisterWorkflow(spikeRemediation)
	w.RegisterActivity(spikeWork)

	if err := w.Start(); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(w.Stop)

	for _, drain := range []bool{true, false} {
		t.Run(fmt.Sprintf("drain=%v", drain), func(t *testing.T) {
			sent, consumed := runSignalStorm(t, srv.Client, queue, drain)
			t.Logf("drain=%v sent=%d consumed=%d lost=%d", drain, sent, consumed, sent-consumed)

			if drain && consumed != sent {
				t.Errorf("with drain: consumed %d of %d signals", consumed, sent)
			}
		})
	}
}

func runSignalStorm(t *testing.T, c client.Client, queue string, drain bool) (sent, consumed int) {
	t.Helper()

	const (
		senders   = 4
		perSender = 50
	)

	id := fmt.Sprintf("spike-sws-%v", drain)
	ctx := t.Context()

	var (
		mu   sync.Mutex
		runs = map[string]bool{}
		wg   sync.WaitGroup
	)

	for s := range senders {
		wg.Go(func() {
			for i := range perSender {
				run, err := c.SignalWithStartWorkflow(ctx, id, spikeSignal, s*perSender+i,
					client.StartWorkflowOptions{TaskQueue: queue}, spikeRemediation, drain)
				if err != nil {
					t.Errorf("signal-with-start: %v", err)
					return
				}

				mu.Lock()
				runs[run.GetRunID()] = true
				mu.Unlock()

				time.Sleep(time.Duration(5+(i*7)%20) * time.Millisecond)
			}
		})
	}

	wg.Wait()

	for runID := range runs {
		var n int
		if err := c.GetWorkflow(ctx, id, runID).Get(ctx, &n); err != nil {
			t.Fatalf("run %s: %v", runID, err)
		}

		consumed += n
	}

	t.Logf("drain=%v runs=%d", drain, len(runs))

	return senders * perSender, consumed
}

// spikeEvaluation is EvaluationWorkflow's loop shape: each iteration takes
// one recheck signal carrying changed paths, unions them into the carried
// set, runs one batched start activity, and continues as new after
// maxIter iterations with the union as input.
func spikeEvaluation(ctx workflow.Context, carried []string, maxIter int) error {
	ch := workflow.GetSignalChannel(ctx, spikeSignal)
	actx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})

	union := map[string]struct{}{}
	for _, p := range carried {
		union[p] = struct{}{}
	}

	for range maxIter {
		var paths []string

		ch.Receive(ctx, &paths)

		for _, p := range paths {
			union[p] = struct{}{}
		}

		if err := workflow.ExecuteActivity(actx, spikeStartBatch, []string{"codeowners", "dependency_updates"}).Get(ctx, nil); err != nil {
			return err
		}
	}

	return nil
}

func spikeStartBatch(context.Context, []string) error { return nil }

// TestSpike_EvaluationHistoryGrowth measures one run's history under
// maximum-size changed-path signals against the 100-iteration
// ContinueAsNew bound. Paths are 60 bytes, the length of a deep monorepo
// path.
func TestSpike_EvaluationHistoryGrowth(t *testing.T) {
	srv := temporaltest.Start(t)

	const queue = "spike-history"

	w := worker.New(srv.Client, queue, worker.Options{})
	w.RegisterWorkflow(spikeEvaluation)
	w.RegisterActivity(spikeStartBatch)

	if err := w.Start(); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(w.Stop)

	cases := []struct {
		name  string
		paths int
		iters int
	}{
		{"policy-filtered-200", 200, 100},
		{"one-path-per-commit-2048", 2048, 100},
		{"raw-20000", 20000, 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			id := "spike-history-" + tc.name

			opts := client.StartWorkflowOptions{ID: id, TaskQueue: queue}

			run, err := srv.Client.ExecuteWorkflow(ctx, opts, spikeEvaluation, []string(nil), tc.iters)
			if err != nil {
				t.Fatal(err)
			}

			for i := range tc.iters {
				if err := srv.Client.SignalWorkflow(ctx, id, run.GetRunID(), spikeSignal, spikePaths(tc.paths, i)); err != nil {
					t.Fatalf("signal %d: %v", i, err)
				}
			}

			if err := run.Get(ctx, nil); err != nil {
				t.Fatal(err)
			}

			desc, err := srv.Client.DescribeWorkflowExecution(ctx, id, run.GetRunID())
			if err != nil {
				t.Fatal(err)
			}

			info := desc.GetWorkflowExecutionInfo()
			perIter := info.GetHistorySizeBytes() / int64(tc.iters)
			t.Logf("%s: iterations=%d events=%d bytes=%d bytes/iteration=%d projected-100=%d",
				tc.name, tc.iters, info.GetHistoryLength(), info.GetHistorySizeBytes(), perIter, perIter*100)
		})
	}
}

// spikePaths returns n distinct 60-byte paths; i varies them per signal so
// each iteration carries new paths.
func spikePaths(n, i int) []string {
	out := make([]string, n)
	for j := range out {
		p := fmt.Sprintf("services/team-%03d/internal/handlers/v2/handler_%05d_%03d", j%1000, j, i%1000)
		out[j] = p + strings.Repeat("x", max(0, 60-len(p)))
	}

	return out
}

// TestSpike_BacklogMetricLabels starts workflows on two task queues with
// no worker, so each queue holds a backlog, then scrapes the dev server's
// metrics for approximate_backlog_count and logs every series. The labels
// are what Phase 5's KEDA query selects on.
func TestSpike_BacklogMetricLabels(t *testing.T) {
	port := freePort(t)

	srv, err := testsuite.StartDevServer(t.Context(), testsuite.DevServerOptions{
		CachedDownload: testsuite.CachedDownload{Version: temporaltest.CLIVersion},
		ClientOptions:  &client.Options{Namespace: "repo-guardian"},
		LogLevel:       "error",
		ExtraArgs: []string{
			"--metrics-port", strconv.Itoa(port),
			"--dynamic-config-value", "matching.enableFairness=true",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = srv.Stop() })

	c := srv.Client()
	ctx := t.Context()

	for _, q := range []string{"repo-guardian-eval", "repo-guardian-remediate"} {
		for i := range 5 {
			_, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
				ID:        fmt.Sprintf("%s-%d", q, i),
				TaskQueue: q,
				Priority:  temporalsdk.Priority{PriorityKey: 1 + i%3, FairnessKey: "installation-7"},
			}, "backlog")
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	deadline := time.Now().Add(90 * time.Second)

	var lines []string

	for time.Now().Before(deadline) {
		// Temporal sanitises label values: repo-guardian-eval is
		// repo_guardian_eval on the wire.
		lines = slices.DeleteFunc(scrape(t, port, "repo_guardian_"), func(l string) bool {
			return !strings.Contains(l, "approximate_backlog")
		})
		if len(lines) > 0 {
			break
		}

		time.Sleep(5 * time.Second)
	}

	if len(lines) == 0 {
		t.Log("no approximate_backlog* series within 90s")
	}

	for _, l := range lines {
		t.Log(l)
	}
}

func scrape(t *testing.T, port int, substr string) []string {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/metrics", port), http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("scrape: %v", err)
		return nil
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	var out []string

	for l := range strings.SplitSeq(string(b), "\n") {
		if strings.Contains(l, substr) && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}

	return out
}

func freePort(t *testing.T) int {
	t.Helper()

	var lc net.ListenConfig

	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	return l.Addr().(*net.TCPAddr).Port
}
