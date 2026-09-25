package checker

import (
	"github.com/donaldgifford/repo-guardian/internal/findings"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
)

// outcomeDetail is the v2 enrichment of one rule's verdict: why the rule
// has its status and what evidence supports it (DESIGN-0025 § Engine
// outcome enrichment). It is data only; no action reads it.
type outcomeDetail struct {
	status      findings.Status
	reason      findings.Reason
	evidence    findings.Evidence
	remediation findings.Remediation
	foreignPR   *findings.ForeignPREvidence
}

func compliantDetail() outcomeDetail {
	return outcomeDetail{status: findings.StatusCompliant}
}

// appliedDetail is a setting or ruleset fixed in place during this
// check: the repository complies by the time the check ends.
func appliedDetail() outcomeDetail {
	return outcomeDetail{status: findings.StatusCompliant, remediation: findings.RemediationApplied}
}

// withEvidence builds a detail whose reason is the evidence's own.
func withEvidence(status findings.Status, ev findings.Evidence) outcomeDetail {
	return outcomeDetail{status: status, reason: ev.Reason(), evidence: ev}
}

func nonCompliant(ev findings.Evidence) outcomeDetail {
	return withEvidence(findings.StatusNonCompliant, ev)
}

func notApplicable(ev findings.Evidence) outcomeDetail {
	return withEvidence(findings.StatusNotApplicable, ev)
}

// outcome turns the detail into the rule's recorded outcome.
func (d *outcomeDetail) outcome(name string, kind RuleKind) RuleOutcome {
	return RuleOutcome{
		RuleName:    name,
		Kind:        kind,
		Status:      d.status,
		Reason:      d.reason,
		Remediation: d.remediation,
		Evidence:    d.evidence,
		ForeignPR:   d.foreignPR,
	}
}

// gateClosedDetail records a closed when-gate: not_applicable when the
// referee is simply unsatisfied, unknown when evaluating it failed (the
// gate fails closed, so we could not tell).
func gateClosedDetail(g *gateEvaluator, referee string) outcomeDetail {
	res, _ := g.gateDetail(referee)
	if res.reason == gateReasonError {
		msg := ""
		if res.err != nil {
			msg = findings.Clip(res.err.Error(), findings.ClipRunes)
		}

		return withEvidence(findings.StatusUnknown, findings.GateErrorEvidence{Referee: referee, Error: msg})
	}

	return notApplicable(findings.GateClosedEvidence{Referee: referee})
}

// recordRule records a rule's outcome from its detail.
func recordRule(result *CheckResult, name string, kind RuleKind, detail outcomeDetail) {
	result.record(detail.outcome(name, kind))
}

// markFileRemediation records what is being done about each actionable
// file outcome: dry_run when the engine is in dry-run, else pr_open with
// our reconcile PR when one is already open. It runs after the action
// decision and changes no action; a PR this pass creates is recorded on
// the next check.
func markFileRemediation(result *CheckResult, ourPR *ghclient.PullRequest, dryRun bool) {
	if result == nil || (ourPR == nil && !dryRun) {
		return
	}

	for i := range result.Outcomes {
		o := &result.Outcomes[i]
		if o.Kind != RuleKindFile || !o.Actionable() {
			continue
		}

		if dryRun {
			o.Remediation = findings.RemediationDryRun

			continue
		}

		o.Remediation = findings.RemediationPROpen
		o.PR = &findings.PREvidence{Number: ourPR.Number, URL: ourPR.HTMLURL, CreatedAt: ourPR.CreatedAt}
	}
}
