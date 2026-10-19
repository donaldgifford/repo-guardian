package activities

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.temporal.io/sdk/activity"

	"github.com/donaldgifford/repo-guardian/internal/control"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// ErrStaleEpoch means the assignment moved under a remediation run: its
// epoch, state, effective mode, revision (the write set) or the
// Remediation App's reach changed since the run read it (IMPL-0030 3.5).
// The run stops before its next write; the next evaluation decides
// again.
var ErrStaleEpoch = errors.New("assignment moved under the remediation run")

// holdError stops a run with a hold (one of the store.Hold* values),
// which the activity records on the control's result.
type holdError struct {
	hold  string
	cause error
}

func (e *holdError) Error() string {
	if e.cause == nil {
		return "remediation held: " + e.hold
	}

	return "remediation held: " + e.hold + ": " + e.cause.Error()
}

func (e *holdError) Unwrap() error { return e.cause }

// RemediationStore is what the remediation activities read and write
// (IMPL-0030 Phase 3); *postgres.RemediationStore satisfies it, as
// rg_remediator.
type RemediationStore interface {
	RemediationTarget(ctx context.Context, repositoryID int64, controlID string) (*store.RemediationTarget, error)
	SetHold(ctx context.Context, repositoryID int64, controlID string, epoch int64, hold string) (bool, error)
}

// RemediationClients opens a repository as the Remediation App.
type RemediationClients interface {
	Repo(ctx context.Context, installationID int64, owner, name string) (RemediationRepo, error)
}

// RemediationRepo is one repository as the Remediation App reaches it:
// the Writer, and reads at a pinned commit.
type RemediationRepo interface {
	Writer() control.Writer
	Reader(sha string) control.Reader
	Observer() control.PRObserver
}

// remediationAct is one remediation's work on a run whose target is in
// remediate mode: the pull-request path (IMPL-0030 Phase 4) or an API
// apply (Phase 5). It calls run.fence immediately before every write.
type remediationAct func(ctx context.Context, run *remediationRun) error

// Remediator is the remediator worker's activities (IMPL-0030 Phase 3):
// Remediate and ReadRemediationState, as the Remediation App.
type Remediator struct {
	store   RemediationStore
	clients RemediationClients
	bot     ghclient.BotIdentity
	logger  *slog.Logger

	// act does one run's work; nil is the no-op remediation.
	act remediationAct
}

// NewRemediator returns the remediation activities over st as the
// Remediation App, whose bot identity was resolved at startup.
func NewRemediator(st RemediationStore, clients RemediationClients, bot ghclient.BotIdentity, logger *slog.Logger) *Remediator {
	return &Remediator{store: st, clients: clients, bot: bot, logger: logger}
}

// Register registers the remediation activities on the remediator
// worker; nothing else registers them.
func (r *Remediator) Register(reg Registry) {
	reg.RegisterActivityWithOptions(r.Remediate, activity.RegisterOptions{Name: workflows.RemediateActivity})
	reg.RegisterActivityWithOptions(r.ReadRemediationState, activity.RegisterOptions{Name: workflows.ReadRemediationStateActivity})
}

// remediationRun is one Remediate attempt: what it read, and the fence
// every write goes through.
type remediationRun struct {
	in     *workflows.RemediateInput
	target *store.RemediationTarget
	repo   RemediationRepo
	store  RemediationStore
}

// remediates reports whether t is a target a run acts on: an active
// assignment on an active repository in remediate mode that the
// Remediation App can reach. Anything else completes with nothing done;
// resolution's effective-mode downgrade takes an unreachable repository.
func remediates(t *store.RemediationTarget) bool {
	return t.Active && t.State == "active" && t.EffectiveMode == "remediate" && t.Remediable && t.InstallationID != 0
}

// Remediate runs one remediation loop of RemediationWorkflow (IMPL-0030
// 3.1, 3.6). A throttle is a Deferred result, never a sleep here; access
// loss holds permission; a moved assignment, a held operation and a hold
// are results too, so the retry policy sees only operational failures.
func (r *Remediator) Remediate(ctx context.Context, in *workflows.RemediateInput) (*workflows.RemediateResult, error) {
	log := r.logger.With("repository_id", in.RepositoryID, "control", in.Control, "operation_key", in.OperationKey)
	activity.RecordHeartbeat(ctx, "read")

	t, err := r.store.RemediationTarget(ctx, in.RepositoryID, in.Control)
	if err != nil {
		return nil, fmt.Errorf("remediate: %w", err)
	}

	if t == nil {
		return &workflows.RemediateResult{Kind: workflows.RemediationDone}, nil
	}

	out := &workflows.RemediateResult{Kind: workflows.RemediationDone, Start: stateOf(t)}
	if !remediates(t) || r.act == nil {
		return out, nil
	}

	runCtx, usage := ghclient.WithUsage(ctx)
	err = r.run(runCtx, in, t)
	out.Calls, out.Rate = usage.Calls(), toRate(usage.Rate())
	activity.RecordHeartbeat(ctx, "done")

	return r.classify(ctx, log, in, t, out, err)
}

func (r *Remediator) run(ctx context.Context, in *workflows.RemediateInput, t *store.RemediationTarget) error {
	repo, err := r.clients.Repo(ctx, t.InstallationID, t.Org, t.Name)
	if err != nil {
		return err
	}

	return r.act(ctx, &remediationRun{in: in, target: t, repo: repo, store: r.store})
}

// classify turns a run's error into a disposition, in order (IMPL-0030
// 3.6): a throttle from either App defers (a secondary-limit 403 is a
// throttle before it is a permission failure); access loss holds
// permission; then the run's own outcomes.
func (r *Remediator) classify(
	ctx context.Context, log *slog.Logger, in *workflows.RemediateInput, t *store.RemediationTarget, out *workflows.RemediateResult, err error,
) (*workflows.RemediateResult, error) {
	var held *holdError

	switch thr, throttled := ghclient.AsThrottled(err); {
	case err == nil:
		return out, nil
	case throttled:
		out.Kind, out.Until = workflows.RemediationDeferred, deferUntil(time.Now(), thr.ResetAt, in.Deferrals)
		log.Info("rate limit throttled; deferring remediation", "reset_at", thr.ResetAt, "due", out.Until, "deferrals", in.Deferrals)

		return out, nil
	case ghclient.IsAccessDenied(err):
		held = &holdError{hold: store.HoldPermission, cause: err}
	case errors.Is(err, ErrStaleEpoch):
		out.Kind = workflows.RemediationStale
		log.Info("assignment moved under the remediation; stopping")

		return out, nil
	case errors.Is(err, store.ErrOperationHeld):
		out.Kind = workflows.RemediationBusy

		return out, nil
	case errors.As(err, &held):
	default:
		return nil, fmt.Errorf("remediate: %w", err)
	}

	if _, err := r.store.SetHold(ctx, in.RepositoryID, in.Control, t.Epoch, held.hold); err != nil {
		return nil, fmt.Errorf("record hold %s: %w", held.hold, err)
	}

	log.Info("remediation held", "hold", held.hold, "cause", held.cause)
	out.Kind, out.Hold = workflows.RemediationHeld, held.hold

	return out, nil
}

// ReadRemediationState re-reads what RemediationWorkflow compares after a
// loop; a withdrawn control reads as the zero state.
func (r *Remediator) ReadRemediationState(ctx context.Context, in *workflows.ReadRemediationStateInput) (*workflows.RemediationState, error) {
	t, err := r.store.RemediationTarget(ctx, in.RepositoryID, in.Control)
	if err != nil {
		return nil, fmt.Errorf("read remediation state: %w", err)
	}

	if t == nil {
		return &workflows.RemediationState{}, nil
	}

	s := stateOf(t)

	return &s, nil
}

func stateOf(t *store.RemediationTarget) workflows.RemediationState {
	return workflows.RemediationState{Generation: t.Generation, IntentRevision: t.IntentRevision, ObservedHead: t.ObservedHead}
}
