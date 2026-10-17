package policy

import (
	"slices"
	"strings"
	"testing"
)

// ent is an enterprise policy over the test catalogue.
func ent(controls string) string {
	return "enterprise {\n  orgs     = [\"acme\"]\n  controls = [" + controls + "]\n}\n"
}

// org is an org policy for acme with body.
func org(body string) string { return "org \"acme\" {\n" + body + "}\n" }

func TestValidate_Errors(t *testing.T) {
	t.Parallel()

	base := ent(`"codeowners@1", "deps@1"`)

	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			"unknown control",
			map[string]string{"enterprise.hcl": ent(`"nope@1"`)},
			`enterprise.hcl:3: unknown control nope@1: no "nope" in the catalogue`,
		},
		{
			"unknown version",
			map[string]string{"enterprise.hcl": ent(`"codeowners@9"`)},
			"enterprise.hcl:3: unknown control version codeowners@9: the catalogue has codeowners@1, codeowners@2",
		},
		{
			"rejected parameter",
			map[string]string{
				"enterprise.hcl":    base,
				"catalogue/bad.hcl": "control \"bad\" {\n  version = 1\n  type = \"testsetting\"\n  rule \"x\" {\n    kind = \"equals\"\n    property = 1\n    value = true\n  }\n}\n",
			},
			`catalogue/bad.hcl:1: bad@1/x: parameter "property" must be string`,
		},
		{
			"rule without kind",
			map[string]string{
				"enterprise.hcl":    base,
				"catalogue/bad.hcl": "control \"bad\" {\n  version = 1\n  type = \"testsetting\"\n  rule \"x\" {}\n}\n",
			},
			"catalogue/bad.hcl:4: bad@1/x: a rule without kind needs an id that is a rule kind of its type",
		},
		{
			"org not onboarded",
			map[string]string{"enterprise.hcl": base, "orgs/x.hcl": "org \"stray\" {}\n"},
			`orgs/x.hcl:1: org "stray" is not in enterprise.orgs`,
		},
		{
			"replace source missing",
			map[string]string{
				"enterprise.hcl": base,
				"orgs/acme.hcl":  org("  replace \"legacy_codeowners@1\" {\n    with = \"codeowners@2\"\n    reason = \"r\"\n  }\n"),
			},
			"orgs/acme.hcl:2: replace legacy_codeowners@1: the source is not assigned at this layer",
		},
		{
			"replace shares no resource",
			map[string]string{
				"enterprise.hcl": base,
				"orgs/acme.hcl":  org("  replace \"codeowners@1\" {\n    with = \"deps@1\"\n    reason = \"r\"\n  }\n"),
			},
			"orgs/acme.hcl:2: replace codeowners@1 with deps@1: the two controls own no resource in common",
		},
		{
			"exclude without reason",
			map[string]string{"enterprise.hcl": base, "orgs/acme.hcl": org("  exclude \"deps@1\" {}\n")},
			"orgs/acme.hcl:2: exclude deps@1 needs a reason",
		},
		{
			"replace without reason",
			map[string]string{
				"enterprise.hcl": base,
				"orgs/acme.hcl":  org("  replace \"codeowners@1\" {\n    with = \"codeowners@2\"\n  }\n"),
			},
			"orgs/acme.hcl:2: replace codeowners@1 needs a reason",
		},
		{
			"exclude_repos without reason",
			map[string]string{"enterprise.hcl": base, "orgs/acme.hcl": org("  exclude_repos {\n    match = [\"x\"]\n  }\n")},
			"orgs/acme.hcl:2: exclude_repos needs a reason",
		},
		{
			"repos without label",
			map[string]string{"enterprise.hcl": base, "orgs/acme.hcl": org("  repos {\n    match = [\"x\"]\n  }\n")},
			"orgs/acme.hcl:2",
		},
		{
			"duplicate repos label",
			map[string]string{
				"enterprise.hcl": base,
				"orgs/acme.hcl":  org("  repos \"a\" {\n    match = [\"x\"]\n  }\n  repos \"a\" {\n    match = [\"y\"]\n  }\n"),
			},
			`orgs/acme.hcl:5: repos block "a" is declared twice in org acme (first at orgs/acme.hcl:2)`,
		},
		{
			"override of unassigned control",
			map[string]string{"enterprise.hcl": base, "orgs/acme.hcl": org("  control \"wiki@1\" {\n    mode = \"evaluate\"\n  }\n")},
			"orgs/acme.hcl:2: control wiki@1 override: no layer of org acme assigns wiki@1",
		},
		{
			"max_open_prs below 1",
			map[string]string{"enterprise.hcl": base, "orgs/acme.hcl": org("  remediation {\n    max_open_prs = 0\n  }\n")},
			"orgs/acme.hcl:3: remediation.max_open_prs = 0: must be at least 1",
		},
		{
			"reopen_after not a duration",
			map[string]string{"enterprise.hcl": base, "orgs/acme.hcl": org("  remediation {\n    reopen_after = \"soon\"\n  }\n")},
			`orgs/acme.hcl:3: remediation.reopen_after = "soon": must be a duration of at least 1h0m0s`,
		},
		{
			"reopen_after below 1h",
			map[string]string{"enterprise.hcl": base, "orgs/acme.hcl": org("  remediation {\n    reopen_after = \"30m\"\n  }\n")},
			`orgs/acme.hcl:3: remediation.reopen_after = "30m": must be a duration of at least 1h0m0s`,
		},
		{
			"duplicate org policy",
			map[string]string{"enterprise.hcl": base, "orgs/a.hcl": org(""), "orgs/b.hcl": org("")},
			`orgs/b.hcl:1: a second policy for org "acme" (first at orgs/a.hcl:1)`,
		},
		{
			"duplicate control version",
			map[string]string{
				"enterprise.hcl":    base,
				"catalogue/dup.hcl": "control \"deps\" {\n  version = 1\n  type = \"testfile\"\n  paths = [\"x\"]\n  rule \"exists\" {}\n}\n",
			},
			"control deps@1 is defined twice",
		},
		{
			"referenced file missing",
			map[string]string{"enterprise.hcl": "enterprise {\n  orgs = file(\"orgs.txt\")\n}\n"},
			"orgs.txt: no such file under the policy root",
		},
		{
			"referenced file too large",
			map[string]string{"enterprise.hcl": base, "data/big": strings.Repeat("x", MaxFileBytes+1)},
			"data/big: 1048577 bytes, over the 1048576-byte limit",
		},
		{
			"bad mode",
			map[string]string{"enterprise.hcl": base, "orgs/acme.hcl": org("  mode = \"fix\"\n")},
			`orgs/acme.hcl:2: mode "fix": want "evaluate" or "remediate"`,
		},
		{
			"bad glob",
			map[string]string{"enterprise.hcl": base, "orgs/acme.hcl": org("  repos \"a\" {\n    match = [\"[\"]\n  }\n")},
			`orgs/acme.hcl:2: match pattern "["`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Load(writeRoot(t, withCatalogue(tt.files)), testRegistry(), discard())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load() err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestValidate_Warnings(t *testing.T) {
	t.Parallel()

	base := ent(`"codeowners@1", "deps@1", "catalog@1"`)

	tests := []struct {
		name string
		org  string
		want string
	}{
		{
			"exclusion of an unassigned control", org("  exclude \"wiki@1\" {\n    reason = \"r\"\n  }\n"),
			"orgs/acme.hcl:2: exclude wiki@1: not assigned at this layer, so the exclusion does nothing",
		},
		{"re-add after an overlapping exclusion", org(`  repos "a" {
    match = ["backstage-*"]
    exclude "catalog@1" {
      reason = "r"
    }
  }
  repos "b" {
    match    = ["backstage-api"]
    controls = ["catalog@1"]
  }
`), `orgs/acme.hcl:10: repos "b" re-adds catalog@1, which repos "a" excluded for an overlapping match; block order decides`},
		{"two modes at one specificity", org(`  repos "a" {
    match = ["x-*"]
    mode  = "remediate"
  }
  repos "b" {
    match = ["x-1"]
    mode  = "evaluate"
  }
`), `orgs/acme.hcl:8: repos "a" and "b" may match one repository with different modes; the later one wins`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			snap := mustLoad(t, map[string]string{"enterprise.hcl": base, "orgs/acme.hcl": tt.org})

			var got []string
			for _, w := range snap.Warnings() {
				got = append(got, w.String())
			}

			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("Warnings() = %q, want [%q]", got, tt.want)
			}
		})
	}
}

func TestUnmatchedReposBlocks(t *testing.T) {
	t.Parallel()

	snap := mustLoad(t, map[string]string{
		"enterprise.hcl": ent(`"codeowners@1"`),
		"orgs/acme.hcl":  org("  repos \"web\" {\n    match = [\"web-*\"]\n  }\n  repos \"ghost\" {\n    match = [\"nothing\"]\n  }\n"),
	})

	got := snap.UnmatchedReposBlocks("ACME", []string{"web-app", "api"})
	if len(got) != 1 || got[0].String() != `orgs/acme.hcl:5: repos "ghost" matches no known repository` {
		t.Errorf("UnmatchedReposBlocks() = %v, want only ghost", got)
	}
}

func TestValidate_OwnershipWarnings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			"baseline overlap for an org with no file",
			map[string]string{"enterprise.hcl": ent(`"codeowners@1", "legacy_codeowners@1"`)},
			[]string{
				"enterprise.hcl:2: codeowners@1 and legacy_codeowners@1 both own file:CODEOWNERS in org acme; " +
					"a repository assigned both is a resource conflict",
			},
		},
		{
			"org addition overlaps the baseline",
			map[string]string{
				"enterprise.hcl": ent(`"codeowners@1"`),
				"orgs/acme.hcl":  org("  controls = [\"legacy_codeowners@1\"]\n"),
			},
			[]string{
				"orgs/acme.hcl:1: codeowners@1 and legacy_codeowners@1 both own file:CODEOWNERS in org acme; " +
					"a repository assigned both is a resource conflict",
			},
		},
		{
			"two blocks together overlap",
			map[string]string{
				"enterprise.hcl": ent(`"deps@1"`),
				"orgs/acme.hcl": org(`  repos "a" {
    match    = ["x-*"]
    controls = ["codeowners@2"]
  }
  repos "b" {
    match    = ["y-*"]
    controls = ["legacy_codeowners@1"]
  }
`),
			},
			[]string{
				"orgs/acme.hcl:6: codeowners@2 and legacy_codeowners@1 both own file:CODEOWNERS in org acme; " +
					"a repository assigned both is a resource conflict",
			},
		},
		{
			"a replace removes its source",
			map[string]string{
				"enterprise.hcl": ent(`"legacy_codeowners@1"`),
				"orgs/acme.hcl":  org("  replace \"legacy_codeowners@1\" {\n    with   = \"codeowners@1\"\n    reason = \"r\"\n  }\n"),
			},
			nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			snap := mustLoad(t, tt.files)

			var got []string
			for _, w := range snap.Warnings() {
				got = append(got, w.String())
			}

			if !slices.Equal(got, tt.want) {
				t.Errorf("Warnings() = %q, want %q", got, tt.want)
			}
		})
	}
}
