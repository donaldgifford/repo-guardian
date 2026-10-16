package catalog

import (
	"bytes"
	"errors"
	"strings"
	"testing"
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
