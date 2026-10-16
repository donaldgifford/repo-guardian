package builtin

import (
	"strings"
	"testing"
)

func TestWriteReference_CoversEveryKind(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	if err := WriteReference(&b); err != nil {
		t.Fatal(err)
	}

	got := b.String()

	for _, typ := range Types() {
		if !strings.Contains(got, "## `"+typ.Name()+"`") {
			t.Errorf("WriteReference() lacks a section for %s", typ.Name())
		}

		for _, k := range typ.RuleKinds() {
			if !strings.Contains(got, "| `"+string(k.Kind)+"` |") {
				t.Errorf("WriteReference() lacks %s rule kind %s", typ.Name(), k.Kind)
			}
		}
	}
}
