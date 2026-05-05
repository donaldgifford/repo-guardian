package template

// ValidateZero executes c against a zero-value of context type T to
// surface render-time errors at config-load time. Used by the opt-in
// --strict-templates flag (see DESIGN-0013 §Render-time behavior). A
// successful Validate proves the template references no fields that
// would fail under the renderer's missingkey=error option for an empty
// context; a Validate error returns the same wrapped render error
// Render would produce.
//
// c must be non-nil; a nil receiver returns ErrNilCompiled (matching
// (*Compiled).Render's contract).
//
// Templates that conditionally use nil-able pointer fields (Catalog,
// etc.) must guard with `{{ if .Field }}...{{ end }}` to satisfy
// strict mode.
func ValidateZero[T any](c *Compiled) error {
	var zero T

	_, err := c.Render(zero)

	return err
}
