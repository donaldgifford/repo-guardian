package policy_test

import (
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/policy"
)

func TestVersion_Deterministic(t *testing.T) {
	t.Parallel()

	cfg := policy.BuiltinDefaults()
	templates := map[string]string{"codeowners": "* @platform"}

	first, err := policy.Version(cfg, templates)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}

	second, err := policy.Version(cfg, templates)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}

	if first != second {
		t.Errorf("expected deterministic hash, got %q vs %q", first, second)
	}

	if len(first) != 64 {
		t.Errorf("expected 64-char hex hash, got %d chars", len(first))
	}
}

func TestVersion_TemplateContentChangesHash(t *testing.T) {
	t.Parallel()

	cfg := policy.BuiltinDefaults()

	a, _ := policy.Version(cfg, map[string]string{"codeowners": "* @team-a"})
	b, _ := policy.Version(cfg, map[string]string{"codeowners": "* @team-b"})

	if a == b {
		t.Error("template content change must alter the hash")
	}
}

func TestVersion_TemplateOrderIrrelevant(t *testing.T) {
	t.Parallel()

	cfg := policy.BuiltinDefaults()

	a, _ := policy.Version(cfg, map[string]string{"a": "1", "b": "2", "c": "3"})
	b, _ := policy.Version(cfg, map[string]string{"c": "3", "a": "1", "b": "2"})

	if a != b {
		t.Error("template ordering must not affect the hash")
	}
}

func TestVersion_PolicyConfigChangesHash(t *testing.T) {
	t.Parallel()

	cfg1 := policy.BuiltinDefaults()
	cfg2 := policy.BuiltinDefaults()
	cfg2.Guardian.DryRun = !cfg2.Guardian.DryRun

	templates := map[string]string{"codeowners": "* @platform"}

	a, _ := policy.Version(cfg1, templates)
	b, _ := policy.Version(cfg2, templates)

	if a == b {
		t.Error("policy config change must alter the hash")
	}
}

func TestVersion_NilConfigErrors(t *testing.T) {
	t.Parallel()

	if _, err := policy.Version(nil, nil); err == nil {
		t.Error("expected error on nil config")
	}
}

func TestVersion_EmptyTemplatesValid(t *testing.T) {
	t.Parallel()

	cfg := policy.BuiltinDefaults()

	hash, err := policy.Version(cfg, nil)
	if err != nil {
		t.Errorf("nil templates should be valid: %v", err)
	}

	if len(hash) != 64 {
		t.Errorf("expected 64-char hash, got %d", len(hash))
	}
}
