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

func counts(c store.StatusCounts) gen.StatusCounts {
	return gen.StatusCounts{Compliant: c.Compliant, NonCompliant: c.NonCompliant, NotApplicable: c.NotApplicable, Unknown: c.Unknown}
}

func repositoryOut(r *store.Repository) gen.Repository {
	return gen.Repository{
		Id: r.ID, Org: r.Org, Name: r.Name, ProviderRepoId: r.ProviderRepoID,
		Active: r.Active, ParkReason: strPtr(r.ParkReason), ParkedAt: r.ParkedAt, DiscoveredAt: r.DiscoveredAt,
		NextDueAt: r.NextDueAt, LastCheckedAt: r.LastCheckedAt,
	}
}

func checkOut(c *store.Check) gen.Check {
	out := gen.Check{
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

	out.ControlId, out.FromStatus, out.ToStatus = optStr(e.ControlID), e.FromStatus, e.ToStatus

	return out, nil
}
