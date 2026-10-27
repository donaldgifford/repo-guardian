// Package dependency_updates is the dependency-updates control type
// (DESIGN-0031 § Dependency updates): one opinion per definition, Renovate
// or Dependabot. configured says the chosen tool's config exists,
// extends that the Renovate config extends the preset, and exclusive
// that the other tool's config is absent. Remediation is one change set:
// write the chosen tool's config, add the preset to a JSON Renovate
// config (JSON5 gets a note, D5) and delete the other tool's config. It
// may be partial (D14).
package dependency_updates

import (
	"errors"
	"fmt"
	"slices"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/template"
)

// Name is the type name a catalogue definition names.
const Name = "dependency_updates"

// The rule kinds.
const (
	KindConfigured control.RuleKind = "configured"
	KindExtends    control.RuleKind = "extends"
	KindExclusive  control.RuleKind = "exclusive"
)

// The definition parameters: tool, and renovate = { extends = "..." }.
// The preset is a template, so one catalogue entry serves every org.
const (
	ParamTool     = "tool"
	ParamRenovate = "renovate"
	keyExtends    = "extends"
)

// The tools.
const (
	ToolRenovate   = "renovate"
	ToolDependabot = "dependabot"
)

// PathPackageJSON is read for its renovate key, never owned.
const PathPackageJSON = "package.json"

// The canonical locations, where a new config is written.
const (
	PathDependabot = ".github/dependabot.yml"
	PathRenovate   = "renovate.json"
)

// evidencePath names the file a result is about.
const evidencePath = "path"

// DependabotPaths and RenovatePaths are each tool's locations, in the
// order the tool reads them; all are owned and read.
var (
	DependabotPaths = []string{PathDependabot, ".github/dependabot.yaml"}
	RenovatePaths   = []string{
		PathRenovate, "renovate.json5", ".github/renovate.json", ".github/renovate.json5",
		".renovaterc", ".renovaterc.json", ".renovaterc.json5",
	}
)

const (
	maxBytes        = 1 << 20
	evidenceVersion = 1
)

// Type is the dependency-updates control type.
type Type struct{}

var _ control.Type = Type{}

// Name returns "dependency_updates".
func (Type) Name() string { return Name }

// Semantics is 1.
func (Type) Semantics() int { return 1 }

// RuleKinds says all three kinds are remediable, none with parameters.
func (Type) RuleKinds() []control.RuleKindSpec {
	return []control.RuleKindSpec{
		{Kind: KindConfigured, Remediable: true},
		{Kind: KindExtends, Remediable: true},
		{Kind: KindExclusive, Remediable: true},
	}
}

// Build validates tool, the preset and the rules, and compiles the
// template and the preset.
func (Type) Build(def control.Definition, env control.BuildEnv) (control.Control, error) { //nolint:gocritic // hugeParam: Type.Build's signature
	c := &depControl{def: def, rules: make(map[control.RuleKind]control.Rule)}

	switch tool, _ := def.Params[ParamTool].(string); tool { //nolint:errcheck // a missing tool is the default case
	case ToolRenovate:
		c.tool, c.own, c.other = ToolRenovate, RenovatePaths, DependabotPaths
	case ToolDependabot:
		c.tool, c.own, c.other = ToolDependabot, DependabotPaths, RenovatePaths
	default:
		return nil, fmt.Errorf("%s: parameter %q must be %q or %q", def.ID, ParamTool, ToolRenovate, ToolDependabot)
	}

	for _, r := range def.Rules {
		if _, dup := c.rules[r.Kind]; dup {
			return nil, fmt.Errorf("%s: rule kind %s is declared twice", def.ID, r.Kind)
		}

		c.rules[r.Kind] = r
	}

	if err := c.buildExtends(env); err != nil {
		return nil, err
	}

	if r, ok := c.rules[KindConfigured]; ok && r.Remediate {
		tmpl, err := compile(&def, &env, def.Template)
		if err != nil {
			return nil, err
		}

		c.tmpl = tmpl
	}

	for _, p := range slices.Concat(RenovatePaths, DependabotPaths) {
		c.owned = append(c.owned, control.MustCanonical(control.KindFile, p))
	}

	return c, nil
}

// depControl is the built control.
type depControl struct {
	def        control.Definition
	rules      map[control.RuleKind]control.Rule
	tool       string
	own, other []string
	tmpl       *template.Compiled
	preset     *template.Compiled
	owned      []control.Resource
}

func (c *depControl) buildExtends(env control.BuildEnv) error {
	_, hasRule := c.rules[KindExtends]
	if hasRule && c.tool != ToolRenovate {
		return fmt.Errorf("%s: an extends rule needs tool = %q", c.def.ID, ToolRenovate)
	}

	reno, _ := c.def.Params[ParamRenovate].(map[string]any) //nolint:errcheck // absent is checked below
	preset, _ := reno[keyExtends].(string)                  //nolint:errcheck // absent is checked below

	switch {
	case hasRule && preset == "":
		return fmt.Errorf("%s: an extends rule needs renovate = { extends = \"<preset>\" }", c.def.ID)
	case preset == "":
		return nil
	}

	if env.Renderer == nil {
		return errors.New("no template renderer")
	}

	tmpl, err := env.Renderer.Parse(c.def.ID.String()+"/extends", preset)
	if err != nil {
		return fmt.Errorf("%s: renovate.extends: %w", c.def.ID, err)
	}

	c.preset = tmpl

	return nil
}

func compile(def *control.Definition, env *control.BuildEnv, name string) (*template.Compiled, error) {
	if name == "" {
		return nil, fmt.Errorf("%s: a remediable configured rule needs a template", def.ID)
	}

	if env.Templates == nil || env.Renderer == nil {
		return nil, errors.New("no template store")
	}

	body, ok := env.Templates.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("template %q not found", name)
	}

	tmpl, err := env.Renderer.Parse(name, body)
	if err != nil {
		return nil, fmt.Errorf("template %q: %w", name, err)
	}

	return tmpl, nil
}

func (c *depControl) ID() control.ID { return c.def.ID }

// Owns is both tools' locations: deleting the other tool's config is
// part of the fix, so it must be owned.
func (c *depControl) Owns() []control.Resource { return append([]control.Resource(nil), c.owned...) }

// Reads is Owns plus package.json, whose renovate key is read, never
// owned.
func (c *depControl) Reads() []control.Resource {
	return append(c.Owns(), control.MustCanonical(control.KindFile, PathPackageJSON))
}

func (c *depControl) Rules() []control.Rule { return append([]control.Rule(nil), c.def.Rules...) }

func (c *depControl) canonical() string { return c.own[0] }
