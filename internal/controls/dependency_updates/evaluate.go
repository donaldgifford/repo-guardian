package dependency_updates

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// failure is why something could not be judged.
type failure struct {
	reason string
	status control.RuleStatus
}

func classify(err error) *failure {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, control.ErrPermission):
		return &failure{control.ReasonPermission, control.Unknown}
	case errors.Is(err, control.ErrNotRegularFile):
		return &failure{control.ReasonNotRegularFile, control.Unknown}
	default:
		return &failure{control.ReasonReadError, control.Error}
	}
}

// observation is what one pinned state says about both tools.
type observation struct {
	// own is the chosen tool's config GitHub or the tool would use.
	ownPath    string
	ownContent []byte
	ownFailed  *failure
	// others are the other tool's configs present.
	others       []string
	othersFailed *failure
	// pkg is package.json's renovate key.
	pkgRenovate bool
	pkgExtends  []string
	pkgFailed   *failure
}

func (c *depControl) observe(ctx context.Context, r control.Reader) observation {
	var obs observation

	path, content, found, err := control.LocateFile(ctx, r, control.FileSpec{Locations: c.own, Canonical: c.canonical()})
	obs.ownFailed = classify(err)

	if found {
		obs.ownPath, obs.ownContent = path, content
	}

	for _, p := range c.other {
		_, _, ok, err := r.GetContents(ctx, p)

		switch {
		case err != nil && obs.othersFailed == nil:
			obs.othersFailed = classify(err)
		case ok:
			obs.others = append(obs.others, p)
		}
	}

	body, _, ok, err := r.GetContents(ctx, PathPackageJSON)

	switch {
	case err != nil:
		obs.pkgFailed = classify(err)
	case ok:
		present, extends, perr := packageRenovate(body)
		if perr != nil && present {
			obs.pkgFailed = &failure{control.ReasonParseError, control.Unknown}
		}

		// An unparseable package.json is not a Renovate config.
		obs.pkgRenovate, obs.pkgExtends = present, extends
	}

	return obs
}

// Evaluate checks each declared rule.
//
//nolint:gocritic // hugeParam: the Control interface is DESIGN-0031's.
func (c *depControl) Evaluate(ctx context.Context, in control.EvalInput) (control.Evaluation, error) {
	obs := c.observe(ctx, in.Reader)
	results := make([]control.RuleResult, 0, len(c.def.Rules))

	for _, rule := range c.def.Rules {
		var res control.RuleResult

		switch rule.Kind {
		case KindConfigured:
			res = c.configured(&obs)
		case KindExtends:
			res = c.extends(&obs, &in.Repo)
		case KindExclusive:
			res = c.exclusive(&obs)
		}

		res.RuleID = rule.ID
		results = append(results, res)
	}

	return control.Evaluation{Results: results}, nil
}

func (c *depControl) evidence(kind control.RuleKind, detail map[string]any) map[string]any {
	ev := map[string]any{control.EvidenceKind: Name + "." + string(kind), control.EvidenceVersion: evidenceVersion, ParamTool: c.tool}
	for k, v := range detail {
		ev[k] = v
	}

	return ev
}

func framework(f *failure, detail map[string]any) control.RuleResult {
	ev := control.FrameworkEvidence(f.reason)
	for k, v := range detail {
		ev[k] = v
	}

	return control.RuleResult{Status: f.status, Evidence: ev}
}

func (c *depControl) configured(obs *observation) control.RuleResult {
	switch {
	case obs.ownFailed != nil:
		return framework(obs.ownFailed, nil)
	case obs.ownPath != "":
		return control.RuleResult{Status: control.Pass, Evidence: c.evidence(KindConfigured, map[string]any{evidencePath: obs.ownPath})}
	case c.tool == ToolRenovate && obs.pkgRenovate:
		return control.RuleResult{Status: control.Pass, Evidence: c.evidence(KindConfigured, map[string]any{evidencePath: PathPackageJSON})}
	default:
		return framework(&failure{control.ReasonFileMissing, control.Fail}, map[string]any{"locations": c.own})
	}
}

func (c *depControl) extends(obs *observation, repo *control.RepoContext) control.RuleResult {
	preset, err := c.preset.Render(repo.Vars)
	if err != nil {
		return framework(&failure{"template_error", control.Error}, map[string]any{"error": err.Error()})
	}

	var (
		list   []string
		source string
	)

	switch {
	case obs.ownFailed != nil:
		return framework(obs.ownFailed, nil)
	case obs.ownPath != "" && len(obs.ownContent) > maxBytes:
		return framework(&failure{control.ReasonFileTooLarge, control.Unknown}, map[string]any{evidencePath: obs.ownPath})
	case obs.ownPath != "":
		source = obs.ownPath

		list, err = extendsOf(obs.ownContent, isJSON5(obs.ownPath))
		if err != nil && !errors.Is(err, errExtendsNotList) {
			return framework(&failure{control.ReasonParseError, control.Unknown}, map[string]any{evidencePath: obs.ownPath, "error": err.Error()})
		}
	case obs.pkgFailed != nil:
		return framework(obs.pkgFailed, map[string]any{evidencePath: PathPackageJSON})
	case obs.pkgRenovate:
		source, list = PathPackageJSON, obs.pkgExtends
	default:
		return framework(&failure{control.ReasonFileMissing, control.Fail}, map[string]any{"locations": c.own})
	}

	status := control.Fail
	if slices.Contains(list, preset) {
		status = control.Pass
	}

	return control.RuleResult{Status: status, Evidence: c.evidence(KindExtends, map[string]any{evidencePath: source, "preset": preset})}
}

func (c *depControl) exclusive(obs *observation) control.RuleResult {
	switch {
	case obs.othersFailed != nil:
		return framework(obs.othersFailed, nil)
	case c.tool == ToolDependabot && obs.pkgFailed != nil:
		return framework(obs.pkgFailed, map[string]any{evidencePath: PathPackageJSON})
	}

	present := slices.Clone(obs.others)
	if c.tool == ToolDependabot && obs.pkgRenovate {
		present = append(present, PathPackageJSON)
	}

	status := control.Pass
	if len(present) > 0 {
		status = control.Fail
	}

	return control.RuleResult{Status: status, Evidence: c.evidence(KindExclusive, map[string]any{"present": present})}
}

func isJSON5(p string) bool { return strings.HasSuffix(p, ".json5") }

// Remediate fixes what this state allows, in one change set: write the
// chosen tool's config, add the preset to a JSON Renovate config, delete
// the other tool's configs. The rest is Manual or Blocked with a note.
//
//nolint:gocritic // hugeParam: the Control interface is DESIGN-0031's.
func (c *depControl) Remediate(ctx context.Context, in control.RemediationInput) (control.ChangeSet, error) {
	obs := c.observe(ctx, in.Reader)
	plan := &remediation{c: c, obs: &obs, repo: &in.Repo, status: statuses(in.Evaluation)}

	for _, rule := range c.def.Rules {
		if st := plan.status[rule.ID]; st == control.Unknown || st == control.Error {
			plan.block(rule, "it could not be judged; fix the input by hand")
		}
	}

	if err := plan.configured(); err != nil {
		return control.ChangeSet{}, err
	}

	if err := plan.extends(); err != nil {
		return control.ChangeSet{}, err
	}

	plan.exclusive()

	return plan.cs, nil
}

func statuses(ev control.Evaluation) map[control.RuleID]control.RuleStatus {
	out := make(map[control.RuleID]control.RuleStatus, len(ev.Results))
	for _, r := range ev.Results {
		out[r.RuleID] = r.Status
	}

	return out
}

// remediation builds one change set.
type remediation struct {
	c      *depControl
	obs    *observation
	repo   *control.RepoContext
	status map[control.RuleID]control.RuleStatus
	cs     control.ChangeSet
	// written is the config this change set creates, if any.
	written []byte
}

// failing returns the declared rule of kind when it failed.
func (p *remediation) failing(kind control.RuleKind) (control.Rule, bool) {
	r, ok := p.c.rules[kind]

	return r, ok && p.status[r.ID] == control.Fail
}

func (p *remediation) fixed(r control.Rule) { p.cs.Fixed = append(p.cs.Fixed, r.ID) } //nolint:gocritic // hugeParam: small

func (p *remediation) manual(r control.Rule, why string) { //nolint:gocritic // hugeParam: small
	p.cs.Manual = append(p.cs.Manual, r.ID)
	p.cs.Notes = append(p.cs.Notes, fmt.Sprintf("%s/%s: %s", p.c.def.ID, r.ID, why))
}

func (p *remediation) block(r control.Rule, why string) { //nolint:gocritic // hugeParam: small
	p.cs.Blocked = append(p.cs.Blocked, r.ID)
	p.cs.Notes = append(p.cs.Notes, fmt.Sprintf("%s/%s: %s", p.c.def.ID, r.ID, why))
}

func (p *remediation) configured() error {
	rule, ok := p.failing(KindConfigured)
	if !ok {
		return nil
	}

	if !rule.Remediate {
		p.manual(rule, "add the "+p.c.tool+" config by hand")

		return nil
	}

	body, err := p.c.tmpl.Render(p.repo.Vars)
	if err != nil {
		return fmt.Errorf("%s: rendering template: %w", p.c.def.ID, err)
	}

	p.written = []byte(body)
	p.cs.Files = append(p.cs.Files, control.FileChange{Path: p.c.canonical(), Content: p.written, Reason: "missing"})
	p.fixed(rule)

	return nil
}

func (p *remediation) extends() error {
	rule, ok := p.failing(KindExtends)
	if !ok {
		return nil
	}

	preset, err := p.c.preset.Render(p.repo.Vars)
	if err != nil {
		return fmt.Errorf("%s: rendering renovate.extends: %w", p.c.def.ID, err)
	}

	switch {
	case !rule.Remediate:
		p.manual(rule, fmt.Sprintf("add %q to extends by hand", preset))
	case p.written != nil:
		// The new config comes from the template; it fixes extends only
		// if the template carries the preset.
		if list, err := extendsOf(p.written, false); err == nil && slices.Contains(list, preset) {
			p.fixed(rule)
		} else {
			p.manual(rule, fmt.Sprintf("the template does not extend %q; add it by hand", preset))
		}
	case p.obs.ownPath == "":
		p.manual(rule, fmt.Sprintf("package.json's renovate config is not owned; add %q to its extends by hand", preset))
	case isJSON5(p.obs.ownPath):
		p.manual(rule, fmt.Sprintf("%s is JSON5, which is not rewritten; add %q to its extends by hand", p.obs.ownPath, preset))
	default:
		out, err := addExtends(p.obs.ownContent, preset)
		if err != nil {
			p.manual(rule, fmt.Sprintf("%s: %v; add %q to its extends by hand", p.obs.ownPath, err, preset))

			return nil
		}

		p.cs.Files = append(p.cs.Files, control.FileChange{Path: p.obs.ownPath, Content: out, Reason: "rule_failed:" + string(rule.ID)})
		p.fixed(rule)
	}

	return nil
}

func (p *remediation) exclusive() {
	rule, ok := p.failing(KindExclusive)
	if !ok {
		return
	}

	if !rule.Remediate {
		p.manual(rule, "remove the other tool's config by hand: "+strings.Join(p.obs.others, ", "))

		return
	}

	for _, path := range p.obs.others {
		p.cs.Files = append(p.cs.Files, control.FileChange{Path: path, Delete: true, Reason: "rule_failed:" + string(rule.ID)})
	}

	if p.c.tool == ToolDependabot && p.obs.pkgRenovate {
		p.manual(rule, "package.json carries a renovate config, and package.json is not owned; remove its renovate key by hand")

		return
	}

	p.fixed(rule)
}
