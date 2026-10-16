// Package file is the generic file control type, the escape hatch
// (DESIGN-0031 § Generic file): one path, one rule, either exists (the
// file is present) or exact (the file equals the rendered template, YAML
// compared semantically for .yml/.yaml and bytes otherwise, as v1).
// Remediation writes the template. There is no contains and no absent
// (D6): anything that looks inside a file is a control type.
package file

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/template"
)

// Name is the type name a catalogue definition names.
const Name = "file"

// The rule kinds, one per definition.
const (
	KindExists control.RuleKind = "exists"
	KindExact  control.RuleKind = "exact"
)

// The definition parameters.
const (
	ParamPath = "path"
	ParamMode = "mode"
)

// maxBytes is the size cap: the Contents API's 1 MiB.
const maxBytes = 1 << 20

// evidenceVersion versions both rules' evidence.
const evidenceVersion = 1

// The exact rule's comparisons.
const (
	compareYAML  = "yaml"
	compareBytes = "bytes"
)

// Type is the generic file control type.
type Type struct{}

var _ control.Type = Type{}

// Name returns "file".
func (Type) Name() string { return Name }

// Semantics is 1: v1's exists and exact.
func (Type) Semantics() int { return 1 }

// OneRulePerDefinition is true: exists or exact, never both
// (controltest.OneRule).
func (Type) OneRulePerDefinition() bool { return true }

// RuleKinds is exists and exact, both remediable, neither with
// parameters.
func (Type) RuleKinds() []control.RuleKindSpec {
	return []control.RuleKindSpec{{Kind: KindExists, Remediable: true}, {Kind: KindExact, Remediable: true}}
}

// Build validates path, mode and the single rule and compiles the
// template. exact always needs the template, remediable or not, because
// it is what the file is compared with.
func (Type) Build(def control.Definition, env control.BuildEnv) (control.Control, error) { //nolint:gocritic // hugeParam: Type.Build's signature
	p, ok := def.Params[ParamPath].(string)
	if !ok || p == "" {
		return nil, fmt.Errorf("%s: parameter %q (string) is required", def.ID, ParamPath)
	}

	res, err := control.Canonical(control.KindFile, p)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", def.ID, err)
	}

	if len(def.Rules) != 1 {
		return nil, fmt.Errorf("%s: a file control has exactly one rule, exists or exact, never both", def.ID)
	}

	rule := def.Rules[0]

	if m, set := def.Params[ParamMode]; set && m != string(rule.Kind) {
		return nil, fmt.Errorf("%s: mode %v does not match the rule's kind %s", def.ID, m, rule.Kind)
	}

	c := &fileControl{def: def, path: res.Key, owned: []control.Resource{res}}

	if rule.Remediate || rule.Kind == KindExact {
		tmpl, err := compile(def, env)
		if err != nil {
			return nil, err
		}

		c.tmpl = tmpl
	}

	return c, nil
}

func compile(def control.Definition, env control.BuildEnv) (*template.Compiled, error) { //nolint:gocritic // hugeParam: one call per Build
	if def.Template == "" {
		return nil, fmt.Errorf("%s: a remediable or exact file control needs a template", def.ID)
	}

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

	return tmpl, nil
}

// fileControl is the built control.
type fileControl struct {
	def   control.Definition
	path  string
	owned []control.Resource
	tmpl  *template.Compiled
}

func (c *fileControl) ID() control.ID { return c.def.ID }

// Owns is file:<path>: one path, one owner.
func (c *fileControl) Owns() []control.Resource { return append([]control.Resource(nil), c.owned...) }

// Reads is the same path.
func (c *fileControl) Reads() []control.Resource { return append([]control.Resource(nil), c.owned...) }

func (c *fileControl) Rules() []control.Rule { return append([]control.Rule(nil), c.def.Rules...) }

func (c *fileControl) rule() control.Rule { return c.def.Rules[0] }

// observation is what the pinned state says about the path.
type observation struct {
	content []byte
	found   bool
	// reason and status are set when the file cannot be judged.
	reason string
	status control.RuleStatus
	detail map[string]any
}

func (c *fileControl) observe(ctx context.Context, r control.Reader) observation {
	_, content, found, err := control.LocateFile(ctx, r, control.FileSpec{Locations: []string{c.path}, Canonical: c.path})

	switch {
	case errors.Is(err, control.ErrPermission):
		return observation{reason: control.ReasonPermission, status: control.Unknown}
	case errors.Is(err, control.ErrNotRegularFile):
		return observation{reason: control.ReasonNotRegularFile, status: control.Unknown}
	case err != nil:
		return observation{reason: control.ReasonReadError, status: control.Error}
	case found && len(content) > maxBytes:
		return observation{
			found: true, reason: control.ReasonFileTooLarge, status: control.Unknown,
			detail: map[string]any{ParamPath: c.path, "bytes": len(content), "max_bytes": maxBytes},
		}
	}

	return observation{content: content, found: found}
}

// Evaluate checks the one rule. A missing file fails either rule; exact
// renders the template for this repository and compares.
//
//nolint:gocritic // hugeParam: the Control interface is DESIGN-0031's.
func (c *fileControl) Evaluate(ctx context.Context, in control.EvalInput) (control.Evaluation, error) {
	rule := c.rule()
	obs := c.observe(ctx, in.Reader)

	res := control.RuleResult{RuleID: rule.ID}

	switch {
	case obs.reason != "":
		res.Status, res.Evidence = obs.status, merge(control.FrameworkEvidence(obs.reason), obs.detail)
	case !obs.found:
		res.Status, res.Evidence = control.Fail, merge(control.FrameworkEvidence(control.ReasonFileMissing), map[string]any{ParamPath: c.path})
	case rule.Kind == KindExists:
		res.Status, res.Evidence = control.Pass, c.evidence(KindExists, nil)
	default:
		res.Status, res.Evidence = c.compare(obs.content, &in.Repo.Vars)
	}

	return control.Evaluation{Results: []control.RuleResult{res}}, nil
}

// compare is the exact rule against present content.
func (c *fileControl) compare(content []byte, vars *template.Vars) (control.RuleStatus, map[string]any) {
	want, err := c.tmpl.Render(*vars)
	if err != nil {
		return control.Error, merge(control.FrameworkEvidence("template_error"), map[string]any{ParamPath: c.path, "error": err.Error()})
	}

	how := compareBytes
	if isYAML(c.path) {
		how = compareYAML
	}

	equal, err := equalContent(how, content, []byte(want))
	if err != nil {
		return control.Error, merge(control.FrameworkEvidence("template_error"), map[string]any{ParamPath: c.path, "error": err.Error()})
	}

	status := control.Fail
	if equal {
		status = control.Pass
	}

	return status, c.evidence(KindExact, map[string]any{"comparison": how, "equal": equal})
}

func (c *fileControl) evidence(kind control.RuleKind, extra map[string]any) map[string]any {
	ev := map[string]any{
		control.EvidenceKind:    Name + "." + string(kind),
		control.EvidenceVersion: evidenceVersion,
		ParamPath:               c.path,
	}

	return merge(ev, extra)
}

// Remediate writes the template when the rule failed and is remediable:
// a missing file, or one that differs from the template under exact. An
// unjudgeable file is never written.
//
//nolint:gocritic // hugeParam: the Control interface is DESIGN-0031's.
func (c *fileControl) Remediate(ctx context.Context, in control.RemediationInput) (control.ChangeSet, error) {
	rule := c.rule()

	if obs := c.observe(ctx, in.Reader); obs.reason != "" {
		return control.ChangeSet{
			Blocked: []control.RuleID{rule.ID},
			Notes:   []string{fmt.Sprintf("%s/%s: %s could not be read (%s); fix it by hand", c.def.ID, rule.ID, c.path, obs.reason)},
		}, nil
	}

	if !failed(in.Evaluation, rule.ID) {
		return control.ChangeSet{}, nil
	}

	if !rule.Remediate {
		return control.ChangeSet{
			Manual: []control.RuleID{rule.ID},
			Notes:  []string{fmt.Sprintf("%s/%s: not fixable automatically; fix it by hand", c.def.ID, rule.ID)},
		}, nil
	}

	body, err := c.tmpl.Render(in.Repo.Vars)
	if err != nil {
		return control.ChangeSet{}, fmt.Errorf("%s: rendering template: %w", c.def.ID, err)
	}

	reason := "missing"
	if ev := evidenceOf(in.Evaluation, rule.ID); ev["reason"] != control.ReasonFileMissing {
		reason = "rule_failed:" + string(rule.ID)
	}

	return control.ChangeSet{
		Files: []control.FileChange{{Path: c.path, Content: []byte(body), Reason: reason}},
		Fixed: []control.RuleID{rule.ID},
	}, nil
}

func failed(ev control.Evaluation, id control.RuleID) bool {
	for _, r := range ev.Results {
		if r.RuleID == id {
			return r.Status == control.Fail
		}
	}

	return false
}

func evidenceOf(ev control.Evaluation, id control.RuleID) map[string]any {
	for _, r := range ev.Results {
		if r.RuleID == id {
			return r.Evidence
		}
	}

	return nil
}

func isYAML(p string) bool {
	ext := strings.ToLower(path.Ext(p))

	return ext == ".yml" || ext == ".yaml"
}

// equalContent compares bytes, or YAML documents semantically: a file
// that is not YAML is simply not equal, while a template that is not
// YAML is an error.
func equalContent(how string, got, want []byte) (bool, error) {
	if how == compareBytes {
		return bytes.Equal(got, want), nil
	}

	wantDocs, err := yamlDocs(want)
	if err != nil {
		return false, fmt.Errorf("rendered template is not YAML: %w", err)
	}

	gotDocs, err := yamlDocs(got)
	if err != nil {
		return false, nil //nolint:nilerr // an unparseable file is a failed comparison, not an error
	}

	return reflect.DeepEqual(gotDocs, wantDocs), nil
}

// FIX: yaml.v3 checks duplicate mapping keys pairwise, so decoding a
// repository-controlled file with a very wide mapping is quadratic (50k
// keys took >20s in IMPL-0029 3.10). internal/catalog walks the node tree
// linearly instead; this compare needs the same treatment.

func yamlDocs(b []byte) ([]any, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))

	var docs []any

	for {
		var doc any

		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return docs, nil
		}

		if err != nil {
			return nil, err
		}

		docs = append(docs, doc)
	}
}

func merge(ev, extra map[string]any) map[string]any {
	for k, v := range extra {
		ev[k] = v
	}

	return ev
}
