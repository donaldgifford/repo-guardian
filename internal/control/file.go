package control

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/donaldgifford/repo-guardian/internal/template"
)

// evidencePath is the evidence field naming the file judged.
const evidencePath = "path"

// Framework evidence reasons for file controls.
const (
	ReasonFileMissing    = "file_missing"
	ReasonParseError     = "parse_error"
	ReasonFileTooLarge   = "file_too_large"
	ReasonPermission     = "permission"
	ReasonReadError      = "read_error"
	ReasonNotRegularFile = NotRegularFile
)

// FileSpec describes the file a file control owns.
type FileSpec struct {
	// Locations are in the precedence GitHub (or the tool) uses; the
	// first found wins.
	Locations []string
	// Canonical is where a new file is created; one of Locations.
	Canonical string
}

// LocateFile finds the file a tool would actually use: the first of
// spec.Locations that exists. Every location is read, so through an
// EvalReader every one is recorded as a ResourceRead, the absent ones
// included. A failed read at or above the found location is returned,
// because it hides which file the tool uses; one below it is recorded and
// otherwise ignored.
func LocateFile(ctx context.Context, r Reader, spec FileSpec) (path string, content []byte, found bool, err error) {
	for _, loc := range spec.Locations {
		body, _, ok, rerr := r.GetContents(ctx, loc)

		switch {
		case found:
			// Read only to record it.
		case rerr != nil:
			err = fmt.Errorf("reading %s: %w", loc, rerr)
			found = true // stop looking; the error decides
		case ok:
			path, content, found = loc, body, true
		}
	}

	if err != nil {
		return "", nil, false, err
	}

	return path, content, found, nil
}

// ParseError is a parse failure with the 1-based line it was found on,
// zero when unknown. A FileType's Parse returns it so the evidence can
// name the line.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
	}

	return e.Msg
}

// FileType is what a file control type implements; NewFileControl turns
// it into Evaluate and Remediate (DESIGN-0031 File controls).
type FileType[M any] interface {
	// Parse turns content, at most the adapter's size cap, into the
	// model. A *ParseError names the line.
	Parse(content []byte) (M, error)
	// Check evaluates one rule against the model.
	Check(m M, rule Rule) RuleResult
	// Fix edits the model so the given failing remediable rules pass,
	// and returns the ids it fixed; the rest become Manual. It must
	// change only what those rules require.
	Fix(m M, failing []Rule) (M, []RuleID, error)
	// Render returns the model's content, byte-identical to the parsed
	// input wherever Fix changed nothing.
	Render(m M) ([]byte, error)
}

// FileControlConfig is everything NewFileControl needs.
type FileControlConfig[M any] struct {
	Def  Definition
	Spec FileSpec
	Type FileType[M]
	// MaxBytes is the parser size cap; required. A larger file is
	// unknown{file_too_large}, never parsed.
	MaxBytes int
	// Template is rendered to Spec.Canonical when no location has a
	// file. Required when any rule is remediable.
	Template *template.Compiled
}

// fileControl is the Control NewFileControl returns.
type fileControl[M any] struct {
	cfg   FileControlConfig[M]
	owned []Resource
}

// NewFileControl builds the Control for a file control type. It fails
// on a missing size cap, an empty or non-canonical FileSpec, and a
// remediable rule with no template.
func NewFileControl[M any](cfg FileControlConfig[M]) (Control, error) {
	if cfg.MaxBytes <= 0 {
		return nil, fmt.Errorf("%s: file control needs a size cap", cfg.Def.ID)
	}

	if len(cfg.Spec.Locations) == 0 {
		return nil, fmt.Errorf("%s: file control needs at least one location", cfg.Def.ID)
	}

	owned := make([]Resource, 0, len(cfg.Spec.Locations))
	canonicalListed := false

	for _, loc := range cfg.Spec.Locations {
		res, err := Canonical(KindFile, loc)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", cfg.Def.ID, err)
		}

		if res.Key != loc {
			return nil, fmt.Errorf("%s: location %q is not canonical (%q)", cfg.Def.ID, loc, res.Key)
		}

		canonicalListed = canonicalListed || loc == cfg.Spec.Canonical
		owned = append(owned, res)
	}

	if !canonicalListed {
		return nil, fmt.Errorf("%s: canonical path %q is not a location", cfg.Def.ID, cfg.Spec.Canonical)
	}

	for _, r := range cfg.Def.Rules {
		if r.Remediate && cfg.Template == nil {
			return nil, fmt.Errorf("%s/%s: remediable rule needs a template", cfg.Def.ID, r.ID)
		}
	}

	return &fileControl[M]{cfg: cfg, owned: owned}, nil
}

func (c *fileControl[M]) ID() ID { return c.cfg.Def.ID }

// Owns is every location: a generic file control on any of them collides
// at policy load.
func (c *fileControl[M]) Owns() []Resource { return append([]Resource(nil), c.owned...) }

// Reads is every location too, so a file appearing above the one read
// invalidates the result (D12).
func (c *fileControl[M]) Reads() []Resource { return append([]Resource(nil), c.owned...) }

func (c *fileControl[M]) Rules() []Rule { return append([]Rule(nil), c.cfg.Def.Rules...) }

// observation is what one pinned state says about the file.
type observation[M any] struct {
	path    string
	content []byte
	found   bool
	model   M
	// reason is set when the file could not be judged: a read failure,
	// an oversized file or a parse error.
	reason string
	status RuleStatus
	detail map[string]any
}

func (c *fileControl[M]) observe(ctx context.Context, r Reader) observation[M] {
	path, content, found, err := LocateFile(ctx, r, c.cfg.Spec)

	switch {
	case errors.Is(err, ErrPermission):
		return observation[M]{reason: ReasonPermission, status: Unknown}
	case errors.Is(err, ErrNotRegularFile):
		return observation[M]{reason: ReasonNotRegularFile, status: Unknown}
	case err != nil:
		return observation[M]{reason: ReasonReadError, status: Error}
	case !found:
		return observation[M]{}
	case len(content) > c.cfg.MaxBytes:
		return observation[M]{
			path: path, found: true, reason: ReasonFileTooLarge, status: Unknown,
			detail: map[string]any{evidencePath: path, "bytes": len(content), "max_bytes": c.cfg.MaxBytes},
		}
	}

	m, err := c.cfg.Type.Parse(content)
	if err != nil {
		detail := map[string]any{evidencePath: path, "error": err.Error()}

		var pe *ParseError
		if errors.As(err, &pe) && pe.Line > 0 {
			detail["line"] = pe.Line
		}

		return observation[M]{path: path, found: true, reason: ReasonParseError, status: Unknown, detail: detail}
	}

	return observation[M]{path: path, content: content, found: true, model: m}
}

// Evaluate locates the file and checks every rule: absent → every rule
// fails with file_missing; unreadable or unparseable → every rule takes
// the observation's status with its reason; present → Type.Check.
//
//nolint:gocritic // hugeParam: the Control interface is DESIGN-0031's.
func (c *fileControl[M]) Evaluate(ctx context.Context, in EvalInput) (Evaluation, error) {
	obs := c.observe(ctx, in.Reader)

	results := make([]RuleResult, 0, len(c.cfg.Def.Rules))

	for _, rule := range c.cfg.Def.Rules {
		switch {
		case obs.reason != "":
			results = append(results, RuleResult{RuleID: rule.ID, Status: obs.status, Evidence: evidence(obs.reason, obs.detail)})
		case !obs.found:
			results = append(results, RuleResult{
				RuleID: rule.ID, Status: Fail,
				Evidence: evidence(ReasonFileMissing, map[string]any{"locations": c.cfg.Spec.Locations}),
			})
		default:
			res := c.cfg.Type.Check(obs.model, rule)
			res.RuleID = rule.ID
			results = append(results, res)
		}
	}

	return Evaluation{Results: results}, nil
}

// evidence is FrameworkEvidence(reason) with detail merged in.
func evidence(reason string, detail map[string]any) map[string]any {
	ev := FrameworkEvidence(reason)
	for k, v := range detail {
		ev[k] = v
	}

	return ev
}

// Remediate turns the failing rules of in.Evaluation into a change set
// against the same pinned state: an absent file gets the template at
// Canonical; a present file is edited by Type.Fix at the path it was
// found; an unreadable or unparseable file is never written and every
// rule is Blocked.
//
//nolint:gocritic // hugeParam: the Control interface is DESIGN-0031's.
func (c *fileControl[M]) Remediate(ctx context.Context, in RemediationInput) (ChangeSet, error) {
	failing, remediable := c.failing(in.Evaluation)

	obs := c.observe(ctx, in.Reader)

	if obs.reason != "" {
		var cs ChangeSet
		for _, rule := range c.cfg.Def.Rules {
			cs.Blocked = append(cs.Blocked, rule.ID)
			cs.Notes = append(
				cs.Notes,
				fmt.Sprintf("%s/%s: the file could not be read or parsed (%s); fix it by hand", c.cfg.Def.ID, rule.ID, obs.reason),
			)
		}

		return cs, nil
	}

	if len(failing) == 0 {
		return ChangeSet{}, nil
	}

	var cs ChangeSet

	switch {
	case len(remediable) == 0:
	case !obs.found:
		body, err := c.cfg.Template.Render(in.Repo.Vars)
		if err != nil {
			return ChangeSet{}, fmt.Errorf("%s: rendering template: %w", c.cfg.Def.ID, err)
		}

		cs.Files = []FileChange{{Path: c.cfg.Spec.Canonical, Content: []byte(body), Reason: "missing"}}
		cs.Fixed = ruleIDs(remediable)
	default:
		if err := c.fix(obs, remediable, &cs); err != nil {
			return ChangeSet{}, err
		}
	}

	for _, rule := range failing {
		if !slices.Contains(cs.Fixed, rule.ID) {
			cs.Manual = append(cs.Manual, rule.ID)
			cs.Notes = append(cs.Notes, fmt.Sprintf("%s/%s: not fixable automatically; fix it by hand", c.cfg.Def.ID, rule.ID))
		}
	}

	return cs, nil
}

// fix applies Type.Fix to a present file and adds the edit to cs. An
// edit that leaves the bytes unchanged fixes nothing.
func (c *fileControl[M]) fix(obs observation[M], remediable []Rule, cs *ChangeSet) error {
	m, fixed, err := c.cfg.Type.Fix(obs.model, remediable)
	if err != nil {
		return fmt.Errorf("%s: fixing %s: %w", c.cfg.Def.ID, obs.path, err)
	}

	if len(fixed) == 0 {
		return nil
	}

	out, err := c.cfg.Type.Render(m)
	if err != nil {
		return fmt.Errorf("%s: rendering %s: %w", c.cfg.Def.ID, obs.path, err)
	}

	if bytes.Equal(out, obs.content) {
		return nil
	}

	cs.Files = []FileChange{{Path: obs.path, Content: out, Reason: "rule_failed:" + string(fixed[0])}}
	cs.Fixed = fixed

	return nil
}

// failing returns the declared rules that failed in ev, and the
// remediable subset.
func (c *fileControl[M]) failing(ev Evaluation) (failing, remediable []Rule) {
	for _, rule := range c.cfg.Def.Rules {
		for _, res := range ev.Results {
			if res.RuleID != rule.ID || res.Status != Fail {
				continue
			}

			failing = append(failing, rule)
			if rule.Remediate {
				remediable = append(remediable, rule)
			}
		}
	}

	return failing, remediable
}

func ruleIDs(rules []Rule) []RuleID {
	ids := make([]RuleID, 0, len(rules))
	for _, r := range rules {
		ids = append(ids, r.ID)
	}

	return ids
}
