package builtin

import (
	"slices"
	"testing"
)

func TestTypes_NamesSortedAndRegistered(t *testing.T) {
	t.Parallel()

	reg := Registry()

	names := make([]string, 0, len(Types()))

	for _, typ := range Types() {
		names = append(names, typ.Name())

		if _, ok := reg.Type(typ.Name()); !ok {
			t.Errorf("Registry() lacks %s", typ.Name())
		}
	}

	if !slices.IsSorted(names) || len(names) != 8 {
		t.Errorf("Types() = %v, want the eight built-ins in name order", names)
	}
}
