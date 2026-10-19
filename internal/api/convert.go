package api

import (
	"encoding/json"
	"fmt"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// optStr returns nil for "", so absent values are omitted on the wire.
func optStr[T ~string](v T) *string {
	if v == "" {
		return nil
	}

	s := string(v)

	return &s
}

func strPtr[T ~string](v *T) *string {
	if v == nil {
		return nil
	}

	s := string(*v)

	return &s
}

func postureOut(p *store.ControlPosture) gen.Posture {
	return gen.Posture{
		Compliant: p.Compliant, NonCompliant: p.NonCompliant, Pending: p.Pending, Stale: p.Stale,
		UnmeasurableAccessDenied: p.UnmeasurableAccessDenied, UnmeasurableSuspended: p.UnmeasurableSuspended,
		UnmeasurableConflict: p.UnmeasurableConflict, NotApplicable: p.NotApplicable, Unknown: p.Unknown,
		Excluded: p.Excluded, Assigned: p.Assigned, Percent: p.Percent, Coverage: p.Coverage,
	}
}

func repositoryOut(r *store.Repository) gen.Repository {
	return gen.Repository{
		Id: r.ID, Org: r.Org, Name: r.Name, ProviderRepoId: r.ProviderRepoID,
		Active: r.Active, ParkReason: strPtr(r.ParkReason), ParkedAt: r.ParkedAt, DiscoveredAt: r.DiscoveredAt,
		NextDueAt: r.NextDueAt, LastEvaluatedAt: r.LastEvaluatedAt,
		PolicyState: optStr(r.PolicyState), PolicyReason: optStr(r.PolicyReason), Evaluable: r.Evaluable, Remediable: r.Remediable,
	}
}

func evaluationOut(c *store.Check) gen.Evaluation {
	out := gen.Evaluation{
		Id: c.ID, Trigger: c.Trigger, Outcome: c.Outcome, PolicyVersion: c.PolicyVersion,
		StartedAt: c.StartedAt, FinishedAt: c.FinishedAt, Error: optStr(c.Error),
	}

	if c.FinishedAt != nil {
		d := c.FinishedAt.Sub(c.StartedAt).Seconds()
		out.DurationSeconds = &d
	}

	return out
}

func eventOut(e *store.Event) (gen.Event, error) {
	out := gen.Event{Source: e.Source, Id: e.ID, OccurredAt: e.OccurredAt}

	switch e.Source {
	case store.EventSourceRepository:
	case store.EventSourceRemediation:
		out.ControlId = optStr(e.ControlID)
	default:
		out.ControlId, out.RuleId, out.FromStatus, out.ToStatus = optStr(e.ControlID), optStr(e.RuleID), e.FromStatus, e.ToStatus

		return out, nil
	}

	out.Kind = optStr(e.Kind)

	if len(e.Detail) > 0 {
		detail := map[string]any{}
		if err := json.Unmarshal(e.Detail, &detail); err != nil {
			return out, fmt.Errorf("event %d detail: %w", e.ID, err)
		}

		out.Detail = &detail
	}

	return out, nil
}

func repositoryControlOut(c *store.RepositoryControl) (gen.RepositoryControl, error) {
	out := gen.RepositoryControl{
		ControlId: c.ControlID, Version: c.Version, Revision: c.Revision, State: c.State, Decision: c.Decision,
		BaselineControl: optStr(c.BaselineControl), Source: c.Source, SourceBlock: optStr(c.SourceBlock),
		Replaced: optStr(c.Replaced), ExcludedBy: optStr(c.ExcludedBy), Reason: optStr(c.Reason), Error: optStr(c.Error),
		RequestedMode: c.RequestedMode, EffectiveMode: c.EffectiveMode, ModeSource: c.ModeSource, ModeReason: optStr(c.ModeReason),
		Epoch: c.Epoch, Bucket: c.Bucket, Status: optStr(c.Status), EvaluatedSha: optStr(c.EvaluatedSHA),
		EvaluatedAt: c.EvaluatedAt, LastChangedAt: c.LastChangedAt, Hold: optStr(c.Hold), HoldSince: c.HoldSince,
		Rules: make([]gen.RuleResult, 0, len(c.Rules)),
	}

	for _, r := range c.Rules {
		var ev gen.Evidence
		if err := ev.UnmarshalJSON(r.Evidence); err != nil {
			return out, fmt.Errorf("%s/%s evidence: %w", c.ControlID, r.RuleID, err)
		}

		out.Rules = append(out.Rules, gen.RuleResult{RuleId: r.RuleID, Status: r.Status, Remediate: r.Remediate, Evidence: ev})
	}

	if c.Remediation != nil {
		rem, err := remediationOut(c.Remediation)
		if err != nil {
			return out, err
		}

		out.Remediation = &rem
	}

	return out, nil
}
