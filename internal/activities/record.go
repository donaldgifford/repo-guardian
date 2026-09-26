package activities

import (
	"context"
	"fmt"
	"strconv"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// RecordCheck finalizes a Checked result: it applies the outcomes
// CheckRepo staged under the check key in one transaction. A retry after
// commit returns the stored transitions, so it is safe to run twice.
func (a *Activities) RecordCheck(ctx context.Context, in *workflows.RecordCheckInput) (*workflows.RecordCheckResult, error) {
	r := &in.Result

	c := &store.CheckRecord{
		Key:            r.CheckKey,
		RepositoryID:   in.RepositoryID,
		InstallationID: r.InstallationID,
		Trigger:        store.Trigger(in.Trigger),
		PolicyVersion:  r.PolicyVersion,
		StartedAt:      r.StartedAt,
		FinishedAt:     r.FinishedAt,
		CatalogParseOK: r.CatalogParseOK,
		ProviderRepoID: r.ProviderRepoID,
		Org:            r.Org,
		Name:           r.Name,
	}

	if r.Rate != nil {
		c.Rate = &store.RateSnapshot{
			Limit:      r.Rate.Limit,
			Remaining:  r.Rate.Remaining,
			ResetAt:    r.Rate.ResetAt,
			ObservedAt: r.Rate.ObservedAt,
		}
	}

	applied, err := a.store.RecordCheck(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("record check %s: %w", r.CheckKey, err)
	}

	return &workflows.RecordCheckResult{Transitions: len(applied.Transitions), AlreadyFinal: applied.AlreadyFinal}, nil
}

// RecordCheckError records a check that still failed after its retries.
// Findings are untouched: an error learned nothing about the rules.
func (a *Activities) RecordCheckError(ctx context.Context, in *workflows.RecordCheckErrorInput) error {
	if err := a.store.RecordCheckError(ctx, &store.CheckErrorRecord{
		Key:           in.CheckKey,
		RepositoryID:  in.RepositoryID,
		Trigger:       store.Trigger(in.Trigger),
		PolicyVersion: a.policyVersion,
		StartedAt:     in.StartedAt,
		FinishedAt:    in.FinishedAt,
		Err:           store.Truncate(in.Error, causeMaxRunes),
	}); err != nil {
		return fmt.Errorf("record check error %s: %w", in.CheckKey, err)
	}

	return nil
}

// Park takes a repository out of scheduling until discovery sees it
// again. It re-emits v1's parking log line with v1's keys, because the
// E4 evidence panel and operators' Loki rules match on it; job_id
// carries the check key.
//
// ClearFindings is false for access_denied (we learned nothing, so the
// findings stand) and true for archived and fork (no rule applies any
// more), the nil-vs-empty rule from v1's worker.park.
func (a *Activities) Park(ctx context.Context, in *workflows.ParkInput) error {
	repo, err := a.store.GetRepository(ctx, in.RepositoryID)
	if err != nil {
		return fmt.Errorf("get repository %d: %w", in.RepositoryID, err)
	}

	log := a.logger.With(
		"owner", repo.Org,
		"repo", repo.Name,
		"trigger", in.Trigger,
		"installation_id", repo.InstallationID,
		"job_id", in.CheckKey,
	)

	if in.Cause != "" {
		log.Error("parking repository until discovery sees it again", "reason", in.Reason, "error", in.Cause)
	} else {
		log.Info("parking repository until discovery sees it again", "reason", in.Reason)
	}

	if err := a.store.Park(ctx, in.RepositoryID, store.ParkReason(in.Reason), in.ClearFindings); err != nil {
		return fmt.Errorf("park repository %d: %w", in.RepositoryID, err)
	}

	metrics.ReposParkedTotal.WithLabelValues(repo.Org, strconv.FormatInt(repo.InstallationID, 10), in.Reason).Inc()

	return nil
}
