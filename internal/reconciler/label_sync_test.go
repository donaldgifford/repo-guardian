package reconciler_test

import (
	"context"
	"log/slog"
	"testing"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/reconciler"
)

// labelMockClient is a focused mock for label sync tests.
type labelMockClient struct {
	mockClient
	labels        []*ghclient.Label
	createdLabels []*ghclient.Label
	updatedLabels map[string]*ghclient.Label
	deletedLabels []string
	listLabelsErr error
}

func newLabelMockClient() *labelMockClient {
	return &labelMockClient{
		mockClient: mockClient{
			contents:         make(map[string]bool),
			fileContents:     make(map[string]string),
			customProperties: make(map[string][]*ghclient.CustomPropertyValue),
			branchSHAs:       make(map[string]string),
			installRepos:     make(map[int64][]*ghclient.Repository),
		},
		updatedLabels: make(map[string]*ghclient.Label),
	}
}

func (m *labelMockClient) ListLabels(_ context.Context, _, _ string) ([]*ghclient.Label, error) {
	if m.listLabelsErr != nil {
		return nil, m.listLabelsErr
	}

	return m.labels, nil
}

func (m *labelMockClient) CreateLabel(_ context.Context, _, _ string, label *ghclient.Label) error {
	m.createdLabels = append(m.createdLabels, label)
	return nil
}

func (m *labelMockClient) UpdateLabel(_ context.Context, _, _, name string, label *ghclient.Label) error {
	m.updatedLabels[name] = label
	return nil
}

func (m *labelMockClient) DeleteLabel(_ context.Context, _, _, name string) error {
	m.deletedLabels = append(m.deletedLabels, name)
	return nil
}

func labelReconciler(deleteExtra bool) reconciler.Reconciler {
	rec, err := reconciler.NewLabelSyncReconciler(policy.ReconcilerConfig{
		Type:        "label_sync",
		DeleteExtra: deleteExtra,
	})
	if err != nil {
		panic(err)
	}

	return rec
}

func labelParams(client ghclient.Client, content string, dryRun bool) *reconciler.ReconcileParams {
	return &reconciler.ReconcileParams{
		Client:  client,
		Owner:   "org",
		Repo:    "repo",
		Content: content,
		DryRun:  dryRun,
		Logger:  slog.Default(),
	}
}

func TestLabelSync_CreatesMissingLabels(t *testing.T) {
	t.Parallel()

	client := newLabelMockClient()
	rec := labelReconciler(false)

	content := `labels:
  - name: bug
    color: "d73a4a"
    description: Something isn't working
  - name: enhancement
    color: "a2eeef"
    description: New feature or request
`

	err := rec.Reconcile(context.Background(), labelParams(client, content, false))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.createdLabels) != 2 {
		t.Fatalf("expected 2 labels created, got %d", len(client.createdLabels))
	}

	if client.createdLabels[0].Name != "bug" && client.createdLabels[1].Name != "bug" {
		t.Error("expected 'bug' label to be created")
	}
}

func TestLabelSync_UpdatesChangedLabels(t *testing.T) {
	t.Parallel()

	client := newLabelMockClient()
	client.labels = []*ghclient.Label{
		{Name: "bug", Color: "d73a4a", Description: "Old description"},
	}
	rec := labelReconciler(false)

	content := `labels:
  - name: bug
    color: "d73a4a"
    description: Something isn't working
`

	err := rec.Reconcile(context.Background(), labelParams(client, content, false))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.createdLabels) != 0 {
		t.Error("should not create existing label")
	}

	if len(client.updatedLabels) != 1 {
		t.Fatalf("expected 1 label updated, got %d", len(client.updatedLabels))
	}

	updated := client.updatedLabels["bug"]
	if updated.Description != "Something isn't working" {
		t.Errorf("expected updated description, got %q", updated.Description)
	}
}

func TestLabelSync_RenamesLabels(t *testing.T) {
	t.Parallel()

	client := newLabelMockClient()
	client.labels = []*ghclient.Label{
		{Name: "wontfix", Color: "ffffff", Description: "Will not fix"},
	}
	rec := labelReconciler(false)

	content := `labels:
  - name: "won't fix"
    color: "ffffff"
    description: "Will not be fixed"
    renamed_from: wontfix
`

	err := rec.Reconcile(context.Background(), labelParams(client, content, false))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	updated := client.updatedLabels["wontfix"]
	if updated == nil {
		t.Fatal("expected label to be renamed")
	}

	if updated.Name != "won't fix" {
		t.Errorf("expected renamed label name %q, got %q", "won't fix", updated.Name)
	}
}

func TestLabelSync_DeletesExtraLabels(t *testing.T) {
	t.Parallel()

	client := newLabelMockClient()
	client.labels = []*ghclient.Label{
		{Name: "bug", Color: "d73a4a", Description: "Something isn't working"},
		{Name: "extra", Color: "000000", Description: "Should be deleted"},
	}
	rec := labelReconciler(true)

	content := `labels:
  - name: bug
    color: "d73a4a"
    description: Something isn't working
`

	err := rec.Reconcile(context.Background(), labelParams(client, content, false))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.deletedLabels) != 1 {
		t.Fatalf("expected 1 label deleted, got %d", len(client.deletedLabels))
	}

	if client.deletedLabels[0] != "extra" {
		t.Errorf("expected 'extra' deleted, got %q", client.deletedLabels[0])
	}
}

func TestLabelSync_KeepsExtraLabels(t *testing.T) {
	t.Parallel()

	client := newLabelMockClient()
	client.labels = []*ghclient.Label{
		{Name: "bug", Color: "d73a4a", Description: "Something isn't working"},
		{Name: "extra", Color: "000000", Description: "Should be kept"},
	}
	rec := labelReconciler(false)

	content := `labels:
  - name: bug
    color: "d73a4a"
    description: Something isn't working
`

	err := rec.Reconcile(context.Background(), labelParams(client, content, false))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.deletedLabels) != 0 {
		t.Errorf("expected 0 labels deleted, got %d", len(client.deletedLabels))
	}
}

func TestLabelSync_EmptyLabelFile(t *testing.T) {
	t.Parallel()

	client := newLabelMockClient()
	rec := labelReconciler(false)

	err := rec.Reconcile(context.Background(), labelParams(client, "labels: []\n", false))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.createdLabels) != 0 {
		t.Error("should not create labels from empty file")
	}
}

func TestLabelSync_InvalidYAML(t *testing.T) {
	t.Parallel()

	client := newLabelMockClient()
	rec := labelReconciler(false)

	err := rec.Reconcile(context.Background(), labelParams(client, "not: [yaml: {", false))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLabelSync_DryRun(t *testing.T) {
	t.Parallel()

	client := newLabelMockClient()
	rec := labelReconciler(true)

	content := `labels:
  - name: new-label
    color: "ff0000"
    description: New label
`

	err := rec.Reconcile(context.Background(), labelParams(client, content, true))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.createdLabels) != 0 {
		t.Error("should not create labels in dry run mode")
	}
}
