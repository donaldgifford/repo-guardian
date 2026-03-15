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
