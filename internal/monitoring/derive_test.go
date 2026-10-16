package monitoring_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/controls/builtin"
	"github.com/donaldgifford/repo-guardian/internal/monitoring"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// load loads a policy root with the built-in control types.
func load(t *testing.T, root string) *policy.Snapshot {
	t.Helper()

	snap, err := policy.Load(root, builtin.Registry(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("policy.Load(%s) = %v, want nil", root, err)
	}

	return snap
}

// writeRoot writes a policy root from files keyed by relative path.
func writeRoot(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()

	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

const codeownersCatalogue = `control "codeowners" {
  version  = 1
  type     = "codeowners"
  title    = "CODEOWNERS"
  template = "codeowners"

  rule "exists" {
    remediate = true
  }
}
`

const workflowPropertiesCatalogue = `control "custom_properties" {
  version = 1
  type    = "custom_properties"
  title   = "Custom properties"

  rule "matches" {
    remediate = true
  }

  remediation {
    apply = "workflow"
  }
}
`

// twoOrgRoot assigns codeowners and custom_properties to acme and
// globex; acme excludes codeowners org-wide.
func twoOrgRoot(t *testing.T) string {
	t.Helper()

	return writeRoot(t, map[string]string{
		"enterprise.hcl": `enterprise {
  orgs     = ["acme", "globex"]
  controls = ["codeowners@1", "custom_properties@1"]
}
`,
		"catalogue/codeowners.hcl":        codeownersCatalogue,
		"catalogue/custom_properties.hcl": workflowPropertiesCatalogue,
		"orgs/acme.hcl": `org "acme" {
  exclude "codeowners@1" {
    reason = "acme keeps owners elsewhere"
  }
}
`,
	})
}

func controlByID(t *testing.T, m *monitoring.Model, id string) monitoring.Control {
	t.Helper()

	for i := range m.Controls {
		if m.Controls[i].ID == id {
			return m.Controls[i]
		}
	}

	t.Fatalf("model has no control %s; controls = %+v", id, m.Controls)

	return monitoring.Control{}
}

func TestDerive_KeyedOnControlID(t *testing.T) {
	t.Parallel()

	m := monitoring.Derive(load(t, twoOrgRoot(t)), monitoring.Options{ConfigPath: "root"})

	if got := m.OrgNames(); !slices.Equal(got, []string{"acme", "globex"}) {
		t.Errorf("orgs = %v, want [acme globex]", got)
	}

	if c := controlByID(t, m, "codeowners@1"); !slices.Equal(c.Orgs, []string{"globex"}) || !c.Remediates || c.Type != "codeowners" {
		t.Errorf("codeowners@1 = %+v, want assigned in globex only (acme excludes it), remediable", c)
	}

	if c := controlByID(t, m, "custom_properties@1"); c.Apply != "workflow" || !slices.Equal(c.Orgs, []string{"acme", "globex"}) {
		t.Errorf("custom_properties@1 = %+v, want apply=workflow in both orgs", c)
	}

	if got := m.ControlsFor("acme"); len(got) != 1 || got[0].ID != "custom_properties@1" {
		t.Errorf("ControlsFor(acme) = %+v, want custom_properties@1 alone", got)
	}

	if m.Source.ConfigPath != "root" || !strings.HasPrefix(m.Source.PolicyVersion, "controls:") {
		t.Errorf("source = %+v, want the root and a controls: version", m.Source)
	}
}

func TestDerive_MechanismsFromTheSnapshot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		root func(*testing.T) string
		want []monitoring.Mechanism
	}{
		{
			name: "custom_properties with apply = workflow",
			root: twoOrgRoot,
			want: []monitoring.Mechanism{
				monitoring.MechanismCustomProperties, monitoring.MechanismCustomPropertiesGHA, monitoring.MechanismFileRules,
			},
		},
		{
			name: "the full example",
			root: func(*testing.T) string { return filepath.Join("..", "..", "examples", "policy", "full") },
			want: []monitoring.Mechanism{
				monitoring.MechanismBranchProtectionRemediation, monitoring.MechanismBranchProtectionRules,
				monitoring.MechanismCustomProperties, monitoring.MechanismFileRules,
				monitoring.MechanismSettingRemediation, monitoring.MechanismSettingRules,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			want := slices.Clone(tt.want)
			slices.Sort(want)

			if got := monitoring.Derive(load(t, tt.root(t)), monitoring.Options{}).Mechanisms.Sorted(); !reflect.DeepEqual(got, want) {
				t.Errorf("mechanisms = %v, want %v", got, want)
			}
		})
	}
}

func TestDerive_UnassignedControlTurnsNothingOn(t *testing.T) {
	t.Parallel()

	root := writeRoot(t, map[string]string{
		"enterprise.hcl":                  "enterprise {\n  orgs     = [\"acme\"]\n  controls = [\"codeowners@1\"]\n}\n",
		"catalogue/codeowners.hcl":        codeownersCatalogue,
		"catalogue/custom_properties.hcl": workflowPropertiesCatalogue,
	})

	m := monitoring.Derive(load(t, root), monitoring.Options{})

	if c := controlByID(t, m, "custom_properties@1"); len(c.Orgs) != 0 {
		t.Errorf("custom_properties@1 orgs = %v, want none: nothing assigns it", c.Orgs)
	}

	if m.Mechanisms.HasAny(monitoring.MechanismCustomProperties) {
		t.Error("an unassigned custom_properties control turned its mechanism on; its alerts would watch series nothing produces")
	}
}

func TestDerive_UnconfiguredStaticTier(t *testing.T) {
	t.Parallel()

	m := monitoring.Derive(nil, monitoring.Options{ExtraOrgs: []string{"acme", "acme-labs-*", ""}})

	if len(m.Controls) != 0 || len(m.Mechanisms) != 0 || m.Source.PolicyVersion != "" {
		t.Errorf("Derive(nil) = %+v, want no controls, mechanisms or version", m)
	}

	want := []monitoring.Org{{Name: "acme"}, {Name: "acme-labs-*", Pattern: true}}
	if !reflect.DeepEqual(m.Orgs, want) {
		t.Errorf("orgs = %+v, want the --org names with the pattern flagged and the empty one dropped", m.Orgs)
	}
}

func TestDerive_IsDeterministic(t *testing.T) {
	t.Parallel()

	snap := load(t, filepath.Join("..", "..", "examples", "policy", "full"))
	first := monitoring.Derive(snap, monitoring.Options{ExtraOrgs: []string{"zeta", "alpha"}})

	for range 10 {
		if again := monitoring.Derive(snap, monitoring.Options{ExtraOrgs: []string{"zeta", "alpha"}}); !reflect.DeepEqual(first, again) {
			t.Fatalf("Derive is not deterministic:\n%+v\n%+v", first, again)
		}
	}

	if !slices.IsSortedFunc(first.Controls, func(a, b monitoring.Control) int { return strings.Compare(a.ID, b.ID) }) {
		t.Errorf("controls are not sorted by id: %+v", first.Controls)
	}
}

// TestMechanisms_HasAnyWithNoArguments pins that an artifact requiring
// no mechanism is unconditional.
//
// The common case. An empty-slice check that returned false here would
// filter out every unconditional alert.
func TestMechanisms_HasAnyWithNoArguments(t *testing.T) {
	t.Parallel()

	var ms monitoring.Mechanisms = map[monitoring.Mechanism]struct{}{}

	if !ms.HasAny() {
		t.Error("HasAny() with no arguments = false, want true; unconditional artifacts would be dropped")
	}

	if ms.HasAny(monitoring.MechanismAbsentRules) {
		t.Error("HasAny(absent_rules) = true on an empty set")
	}
}
