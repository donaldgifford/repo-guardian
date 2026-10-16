package policy

import (
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

const codeownersCatalogue = `control "codeowners" {
  version     = 1
  title       = "CODEOWNERS"
  description = "A CODEOWNERS file exists"
  type        = "testfile"
  template    = "codeowners"
  paths       = [".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"]

  rule "exists" {
    number    = "1.1"
    title     = "A CODEOWNERS file exists"
    remediate = true
  }

  rule "has-team" {
    kind = "contains"
    text = "@"
  }

  remediation {
    apply = "recommend"
  }

  pr {
    title = "chore: {{ .Control.Title }}"
  }
}
`

func TestCatalogue_Decode(t *testing.T) {
	t.Parallel()

	snap, err := Load(writeRoot(t, map[string]string{
		"enterprise.hcl":           minimalEnterprise,
		"catalogue/codeowners.hcl": codeownersCatalogue,
	}), testRegistry(), discard())
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	d := snap.defs[control.ID{Slug: "codeowners", Version: 1}]
	if d == nil {
		t.Fatalf("codeowners@1 not decoded; have %v", snap.defs)
	}

	def := d.def
	if def.Title != "CODEOWNERS" || def.Description != "A CODEOWNERS file exists" || def.Type != "testfile" ||
		def.Template != "codeowners" || def.Apply != "recommend" || def.PR.Title == nil || def.PR.CompiledTitle == nil {
		t.Errorf("definition = %+v", def)
	}

	if len(def.Rules) != 2 || def.Rules[0].Kind != "exists" || def.Rules[1].Kind != "contains" || def.Rules[1].Params["text"] != "@" {
		t.Errorf("rules = %+v, want exists (kind from id) and has-team (contains, text @)", def.Rules)
	}

	if paths, _ := def.Params["paths"].([]any); len(paths) != 3 {
		t.Errorf("params = %v, want the three paths", def.Params)
	}

	if d.revision == "" || len(d.ctl.Owns()) != 3 {
		t.Errorf("revision = %q, owns = %v", d.revision, d.ctl.Owns())
	}

	if want := []string{"codeowners"}; len(snap.embeddedUsed) != 1 || snap.embeddedUsed[0] != want[0] {
		t.Errorf("embeddedUsed = %v, want %v (no root template, so the embedded fallback)", snap.embeddedUsed, want)
	}
}

func TestCatalogue_RootTemplateShadowsEmbedded(t *testing.T) {
	t.Parallel()

	snap, err := Load(writeRoot(t, map[string]string{
		"enterprise.hcl":            minimalEnterprise,
		"catalogue/codeowners.hcl":  codeownersCatalogue,
		"templates/codeowners.tmpl": "* @{{ .Org }}/owners\n",
	}), testRegistry(), discard())
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	if len(snap.embeddedUsed) != 0 {
		t.Errorf("embeddedUsed = %v, want none (the root shadows codeowners)", snap.embeddedUsed)
	}
}

func TestCatalogue_Errors(t *testing.T) {
	t.Parallel()

	block := func(body string) string {
		return "control \"x\" {\n  version = 1\n  type = \"testfile\"\n  paths = [\"X\"]\n" + body + "}\n"
	}

	tests := []struct {
		name      string
		catalogue string
		want      string
	}{
		{
			"duplicate",
			block("  rule \"exists\" {}\n") + block("  rule \"exists\" {}\n"),
			"catalogue/c.hcl:7: control x@1 is defined twice (first at catalogue/c.hcl:1)",
		},
		{"bad slug", "control \"Bad\" {\n  version = 1\n  type = \"testfile\"\n}\n", `catalogue/c.hcl:1: control slug "Bad" must match`},
		{"bad rule id", block("  rule \"Bad_\" {}\n"), `catalogue/c.hcl:5: x@1: rule id "Bad_" must match`},
		{
			"rule without kind",
			block("  rule \"present\" {}\n"),
			"catalogue/c.hcl:5: x@1/present: a rule without kind needs an id that is a rule kind of its type",
		},
		{"unknown type", "control \"x\" {\n  version = 1\n  type = \"nope\"\n}\n", `catalogue/c.hcl:3: control x@1: unknown control type "nope"`},
		{
			"rejected param",
			block("  rule \"has\" {\n    kind = \"contains\"\n    text = 3\n  }\n"),
			`catalogue/c.hcl:1: x@1/has: parameter "text" must be string`,
		},
		{"missing param", block("  rule \"has\" {\n    kind = \"contains\"\n  }\n"), `x@1/has: missing required parameter "text"`},
		{"no rules", block(""), "catalogue/c.hcl:1: x@1: a definition needs at least one rule"},
		{
			"version zero",
			"control \"x\" {\n  version = 0\n  type = \"testfile\"\n}\n",
			"catalogue/c.hcl:2: control x: version must be a positive integer",
		},
		{"unknown block", block("  rule \"exists\" {}\n  extra {}\n"), "catalogue/c.hcl:6"},
		{"missing template", block("  template = \"nope\"\n  rule \"exists\" {\n    remediate = true\n  }\n"), `template "nope" not found`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Load(
				writeRoot(t, map[string]string{"enterprise.hcl": minimalEnterprise, "catalogue/c.hcl": tt.catalogue}),
				testRegistry(),
				discard(),
			)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load() err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}
