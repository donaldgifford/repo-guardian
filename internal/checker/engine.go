// Package checker implements the core check-and-PR engine for repo-guardian.
// It inspects repositories for missing configuration files and creates pull
// requests to add sensible defaults.
package checker

import (
	"context"
	"fmt"
	"log/slog"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/reconciler"
	"github.com/donaldgifford/repo-guardian/internal/rules"
)

const (
	// BranchName is the deterministic branch name used by repo-guardian.
	BranchName = "repo-guardian/add-missing-files"

	// PRTitle is the title used for repo-guardian pull requests.
	PRTitle = "chore: add missing repo configuration files"
)

// Engine is the core checker that evaluates repositories against the
// policy-driven rule set and creates PRs for missing files.
type Engine struct {
	templates    *rules.TemplateStore
	logger       *slog.Logger
	skipForks    bool
	skipArchived bool
	dryRun       bool

	policy             *policy.PolicyConfig
	compiledAssertions map[string][]policy.CompiledAssertion

	// ruleReconcilers maps rule key (type:name) to built reconcilers.
	// Reconcilers run after file checks pass.
	ruleReconcilers map[string][]reconciler.Reconciler
}

// NewEngine creates a policy-driven checker Engine. Reconcilers are
// built from the policy config using the supplied registry.
func NewEngine(
	cfg *policy.PolicyConfig,
	templates *rules.TemplateStore,
	logger *slog.Logger,
	registry *reconciler.Registry,
) (*Engine, error) {
	compiled := make(map[string][]policy.CompiledAssertion)
	ruleReconcilers := make(map[string][]reconciler.Reconciler)

	for i := range cfg.FileRules {
		r := &cfg.FileRules[i]
		key := r.Type + ":" + r.Name

		if len(r.Assertions) > 0 {
			ca, err := policy.CompileAssertions(r.Assertions)
			if err != nil {
				return nil, fmt.Errorf("compiling assertions for rule %q: %w", r.Name, err)
			}

			compiled[key] = ca
		}

		if len(r.Reconcilers) > 0 && registry != nil {
			recs := make([]reconciler.Reconciler, 0, len(r.Reconcilers))

			for j := range r.Reconcilers {
				rec, err := registry.Build(r.Reconcilers[j])
				if err != nil {
					return nil, fmt.Errorf("building reconciler for rule %q: %w", r.Name, err)
				}

				recs = append(recs, rec)
			}

			ruleReconcilers[key] = recs
		}
	}

	return &Engine{
		templates:          templates,
		logger:             logger,
		skipForks:          cfg.Guardian.SkipForks,
		skipArchived:       cfg.Guardian.SkipArchived,
		dryRun:             cfg.Guardian.DryRun,
		policy:             cfg,
		compiledAssertions: compiled,
		ruleReconcilers:    ruleReconcilers,
	}, nil
}

// CheckRepo evaluates a single repository against the policy and creates
// a PR if any file rules are actionable.
//
// The returned *CheckResult carries the per-rule verdicts the caller
// persists as posture state (IMPL-0023 Phase 1). Error paths return
// (nil, err): a check that blew up mid-way has no trustworthy verdict
// for the rules it never reached, and writing a partial set would let
// delete-not-in reconciliation drop rows that are merely unvisited.
//
// The non-durable skip paths — empty repo, global ignore, and
// out-of-policy-scope — return an *empty* result rather than nil. That
// is deliberate: an empty evaluated set reconciles every existing row
// for the repo away, which is exactly right for a repo that just left
// the fleet's scope. It should stop counting against compliance, not
// freeze at its last verdict forever.
//
// Durable skips (archived, fork) instead return a *SkippedError so the
// worker parks the row (INV-0015). Their posture must ALSO be cleared,
// for the same reason — but that is the worker's call, since it is what
// decides disposition; see Pool.park.
func (e *Engine) CheckRepo(
	ctx context.Context,
	client ghclient.Client,
	owner, repo string,
) (*CheckResult, error) {
	log := e.logger.With("owner", owner, "repo", repo)

	repoInfo, err := client.GetRepository(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("getting repository info: %w", err)
	}

	// Authoritative skip checks — the scheduler pre-filters as an
	// optimization, but the engine is the single source of truth.
	if reason, durable := e.skipReason(repoInfo); reason != "" {
		log.Info("skipping repository", "reason", reason, "durable", durable)

		if durable {
			// Surfaced as an error so the worker can park the row. The
			// repo is otherwise re-enqueued and re-fetched every
			// freshness cycle for as long as it exists (INV-0015).
			//
			// Returns a nil result, per this method's error contract.
			// The empty-result-clears-posture decision for a parked repo
			// belongs to the worker, which is what classifies the
			// disposition — see Pool.park.
			return nil, &SkippedError{Reason: reason}
		}

		return &CheckResult{}, nil
	}

	// Global ignore list short-circuits all rule evaluation.
	if e.policy.IgnoreList.Matches(owner + "/" + repo) {
		log.Info("repository matched global ignore list, skipping all rules")
		metrics.IgnoredTotal.WithLabelValues("global", owner).Inc()

		return &CheckResult{}, nil
	}

	openPRs, err := client.ListOpenPullRequests(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("listing open PRs: %w", err)
	}

	return e.checkRepoWithPolicy(ctx, log, client, owner, repo, repoInfo.DefaultRef, openPRs)
}

// findOurPR returns the open repo-guardian PR (head ref ==
// BranchName) if one exists, otherwise nil.
func findOurPR(openPRs []*ghclient.PullRequest) *ghclient.PullRequest {
	for _, pr := range openPRs {
		if pr.Head == BranchName {
			return pr
		}
	}

	return nil
}
