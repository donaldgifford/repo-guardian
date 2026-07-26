package reconciler_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/reconciler"
)

// Compile-time interface compliance check.
var _ reconciler.Reconciler = (*stubReconciler)(nil)

// stubReconciler is a test double implementing the Reconciler interface.
type stubReconciler struct {
	name          string
	called        bool
	returnErr     error
	runsOnAbsence bool
}

func (s *stubReconciler) Name() string { return s.name }

func (s *stubReconciler) RunsOnAbsence() bool { return s.runsOnAbsence }

func (s *stubReconciler) Reconcile(_ context.Context, _ *reconciler.ReconcileParams) error {
	s.called = true
	return s.returnErr
}

func TestReconcileParams_Construction(t *testing.T) {
	logger := slog.Default()
	params := reconciler.ReconcileParams{
		Owner:         "myorg",
		Repo:          "myrepo",
		DefaultBranch: "main",
		Content:       "file content",
		DryRun:        true,
		Logger:        logger,
	}

	if params.Owner != "myorg" {
		t.Errorf("Owner = %q, want %q", params.Owner, "myorg")
	}

	if params.Repo != "myrepo" {
		t.Errorf("Repo = %q, want %q", params.Repo, "myrepo")
	}

	if params.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %q, want %q", params.DefaultBranch, "main")
	}

	if params.Content != "file content" {
		t.Errorf("Content = %q, want %q", params.Content, "file content")
	}

	if !params.DryRun {
		t.Error("DryRun = false, want true")
	}

	if params.Logger != logger {
		t.Error("Logger not set correctly")
	}
}

func TestRegistry_RegisterAndBuild(t *testing.T) {
	reg := reconciler.NewRegistry()

	factory := func(_ policy.ReconcilerConfig) (reconciler.Reconciler, error) {
		return &stubReconciler{name: "test_reconciler"}, nil
	}

	reg.Register("test", factory)

	cfg := policy.ReconcilerConfig{Type: "test"}

	r, err := reg.Build(cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if r.Name() != "test_reconciler" {
		t.Errorf("Name() = %q, want %q", r.Name(), "test_reconciler")
	}
}

func TestRegistry_Build_UnknownType(t *testing.T) {
	reg := reconciler.NewRegistry()

	cfg := policy.ReconcilerConfig{Type: "nonexistent"}

	_, err := reg.Build(cfg)
	if err == nil {
		t.Fatal("Build() expected error for unknown type, got nil")
	}

	if !errors.Is(err, err) {
		t.Errorf("unexpected error type: %v", err)
	}

	want := `unknown reconciler type: "nonexistent"`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestRegistry_Build_FactoryError(t *testing.T) {
	reg := reconciler.NewRegistry()

	factoryErr := errors.New("factory failed")
	factory := func(_ policy.ReconcilerConfig) (reconciler.Reconciler, error) {
		return nil, factoryErr
	}

	reg.Register("broken", factory)

	cfg := policy.ReconcilerConfig{Type: "broken"}

	_, err := reg.Build(cfg)
	if !errors.Is(err, factoryErr) {
		t.Errorf("Build() error = %v, want %v", err, factoryErr)
	}
}

func TestRegistry_Build_PassesConfig(t *testing.T) {
	reg := reconciler.NewRegistry()

	var receivedCfg policy.ReconcilerConfig

	factory := func(cfg policy.ReconcilerConfig) (reconciler.Reconciler, error) {
		receivedCfg = cfg
		return &stubReconciler{name: "configured"}, nil
	}

	reg.Register("custom", factory)

	cfg := policy.ReconcilerConfig{
		Type:  "custom",
		Mode:  "api",
		Watch: true,
	}

	_, err := reg.Build(cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if receivedCfg.Mode != "api" {
		t.Errorf("factory received Mode = %q, want %q", receivedCfg.Mode, "api")
	}

	if !receivedCfg.Watch {
		t.Error("factory received Watch = false, want true")
	}
}

func TestRegistry_MultipleFactories(t *testing.T) {
	reg := reconciler.NewRegistry()

	reg.Register("alpha", func(_ policy.ReconcilerConfig) (reconciler.Reconciler, error) {
		return &stubReconciler{name: "alpha"}, nil
	})

	reg.Register("beta", func(_ policy.ReconcilerConfig) (reconciler.Reconciler, error) {
		return &stubReconciler{name: "beta"}, nil
	})

	r1, err := reg.Build(policy.ReconcilerConfig{Type: "alpha"})
	if err != nil {
		t.Fatalf("Build alpha error = %v", err)
	}

	r2, err := reg.Build(policy.ReconcilerConfig{Type: "beta"})
	if err != nil {
		t.Fatalf("Build beta error = %v", err)
	}

	if r1.Name() != "alpha" {
		t.Errorf("alpha Name() = %q", r1.Name())
	}

	if r2.Name() != "beta" {
		t.Errorf("beta Name() = %q", r2.Name())
	}
}
