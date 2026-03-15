package reconciler_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/reconciler"
)

func workflowReconciler() reconciler.Reconciler {
	rec, err := reconciler.NewWorkflowSyncReconciler(policy.ReconcilerConfig{
		Type: "workflow_sync",
	})
	if err != nil {
		panic(err)
	}

	return rec
}

func TestWorkflowSync_FilePresent(t *testing.T) {
	t.Parallel()

	client := newMockClient()
	rec := workflowReconciler()

	params := &reconciler.ReconcileParams{
		Client:  client,
		Owner:   "org",
		Repo:    "repo",
		Content: "name: CI\non: push\njobs: {}\n",
		Logger:  slog.Default(),
	}

	err := rec.Reconcile(context.Background(), params)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

func TestWorkflowSync_FileNotFound(t *testing.T) {
	t.Parallel()

	client := newMockClient()
	rec := workflowReconciler()

	params := &reconciler.ReconcileParams{
		Client:  client,
		Owner:   "org",
		Repo:    "repo",
		Content: "",
		Logger:  slog.Default(),
	}

	err := rec.Reconcile(context.Background(), params)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

func TestWorkflowSync_DryRun(t *testing.T) {
	t.Parallel()

	client := newMockClient()
	rec := workflowReconciler()

	params := &reconciler.ReconcileParams{
		Client:  client,
		Owner:   "org",
		Repo:    "repo",
		Content: "name: CI\n",
		DryRun:  true,
		Logger:  slog.Default(),
	}

	err := rec.Reconcile(context.Background(), params)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}
