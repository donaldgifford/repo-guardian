package control

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
)

// FileReferencer is implemented by a Type whose definitions reference
// template-store files beyond Definition.Template (a labels file, a
// ruleset document). Their bytes are part of the revision (D19).
type FileReferencer interface {
	ReferencedFiles(def Definition) []string
}

// revisionInput is the canonical definition Revision hashes. Display-only
// fields (the definition's title, each rule's number and title, the pr {}
// block) are not in it, so a cosmetic edit changes no revision.
type revisionInput struct {
	Slug      string            `json:"slug"`
	Version   int               `json:"version"`
	Type      string            `json:"type"`
	Semantics int               `json:"semantics"`
	Rules     []revisionRule    `json:"rules"`
	Apply     string            `json:"apply"`
	Params    map[string]any    `json:"params"`
	Template  string            `json:"template"`
	Files     map[string]string `json:"files"`
}

type revisionRule struct {
	ID        RuleID         `json:"id"`
	Kind      RuleKind       `json:"kind"`
	Params    map[string]any `json:"params"`
	Remediate bool           `json:"remediate"`
}

// Revision is the content digest results bind to (D19): sha256 over the
// canonical definition, which is the id, version, type name, the type's
// Semantics, the rules with their parameters, apply, the type-level
// parameters, the template bytes and the bytes of every file the
// definition references. A referenced name the template store lacks is an
// error.
//
//nolint:gocritic // hugeParam: the signature is DESIGN-0031's, matching Type.Build.
func Revision(t Type, def Definition, env BuildEnv) (string, error) {
	in := revisionInput{
		Slug: def.ID.Slug, Version: def.ID.Version, Type: t.Name(), Semantics: t.Semantics(),
		Apply: def.Apply, Params: def.Params,
	}

	for _, r := range def.Rules {
		in.Rules = append(in.Rules, revisionRule{ID: r.ID, Kind: r.Kind, Params: r.Params, Remediate: r.Remediate})
	}

	if def.Template != "" {
		body, err := lookup(env, def.Template)
		if err != nil {
			return "", fmt.Errorf("revision of %s: %w", def.ID, err)
		}

		in.Template = body
	}

	if ref, ok := t.(FileReferencer); ok {
		names := slices.Sorted(slices.Values(ref.ReferencedFiles(def)))
		in.Files = make(map[string]string, len(names))

		for _, name := range names {
			body, err := lookup(env, name)
			if err != nil {
				return "", fmt.Errorf("revision of %s: %w", def.ID, err)
			}

			in.Files[name] = body
		}
	}

	return digest(in)
}

func lookup(env BuildEnv, name string) (string, error) {
	if env.Templates == nil {
		return "", fmt.Errorf("template %q: no template store", name)
	}

	body, ok := env.Templates.Lookup(name)
	if !ok {
		return "", fmt.Errorf("template %q not found", name)
	}

	return body, nil
}

// fingerprintInput is what Fingerprint hashes.
type fingerprintInput struct {
	Revision string      `json:"revision"`
	Rules    [][2]string `json:"rules"`
	Reads    [][3]string `json:"reads"`
}

// Fingerprint is what DESIGN-0032 compares between evaluations. It hashes
// the control revision, the sorted (rule id, status) pairs and the sorted
// (resource, state, digest) reads, and nothing else: not evidence
// wording, not timestamps, not the pinned commit. Because reads carry
// digests, an API value moving from one wrong value to another changes
// the fingerprint even when every rule status is unchanged (D13).
func Fingerprint(revision string, ev Evaluation) string {
	in := fingerprintInput{Revision: revision, Rules: [][2]string{}, Reads: [][3]string{}}

	for _, r := range ev.Results {
		in.Rules = append(in.Rules, [2]string{string(r.RuleID), string(r.Status)})
	}

	for _, r := range ev.Reads {
		in.Reads = append(in.Reads, [3]string{r.Resource.String(), string(r.State), r.Digest})
	}

	slices.SortFunc(in.Rules, func(a, b [2]string) int { return compareStrings(a[:], b[:]) })
	slices.SortFunc(in.Reads, func(a, b [3]string) int { return compareStrings(a[:], b[:]) })

	// The input is strings only, so encoding cannot fail.
	d, _ := digest(in) //nolint:errcheck // strings always encode

	return d
}

func compareStrings(a, b []string) int {
	return slices.Compare(a, b)
}

// digest is the hex sha256 of v's JSON encoding. encoding/json sorts map
// keys, so a map encodes canonically.
func digest(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encoding: %w", err)
	}

	sum := sha256.Sum256(b)

	return hex.EncodeToString(sum[:]), nil
}
