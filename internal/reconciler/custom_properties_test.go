package reconciler_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
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

	return m.customProperties[key], nil
}

func (m *mockClient) SetCustomPropertyValues(_ context.Context, _, _ string, properties []*ghclient.CustomPropertyValue) error {
	if m.setCustomPropsErr != nil {
		return m.setCustomPropsErr
	}

	m.setProperties = append(m.setProperties, properties...)

	return nil
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
		"-f 'properties[][value]=my-service'",
		"-f 'properties[][value]=PROJ'",
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

	if !strings.Contains(rendered, "-f 'properties[][value]=PROJ'") {
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

	err := r.Reconcile(context.Background(), newParams(client, "{{{invalid yaml", false, nil))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if client.createdPR == nil {
		t.Fatal("expected PR to be created with Unclassified defaults")
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

func TestReconciler_MissingFields_UsesDefaults(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t, "api")
	client := basePropertiesClient()

	// Unparseable content → catalog.Parse returns defaults.
	err := r.Reconcile(context.Background(), newParams(client, "not yaml {{{", false, nil))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.setProperties) == 0 {
		t.Fatal("expected properties to be set")
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

	if propMap["Component"] != catalog.DefaultComponent {
		t.Errorf("expected Component=%s, got %q", catalog.DefaultComponent, propMap["Component"])
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
