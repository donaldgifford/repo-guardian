package policy

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

func TestSnapshot_Version(t *testing.T) {
	t.Parallel()

	base := map[string]string{
		"enterprise.hcl":           minimalEnterprise,
		"catalogue/codeowners.hcl": codeownersCatalogue,
	}

	load := func(t *testing.T, files map[string]string) string {
		t.Helper()

		snap, err := Load(writeRoot(t, files), testRegistry(), discard())
		if err != nil {
			t.Fatalf("Load() err = %v", err)
		}

		return snap.Version()
	}

	with := func(k, v string) map[string]string {
		m := maps.Clone(base)
		m[k] = v

		return m
	}

	v := load(t, base)
	if !strings.HasPrefix(v, versionPrefix) || v != load(t, base) {
		t.Fatalf("Version() = %q, want a stable %s digest", v, versionPrefix)
	}

	tests := []struct {
		name  string
		files map[string]string
	}{
		{"an HCL edit", with("enterprise.hcl", minimalEnterprise+"\n# comment\n")},
		{"a root template shadowing the embedded one", with("templates/codeowners.tmpl", "* @{{ .Org }}/owners\n")},
		{"an unreferenced template", with("templates/other.tmpl", "x\n")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := load(t, tt.files); got == v {
				t.Errorf("Version() after %s = %q, unchanged", tt.name, got)
			}
		})
	}
}

func TestSnapshot_Accessors(t *testing.T) {
	t.Parallel()

	snap := mustLoad(t, map[string]string{"enterprise.hcl": ent(`"codeowners@1"`)})
	id := control.ID{Slug: "codeowners", Version: 2}

	if c := snap.Control(id); c == nil || c.ID() != id {
		t.Errorf("Control(%s) = %v", id, c)
	}

	if d, ok := snap.Definition(id); !ok || d.ID != id || snap.Revision(id) == "" {
		t.Errorf("Definition(%s) = %+v, %v; revision %q", id, d, ok, snap.Revision(id))
	}

	missing := control.ID{Slug: "nope", Version: 1}
	if _, ok := snap.Definition(missing); ok || snap.Control(missing) != nil || snap.Revision(missing) != "" {
		t.Errorf("accessors(%s) found an unknown control", missing)
	}

	if got := len(snap.Controls()); got != 7 || snap.Controls()[0].String() != "branch@1" {
		t.Errorf("Controls() = %v, want the 7 test definitions sorted", snap.Controls())
	}

	if got, want := snap.WatchedPaths(), []string{"CODEOWNERS", "catalog-info.yaml", "renovate.json"}; !slices.Equal(got, want) {
		t.Errorf("WatchedPaths() = %v, want %v", got, want)
	}
}

func TestSnapshot_Summarize(t *testing.T) {
	t.Parallel()

	snap := mustLoad(t, map[string]string{
		"enterprise.hcl": ent(`"codeowners@1", "deps@1"`),
		"orgs/acme.hcl": org(`  mode = "remediate"
  exclude "deps@1" {
    reason = "uses dependabot"
  }
  exclude_repos {
    match  = ["sandbox-*"]
    reason = "scratch"
  }
  repos "web" {
    match = ["web-*"]
    replace "codeowners@1" {
      with   = "codeowners@2"
      reason = "stricter"
    }
  }
`),
	})

	sum := snap.Summarize()
	if sum.Version != snap.Version() || sum.Enterprise.Mode != ModeEvaluate || len(sum.Enterprise.Controls) != 2 || len(sum.Controls) != 7 {
		t.Errorf("Summarize() = %+v", sum)
	}

	want := OrgSummary{
		Org:  "acme",
		Mode: ModeRemediate,
		Replaced: []ReplacedSummary{
			{Control: control.ID{Slug: "codeowners", Version: 1}, With: control.ID{Slug: "codeowners", Version: 2}, Reason: "stricter", Block: "web"},
		},
		Excluded:      []ExcludedSummary{{Control: control.ID{Slug: "deps", Version: 1}, Reason: "uses dependabot"}},
		ExcludedRepos: []ExcludedReposSummary{{Match: []string{"sandbox-*"}, Reason: "scratch"}},
		Repos:         []ReposSummary{{Label: "web", Match: []string{"web-*"}}},
	}

	if !reflect.DeepEqual(sum.Orgs, []OrgSummary{want}) {
		t.Errorf("Summarize().Orgs = %+v, want [%+v]", sum.Orgs, want)
	}
}
