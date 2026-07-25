package reconciler_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/donaldgifford/repo-guardian/internal/catalog"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/reconciler"
	"github.com/donaldgifford/repo-guardian/internal/rules"
	tmpl "github.com/donaldgifford/repo-guardian/internal/template"
)

const validCatalogInfo = `---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: my-service
  annotations:
    jira/project-key: "PROJ"
    jira/label: "my-service"
spec:
  owner: platform-team
  lifecycle: production
  type: service
`

// mockClient implements ghclient.Client for testing.
type mockClient struct {
	contents         map[string]bool
	fileContents     map[string]string
	customProperties map[string][]*ghclient.CustomPropertyValue
	setProperties    []*ghclient.CustomPropertyValue
	openPRs          []*ghclient.PullRequest
	repo             *ghclient.Repository
	branchSHAs       map[string]string
	createdBranches  []string
	deletedBranches  []string
	createdFiles     []string
	createdFileBody  map[string]string
	createdPR        *ghclient.PullRequest
	createdPRBody    string
	addedLabels      []string
	installations    []*ghclient.Installation
	installRepos     map[int64][]*ghclient.Repository
	processedJobs    atomic.Int32

	orgSchema      map[string][]string
	orgSchemaErr   error
	orgSchemaCalls atomic.Int32
	orgSchemaDelay time.Duration // artificial latency to widen the concurrent-miss race window in tests

	// setPropsCalls counts SetCustomPropertyValues invocations, kept
	// separate from setProperties (which accumulates payload entries)
	// so a test can assert "how many PATCH round-trips" independently
	// of how many properties each carried.
	setPropsCalls atomic.Int32

	// setPropsMu guards setProperties AND customProperties: a write
	// lands in both (list-then-act fidelity), and the one test that
	// calls Reconcile concurrently across goroutines sharing this
	// client exercises real concurrent reads and writes.
	setPropsMu sync.Mutex

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
		createdFileBody:  make(map[string]string),
		branchSHAs:       make(map[string]string),
		installRepos:     make(map[int64][]*ghclient.Repository),
		orgSchema:        make(map[string][]string),
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

func (m *mockClient) CreateOrUpdateFile(_ context.Context, _, _, _, path, content, _ string) error {
	if m.createFileErr != nil {
		return m.createFileErr
	}

	m.createdFiles = append(m.createdFiles, path)
	m.createdFileBody[path] = content

	return nil
}

func (m *mockClient) CreatePullRequest(_ context.Context, _, _, title, body, head, _ string) (*ghclient.PullRequest, error) {
	if m.createPRErr != nil {
		return nil, m.createPRErr
	}

	m.createdPRBody = body
	m.createdPR = &ghclient.PullRequest{
		Number: 1,
		Title:  title,
		Head:   head,
		State:  "open",
	}

	return m.createdPR, nil
}

func (m *mockClient) AddLabelsToPR(_ context.Context, _, _ string, _ int, labels []string) error {
	m.addedLabels = append(m.addedLabels, labels...)
	return nil
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

	m.setPropsMu.Lock()
	defer m.setPropsMu.Unlock()

	return m.customProperties[key], nil
}

func (m *mockClient) SetCustomPropertyValues(_ context.Context, owner, repo string, properties []*ghclient.CustomPropertyValue) error {
	if m.setCustomPropsErr != nil {
		return m.setCustomPropsErr
	}

	m.setPropsCalls.Add(1)

	m.setPropsMu.Lock()
	defer m.setPropsMu.Unlock()

	m.setProperties = append(m.setProperties, properties...)

	// List-then-act fidelity (CLAUDE.md): a later GetCustomPropertyValues
	// must observe this write the way GitHub would. Without it a
	// "converges to zero PATCHes" assertion is vacuous — the second
	// sweep would re-diff against stale state and act again for reasons
	// that have nothing to do with the behavior under test.
	key := fmt.Sprintf("%s/%s", owner, repo)
	m.customProperties[key] = mergeProperties(m.customProperties[key], properties)

	return nil
}

// mergeProperties applies a PATCH payload onto the current property
// list, replacing values by name and appending names not yet present.
func mergeProperties(current, writes []*ghclient.CustomPropertyValue) []*ghclient.CustomPropertyValue {
	merged := make([]*ghclient.CustomPropertyValue, 0, len(current)+len(writes))
	index := make(map[string]int, len(current))

	for _, p := range current {
		index[p.PropertyName] = len(merged)
		merged = append(merged, p)
	}

	for _, w := range writes {
		if i, ok := index[w.PropertyName]; ok {
			merged[i] = w
			continue
		}

		index[w.PropertyName] = len(merged)
		merged = append(merged, w)
	}

	return merged
}

// GetOrgPropertySchema fails open (returns an error) for any org the
// test hasn't explicitly configured via m.orgSchema, so tests that
// predate schema preflight keep exercising the unfiltered payload
// path without every one of them needing to set up a schema. Tests
// that want to exercise the filter path set m.orgSchema[org]
// explicitly, including to an empty slice for the "nothing defined"
// case.
func (m *mockClient) GetOrgPropertySchema(_ context.Context, org string) ([]string, error) {
	m.orgSchemaCalls.Add(1)

	if m.orgSchemaDelay > 0 {
		time.Sleep(m.orgSchemaDelay)
	}

	if m.orgSchemaErr != nil {
		return nil, m.orgSchemaErr
	}

	names, ok := m.orgSchema[org]
	if !ok {
		return nil, fmt.Errorf("mockClient: org schema not configured for %q", org)
	}

	return names, nil
}

func (*mockClient) GetVulnerabilityAlertsEnabled(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}

func (*mockClient) EnableVulnerabilityAlerts(_ context.Context, _, _ string) error {
	return nil
}

func (*mockClient) DisableVulnerabilityAlerts(_ context.Context, _, _ string) error {
	return nil
}

func (*mockClient) GetRepoSettings(_ context.Context, _, _ string) (*ghclient.RepoSettings, error) {
	return &ghclient.RepoSettings{}, nil
}

func (*mockClient) UpdateRepository(_ context.Context, _, _ string, _ *ghclient.RepoUpdateOpts) error {
	return nil
}

func (*mockClient) ListRepositoryRulesets(_ context.Context, _, _ string) ([]*ghclient.Ruleset, error) {
	return nil, nil
}

func (*mockClient) GetRepositoryRuleset(_ context.Context, _, _ string, _ int64) (*ghclient.Ruleset, error) {
	return nil, nil //nolint:nilnil // mock stub
}

func (*mockClient) CreateRepositoryRuleset(_ context.Context, _, _ string, _ *ghclient.Ruleset) (*ghclient.Ruleset, error) {
	return nil, nil //nolint:nilnil // mock stub
}

func (*mockClient) UpdateRepositoryRuleset(_ context.Context, _, _ string, _ int64, _ *ghclient.Ruleset) (*ghclient.Ruleset, error) {
	return nil, nil //nolint:nilnil // mock stub
}

func (*mockClient) ListLabels(_ context.Context, _, _ string) ([]*ghclient.Label, error) {
	return nil, nil
}

func (*mockClient) CreateLabel(_ context.Context, _, _ string, _ *ghclient.Label) error {
	return nil
}

func (*mockClient) UpdateLabel(_ context.Context, _, _, _ string, _ *ghclient.Label) error {
	return nil
}

func (*mockClient) DeleteLabel(_ context.Context, _, _, _ string) error {
	return nil
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

func (*mockClient) ClosePullRequest(_ context.Context, _, _ string, _ int) error {
	return nil
}

func (*mockClient) ListPRComments(_ context.Context, _, _ string, _ int) ([]*ghclient.Comment, error) {
	return nil, nil
}

func (*mockClient) UpsertPRComment(_ context.Context, _, _ string, _ int, _, _ string) error {
	return nil
}

func basePropertiesClient() *mockClient {
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "my-service", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/my-service/main"] = "abc123"

	return client
}

func newTestReconciler(t *testing.T, mode string) reconciler.Reconciler {
	t.Helper()

	return newTestReconcilerWithProps(t, mode, nil)
}

// jiraAnnotationProps is the pre-DESIGN-0019 hardcoded Jira mapping,
// reproduced as operator config for the regression tests proving
// map-driven output matches the old built-in behavior set-for-set.
func jiraAnnotationProps() map[string]string {
	return map[string]string{
		"jira/project-key": "JiraProject",
		"jira/label":       "JiraLabel",
	}
}

func newTestReconcilerWithProps(t *testing.T, mode string, annotationProps map[string]string) reconciler.Reconciler {
	t.Helper()

	ts := rules.NewTemplateStore()
	if err := ts.Load(""); err != nil {
		t.Fatalf("loading templates: %v", err)
	}

	r, err := reconciler.NewCustomPropertiesReconciler(
		policy.ReconcilerConfig{Type: "custom_properties", Mode: mode, AnnotationProperties: annotationProps},
		ts,
	)
	if err != nil {
		t.Fatalf("creating reconciler: %v", err)
	}

	return r
}

// strPtr returns a pointer to s, for constructing
// ghclient.CustomPropertyValue literals in tests.
func strPtr(s string) *string {
	return &s
}

func newParams(client ghclient.Client, content string, dryRun bool, openPRs []*ghclient.PullRequest) *reconciler.ReconcileParams {
	return &reconciler.ReconcileParams{
		Client:        client,
		Owner:         "org",
		Repo:          "my-service",
		DefaultBranch: "main",
		Content:       content,
		OpenPRs:       openPRs,
		DryRun:        dryRun,
		Logger:        slog.Default(),
	}
}

// --- Factory tests ---

func TestNewCustomPropertiesReconciler_ValidModes(t *testing.T) {
	t.Parallel()

	ts := rules.NewTemplateStore()
	if err := ts.Load(""); err != nil {
		t.Fatalf("loading templates: %v", err)
	}

	for _, mode := range []string{"api", "github-action"} {
		r, err := reconciler.NewCustomPropertiesReconciler(
			policy.ReconcilerConfig{Type: "custom_properties", Mode: mode},
			ts,
		)
		if err != nil {
			t.Errorf("mode %q: unexpected error: %v", mode, err)
		}

		if r.Name() != "custom_properties" {
			t.Errorf("mode %q: Name() = %q, want %q", mode, r.Name(), "custom_properties")
		}
	}
}

func TestNewCustomPropertiesReconciler_InvalidMode(t *testing.T) {
	t.Parallel()

	ts := rules.NewTemplateStore()
	if err := ts.Load(""); err != nil {
		t.Fatalf("loading templates: %v", err)
	}

	_, err := reconciler.NewCustomPropertiesReconciler(
		policy.ReconcilerConfig{Type: "custom_properties", Mode: "invalid"},
		ts,
	)
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}

	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("error = %q, expected to contain 'invalid'", err.Error())
	}
}

// --- GHA mode tests ---

func TestGHAMode_SetsFromCatalogInfo(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t, "github-action")
	client := basePropertiesClient()
	client.fileContents["org/my-service/catalog-info.yaml"] = validCatalogInfo

	err := r.Reconcile(context.Background(), newParams(client, validCatalogInfo, false, nil))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if client.createdPR == nil {
		t.Fatal("expected PR to be created")
	}

	if client.createdPR.Title != reconciler.PropertiesPRTitle {
		t.Errorf("expected PR title %q, got %q", reconciler.PropertiesPRTitle, client.createdPR.Title)
	}

	if client.createdPR.Head != reconciler.PropertiesBranchName {
		t.Errorf("expected head branch %q, got %q", reconciler.PropertiesBranchName, client.createdPR.Head)
	}

	if len(client.createdFiles) != 1 {
		t.Fatalf("expected 1 file created, got %d: %v", len(client.createdFiles), client.createdFiles)
	}

	if client.createdFiles[0] != ".github/workflows/set-custom-properties.yml" {
		t.Errorf("expected workflow file, got %q", client.createdFiles[0])
	}

	rendered := client.createdFileBody[".github/workflows/set-custom-properties.yml"]
	if strings.Contains(rendered, "JiraProject") || strings.Contains(rendered, "-F ") {
		t.Errorf("expected no mapped properties or clears with an empty map, got:\n%s", rendered)
	}
}

// TestGHAMode_SetsFromCatalogInfo_JiraMapConfigured is the DESIGN-0019
// GHA-mode regression case: with annotation_properties reproducing the
// pre-change hardcoded Jira mapping, the rendered workflow carries the
// same Jira values the old hardcoded template produced.
func TestGHAMode_SetsFromCatalogInfo_JiraMapConfigured(t *testing.T) {
	t.Parallel()

	r := newTestReconcilerWithProps(t, "github-action", jiraAnnotationProps())
	client := basePropertiesClient()
	client.fileContents["org/my-service/catalog-info.yaml"] = validCatalogInfo

	err := r.Reconcile(context.Background(), newParams(client, validCatalogInfo, false, nil))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if client.createdPR == nil {
		t.Fatal("expected PR to be created")
	}

	rendered := client.createdFileBody[".github/workflows/set-custom-properties.yml"]

	for _, want := range []string{
		"-f 'properties[][property_name]=JiraLabel'",
		"-f 'properties[][property_name]=JiraProject'",
		`RG_PROP_Component: "my-service"`,
		`RG_PROP_JiraProject: "PROJ"`,
		`properties[][value]=$RG_PROP_JiraProject`,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered workflow missing %q; got:\n%s", want, rendered)
		}
	}
}

// TestGHAMode_RemovedAnnotationRendersClear proves the GHA-mode
// workflow renders a JSON-null clear for a managed property whose
// source annotation is no longer present in catalog-info.yaml.
func TestGHAMode_RemovedAnnotationRendersClear(t *testing.T) {
	t.Parallel()

	r := newTestReconcilerWithProps(t, "github-action", jiraAnnotationProps())
	client := basePropertiesClient()

	contentWithOnlyProject := `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: my-service
  annotations:
    jira/project-key: "PROJ"
spec:
  owner: platform-team
`

	err := r.Reconcile(context.Background(), newParams(client, contentWithOnlyProject, false, nil))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if client.createdPR == nil {
		t.Fatal("expected PR to be created")
	}

	rendered := client.createdFileBody[".github/workflows/set-custom-properties.yml"]

	if !strings.Contains(rendered, "-f 'properties[][property_name]=JiraLabel'") {
		t.Errorf("expected JiraLabel property_name to still be emitted; got:\n%s", rendered)
	}

	if !strings.Contains(rendered, "-F 'properties[][value]=null'") {
		t.Errorf("expected a JSON-null clear for the removed JiraLabel annotation; got:\n%s", rendered)
	}

	if !strings.Contains(rendered, `RG_PROP_JiraProject: "PROJ"`) {
		t.Errorf("expected JiraProject to still be set from the present annotation; got:\n%s", rendered)
	}
}

func TestGHAMode_NoCatalogFile(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t, "github-action")
	client := basePropertiesClient()

	err := r.Reconcile(context.Background(), newParams(client, "", false, nil))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if client.createdPR == nil {
		t.Fatal("expected PR to be created with Unclassified defaults")
	}

	if client.createdPR.Head != reconciler.PropertiesBranchName {
		t.Errorf("expected head branch %q, got %q", reconciler.PropertiesBranchName, client.createdPR.Head)
	}
}

func TestGHAMode_UnparseableFile(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t, "github-action")
	client := basePropertiesClient()

	// Unparseable content → skip without opening a PR (INV-0011 A1).
	err := r.Reconcile(context.Background(), newParams(client, "{{{invalid yaml", false, nil))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if client.createdPR != nil {
		t.Errorf("expected no PR on unparseable catalog-info, got %+v", client.createdPR)
	}
}

// TestGHAMode_CustomizedPRTemplate verifies that a non-nil
// ReconcileParams.PRTemplate flows through the new
// resolveReconcilerPR helper: rendered title replaces the hardcoded
// constant, labels are applied to the PR. This covers the
// previously-deferred "Update existing reconciler tests under
// customized policy" item from IMPL-0012 Phase 5.
func TestGHAMode_CustomizedPRTemplate(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t, "github-action")
	client := basePropertiesClient()
	client.fileContents["org/my-service/catalog-info.yaml"] = validCatalogInfo

	titleC, err := tmpl.NewRenderer().Parse("title", "[PLAT-CHORE] sync {{ .Repo }} properties")
	if err != nil {
		t.Fatalf("compiling title: %v", err)
	}

	bodyC, err := tmpl.NewRenderer().Parse("body", "Reconciler: {{ .Reconciler }} on {{ .Owner }}/{{ .Repo }}")
	if err != nil {
		t.Fatalf("compiling body: %v", err)
	}

	params := newParams(client, validCatalogInfo, false, nil)
	params.PRTemplate = &policy.PRTemplate{
		Title:     titleC,
		Body:      bodyC,
		Labels:    []string{"automated", "catalog-sync"},
		LabelsSet: true,
		Inherits:  false,
	}

	if err := r.Reconcile(context.Background(), params); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if client.createdPR == nil {
		t.Fatal("expected PR to be created")
	}

	wantTitle := "[PLAT-CHORE] sync my-service properties"
	if client.createdPR.Title != wantTitle {
		t.Errorf("PR title: got %q, want %q", client.createdPR.Title, wantTitle)
	}

	if !strings.Contains(client.createdPRBody, "Reconciler: custom_properties on org/my-service") {
		t.Errorf("PR body missing rendered content; got: %s", client.createdPRBody)
	}

	wantLabels := []string{"automated", "catalog-sync"}
	if len(client.addedLabels) != len(wantLabels) {
		t.Fatalf("expected %d labels, got %v", len(wantLabels), client.addedLabels)
	}

	for i, want := range wantLabels {
		if client.addedLabels[i] != want {
			t.Errorf("label[%d]: got %q, want %q", i, client.addedLabels[i], want)
		}
	}
}

func TestGHAMode_AlreadyCorrect(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t, "github-action")
	client := basePropertiesClient()
	client.customProperties["org/my-service"] = []*ghclient.CustomPropertyValue{
		{PropertyName: "Owner", Value: strPtr("platform-team")},
		{PropertyName: "Component", Value: strPtr("my-service")},
		{PropertyName: "JiraProject", Value: strPtr("PROJ")},
		{PropertyName: "JiraLabel", Value: strPtr("my-service")},
	}

	err := r.Reconcile(context.Background(), newParams(client, validCatalogInfo, false, nil))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if client.createdPR != nil {
		t.Error("should not create PR when properties already correct")
	}
}

func TestGHAMode_ExistingPR(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t, "github-action")
	client := basePropertiesClient()

	openPRs := []*ghclient.PullRequest{
		{Number: 42, Title: reconciler.PropertiesPRTitle, Head: reconciler.PropertiesBranchName, State: "open"},
	}

	err := r.Reconcile(context.Background(), newParams(client, validCatalogInfo, false, openPRs))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if client.createdPR != nil {
		t.Error("should not create PR when one already exists")
	}
}

func TestGHAMode_DryRun(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t, "github-action")
	client := basePropertiesClient()

	err := r.Reconcile(context.Background(), newParams(client, validCatalogInfo, true, nil))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if client.createdPR != nil {
		t.Error("dry run should not create PR")
	}

	if len(client.createdBranches) != 0 {
		t.Error("dry run should not create branches")
	}
}

func TestGHAMode_StaleBranchCleanup(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t, "github-action")
	client := basePropertiesClient()
	client.branchSHAs["org/my-service/"+reconciler.PropertiesBranchName] = "stale-sha"

	err := r.Reconcile(context.Background(), newParams(client, validCatalogInfo, false, nil))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.deletedBranches) != 1 || client.deletedBranches[0] != reconciler.PropertiesBranchName {
		t.Errorf("expected stale branch %q deleted, got %v", reconciler.PropertiesBranchName, client.deletedBranches)
	}

	if client.createdPR == nil {
		t.Fatal("expected new PR after stale branch cleanup")
	}
}

// --- API mode tests ---

// TestAPIMode_SetsFromCatalogInfo_JiraMapConfigured is the DESIGN-0019
// regression case: with annotation_properties configured to reproduce
// the pre-change hardcoded Jira mapping, the API payload is
// set-identical to the old built-in behavior.
func TestAPIMode_SetsFromCatalogInfo_JiraMapConfigured(t *testing.T) {
	t.Parallel()

	r := newTestReconcilerWithProps(t, "api", jiraAnnotationProps())
	client := basePropertiesClient()

	err := r.Reconcile(context.Background(), newParams(client, validCatalogInfo, false, nil))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.setProperties) == 0 {
		t.Fatal("expected properties to be set via API")
	}

	propMap := make(map[string]string)
	for _, p := range client.setProperties {
		if p.Value != nil {
			propMap[p.PropertyName] = *p.Value
		}
	}

	if propMap["Owner"] != "platform-team" {
		t.Errorf("expected Owner=platform-team, got %q", propMap["Owner"])
	}

	if propMap["Component"] != "my-service" {
		t.Errorf("expected Component=my-service, got %q", propMap["Component"])
	}

	if propMap["JiraProject"] != "PROJ" {
		t.Errorf("expected JiraProject=PROJ, got %q", propMap["JiraProject"])
	}

	if propMap["JiraLabel"] != "my-service" {
		t.Errorf("expected JiraLabel=my-service, got %q", propMap["JiraLabel"])
	}

	if client.createdPR != nil {
		t.Error("should not create catalog-info PR when file exists")
	}
}

// TestAPIMode_UnmanagedPropertyNeverTouched proves properties outside
// the managed set ({Owner, Component} ∪ annotation_properties targets)
// are never diffed or emitted, even when GitHub already has a value
// for a name that happens to collide with an annotation the operator
// hasn't mapped.
func TestAPIMode_UnmanagedPropertyNeverTouched(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t, "api") // no annotation_properties configured
	client := basePropertiesClient()
	client.customProperties["org/my-service"] = []*ghclient.CustomPropertyValue{
		{PropertyName: "Owner", Value: strPtr("someone-else")},
		{PropertyName: "Component", Value: strPtr("my-service")},
		{PropertyName: "CostCenter", Value: strPtr("manually-set-by-a-human")},
	}

	err := r.Reconcile(context.Background(), newParams(client, validCatalogInfo, false, nil))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.setProperties) == 0 {
		t.Fatal("expected Owner drift to trigger a PATCH")
	}

	for _, p := range client.setProperties {
		if p.PropertyName == "CostCenter" {
			t.Errorf("unmanaged property CostCenter must never appear in the payload, got %+v", p)
		}
	}
}

// TestAPIMode_RemovedAnnotationClearsProperty is the DESIGN-0019
// clear-on-removal case: a property previously set from a mapped
// annotation must be nulled once the annotation disappears from
// catalog-info.yaml, and the clear is counted + logged.
func TestAPIMode_RemovedAnnotationClearsProperty(t *testing.T) {
	t.Parallel()

	metrics.CustomPropertyClearedTotal.Reset()

	r := newTestReconcilerWithProps(t, "api", jiraAnnotationProps())
	client := basePropertiesClient()
	client.customProperties["org/my-service"] = []*ghclient.CustomPropertyValue{
		{PropertyName: "Owner", Value: strPtr("platform-team")},
		{PropertyName: "Component", Value: strPtr("my-service")},
		{PropertyName: "JiraProject", Value: strPtr("PROJ")},
		{PropertyName: "JiraLabel", Value: strPtr("my-service")},
	}

	contentWithoutJira := `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: my-service
spec:
  owner: platform-team
  lifecycle: production
  type: service
`

	err := r.Reconcile(context.Background(), newParams(client, contentWithoutJira, false, nil))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.setProperties) == 0 {
		t.Fatal("expected a PATCH clearing the removed Jira properties")
	}

	var clearedJiraProject, clearedJiraLabel bool

	for _, p := range client.setProperties {
		switch p.PropertyName {
		case "JiraProject":
			clearedJiraProject = p.Value == nil
		case "JiraLabel":
			clearedJiraLabel = p.Value == nil
		case "Owner", "Component":
			if p.Value == nil {
				t.Errorf("Owner/Component must never be cleared, got nil for %s", p.PropertyName)
			}
		}
	}

	if !clearedJiraProject {
		t.Error("expected JiraProject to be cleared (nil Value)")
	}

	if !clearedJiraLabel {
		t.Error("expected JiraLabel to be cleared (nil Value)")
	}

	if got := testutil.ToFloat64(metrics.CustomPropertyClearedTotal.WithLabelValues("org")); got != 2 {
		t.Errorf("CustomPropertyClearedTotal = %v, want 2", got)
	}
}

// TestAPIMode_FiltersUndefinedMappedProperty is the DESIGN-0019
// schema-preflight partition case: JiraLabel has no org-level
// property definition, so it must be dropped from the PATCH while
// Owner/Component/JiraProject (all defined) still sync in the same
// call, with a warn line and a per-property counter increment.
func TestAPIMode_FiltersUndefinedMappedProperty(t *testing.T) {
	t.Parallel()

	// Reset is safe here even under t.Parallel(): "filter-org" is a
	// label value unique to this test, so no other test observes the
	// zeroing. It's needed because CounterVec state is process-global
	// and would otherwise accumulate across repeated test-binary runs
	// (e.g. `go test -count=N`).
	metrics.CustomPropertyMissingSchemaTotal.Reset()

	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, nil))

	r := newTestReconcilerWithProps(t, "api", jiraAnnotationProps())
	client := basePropertiesClient()
	client.orgSchema["filter-org"] = []string{"Owner", "Component", "JiraProject"} // JiraLabel undefined

	params := newParams(client, validCatalogInfo, false, nil)
	params.Owner = "filter-org" // unique per test: avoids racing other parallel tests' counter assertions
	params.Logger = logger

	if err := r.Reconcile(context.Background(), params); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	seen := make(map[string]bool)
	for _, p := range client.setProperties {
		seen[p.PropertyName] = true
	}

	for _, name := range []string{"Owner", "Component", "JiraProject"} {
		if !seen[name] {
			t.Errorf("expected %s (defined in org schema) in the single PATCH payload, got %+v", name, client.setProperties)
		}
	}

	if seen["JiraLabel"] {
		t.Error("JiraLabel is undefined in the org schema and must not appear in the payload")
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "custom properties missing from org schema") {
		t.Errorf("expected missing-schema warn message, got log: %s", logOutput)
	}

	if !strings.Contains(logOutput, "org=filter-org") {
		t.Errorf("expected org=filter-org in log output, got: %s", logOutput)
	}

	if !strings.Contains(logOutput, "JiraLabel") {
		t.Errorf("expected missing_properties to mention JiraLabel, got: %s", logOutput)
	}

	if got := testutil.ToFloat64(metrics.CustomPropertyMissingSchemaTotal.WithLabelValues("filter-org", "JiraLabel")); got != 1 {
		t.Errorf("CustomPropertyMissingSchemaTotal{filter-org,JiraLabel} = %v, want 1", got)
	}
}

// TestAPIMode_SchemaMissingProperty_ConvergesToZeroPatches is the
// INV-0011 A5 regression: a mapped property the org schema does not
// define must not report drift forever. Before the fix, diffProperties
// compared the full managed set while only the payload was filtered, so
// JiraLabel — undefined in the org and therefore never writable — kept
// the reconciler in a no-op PATCH loop, re-sending the already-correct
// Owner/Component/JiraProject on every sweep.
func TestAPIMode_SchemaMissingProperty_ConvergesToZeroPatches(t *testing.T) {
	t.Parallel()

	r := newTestReconcilerWithProps(t, "api", jiraAnnotationProps())
	client := basePropertiesClient()
	client.orgSchema["org"] = []string{"Owner", "Component", "JiraProject"} // JiraLabel undefined

	// Sweep 1 converges the three defined properties from empty.
	if err := r.Reconcile(context.Background(), newParams(client, validCatalogInfo, false, nil)); err != nil {
		t.Fatalf("Reconcile sweep 1: %v", err)
	}

	if got := client.setPropsCalls.Load(); got != 1 {
		t.Fatalf("sweep 1 SetCustomPropertyValues calls = %d, want 1 (initial convergence)", got)
	}

	seen := make(map[string]bool)
	for _, p := range client.setProperties {
		seen[p.PropertyName] = true
	}

	if seen["JiraLabel"] {
		t.Error("JiraLabel is undefined in the org schema and must never be sent")
	}

	// Sweep 2 over identical state must be a complete no-op: the only
	// property still "differing" is the one the org cannot store.
	if err := r.Reconcile(context.Background(), newParams(client, validCatalogInfo, false, nil)); err != nil {
		t.Fatalf("Reconcile sweep 2: %v", err)
	}

	if got := client.setPropsCalls.Load(); got != 1 {
		t.Errorf("SetCustomPropertyValues calls after 2 sweeps = %d, want 1 (second sweep must issue zero PATCHes)", got)
	}
}

// TestAPIMode_SchemaMissingProperty_StillReportsMissingSchema pins the
// observability half of the A5 fix: because a schema-missing property
// no longer registers as drift, the missing-schema warn and counter had
// to move onto the every-reconcile path. If they regress back onto the
// drift path this test fails, and RepoGuardianPropertySchemaMissing
// would silently stop firing.
func TestAPIMode_SchemaMissingProperty_StillReportsMissingSchema(t *testing.T) {
	t.Parallel()

	metrics.CustomPropertyMissingSchemaTotal.Reset()

	var buf bytes.Buffer

	r := newTestReconcilerWithProps(t, "api", jiraAnnotationProps())
	client := basePropertiesClient()
	client.orgSchema["converged-org"] = []string{"Owner", "Component", "JiraProject"}
	client.customProperties["converged-org/my-service"] = []*ghclient.CustomPropertyValue{
		{PropertyName: "Owner", Value: strPtr("platform-team")},
		{PropertyName: "Component", Value: strPtr("my-service")},
		{PropertyName: "JiraProject", Value: strPtr("PROJ")},
	}

	params := newParams(client, validCatalogInfo, false, nil)
	params.Owner = "converged-org" // unique per test: parallel-safe counter assertions
	params.Logger = slog.New(slog.NewTextHandler(&buf, nil))

	if err := r.Reconcile(context.Background(), params); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// Fully converged: nothing to write.
	if got := client.setPropsCalls.Load(); got != 0 {
		t.Errorf("SetCustomPropertyValues calls = %d, want 0 (already converged)", got)
	}

	// ...yet the schema gap is still reported.
	if got := testutil.ToFloat64(
		metrics.CustomPropertyMissingSchemaTotal.WithLabelValues("converged-org", "JiraLabel"),
	); got != 1 {
		t.Errorf("CustomPropertyMissingSchemaTotal{converged-org,JiraLabel} = %v, want 1 on a no-drift reconcile", got)
	}

	if logOutput := buf.String(); !strings.Contains(logOutput, "custom properties missing from org schema") {
		t.Errorf("expected missing-schema warn on a no-drift reconcile, got log: %s", logOutput)
	}
}

// TestAPIMode_EmptySchemaSkipsPatch covers the degenerate case where
// the org schema is reachable but defines nothing: every managed
// property is "missing," so the PATCH is skipped entirely rather than
// sent empty.
func TestAPIMode_EmptySchemaSkipsPatch(t *testing.T) {
	t.Parallel()

	r := newTestReconcilerWithProps(t, "api", jiraAnnotationProps())
	client := basePropertiesClient()
	client.orgSchema["empty-schema-org"] = []string{}

	params := newParams(client, validCatalogInfo, false, nil)
	params.Owner = "empty-schema-org"

	if err := r.Reconcile(context.Background(), params); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.setProperties) != 0 {
		t.Errorf("expected no PATCH when org schema defines nothing, got %+v", client.setProperties)
	}
}

// TestAPIMode_SchemaFetchError_FailsOpenAndLogsOncePerOrg exercises
// the 403/5xx/timeout path: repo-guardian must keep syncing the full,
// unfiltered payload (today's pre-Phase-3 behavior) rather than block
// on a broken schema endpoint, and must log the failure exactly once
// per org per TTL window rather than once per repo.
func TestAPIMode_SchemaFetchError_FailsOpenAndLogsOncePerOrg(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, nil))

	r := newTestReconcilerWithProps(t, "api", jiraAnnotationProps())
	client := basePropertiesClient()
	client.orgSchemaErr = fmt.Errorf("403 Forbidden")

	const repoCount = 3
	for i := range repoCount {
		params := newParams(client, validCatalogInfo, false, nil)
		params.Repo = fmt.Sprintf("svc-%d", i)
		params.Logger = logger

		if err := r.Reconcile(context.Background(), params); err != nil {
			t.Fatalf("Reconcile repo %d: %v", i, err)
		}
	}

	counts := make(map[string]int)
	for _, p := range client.setProperties {
		counts[p.PropertyName]++
	}

	for _, name := range []string{"Owner", "Component", "JiraProject", "JiraLabel"} {
		if counts[name] != repoCount {
			t.Errorf("fail-open: expected %s in all %d unfiltered PATCHes, got count=%d", name, repoCount, counts[name])
		}
	}

	if got := client.orgSchemaCalls.Load(); got != 1 {
		t.Errorf("GetOrgPropertySchema calls = %d, want 1 (error cached for the TTL window)", got)
	}

	logOutput := buf.String()
	if got := strings.Count(logOutput, "fetching org custom property schema failed"); got != 1 {
		t.Errorf("expected exactly one fail-open warn line across %d repos, got %d in: %s", repoCount, got, logOutput)
	}
}

// TestAPIMode_SchemaCache_OneFetchPerOrgWithinTTL is the fleet-sweep
// cost bound from the Phase 3 success criteria: N repos processed for
// the same org within the TTL window must cost exactly one
// GetOrgPropertySchema call.
func TestAPIMode_SchemaCache_OneFetchPerOrgWithinTTL(t *testing.T) {
	t.Parallel()

	r := newTestReconcilerWithProps(t, "api", jiraAnnotationProps())
	client := basePropertiesClient()
	client.orgSchema["org"] = []string{"Owner", "Component", "JiraProject", "JiraLabel"}

	const repoCount = 3
	for i := range repoCount {
		params := newParams(client, validCatalogInfo, false, nil)
		params.Repo = fmt.Sprintf("svc-%d", i)

		if err := r.Reconcile(context.Background(), params); err != nil {
			t.Fatalf("Reconcile repo %d: %v", i, err)
		}
	}

	if got := client.orgSchemaCalls.Load(); got != 1 {
		t.Errorf("GetOrgPropertySchema calls = %d, want 1 (cached within TTL across %d repos in the same org)", got, repoCount)
	}
}

// TestAPIMode_SchemaCache_ConcurrentMissesCollapseToOneFetch proves
// the singleflight guard, not just the TTL cache: a burst of repos
// from the same org processed concurrently by the worker pool (the
// realistic cold-start scenario) must still cost exactly one
// GetOrgPropertySchema call, not one per goroutine that raced past
// the empty cache before the first fetch completed.
func TestAPIMode_SchemaCache_ConcurrentMissesCollapseToOneFetch(t *testing.T) {
	t.Parallel()

	r := newTestReconcilerWithProps(t, "api", jiraAnnotationProps())
	client := basePropertiesClient()
	client.orgSchema["org"] = []string{"Owner", "Component", "JiraProject", "JiraLabel"}
	client.orgSchemaDelay = 20 * time.Millisecond

	const goroutines = 20

	var wg sync.WaitGroup

	wg.Add(goroutines)

	for i := range goroutines {
		go func() {
			defer wg.Done()

			params := newParams(client, validCatalogInfo, false, nil)
			params.Repo = fmt.Sprintf("svc-%d", i)

			if err := r.Reconcile(context.Background(), params); err != nil {
				t.Errorf("Reconcile repo %d: %v", i, err)
			}
		}()
	}

	wg.Wait()

	if got := client.orgSchemaCalls.Load(); got != 1 {
		t.Errorf("GetOrgPropertySchema calls = %d, want 1 (singleflight-collapsed concurrent cache misses)", got)
	}
}

func TestAPIMode_NoCatalogFile(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t, "api")
	client := basePropertiesClient()

	err := r.Reconcile(context.Background(), newParams(client, "", false, nil))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.setProperties) == 0 {
		t.Fatal("expected properties to be set via API")
	}

	propMap := make(map[string]string)
	for _, p := range client.setProperties {
		if p.Value != nil {
			propMap[p.PropertyName] = *p.Value
		}
	}

	if propMap["Owner"] != catalog.DefaultOwner {
		t.Errorf("expected Owner=%s, got %q", catalog.DefaultOwner, propMap["Owner"])
	}

	if client.createdPR == nil {
		t.Fatal("expected catalog-info PR to be created")
	}

	if client.createdPR.Head != reconciler.CatalogInfoBranchName {
		t.Errorf("expected head branch %q, got %q", reconciler.CatalogInfoBranchName, client.createdPR.Head)
	}
}

func TestAPIMode_AlreadyCorrect(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t, "api")
	client := basePropertiesClient()
	client.customProperties["org/my-service"] = []*ghclient.CustomPropertyValue{
		{PropertyName: "Owner", Value: strPtr("platform-team")},
		{PropertyName: "Component", Value: strPtr("my-service")},
		{PropertyName: "JiraProject", Value: strPtr("PROJ")},
		{PropertyName: "JiraLabel", Value: strPtr("my-service")},
	}

	err := r.Reconcile(context.Background(), newParams(client, validCatalogInfo, false, nil))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.setProperties) != 0 {
		t.Error("should not call SetCustomPropertyValues when already correct")
	}

	if client.createdPR != nil {
		t.Error("should not create PR when already correct")
	}
}

func TestAPIMode_DryRun(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t, "api")
	client := basePropertiesClient()

	err := r.Reconcile(context.Background(), newParams(client, validCatalogInfo, true, nil))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.setProperties) != 0 {
		t.Error("dry run should not set properties via API")
	}

	if client.createdPR != nil {
		t.Error("dry run should not create PR")
	}
}

func TestAPIMode_NoCatalog_ExistingPR(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t, "api")
	client := basePropertiesClient()

	openPRs := []*ghclient.PullRequest{
		{Number: 99, Title: reconciler.CatalogInfoPRTitle, Head: reconciler.CatalogInfoBranchName, State: "open"},
	}

	err := r.Reconcile(context.Background(), newParams(client, "", false, openPRs))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.setProperties) == 0 {
		t.Fatal("expected properties to be set via API even with existing PR")
	}

	if client.createdPR != nil {
		t.Error("should not create duplicate catalog-info PR")
	}
}

func TestAPIMode_NoCatalog_StaleBranchCleanup(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t, "api")
	client := basePropertiesClient()
	client.branchSHAs["org/my-service/"+reconciler.CatalogInfoBranchName] = "stale-sha"

	err := r.Reconcile(context.Background(), newParams(client, "", false, nil))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.deletedBranches) != 1 || client.deletedBranches[0] != reconciler.CatalogInfoBranchName {
		t.Errorf("expected stale branch %q deleted, got %v", reconciler.CatalogInfoBranchName, client.deletedBranches)
	}

	if client.createdPR == nil {
		t.Fatal("expected new catalog-info PR after stale branch cleanup")
	}
}

func TestReconciler_UnparseableCatalog_Skips(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t, "api")
	client := basePropertiesClient()

	// Unparseable content → skip without touching GitHub state
	// (INV-0011 A1: a parse failure must never masquerade as "clear
	// everything").
	err := r.Reconcile(context.Background(), newParams(client, "not yaml {{{", false, nil))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.setProperties) != 0 {
		t.Errorf("expected zero SetCustomPropertyValues calls, got %v", client.setProperties)
	}
}

func TestAPIMode_UnparseableCatalog_NoWriteAndCounterIncrements(t *testing.T) {
	t.Parallel()

	r := newTestReconcilerWithProps(t, "api", jiraAnnotationProps())
	client := basePropertiesClient()
	client.customProperties["parse-fail-org/my-service"] = []*ghclient.CustomPropertyValue{
		{PropertyName: "Owner", Value: strPtr("platform-team")},
		{PropertyName: "JiraProject", Value: strPtr("PROJ")},
	}

	// Owner doubles as the org metric label; a test-unique value keeps
	// the counter delta isolated from parallel tests that also feed
	// malformed content through owner "org".
	params := newParams(client, "not yaml {{{", false, nil)
	params.Owner = "parse-fail-org"

	before := testutil.ToFloat64(metrics.CatalogParseFailedTotal.WithLabelValues("parse-fail-org"))

	if err := r.Reconcile(context.Background(), params); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.setProperties) != 0 {
		t.Errorf("expected zero SetCustomPropertyValues calls on parse failure, got %v", client.setProperties)
	}

	got := testutil.ToFloat64(metrics.CatalogParseFailedTotal.WithLabelValues("parse-fail-org")) - before
	if got != 1 {
		t.Errorf("CatalogParseFailedTotal delta = %v, want 1", got)
	}
}

func TestAPIMode_NonComponentCatalog_SkipsWithoutCounter(t *testing.T) {
	t.Parallel()

	r := newTestReconcilerWithProps(t, "api", jiraAnnotationProps())
	client := basePropertiesClient()

	content := `apiVersion: backstage.io/v1alpha1
kind: API
metadata:
  name: my-api
spec:
  owner: api-team
`

	params := newParams(client, content, false, nil)
	params.Owner = "non-component-org"

	before := testutil.ToFloat64(metrics.CatalogParseFailedTotal.WithLabelValues("non-component-org"))

	if err := r.Reconcile(context.Background(), params); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.setProperties) != 0 {
		t.Errorf("expected zero SetCustomPropertyValues calls for non-Component entity, got %v", client.setProperties)
	}

	if client.createdPR != nil {
		t.Errorf("expected no PR for non-Component entity, got %+v", client.createdPR)
	}

	// A valid non-Component entity is a legitimate skip, not a parse
	// failure — the counter must not move (IMPL-0020 Decision 1).
	got := testutil.ToFloat64(metrics.CatalogParseFailedTotal.WithLabelValues("non-component-org")) - before
	if got != 0 {
		t.Errorf("CatalogParseFailedTotal delta = %v, want 0", got)
	}
}

// --- Registry integration test ---

func TestRegistry_CustomPropertiesFactory(t *testing.T) {
	t.Parallel()

	ts := rules.NewTemplateStore()
	if err := ts.Load(""); err != nil {
		t.Fatalf("loading templates: %v", err)
	}

	reg := reconciler.NewRegistry()
	reg.Register("custom_properties", func(cfg policy.ReconcilerConfig) (reconciler.Reconciler, error) {
		return reconciler.NewCustomPropertiesReconciler(cfg, ts)
	})

	r, err := reg.Build(policy.ReconcilerConfig{Type: "custom_properties", Mode: "api"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if r.Name() != "custom_properties" {
		t.Errorf("Name() = %q, want %q", r.Name(), "custom_properties")
	}
}
