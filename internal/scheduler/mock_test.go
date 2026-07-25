package scheduler

import (
	"context"
	"fmt"
	"time"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
)

// mockClient implements ghclient.Client for scheduler tests.
type mockClient struct {
	installations []*ghclient.Installation
	installRepos  map[int64][]*ghclient.Repository

	listInstallErr error
	listReposErr   error
}

func newMockClient() *mockClient {
	return &mockClient{
		installRepos: make(map[int64][]*ghclient.Repository),
	}
}

func (*mockClient) GetContents(_ context.Context, _, _, _ string) (bool, error) {
	return false, fmt.Errorf("not implemented")
}

func (*mockClient) ListOpenPullRequests(_ context.Context, _, _ string) ([]*ghclient.PullRequest, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) GetRepository(_ context.Context, _, _ string) (*ghclient.Repository, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) GetBranchSHA(_ context.Context, _, _, _ string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (*mockClient) CreateBranch(_ context.Context, _, _, _, _ string) error {
	return fmt.Errorf("not implemented")
}

func (*mockClient) DeleteBranch(_ context.Context, _, _, _ string) error {
	return fmt.Errorf("not implemented")
}

func (*mockClient) CreateOrUpdateFile(_ context.Context, _, _, _, _, _, _ string) error {
	return fmt.Errorf("not implemented")
}

func (*mockClient) CreatePullRequest(_ context.Context, _, _, _, _, _, _ string) (*ghclient.PullRequest, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) AddLabelsToPR(_ context.Context, _, _ string, _ int, _ []string) error {
	return nil
}

func (m *mockClient) ListInstallations(_ context.Context) ([]*ghclient.Installation, error) {
	if m.listInstallErr != nil {
		return nil, m.listInstallErr
	}

	return m.installations, nil
}

func (m *mockClient) ListInstallationRepos(_ context.Context, installationID int64) ([]*ghclient.Repository, error) {
	if m.listReposErr != nil {
		return nil, m.listReposErr
	}

	return m.installRepos[installationID], nil
}

func (*mockClient) CreateInstallationClient(_ context.Context, _ int64) (ghclient.Client, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) GetFileContent(_ context.Context, _, _, _ string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (*mockClient) GetCustomPropertyValues(_ context.Context, _, _ string) ([]*ghclient.CustomPropertyValue, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) SetCustomPropertyValues(_ context.Context, _, _ string, _ []*ghclient.CustomPropertyValue) error {
	return fmt.Errorf("not implemented")
}

func (*mockClient) GetOrgPropertySchema(_ context.Context, _ string) ([]string, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) GetVulnerabilityAlertsEnabled(_ context.Context, _, _ string) (bool, error) {
	return false, fmt.Errorf("not implemented")
}

func (*mockClient) EnableVulnerabilityAlerts(_ context.Context, _, _ string) error {
	return fmt.Errorf("not implemented")
}

func (*mockClient) DisableVulnerabilityAlerts(_ context.Context, _, _ string) error {
	return fmt.Errorf("not implemented")
}

func (*mockClient) GetRepoSettings(_ context.Context, _, _ string) (*ghclient.RepoSettings, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) UpdateRepository(_ context.Context, _, _ string, _ *ghclient.RepoUpdateOpts) error {
	return fmt.Errorf("not implemented")
}

func (*mockClient) ListRepositoryRulesets(_ context.Context, _, _ string) ([]*ghclient.Ruleset, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) GetRepositoryRuleset(_ context.Context, _, _ string, _ int64) (*ghclient.Ruleset, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) CreateRepositoryRuleset(_ context.Context, _, _ string, _ *ghclient.Ruleset) (*ghclient.Ruleset, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) UpdateRepositoryRuleset(_ context.Context, _, _ string, _ int64, _ *ghclient.Ruleset) (*ghclient.Ruleset, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) ListLabels(_ context.Context, _, _ string) ([]*ghclient.Label, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) CreateLabel(_ context.Context, _, _ string, _ *ghclient.Label) error {
	return fmt.Errorf("not implemented")
}

func (*mockClient) UpdateLabel(_ context.Context, _, _, _ string, _ *ghclient.Label) error {
	return fmt.Errorf("not implemented")
}

func (*mockClient) DeleteLabel(_ context.Context, _, _, _ string) error {
	return fmt.Errorf("not implemented")
}

func (*mockClient) RateLimitRemaining(_ context.Context, _ int64) (int, int, time.Time, error) {
	return 5000, 5000, time.Time{}, nil
}

func (*mockClient) GetContentsOnBranch(_ context.Context, _, _, _, _ string) (string, bool, error) {
	return "", false, nil
}

func (*mockClient) DeleteFile(_ context.Context, _, _, _, _, _, _ string) error {
	return nil
}

func (*mockClient) UpdatePullRequest(_ context.Context, _, _ string, _ int, _, _ string) error {
	return nil
}

func (*mockClient) UpdatePRBranch(_ context.Context, _, _ string, _ int) error {
	return nil
}

func (*mockClient) ClosePullRequest(_ context.Context, _, _ string, _ int) error {
	return nil
}

func (*mockClient) ListPRComments(_ context.Context, _, _ string, _ int) ([]*ghclient.Comment, error) {
	return nil, nil
}

func (*mockClient) UpsertPRComment(_ context.Context, _, _ string, _ int, _, _ string) error {
	return nil
}
