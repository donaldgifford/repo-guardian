package reconciler_test

import (
	"context"
	"log/slog"
	"testing"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/reconciler"
)

// bpMockClient is a focused mock for branch protection reconciler tests.
type bpMockClient struct {
	mockClient
	rulesets         []*ghclient.Ruleset
	createdRuleset   *ghclient.Ruleset
	updatedRuleset   *ghclient.Ruleset
	updatedRulesetID int64
}

func newBPMockClient() *bpMockClient {
	return &bpMockClient{
		mockClient: mockClient{
			contents:         make(map[string]bool),
			fileContents:     make(map[string]string),
			customProperties: make(map[string][]*ghclient.CustomPropertyValue),
			branchSHAs:       make(map[string]string),
			installRepos:     make(map[int64][]*ghclient.Repository),
		},
	}
}

func (m *bpMockClient) ListRepositoryRulesets(_ context.Context, _, _ string) ([]*ghclient.Ruleset, error) {
	return m.rulesets, nil
}

func (m *bpMockClient) CreateRepositoryRuleset(_ context.Context, _, _ string, rs *ghclient.Ruleset) (*ghclient.Ruleset, error) {
	m.createdRuleset = rs
	rs.ID = 100

	return rs, nil
}

func (m *bpMockClient) UpdateRepositoryRuleset(_ context.Context, _, _ string, id int64, rs *ghclient.Ruleset) (*ghclient.Ruleset, error) {
	m.updatedRuleset = rs
	m.updatedRulesetID = id

	return rs, nil
}

func bpReconciler() reconciler.Reconciler {
	rec, err := reconciler.NewBranchProtectionReconciler(policy.ReconcilerConfig{
		Type: "branch_protection",
	})
	if err != nil {
		panic(err)
	}

	return rec
}

func bpParams(client ghclient.Client, content string, dryRun bool) *reconciler.ReconcileParams {
	return &reconciler.ReconcileParams{
		Client:  client,
		Owner:   "org",
		Repo:    "repo",
		Content: content,
		DryRun:  dryRun,
		Logger:  slog.Default(),
	}
}

func TestBPReconciler_CreatesRuleset(t *testing.T) {
	t.Parallel()

	client := newBPMockClient()
	rec := bpReconciler()

	content := `rules:
  - branch: main
    require_pr: true
    required_approvals: 2
    dismiss_stale_reviews: true
`

	err := rec.Reconcile(context.Background(), bpParams(client, content, false))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if client.createdRuleset == nil {
		t.Fatal("expected ruleset to be created")
	}

	if client.createdRuleset.RequirePullRequest == nil {
		t.Fatal("expected PR requirement")
	}

	if client.createdRuleset.RequirePullRequest.RequiredApprovals != 2 {
		t.Errorf("expected 2 approvals, got %d",
			client.createdRuleset.RequirePullRequest.RequiredApprovals)
	}
}

func TestBPReconciler_UpdatesExistingRuleset(t *testing.T) {
	t.Parallel()

	client := newBPMockClient()
	client.rulesets = []*ghclient.Ruleset{
		{
			ID:         42,
			Conditions: &ghclient.RulesetConditions{IncludePatterns: []string{"refs/heads/main"}},
			RequirePullRequest: &ghclient.RulesetPullRequest{
				RequiredApprovals: 1,
			},
		},
	}
	rec := bpReconciler()

	content := `rules:
  - branch: main
    require_pr: true
    required_approvals: 2
`

	err := rec.Reconcile(context.Background(), bpParams(client, content, false))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if client.updatedRuleset == nil {
		t.Fatal("expected ruleset to be updated")
	}

	if client.updatedRulesetID != 42 {
		t.Errorf("expected update on ruleset 42, got %d", client.updatedRulesetID)
	}
}

func TestBPReconciler_NoChangesWhenMatching(t *testing.T) {
	t.Parallel()

	client := newBPMockClient()
	client.rulesets = []*ghclient.Ruleset{
		{
			ID:         1,
			Conditions: &ghclient.RulesetConditions{IncludePatterns: []string{"refs/heads/main"}},
			RequirePullRequest: &ghclient.RulesetPullRequest{
				RequiredApprovals:   1,
				DismissStaleReviews: true,
			},
		},
	}
	rec := bpReconciler()

	content := `rules:
  - branch: main
    require_pr: true
    required_approvals: 1
    dismiss_stale_reviews: true
`

	err := rec.Reconcile(context.Background(), bpParams(client, content, false))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if client.createdRuleset != nil {
		t.Error("should not create ruleset when matching")
	}

	if client.updatedRuleset != nil {
		t.Error("should not update ruleset when matching")
	}
}

func TestBPReconciler_InvalidYAML(t *testing.T) {
	t.Parallel()

	client := newBPMockClient()
	rec := bpReconciler()

	err := rec.Reconcile(context.Background(), bpParams(client, "not: [yaml: {", false))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestBPReconciler_DryRun(t *testing.T) {
	t.Parallel()

	client := newBPMockClient()
	rec := bpReconciler()

	content := `rules:
  - branch: main
    require_pr: true
    required_approvals: 1
`

	err := rec.Reconcile(context.Background(), bpParams(client, content, true))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if client.createdRuleset != nil {
		t.Error("should not create ruleset in dry run mode")
	}
}
