package controltest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/template"
)

// linesModel is a file as lines.
type linesModel []string

// linesFile is the FileType of linesType: "has" appends a missing line,
// "header" prepends one. A line "!!" fails to parse.
type linesFile struct{}

func (linesFile) Parse(content []byte) (linesModel, error) {
	lines := strings.Split(string(content), "\n")
	if i := slices.Index(lines, "!!"); i >= 0 {
		return nil, &control.ParseError{Line: i + 1, Msg: "bang"}
	}

	return lines, nil
}

func (linesFile) Check(m linesModel, rule control.Rule) control.RuleResult { //nolint:gocritic // FileType signature
	want, _ := rule.Params["line"].(string)
	ok := slices.Contains(m, want)

	if rule.Kind == "header" {
		ok = len(m) > 0 && m[0] == want
	}

	res := control.RuleResult{Status: control.Fail, Evidence: map[string]any{control.EvidenceKind: "lines", control.EvidenceVersion: 1}}
	if ok {
		res.Status = control.Pass
	}

	return res
}

func (linesFile) Fix(m linesModel, failing []control.Rule) (linesModel, []control.RuleID, error) {
	out := slices.Clone(m)

	fixed := make([]control.RuleID, 0, len(failing))

	for _, r := range failing {
		line, _ := r.Params["line"].(string)

		if r.Kind == "header" {
			out = append(linesModel{line}, out...)
		} else {
			out = append(out, line)
		}

		fixed = append(fixed, r.ID)
	}

	return out, fixed, nil
}

func (linesFile) Render(m linesModel) ([]byte, error) { return []byte(strings.Join(m, "\n")), nil }

type linesType struct{}

func (linesType) Name() string   { return "lines" }
func (linesType) Semantics() int { return 1 }

func (linesType) RuleKinds() []control.RuleKindSpec {
	line := []control.ParamSpec{{Name: "line", Type: "string", Required: true}}

	return []control.RuleKindSpec{{Kind: "has", Params: line, Remediable: true}, {Kind: "header", Params: line, Remediable: true}}
}

func (linesType) Build(def control.Definition, env control.BuildEnv) (control.Control, error) { //nolint:gocritic // Type.Build signature
	body, _ := env.Templates.Lookup(def.Template)

	tmpl, err := env.Renderer.Parse(def.Template, body)
	if err != nil {
		return nil, err
	}

	return control.NewFileControl(control.FileControlConfig[linesModel]{
		Def: def, Spec: control.FileSpec{Locations: []string{"L"}, Canonical: "L"},
		Type: linesFile{}, MaxBytes: 1 << 10, Template: tmpl,
	})
}

type templates map[string]string

func (m templates) Lookup(name string) (string, bool) {
	b, ok := m[name]

	return b, ok
}

func TestRun_LinesType(t *testing.T) {
	def := control.Definition{
		ID:       control.ID{Slug: "lines", Version: 1},
		Template: "t",
		Rules: []control.Rule{
			{ID: "head", Kind: "header", Params: map[string]any{"line": "# {{ .Org }}"}, Remediate: true},
			{ID: "body", Kind: "has", Params: map[string]any{"line": "x"}, Remediate: true},
		},
	}
	def.Rules[0].Params["line"] = "# example-org"

	env := control.BuildEnv{Templates: templates{"t": "# {{ .Org }}\nx"}, Renderer: template.NewRenderer()}

	Run(t, linesType{}, []Fixture{
		{Name: "no file", Def: def, Env: env, Want: map[control.RuleID]control.RuleStatus{"head": control.Fail, "body": control.Fail}},
		{
			Name: "both failing", Def: def, Env: env, Repo: &FakeRepo{Files: map[string]string{"L": "keep"}},
			WantFiles: map[string]string{"L": "# example-org\nkeep\nx"},
		},
		{Name: "passing", Def: def, Env: env, Repo: &FakeRepo{Files: map[string]string{"L": "# example-org\nx"}}},
		{
			Name: "unparseable is a recommendation", Def: def, Env: env, Repo: &FakeRepo{Files: map[string]string{"L": "!!"}},
			Want: map[control.RuleID]control.RuleStatus{"head": control.Unknown, "body": control.Unknown},
		},
	})
}

func TestApply_FilesAndAPI(t *testing.T) {
	t.Parallel()

	repo := &FakeRepo{
		Files:  map[string]string{"a": "1", "gone": "x"},
		Labels: []control.Label{{Name: "Bug", Color: "red"}},
	}

	got, err := Apply(repo, control.ChangeSet{
		Files: []control.FileChange{{Path: "b", Content: []byte("2")}, {Path: "gone", Delete: true}},
		API: []control.APIChange{
			{Resource: control.MustCanonical(control.KindSetting, "has_wiki"), Op: control.OpSet, Value: false},
			{Resource: control.MustCanonical(control.KindProperty, "Owner"), Op: control.OpSet, Value: "team"},
			{Resource: control.MustCanonical(control.KindLabel, "bug"), Op: control.OpSet, Value: control.Label{Name: "bug", Color: "blue"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := repo.Files["b"]; ok || repo.Settings.HasWiki != nil || repo.Labels[0].Color != "red" {
		t.Error("Apply() modified its input")
	}

	if got.Files["b"] != "2" || got.Files["a"] != "1" {
		t.Errorf("Files = %v, want a and b", got.Files)
	}

	if _, ok := got.Files["gone"]; ok {
		t.Error("deleted file still present")
	}

	if got.Settings.HasWiki == nil || *got.Settings.HasWiki {
		t.Errorf("has_wiki = %v, want false", got.Settings.HasWiki)
	}

	if p := got.Properties["Owner"]; p == nil || *p != "team" {
		t.Errorf("property Owner = %v, want team", p)
	}

	if len(got.Labels) != 1 || got.Labels[0].Color != "blue" {
		t.Errorf("Labels = %v, want bug replaced", got.Labels)
	}
}

func TestFakeRepo_Reads(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	repo := &FakeRepo{
		Files:      map[string]string{"a/b": "1", "a/c/d": "2", "e": "3"},
		NotRegular: []string{"link"},
		Errors:     map[string]error{"file:bad": boom, ErrKeyLabels: boom},
	}
	ctx := context.Background()

	if names, _ := repo.ListDirectory(ctx, "a"); !slices.Equal(names, []string{"b", "c"}) {
		t.Errorf("ListDirectory(a) = %v, want [b c]", names)
	}

	if names, _ := repo.ListDirectory(ctx, ""); !slices.Equal(names, []string{"a", "e"}) {
		t.Errorf("ListDirectory(root) = %v, want [a e]", names)
	}

	if _, sha, found, _ := repo.GetContents(ctx, "e"); !found || sha != BlobSHA("3") {
		t.Errorf("GetContents(e) = %q, %v; want found with BlobSHA", sha, found)
	}

	if _, _, _, err := repo.GetContents(ctx, "link"); !errors.Is(err, control.ErrNotRegularFile) {
		t.Errorf("GetContents(link) err = %v, want ErrNotRegularFile", err)
	}

	if _, _, _, err := repo.GetContents(ctx, "bad"); !errors.Is(err, boom) {
		t.Errorf("GetContents(bad) err = %v, want boom", err)
	}

	if _, err := repo.ListLabels(ctx); !errors.Is(err, boom) {
		t.Errorf("ListLabels() err = %v, want boom", err)
	}
}
