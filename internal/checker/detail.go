package checker

import (
	"github.com/donaldgifford/repo-guardian/internal/findings"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
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

// notApplicableForAll is the result of a repository-level skip (empty
// repository, global ignore, policy out of scope): one not_applicable
// outcome, with the given evidence, per enabled rule of every kind. An
// empty result therefore means only an archived/fork park, where the
// findings are deleted (DESIGN-0025 § Engine outcome enrichment).
func notApplicableForAll(p *policy.PolicyConfig, ev findings.Evidence) *CheckResult {
	result := &CheckResult{}
	detail := notApplicable(ev)

	for i := range p.FileRules {
		if p.FileRules[i].IsEnabled() {
			recordRule(result, p.FileRules[i].Name, RuleKindFile, detail)
		}
	}

	for i := range p.SettingRules {
		if p.SettingRules[i].IsEnabled() {
			recordRule(result, p.SettingRules[i].Name, RuleKindSetting, detail)
		}
	}

	for i := range p.BranchProtectionRules {
		if p.BranchProtectionRules[i].IsEnabled() {
			recordRule(result, p.BranchProtectionRules[i].Name, RuleKindBranchProtection, detail)
		}
	}

	return result
}
