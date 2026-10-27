package store

import (
	"encoding/json"
	"os"
	"testing"
)

// TestCompliantPercent_Golden pins the compliance math to a table the
// UI's view-model test reads too (ui/web/src/lib/format.test.ts), so the
// API and the UI can never floor differently.
func TestCompliantPercent_Golden(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../api/testdata/compliant_percent.json")
	if err != nil {
		t.Fatal(err)
	}

	var cases []struct {
		Compliant    int      `json:"compliant"`
		NonCompliant int      `json:"non_compliant"`
		Percent      *float64 `json:"percent"`
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}

	for _, c := range cases {
		got := CompliantPercent(StatusCounts{Compliant: c.Compliant, NonCompliant: c.NonCompliant, NotApplicable: 3, Unknown: 2})

		switch {
		case c.Percent == nil && got != nil:
			t.Errorf("%d/%d: got %v, want null", c.Compliant, c.NonCompliant, *got)
		case c.Percent != nil && (got == nil || *got != *c.Percent):
			t.Errorf("%d/%d: got %v, want %v", c.Compliant, c.NonCompliant, got, *c.Percent)
		}
	}
}
