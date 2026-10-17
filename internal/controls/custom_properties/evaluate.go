package custom_properties

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/catalog"
	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
)

// The reasons this type writes beyond the framework's.
const (
	ReasonUserAccount  = "user_account"
	ReasonOutOfScope   = "out_of_scope"
	ReasonNotComponent = "not_component"
)

// The gates that withhold a property's write.
const (
	GateValue  = "value"  // the derived value fails the property's pattern
	GateSchema = "schema" // the org schema does not define the key, or could not be read
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

// state is everything one evaluation reads, once.
type state struct {
	user bool
	// The catalog: catalogFailed, or present with doc or parseErr.
	catalogFailed *failure
	present       bool
	path          string
	doc           *catalog.Document
	parseErr      error
	// desired is each in-scope property's derived value; nil clears.
	desired map[string]*string
	current map[string]*string
	curErr  error
	schema  map[string]bool
	schErr  error
}

// observe reads the state; count is true for Evaluate alone, so a parse
// failure is counted once per evaluation, not again by Remediate.
func (c *propsControl) observe(ctx context.Context, in *control.EvalInput, count bool) *state {
	st := &state{user: in.Repo.AccountType == control.AccountUser}
	if st.user {
		return st
	}

	path, body, found, err := control.LocateFile(ctx, in.Reader, catalogSpec)
	st.catalogFailed = classify(err)
	st.present, st.path = found, path

	if found && st.catalogFailed == nil {
		if len(body) > maxBytes {
			st.parseErr = catalog.ErrTooLarge
		} else {
			st.doc, st.parseErr = catalog.ParseDocument(body)
		}

		if st.parseErr != nil && count {
			metrics.CatalogParseFailedTotal.WithLabelValues(in.Repo.Org).Inc()
		}
	}

	st.desired = make(map[string]*string, len(c.props))

	if st.doc == nil || st.doc.IsComponent() {
		for _, p := range c.props {
			if p.inScope(in.Repo.Org) {
				st.desired[p.key] = derive(st.doc, p)
			}
		}
	}

	st.current, st.curErr = in.Reader.GetCustomProperties(ctx)

	names, err := in.Reader.OrgPropertySchema(ctx)
	st.schErr = err
	st.schema = make(map[string]bool, len(names))

	for _, n := range names {
		st.schema[n] = true
	}

	return st
}

// derive is the property's normalized value from a valid Component; a
// nil document (no catalog file) and a missing or empty source clear.
func derive(doc *catalog.Document, p *property) *string {
	if doc == nil {
		return nil
	}

	var value string

	if annotation, ok := strings.CutPrefix(p.source, SourceAnnotation); ok {
		value = doc.Entity.Metadata.Annotations[annotation]
	} else if v, _, ok := doc.Lookup(strings.Split(p.source, ".")...); ok {
		value = v
	}

	value = normalize(p.normalize, strings.TrimSpace(value))
	if value == "" {
		return nil
	}

	return &value
}

func normalize(rule, v string) string {
	switch rule {
	case NormalizeEntityRef:
		// [<kind>:][<namespace>/]<name>
		if _, rest, ok := strings.Cut(v, ":"); ok {
			v = rest
		}

		if i := strings.LastIndexByte(v, '/'); i >= 0 {
			v = v[i+1:]
		}

		return v
	case NormalizeLower:
		return strings.ToLower(v)
	default:
		return v
	}
}

// gate returns which gate, if any, withholds writing p's derived value.
func (st *state) gate(p *property) string {
	switch {
	case !p.valid(st.desired[p.key]):
		return GateValue
	case st.schErr != nil || !st.schema[p.key]:
		return GateSchema
	default:
		return ""
	}
}

// Evaluate reads the catalog, the property values and the schema once
// and judges every rule from them.
//
//nolint:gocritic // hugeParam: the Control interface is DESIGN-0031's.
func (c *propsControl) Evaluate(ctx context.Context, in control.EvalInput) (control.Evaluation, error) {
	st := c.observe(ctx, &in, true)
	results := make([]control.RuleResult, 0, len(c.def.Rules))

	for _, r := range c.def.Rules {
		res := c.judge(st, r, in.Repo.Org)
		res.RuleID = r.ID
		results = append(results, res)
	}

	return control.Evaluation{Results: results}, nil
}

// scoped is the rule's properties that apply in org.
func (c *propsControl) scoped(r *control.Rule, org string) []*property {
	key, narrowed := r.Params[ParamProperty].(string)

	var out []*property

	for _, p := range c.props {
		if (!narrowed || p.key == key) && p.inScope(org) {
			out = append(out, p)
		}
	}

	return out
}

func (c *propsControl) judge(st *state, r control.Rule, org string) control.RuleResult { //nolint:gocritic // hugeParam: one rule per call
	props := c.scoped(&r, org)

	switch {
	case st.user:
		return framework(control.NotApplicable, ReasonUserAccount, map[string]any{"account_type": control.AccountUser})
	case len(props) == 0:
		return framework(control.NotApplicable, ReasonOutOfScope, nil)
	case st.catalogFailed != nil:
		return framework(st.catalogFailed.status, st.catalogFailed.reason, nil)
	case st.parseErr != nil:
		return framework(control.Unknown, control.ReasonParseError, map[string]any{"path": st.path, "error": st.parseErr.Error()})
	case st.doc != nil && !st.doc.IsComponent():
		return framework(control.NotApplicable, ReasonNotComponent, map[string]any{"entity_kind": st.doc.Entity.Kind, "path": st.path})
	}

	switch r.Kind {
	case KindMatches:
		return c.matches(st, props)
	case KindValuePattern:
		return c.valuePattern(st, props)
	default:
		return c.defined(st, props)
	}
}

func (c *propsControl) matches(st *state, props []*property) control.RuleResult {
	if f := classify(st.curErr); f != nil {
		return framework(f.status, f.reason, nil)
	}

	var keys, sources, differs, withheld, gates []string

	for _, p := range props {
		keys, sources = append(keys, p.key), append(sources, p.source)

		if equal(st.current[p.key], st.desired[p.key]) {
			continue
		}

		differs = append(differs, p.key)

		if g := st.gate(p); g != "" {
			withheld, gates = append(withheld, p.key), append(gates, g)
		}
	}

	return c.result(KindMatches, len(differs) == 0, map[string]any{
		"catalog_present": st.present, "properties": keys, "sources": sources,
		"differs": differs, "withheld": withheld, "withheld_gates": gates,
	})
}

func (c *propsControl) valuePattern(st *state, props []*property) control.RuleResult {
	var failing []string

	for _, p := range props {
		if !p.valid(st.desired[p.key]) {
			failing = append(failing, p.key)
		}
	}

	return c.result(KindValuePattern, len(failing) == 0, map[string]any{"failing": failing})
}

func (c *propsControl) defined(st *state, props []*property) control.RuleResult {
	if f := classify(st.schErr); f != nil {
		return framework(f.status, f.reason, nil)
	}

	var undefined []string

	for _, p := range props {
		if !st.schema[p.key] {
			undefined = append(undefined, p.key)
		}
	}

	return c.result(KindDefined, len(undefined) == 0, map[string]any{"undefined": undefined})
}

func (*propsControl) result(kind control.RuleKind, pass bool, detail map[string]any) control.RuleResult {
	ev := map[string]any{control.EvidenceKind: Name + "." + string(kind), control.EvidenceVersion: evidenceVersion}
	for k, v := range detail {
		ev[k] = v
	}

	status := control.Fail
	if pass {
		status = control.Pass
	}

	return control.RuleResult{Status: status, Evidence: ev}
}

func framework(status control.RuleStatus, reason string, detail map[string]any) control.RuleResult {
	ev := control.FrameworkEvidence(reason)
	for k, v := range detail {
		ev[k] = v
	}

	return control.RuleResult{Status: status, Evidence: ev}
}

func equal(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}

	return *a == *b
}

// Remediate writes every differing property of a failing matches rule
// whose own gates pass, and withholds the rest with a note: one bad
// value withholds one write. value_pattern and defined are Manual.
//
//nolint:gocritic // hugeParam: the Control interface is DESIGN-0031's.
func (c *propsControl) Remediate(ctx context.Context, in control.RemediationInput) (control.ChangeSet, error) {
	st := c.observe(ctx, &in.EvalInput, false)

	var cs control.ChangeSet

	written := make(map[string]bool)

	for _, res := range in.Evaluation.Results {
		r := c.rule(res.RuleID)

		switch {
		case res.Status == control.Unknown || res.Status == control.Error:
			c.block(&cs, r.ID, "it could not be judged; nothing is written until the input changes")
		case res.Status != control.Fail:
		case r.Kind != KindMatches:
			cs.Manual = append(cs.Manual, r.ID)
			cs.Notes = append(cs.Notes, fmt.Sprintf("%s/%s: %s", c.def.ID, r.ID, manualNote(r.Kind)))
		case !r.Remediate:
			cs.Manual = append(cs.Manual, r.ID)
			cs.Notes = append(cs.Notes, fmt.Sprintf("%s/%s: set the properties by hand", c.def.ID, r.ID))
		default:
			c.sync(&cs, st, &r, in.Repo.Org, written)
		}
	}

	return cs, nil
}

// sync writes one matches rule's differing, ungated properties.
func (c *propsControl) sync(cs *control.ChangeSet, st *state, r *control.Rule, org string, written map[string]bool) {
	var withheld []string

	for _, p := range c.scoped(r, org) {
		want := st.desired[p.key]
		if equal(st.current[p.key], want) {
			continue
		}

		if g := st.gate(p); g != "" {
			withheld = append(withheld, fmt.Sprintf("%s (%s)", p.key, gateNote(g)))

			continue
		}

		if written[p.key] {
			continue
		}

		written[p.key] = true
		cs.API = append(cs.API, control.APIChange{
			Resource: control.Resource{Kind: control.KindProperty, Key: p.key},
			Op:       control.OpSet, Value: want, Reason: "rule_failed:" + string(r.ID),
		})
	}

	if len(withheld) > 0 {
		c.block(cs, r.ID, "withheld "+strings.Join(withheld, ", ")+"; the other properties still sync")

		return
	}

	cs.Fixed = append(cs.Fixed, r.ID)
}

func (c *propsControl) block(cs *control.ChangeSet, id control.RuleID, why string) {
	cs.Blocked = append(cs.Blocked, id)
	cs.Notes = append(cs.Notes, fmt.Sprintf("%s/%s: %s", c.def.ID, id, why))
}

func gateNote(g string) string {
	if g == GateValue {
		return "its value fails the property's pattern"
	}

	return "the org schema does not define it"
}

func manualNote(kind control.RuleKind) string {
	if kind == KindValuePattern {
		return "fix the catalog-info value so it matches the property's pattern"
	}

	return "define the property in the org's custom property schema"
}

func (c *propsControl) rule(id control.RuleID) control.Rule {
	for _, r := range c.def.Rules {
		if r.ID == id {
			return r
		}
	}

	return control.Rule{ID: id}
}
