// Package codeowners is the codeowners control type (DESIGN-0031
// § CODEOWNERS): a CODEOWNERS file exists in one of GitHub's locations,
// and when none does the remediation adds the operator's template at
// .github/CODEOWNERS. An existing file is never edited. Ownership rules
// are DESIGN-0034's and not part of this type.
package codeowners

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// Name is the type name a catalogue definition names.
const Name = "codeowners"

// KindExists is the type's one rule kind.
const KindExists control.RuleKind = "exists"

// maxBytes is the parser size cap: GitHub ignores a CODEOWNERS file of
// 3 MB or more.
const maxBytes = 3 << 20

// The locations, in GitHub's order.
const (
	PathGitHub = ".github/CODEOWNERS"
	PathRoot   = "CODEOWNERS"
	PathDocs   = "docs/CODEOWNERS"
)

// spec is GitHub's precedence: the first found is the file GitHub uses
// (DESIGN-0031 assumption A21).
var spec = control.FileSpec{
	Locations: []string{PathGitHub, PathRoot, PathDocs},
	Canonical: PathGitHub,
}

// evidenceKind and evidenceVersion version the exists rule's evidence.
const (
	evidenceKind    = "codeowners.exists"
	evidenceVersion = 1
)

// Type is the codeowners control type.
type Type struct{}

var _ control.Type = Type{}

// Name returns "codeowners".
func (Type) Name() string { return Name }

// Semantics is 1: v1's existence check.
func (Type) Semantics() int { return 1 }

// RuleKinds is exists, remediable, with no parameters.
func (Type) RuleKinds() []control.RuleKindSpec {
	return []control.RuleKindSpec{{Kind: KindExists, Remediable: true}}
}

// Build compiles the definition's template and returns the file
// control. A remediable rule needs a template; the template source
// supplies the operator's or the embedded fallback (IMPL-0028 spike 7).
func (Type) Build(def control.Definition, env control.BuildEnv) (control.Control, error) { //nolint:gocritic // hugeParam: Type.Build's signature
	cfg := control.FileControlConfig[model]{Def: def, Spec: spec, Type: fileType{}, MaxBytes: maxBytes}

	if def.Template != "" {
		if env.Templates == nil || env.Renderer == nil {
			return nil, errors.New("no template store")
		}

		body, ok := env.Templates.Lookup(def.Template)
		if !ok {
			return nil, fmt.Errorf("template %q not found", def.Template)
		}

		tmpl, err := env.Renderer.Parse(def.Template, body)
		if err != nil {
			return nil, fmt.Errorf("template %q: %w", def.Template, err)
		}

		cfg.Template = tmpl
	}

	return control.NewFileControl(cfg)
}

// model is the file as text; this version does not parse entries.
type model []byte

// fileType is the codeowners FileType.
type fileType struct{}

// Parse keeps the bytes; a CODEOWNERS file of any content parses.
func (fileType) Parse(content []byte) (model, error) { return model(content), nil }

// Check passes exists when the file has a line that is neither blank nor
// a comment.
func (fileType) Check(m model, rule control.Rule) control.RuleResult { //nolint:gocritic // hugeParam: FileType's signature
	entries := entryLines(m)

	status := control.Fail
	if entries > 0 {
		status = control.Pass
	}

	return control.RuleResult{
		RuleID: rule.ID,
		Status: status,
		Evidence: map[string]any{
			control.EvidenceKind:    evidenceKind,
			control.EvidenceVersion: evidenceVersion,
			"entries":               entries,
		},
	}
}

// Fix never edits an existing file: a comment-only CODEOWNERS is left to
// its team (Manual).
func (fileType) Fix(m model, _ []control.Rule) (model, []control.RuleID, error) { return m, nil, nil }

// Render returns the bytes unchanged.
func (fileType) Render(m model) ([]byte, error) { return m, nil }

// entryLines counts the lines that are neither blank nor a comment.
func entryLines(content []byte) int {
	n := 0

	for line := range bytes.Lines(content) {
		line = bytes.TrimSpace(line)
		if len(line) > 0 && line[0] != '#' {
			n++
		}
	}

	return n
}
