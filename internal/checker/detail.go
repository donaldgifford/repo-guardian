package checker

import (
	"github.com/donaldgifford/repo-guardian/internal/findings"
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
