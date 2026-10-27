package dependency_updates

import (
	"errors"
	"slices"
	"testing"
)

func TestAddExtends(t *testing.T) {
	t.Parallel()

	const p = "github>acme/cfg"

	tests := []struct{ name, in, want string }{
		{"empty list", `{"extends": []}`, `{"extends": ["github>acme/cfg"]}`},
		{"single-line list", `{"extends": ["a"], "x": 1}`, `{"extends": ["a", "github>acme/cfg"], "x": 1}`},
		{"empty object", `{}`, `{"extends": ["github>acme/cfg"]}`},
		{"tab-indented", "{\n\t\"extends\": [\n\t\t\"a\"\n\t]\n}", "{\n\t\"extends\": [\n\t\t\"a\",\n\t\t\"github>acme/cfg\"\n\t]\n}"},
		{
			"nested extends untouched",
			`{"packageRules": [{"extends": ["z"]}]}`,
			`{"extends": ["github>acme/cfg"],"packageRules": [{"extends": ["z"]}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := addExtends([]byte(tt.in), p)
			if err != nil || string(got) != tt.want {
				t.Errorf("addExtends(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
			}
		})
	}

	if _, err := addExtends([]byte(`{"extends": "a"}`), p); !errors.Is(err, errExtendsNotList) {
		t.Errorf("addExtends(string extends) err = %v, want errExtendsNotList", err)
	}
}

func TestNormaliseJSON5(t *testing.T) {
	t.Parallel()

	in := "// head\n{\n  extends: ['a', \"b\", 'it\\'s \"q\"',], /* c */\n  $schema: 'x', n: 1e3,\n}\n"

	got, err := extendsOf([]byte(in), true)
	if err != nil || !slices.Equal(got, []string{"a", "b", `it's "q"`}) {
		t.Errorf("extendsOf(json5) = %q, %v", got, err)
	}
}
