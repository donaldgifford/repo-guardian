package findings

import (
	"strings"
	"testing"
)

func TestClip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{name: "short", in: "abc", max: 5, want: "abc"},
		{name: "exact", in: "abcde", max: 5, want: "abcde"},
		{name: "clipped", in: "abcdef", max: 5, want: "abcd…"},
		{name: "multibyte never split", in: "héllo wörld", max: 3, want: "hé…"},
		{name: "zero", in: "abc", max: 0, want: ""},
		{name: "negative", in: "abc", max: -1, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := Clip(tt.in, tt.max); got != tt.want {
				t.Errorf("Clip(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
			}
		})
	}
}

func TestClip_LongInputStaysWithinLimit(t *testing.T) {
	t.Parallel()

	got := Clip(strings.Repeat("ü", 5000), ClipRunes)
	if n := len([]rune(got)); n != ClipRunes {
		t.Errorf("rune count = %d, want %d", n, ClipRunes)
	}
}
