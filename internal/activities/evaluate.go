package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.temporal.io/sdk/activity"

	"github.com/donaldgifford/repo-guardian/internal/control"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/template"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// EvalStore is the part of the controls store the evaluator activities
// use. *postgres.ControlsStore satisfies it.
type EvalStore interface {
	EvaluationTarget(ctx context.Context, repoID int64) (*store.EvaluationTarget, error)
	StageEvaluation(ctx context.Context, st *store.EvaluationStage) error
	RecordEvaluation(ctx context.Context, checkKey string) (store.RecordOutcome, error)
	Park(ctx context.Context, repoID int64, reason string) error
}

// EvalPolicy is the loaded controls policy. *policy.Snapshot satisfies
// it.
type EvalPolicy interface {
	Version() string
	Control(id control.ID) control.Control
	Definition(id control.ID) (control.Definition, bool)
	Revision(id control.ID) string
}

// EvalRepo is one repository as the Evaluation App sees it.
type EvalRepo interface {
	// Head returns the default branch and its head commit. An empty sha
	// is an empty repository. A lost repository is an error
	// ghclient.IsAccessDenied recognises.
	Head(ctx context.Context) (branch, sha string, err error)
	// Reader returns a Reader with file reads pinned to sha.
	Reader(sha string) control.Reader
}

// EvalClients opens repositories with the Evaluation App's installation
// tokens.
type EvalClients interface {
	Repo(ctx context.Context, installationID int64, owner, name string) (EvalRepo, error)
}

// Evaluator holds the controls evaluation activities, registered on the
// evaluation queue only. It holds no per-evaluation state.
type Evaluator struct {
	store  EvalStore
	policy EvalPolicy
	github EvalClients
	logger *slog.Logger
	now    func() time.Time
}

// NewEvaluator returns the evaluator activities over the policy the
// worker loaded.
func NewEvaluator(st EvalStore, pol EvalPolicy, gh EvalClients, logger *slog.Logger) *Evaluator {
	return &Evaluator{store: st, policy: pol, github: gh, logger: logger, now: time.Now}
}

// Evaluate evaluates the selected controls of one repository at its
// pinned default-branch head and stages the payload under the check key
// (DESIGN-0032 § Evaluation). It classifies like the rc's check did: a throttle
// anywhere is Deferred and stages nothing; losing the repository at the
// pin is Parked; a repository that is not managed, has nothing selected
// or is empty is Skipped. A 403 on one endpoint is the control's own
// unknown{reason=permission} and parks nothing.
func (e *Evaluator) Evaluate(ctx context.Context, in *workflows.EvaluateInput) (*workflows.EvaluateResult, error) {
	t, err := e.store.EvaluationTarget(ctx, in.RepositoryID)
	if err != nil {
		return nil, fmt.Errorf("evaluate repository %d: %w", in.RepositoryID, err)
	}

	res, err := e.evaluate(ctx, in, t)
	countEvaluation(t.Org, res, err)

	return res, err
}

// countEvaluation counts one Evaluate attempt in evaluations_total. A
// deferral is not an evaluation; a parked repository is a skip.
func countEvaluation(org string, res *workflows.EvaluateResult, err error) {
	outcome := evalOutcomeError

	switch {
	case err != nil:
	case res.Kind == workflows.CheckChecked:
		outcome = evalOutcomeEvaluated
	case res.Kind == workflows.CheckDeferred:
		return
	default:
		outcome = evalOutcomeSkipped
	}

	metrics.EvaluationsTotal.WithLabelValues(org, outcome).Inc()
}

// The evaluations_total outcomes (DESIGN-0032 § Metrics).
const (
	evalOutcomeEvaluated = "evaluated"
	evalOutcomeError     = "error"
	evalOutcomeSkipped   = "skipped"
)

func (e *Evaluator) evaluate(ctx context.Context, in *workflows.EvaluateInput, t *store.EvaluationTarget) (*workflows.EvaluateResult, error) {
	// Already parked: the loop ends and discovery starts it again.
	if !t.Active {
		return &workflows.EvaluateResult{Kind: workflows.CheckParked}, nil
	}

	log := e.logger.With("owner", t.Org, "repo", t.Name, "trigger", in.Trigger, "check_key", in.CheckKey)
	skipped := &workflows.EvaluateResult{Kind: workflows.CheckSkipped}

	if t.PolicyState != policy.StateManaged || t.InstallationID == 0 {
		return skipped, nil
	}

	selected := e.selectControls(log, t.Assignments, in)
	if len(selected) == 0 {
		return skipped, nil
	}

	repo, err := e.github.Repo(ctx, t.InstallationID, t.Org, t.Name)
	if err != nil {
		return nil, fmt.Errorf("open %s/%s: %w", t.Org, t.Name, err)
	}

	evalCtx, usage := ghclient.WithUsage(ctx)
	started := e.now()
	out, err := e.run(evalCtx, log, t, repo, selected, in)

	if out != nil {
		out.Calls, out.Rate = usage.Calls(), toRate(usage.Rate())
	}

	// The evaluator is rate_limit_remaining's producer;
	// RateLimitNearExhaustion reads nothing else.
	if out != nil && out.Rate != nil {
		metrics.RateLimitRemaining.WithLabelValues(strconv.FormatInt(t.InstallationID, 10)).Set(float64(out.Rate.Remaining))
	}

	if err != nil {
		return nil, err
	}

	if out.Kind != workflows.CheckChecked {
		return &out.EvaluateResult, nil
	}

	if err := e.store.StageEvaluation(ctx, &store.EvaluationStage{
		CheckKey: in.CheckKey, RepositoryID: in.RepositoryID, Trigger: in.Trigger,
		PolicyVersion: e.policy.Version(), StartedAt: started, Controls: out.staged,
	}); err != nil {
		return nil, fmt.Errorf("stage %s: %w", in.CheckKey, err)
	}

	log.Info("evaluation staged", "controls", len(out.staged), "calls", out.Calls)

	return &out.EvaluateResult, nil
}

// evaluation is the activity result plus what run staged.
type evaluation struct {
	workflows.EvaluateResult

	staged      []store.ControlEvaluation
	branch, sha string
}

// run pins the head and evaluates every selected control through one
// EvalReader, so a resource two controls read is fetched once.
func (e *Evaluator) run(
	ctx context.Context, log *slog.Logger, t *store.EvaluationTarget, repo EvalRepo, selected []selection, in *workflows.EvaluateInput,
) (*evaluation, error) {
	branch, sha, err := repo.Head(ctx)
	if err != nil {
		return classifyEval(log, in, err)
	}

	if sha == "" {
		return &evaluation{EvaluateResult: workflows.EvaluateResult{Kind: workflows.CheckSkipped}}, nil
	}

	watch := &throttleWatch{base: repo.Reader(sha)}
	reader := control.NewEvalReader(watch, e.now)
	rc := control.RepoContext{
		Org: t.Org, Name: t.Name, DefaultBranch: branch, SHA: sha, AccountType: t.AccountType,
		Vars: template.Vars{Common: template.Common{Owner: t.Org, Repo: t.Name, DefaultBranch: branch}, Org: t.Org},
	}

	out := &evaluation{EvaluateResult: workflows.EvaluateResult{Kind: workflows.CheckChecked}, branch: branch, sha: sha}

	for i := range selected {
		ce, err := evaluateControl(ctx, &selected[i], &rc, reader)
		if err != nil {
			if thr := watch.throttled(); thr != nil {
				return classifyEval(log, in, thr)
			}

			metrics.EvaluationErrorsTotal.WithLabelValues(t.Org, selected[i].assigned.ControlID).Inc()

			return nil, err
		}

		ce.EvaluatedSHA = sha
		out.staged = append(out.staged, ce)
	}

	// A throttled read became an error result inside the control; the
	// evaluation is incomplete, so it is deferred and nothing is staged.
	if thr := watch.throttled(); thr != nil {
		return classifyEval(log, in, thr)
	}

	out.Controls = len(out.staged)

	for i := range out.staged {
		if hasErrorRule(&out.staged[i]) {
			metrics.EvaluationErrorsTotal.WithLabelValues(t.Org, out.staged[i].ControlID).Inc()
		}
	}

	return out, nil
}

// hasErrorRule reports whether any rule of ce ended in an error status.
func hasErrorRule(ce *store.ControlEvaluation) bool {
	for i := range ce.Rules {
		if ce.Rules[i].Status == string(control.Error) {
			return true
		}
	}

	return false
}

// classifyEval turns a GitHub error into Deferred or Parked, or returns
// it for the activity to retry. Throttle comes first: a secondary rate
// limit is also a 403.
func classifyEval(log *slog.Logger, in *workflows.EvaluateInput, err error) (*evaluation, error) {
	if thr, ok := ghclient.AsThrottled(err); ok {
		until := deferUntil(time.Now(), thr.ResetAt, in.Deferrals)
		log.Info("rate limit throttled; deferring evaluation", "reset_at", thr.ResetAt, "due", until, "deferrals", in.Deferrals)

		return &evaluation{EvaluateResult: workflows.EvaluateResult{Kind: workflows.CheckDeferred, Until: until}}, nil
	}

	if ghclient.IsAccessDenied(err) {
		return &evaluation{EvaluateResult: workflows.EvaluateResult{
			Kind: workflows.CheckParked, ParkReason: workflows.ParkAccessDenied, Cause: store.Truncate(err.Error(), causeMaxRunes),
		}}, nil
	}

	return nil, fmt.Errorf("evaluate: %w", err)
}

// selection is one assignment chosen for this evaluation.
type selection struct {
	assigned store.AssignedControl
	control  control.Control
	def      control.Definition
}

// selectControls picks the active assignments this evaluation runs.
// Conflict and excluded assignments never run (a conflict is reported as
// unmeasurable{reason=conflict} by the compliance read). An assignment
// whose revision is not the loaded policy's waits for the rollout to
// re-resolve it. Every control runs on the schedule; an unknown change
// or a policy change runs every control that has a file to read, since
// API resources carry no push signal and are evaluated on the schedule
// only (DESIGN-0031 D12); a push runs the controls whose Owns() ∪
// Reads() match a changed path.
func (e *Evaluator) selectControls(log *slog.Logger, assigned []store.AssignedControl, in *workflows.EvaluateInput) []selection {
	var out []selection

	for i := range assigned {
		a := &assigned[i]
		if a.State != policy.AssignmentActive {
			continue
		}

		// The store keeps the slug and the version in their own columns.
		id := control.ID{Slug: a.ControlID, Version: a.ControlVersion}
		c := e.policy.Control(id)

		if c == nil || e.policy.Revision(id) != a.Revision {
			log.Info("assignment awaits re-resolution; not evaluated", "control", a.ControlID, "revision", a.Revision)

			continue
		}

		if !selects(c, in) {
			continue
		}

		def, _ := e.policy.Definition(id)
		out = append(out, selection{assigned: *a, control: c, def: def})
	}

	return out
}

// selects reports whether in selects c.
func selects(c control.Control, in *workflows.EvaluateInput) bool {
	if in.Scheduled {
		return true
	}

	var files []control.Resource

	for _, r := range slices.Concat(c.Owns(), c.Reads()) {
		if r.Kind == control.KindFile || r.Kind == control.KindTree {
			files = append(files, r)
		}
	}

	if in.All {
		return len(files) > 0
	}

	for _, p := range in.Paths {
		changed, err := control.Canonical(control.KindFile, p)
		if err != nil {
			continue
		}

		for _, r := range files {
			if matchesPath(r, changed.Key) {
				return true
			}
		}
	}

	return false
}

// matchesPath reports whether a changed file path touches r.
func matchesPath(r control.Resource, path string) bool {
	if r.Kind == control.KindFile {
		return r.Key == path
	}

	return r.Key == "." || strings.HasPrefix(path, r.Key+"/")
}

// evaluateControl runs one control and builds its staged payload: the
// reads of its own resources only, so another control's file never moves
// its fingerprint, then Validate, StatusOf and Fingerprint.
func evaluateControl(ctx context.Context, s *selection, rc *control.RepoContext, reader *control.EvalReader) (store.ControlEvaluation, error) {
	c, a := s.control, &s.assigned

	ev, err := c.Evaluate(ctx, control.EvalInput{Repo: *rc, Reader: reader})
	if err != nil {
		return store.ControlEvaluation{}, fmt.Errorf("evaluate %s: %w", a.ControlID, err)
	}

	ev.Reads = readsOf(c, reader.Reads())

	if ev, err = control.Validate(c, ev); err != nil {
		return store.ControlEvaluation{}, fmt.Errorf("validate %s: %w", a.ControlID, err)
	}

	out := store.ControlEvaluation{
		ControlID: a.ControlID, ControlVersion: a.ControlVersion, Revision: a.Revision, Epoch: a.Epoch,
		Status: string(control.StatusOf(ev)), Fingerprint: control.Fingerprint(a.Revision, ev), Apply: s.def.Apply,
		Rules: make([]store.RuleEvaluation, 0, len(ev.Results)),
	}

	remediate := make(map[control.RuleID]bool)
	for _, r := range c.Rules() {
		remediate[r.ID] = r.Remediate
	}

	for _, res := range ev.Results {
		re, err := ruleEvaluation(res, remediate[res.RuleID])
		if err != nil {
			return store.ControlEvaluation{}, fmt.Errorf("%s/%s: %w", a.ControlID, res.RuleID, err)
		}

		out.Remediable = out.Remediable || (re.Remediate && res.Status == control.Fail)
		out.Rules = append(out.Rules, re)
	}

	return out, nil
}

// ruleEvaluation lifts the evidence kind and version Validate guaranteed
// out of the evidence map.
func ruleEvaluation(res control.RuleResult, remediate bool) (store.RuleEvaluation, error) {
	evidence, err := json.Marshal(res.Evidence)
	if err != nil {
		return store.RuleEvaluation{}, fmt.Errorf("encode evidence: %w", err)
	}

	kind := fmt.Sprint(res.Evidence[control.EvidenceKind])

	var version int

	switch v := res.Evidence[control.EvidenceVersion].(type) {
	case int:
		version = v
	case int64:
		version = int(v)
	case float64:
		version = int(v)
	}

	return store.RuleEvaluation{
		RuleID: string(res.RuleID), Status: string(res.Status), Remediate: remediate,
		Evidence: evidence, EvidenceKind: kind, EvidenceVersion: version,
	}, nil
}

// readsOf keeps the reads of c's own resources, plus a failed listing of
// a kind c reads (recorded under the "*" key: which names it would have
// returned is unknown).
func readsOf(c control.Control, all []control.ResourceRead) []control.ResourceRead {
	mine := make(map[control.Resource]bool)
	kinds := make(map[string]bool)

	for _, r := range slices.Concat(c.Owns(), c.Reads()) {
		mine[r], kinds[r.Kind] = true, true
	}

	var out []control.ResourceRead

	for _, rr := range all {
		if mine[rr.Resource] || (rr.Resource.Key == "*" && kinds[rr.Resource.Kind]) {
			out = append(out, rr)
		}
	}

	return out
}

// throttleWatch passes reads through and remembers the first throttle,
// which a control would otherwise fold into an error result.
type throttleWatch struct {
	base control.Reader

	mu  sync.Mutex
	thr error
}

var _ control.Reader = (*throttleWatch)(nil)

func (w *throttleWatch) throttled() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.thr
}

func (w *throttleWatch) see(err error) {
	if thr, ok := ghclient.AsThrottled(err); !ok || thr == nil {
		return
	}

	w.mu.Lock()
	if w.thr == nil {
		w.thr = err
	}
	w.mu.Unlock()
}

func watched[T any](w *throttleWatch, v T, err error) (T, error) {
	w.see(err)

	return v, err
}

func (w *throttleWatch) GetContents(ctx context.Context, path string) ([]byte, string, bool, error) {
	content, sha, found, err := w.base.GetContents(ctx, path)
	w.see(err)

	return content, sha, found, err
}

func (w *throttleWatch) ListDirectory(ctx context.Context, path string) ([]string, error) {
	v, err := w.base.ListDirectory(ctx, path)

	return watched(w, v, err)
}

func (w *throttleWatch) GetRepository(ctx context.Context) (control.RepositorySettings, error) {
	v, err := w.base.GetRepository(ctx)

	return watched(w, v, err)
}

func (w *throttleWatch) ListRulesets(ctx context.Context) ([]control.Ruleset, error) {
	v, err := w.base.ListRulesets(ctx)

	return watched(w, v, err)
}

func (w *throttleWatch) GetCustomProperties(ctx context.Context) (map[string]*string, error) {
	v, err := w.base.GetCustomProperties(ctx)

	return watched(w, v, err)
}

func (w *throttleWatch) OrgPropertySchema(ctx context.Context) ([]string, error) {
	v, err := w.base.OrgPropertySchema(ctx)

	return watched(w, v, err)
}

func (w *throttleWatch) ListLabels(ctx context.Context) ([]control.Label, error) {
	v, err := w.base.ListLabels(ctx)

	return watched(w, v, err)
}

func (w *throttleWatch) GetVulnerabilityAlerts(ctx context.Context) (bool, error) {
	v, err := w.base.GetVulnerabilityAlerts(ctx)

	return watched(w, v, err)
}

// RecordEvaluation commits a staged evaluation, fenced per control on
// its assignment epoch. A recorded check key is AlreadyFinal.
func (e *Evaluator) RecordEvaluation(ctx context.Context, in *workflows.RecordEvaluationInput) (*workflows.RecordEvaluationResult, error) {
	out, err := e.store.RecordEvaluation(ctx, in.CheckKey)
	if err != nil {
		return nil, fmt.Errorf("record evaluation %s: %w", in.CheckKey, err)
	}

	return &workflows.RecordEvaluationResult{
		AlreadyFinal: out.AlreadyFinal, Recorded: len(out.Recorded), Discarded: len(out.Discarded), Changed: len(out.Changed),
	}, nil
}

// ParkRepository parks a repository the evaluator lost (DESIGN-0030):
// access_denied keeps its results, because access loss must never
// improve posture. It logs the line the E4 evidence tier matches.
func (e *Evaluator) ParkRepository(ctx context.Context, in *workflows.ParkRepositoryInput) error {
	if err := e.store.Park(ctx, in.RepositoryID, in.Reason); err != nil {
		return fmt.Errorf("park repository %d: %w", in.RepositoryID, err)
	}

	log := e.logger.With("repository_id", in.RepositoryID, "check_key", in.CheckKey)
	if in.Cause != "" {
		log.ErrorContext(ctx, "parking repository until discovery sees it again", "reason", in.Reason, "error", in.Cause)
	} else {
		log.InfoContext(ctx, "parking repository until discovery sees it again", "reason", in.Reason)
	}

	return nil
}

// Register registers the evaluator activities under their workflows
// package names, on the evaluation worker only.
func (e *Evaluator) Register(r Registry) {
	r.RegisterActivityWithOptions(e.Evaluate, activity.RegisterOptions{Name: workflows.EvaluateActivity})
	r.RegisterActivityWithOptions(e.RecordEvaluation, activity.RegisterOptions{Name: workflows.RecordEvaluationActivity})
	r.RegisterActivityWithOptions(e.ParkRepository, activity.RegisterOptions{Name: workflows.ParkRepositoryActivity})
}
