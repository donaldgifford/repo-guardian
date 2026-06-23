package store_test

import (
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

func TestTruncate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"empty stays empty", "", 10, ""},
		{"short stays unchanged", "hello", 10, "hello"},
		{"exact length stays unchanged", "hello", 5, "hello"},
		{"longer gets clipped with ellipsis", "hello world", 8, "hello w…"},
		{"zero max returns empty", "anything", 0, ""},
		{"negative max returns empty", "anything", -3, ""},
		// "héllo" has 5 runes but 6 bytes — clip on runes, not bytes.
		{"clips on rune boundary", "héllo world", 6, "héllo…"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := store.Truncate(tc.in, tc.max)
			if got != tc.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

// TestTruncate_LongInput_RuneCount confirms the rune-count guarantee
// against a long input (operator dashboards rely on width being
// bounded; if Truncate ever regresses to a byte count this fails on
// any multibyte input).
func TestTruncate_LongInput_RuneCount(t *testing.T) {
	t.Parallel()

	// 50 ASCII chars + 50 multibyte chars + tail.
	in := strings.Repeat("a", 50) + strings.Repeat("é", 50) + "TAIL"

	got := store.Truncate(in, 64)

	runes := []rune(got)
	if len(runes) != 64 {
		t.Fatalf("expected 64 runes, got %d (%q)", len(runes), got)
	}

	if runes[63] != '…' {
		t.Fatalf("expected last rune to be ellipsis, got %q", string(runes[63]))
	}
}
