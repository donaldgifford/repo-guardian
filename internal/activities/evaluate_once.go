package activities

import (
	"context"

	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// RepositoryEvaluation is a store-free evaluation of one repository: the
// `repo-guardian evaluate` command's result (IMPL-0029 task 5.9).
type RepositoryEvaluation struct {
	// Kind is CheckChecked, or CheckSkipped (nothing managed, nothing
	// assigned or an empty repository), CheckParked (the Evaluation App
	// lost the repository) or CheckDeferred (throttled).
	Kind   workflows.CheckKind
	Cause  string
	Branch string
	SHA    string
	// Controls are the evaluated controls, in assignment order.
	Controls []store.ControlEvaluation
}

// EvaluateResolved evaluates every active assignment of res against repo
// exactly as Evaluate does on the schedule, with no store: nothing is
// staged and epochs are zero. The Evaluator may be built with a nil
// store.
func (e *Evaluator) EvaluateResolved(
	ctx context.Context, org, name, accountType string, res *policy.Resolution, repo EvalRepo,
) (*RepositoryEvaluation, error) {
	if res.State != policy.StateManaged {
		return &RepositoryEvaluation{Kind: workflows.CheckSkipped}, nil
	}

	t := &store.EvaluationTarget{Org: org, Name: name, Active: true, PolicyState: res.State, AccountType: accountType}
	for i := range res.Assignments {
		a := &res.Assignments[i]
		t.Assignments = append(t.Assignments, store.AssignedControl{
			ControlID: a.Control.String(), ControlVersion: a.Control.Version, Revision: a.Revision, State: a.State,
			RequestedMode: string(a.RequestedMode), EffectiveMode: string(a.EffectiveMode),
		})
	}

	in := &workflows.EvaluateInput{Trigger: workflows.TriggerManual, Scheduled: true}
	log := e.logger.With("owner", org, "repo", name)

	selected := e.selectControls(log, t.Assignments, in)
	if len(selected) == 0 {
		return &RepositoryEvaluation{Kind: workflows.CheckSkipped}, nil
	}

	out, err := e.run(ctx, log, t, repo, selected, in)
	if err != nil {
		return nil, err
	}

	return &RepositoryEvaluation{Kind: out.Kind, Cause: out.Cause, Branch: out.branch, SHA: out.sha, Controls: out.staged}, nil
}
