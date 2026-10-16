// Package policy is the controls policy model (DESIGN-0030): the policy
// root (a control catalogue, one enterprise policy and an optional
// policy per org) loaded into one immutable Snapshot, and the pure
// resolution of a repository's control assignments from it.
package policy

import (
	"fmt"
)

// Snapshot is one loaded policy root. It is immutable and safe for
// concurrent use.
type Snapshot struct {
	files    map[string][]byte
	warnings []Warning
}

// Warning is a load-time warning with its source location.
type Warning struct {
	File string
	Line int
	Msg  string
}

func (w Warning) String() string {
	if w.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", w.File, w.Line, w.Msg)
	}

	return fmt.Sprintf("%s: %s", w.File, w.Msg)
}

// Warnings returns the load-time warnings, in the order found.
func (s *Snapshot) Warnings() []Warning { return append([]Warning(nil), s.warnings...) }
