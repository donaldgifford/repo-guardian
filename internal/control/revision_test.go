package control

import (
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/template"
)

func baseDef() Definition {
	title := "Add CODEOWNERS"

	return Definition{
		ID:    ID{Slug: "codeowners", Version: 2},
		Title: "CODEOWNERS exists",
		Type:  "codeowners",
		Rules: []Rule{
			{ID: "exists", Number: "1.1", Title: "A CODEOWNERS file exists", Kind: "exists", Remediate: true, Params: map[string]any{"x": "y"}},
		},
		Template: "codeowners",
		PR:       template.PRConfig{Title: &title},
		Apply:    "recommend",
		Params:   map[string]any{"path": ".github/CODEOWNERS"},
	}
}

func revisionOf(t *testing.T, typ *stubType, def Definition, tpl templates) string { //nolint:gocritic // test helper mirrors Revision
	t.Helper()

	r, err := Revision(typ, def, BuildEnv{Templates: tpl})
	if err != nil {
		t.Fatalf("Revision() = %v", err)
	}

	return r
}

func TestRevision(t *testing.T) {
	t.Parallel()

	tpl := templates{"codeowners": "* @{{ .Org }}/owners\n", "extra": "a"}
	typ := &stubType{name: "codeowners", semantics: 1}
	base := revisionOf(t, typ, baseDef(), tpl)

	same := map[string]func(*Definition){
		"definition title": func(d *Definition) { d.Title = "Something else" },
		"rule title":       func(d *Definition) { d.Rules[0].Title = "Other" },
		"rule number":      func(d *Definition) { d.Rules[0].Number = "9.9" },
		"pr block":         func(d *Definition) { body := "body"; d.PR.Body = &body; d.PR.Labels = []string{"x"} },
	}

	for name, edit := range same {
		t.Run("unchanged by "+name, func(t *testing.T) {
			t.Parallel()

			d := baseDef()
			edit(&d)

			if got := revisionOf(t, typ, d, tpl); got != base {
				t.Errorf("a %s edit moved the revision", name)
			}
		})
	}

	moved := map[string]func(*Definition){
		"rule parameter": func(d *Definition) { d.Rules[0].Params = map[string]any{"x": "z"} },
		"rule kind":      func(d *Definition) { d.Rules[0].Kind = "other" },
		"remediate":      func(d *Definition) { d.Rules[0].Remediate = false },
		"apply":          func(d *Definition) { d.Apply = "direct" },
		"type parameter": func(d *Definition) { d.Params = map[string]any{"path": "CODEOWNERS"} },
		"version":        func(d *Definition) { d.ID.Version = 3 },
		"template name":  func(d *Definition) { d.Template = "extra" },
	}

	for name, edit := range moved {
		t.Run("moved by "+name, func(t *testing.T) {
			t.Parallel()

			d := baseDef()
			edit(&d)

			if got := revisionOf(t, typ, d, tpl); got == base {
				t.Errorf("a %s change left the revision unchanged", name)
			}
		})
	}

	t.Run("moved by a template byte", func(t *testing.T) {
		t.Parallel()

		if got := revisionOf(t, typ, baseDef(), templates{"codeowners": "* @{{ .Org }}/owners \n"}); got == base {
			t.Error("a template byte change left the revision unchanged")
		}
	})

	t.Run("moved by Semantics", func(t *testing.T) {
		t.Parallel()

		if got := revisionOf(t, &stubType{name: "codeowners", semantics: 2}, baseDef(), tpl); got == base {
			t.Error("a Semantics bump left the revision unchanged")
		}
	})

	t.Run("moved by a referenced file", func(t *testing.T) {
		t.Parallel()

		ref := &stubType{name: "codeowners", semantics: 1, files: []string{"extra"}}
		a := revisionOf(t, ref, baseDef(), tpl)
		b := revisionOf(t, ref, baseDef(), templates{"codeowners": tpl["codeowners"], "extra": "b"})

		if a == b {
			t.Error("a referenced file's bytes changed but the revision did not")
		}
	})

	t.Run("missing template", func(t *testing.T) {
		t.Parallel()

		if _, err := Revision(typ, baseDef(), BuildEnv{Templates: templates{}}); err == nil {
			t.Error("Revision() with a missing template = nil error")
		}
	})
}

func TestFingerprint(t *testing.T) {
	t.Parallel()

	setting := MustCanonical(KindSetting, "delete_branch_on_merge")
	base := Evaluation{
		Results: []RuleResult{result("a", Fail), result("b", Pass)},
		Reads:   []ResourceRead{{Resource: setting, State: Present, Digest: "d1", ObservedAt: time.Unix(1, 0)}},
	}
	fp := Fingerprint("rev", base)

	t.Run("an API value between two wrong values", func(t *testing.T) {
		t.Parallel()

		e := base
		e.Reads = []ResourceRead{{Resource: setting, State: Present, Digest: "d2"}}

		if Fingerprint("rev", e) == fp {
			t.Error("the read digest moved with statuses unchanged, but the fingerprint did not")
		}
	})

	t.Run("an absent property and a present null", func(t *testing.T) {
		t.Parallel()

		prop := MustCanonical(KindProperty, "Owner")
		absent := Evaluation{Results: base.Results, Reads: []ResourceRead{{Resource: prop, State: Absent}}}
		null := Evaluation{
			Results: base.Results,
			Reads:   []ResourceRead{{Resource: prop, State: Present, Digest: "74234e98afe7498fb5daf1f36ac2d78acc339464f950703b8c019892f982b90b"}},
		}

		if Fingerprint("rev", absent) == Fingerprint("rev", null) {
			t.Error("an absent property and a present null fingerprint alike")
		}
	})

	t.Run("evidence, ObservedAt and order do not move it", func(t *testing.T) {
		t.Parallel()

		e := Evaluation{
			Results: []RuleResult{
				{RuleID: "b", Status: Pass, Evidence: map[string]any{"msg": "different wording"}},
				{RuleID: "a", Status: Fail},
			},
			Reads: []ResourceRead{{Resource: setting, State: Present, Digest: "d1", ObservedAt: time.Unix(999, 0), Version: "etag"}},
		}

		if Fingerprint("rev", e) != fp {
			t.Error("evidence wording, ObservedAt, Version or result order moved the fingerprint")
		}
	})

	t.Run("a status or the revision moves it", func(t *testing.T) {
		t.Parallel()

		e := base
		e.Results = []RuleResult{result("a", Pass), result("b", Pass)}

		if Fingerprint("rev", e) == fp {
			t.Error("a status change left the fingerprint unchanged")
		}

		if Fingerprint("rev2", base) == fp {
			t.Error("a revision change left the fingerprint unchanged")
		}
	})
}
