package dependency_updates

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
)

const fuzzPreset = "github>example-org/renovate-config"

// FuzzAddExtends is Render(Parse(x)) == x for the Renovate JSON editor
// (IMPL-0029 3.10): when addExtends edits a config, the result is valid
// JSON whose extends holds the preset, and it is x with one contiguous
// insertion, every other byte unchanged.
func FuzzAddExtends(f *testing.F) {
	for _, seed := range []string{
		`{}`,
		`{"extends": []}`,
		`{"extends": ["config:recommended"]}`,
		"{\n  \"$schema\": \"https://docs.renovatebot.com/renovate-schema.json\",\n  \"extends\": [\n    \"config:recommended\"\n  ]\n}\n",
		`{"labels": ["deps"], "extends": ["a", "b"], "x": {"extends": 1}}`,
		`{"extends": "config:recommended"}`,
		`[1, 2]`,
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, src []byte) {
		out, err := addExtends(src, fuzzPreset)
		if err != nil {
			return
		}

		if !json.Valid(out) {
			t.Fatalf("addExtends(%q) = %q, not valid JSON", src, out)
		}

		list, err := extendsOf(out, false)
		if err != nil || !slices.Contains(list, fuzzPreset) {
			t.Fatalf("addExtends(%q) = %q: extends %v (err %v), want the preset", src, out, list, err)
		}

		if !oneInsertion(src, out) {
			t.Fatalf("addExtends(%q) = %q, want src with one insertion", src, out)
		}
	})
}

// oneInsertion reports whether out is src with one contiguous run of
// bytes inserted.
func oneInsertion(src, out []byte) bool {
	if len(out) < len(src) {
		return false
	}

	prefix := 0
	for prefix < len(src) && src[prefix] == out[prefix] {
		prefix++
	}

	return bytes.HasSuffix(out, src[prefix:])
}

// FuzzNormaliseJSON5 never panics, and what it accepts as JSON5 that is
// already JSON keeps its value.
func FuzzNormaliseJSON5(f *testing.F) {
	for _, seed := range []string{
		"{\n  // comment\n  extends: ['config:recommended',],\n}\n",
		`{"extends": ["a"]}`,
		"/* block */ {'a': \"it's\"}",
		`{a: 'x\'y'}`,
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, src []byte) {
		out, err := normaliseJSON5(src)
		if err != nil || !json.Valid(src) {
			return
		}

		a, aerr := decodeNumbers(src)
		b, berr := decodeNumbers(out)

		if aerr != nil {
			return
		}

		if berr != nil {
			t.Fatalf("normaliseJSON5(%q) = %q: valid JSON did not stay valid: %v", src, out, berr)
		}

		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)

		if !bytes.Equal(ja, jb) {
			t.Fatalf("normaliseJSON5(%q) = %q: value changed", src, out)
		}
	})
}

// decodeNumbers decodes JSON keeping numbers as written, so a value too
// large for a float64 still compares.
func decodeNumbers(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()

	var v any

	err := dec.Decode(&v)

	return v, err
}
