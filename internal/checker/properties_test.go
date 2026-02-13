package checker

import (
	"context"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/catalog"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
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

const catalogInfoNoJira = `---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: my-service
spec:
  owner: platform-team
  lifecycle: production
  type: service
`

// basePropertiesClient returns a mockClient pre-configured for properties tests.
// The repo has a default branch "main" with SHA set.
func basePropertiesClient() *mockClient {
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "my-service", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/my-service/main"] = "abc123"

	return client
}

// --- github-action mode tests ---

func TestGHAMode_SetsFromCatalogInfo(t *testing.T) {
	t.Parallel()

	engine := testEngineWithMode(false, "github-action")
	client := basePropertiesClient()
	client.fileContents["org/my-service/catalog-info.yaml"] = validCatalogInfo
	// Current properties are empty — diff will be detected.

	err := engine.CheckCustomProperties(context.Background(), client, "org", "my-service", "main", nil)
	if err != nil {
		t.Fatalf("CheckCustomProperties: %v", err)
	}

	if client.createdPR == nil {
		t.Fatal("expected PR to be created")
	}

	if client.createdPR.Title != PropertiesPRTitle {
		t.Errorf("expected PR title %q, got %q", PropertiesPRTitle, client.createdPR.Title)
	}

	if client.createdPR.Head != PropertiesBranchName {
		t.Errorf("expected head branch %q, got %q", PropertiesBranchName, client.createdPR.Head)
	}

	// Should have created one file (the workflow).
	if len(client.createdFiles) != 1 {
		t.Fatalf("expected 1 file created, got %d: %v", len(client.createdFiles), client.createdFiles)
	}

	if client.createdFiles[0] != ".github/workflows/set-custom-properties.yml" {
		t.Errorf("expected workflow file, got %q", client.createdFiles[0])
	}

	// Branch should be created.
	if len(client.createdBranches) != 1 || client.createdBranches[0] != PropertiesBranchName {
		t.Errorf("expected branch %q created, got %v", PropertiesBranchName, client.createdBranches)
	}
}

func TestGHAMode_NoCatalogFile(t *testing.T) {
	t.Parallel()

	engine := testEngineWithMode(false, "github-action")
	client := basePropertiesClient()
	// No catalog-info.yaml or .yml — Parse returns Unclassified defaults.

	err := engine.CheckCustomProperties(context.Background(), client, "org", "my-service", "main", nil)
	if err != nil {
		t.Fatalf("CheckCustomProperties: %v", err)
	}

	// Should still create PR with Unclassified defaults.
	if client.createdPR == nil {
		t.Fatal("expected PR to be created with Unclassified defaults")
	}

	if client.createdPR.Head != PropertiesBranchName {
		t.Errorf("expected head branch %q, got %q", PropertiesBranchName, client.createdPR.Head)
	}
}

func TestGHAMode_UnparseableFile(t *testing.T) {
	t.Parallel()

	engine := testEngineWithMode(false, "github-action")
	client := basePropertiesClient()
	client.fileContents["org/my-service/catalog-info.yaml"] = "{{{invalid yaml"

	err := engine.CheckCustomProperties(context.Background(), client, "org", "my-service", "main", nil)
	if err != nil {
		t.Fatalf("CheckCustomProperties: %v", err)
	}

	// Should create PR with Unclassified defaults (invalid YAML → defaults).
	if client.createdPR == nil {
		t.Fatal("expected PR to be created with Unclassified defaults")
	}
}

func TestGHAMode_NotBackstageComponent(t *testing.T) {
	t.Parallel()

	engine := testEngineWithMode(false, "github-action")
	client := basePropertiesClient()
	client.fileContents["org/my-service/catalog-info.yaml"] = `---
apiVersion: backstage.io/v1alpha1
kind: API
metadata:
  name: my-api
spec:
  owner: some-team
`

	err := engine.CheckCustomProperties(context.Background(), client, "org", "my-service", "main", nil)
	if err != nil {
		t.Fatalf("CheckCustomProperties: %v", err)
	}

	// kind: API → defaults (Unclassified) → creates PR.
	if client.createdPR == nil {
		t.Fatal("expected PR to be created with Unclassified defaults")
	}
}

func TestGHAMode_AlreadyCorrect(t *testing.T) {
	t.Parallel()

	engine := testEngineWithMode(false, "github-action")
	client := basePropertiesClient()
	client.fileContents["org/my-service/catalog-info.yaml"] = validCatalogInfo

	// Current properties already match desired.
	client.customProperties["org/my-service"] = []*ghclient.CustomPropertyValue{
		{PropertyName: "Owner", Value: "platform-team"},
		{PropertyName: "Component", Value: "my-service"},
		{PropertyName: "JiraProject", Value: "PROJ"},
		{PropertyName: "JiraLabel", Value: "my-service"},
	}

	err := engine.CheckCustomProperties(context.Background(), client, "org", "my-service", "main", nil)
	if err != nil {
		t.Fatalf("CheckCustomProperties: %v", err)
	}

	if client.createdPR != nil {
		t.Error("should not create PR when properties already correct")
	}

	if len(client.createdBranches) != 0 {
		t.Error("should not create branches when properties already correct")
	}
}

func TestGHAMode_PartialAnnotations(t *testing.T) {
	t.Parallel()

	engine := testEngineWithMode(false, "github-action")
	client := basePropertiesClient()
	client.fileContents["org/my-service/catalog-info.yaml"] = catalogInfoNoJira

	err := engine.CheckCustomProperties(context.Background(), client, "org", "my-service", "main", nil)
	if err != nil {
		t.Fatalf("CheckCustomProperties: %v", err)
	}

	// Should create PR — Owner/Component differ from empty current.
	if client.createdPR == nil {
		t.Fatal("expected PR to be created")
	}
}

func TestGHAMode_ExistingPR(t *testing.T) {
	t.Parallel()

	engine := testEngineWithMode(false, "github-action")
	client := basePropertiesClient()
	client.fileContents["org/my-service/catalog-info.yaml"] = validCatalogInfo

	// There's already an open PR for the properties branch.
	openPRs := []*ghclient.PullRequest{
		{Number: 42, Title: PropertiesPRTitle, Head: PropertiesBranchName, State: "open"},
	}

	err := engine.CheckCustomProperties(context.Background(), client, "org", "my-service", "main", openPRs)
	if err != nil {
		t.Fatalf("CheckCustomProperties: %v", err)
	}

	// Should not create another PR.
	if client.createdPR != nil {
		t.Error("should not create PR when one already exists")
	}

	if len(client.createdBranches) != 0 {
		t.Error("should not create branches when PR already exists")
	}
}

func TestGHAMode_DryRun(t *testing.T) {
	t.Parallel()

	engine := testEngineWithMode(true, "github-action")
	client := basePropertiesClient()
	client.fileContents["org/my-service/catalog-info.yaml"] = validCatalogInfo

	err := engine.CheckCustomProperties(context.Background(), client, "org", "my-service", "main", nil)
	if err != nil {
		t.Fatalf("CheckCustomProperties: %v", err)
	}

	if client.createdPR != nil {
		t.Error("dry run should not create PR")
	}

	if len(client.createdBranches) != 0 {
		t.Error("dry run should not create branches")
	}

	if len(client.createdFiles) != 0 {
		t.Error("dry run should not create files")
	}
}

func TestGHAMode_StaleBranchCleanup(t *testing.T) {
	t.Parallel()

	engine := testEngineWithMode(false, "github-action")
	client := basePropertiesClient()
	client.fileContents["org/my-service/catalog-info.yaml"] = validCatalogInfo

	// Stale branch exists (from a previously closed PR) but no open PR.
	client.branchSHAs["org/my-service/"+PropertiesBranchName] = "stale-sha"

	err := engine.CheckCustomProperties(context.Background(), client, "org", "my-service", "main", nil)
	if err != nil {
		t.Fatalf("CheckCustomProperties: %v", err)
	}

	// Should delete the stale branch.
	if len(client.deletedBranches) != 1 || client.deletedBranches[0] != PropertiesBranchName {
		t.Errorf("expected stale branch %q deleted, got %v", PropertiesBranchName, client.deletedBranches)
	}

	// Should create a new branch and PR.
	if len(client.createdBranches) != 1 || client.createdBranches[0] != PropertiesBranchName {
		t.Errorf("expected new branch %q created, got %v", PropertiesBranchName, client.createdBranches)
	}

	if client.createdPR == nil {
		t.Fatal("expected new PR after stale branch cleanup")
	}
}

// --- api mode tests ---

func TestAPIMode_SetsFromCatalogInfo(t *testing.T) {
	t.Parallel()

	engine := testEngineWithMode(false, "api")
	client := basePropertiesClient()
	client.fileContents["org/my-service/catalog-info.yaml"] = validCatalogInfo

	err := engine.CheckCustomProperties(context.Background(), client, "org", "my-service", "main", nil)
	if err != nil {
		t.Fatalf("CheckCustomProperties: %v", err)
	}

	// Should set properties via API.
	if len(client.setProperties) == 0 {
		t.Fatal("expected properties to be set via API")
	}

	// Verify the correct values were set.
	propMap := make(map[string]string)
	for _, p := range client.setProperties {
		propMap[p.PropertyName] = p.Value
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

	// Should NOT create a catalog-info PR (file exists).
	if client.createdPR != nil {
		t.Error("should not create catalog-info PR when file exists")
	}
}

func TestAPIMode_NoCatalogFile(t *testing.T) {
	t.Parallel()

	engine := testEngineWithMode(false, "api")
	client := basePropertiesClient()
	// No catalog-info file → Unclassified defaults set via API + catalog-info PR.

	err := engine.CheckCustomProperties(context.Background(), client, "org", "my-service", "main", nil)
	if err != nil {
		t.Fatalf("CheckCustomProperties: %v", err)
	}

	// Should set Unclassified defaults via API.
	if len(client.setProperties) == 0 {
		t.Fatal("expected properties to be set via API")
	}

	propMap := make(map[string]string)
	for _, p := range client.setProperties {
		propMap[p.PropertyName] = p.Value
	}

	if propMap["Owner"] != "Unclassified" {
		t.Errorf("expected Owner=Unclassified, got %q", propMap["Owner"])
	}

	// Should also create a catalog-info PR.
	if client.createdPR == nil {
		t.Fatal("expected catalog-info PR to be created")
	}

	if client.createdPR.Head != CatalogInfoBranchName {
		t.Errorf("expected head branch %q, got %q", CatalogInfoBranchName, client.createdPR.Head)
	}

	// Should have committed the catalog-info.yaml file.
	if len(client.createdFiles) != 1 || client.createdFiles[0] != "catalog-info.yaml" {
		t.Errorf("expected catalog-info.yaml created, got %v", client.createdFiles)
	}
}

func TestAPIMode_AlreadyCorrect(t *testing.T) {
	t.Parallel()

	engine := testEngineWithMode(false, "api")
	client := basePropertiesClient()
	client.fileContents["org/my-service/catalog-info.yaml"] = validCatalogInfo
	client.customProperties["org/my-service"] = []*ghclient.CustomPropertyValue{
		{PropertyName: "Owner", Value: "platform-team"},
		{PropertyName: "Component", Value: "my-service"},
		{PropertyName: "JiraProject", Value: "PROJ"},
		{PropertyName: "JiraLabel", Value: "my-service"},
	}

	err := engine.CheckCustomProperties(context.Background(), client, "org", "my-service", "main", nil)
	if err != nil {
		t.Fatalf("CheckCustomProperties: %v", err)
	}

	if len(client.setProperties) != 0 {
		t.Error("should not call SetCustomPropertyValues when already correct")
	}

	if client.createdPR != nil {
		t.Error("should not create PR when already correct")
	}
}

func TestAPIMode_NoCatalog_ExistingPR(t *testing.T) {
	t.Parallel()

	engine := testEngineWithMode(false, "api")
	client := basePropertiesClient()
	// No catalog-info file.

	// There's already an open PR for the catalog-info branch.
	openPRs := []*ghclient.PullRequest{
		{Number: 99, Title: CatalogInfoPRTitle, Head: CatalogInfoBranchName, State: "open"},
	}

	err := engine.CheckCustomProperties(context.Background(), client, "org", "my-service", "main", openPRs)
	if err != nil {
		t.Fatalf("CheckCustomProperties: %v", err)
	}

	// Should still set properties via API.
	if len(client.setProperties) == 0 {
		t.Fatal("expected properties to be set via API even with existing PR")
	}

	// Should NOT create another PR.
	if client.createdPR != nil {
		t.Error("should not create duplicate catalog-info PR")
	}
}

func TestAPIMode_NoCatalog_StaleBranchCleanup(t *testing.T) {
	t.Parallel()

	engine := testEngineWithMode(false, "api")
	client := basePropertiesClient()
	// No catalog-info file.

	// Stale catalog-info branch exists but no open PR.
	client.branchSHAs["org/my-service/"+CatalogInfoBranchName] = "stale-sha"

	err := engine.CheckCustomProperties(context.Background(), client, "org", "my-service", "main", nil)
	if err != nil {
		t.Fatalf("CheckCustomProperties: %v", err)
	}

	// Should delete stale branch.
	if len(client.deletedBranches) != 1 || client.deletedBranches[0] != CatalogInfoBranchName {
		t.Errorf("expected stale branch %q deleted, got %v", CatalogInfoBranchName, client.deletedBranches)
	}

	// Should create new branch and PR.
	if len(client.createdBranches) != 1 || client.createdBranches[0] != CatalogInfoBranchName {
		t.Errorf("expected new branch %q created, got %v", CatalogInfoBranchName, client.createdBranches)
	}

	if client.createdPR == nil {
		t.Fatal("expected new catalog-info PR after stale branch cleanup")
	}
}

func TestAPIMode_DryRun(t *testing.T) {
	t.Parallel()

	engine := testEngineWithMode(true, "api")
	client := basePropertiesClient()
	client.fileContents["org/my-service/catalog-info.yaml"] = validCatalogInfo

	err := engine.CheckCustomProperties(context.Background(), client, "org", "my-service", "main", nil)
	if err != nil {
		t.Fatalf("CheckCustomProperties: %v", err)
	}

	if len(client.setProperties) != 0 {
		t.Error("dry run should not set properties via API")
	}

	if client.createdPR != nil {
		t.Error("dry run should not create PR")
	}

	if len(client.createdBranches) != 0 {
		t.Error("dry run should not create branches")
	}
}

// --- Disabled mode test ---

func TestCustomProperties_Disabled(t *testing.T) {
	t.Parallel()

	engine := testEngineWithMode(false, "")
	client := basePropertiesClient()
	client.fileContents["org/my-service/catalog-info.yaml"] = validCatalogInfo

	// Set all files as present so CheckRepo doesn't create file-rule PRs.
	client.contents["org/my-service/CODEOWNERS"] = true
	client.contents["org/my-service/.github/dependabot.yml"] = true

	err := engine.CheckRepo(context.Background(), client, "org", "my-service")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	// With mode="" the properties check should not run.
	// No properties should be set and no properties PRs created.
	if len(client.setProperties) != 0 {
		t.Error("disabled mode should not set any properties")
	}

	// createdPR should be nil (no file-rule PR since all files exist, no properties PR since disabled).
	if client.createdPR != nil {
		t.Error("disabled mode should not create any PR")
	}
}

// --- Helper unit tests ---

func TestDiffProperties(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		desired string
		current []*ghclient.CustomPropertyValue
		want    bool
	}{
		{
			name:    "all match",
			desired: validCatalogInfo,
			current: []*ghclient.CustomPropertyValue{
				{PropertyName: "Owner", Value: "platform-team"},
				{PropertyName: "Component", Value: "my-service"},
				{PropertyName: "JiraProject", Value: "PROJ"},
				{PropertyName: "JiraLabel", Value: "my-service"},
			},
			want: false,
		},
		{
			name:    "owner differs",
			desired: validCatalogInfo,
			current: []*ghclient.CustomPropertyValue{
				{PropertyName: "Owner", Value: "other-team"},
				{PropertyName: "Component", Value: "my-service"},
				{PropertyName: "JiraProject", Value: "PROJ"},
				{PropertyName: "JiraLabel", Value: "my-service"},
			},
			want: true,
		},
		{
			name:    "empty current",
			desired: validCatalogInfo,
			current: nil,
			want:    true,
		},
		{
			name:    "jira fields ignored when desired empty",
			desired: catalogInfoNoJira,
			current: []*ghclient.CustomPropertyValue{
				{PropertyName: "Owner", Value: "platform-team"},
				{PropertyName: "Component", Value: "my-service"},
				// JiraProject and JiraLabel not set — should not matter since desired is empty.
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Use catalog.Parse to get desired properties (same as production code).
			desired := parseForTest(tt.desired)

			got := diffProperties(desired, tt.current)
			if got != tt.want {
				t.Errorf("diffProperties() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildPropertiesPRBody_GHAMode(t *testing.T) {
	t.Parallel()

	props := parseForTest(validCatalogInfo)
	body := buildPropertiesPRBody(props, "github-action")

	if !strings.Contains(body, "Set Custom Properties") {
		t.Error("GHA body should contain 'Set Custom Properties'")
	}

	if !strings.Contains(body, "platform-team") {
		t.Error("GHA body should contain owner value")
	}

	if !strings.Contains(body, "PROJ") {
		t.Error("GHA body should contain JiraProject value")
	}

	if !strings.Contains(body, "platform-engineering") {
		t.Error("body should reference platform-engineering channel")
	}
}

func TestBuildPropertiesPRBody_APIMode(t *testing.T) {
	t.Parallel()

	body := buildPropertiesPRBody(nil, "api")

	if !strings.Contains(body, "catalog-info.yaml") {
		t.Error("API body should mention catalog-info.yaml")
	}

	if !strings.Contains(body, "TODO") {
		t.Error("API body should mention TODO placeholders")
	}

	if !strings.Contains(body, "platform-engineering") {
		t.Error("body should reference platform-engineering channel")
	}
}

// parseForTest is a test helper that wraps catalog.Parse.
func parseForTest(content string) *catalog.Properties {
	return catalog.Parse(content)
}
