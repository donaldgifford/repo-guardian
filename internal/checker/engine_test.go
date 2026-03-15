package checker

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/rules"
)

// mockClient implements ghclient.Client for testing.
type mockClient struct {
	contents         map[string]bool                            // "owner/repo/path" -> exists
	fileContents     map[string]string                          // "owner/repo/path" -> content
	customProperties map[string][]*ghclient.CustomPropertyValue // "owner/repo" -> values
	setProperties    []*ghclient.CustomPropertyValue            // records what was set
	openPRs          []*ghclient.PullRequest
	repo             *ghclient.Repository
	branchSHAs       map[string]string // "owner/repo/branch" -> sha
	createdBranches  []string
	deletedBranches  []string
	createdFiles     []string
	createdPR        *ghclient.PullRequest
	installations    []*ghclient.Installation
	installRepos     map[int64][]*ghclient.Repository
	processedJobs    atomic.Int32

	getRepoErr        error
	getContentsErr    error
	getFileContentErr error
	getCustomPropsErr error
	setCustomPropsErr error
	listPRsErr        error
	getBranchErr      error
	createBranchErr   error
	deleteBranchErr   error
	createFileErr     error
	createPRErr       error
}

func newMockClient() *mockClient {
	return &mockClient{
		contents:         make(map[string]bool),
		fileContents:     make(map[string]string),
		customProperties: make(map[string][]*ghclient.CustomPropertyValue),
		branchSHAs:       make(map[string]string),
		installRepos:     make(map[int64][]*ghclient.Repository),
	}
}

func (m *mockClient) GetContents(_ context.Context, owner, repo, path string) (bool, error) {
	if m.getContentsErr != nil {
		return false, m.getContentsErr
	}

	key := fmt.Sprintf("%s/%s/%s", owner, repo, path)

	return m.contents[key], nil
}

func (m *mockClient) ListOpenPullRequests(_ context.Context, _, _ string) ([]*ghclient.PullRequest, error) {
	if m.listPRsErr != nil {
		return nil, m.listPRsErr
	}

	return m.openPRs, nil
}

func (m *mockClient) GetRepository(_ context.Context, _, _ string) (*ghclient.Repository, error) {
	if m.getRepoErr != nil {
		return nil, m.getRepoErr
	}

	m.processedJobs.Add(1)

	return m.repo, nil
}

func (m *mockClient) GetBranchSHA(_ context.Context, owner, repo, branch string) (string, error) {
	if m.getBranchErr != nil {
		return "", m.getBranchErr
	}

	key := fmt.Sprintf("%s/%s/%s", owner, repo, branch)

	return m.branchSHAs[key], nil
}

func (m *mockClient) CreateBranch(_ context.Context, _, _, branch, _ string) error {
	if m.createBranchErr != nil {
		return m.createBranchErr
	}

	m.createdBranches = append(m.createdBranches, branch)

	return nil
}

func (m *mockClient) DeleteBranch(_ context.Context, _, _, branch string) error {
	if m.deleteBranchErr != nil {
		return m.deleteBranchErr
	}

	m.deletedBranches = append(m.deletedBranches, branch)

	return nil
}

func (m *mockClient) CreateOrUpdateFile(_ context.Context, _, _, _, path, _, _ string) error {
	if m.createFileErr != nil {
		return m.createFileErr
	}

	m.createdFiles = append(m.createdFiles, path)

	return nil
}

func (m *mockClient) CreatePullRequest(_ context.Context, _, _, title, _, head, _ string) (*ghclient.PullRequest, error) {
	if m.createPRErr != nil {
		return nil, m.createPRErr
	}

	m.createdPR = &ghclient.PullRequest{
		Number: 1,
		Title:  title,
		Head:   head,
		State:  "open",
	}

	return m.createdPR, nil
}

func (m *mockClient) ListInstallations(_ context.Context) ([]*ghclient.Installation, error) {
	return m.installations, nil
}

func (m *mockClient) ListInstallationRepos(_ context.Context, installationID int64) ([]*ghclient.Repository, error) {
	return m.installRepos[installationID], nil
}

func (m *mockClient) CreateInstallationClient(_ context.Context, _ int64) (ghclient.Client, error) {
	return m, nil
}

func (m *mockClient) GetFileContent(_ context.Context, owner, repo, path string) (string, error) {
	if m.getFileContentErr != nil {
		return "", m.getFileContentErr
	}

	key := fmt.Sprintf("%s/%s/%s", owner, repo, path)

	return m.fileContents[key], nil
}

func (m *mockClient) GetCustomPropertyValues(_ context.Context, owner, repo string) ([]*ghclient.CustomPropertyValue, error) {
	if m.getCustomPropsErr != nil {
		return nil, m.getCustomPropsErr
	}

	key := fmt.Sprintf("%s/%s", owner, repo)

	return m.customProperties[key], nil
}

func (m *mockClient) SetCustomPropertyValues(_ context.Context, _, _ string, properties []*ghclient.CustomPropertyValue) error {
	if m.setCustomPropsErr != nil {
		return m.setCustomPropsErr
	}

	m.setProperties = append(m.setProperties, properties...)

	return nil
}

func testEngine(dryRun bool) *Engine {
	reg := rules.NewRegistry(rules.DefaultRules)
	ts := rules.NewTemplateStore()

	if err := ts.Load(""); err != nil {
		panic(err)
	}

	return NewEngine(reg, ts, slog.Default(), true, true, dryRun)
}

func TestCheckRepo_AllFilesExist(t *testing.T) {
	t.Parallel()

	engine := testEngine(false)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}

	// All enabled files exist.
	client.contents["org/repo/CODEOWNERS"] = true
	client.contents["org/repo/.github/dependabot.yml"] = true

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR != nil {
		t.Error("should not create PR when all files exist")
	}
}

func TestCheckRepo_MissingFiles_NoPR(t *testing.T) {
	t.Parallel()

	engine := testEngine(false)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}

	// No files exist, no open PRs.
	client.branchSHAs["org/repo/main"] = "abc123"

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR == nil {
		t.Fatal("expected PR to be created")
	}

	if len(client.createdBranches) != 1 {
		t.Errorf("expected 1 branch created, got %d", len(client.createdBranches))
	}

	if client.createdBranches[0] != BranchName {
		t.Errorf("expected branch %q, got %q", BranchName, client.createdBranches[0])
	}

	// Should create files for both enabled rules (CODEOWNERS + Dependabot).
	if len(client.createdFiles) != 2 {
		t.Errorf("expected 2 files created, got %d: %v", len(client.createdFiles), client.createdFiles)
	}
}

func TestCheckRepo_MissingFiles_ExistingPR(t *testing.T) {
	t.Parallel()

	engine := testEngine(false)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"
	client.branchSHAs["org/repo/"+BranchName] = "def456"

	// Our own PR already exists and is open.
	client.openPRs = []*ghclient.PullRequest{
		{Number: 5, Title: PRTitle, Head: BranchName, State: "open"},
	}

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	// Should not create a new PR, but should create files.
	if client.createdPR != nil {
		t.Error("should not create a new PR when one already exists")
	}

	if len(client.createdFiles) != 2 {
		t.Errorf("expected 2 files updated, got %d", len(client.createdFiles))
	}

	// Should not delete the branch since PR is open.
	if len(client.deletedBranches) != 0 {
		t.Errorf("should not delete branch when PR is open, deleted: %v", client.deletedBranches)
	}
}

func TestCheckRepo_MissingFiles_ThirdPartyPR(t *testing.T) {
	t.Parallel()

	engine := testEngine(false)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"

	// Third-party PR that addresses CODEOWNERS.
	client.openPRs = []*ghclient.PullRequest{
		{Number: 10, Title: "Add CODEOWNERS file", Head: "add-codeowners", State: "open"},
	}

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	// Should only create file for Dependabot (CODEOWNERS has existing PR).
	if client.createdPR == nil {
		t.Fatal("expected PR to be created for Dependabot")
	}

	if len(client.createdFiles) != 1 {
		t.Fatalf("expected 1 file created (Dependabot only), got %d: %v", len(client.createdFiles), client.createdFiles)
	}

	if client.createdFiles[0] != ".github/dependabot.yml" {
		t.Errorf("expected dependabot file, got %q", client.createdFiles[0])
	}
}

func TestCheckRepo_Archived(t *testing.T) {
	t.Parallel()

	engine := testEngine(false)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", Archived: true, HasBranch: true, DefaultRef: "main",
	}

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR != nil {
		t.Error("should not create PR for archived repo")
	}
}

func TestCheckRepo_Fork(t *testing.T) {
	t.Parallel()

	engine := testEngine(false)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", Fork: true, HasBranch: true, DefaultRef: "main",
	}

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR != nil {
		t.Error("should not create PR for forked repo")
	}
}

func TestCheckRepo_EmptyRepo(t *testing.T) {
	t.Parallel()

	engine := testEngine(false)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: false, DefaultRef: "",
	}

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR != nil {
		t.Error("should not create PR for empty repo")
	}
}

func TestCheckRepo_DryRun(t *testing.T) {
	t.Parallel()

	engine := testEngine(true)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR != nil {
		t.Error("dry run should not create PR")
	}

	if len(client.createdBranches) != 0 {
		t.Error("dry run should not create branches")
	}
}

func TestCheckRepo_StaleBranchCleanup(t *testing.T) {
	t.Parallel()

	engine := testEngine(false)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"
	client.branchSHAs["org/repo/"+BranchName] = "stale-sha"

	// Branch exists but no open PR (previously closed).

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	// Should delete the stale branch.
	if len(client.deletedBranches) != 1 {
		t.Fatalf("expected 1 branch deleted, got %d", len(client.deletedBranches))
	}

	if client.deletedBranches[0] != BranchName {
		t.Errorf("expected branch %q deleted, got %q", BranchName, client.deletedBranches[0])
	}

	// Should create a new branch and PR.
	if client.createdPR == nil {
		t.Error("expected new PR after stale branch cleanup")
	}

	if len(client.createdBranches) != 1 {
		t.Errorf("expected 1 branch created, got %d", len(client.createdBranches))
	}
}

func TestBuildPRBody(t *testing.T) {
	t.Parallel()

	missing := []rules.FileRule{
		{Name: "CODEOWNERS", TargetPath: ".github/CODEOWNERS"},
		{Name: "Dependabot", TargetPath: ".github/dependabot.yml"},
	}

	body := BuildPRBody(missing)

	if !strings.Contains(body, "Repo Guardian") {
		t.Error("PR body should contain 'Repo Guardian'")
	}

	if !strings.Contains(body, ".github/CODEOWNERS") {
		t.Error("PR body should list CODEOWNERS path")
	}

	if !strings.Contains(body, ".github/dependabot.yml") {
		t.Error("PR body should list dependabot path")
	}

	if !strings.Contains(body, "@org/CHANGEME") {
		t.Error("PR body should mention CODEOWNERS placeholder")
	}

	if !strings.Contains(body, "platform-engineering") {
		t.Error("PR body should reference platform-engineering channel")
	}
}
