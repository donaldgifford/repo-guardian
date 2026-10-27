package alert

import (
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/monitoring"
)

// TestSpec_SkipReason pins mechanism gating while the catalogue gates
// nothing (TestGenerate_NothingIsGatedYet): IMPL-0030's remediation
// alerts depend on it, and every omission must say why.
func TestSpec_SkipReason(t *testing.T) {
	t.Parallel()

	gated := Spec{Requires: []monitoring.Mechanism{monitoring.MechanismCustomPropertiesGHA}}
	suppressed := Spec{Excludes: []monitoring.Mechanism{monitoring.MechanismDryRun}}

	tests := []struct {
		name string
		spec Spec
		ms   monitoring.Mechanisms
		want string // "" = kept
	}{
		{name: "unconditional", spec: Spec{}, ms: monitoring.Mechanisms{}},
		{name: "required and configured", spec: gated, ms: monitoring.Mechanisms{monitoring.MechanismCustomPropertiesGHA: {}}},
		{name: "required and missing", spec: gated, ms: monitoring.Mechanisms{}, want: "none configured"},
		{name: "excluded", spec: suppressed, ms: monitoring.Mechanisms{monitoring.MechanismDryRun: {}}, want: "suppressed by"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reason, skip := tt.spec.skipReason(tt.ms)

			if skip != (tt.want != "") || !strings.Contains(reason, tt.want) {
				t.Errorf("skipReason() = (%q, %v), want a skip containing %q", reason, skip, tt.want)
			}
		})
	}
}
