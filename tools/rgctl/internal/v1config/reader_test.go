package v1config

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata/golden")

func TestRead_Golden(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"guardian-full", "guardian-enterprise", "legacy-mode", "unknown-block"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			doc, err := Read(filepath.Join("testdata", name+".hcl"))
			if err != nil {
				t.Fatalf("Read(%s) error = %v", name, err)
			}
			got, err := json.MarshalIndent(doc, "", "  ")
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got = append(got, '\n')
			golden := filepath.Join("testdata", "golden", name+".json")
			if *update {
				if err := os.WriteFile(golden, got, 0o600); err != nil {
					t.Fatalf("write golden: %v", err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("Read(%s) differs from %s; run go test -update and review the diff", name, golden)
			}
		})
	}
}

func TestRead_ExamplesHaveNothingUnrecognised(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"guardian-full", "guardian-enterprise"} {
		doc, err := Read(filepath.Join("testdata", name+".hcl"))
		if err != nil {
			t.Fatalf("Read(%s) error = %v", name, err)
		}
		if len(doc.Unrecognised) != 0 {
			t.Errorf("Read(%s).Unrecognised = %+v, want none", name, doc.Unrecognised)
		}
	}
}

func TestRead_Unrecognised(t *testing.T) {
	t.Parallel()
	doc, err := Read(filepath.Join("testdata", "unknown-block.hcl"))
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	want := []Item{
		{Kind: kindBlock, Name: "widget", Source: `widget "sprocket"`, Line: 5},
		{Kind: kindAttribute, Name: "guardian > webhook_ip_allowlist", Source: `webhook_ip_allowlist = ["10.0.0.0/8"]`, Line: 11},
		{Kind: kindAttribute, Name: `rule "file" "renovate" > colour`, Source: `colour = "blue"`, Line: 16},
		{Kind: kindAttribute, Name: `rule "file" "renovate" > reconcile "workflow_sync" > cadence`, Source: `cadence = "daily"`, Line: 22},
		{Kind: kindBlock, Name: "rule", Source: `rule "teleport" "beam"`, Line: 26},
	}
	if !slices.Equal(doc.Unrecognised, want) {
		t.Errorf("Read().Unrecognised =\n%+v\nwant\n%+v", doc.Unrecognised, want)
	}
	// Everything the reader does know is unaffected by the unknown items.
	if len(doc.Rules) != 1 || doc.Rules[0].Name != "renovate" || !slices.Equal(doc.Rules[0].Paths, []string{"renovate.json"}) {
		t.Errorf("Read().Rules = %+v, want the one renovate rule with its paths", doc.Rules)
	}
	if len(doc.Guardian) != 1 || doc.Guardian[0].Name != "dry_run" {
		t.Errorf("Read().Guardian = %+v, want only dry_run", doc.Guardian)
	}
}

func TestRead_SourceTextIsNotEvaluated(t *testing.T) {
	t.Parallel()
	doc, err := Read(filepath.Join("testdata", "legacy-mode.hcl"))
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(doc.Locals) != 1 || doc.Locals[0].Source != `"platform"` {
		t.Errorf("Read().Locals = %+v, want owner as declared", doc.Locals)
	}
	rule := doc.Rules[0]
	if rule.Enabled != nil {
		t.Errorf("rule.Enabled = %v, want nil for a non-literal", *rule.Enabled)
	}
	if len(rule.Attrs) != 1 || rule.Attrs[0].Source != "local.enabled" {
		t.Errorf("rule.Attrs = %+v, want enabled kept as source text", rule.Attrs)
	}
	if got := rule.PR.Attrs[0].Source; !strings.Contains(got, "${local.owner}") {
		t.Errorf("rule.PR title source = %q, want the interpolation verbatim", got)
	}
}

func TestRead_Errors(t *testing.T) {
	t.Parallel()
	if _, err := Read(filepath.Join("testdata", "broken.hcl")); err == nil || !strings.Contains(err.Error(), "broken.hcl:") {
		t.Errorf("Read(broken.hcl) error = %v, want an HCL diagnostic naming the file", err)
	}
	if _, err := Read(filepath.Join("testdata", "missing.hcl")); err == nil {
		t.Error("Read(missing.hcl) error = nil, want an error")
	}
}

func TestOrgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		file       string
		wantOrgs   []string
		wantLegacy bool
	}{
		{file: "guardian-enterprise.hcl", wantOrgs: []string{"myent-platform", "myent-product", "myent-sandbox"}},
		{file: "legacy-mode.hcl", wantLegacy: true},
	}
	for _, tt := range tests {
		orgs, legacy, err := Orgs(filepath.Join("testdata", tt.file))
		if err != nil {
			t.Fatalf("Orgs(%s) error = %v", tt.file, err)
		}
		if !slices.Equal(orgs, tt.wantOrgs) || legacy != tt.wantLegacy {
			t.Errorf("Orgs(%s) = %v, %v, want %v, %v", tt.file, orgs, legacy, tt.wantOrgs, tt.wantLegacy)
		}
	}
}
