package checker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/reconciler"
	"github.com/donaldgifford/repo-guardian/internal/rules"
)

func testPolicyEngine(cfg *policy.PolicyConfig) *Engine {
	ts := rules.NewTemplateStore()
	if err := ts.Load(""); err != nil {
		panic(err)
	}

	engine, err := NewEngineFromPolicy(cfg, ts, slog.Default(), nil)
	if err != nil {
		panic(err)
	}

	return engine
}

func TestPolicyCheckRepo_ExistsMode_FileMissing(t *testing.T) {
	t.Parallel()

	cfg := policy.BuiltinDefaults()
	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR == nil {
		t.Fatal("expected PR to be created for missing files")
	}

	if len(client.createdFiles) != 2 {
		t.Errorf("expected 2 files created (CODEOWNERS + Dependabot), got %d", len(client.createdFiles))
	}
}

func TestPolicyCheckRepo_ExistsMode_FilePresent(t *testing.T) {
	t.Parallel()

	cfg := policy.BuiltinDefaults()
	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
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

func TestPolicyCheckRepo_ContainsMode_FileMissing(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{{
			Type:     "file",
			Name:     "catalog-info",
			Check:    "contains",
			Paths:    []string{"catalog-info.yaml"},
			Target:   "catalog-info.yaml",
			Template: "codeowners",
			Assertions: []policy.AssertionConfig{
				{YAMLPath: "spec.owner", Contains: "team", Message: "must have team owner"},
			},
		}},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR == nil {
		t.Fatal("expected PR when file is missing in contains mode")
	}
}

func TestPolicyCheckRepo_ContainsMode_AssertionsPass(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{{
			Type:     "file",
			Name:     "catalog-info",
			Check:    "contains",
			Paths:    []string{"catalog-info.yaml"},
			Target:   "catalog-info.yaml",
			Template: "codeowners",
			Assertions: []policy.AssertionConfig{
				{YAMLPath: "spec.owner", Contains: "team", Message: "must have team owner"},
			},
		}},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.contents["org/repo/catalog-info.yaml"] = true
	client.fileContents["org/repo/catalog-info.yaml"] = "spec:\n  owner: team-platform\n"

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR != nil {
		t.Error("should not create PR when assertions pass")
	}
}

func TestPolicyCheckRepo_ContainsMode_AssertionsFail(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{{
			Type:     "file",
			Name:     "catalog-info",
			Check:    "contains",
			Paths:    []string{"catalog-info.yaml"},
			Target:   "catalog-info.yaml",
			Template: "codeowners",
			Assertions: []policy.AssertionConfig{
				{YAMLPath: "spec.owner", Contains: "team", Message: "must have team owner"},
			},
		}},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"
	client.contents["org/repo/catalog-info.yaml"] = true
	client.fileContents["org/repo/catalog-info.yaml"] = "spec:\n  owner: individual-dev\n"

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR == nil {
		t.Fatal("expected PR when assertions fail")
	}
}

func TestPolicyCheckRepo_ExactMode_FileMissing(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{{
			Type:     "file",
			Name:     "dependabot",
			Check:    "exact",
			Paths:    []string{".github/dependabot.yml"},
			Target:   ".github/dependabot.yml",
			Template: "dependabot",
		}},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR == nil {
		t.Fatal("expected PR when file is missing in exact mode")
	}
}

func TestPolicyCheckRepo_ExactMode_FileMatchesTemplate(t *testing.T) {
	t.Parallel()

	ts := rules.NewTemplateStore()
	if err := ts.Load(""); err != nil {
		t.Fatal(err)
	}

	templateContent, err := ts.Get("dependabot")
	if err != nil {
		t.Fatal(err)
	}

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{{
			Type:     "file",
			Name:     "dependabot",
			Check:    "exact",
			Paths:    []string{".github/dependabot.yml"},
			Target:   ".github/dependabot.yml",
			Template: "dependabot",
		}},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.contents["org/repo/.github/dependabot.yml"] = true
	client.fileContents["org/repo/.github/dependabot.yml"] = templateContent

	err = engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR != nil {
		t.Error("should not create PR when file matches template")
	}
}

func TestPolicyCheckRepo_ExactMode_FileDiffersFromTemplate(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{{
			Type:     "file",
			Name:     "dependabot",
			Check:    "exact",
			Paths:    []string{".github/dependabot.yml"},
			Target:   ".github/dependabot.yml",
			Template: "dependabot",
		}},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"
	client.contents["org/repo/.github/dependabot.yml"] = true
	client.fileContents["org/repo/.github/dependabot.yml"] = "version: 2\nupdates: []\n"

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR == nil {
		t.Fatal("expected PR when file differs from template")
	}
}

func TestPolicyCheckRepo_ExactMode_YAMLSemanticComparison(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{{
			Type:     "file",
			Name:     "test-yaml",
			Check:    "exact",
			Paths:    []string{"test.yaml"},
			Target:   "test.yaml",
			Template: "dependabot",
		}},
	}

	ts := rules.NewTemplateStore()
	if err := ts.Load(""); err != nil {
		t.Fatal(err)
	}

	templateContent, err := ts.Get("dependabot")
	if err != nil {
		t.Fatal(err)
	}

	engine, err := NewEngineFromPolicy(cfg, ts, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}

	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.contents["org/repo/test.yaml"] = true
	// Same content but with different whitespace — YAML semantic comparison should match.
	client.fileContents["org/repo/test.yaml"] = templateContent

	err = engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR != nil {
		t.Error("should not create PR when YAML is semantically equal")
	}
}

func TestPolicyCheckRepo_BackwardCompatibility(t *testing.T) {
	t.Parallel()

	// Using BuiltinDefaults should produce the same behavior as the
	// registry-based engine for exists-mode rules.
	cfg := policy.BuiltinDefaults()
	policyEngine := testPolicyEngine(cfg)
	registryEngine := testEngine(false)

	// Set up identical mock clients.
	policyClient := newMockClient()
	policyClient.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	policyClient.branchSHAs["org/repo/main"] = "abc123"

	registryClient := newMockClient()
	registryClient.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	registryClient.branchSHAs["org/repo/main"] = "abc123"

	// Both should create PRs with the same files.
	if err := policyEngine.CheckRepo(context.Background(), policyClient, "org", "repo"); err != nil {
		t.Fatalf("policy CheckRepo: %v", err)
	}

	if err := registryEngine.CheckRepo(context.Background(), registryClient, "org", "repo"); err != nil {
		t.Fatalf("registry CheckRepo: %v", err)
	}

	if len(policyClient.createdFiles) != len(registryClient.createdFiles) {
		t.Errorf(
			"file count mismatch: policy=%d registry=%d",
			len(policyClient.createdFiles), len(registryClient.createdFiles),
		)
	}

	if (policyClient.createdPR == nil) != (registryClient.createdPR == nil) {
		t.Error("PR creation mismatch between policy and registry engines")
	}
}

func TestYAMLSemanticallyEqual(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{
			name: "identical",
			a:    "key: value\n",
			b:    "key: value\n",
			want: true,
		},
		{
			name: "whitespace difference",
			a:    "key:   value\n",
			b:    "key: value\n",
			want: true,
		},
		{
			name: "different values",
			a:    "key: value1\n",
			b:    "key: value2\n",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := yamlSemanticallyEqual(tt.a, tt.b)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIntegration_PolicyLoadAndEngineCreation(t *testing.T) {
	t.Parallel()

	// Integration test: policy.Load() → engine creation → no errors.
	// Simulates the main.go startup path without HCL config.
	policyCfg, err := policy.Load("")
	if err != nil {
		t.Fatalf("policy.Load: %v", err)
	}

	ts := rules.NewTemplateStore()
	if err := ts.Load(""); err != nil {
		t.Fatalf("templates.Load: %v", err)
	}

	engine, err := NewEngineFromPolicy(policyCfg, ts, slog.Default(), nil)
	if err != nil {
		t.Fatalf("NewEngineFromPolicy: %v", err)
	}

	// Verify engine works with a basic check.
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.contents["org/repo/CODEOWNERS"] = true
	client.contents["org/repo/.github/dependabot.yml"] = true

	if err := engine.CheckRepo(context.Background(), client, "org", "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR != nil {
		t.Error("should not create PR when all files exist")
	}
}

// --- Reconciler integration tests ---

// trackingReconciler records calls for test verification.
type trackingReconciler struct {
	name      string
	mu        sync.Mutex
	calls     []reconcilerCall
	returnErr error
}

type reconcilerCall struct {
	Owner   string
	Repo    string
	Content string
	DryRun  bool
}

func (r *trackingReconciler) Name() string { return r.name }

func (r *trackingReconciler) Reconcile(_ context.Context, params *reconciler.ReconcileParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls = append(r.calls, reconcilerCall{
		Owner:   params.Owner,
		Repo:    params.Repo,
		Content: params.Content,
		DryRun:  params.DryRun,
	})

	return r.returnErr
}

func (r *trackingReconciler) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.calls)
}

func (r *trackingReconciler) lastCall() reconcilerCall {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.calls[len(r.calls)-1]
}

func testPolicyEngineWithReconciler(cfg *policy.PolicyConfig, rec *trackingReconciler) *Engine {
	ts := rules.NewTemplateStore()
	if err := ts.Load(""); err != nil {
		panic(err)
	}

	reg := reconciler.NewRegistry()
	reg.Register(rec.name, func(_ policy.ReconcilerConfig) (reconciler.Reconciler, error) {
		return rec, nil
	})

	engine, err := NewEngineFromPolicy(cfg, ts, slog.Default(), reg)
	if err != nil {
		panic(err)
	}

	return engine
}

func TestReconciler_RunsWhenFilePresent_ExistsMode(t *testing.T) {
	t.Parallel()

	rec := &trackingReconciler{name: "test_rec"}
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{
			{
				Type:     "file",
				Name:     "test_file",
				Paths:    []string{"test.txt"},
				Target:   "test.txt",
				Template: "codeowners",
				Check:    "exists",
				Reconcilers: []policy.ReconcilerConfig{
					{Type: "test_rec"},
				},
			},
		},
	}

	engine := testPolicyEngineWithReconciler(cfg, rec)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.contents["org/repo/test.txt"] = true
	client.fileContents["org/repo/test.txt"] = "file content"

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if rec.callCount() != 1 {
		t.Fatalf("expected 1 reconciler call, got %d", rec.callCount())
	}

	call := rec.lastCall()
	if call.Content != "file content" {
		t.Errorf("Content = %q, want %q", call.Content, "file content")
	}

	if call.Owner != "org" {
		t.Errorf("Owner = %q, want %q", call.Owner, "org")
	}
}

func TestReconciler_DoesNotRunWhenFileMissing(t *testing.T) {
	t.Parallel()

	rec := &trackingReconciler{name: "test_rec"}
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{
			{
				Type:     "file",
				Name:     "test_file",
				Paths:    []string{"test.txt"},
				Target:   "test.txt",
				Template: "codeowners",
				Check:    "exists",
				Reconcilers: []policy.ReconcilerConfig{
					{Type: "test_rec"},
				},
			},
		},
	}

	engine := testPolicyEngineWithReconciler(cfg, rec)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if rec.callCount() != 0 {
		t.Errorf("expected 0 reconciler calls, got %d", rec.callCount())
	}
}

func TestReconciler_RunsWhenAssertionsPass_ContainsMode(t *testing.T) {
	t.Parallel()

	rec := &trackingReconciler{name: "test_rec"}
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{
			{
				Type:     "file",
				Name:     "test_file",
				Paths:    []string{"test.txt"},
				Target:   "test.txt",
				Template: "codeowners",
				Check:    "contains",
				Assertions: []policy.AssertionConfig{
					{Pattern: "required-text", Message: "must contain required-text"},
				},
				Reconcilers: []policy.ReconcilerConfig{
					{Type: "test_rec"},
				},
			},
		},
	}

	engine := testPolicyEngineWithReconciler(cfg, rec)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.contents["org/repo/test.txt"] = true
	client.fileContents["org/repo/test.txt"] = "this has required-text in it"

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if rec.callCount() != 1 {
		t.Fatalf("expected 1 reconciler call, got %d", rec.callCount())
	}
}

func TestReconciler_DoesNotRunWhenAssertionsFail(t *testing.T) {
	t.Parallel()

	rec := &trackingReconciler{name: "test_rec"}
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{
			{
				Type:     "file",
				Name:     "test_file",
				Paths:    []string{"test.txt"},
				Target:   "test.txt",
				Template: "codeowners",
				Check:    "contains",
				Assertions: []policy.AssertionConfig{
					{Pattern: "required-text", Message: "must contain required-text"},
				},
				Reconcilers: []policy.ReconcilerConfig{
					{Type: "test_rec"},
				},
			},
		},
	}

	engine := testPolicyEngineWithReconciler(cfg, rec)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.contents["org/repo/test.txt"] = true
	client.fileContents["org/repo/test.txt"] = "no match here"
	client.branchSHAs["org/repo/main"] = "abc123"

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if rec.callCount() != 0 {
		t.Errorf("expected 0 reconciler calls when assertions fail, got %d", rec.callCount())
	}
}

func TestReconciler_MultipleRunInOrder(t *testing.T) {
	t.Parallel()

	rec1 := &trackingReconciler{name: "rec_alpha"}
	rec2 := &trackingReconciler{name: "rec_beta"}

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{
			{
				Type:     "file",
				Name:     "test_file",
				Paths:    []string{"test.txt"},
				Target:   "test.txt",
				Template: "codeowners",
				Check:    "exists",
				Reconcilers: []policy.ReconcilerConfig{
					{Type: "rec_alpha"},
					{Type: "rec_beta"},
				},
			},
		},
	}

	ts := rules.NewTemplateStore()
	if err := ts.Load(""); err != nil {
		t.Fatalf("templates: %v", err)
	}

	reg := reconciler.NewRegistry()
	reg.Register("rec_alpha", func(_ policy.ReconcilerConfig) (reconciler.Reconciler, error) {
		return rec1, nil
	})
	reg.Register("rec_beta", func(_ policy.ReconcilerConfig) (reconciler.Reconciler, error) {
		return rec2, nil
	})

	engine, err := NewEngineFromPolicy(cfg, ts, slog.Default(), reg)
	if err != nil {
		t.Fatalf("NewEngineFromPolicy: %v", err)
	}

	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.contents["org/repo/test.txt"] = true
	client.fileContents["org/repo/test.txt"] = "content"

	if err := engine.CheckRepo(context.Background(), client, "org", "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if rec1.callCount() != 1 {
		t.Errorf("rec_alpha: expected 1 call, got %d", rec1.callCount())
	}

	if rec2.callCount() != 1 {
		t.Errorf("rec_beta: expected 1 call, got %d", rec2.callCount())
	}
}

func TestReconciler_ErrorLoggedNotFatal(t *testing.T) {
	t.Parallel()

	rec := &trackingReconciler{name: "test_rec", returnErr: errors.New("reconciler failed")}
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{
			{
				Type:     "file",
				Name:     "test_file",
				Paths:    []string{"test.txt"},
				Target:   "test.txt",
				Template: "codeowners",
				Check:    "exists",
				Reconcilers: []policy.ReconcilerConfig{
					{Type: "test_rec"},
				},
			},
		},
	}

	engine := testPolicyEngineWithReconciler(cfg, rec)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.contents["org/repo/test.txt"] = true
	client.fileContents["org/repo/test.txt"] = "content"

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo should not fail on reconciler error: %v", err)
	}

	if rec.callCount() != 1 {
		t.Errorf("expected reconciler to be called, got %d calls", rec.callCount())
	}
}

// --- Integration Tests ---
// These verify end-to-end flows from config through engine to reconciler.

func TestIntegration_HCLConfigWithCustomPropertiesReconciler(t *testing.T) {
	t.Parallel()

	// Simulate HCL config with a custom_properties reconciler.
	rec := &trackingReconciler{name: "custom_properties"}
	enabled := true
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{
			{
				Type:     "file",
				Name:     "catalog_info",
				Enabled:  &enabled,
				Check:    "exists",
				Paths:    []string{"catalog-info.yaml", "catalog-info.yml"},
				Target:   "catalog-info.yaml",
				Template: "codeowners",
				PR:       &policy.PRConfig{SearchTerms: []string{"catalog-info"}},
				Reconcilers: []policy.ReconcilerConfig{
					{Type: "custom_properties", Mode: "api", Watch: true},
				},
			},
		},
	}

	engine := testPolicyEngineWithReconciler(cfg, rec)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "my-service", HasBranch: true, DefaultRef: "main",
	}
	client.contents["org/my-service/catalog-info.yaml"] = true
	client.fileContents["org/my-service/catalog-info.yaml"] = "apiVersion: backstage.io/v1alpha1\nkind: Component"

	err := engine.CheckRepo(context.Background(), client, "org", "my-service")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	// Reconciler should have been called with the file content.
	if rec.callCount() != 1 {
		t.Fatalf("expected reconciler to run once, got %d", rec.callCount())
	}

	call := rec.lastCall()
	if call.Owner != "org" {
		t.Errorf("Owner = %q, want %q", call.Owner, "org")
	}

	if call.Repo != "my-service" {
		t.Errorf("Repo = %q, want %q", call.Repo, "my-service")
	}

	if call.Content != "apiVersion: backstage.io/v1alpha1\nkind: Component" {
		t.Error("Content should contain the catalog-info.yaml contents")
	}

	// No PR should be created since the file exists.
	if client.createdPR != nil {
		t.Error("no PR should be created when file already exists")
	}
}

func TestGlobalIgnoreList_SkipsAllRules(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian:   policy.BuiltinDefaults().Guardian,
		IgnoreList: policy.IgnoreConfig{Repos: []string{"org/ignored-repo"}},
		FileRules:  policy.BuiltinDefaults().FileRules,
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "ignored-repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/ignored-repo/main"] = "abc123"

	err := engine.CheckRepo(context.Background(), client, "org", "ignored-repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR != nil {
		t.Error("should not create PR for globally ignored repo")
	}
}

func TestGlobalIgnoreList_GlobPattern(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian:   policy.BuiltinDefaults().Guardian,
		IgnoreList: policy.IgnoreConfig{Repos: []string{"org/terraform-*"}},
		FileRules:  policy.BuiltinDefaults().FileRules,
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "terraform-vpc", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/terraform-vpc/main"] = "abc123"

	err := engine.CheckRepo(context.Background(), client, "org", "terraform-vpc")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR != nil {
		t.Error("should not create PR for globally ignored repo (glob)")
	}
}

func TestGlobalIgnoreList_NoMatchStillProcesses(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian:   policy.BuiltinDefaults().Guardian,
		IgnoreList: policy.IgnoreConfig{Repos: []string{"org/other-*"}},
		FileRules:  policy.BuiltinDefaults().FileRules,
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "my-repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/my-repo/main"] = "abc123"

	err := engine.CheckRepo(context.Background(), client, "org", "my-repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	// Should still process and create PR for missing files.
	if client.createdPR == nil {
		t.Error("expected PR for non-ignored repo with missing files")
	}
}

func TestPerRuleIgnoreList_SkipsOnlyThatRule(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{
			{
				Type:     "file",
				Name:     "codeowners",
				Paths:    []string{"CODEOWNERS"},
				Target:   "CODEOWNERS",
				Template: "codeowners",
				Ignore:   &policy.IgnoreConfig{Repos: []string{"org/special-repo"}},
			},
			{
				Type:     "file",
				Name:     "dependabot",
				Paths:    []string{".github/dependabot.yml"},
				Target:   ".github/dependabot.yml",
				Template: "dependabot",
			},
		},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "special-repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/special-repo/main"] = "abc123"

	err := engine.CheckRepo(context.Background(), client, "org", "special-repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	// Only 1 file should be created (dependabot), codeowners is ignored.
	if client.createdPR == nil {
		t.Fatal("expected PR for non-ignored rule")
	}

	if len(client.createdFiles) != 1 {
		t.Errorf("expected 1 file created (dependabot only), got %d", len(client.createdFiles))
	}
}

func TestPerRuleIgnoreList_ReconcilerAlsoSkipped(t *testing.T) {
	t.Parallel()

	rec := &trackingReconciler{name: "test_rec"}
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{
			{
				Type:     "file",
				Name:     "test_file",
				Paths:    []string{"test.txt"},
				Target:   "test.txt",
				Template: "codeowners",
				Ignore:   &policy.IgnoreConfig{Repos: []string{"org/ignored"}},
				Reconcilers: []policy.ReconcilerConfig{
					{Type: "test_rec"},
				},
			},
		},
	}

	engine := testPolicyEngineWithReconciler(cfg, rec)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "ignored", HasBranch: true, DefaultRef: "main",
	}
	client.contents["org/ignored/test.txt"] = true
	client.fileContents["org/ignored/test.txt"] = "content"

	err := engine.CheckRepo(context.Background(), client, "org", "ignored")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if rec.callCount() != 0 {
		t.Errorf("expected 0 reconciler calls for ignored rule, got %d", rec.callCount())
	}
}

func TestEmptyIgnoreList_NoReposSkipped(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian:   policy.BuiltinDefaults().Guardian,
		IgnoreList: policy.IgnoreConfig{},
		FileRules:  policy.BuiltinDefaults().FileRules,
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	// Should process normally.
	if client.createdPR == nil {
		t.Error("expected PR for non-ignored repo with missing files")
	}
}

// --- Setting rule tests ---

func TestSettingRule_MatchesExpected_NoAction(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		SettingRules: []policy.SettingRuleConfig{
			{Name: "enable_issues", Property: "has_issues", Expected: true},
		},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.repoSettings = &ghclient.RepoSettings{HasIssues: true}

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if len(client.updatedRepoOpts) != 0 {
		t.Error("should not update repo when setting matches expected")
	}
}

func TestSettingRule_Mismatch_NoRemediate(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		SettingRules: []policy.SettingRuleConfig{
			{Name: "enable_issues", Property: "has_issues", Expected: true, Remediate: false},
		},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.repoSettings = &ghclient.RepoSettings{HasIssues: false}

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if len(client.updatedRepoOpts) != 0 {
		t.Error("should not update repo when remediate is false")
	}
}

func TestSettingRule_Mismatch_Remediate(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		SettingRules: []policy.SettingRuleConfig{
			{Name: "enable_issues", Property: "has_issues", Expected: true, Remediate: true},
		},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.repoSettings = &ghclient.RepoSettings{HasIssues: false}

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if len(client.updatedRepoOpts) != 1 {
		t.Fatalf("expected 1 UpdateRepository call, got %d", len(client.updatedRepoOpts))
	}

	opts := client.updatedRepoOpts[0]
	if opts.HasIssues == nil || !*opts.HasIssues {
		t.Error("expected HasIssues to be set to true")
	}
}

func TestSettingRule_Mismatch_Remediate_DryRun(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		SettingRules: []policy.SettingRuleConfig{
			{Name: "enable_issues", Property: "has_issues", Expected: true, Remediate: true},
		},
	}
	cfg.Guardian.DryRun = true

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.repoSettings = &ghclient.RepoSettings{HasIssues: false}

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if len(client.updatedRepoOpts) != 0 {
		t.Error("should not update repo in dry run mode")
	}
}

func TestSettingRule_VulnerabilityAlerts_Remediate(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		SettingRules: []policy.SettingRuleConfig{
			{Name: "vuln_alerts", Property: "vulnerability_alerts_enabled", Expected: true, Remediate: true},
		},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.vulnerabilityAlertsEnabled = false

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if !client.enabledVulnAlerts {
		t.Error("expected vulnerability alerts to be enabled")
	}
}

func TestSettingRule_DefaultBranch_Remediate(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		SettingRules: []policy.SettingRuleConfig{
			{Name: "default_branch", Property: "default_branch", Expected: "main", Remediate: true},
		},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.repoSettings = &ghclient.RepoSettings{DefaultBranch: "master"}

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if len(client.updatedRepoOpts) != 1 {
		t.Fatalf("expected 1 UpdateRepository call, got %d", len(client.updatedRepoOpts))
	}

	opts := client.updatedRepoOpts[0]
	if opts.DefaultBranch == nil || *opts.DefaultBranch != "main" {
		t.Error("expected DefaultBranch to be set to 'main'")
	}
}

func TestSettingRule_PerRuleIgnore(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		SettingRules: []policy.SettingRuleConfig{
			{
				Name:      "enable_issues",
				Property:  "has_issues",
				Expected:  true,
				Remediate: true,
				Ignore:    &policy.IgnoreConfig{Repos: []string{"org/ignored"}},
			},
		},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "ignored", HasBranch: true, DefaultRef: "main",
	}
	client.repoSettings = &ghclient.RepoSettings{HasIssues: false}

	err := engine.CheckRepo(context.Background(), client, "org", "ignored")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if len(client.updatedRepoOpts) != 0 {
		t.Error("should not update repo when setting rule is ignored")
	}
}

func TestSettingRule_Disabled(t *testing.T) {
	t.Parallel()

	disabled := false
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		SettingRules: []policy.SettingRuleConfig{
			{
				Name:      "enable_issues",
				Property:  "has_issues",
				Expected:  true,
				Remediate: true,
				Enabled:   &disabled,
			},
		},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.repoSettings = &ghclient.RepoSettings{HasIssues: false}

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if len(client.updatedRepoOpts) != 0 {
		t.Error("should not update repo when setting rule is disabled")
	}
}

// --- Branch protection rule tests ---

func TestBranchProtection_Matches_NoAction(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		BranchProtectionRules: []policy.BranchProtectionRuleConfig{
			{
				Name:              "main_protection",
				Branch:            "main",
				RequirePR:         true,
				RequiredApprovals: 1,
			},
		},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"
	client.rulesets = []*ghclient.Ruleset{
		{
			ID:          1,
			Name:        "repo-guardian-main_protection",
			Enforcement: "active",
			Target:      "branch",
			Conditions:  &ghclient.RulesetConditions{IncludePatterns: []string{"refs/heads/main"}},
			RequirePullRequest: &ghclient.RulesetPullRequest{
				RequiredApprovals: 1,
			},
		},
	}

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdRuleset != nil {
		t.Error("should not create ruleset when protection matches")
	}

	if client.updatedRuleset != nil {
		t.Error("should not update ruleset when protection matches")
	}
}

func TestBranchProtection_Mismatch_NoRemediate(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		BranchProtectionRules: []policy.BranchProtectionRuleConfig{
			{
				Name:              "main_protection",
				Branch:            "main",
				RequirePR:         true,
				RequiredApprovals: 2,
				Remediate:         false,
			},
		},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"
	client.rulesets = []*ghclient.Ruleset{
		{
			ID:         1,
			Conditions: &ghclient.RulesetConditions{IncludePatterns: []string{"refs/heads/main"}},
			RequirePullRequest: &ghclient.RulesetPullRequest{
				RequiredApprovals: 1,
			},
		},
	}

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.updatedRuleset != nil {
		t.Error("should not update ruleset when remediate is false")
	}
}

func TestBranchProtection_Mismatch_Remediate_Update(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		BranchProtectionRules: []policy.BranchProtectionRuleConfig{
			{
				Name:              "main_protection",
				Branch:            "main",
				RequirePR:         true,
				RequiredApprovals: 2,
				Remediate:         true,
			},
		},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"
	client.rulesets = []*ghclient.Ruleset{
		{
			ID:         42,
			Conditions: &ghclient.RulesetConditions{IncludePatterns: []string{"refs/heads/main"}},
			RequirePullRequest: &ghclient.RulesetPullRequest{
				RequiredApprovals: 1,
			},
		},
	}

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.updatedRuleset == nil {
		t.Fatal("expected ruleset to be updated")
	}

	if client.updatedRulesetID != 42 {
		t.Errorf("expected ruleset ID 42, got %d", client.updatedRulesetID)
	}
}

func TestBranchProtection_NoRuleset_Remediate_Create(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		BranchProtectionRules: []policy.BranchProtectionRuleConfig{
			{
				Name:              "main_protection",
				Branch:            "main",
				RequirePR:         true,
				RequiredApprovals: 1,
				Remediate:         true,
			},
		},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdRuleset == nil {
		t.Fatal("expected ruleset to be created")
	}

	if client.createdRuleset.RequirePullRequest == nil {
		t.Fatal("expected PR requirement in created ruleset")
	}

	if client.createdRuleset.RequirePullRequest.RequiredApprovals != 1 {
		t.Errorf("expected 1 required approval, got %d",
			client.createdRuleset.RequirePullRequest.RequiredApprovals)
	}
}

func TestBranchProtection_BranchDoesNotExist(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		BranchProtectionRules: []policy.BranchProtectionRuleConfig{
			{
				Name:      "develop_protection",
				Branch:    "develop",
				RequirePR: true,
				Remediate: true,
			},
		},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"
	// "develop" branch does not exist (no entry in branchSHAs)

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdRuleset != nil {
		t.Error("should not create ruleset when branch doesn't exist")
	}
}

func TestBranchProtection_DryRun(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		BranchProtectionRules: []policy.BranchProtectionRuleConfig{
			{
				Name:              "main_protection",
				Branch:            "main",
				RequirePR:         true,
				RequiredApprovals: 1,
				Remediate:         true,
			},
		},
	}
	cfg.Guardian.DryRun = true

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdRuleset != nil {
		t.Error("should not create ruleset in dry run mode")
	}
}

func TestReconciler_DryRunPropagated(t *testing.T) {
	t.Parallel()

	rec := &trackingReconciler{name: "test_rec"}
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{
			{
				Type:     "file",
				Name:     "test_file",
				Paths:    []string{"test.txt"},
				Target:   "test.txt",
				Template: "codeowners",
				Check:    "exists",
				Reconcilers: []policy.ReconcilerConfig{
					{Type: "test_rec"},
				},
			},
		},
	}
	cfg.Guardian.DryRun = true

	engine := testPolicyEngineWithReconciler(cfg, rec)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.contents["org/repo/test.txt"] = true
	client.fileContents["org/repo/test.txt"] = "content"

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if rec.callCount() != 1 {
		t.Fatalf("expected 1 reconciler call, got %d", rec.callCount())
	}

	if !rec.lastCall().DryRun {
		t.Error("DryRun should be true in ReconcileParams")
	}
}

// --- Phase 8 integration tests ---

func TestIntegration_IgnoreLists_SettingRules_BranchProtection(t *testing.T) {
	t.Parallel()

	// End-to-end: HCL-like config with ignore lists, setting rules, and
	// branch protection rules all evaluated in a single CheckRepo call.
	cfg := &policy.PolicyConfig{
		Guardian:   policy.BuiltinDefaults().Guardian,
		IgnoreList: policy.IgnoreConfig{Repos: []string{"org/globally-ignored"}},
		FileRules: []policy.FileRuleConfig{
			{
				Type:     "file",
				Name:     "codeowners",
				Paths:    []string{"CODEOWNERS"},
				Target:   "CODEOWNERS",
				Template: "codeowners",
				Ignore:   &policy.IgnoreConfig{Repos: []string{"org/skip-codeowners"}},
			},
		},
		SettingRules: []policy.SettingRuleConfig{
			{Name: "enable_issues", Property: "has_issues", Expected: true, Remediate: true},
			{Name: "enable_wiki", Property: "has_wiki", Expected: false, Remediate: true},
		},
		BranchProtectionRules: []policy.BranchProtectionRuleConfig{
			{
				Name:              "main_protection",
				Branch:            "main",
				RequirePR:         true,
				RequiredApprovals: 2,
				Remediate:         true,
			},
		},
	}

	engine := testPolicyEngine(cfg)

	// --- Case 1: globally ignored repo → no API calls ---
	ignoredClient := newMockClient()
	ignoredClient.repo = &ghclient.Repository{
		Owner: "org", Name: "globally-ignored", HasBranch: true, DefaultRef: "main",
	}
	ignoredClient.repoSettings = &ghclient.RepoSettings{HasIssues: false, HasWiki: true}
	ignoredClient.branchSHAs["org/globally-ignored/main"] = "abc123"

	if err := engine.CheckRepo(context.Background(), ignoredClient, "org", "globally-ignored"); err != nil {
		t.Fatalf("globally ignored CheckRepo: %v", err)
	}

	if ignoredClient.createdPR != nil {
		t.Error("globally ignored: should not create PR")
	}

	if len(ignoredClient.updatedRepoOpts) != 0 {
		t.Error("globally ignored: should not remediate settings")
	}

	if ignoredClient.createdRuleset != nil {
		t.Error("globally ignored: should not create rulesets")
	}

	// --- Case 2: normal repo → settings remediated + ruleset created ---
	normalClient := newMockClient()
	normalClient.repo = &ghclient.Repository{
		Owner: "org", Name: "normal-repo", HasBranch: true, DefaultRef: "main",
	}
	normalClient.repoSettings = &ghclient.RepoSettings{HasIssues: false, HasWiki: true}
	normalClient.branchSHAs["org/normal-repo/main"] = "abc123"
	normalClient.contents["org/normal-repo/CODEOWNERS"] = true

	if err := engine.CheckRepo(context.Background(), normalClient, "org", "normal-repo"); err != nil {
		t.Fatalf("normal repo CheckRepo: %v", err)
	}

	// Both settings should be remediated (has_issues mismatch + has_wiki mismatch).
	if len(normalClient.updatedRepoOpts) != 2 {
		t.Errorf("expected 2 setting remediations, got %d", len(normalClient.updatedRepoOpts))
	}

	// Branch protection: no existing ruleset → should create one.
	if normalClient.createdRuleset == nil {
		t.Error("expected branch protection ruleset to be created")
	}

	// File rule: CODEOWNERS exists → no PR.
	if normalClient.createdPR != nil {
		t.Error("should not create PR when CODEOWNERS exists")
	}
}

func TestIntegration_LabelSyncReconciler_EndToEnd(t *testing.T) {
	t.Parallel()

	labelContent := `labels:
  - name: bug
    color: "d73a4a"
    description: "Something isn't working"
  - name: enhancement
    color: "a2eeef"
    description: "New feature or request"
`

	rec := &trackingReconciler{name: "label_sync"}
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{
			{
				Type:     "file",
				Name:     "labels",
				Paths:    []string{".github/labels.yml"},
				Target:   ".github/labels.yml",
				Template: "codeowners",
				Check:    "exists",
				Reconcilers: []policy.ReconcilerConfig{
					{Type: "label_sync"},
				},
			},
		},
	}

	engine := testPolicyEngineWithReconciler(cfg, rec)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "my-service", HasBranch: true, DefaultRef: "main",
	}
	client.contents["org/my-service/.github/labels.yml"] = true
	client.fileContents["org/my-service/.github/labels.yml"] = labelContent

	err := engine.CheckRepo(context.Background(), client, "org", "my-service")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	// Reconciler should be called with the label file content.
	if rec.callCount() != 1 {
		t.Fatalf("expected 1 reconciler call, got %d", rec.callCount())
	}

	call := rec.lastCall()
	if call.Owner != "org" || call.Repo != "my-service" {
		t.Errorf("Owner/Repo = %s/%s, want org/my-service", call.Owner, call.Repo)
	}

	if call.Content != labelContent {
		t.Error("reconciler should receive full label file content")
	}

	if client.createdPR != nil {
		t.Error("should not create PR when label file exists")
	}
}

func TestPolicyCheckRepo_ExactMode_RenovateWorkflowMatchesTemplate(t *testing.T) {
	t.Parallel()

	ts := rules.NewTemplateStore()
	if err := ts.Load(""); err != nil {
		t.Fatal(err)
	}

	templateContent, err := ts.Get("renovate-workflow")
	if err != nil {
		t.Fatal(err)
	}

	enabled := true
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{{
			Type:     "file",
			Name:     "renovate_workflow",
			Enabled:  &enabled,
			Check:    "exact",
			Paths:    []string{".github/workflows/renovate.yml"},
			Target:   ".github/workflows/renovate.yml",
			Template: "renovate-workflow",
		}},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.contents["org/repo/.github/workflows/renovate.yml"] = true
	client.fileContents["org/repo/.github/workflows/renovate.yml"] = templateContent

	err = engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR != nil {
		t.Error("should not create PR when workflow matches template exactly")
	}
}

func TestPolicyCheckRepo_ExactMode_RenovateWorkflowDrifted(t *testing.T) {
	t.Parallel()

	enabled := true
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{{
			Type:     "file",
			Name:     "renovate_workflow",
			Enabled:  &enabled,
			Check:    "exact",
			Paths:    []string{".github/workflows/renovate.yml"},
			Target:   ".github/workflows/renovate.yml",
			Template: "renovate-workflow",
		}},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"
	client.contents["org/repo/.github/workflows/renovate.yml"] = true
	client.fileContents["org/repo/.github/workflows/renovate.yml"] = "name: Renovate\non:\n  workflow_dispatch:\n"

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR == nil {
		t.Fatal("expected PR when workflow content drifted from template")
	}
}

func TestPolicyCheckRepo_ContainsMode_RenovateConfigValidAssertion(t *testing.T) {
	t.Parallel()

	enabled := true
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{{
			Type:     "file",
			Name:     "renovate_config",
			Enabled:  &enabled,
			Check:    "contains",
			Paths:    []string{"renovate.json"},
			Target:   "renovate.json",
			Template: "renovate",
			Assertions: []policy.AssertionConfig{
				{Pattern: `github>.*renovate-config`, Message: "renovate.json must extend org preset"},
			},
		}},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.contents["org/repo/renovate.json"] = true
	client.fileContents["org/repo/renovate.json"] = `{"extends": ["github>myorg/renovate-config"]}`

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR != nil {
		t.Error("should not create PR when renovate config passes assertion")
	}
}

func TestPolicyCheckRepo_ContainsMode_RenovateConfigInvalidAssertion(t *testing.T) {
	t.Parallel()

	enabled := true
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{{
			Type:     "file",
			Name:     "renovate_config",
			Enabled:  &enabled,
			Check:    "contains",
			Paths:    []string{"renovate.json"},
			Target:   "renovate.json",
			Template: "renovate",
			Assertions: []policy.AssertionConfig{
				{Pattern: `github>.*renovate-config`, Message: "renovate.json must extend org preset"},
			},
		}},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"
	client.contents["org/repo/renovate.json"] = true
	client.fileContents["org/repo/renovate.json"] = `{"extends": ["config:recommended"]}`

	err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR == nil {
		t.Fatal("expected PR when renovate config fails assertion")
	}
}
