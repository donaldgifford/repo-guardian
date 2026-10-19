package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// stepAPIWrite is the journal kind of one direct API write.
const stepAPIWrite = "api_write"

// The step statuses.
const stepFailed = "failed"

// proposedChange is one resource of an API remediation's proposal.
type proposedChange struct {
	Key    string `json:"key"`
	Op     string `json:"op"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

// api runs one API remediation (IMPL-0030 Phase 5, DESIGN-0032 § API
// remediations): supersede a recommendation whose intent moved, resolve
// one a fresh read finds compliant, record a new recommendation, or
// apply directly, resuming a failed apply from its journal.
func (r *Remediator) api(ctx context.Context, run *remediationRun, direct bool) error {
	t := run.target

	latest, err := r.store.LatestRemediation(ctx, run.in.RepositoryID, run.in.Control)
	if err != nil {
		return fmt.Errorf("latest remediation: %w", err)
	}

	// The open API row: a recommendation, or a direct apply to resume.
	if latest != nil && (latest.Kind != store.RemediationAPI || (latest.State != store.StateRecommended && latest.State != store.StateFailed)) {
		latest = nil
	}

	moved := latest != nil && latest.IntentRevision != t.IntentRevision

	if !t.Due && !moved {
		return nil
	}

	main, mainSHA, err := run.pinDefault(ctx)
	if err != nil || mainSHA == "" {
		return err
	}

	p := &prRun{remediationRun: run, r: r, main: main, mainSHA: mainSHA}

	prop, err := p.propose(ctx, mainSHA)
	if err != nil {
		return err
	}

	if done, err := p.settle(ctx, latest, prop, direct && !moved); done || err != nil {
		return err
	}

	return p.startAPI(ctx, prop, direct)
}

// settle disposes of the open row before anything new starts: resolved
// when a fresh read finds nothing to change, resumed when it is a failed
// direct apply of the same intent, superseded otherwise (D23). done
// reports that nothing new should start.
func (p *prRun) settle(ctx context.Context, open *store.Remediation, prop *proposal, resume bool) (done bool, err error) {
	nothing := prop.compliant || len(prop.cs.API) == 0

	switch {
	case open == nil && nothing:
		return true, p.record(ctx, "", "")
	case open == nil:
		return false, nil
	case nothing:
		if err := p.closeAPI(ctx, open, store.StateResolved, "compliant"); err != nil {
			return true, err
		}

		return true, p.record(ctx, open.OperationKey, "")
	case resume && open.State == store.StateFailed:
		return true, p.apply(ctx, open, prop)
	default:
		return false, p.closeAPI(ctx, open, store.StateSuperseded, "intent_moved")
	}
}

// startAPI records a new API remediation: a recommendation with its
// proposal, or a direct apply, written failed until every step is done.
func (p *prRun) startAPI(ctx context.Context, prop *proposal, direct bool) error {
	t := p.target

	proposal, err := p.proposal(ctx, prop.cs.API)
	if err != nil {
		return err
	}

	state := store.StateRecommended
	if direct {
		state = store.StateFailed
	}

	row, err := p.store.InsertAPIRemediation(ctx, &store.APIRemediation{
		RepositoryID: p.in.RepositoryID, ControlID: p.in.Control, Epoch: t.Epoch, OperationKey: p.in.OperationKey,
		State: state, Intent: t.IntentRevision, Generation: t.Generation, Proposal: proposal,
	})
	if err != nil {
		return fmt.Errorf("record %s remediation: %w", state, err)
	}

	if !direct {
		p.event(ctx, row.ID, store.StateRecommended, "{}")

		return p.record(ctx, row.OperationKey, "")
	}

	return p.apply(ctx, row, prop)
}

// closeAPI ends an open API row in a terminal state under its claim.
func (p *prRun) closeAPI(ctx context.Context, row *store.Remediation, state, reason string) error {
	err := p.claim(ctx, row.OperationKey, func(ctx context.Context, op store.ClaimedOperation) error {
		r := op.Remediation()
		now := p.r.now()
		r.State, r.ClosedAt, r.CloseReason = state, &now, reason

		return op.Update(ctx, &r)
	})
	if err != nil {
		return fmt.Errorf("mark %s %s: %w", p.in.Control, state, err)
	}

	p.event(ctx, row.ID, state, "{}")

	return nil
}

// proposal is the per-resource before and after of changes, read fresh.
// A resource whose read fails is proposed with a null before.
func (p *prRun) proposal(ctx context.Context, changes []control.APIChange) (json.RawMessage, error) {
	out := make([]proposedChange, 0, len(changes))
	rd := p.repo.Reader("")

	for i := range changes {
		ch := &changes[i]
		before, _ := readResource(ctx, rd, ch) //nolint:errcheck // a failed read proposes a null before

		out = append(out, proposedChange{Key: ch.Resource.String(), Op: ch.Op, Before: before, After: ch.Value})
	}

	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("proposal of %s: %w", p.in.Control, err)
	}

	return b, nil
}

// apply writes a direct remediation one resource at a time (IMPL-0030
// 5.3, D14): skip what the journal shows done, read (a failed read is
// Blocked and not attempted, 5.5), fence, write, read back and compare,
// journaling each as api_write with its before and after. All done →
// applied; otherwise the row stays failed with its completed steps and
// the next run resumes.
func (p *prRun) apply(ctx context.Context, row *store.Remediation, prop *proposal) error {
	var finished bool

	err := p.claim(ctx, row.OperationKey, func(ctx context.Context, op store.ClaimedOperation) error {
		r := op.Remediation()

		steps, err := p.store.Steps(ctx, r.ID)
		if err != nil {
			return fmt.Errorf("read journal: %w", err)
		}

		done := make(map[string]bool)

		for i := range steps {
			if steps[i].Kind == stepAPIWrite && steps[i].Status == stepDone {
				done[steps[i].ResourceKey] = true
			}
		}

		var failed []string

		for i := range prop.cs.API {
			ch := &prop.cs.API[i]
			if done[ch.Resource.String()] {
				continue
			}

			if err := p.applyOne(ctx, r.ID, ch); err != nil {
				var held *holdError
				if errors.As(err, &held) || errors.Is(err, ErrStaleEpoch) {
					return err
				}

				failed = append(failed, ch.Resource.String())
			}
		}

		if len(failed) > 0 {
			return fmt.Errorf("direct apply of %s: %d of %d resources not applied: %s",
				p.in.Control, len(failed), len(prop.cs.API), strings.Join(failed, ", "))
		}

		now := p.r.now()
		r.State, r.ClosedAt = store.StateApplied, &now
		finished = true

		return op.Update(ctx, &r)
	})
	if err != nil {
		return err
	}

	if finished {
		p.event(ctx, row.ID, store.StateApplied, "{}")
	}

	return p.record(ctx, row.OperationKey, "")
}

// applyOne is one resource's read, write and read back. A failure is
// journaled. A failed read leaves the write unattempted (Blocked, 5.5);
// a write refused for access holds permission.
func (p *prRun) applyOne(ctx context.Context, remediationID int64, ch *control.APIChange) error {
	rd := p.repo.Reader("")
	step := &store.RemediationStep{Kind: stepAPIWrite, ResourceKey: ch.Resource.String()}

	fail := func(what string, err error) error {
		step.Status, step.Error = stepFailed, what+": "+err.Error()
		p.journal(ctx, remediationID, step)

		return err
	}

	before, err := readResource(ctx, rd, ch)
	if err != nil {
		return fail("read", err)
	}

	step.Before = marshalStep(before)

	if err := p.writeResource(ctx, ch, before); err != nil {
		if errors.Is(err, ErrStaleEpoch) {
			return err
		}

		if err := fail("write", err); accessDenied(err) {
			return &holdError{hold: store.HoldPermission, cause: err}
		}

		return err
	}

	after, err := readResource(ctx, rd, ch)
	if err != nil {
		return fail("read back", err)
	}

	step.After = marshalStep(after)

	if !applied(ch, after) {
		return fail("read back", fmt.Errorf("%s does not hold the written value", ch.Resource))
	}

	p.journal(ctx, remediationID, step)

	return nil
}

// writeResource writes one change through the Writer, fenced
// immediately before the write. before is the resource as just read.
func (p *prRun) writeResource(ctx context.Context, ch *control.APIChange, before any) error {
	w := p.repo.Writer()

	switch ch.Resource.Kind {
	case control.KindSetting:
		return p.writeSetting(ctx, ch)
	case control.KindLabel:
		if ch.Op == control.OpDelete {
			existing, ok := before.(*control.Label)
			if !ok || existing == nil {
				return nil
			}

			if err := p.fence(ctx); err != nil {
				return err
			}

			return w.DeleteLabel(ctx, existing.Name)
		}

		l, ok := ch.Value.(control.Label)
		if !ok {
			return fmt.Errorf("%s: value %T is not a label", ch.Resource, ch.Value)
		}

		if err := p.fence(ctx); err != nil {
			return err
		}

		return w.UpsertLabel(ctx, l)
	case control.KindProperty:
		v := propertyValue(ch.Value)

		if err := p.fence(ctx); err != nil {
			return err
		}

		return w.SetCustomProperties(ctx, map[string]*string{ch.Resource.Key: v})
	case control.KindRuleset:
		rs, ok := ch.Value.(control.Ruleset)
		if !ok || rs.Source != control.RulesetSourceRepository {
			return fmt.Errorf("%s: only a repository-sourced ruleset is written", ch.Resource)
		}

		if err := p.fence(ctx); err != nil {
			return err
		}

		return w.UpsertRuleset(ctx, rs)
	default:
		return fmt.Errorf("%s: no direct write for resource kind %q", ch.Resource, ch.Resource.Kind)
	}
}

// writeSetting writes one repository setting: Dependabot alerts on its
// own endpoint, every other setting as the one non-nil field of an
// update.
func (p *prRun) writeSetting(ctx context.Context, ch *control.APIChange) error {
	w := p.repo.Writer()

	if ch.Resource.Key == control.SettingVulnerabilityAlerts {
		enabled, ok := ch.Value.(bool)
		if !ok {
			return fmt.Errorf("%s: value %T is not a bool", ch.Resource, ch.Value)
		}

		if err := p.fence(ctx); err != nil {
			return err
		}

		return w.SetVulnerabilityAlerts(ctx, enabled)
	}

	b, err := json.Marshal(map[string]any{ch.Resource.Key: ch.Value})
	if err != nil {
		return fmt.Errorf("%s: %w", ch.Resource, err)
	}

	var settings control.RepositorySettings
	if err := json.Unmarshal(b, &settings); err != nil {
		return fmt.Errorf("%s: %w", ch.Resource, err)
	}

	if err := p.fence(ctx); err != nil {
		return err
	}

	return w.UpdateRepository(ctx, settings)
}

// readResource reads ch's resource as it is now: a setting's value, the
// label (nil when absent), the property value (nil when unset) or the
// repository-sourced ruleset (nil when absent).
func readResource(ctx context.Context, rd control.Reader, ch *control.APIChange) (any, error) {
	key := ch.Resource.Key

	switch ch.Resource.Kind {
	case control.KindSetting:
		return readSetting(ctx, rd, key)
	case control.KindLabel:
		labels, err := rd.ListLabels(ctx)
		if err != nil {
			return nil, err
		}

		for i := range labels {
			if strings.EqualFold(labels[i].Name, key) {
				return &labels[i], nil
			}
		}

		return nil, nil //nolint:nilnil // an absent label is a value
	case control.KindProperty:
		props, err := rd.GetCustomProperties(ctx)
		if err != nil {
			return nil, err
		}

		if v := props[key]; v != nil {
			return *v, nil
		}

		return nil, nil //nolint:nilnil // an unset property is a value
	case control.KindRuleset:
		rulesets, err := rd.ListRulesets(ctx)
		if err != nil {
			return nil, err
		}

		for i := range rulesets {
			if rulesets[i].Name == key && rulesets[i].Source == control.RulesetSourceRepository {
				return &rulesets[i], nil
			}
		}

		return nil, nil //nolint:nilnil // an absent ruleset is a value
	default:
		return nil, fmt.Errorf("%s: no read for resource kind %q", ch.Resource, ch.Resource.Kind)
	}
}

func readSetting(ctx context.Context, rd control.Reader, key string) (any, error) {
	if key == control.SettingVulnerabilityAlerts {
		return rd.GetVulnerabilityAlerts(ctx)
	}

	s, err := rd.GetRepository(ctx)
	if err != nil {
		return nil, err
	}

	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}

	var fields map[string]any
	if err := json.Unmarshal(b, &fields); err != nil {
		return nil, err
	}

	return fields[key], nil
}

// applied reports whether after, read back, holds ch.
func applied(ch *control.APIChange, after any) bool {
	switch ch.Resource.Kind {
	case control.KindLabel:
		if ch.Op == control.OpDelete {
			return after == nil
		}

		return labelHolds(after, ch.Value)
	case control.KindProperty:
		want := propertyValue(ch.Value)
		if want == nil {
			return after == nil
		}

		got, ok := after.(string)

		return ok && got == *want
	case control.KindRuleset:
		got, ok := after.(*control.Ruleset)
		want, wok := ch.Value.(control.Ruleset)

		return ok && wok && got != nil && got.Enforcement == want.Enforcement && got.Target == want.Target
	default:
		return jsonEqual(after, ch.Value)
	}
}

// labelHolds reports whether the label read back is the one written.
func labelHolds(after, value any) bool {
	got, ok := after.(*control.Label)
	want, wok := value.(control.Label)

	return ok && wok && got != nil && strings.EqualFold(got.Name, want.Name) && strings.EqualFold(got.Color, want.Color) &&
		got.Description == want.Description
}

// propertyValue is a property change's value; nil clears.
func propertyValue(v any) *string {
	switch v := v.(type) {
	case string:
		return &v
	case *string:
		return v
	default:
		return nil
	}
}

func jsonEqual(a, b any) bool {
	ab, aerr := json.Marshal(a)
	bb, berr := json.Marshal(b)

	if aerr != nil || berr != nil {
		return false
	}

	var av, bv any

	if json.Unmarshal(ab, &av) != nil || json.Unmarshal(bb, &bv) != nil {
		return false
	}

	return reflect.DeepEqual(av, bv)
}

func marshalStep(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}

	return b
}
