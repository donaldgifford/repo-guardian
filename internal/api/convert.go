package api

import (
	"encoding/json"
	"fmt"
	"time"

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

func counts(c store.StatusCounts) gen.StatusCounts {
	return gen.StatusCounts{Compliant: c.Compliant, NonCompliant: c.NonCompliant, NotApplicable: c.NotApplicable, Unknown: c.Unknown}
}

// storedEvidence is a finding's evidence column: the reason's fields
// with any PR under "pr".
type storedEvidence map[string]json.RawMessage

// findingOut maps a finding onto the wire. The stored evidence carries
// no reason; it is injected here so the oneOf can discriminate, and the
// PR is lifted out to the finding's pr field.
func findingOut(f *store.APIFinding) (gen.Finding, error) {
	out := gen.Finding{
		RepositoryId: f.RepositoryID, Org: f.Org, Repository: f.Repository,
		RuleKind: string(f.Kind), RuleName: f.RuleName, Status: string(f.Status), Reason: optStr(f.Reason),
		Remediation: string(f.Remediation), StatusSince: f.StatusSince, LastEvaluatedAt: f.LastEvaluatedAt,
		PrStale: f.PRStale,
	}

	ev := storedEvidence{}
	if len(f.Evidence) > 0 {
		if err := json.Unmarshal(f.Evidence, &ev); err != nil {
			return out, fmt.Errorf("finding %d %s/%s evidence: %w", f.RepositoryID, f.Kind, f.RuleName, err)
		}
	}

	if raw, ok := ev["pr"]; ok {
		var pr gen.FindingPR
		if err := json.Unmarshal(raw, &pr); err != nil {
			return out, fmt.Errorf("finding %d %s/%s pr: %w", f.RepositoryID, f.Kind, f.RuleName, err)
		}

		out.Pr = &pr

		delete(ev, "pr")
	}

	if f.Reason == "" {
		return out, nil
	}

	reason, err := json.Marshal(f.Reason)
	if err != nil {
		return out, err
	}

	ev["reason"] = reason

	b, err := json.Marshal(ev)
	if err != nil {
		return out, err
	}

	var e gen.Evidence
	if err := e.UnmarshalJSON(b); err != nil {
		return out, err
	}

	out.Evidence = &e

	return out, nil
}

func findingsOut(in []store.APIFinding) ([]gen.Finding, error) {
	out := make([]gen.Finding, 0, len(in))

	for i := range in {
		f, err := findingOut(&in[i])
		if err != nil {
			return nil, err
		}

		out = append(out, f)
	}

	return out, nil
}

func repositoryOut(r *store.Repository) gen.Repository {
	return gen.Repository{
		Id: r.ID, Org: r.Org, Name: r.Name, ProviderRepoId: r.ProviderRepoID, InstallationId: r.InstallationID,
		Active: r.Active, ParkReason: strPtr(r.ParkReason), ParkedAt: r.ParkedAt, DiscoveredAt: r.DiscoveredAt,
		NextDueAt: r.NextDueAt, LastCheckedAt: r.LastCheckedAt, LastCheckOutcome: r.LastCheckOutcome,
		PolicyVersion: r.PolicyVersion,
	}
}

func installationOut(i *store.InstallationStatus, now time.Time) gen.Installation {
	out := gen.Installation{
		Id: i.InstallationID, Account: i.Account, SuspendedAt: i.SuspendedAt, RemovedAt: i.RemovedAt,
		RateLimit: i.RateLimit, RateRemaining: i.RateRemaining, RateResetAt: i.RateResetAt, RateObservedAt: i.RateObservedAt,
	}

	if i.RateObservedAt != nil {
		age := int(now.Sub(*i.RateObservedAt).Seconds())
		out.RateSnapshotAgeSeconds = &age
	}

	return out
}

func checkOut(c *store.Check) gen.Check {
	out := gen.Check{
		Id: c.ID, Trigger: string(c.Trigger), Outcome: c.Outcome, PolicyVersion: c.PolicyVersion,
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

	if e.Source == store.EventSourceRepository {
		out.Kind = optStr(e.RepositoryKind)

		if len(e.Detail) > 0 {
			detail := map[string]any{}
			if err := json.Unmarshal(e.Detail, &detail); err != nil {
				return out, fmt.Errorf("event %d detail: %w", e.ID, err)
			}

			out.Detail = &detail
		}

		return out, nil
	}

	out.RuleKind, out.RuleName = optStr(e.Kind), optStr(e.RuleName)
	out.FromStatus, out.ToStatus = strPtr(e.FromStatus), strPtr(e.ToStatus)
	out.FromReason, out.ToReason = strPtr(e.FromReason), strPtr(e.ToReason)
	out.FromRemediation, out.ToRemediation = strPtr(e.FromRemediation), strPtr(e.ToRemediation)

	return out, nil
}

func historyOut(s *store.ComplianceSnapshot) gen.HistoryPoint {
	c := s.Counts()

	return gen.HistoryPoint{
		Org: s.Org, Kind: string(s.Kind), Rule: s.RuleName, SnapshotAt: s.SnapshotAt,
		Counts: counts(c), CompliantPercent: store.CompliantPercent(c),
	}
}

func policyRuleOut(r *store.PolicyRule) gen.PolicyRule {
	out := gen.PolicyRule{Kind: string(r.Kind), Name: r.Name, Description: optStr(r.Description), CheckMode: optStr(r.CheckMode)}

	if len(r.Scope) > 0 {
		scope := r.Scope
		out.Scope = &scope
	}

	if len(r.Ignore) > 0 {
		ignore := r.Ignore
		out.Ignore = &ignore
	}

	return out
}
