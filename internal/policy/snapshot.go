// Package policy is the controls policy model (DESIGN-0030): the policy
// root (a control catalogue, one enterprise policy and an optional
// policy per org) loaded into one immutable Snapshot, and the pure
// resolution of a repository's control assignments from it.
package policy

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// Snapshot is one loaded policy root. It is immutable and safe for
// concurrent use.
type Snapshot struct {
	files    map[string][]byte
	warnings []Warning
	defs     map[control.ID]*definition
	// embeddedUsed are the embedded templates the catalogue resolved to,
	// by name.
	embeddedUsed map[string]string
	enterprise   *enterprisePolicy
	orgs         []*orgPolicy
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

// versionPrefix marks a controls policy version, distinct from the rc's
// "v2:" rule-policy versions.
const versionPrefix = "controls:"

// Version is the policy version: a digest over every loaded file's
// bytes, the embedded templates the catalogue fell back to, and every
// definition's revision (which carries its type's semantics), so a
// template edit, an embedded default changed by a binary upgrade and a
// type semantics bump are all policy changes.
func (s *Snapshot) Version() string {
	h := sha256.New()

	write := func(parts ...string) {
		for _, p := range parts {
			h.Write([]byte(strconv.Itoa(len(p)) + ":" + p))
		}
	}

	for _, name := range slices.Sorted(maps.Keys(s.files)) {
		write("file", name, string(s.files[name]))
	}

	for _, name := range slices.Sorted(maps.Keys(s.embeddedUsed)) {
		write("embedded", name, s.embeddedUsed[name])
	}

	for _, id := range s.ids() {
		write("revision", id.String(), s.defs[id].revision)
	}

	return versionPrefix + hex.EncodeToString(h.Sum(nil))
}

// Control returns the runnable control for id, or nil when the catalogue
// has no such definition.
func (s *Snapshot) Control(id control.ID) control.Control {
	if d := s.defs[id]; d != nil {
		return d.ctl
	}

	return nil
}

// Definition returns the catalogue definition for id and whether it
// exists.
func (s *Snapshot) Definition(id control.ID) (control.Definition, bool) {
	d := s.defs[id]
	if d == nil {
		return control.Definition{}, false
	}

	return d.def, true
}

// Revision returns the revision digest of id, or "" when unknown.
func (s *Snapshot) Revision(id control.ID) string {
	if d := s.defs[id]; d != nil {
		return d.revision
	}

	return ""
}

// Controls returns every catalogue ID, sorted.
func (s *Snapshot) Controls() []control.ID { return s.ids() }

func (s *Snapshot) ids() []control.ID {
	ids := slices.Collect(maps.Keys(s.defs))
	slices.SortFunc(ids, func(a, b control.ID) int {
		return cmp.Or(cmp.Compare(a.Slug, b.Slug), cmp.Compare(a.Version, b.Version))
	})

	return ids
}

// WatchedPaths is the union of every definition's file keys in Owns()
// and Reads(), sorted: ingest's push filter.
func (s *Snapshot) WatchedPaths() []string {
	seen := make(map[string]bool)

	for _, d := range s.defs {
		for _, r := range slices.Concat(d.ctl.Owns(), d.ctl.Reads()) {
			if r.Kind == control.KindFile {
				seen[r.Key] = true
			}
		}
	}

	return slices.Sorted(maps.Keys(seen))
}
