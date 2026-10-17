package control

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/template"
)

// lineType is a FileType over lines of text. Rule kind "has" passes when
// a line equals params["line"]; Fix appends missing lines unless the
// rule's params carry "manual". A line "!!" is a parse error.
type lineType struct{}

func (lineType) Parse(content []byte) ([]string, error) {
	lines := strings.Split(string(content), "\n")
	for i, l := range lines {
		if l == "!!" {
			return nil, &ParseError{Line: i + 1, Msg: "bang"}
		}
	}

	return lines, nil
}

func (lineType) Check(m []string, rule Rule) RuleResult { //nolint:gocritic // hugeParam: FileType's signature
	want, _ := rule.Params["line"].(string)
	if slices.Contains(m, want) {
		return RuleResult{Status: Pass, Evidence: ev()}
	}

	return RuleResult{Status: Fail, Evidence: ev()}
}

func (lineType) Fix(m []string, failing []Rule) ([]string, []RuleID, error) {
	var fixed []RuleID

	for _, r := range failing {
		if r.Params["manual"] != nil {
			continue
		}

		m = append(slices.Clone(m), r.Params["line"].(string))
		fixed = append(fixed, r.ID)
	}

	return m, fixed, nil
}

func (lineType) Render(m []string) ([]byte, error) {
	return []byte(strings.Join(m, "\n")), nil
}

var testSpec = FileSpec{Locations: []string{".github/X", "X", "docs/X"}, Canonical: ".github/X"}

func newLineControl(t *testing.T, rules ...Rule) Control {
	t.Helper()

	tmpl, err := template.NewRenderer().Parse("x", "owner {{ .Org }}\na")
	if err != nil {
		t.Fatal(err)
	}

	c, err := NewFileControl(FileControlConfig[[]string]{
		Def:      Definition{ID: ID{Slug: "x", Version: 1}, Rules: rules},
		Spec:     testSpec,
		Type:     lineType{},
		MaxBytes: 64,
		Template: tmpl,
	})
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func has(id RuleID, line string, remediate bool) Rule {
	return Rule{ID: id, Kind: "has", Params: map[string]any{"line": line}, Remediate: remediate}
}

// run evaluates and remediates c against files, checking both outputs
// with Validate and ValidateChangeSet.
func run(t *testing.T, c Control, base *countingReader) (Evaluation, ChangeSet) {
	t.Helper()

	r := newEval(base)
	in := EvalInput{Repo: RepoContext{Vars: template.Vars{Org: "acme"}}, Reader: r}

	got, err := c.Evaluate(t.Context(), in)
	if err != nil {
		t.Fatalf("Evaluate() err = %v", err)
	}

	got.Reads = r.Reads()

	got, err = Validate(c, got)
	if err != nil {
		t.Fatalf("Validate() err = %v", err)
	}

	cs, err := c.Remediate(t.Context(), RemediationInput{EvalInput: in, Evaluation: got})
	if err != nil {
		t.Fatalf("Remediate() err = %v", err)
	}

	if err := ValidateChangeSet(c, got, cs); err != nil {
		t.Fatalf("ValidateChangeSet() err = %v", err)
	}

	return got, cs
}

func TestLocateFile_PrecedenceAndEveryLocationRead(t *testing.T) {
	t.Parallel()

	r := newEval(&countingReader{files: map[string]string{"X": "root", "docs/X": "docs"}})

	path, content, found, err := LocateFile(t.Context(), r, testSpec)
	if err != nil || !found || path != "X" || string(content) != "root" {
		t.Fatalf("LocateFile() = %q, %q, %v, %v; want X", path, content, found, err)
	}

	reads := r.Reads()
	states := make([]string, 0, len(reads))

	for _, rr := range reads {
		states = append(states, rr.Resource.Key+"="+string(rr.State))
	}

	want := []string{".github/X=absent", "X=present", "docs/X=present"}
	if !slices.Equal(states, want) {
		t.Errorf("reads = %v, want %v", states, want)
	}
}

func TestLocateFile_ErrorAboveFoundIsReturned(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")

	r := newEval(&countingReader{files: map[string]string{"X": "root"}, fileErr: map[string]error{".github/X": boom}})
	if _, _, _, err := LocateFile(t.Context(), r, testSpec); !errors.Is(err, boom) {
		t.Errorf("LocateFile() err = %v, want boom", err)
	}

	r = newEval(&countingReader{files: map[string]string{"X": "root"}, fileErr: map[string]error{"docs/X": boom}})
	if path, _, found, err := LocateFile(t.Context(), r, testSpec); err != nil || !found || path != "X" {
		t.Errorf("LocateFile() = %q, %v, %v; want X (an error below the found file is ignored)", path, found, err)
	}
}

func TestFileControl_Absent_TemplateAtCanonical(t *testing.T) {
	t.Parallel()

	c := newLineControl(t, has("a", "a", true), has("b", "b", false))

	got, cs := run(t, c, &countingReader{})

	for _, res := range got.Results {
		if res.Status != Fail || res.Evidence["reason"] != ReasonFileMissing {
			t.Errorf("result %s = %s %v, want fail file_missing", res.RuleID, res.Status, res.Evidence)
		}
	}

	if len(cs.Files) != 1 || cs.Files[0].Path != ".github/X" || string(cs.Files[0].Content) != "owner acme\na" {
		t.Errorf("Files = %+v, want the rendered template at .github/X", cs.Files)
	}

	if !slices.Equal(cs.Fixed, []RuleID{"a"}) || !slices.Equal(cs.Manual, []RuleID{"b"}) || len(cs.Notes) != 1 {
		t.Errorf("Fixed = %v, Manual = %v, Notes = %v; want [a], [b], one note", cs.Fixed, cs.Manual, cs.Notes)
	}
}

func TestFileControl_Present_EditedWhereFound(t *testing.T) {
	t.Parallel()

	manual := has("m", "m", true)
	manual.Params["manual"] = true

	c := newLineControl(t, has("a", "a", true), has("z", "z", true), manual)

	got, cs := run(t, c, &countingReader{files: map[string]string{"docs/X": "z"}})

	statuses := map[RuleID]RuleStatus{}
	for _, res := range got.Results {
		statuses[res.RuleID] = res.Status
	}

	if statuses["a"] != Fail || statuses["z"] != Pass || statuses["m"] != Fail {
		t.Errorf("statuses = %v, want a fail, z pass, m fail", statuses)
	}

	if len(cs.Files) != 1 || cs.Files[0].Path != "docs/X" || string(cs.Files[0].Content) != "z\na" {
		t.Errorf("Files = %+v, want docs/X edited in place", cs.Files)
	}

	if !slices.Equal(cs.Fixed, []RuleID{"a"}) || !slices.Equal(cs.Manual, []RuleID{"m"}) {
		t.Errorf("Fixed = %v, Manual = %v; want [a], [m]", cs.Fixed, cs.Manual)
	}
}

func TestFileControl_Passing_EmptyChangeSet(t *testing.T) {
	t.Parallel()

	_, cs := run(t, newLineControl(t, has("a", "a", true)), &countingReader{files: map[string]string{"X": "a"}})

	if !cs.IsRecommendation() || len(cs.Fixed)+len(cs.Manual)+len(cs.Blocked) != 0 {
		t.Errorf("change set = %+v, want empty", cs)
	}
}

func TestFileControl_Unjudgeable_NeverWritten(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		base   *countingReader
		status RuleStatus
		reason string
	}{
		{"parse error", &countingReader{files: map[string]string{"X": "ok\n!!"}}, Unknown, ReasonParseError},
		{"too large", &countingReader{files: map[string]string{"X": strings.Repeat("a", 65)}}, Unknown, ReasonFileTooLarge},
		{"permission", &countingReader{fileErr: map[string]error{".github/X": fmt.Errorf("403: %w", ErrPermission)}}, Unknown, ReasonPermission},
		{"symlink", &countingReader{fileErr: map[string]error{".github/X": ErrNotRegularFile}}, Unknown, ReasonNotRegularFile},
		{"transient", &countingReader{fileErr: map[string]error{".github/X": errors.New("502")}}, Error, ReasonReadError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, cs := run(t, newLineControl(t, has("a", "a", true), has("b", "b", true)), tt.base)

			for _, res := range got.Results {
				if res.Status != tt.status || res.Evidence["reason"] != tt.reason {
					t.Errorf("result %s = %s %v, want %s %s", res.RuleID, res.Status, res.Evidence, tt.status, tt.reason)
				}
			}

			if len(cs.Files) != 0 || !slices.Equal(cs.Blocked, []RuleID{"a", "b"}) || len(cs.Notes) != 2 {
				t.Errorf("change set = %+v, want no files and every rule Blocked with a note", cs)
			}
		})
	}
}

func TestFileControl_ParseErrorNamesLine(t *testing.T) {
	t.Parallel()

	got, _ := run(t, newLineControl(t, has("a", "a", true)), &countingReader{files: map[string]string{"X": "ok\n!!"}})

	if line := got.Results[0].Evidence["line"]; line != 2 {
		t.Errorf("evidence line = %v, want 2", line)
	}
}

func TestNewFileControl_Refuses(t *testing.T) {
	t.Parallel()

	def := Definition{ID: ID{Slug: "x", Version: 1}, Rules: []Rule{has("a", "a", true)}}
	tmpl, _ := template.NewRenderer().Parse("x", "a")

	tests := []struct {
		name string
		cfg  FileControlConfig[[]string]
		want string
	}{
		{"no size cap", FileControlConfig[[]string]{Def: def, Spec: testSpec, Type: lineType{}, Template: tmpl}, "size cap"},
		{"no locations", FileControlConfig[[]string]{Def: def, Type: lineType{}, MaxBytes: 1, Template: tmpl}, "location"},
		{"non-canonical", FileControlConfig[[]string]{
			Def: def, Spec: FileSpec{Locations: []string{"a/../b"}, Canonical: "a/../b"}, Type: lineType{}, MaxBytes: 1, Template: tmpl,
		}, ".."},
		{"canonical not listed", FileControlConfig[[]string]{
			Def: def, Spec: FileSpec{Locations: []string{"X"}, Canonical: "Y"}, Type: lineType{}, MaxBytes: 1, Template: tmpl,
		}, "not a location"},
		{"no template", FileControlConfig[[]string]{Def: def, Spec: testSpec, Type: lineType{}, MaxBytes: 1}, "template"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := NewFileControl(tt.cfg); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("NewFileControl() err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}
