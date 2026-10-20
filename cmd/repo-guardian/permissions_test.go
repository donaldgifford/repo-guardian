package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/controls/builtin"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// TestRemediationPermissions is IMPL-0030 5.6: the Remediation App's set
// follows the policy, and Workflows write appears only when a control
// writes under .github/workflows/.
func TestRemediationPermissions(t *testing.T) {
	t.Parallel()

	codeownersOnly := t.TempDir()
	for name, body := range map[string]string{
		"enterprise.hcl": "enterprise {\n  orgs     = [\"acme\"]\n  controls = [\"codeowners@1\"]\n}\n",
		"catalogue/codeowners.hcl": "control \"codeowners\" {\n  version  = 1\n  type     = \"codeowners\"\n  title    = \"CODEOWNERS\"\n" +
			"  template = \"codeowners\"\n\n  rule \"exists\" {\n    remediate = true\n  }\n}\n",
	} {
		p := filepath.Join(codeownersOnly, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, tt := range []struct {
		name, root string
		want       []string
	}{
		{"file controls only", codeownersOnly, []string{"Contents: write", "Metadata: read", "Pull requests: write"}},
		{"examples/policy/full", "../../examples/policy/full", []string{
			"Administration: write", "Contents: write", "Custom properties: write", "Issues: write", "Metadata: read",
			"Pull requests: write", "Workflows: write",
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			snap, err := policy.Load(tt.root, builtin.Registry(), slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatal(err)
			}

			if got := permissionStrings(remediationPermissions(snap)); !slices.Equal(got, tt.want) {
				t.Errorf("remediationPermissions = %v, want %v", got, tt.want)
			}
		})
	}
}
