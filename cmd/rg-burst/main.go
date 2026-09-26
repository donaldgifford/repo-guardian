//go:build burst

// Command rg-burst load-tests one InstallationWorkflow (IMPL-0025 11.7):
// N acquire/report pairs against a single installation, reporting
// acquire latency percentiles and the resulting history size, which set
// the ContinueAsNew threshold (InstallationWorkflowInput.MaxHandled).
//
// It runs its own worker on a dedicated task queue, so it never touches
// production executions. Point it at a cluster with the usual
// TEMPORAL_* environment:
//
//	go run -tags burst ./cmd/rg-burst -n 20000 -concurrency 50
//
// Frontend CPU is read from the cluster's own dashboards while it runs.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"github.com/donaldgifford/repo-guardian/internal/activities"
	"github.com/donaldgifford/repo-guardian/internal/temporal"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

const taskQueue = "rg-burst"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "rg-burst:", err)
		os.Exit(1)
	}
}

func run() error {
	n := flag.Int("n", 20000, "acquire/report pairs")
	concurrency := flag.Int("concurrency", 50, "concurrent callers")
	installation := flag.Int64("installation", time.Now().Unix(), "installation id (fresh by default)")
	maxHandled := flag.Int("max-handled", 0, "ContinueAsNew bound under test (0 = default)")
	flag.Parse()

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	cfg, err := temporal.ConfigFromEnv()
	if err != nil {
		return err
	}

	c, err := temporal.Dial(ctx, &cfg, temporal.DialOptions{Logger: logger})
	if err != nil {
		return err
	}
	defer c.Close()

	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflowWithOptions(workflows.InstallationWorkflow, workflow.RegisterOptions{Name: workflows.InstallationWorkflowName})

	if err := w.Start(); err != nil {
		return err
	}
	defer w.Stop()

	budget := activities.NewBudget(c, taskQueue, 0.10)
	budget.MaxHandled = *maxHandled
	id := workflows.InstallationWorkflowID(*installation)

	latencies := make([]time.Duration, *n)
	jobs := make(chan int)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		failed   int
		firstErr error
	)

	start := time.Now()

	for range *concurrency {
		wg.Go(func() {
			for i := range jobs {
				t0 := time.Now()

				res, err := budget.AcquireBudget(ctx, &workflows.AcquireInput{
					InstallationID: *installation,
					UpdateID:       "burst/" + strconv.Itoa(i),
					Request:        workflows.AcquireRequest{Holder: "burst", Priority: workflows.PrioritySchedule},
				})

				latencies[i] = time.Since(t0)

				if err == nil {
					err = c.SignalWorkflow(ctx, id, "", workflows.ReportSignal, &workflows.Report{LeaseID: res.LeaseID, Calls: 12})
				}

				if err != nil {
					mu.Lock()
					failed++

					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
				}
			}
		})
	}

	for i := range *n {
		jobs <- i
	}

	close(jobs)
	wg.Wait()

	elapsed := time.Since(start)

	desc, err := c.DescribeWorkflowExecution(ctx, id, "")
	if err != nil {
		return err
	}

	info := desc.GetWorkflowExecutionInfo()
	slices.Sort(latencies)

	fmt.Printf("pairs=%d failed=%d elapsed=%s rate=%.0f/s\n", *n, failed, elapsed.Round(time.Millisecond), float64(*n)/elapsed.Seconds())
	if firstErr != nil {
		fmt.Printf("first failure: %v\n", firstErr)
	}

	fmt.Printf("acquire p50=%s p99=%s max=%s\n", pct(latencies, 50), pct(latencies, 99), latencies[len(latencies)-1])
	fmt.Printf("current run: history_length=%d history_size_bytes=%d\n", info.GetHistoryLength(), info.GetHistorySizeBytes())

	return nil
}

func pct(sorted []time.Duration, p int) time.Duration {
	return sorted[(len(sorted)-1)*p/100].Round(time.Microsecond)
}
