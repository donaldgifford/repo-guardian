package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

const sample = `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: payments   # the service
  annotations:
    jira/project-key: "PAY"
    team/slack: 'it''s #payments'
  description: |
    block text
spec:
  owner: team-ü
  lifecycle: production
`

func TestParseDocument_Spans(t *testing.T) {
	t.Parallel()

	d, err := ParseDocument([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(d.Bytes(), []byte(sample)) || !d.IsComponent() {
		t.Fatalf("Bytes() not byte-identical or IsComponent() false")
	}

	tests := []struct {
		path     []string
		value    string
		text     string
		editable bool
	}{
		{[]string{"metadata", "name"}, "payments", "payments", true},
		{[]string{"metadata", "annotations", "jira/project-key"}, "PAY", `"PAY"`, true},
		{[]string{"metadata", "annotations", "team/slack"}, "it's #payments", `'it''s #payments'`, true},
		{[]string{"spec", "owner"}, "team-ü", "team-ü", true},
		{[]string{"metadata", "description"}, "block text\n", "", false},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.path, "."), func(t *testing.T) {
			t.Parallel()

			v, sp, ok := d.Lookup(tt.path...)
			if !ok || v != tt.value || sp.Editable != tt.editable {
				t.Fatalf("Lookup(%v) = %q, %+v, %v; want %q editable=%v", tt.path, v, sp, ok, tt.value, tt.editable)
			}

			if tt.editable && string(d.src[sp.Start:sp.End]) != tt.text {
				t.Errorf("span text = %q, want %q", d.src[sp.Start:sp.End], tt.text)
			}
		})
	}

	if _, _, ok := d.Lookup("metadata", "missing"); ok {
		t.Error("Lookup(metadata.missing) found a value")
	}

	if _, _, ok := d.Lookup("metadata"); ok {
		t.Error("Lookup(metadata) returned a mapping as a scalar")
	}
}

func TestDocument_ReplaceIsMinimal(t *testing.T) {
	t.Parallel()

	d, err := ParseDocument([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}

	_, sp, _ := d.Lookup("spec", "owner")

	out, err := d.Replace(sp, "team-payments")
	if err != nil {
		t.Fatal(err)
	}

	if want := strings.Replace(sample, "owner: team-ü", "owner: team-payments", 1); string(out) != want {
		t.Errorf("Replace() =\n%s\nwant\n%s", out, want)
	}

	_, block, _ := d.Lookup("metadata", "description")
	if _, err := d.Replace(block, "x"); err == nil {
		t.Error("Replace(block scalar) err = nil, want refused")
	}
}

func TestParseDocument_Errors(t *testing.T) {
	t.Parallel()

	if _, err := ParseDocument(bytes.Repeat([]byte("a"), MaxBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("over the cap: err = %v, want ErrTooLarge", err)
	}

	if _, err := ParseDocument(bytes.Repeat([]byte("#"), MaxBytes)); err == nil {
		t.Error("at the cap: a comment-only document parsed as a mapping")
	}

	var pe *ParseError

	if _, err := ParseDocument([]byte("kind: Component\nmetadata: [\n")); !errors.As(err, &pe) || pe.Line == 0 {
		t.Errorf("unparseable: err = %v, want a *ParseError with a line", err)
	}

	if _, err := ParseDocument([]byte("- a\n- b\n")); !errors.As(err, &pe) {
		t.Errorf("sequence root: err = %v, want a *ParseError", err)
	}

	d, err := ParseDocument([]byte("apiVersion: backstage.io/v1alpha1\nkind: API\n"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := d.Properties(nil); !errors.Is(err, ErrNotComponent) {
		t.Errorf("Properties() on an API entity err = %v, want ErrNotComponent", err)
	}
}

// TestParseDocument_WideMappingIsLinear is the regression for the
// quadratic duplicate-key check in yaml.v3's Node.Decode. It compares
// growth, not wall-clock time, so a slow runner under -race cannot fail
// it: quadrupling the keys costs ~4x when linear and ~16x when
// quadratic.
func TestParseDocument_WideMappingIsLinear(t *testing.T) {
	t.Parallel()

	small, large := parseWide(t, MaxBytes/4), parseWide(t, MaxBytes)

	if ratio := float64(large) / float64(small); ratio > 10 {
		t.Errorf("ParseDocument() of 4x the annotations took %.1fx as long (%v vs %v), want linear growth", ratio, large, small)
	}
}

// parseWide parses a Component whose annotations fill size bytes and
// returns the fastest of three runs.
func parseWide(t *testing.T, size int) time.Duration {
	t.Helper()

	var b bytes.Buffer

	b.WriteString("apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  annotations:\n")

	for i := 0; b.Len() < size-64; i++ {
		fmt.Fprintf(&b, "    k%d: v\n", i)
	}

	fastest := time.Duration(1<<63 - 1)

	for range 3 {
		start := time.Now()

		if _, err := ParseDocument(b.Bytes()); err != nil {
			t.Fatalf("ParseDocument() err = %v", err)
		}

		fastest = min(fastest, time.Since(start))
	}

	return fastest
}

func TestParseDocument_Refusals(t *testing.T) {
	t.Parallel()

	for name, src := range map[string]string{
		"duplicate root key":      "kind: Component\nkind: System\n",
		"duplicate annotation":    "metadata:\n  annotations:\n    a: x\n    a: y\n",
		"metadata not a mapping":  "metadata: [a]\n",
		"name not a scalar":       "metadata:\n  name: {a: b}\n",
		"annotation not a scalar": "metadata:\n  annotations:\n    a: [x]\n",
		"spec owner not a scalar": "spec:\n  owner: [x]\n",
	} {
		var pe *ParseError
		if _, err := ParseDocument([]byte(src)); !errors.As(err, &pe) {
			t.Errorf("%s: err = %v, want a *ParseError", name, err)
		}
	}

	d, err := ParseDocument([]byte("kind: Component\nmetadata:\n  name: ~\nspec: &s\n  owner: 7\nother: *s\n"))
	if err != nil || d.Entity.Metadata.Name != "" || d.Entity.Spec.Owner != "7" {
		t.Errorf("ParseDocument() = %+v, %v; want a null name and the scalar owner \"7\"", d, err)
	}
}
