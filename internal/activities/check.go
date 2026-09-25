package activities

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// causeMaxRunes clips error text carried in workflow history, matching
// v1's last_error clip.
const causeMaxRunes = 1024

// CheckRepo checks one repository and stages its outcomes under the
// check key. It classifies engine errors in v1's order
// (worker.processJob), returning typed results instead of queue
// dispositions:
//
//  1. a throttle is Deferred, returned as a value so it consumes no
//     retry attempt;
//  2. access denied is Parked with findings kept;
//  3. an archived or forked repository is Parked with findings cleared;
//  4. anything else is an error, and the activity retries.
//
// Throttle comes first because a secondary rate limit is also a 403.
func (a *Activities) CheckRepo(ctx context.Context, in workflows.CheckRepoInput) (*workflows.CheckRepoResult, error) {
	repo, err := a.store.GetRepository(ctx, in.RepositoryID)
	if err != nil {
		return nil, fmt.Errorf("get repository %d: %w", in.RepositoryID, err)
	}

	log := a.logger.With(
		"owner", repo.Org,
		"repo", repo.Name,
		"trigger", in.Trigger,
		"installation_id", repo.InstallationID,
		"check_key", in.CheckKey,
	)

	metrics.SetInstallationInfo(repo.InstallationID, repo.Org)

	client, err := a.github.CreateInstallationClient(ctx, repo.InstallationID)
	if err != nil {
		metrics.ErrorsTotal.WithLabelValues("create_install_client", repo.Org).Inc()

		return nil, fmt.Errorf("create installation client for %d: %w", repo.InstallationID, err)
	}

	checkCtx, usage := ghclient.WithUsage(ctx)
	started := time.Now()
	res, err := a.engine.CheckRepo(checkCtx, client, repo.Org, repo.Name)

	out := &workflows.CheckRepoResult{
		CheckKey:       in.CheckKey,
		Calls:          usage.Calls(),
		Rate:           toRate(usage.Rate()),
		InstallationID: repo.InstallationID,
		PolicyVersion:  a.policyVersion,
		Org:            repo.Org,
		Name:           repo.Name,
		StartedAt:      started,
		FinishedAt:     time.Now(),
	}

	if err != nil {
		return classify(log, out, &in, repo.Org, err)
	}

	out.Kind = workflows.CheckChecked
	out.CatalogParseOK = res.CatalogParseOK

	if id := res.Repository; id != nil {
		out.Org, out.Name = id.Owner, id.Name
		if id.ID != 0 {
			out.ProviderRepoID = &id.ID
		}
	}

	if err := a.store.StageCheck(ctx, &store.CheckRecord{
		Key:            in.CheckKey,
		RepositoryID:   in.RepositoryID,
		InstallationID: repo.InstallationID,
		Trigger:        store.Trigger(in.Trigger),
		PolicyVersion:  a.policyVersion,
		StartedAt:      out.StartedAt,
		FinishedAt:     out.FinishedAt,
		Outcomes:       toOutcomes(res.Outcomes),
		CatalogParseOK: res.CatalogParseOK,
	}); err != nil {
		return nil, fmt.Errorf("stage check %s: %w", in.CheckKey, err)
	}

	metrics.ReposCheckedTotal.WithLabelValues(in.Trigger, repo.Org).Inc()
	metrics.CheckDurationSeconds.Observe(out.FinishedAt.Sub(out.StartedAt).Seconds())
	log.Info("check completed", "duration", out.FinishedAt.Sub(out.StartedAt), "calls", out.Calls)

	return out, nil
}

// classify turns an engine error into a Deferred or Parked result, or
// returns it for the activity to retry.
func classify(
	log *slog.Logger,
	out *workflows.CheckRepoResult,
	in *workflows.CheckRepoInput,
	org string,
	err error,
) (*workflows.CheckRepoResult, error) {
	if thr, ok := ghclient.AsThrottled(err); ok {
		out.Kind = workflows.CheckDeferred
		out.Until = deferUntil(time.Now(), thr.ResetAt, in.Deferrals)

		log.Info("rate limit throttled; deferring check",
			"reset_at", thr.ResetAt,
			"due", out.Until,
			"deferrals", in.Deferrals,
			"remaining", thr.Remaining,
			"limit", thr.Limit,
		)

		return out, nil
	}

	if ghclient.IsAccessDenied(err) {
		out.Kind = workflows.CheckParked
		out.ParkReason = workflows.ParkAccessDenied
		out.Cause = store.Truncate(err.Error(), causeMaxRunes)

		return out, nil
	}

	if skip, ok := checker.AsSkipped(err); ok {
		out.Kind = workflows.CheckParked
		out.ParkReason = skip.Reason
		out.ClearFindings = true

		return out, nil
	}

	log.Error("check failed", "error", err)
	metrics.ErrorsTotal.WithLabelValues("check_repo", org).Inc()

	return nil, fmt.Errorf("check %s/%s: %w", out.Org, out.Name, err)
}

// Deferral backoff when a throttle has no usable reset: base 30s
// doubling per consecutive deferral to a 30m cap (IMPL-0022 OQ3).
const (
	backoffBase = 30 * time.Second
	backoffCap  = 30 * time.Minute
)

// deferUntil returns when a throttled check may run again: the reset
// time when it is still ahead, otherwise the backoff for deferrals,
// plus jitter of up to min(delay/4, 60s).
func deferUntil(now, resetAt time.Time, deferrals int) time.Time {
	delay := resetAt.Sub(now)
	if delay <= 0 {
		delay = backoffBase

		for range deferrals {
			delay *= 2
			if delay >= backoffCap {
				delay = backoffCap

				break
			}
		}
	}

	return now.Add(delay + jitter(min(delay/4, time.Minute)))
}

// jitter returns a uniform draw from [0, span). A reader failure means no
// jitter, which is safe for a load-spreading optimization.
func jitter(span time.Duration) time.Duration {
	if span <= 0 {
		return 0
	}

	n, err := rand.Int(rand.Reader, big.NewInt(int64(span)))
	if err != nil {
		return 0
	}

	return time.Duration(n.Int64())
}

func toRate(r *ghclient.RateObservation) *workflows.Rate {
	if r == nil {
		return nil
	}

	return &workflows.Rate{Limit: r.Limit, Remaining: r.Remaining, ResetAt: r.ResetAt, ObservedAt: r.ObservedAt}
}

// toOutcomes maps every engine outcome, including those v1 never
// tracked, to the store's outcome type.
func toOutcomes(in []checker.RuleOutcome) []store.Outcome {
	out := make([]store.Outcome, len(in))

	for i := range in {
		o := &in[i]
		out[i] = store.Outcome{
			RuleKind:    o.Kind,
			RuleName:    o.RuleName,
			Status:      o.Status,
			Reason:      o.Reason,
			Remediation: o.Remediation,
			Evidence:    o.Evidence,
			PR:          o.PR,
			ForeignPR:   o.ForeignPR,
		}
	}

	return out
}
