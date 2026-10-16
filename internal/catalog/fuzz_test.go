package catalog

import (
	"bytes"
	"testing"
)

// FuzzParseDocument_RoundTrip is Render(Parse(x)) == x for the
// catalog-info document (IMPL-0029 3.10): a parsed document's bytes are
// its source, and a Replace of a looked-up span changes that span alone.
func FuzzParseDocument_RoundTrip(f *testing.F) {
	for _, seed := range []string{
		"apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: billing\nspec:\n  owner: platform\n",
		"# comment\nkind: Component # trailing\nmetadata: {name: x}\n",
		"metadata:\n  name: \"quoted\"\n  annotations:\n    jira/project-key: 'BILL'\n",
		"spec:\n  owner: >\n    folded\n",
		"---\nkind: System\n...\n",
		"",
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, src []byte) {
		d, err := ParseDocument(src)
		if err != nil {
			return
		}

		if got := d.Bytes(); !bytes.Equal(got, src) {
			t.Fatalf("Bytes() = %q, want the source %q", got, src)
		}

		_, span, ok := d.Lookup("metadata", "name")
		if !ok || !span.Editable {
			return
		}

		out, err := d.Replace(span, "replaced")
		if err != nil {
			t.Fatalf("Replace(%+v) of an editable span: %v", span, err)
		}

		if !bytes.Equal(out[:span.Start], src[:span.Start]) || !bytes.Equal(out[len(out)-(len(src)-span.End):], src[span.End:]) {
			t.Fatalf("Replace(%+v) touched bytes outside the span:\nsrc %q\nout %q", span, src, out)
		}
	})
}
