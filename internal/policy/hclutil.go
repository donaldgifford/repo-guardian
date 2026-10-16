package policy

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

// at renders a source range as file:line.
func at(r hcl.Range) string {
	return fmt.Sprintf("%s:%d", r.Filename, r.Start.Line)
}

// goValue converts a decoded HCL value to the Go shapes a ParamSpec
// accepts: string, bool, int64 (float64 when not integral), []any and
// map[string]any.
func goValue(v cty.Value) (any, error) {
	if v.IsNull() {
		return nil, errors.New("null is not a parameter value")
	}

	if !v.IsWhollyKnown() {
		return nil, errors.New("value is not known at load")
	}

	t := v.Type()

	switch {
	case t == cty.String:
		return v.AsString(), nil
	case t == cty.Bool:
		return v.True(), nil
	case t == cty.Number:
		bf := v.AsBigFloat()
		if bf.IsInt() {
			if i, acc := bf.Int64(); acc == big.Exact {
				return i, nil
			}
		}

		f, _ := bf.Float64()

		return f, nil
	case t.IsListType() || t.IsTupleType() || t.IsSetType():
		return goList(v)
	case t.IsMapType() || t.IsObjectType():
		return goMap(v)
	default:
		return nil, fmt.Errorf("unsupported value type %s", t.FriendlyName())
	}
}

func goList(v cty.Value) ([]any, error) {
	out := make([]any, 0, v.LengthInt())

	for it := v.ElementIterator(); it.Next(); {
		_, e := it.Element()

		g, err := goValue(e)
		if err != nil {
			return nil, err
		}

		out = append(out, g)
	}

	return out, nil
}

func goMap(v cty.Value) (map[string]any, error) {
	out := make(map[string]any, v.LengthInt())

	for it := v.ElementIterator(); it.Next(); {
		k, e := it.Element()

		g, err := goValue(e)
		if err != nil {
			return nil, err
		}

		out[k.AsString()] = g
	}

	return out, nil
}
