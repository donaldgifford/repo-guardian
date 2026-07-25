package checker

import (
	"context"
	"log/slog"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// Gate-closed reasons, used both as the RuleGateClosedTotal metric's
// `reason` label value and in structured logs. reason="error" is the
// alertable signal that API trouble is silently suppressing rules;
// reason="not_satisfied" is the ordinary "referee not yet satisfied"
// path (IMPL-0019 / DESIGN-0020 Decision 3).
const (
	gateReasonNotSatisfied = "not_satisfied"
	gateReasonError        = "error"
)

// gateResult is the memoized outcome of evaluating one referee rule for
// the current repo-check. A closed gate carries the reason so the
// primary pass can pick the RuleGateClosedTotal bucket without
// re-deriving it.
type gateResult struct {
	open   bool
	reason string // "" when open; gateReasonNotSatisfied or gateReasonError when closed
}

// gateEvaluator answers "is this rule's when-gate open?" for a single
// repo-check, memoizing each referee's satisfaction so the two engine
// passes (findActionableRules, runReconcilers) share at most one referee
// content evaluation per referenced rule.
//
// It is constructed inside checkRepoWithPolicy and threaded into both
// passes; it MUST NOT be stored on *Engine, which is shared across
// worker-pool goroutines — the memo map is per-repo-check mutable state
// and would race if hung off the shared engine.
type gateEvaluator struct {
	engine *Engine
	client ghclient.Client
	owner  string
	repo   string

	// byName indexes every file rule (enabled or not) so referee lookup
	// cannot miss under a valid, load-validated policy.
	byName map[string]*policy.FileRuleConfig

	// memo caches each referee's gate outcome, keyed by referee rule name.
	// Closed and error outcomes are cached too (not just successes) so both
	// passes observe identical gate state within a repo-check even if a
	// referee evaluation would resolve differently on a retry.
	memo map[string]gateResult
}

// newGateEvaluator builds a per-repo-check gate evaluator over the
// engine's policy file rules.
func newGateEvaluator(e *Engine, client ghclient.Client, owner, repo string) *gateEvaluator {
	byName := make(map[string]*policy.FileRuleConfig, len(e.policy.FileRules))
	for i := range e.policy.FileRules {
		r := &e.policy.FileRules[i]
		byName[r.Name] = r
	}

	return &gateEvaluator{
		engine: e,
		client: client,
		owner:  owner,
		repo:   repo,
		byName: byName,
		memo:   make(map[string]gateResult),
	}
}

// gateOpen reports whether rule may fire, plus the closed-reason for the
// caller's metric. An ungated rule always passes. A gated rule passes
// only when its referee is satisfied on the default branch; a referee
// evaluation error fails the gate closed (never open), because deleting
// or skipping a file under unknown referee state is unsafe (DESIGN-0020
// fail-closed contract). The result is memoized per referee.
//
// gateOpen deliberately performs no counter side-effects: the primary
// pass increments RuleGateClosedTotal at its call site and the reconciler
// pass stays silent, preserving the file-rule double-iteration contract.
func (g *gateEvaluator) gateOpen(ctx context.Context, log *slog.Logger, rule *policy.FileRuleConfig) (bool, string) {
	if rule.When == nil {
		return true, ""
	}

	ref := rule.When.RuleSatisfied

	if cached, ok := g.memo[ref]; ok {
		return cached.open, cached.reason
	}

	result := g.evaluateReferee(ctx, log, ref)
	g.memo[ref] = result

	return result.open, result.reason
}

// gateStatus reports the memoized gate outcome for a rule, for the
// reconcile-log wording (IMPL-0019 task 2.6). It returns closed=false for
// an ungated rule or one whose referee was not evaluated this repo-check;
// it never triggers a fresh evaluation.
func (g *gateEvaluator) gateStatus(rule *policy.FileRuleConfig) (closed bool, referee, reason string) {
	if rule.When == nil {
		return false, "", ""
	}

	referee = rule.When.RuleSatisfied

	if res, ok := g.memo[referee]; ok && !res.open {
		return true, referee, res.reason
	}

	return false, referee, ""
}

// evaluateReferee computes the gate outcome for a referee rule name. It
// runs only on a memo miss, so its logs fire at most once per referee per
// repo-check.
func (g *gateEvaluator) evaluateReferee(ctx context.Context, log *slog.Logger, ref string) gateResult {
	referee, ok := g.byName[ref]
	if !ok || !referee.IsEnabled() {
		// Load-time validateWhenGates guarantees the referee exists, is a
		// file rule, and is enabled — reaching here means that invariant
		// was violated. Fail closed rather than panic in a worker goroutine.
		log.Error("gate referee missing or disabled — policy invariant violated", "referee", ref)

		return gateResult{open: false, reason: gateReasonError}
	}

	satisfied, err := g.engine.ruleSatisfiedOnDefault(ctx, log, g.client, g.owner, g.repo, referee)
	if err != nil {
		log.Warn("gate referee evaluation failed; failing closed", "referee", ref, "err", err)

		return gateResult{open: false, reason: gateReasonError}
	}

	if !satisfied {
		return gateResult{open: false, reason: gateReasonNotSatisfied}
	}

	return gateResult{open: true, reason: ""}
}

// ruleSatisfiedOnDefault reports whether referee is satisfied on the
// repository's default branch. It is evaluateRule's per-check-mode logic
// MINUS the open-PR short-circuit (an in-flight PR must not make a rule
// look satisfied) and MINUS scope/ignore (content-only: the referee is a
// named bundle of paths/assertions), then inverted — evaluateRule returns
// "actionable"; satisfied is its negation.
//
// It is a read-only method on the shared *Engine (it needs the compiled
// assertions and templates); the per-repo-check memo lives on
// gateEvaluator, not here, so this stays goroutine-safe.
func (e *Engine) ruleSatisfiedOnDefault(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	referee *policy.FileRuleConfig,
) (bool, error) {
	existingPath, err := findExistingFile(ctx, client, owner, repo, referee.Paths)
	if err != nil {
		return false, err
	}

	switch referee.CheckMode() {
	case policy.CheckExists:
		return existingPath != "", nil
	case policy.CheckContains:
		actionable, err := e.evaluateContains(ctx, log, client, owner, repo, referee, existingPath)

		return !actionable, err
	case policy.CheckExact:
		actionable, err := e.evaluateExact(ctx, log, client, owner, repo, referee, existingPath)

		return !actionable, err
	case policy.CheckAbsent:
		// Satisfied iff none of the forbidden paths exist. Computed inline
		// rather than via evaluateExists so the gate is independent of the
		// evaluateAbsent arm's landing order.
		return existingPath == "", nil
	default:
		return existingPath != "", nil
	}
}
