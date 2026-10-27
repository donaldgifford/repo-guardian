package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/tools/rgctl/internal/v1config"
)

const fixtures = "internal/v1config/testdata/"

func TestConfigShow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		want     int
		wantOut  []string
		wantLogs string
	}{
		{
			name: "full example table",
			args: []string{cmdConfig, "show", fixtures + "guardian-full.hcl"},
			want: exitOK,
			wantOut: []string{
				"every installed org (legacy mode)",
				"codeowners",
				"no_dependabot",
				"custom_properties (watch)",
				`"renovate_config"`,
				"UNRECOGNISED\n  (none)",
			},
		},
		{
			name:    "enterprise example orgs",
			args:    []string{cmdConfig, "show", fixtures + "guardian-enterprise.hcl"},
			want:    exitOK,
			wantOut: []string{"myent-platform, myent-product, myent-sandbox"},
		},
		{
			name:     "unrecognised items warn and list with lines",
			args:     []string{cmdConfig, "show", fixtures + "unknown-block.hcl"},
			want:     exitOK,
			wantOut:  []string{"line 5", `widget "sprocket"`, "guardian > webhook_ip_allowlist"},
			wantLogs: "does not recognise",
		},
		{name: "broken file", args: []string{cmdConfig, "show", fixtures + "broken.hcl"}, want: exitUsage, wantLogs: "broken.hcl:"},
		{name: "missing file", args: []string{cmdConfig, "show", fixtures + "missing.hcl"}, want: exitUsage, wantLogs: "missing.hcl"},
		{name: "no file", args: []string{cmdConfig, "show"}, want: exitUsage, wantLogs: "accepts 1 arg"},
		{name: "bad format", args: []string{cmdConfig, "show", "--format", "yaml", fixtures + "broken.hcl"}, want: exitUsage, wantLogs: "--format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			got := run(append(tt.args, "--no-color"), &stdout, &stderr)
			if got != tt.want {
				t.Fatalf("run(%q) = %d, want %d; stdout=%q stderr=%q", tt.args, got, tt.want, stdout.String(), stderr.String())
			}
			for _, w := range tt.wantOut {
				if !strings.Contains(stdout.String(), w) {
					t.Errorf("run(%q) stdout does not contain %q:\n%s", tt.args, w, stdout.String())
				}
			}
			if tt.wantLogs != "" && !strings.Contains(stdout.String()+stderr.String(), tt.wantLogs) {
				t.Errorf("run(%q) output does not contain %q:\n%s%s", tt.args, tt.wantLogs, stdout.String(), stderr.String())
			}
		})
	}
}

func TestConfigShow_JSON(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if got := run([]string{cmdConfig, "show", "--format", "json", fixtures + "guardian-enterprise.hcl"}, &stdout, &stderr); got != exitOK {
		t.Fatalf("run() = %d, want %d; stderr=%q", got, exitOK, stderr.String())
	}
	var doc v1config.Document
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not a JSON document: %v\n%s", err, stdout.String())
	}
	if doc.LegacyMode || len(doc.Orgs) != 3 || len(doc.Rules) == 0 {
		t.Errorf(
			"decoded document = legacy %v, %d orgs, %d rules; want strict mode, 3 orgs, some rules",
			doc.LegacyMode,
			len(doc.Orgs),
			len(doc.Rules),
		)
	}
}
