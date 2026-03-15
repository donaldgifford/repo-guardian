package policy

import "testing"

func TestExtractWatchedPaths_WatchTrue(t *testing.T) {
	t.Parallel()

	cfg := &PolicyConfig{
		FileRules: []FileRuleConfig{
			{
				Paths: []string{"catalog-info.yaml", "catalog-info.yml"},
				Reconcilers: []ReconcilerConfig{
					{Type: "custom_properties", Watch: true},
				},
			},
		},
	}

	got := ExtractWatchedPaths(cfg)

	if len(got) != 2 {
		t.Fatalf("expected 2 watched paths, got %d", len(got))
	}

	for _, path := range []string{"catalog-info.yaml", "catalog-info.yml"} {
		if !got[path] {
			t.Errorf("expected %q to be watched", path)
		}
	}
}

func TestExtractWatchedPaths_WatchFalse(t *testing.T) {
	t.Parallel()

	cfg := &PolicyConfig{
		FileRules: []FileRuleConfig{
			{
				Paths: []string{"catalog-info.yaml"},
				Reconcilers: []ReconcilerConfig{
					{Type: "custom_properties", Watch: false},
				},
			},
		},
	}

	got := ExtractWatchedPaths(cfg)

	if len(got) != 0 {
		t.Errorf("expected 0 watched paths, got %d", len(got))
	}
}

func TestExtractWatchedPaths_NoReconciler(t *testing.T) {
	t.Parallel()

	cfg := &PolicyConfig{
		FileRules: []FileRuleConfig{
			{
				Paths: []string{"CODEOWNERS"},
			},
		},
	}

	got := ExtractWatchedPaths(cfg)

	if len(got) != 0 {
		t.Errorf("expected 0 watched paths, got %d", len(got))
	}
}

func TestExtractWatchedPaths_MultipleRules(t *testing.T) {
	t.Parallel()

	cfg := &PolicyConfig{
		FileRules: []FileRuleConfig{
			{
				Paths: []string{"catalog-info.yaml"},
				Reconcilers: []ReconcilerConfig{
					{Type: "custom_properties", Watch: true},
				},
			},
			{
				Paths: []string{"CODEOWNERS"},
			},
			{
				Paths: []string{".github/dependabot.yml"},
				Reconcilers: []ReconcilerConfig{
					{Type: "label_sync", Watch: true},
				},
			},
		},
	}

	got := ExtractWatchedPaths(cfg)

	if len(got) != 2 {
		t.Fatalf("expected 2 watched paths, got %d", len(got))
	}

	if !got["catalog-info.yaml"] {
		t.Error("expected catalog-info.yaml to be watched")
	}

	if !got[".github/dependabot.yml"] {
		t.Error("expected .github/dependabot.yml to be watched")
	}
}

func TestExtractWatchedPaths_EmptyConfig(t *testing.T) {
	t.Parallel()

	cfg := &PolicyConfig{}

	got := ExtractWatchedPaths(cfg)

	if len(got) != 0 {
		t.Errorf("expected 0 watched paths, got %d", len(got))
	}
}
