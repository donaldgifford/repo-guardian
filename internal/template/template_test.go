package template_test

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/donaldgifford/repo-guardian/internal/template"
)

func TestRenderer_Parse_Valid(t *testing.T) {
	t.Parallel()

	r := template.NewRenderer()

	c, err := r.Parse("greet", "hello {{ .Owner }}")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	if c == nil {
		t.Fatal("expected non-nil Compiled on success")
	}

	if c.Name() != "greet" {
		t.Errorf("Name() = %q, want %q", c.Name(), "greet")
	}
}

func TestRenderer_Parse_Invalid(t *testing.T) {
	t.Parallel()

	r := template.NewRenderer()

	_, err := r.Parse("bad", "{{ .Owner ")
	if err == nil {
		t.Fatal("expected parse error for unterminated action")
	}

	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("error %q should include template name %q", err.Error(), "bad")
	}
}

func TestCompiled_Render_FileVars(t *testing.T) {
	t.Parallel()

	r := template.NewRenderer()

	c, err := r.Parse("file", "owner={{ .Owner }} repo={{ .Repo }} rule={{ .Rule.Name }}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	out, err := c.Render(template.FileVars{
		Common: template.Common{Owner: "octocat", Repo: "hello"},
		Rule:   template.Rule{Name: "codeowners", Target: ".github/CODEOWNERS"},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	want := "owner=octocat repo=hello rule=codeowners"
	if out != want {
		t.Errorf("render output:\ngot:  %q\nwant: %q", out, want)
	}
}

func TestCompiled_Render_PRVars(t *testing.T) {
	t.Parallel()

	r := template.NewRenderer()

	body := "{{ .Owner }}/{{ .Repo }} files: {{ join \", \" .Files }}"

	c, err := r.Parse("pr-body", body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	out, err := c.Render(template.PRVars{
		Common: template.Common{Owner: "octocat", Repo: "hello"},
		Files:  []string{".github/CODEOWNERS", ".github/dependabot.yml"},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	want := "octocat/hello files: .github/CODEOWNERS, .github/dependabot.yml"
	if out != want {
		t.Errorf("render output:\ngot:  %q\nwant: %q", out, want)
	}
}

func TestCompiled_Render_NilCatalogFails(t *testing.T) {
	t.Parallel()

	r := template.NewRenderer()

	c, err := r.Parse("catalog", "{{ .Catalog.Owner }}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	_, err = c.Render(template.FileVars{Common: template.Common{Owner: "x"}})
	if err == nil {
		t.Fatal("expected render error for nil .Catalog under missingkey=error")
	}
}

func TestCompiled_Render_GuardedNilCatalogSucceeds(t *testing.T) {
	t.Parallel()

	r := template.NewRenderer()

	c, err := r.Parse("guarded", "{{ if .Catalog }}{{ .Catalog.Owner }}{{ else }}none{{ end }}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	out, err := c.Render(template.FileVars{Common: template.Common{Owner: "x"}})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if out != "none" {
		t.Errorf("render output: got %q, want %q", out, "none")
	}
}

func TestCompiled_Render_PopulatedCatalog(t *testing.T) {
	t.Parallel()

	r := template.NewRenderer()

	c, err := r.Parse("catalog", "{{ .Catalog.Owner }}/{{ .Catalog.Component }}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	out, err := c.Render(template.FileVars{
		Catalog: &template.CatalogInfo{Owner: "platform", Component: "billing-svc"},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	want := "platform/billing-svc"
	if out != want {
		t.Errorf("render output: got %q, want %q", out, want)
	}
}

func TestCompiled_Render_SharedRendererBothContexts(t *testing.T) {
	t.Parallel()

	r := template.NewRenderer()

	fileTpl, err := r.Parse("file", "{{ .Repo }}")
	if err != nil {
		t.Fatalf("parse file: %v", err)
	}

	prTpl, err := r.Parse("pr", "{{ join \",\" .Files }}")
	if err != nil {
		t.Fatalf("parse pr: %v", err)
	}

	fileOut, err := fileTpl.Render(template.FileVars{Common: template.Common{Repo: "alpha"}})
	if err != nil {
		t.Fatalf("render file: %v", err)
	}

	if fileOut != "alpha" {
		t.Errorf("file render: got %q, want %q", fileOut, "alpha")
	}

	prOut, err := prTpl.Render(template.PRVars{Files: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("render pr: %v", err)
	}

	if prOut != "a,b" {
		t.Errorf("pr render: got %q, want %q", prOut, "a,b")
	}
}

func TestEnvHelper(t *testing.T) {
	// t.Parallel() omitted: subtests use t.Setenv which is incompatible
	// with t.Parallel() under Go 1.25+.
	r := template.NewRenderer()

	c, err := r.Parse("env", `{{ env "TEMPLATE_TEST_VAR" }}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	t.Run("unset returns empty", func(t *testing.T) {
		out, err := c.Render(template.FileVars{})
		if err != nil {
			t.Fatalf("render: %v", err)
		}

		if out != "" {
			t.Errorf("unset env render: got %q, want empty", out)
		}
	})

	t.Run("set returns value", func(t *testing.T) {
		t.Setenv("TEMPLATE_TEST_VAR", "hello")

		out, err := c.Render(template.FileVars{})
		if err != nil {
			t.Fatalf("render: %v", err)
		}

		if out != "hello" {
			t.Errorf("set env render: got %q, want %q", out, "hello")
		}
	})
}

func TestDefaultHelper(t *testing.T) {
	t.Parallel()

	r := template.NewRenderer()

	c, err := r.Parse("default", `{{ default "fallback" .Owner }}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	cases := []struct {
		name  string
		owner string
		want  string
	}{
		{"empty value uses fallback", "", "fallback"},
		{"non-empty value passes through", "octocat", "octocat"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := c.Render(template.FileVars{Common: template.Common{Owner: tc.owner}})
			if err != nil {
				t.Fatalf("render: %v", err)
			}

			if out != tc.want {
				t.Errorf("got %q, want %q", out, tc.want)
			}
		})
	}
}

func TestJoinHelper(t *testing.T) {
	t.Parallel()

	r := template.NewRenderer()

	c, err := r.Parse("join", `{{ join ", " .Files }}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	cases := []struct {
		name  string
		files []string
		want  string
	}{
		{"nil slice renders empty", nil, ""},
		{"empty slice renders empty", []string{}, ""},
		{"single element no separator", []string{"only"}, "only"},
		{"multiple elements joined", []string{"a", "b", "c"}, "a, b, c"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := c.Render(template.PRVars{Files: tc.files})
			if err != nil {
				t.Fatalf("render: %v", err)
			}

			if out != tc.want {
				t.Errorf("got %q, want %q", out, tc.want)
			}
		})
	}
}

func TestCaseHelpers(t *testing.T) {
	t.Parallel()

	r := template.NewRenderer()

	body := `{{ lower .Owner }}|{{ upper .Owner }}|{{ title .Owner }}`

	c, err := r.Parse("case", body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	cases := []struct {
		name  string
		owner string
		want  string
	}{
		{"already-capitalized stays", "OCTOcat", "octocat|OCTOCAT|OCTOcat"},
		{"lowercase first letter rises", "octocat", "octocat|OCTOCAT|Octocat"},
		{"multi-word title", "hello world", "hello world|HELLO WORLD|Hello World"},
		{"empty input", "", "||"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := c.Render(template.FileVars{Common: template.Common{Owner: tc.owner}})
			if err != nil {
				t.Fatalf("render: %v", err)
			}

			if out != tc.want {
				t.Errorf("got %q, want %q", out, tc.want)
			}
		})
	}
}

func TestValidateZero_FileVars(t *testing.T) {
	t.Parallel()

	r := template.NewRenderer()

	t.Run("safe template passes", func(t *testing.T) {
		t.Parallel()

		c, err := r.Parse("safe", "owner={{ .Owner }} repo={{ .Repo }}")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}

		if err := template.ValidateZero[template.FileVars](c); err != nil {
			t.Errorf("expected validate to pass on FileVars-safe template, got: %v", err)
		}
	})

	t.Run("nil-deref template fails", func(t *testing.T) {
		t.Parallel()

		c, err := r.Parse("unsafe", "owner={{ .Catalog.Owner }}")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}

		if err := template.ValidateZero[template.FileVars](c); err == nil {
			t.Error("expected validate to fail on .Catalog.Owner against nil Catalog")
		}
	})
}

func TestValidateZero_PRVars(t *testing.T) {
	t.Parallel()

	r := template.NewRenderer()

	c, err := r.Parse("pr", "{{ .Owner }}/{{ .Repo }}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if err := template.ValidateZero[template.PRVars](c); err != nil {
		t.Errorf("PRVars-safe template should pass validate: %v", err)
	}
}

func TestCompiled_Render_NilReceiverReturnsSentinel(t *testing.T) {
	t.Parallel()

	var c *template.Compiled

	out, err := c.Render(template.FileVars{})
	if !errors.Is(err, template.ErrNilCompiled) {
		t.Errorf("nil receiver should return ErrNilCompiled, got: %v", err)
	}

	if out != "" {
		t.Errorf("nil receiver should return empty string, got: %q", out)
	}
}

func TestValidateZero_NilReceiverReturnsSentinel(t *testing.T) {
	t.Parallel()

	var c *template.Compiled

	err := template.ValidateZero[template.FileVars](c)
	if !errors.Is(err, template.ErrNilCompiled) {
		t.Errorf("nil receiver should return ErrNilCompiled, got: %v", err)
	}
}

func TestCompiled_Name(t *testing.T) {
	t.Parallel()

	r := template.NewRenderer()

	c, err := r.Parse("named", "x")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if c.Name() != "named" {
		t.Errorf("Name() = %q, want %q", c.Name(), "named")
	}
}

// TestPropEnvHelper locks the propenv binding: property names map to
// RG_PROP_-prefixed shell identifiers with every character outside
// [A-Za-z0-9_] replaced by '_'.
func TestPropEnvHelper(t *testing.T) {
	t.Parallel()

	r := template.NewRenderer()

	c, err := r.Parse("propenv", "{{ propenv .Repo }}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "JiraProject", "RG_PROP_JiraProject"},
		{"dot", "my.prop", "RG_PROP_my_prop"},
		{"hyphen", "team-name", "RG_PROP_team_name"},
		{"mixed", "a.b-c_d9", "RG_PROP_a_b_c_d9"},
		{"empty", "", "RG_PROP_"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, err := c.Render(template.FileVars{Common: template.Common{Repo: tt.in}})
			if err != nil {
				t.Fatalf("Render(%q): %v", tt.in, err)
			}

			if out != tt.want {
				t.Errorf("propenv(%q) = %q, want %q", tt.in, out, tt.want)
			}
		})
	}
}

// TestYamlQuoteHelper proves the yamlq binding emits a single-line
// double-quoted YAML scalar that round-trips every hostile input back
// to the exact original value through a real YAML parser (IMPL-0020
// A2 YAML-safe emission).
func TestYamlQuoteHelper(t *testing.T) {
	t.Parallel()

	r := template.NewRenderer()

	c, err := r.Parse("yamlq", "v: {{ yamlq .Repo }}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	tests := []struct {
		name string
		in   string
	}{
		{"plain", "BILL"},
		{"empty", ""},
		{"shell command substitution", "x'$(id)'"},
		{"double quotes", `he said "hi"`},
		{"backslash", `C:\path\to`},
		{"newline", "line1\nline2"},
		{"carriage return", "a\r\nb"},
		{"tab", "a\tb"},
		{"yaml mapping", "a: b"},
		{"yaml flow indicators", "{key: [1, 2]}"},
		{"leading dash", "- item"},
		{"hash comment", "value # not a comment"},
		{"NEL control", "a" + string(rune(0x85)) + "b"},
		{"unicode line separator", "a" + string(rune(0x2028)) + "b"},
		{"unicode paragraph separator", "a" + string(rune(0x2029)) + "b"},
		{"dollar without braces", "$HOME and ${PATH}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, err := c.Render(template.FileVars{Common: template.Common{Repo: tt.in}})
			if err != nil {
				t.Fatalf("Render(%q): %v", tt.in, err)
			}

			if strings.Contains(out, "\n") {
				t.Errorf("yamlq(%q) rendered across multiple lines: %q", tt.in, out)
			}

			var doc struct {
				V string `yaml:"v"`
			}

			if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatalf("rendered scalar is not valid YAML: %v\noutput: %q", err, out)
			}

			if doc.V != tt.in {
				t.Errorf("round-trip = %q, want %q", doc.V, tt.in)
			}
		})
	}
}

// TestYamlQuoteHelper_RejectsGHAExpression proves yamlq refuses values
// carrying a GitHub Actions expression opener: no YAML escaping can
// stop the Actions runner from evaluating "${{ ... }}" after parsing,
// so rendering must fail instead.
func TestYamlQuoteHelper_RejectsGHAExpression(t *testing.T) {
	t.Parallel()

	r := template.NewRenderer()

	c, err := r.Parse("yamlq", "v: {{ yamlq .Repo }}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	hostile := "x${{ secrets.GITHUB_TOKEN }}"

	_, err = c.Render(template.FileVars{Common: template.Common{Repo: hostile}})
	if err == nil {
		t.Fatalf("Render(%q) error = nil, want expression-rejection error", hostile)
	}

	if !strings.Contains(err.Error(), "expression opener") {
		t.Errorf("Render(%q) error = %v, want mention of expression opener", hostile, err)
	}
}
