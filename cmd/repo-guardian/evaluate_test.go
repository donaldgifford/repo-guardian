package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/activities"
	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/control/controltest"
)

var updateEvaluate = flag.Bool("update-evaluate", false, "rewrite testdata/evaluate.golden.json")

// fakeEvalRepo is one repository pinned at a fixed head.
type fakeEvalRepo struct {
	files *controltest.FakeRepo
}

func (*fakeEvalRepo) Head(context.Context) (branch, sha string, err error) {
	return "main", "0123abcd", nil
}

func (r *fakeEvalRepo) Reader(string) control.Reader { return r.files }

func (*fakeEvalRepo) Observer() control.PRObserver { return nil }

// writeEvalPolicy writes a policy root assigning codeowners@1 to acme.
func writeEvalPolicy(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	files := map[string]string{
		"enterprise.hcl": `enterprise {
  orgs     = ["acme"]
  controls = ["codeowners@1"]
}
`,
		"catalogue/codeowners.hcl": `control "codeowners" {
  version     = 1
  type        = "codeowners"
  title       = "CODEOWNERS"
  description = "A CODEOWNERS file exists"
  template    = "codeowners"

  rule "exists" {
    title     = "A CODEOWNERS file exists"
    remediate = true
  }
}
`,
	}

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

func runEvaluateWith(t *testing.T, open evalOpener, args ...string) (string, int) {
	t.Helper()

	var out bytes.Buffer

	cmd := &evaluateCmd{stdout: &out, open: func() (evalOpener, error) { return open, nil }}

	err := cmd.run(t.Context(), args)
	if err == nil {
		return out.String(), exitCompliant
	}

	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("run() = %v, want an exitError", err)
	}

	return out.String(), ee.code
}

func openFiles(files map[string]string) evalOpener {
	return func(context.Context, string, string) (*evalTarget, error) {
		return &evalTarget{repo: &fakeEvalRepo{files: &controltest.FakeRepo{Files: files}}, accountType: control.AccountOrganization}, nil
	}
}

// TestEvaluate_ExitCodes is IMPL-0029 task 5.9: one case per exit code.
func TestEvaluate_ExitCodes(t *testing.T) {
	t.Parallel()

	root := writeEvalPolicy(t)
	noAccess := func(context.Context, string, string) (*evalTarget, error) { return nil, errNoAccess }

	tests := []struct {
		name string
		open evalOpener
		args []string
		want int
	}{
		{name: "compliant", open: openFiles(map[string]string{".github/CODEOWNERS": "* @acme/team\n"}), args: []string{"--repo", "acme/widgets"}},
		{name: "nothing measured", open: openFiles(nil), args: []string{"--repo", "other/widgets"}},
		{name: "non-compliant", open: openFiles(nil), args: []string{"--repo", "acme/widgets"}, want: exitNonCompliant},
		{name: "bad repo", open: openFiles(nil), args: []string{"--repo", "widgets"}, want: exitUsage},
		{name: "bad format", open: openFiles(nil), args: []string{"--repo", "acme/widgets", "--format", "xml"}, want: exitUsage},
		{name: "no access", open: noAccess, args: []string{"--repo", "acme/widgets"}, want: exitOperational},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, got := runEvaluateWith(t, tt.open, append(tt.args, "--policy", root)...); got != tt.want {
				t.Errorf("evaluate %v exit = %d, want %d", tt.args, got, tt.want)
			}
		})
	}

	if _, got := runEvaluateWith(t, openFiles(nil), "--repo", "acme/widgets", "--policy", filepath.Join(root, "missing")); got != exitUsage {
		t.Errorf("evaluate with a missing policy root exit = %d, want %d", got, exitUsage)
	}
}

// TestEvaluate_GoldenJSON pins evaluate's JSON for a fake repository;
// regenerate with -update-evaluate.
func TestEvaluate_GoldenJSON(t *testing.T) {
	t.Parallel()

	out, code := runEvaluateWith(t, openFiles(nil), "--repo", "acme/widgets", "--format", "json", "--policy", writeEvalPolicy(t))
	if code != exitNonCompliant {
		t.Fatalf("exit = %d, want %d", code, exitNonCompliant)
	}

	golden := filepath.Join("testdata", "evaluate.golden.json")

	if *updateEvaluate {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(golden, []byte(out), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update-evaluate): %v", err)
	}

	if out != string(want) {
		t.Errorf("evaluate --format json =\n%s\nwant\n%s", out, want)
	}
}

func TestEvaluate_Table(t *testing.T) {
	t.Parallel()

	out, _ := runEvaluateWith(t, openFiles(nil), "--repo", "acme/widgets", "--policy", writeEvalPolicy(t))

	for _, want := range []string{"acme/widgets", "main@0123abcd", "codeowners@1", "non_compliant", "exists"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

var _ activities.EvalRepo = (*fakeEvalRepo)(nil)
