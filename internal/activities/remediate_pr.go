package activities

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // git object ids are sha1
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/control"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/template"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// The remediation_steps kinds the pull-request path journals.
const (
	stepBranchCreated = "branch_created"
	stepCommitted     = "committed"
	stepReverted      = "reverted"
	stepPRCreated     = "pr_created"
	stepPRUpdated     = "pr_updated"
	stepPRRebased     = "pr_rebased"
	stepPRClosed      = "pr_closed"
	stepDone          = "done"
)

// StickyMarker is row 1 of the one comment repo-guardian keeps on a
// remediation PR, edited in place (IMPL-0030 OQ9).
const StickyMarker = "<!-- repo-guardian:control:v1 -->"

// DefaultReservationTTL bounds a pull-request slot reservation that
// never opened its PR when RemediatorConfig.ReservationTTL is zero;
// maintenance expires it after.
const DefaultReservationTTL = time.Hour

// prOpen is GitHub's state of an open pull request.
const prOpen = "open"

// closeCompliantReason is the close_reason of an artifact main no longer
// needs.
const closeCompliantReason = "compliant"

// branchName is the remediation branch of a control (DESIGN-0032 D4).
func branchName(slug string) string { return workflows.RemediationBranchPrefix + slug }

// pullRequest runs one remediation of a file control by pull request
// (IMPL-0030 Phase 4, DESIGN-0032 § What one run does).
// With wf set the control's API changes travel as its workflow file
// (apply = "workflow", IMPL-0030 5.4).
func (r *Remediator) pullRequest(ctx context.Context, run *remediationRun, wf control.WorkflowApply) error {
	t := run.target
	if !t.Due {
		return nil
	}

	latest, err := r.store.LatestRemediation(ctx, run.in.RepositoryID, run.in.Control)
	if err != nil {
		return fmt.Errorf("latest remediation: %w", err)
	}

	branch := branchName(run.in.Control)

	head, exists, err := run.repo.Observer().GetRef(ctx, branch)
	if err != nil {
		return fmt.Errorf("find %s: %w", branch, err)
	}

	main, mainSHA, err := run.pinDefault(ctx)
	if err != nil || mainSHA == "" {
		return err
	}

	pr := &prRun{remediationRun: run, r: r, branch: branch, main: main, mainSHA: mainSHA, workflow: wf}

	switch {
	case latest != nil && latest.State == store.StateOpen && latest.PRNumber != 0 && latest.Branch == branch:
		if !exists {
			// The evaluator observes the PR closed with its branch.
			return nil
		}

		return pr.tracked(ctx, latest)
	case latest != nil && latest.State == store.StateReserved && latest.Branch == branch:
		return pr.progress(ctx, latest, nil)
	case exists:
		return pr.untracked(ctx, latest, head)
	default:
		return pr.open(ctx)
	}
}

// pinDefault reads the default branch and pins its head.
func (run *remediationRun) pinDefault(ctx context.Context) (branch, sha string, err error) {
	settings, err := run.repo.Reader("").GetRepository(ctx)
	if err != nil {
		return "", "", fmt.Errorf("read default branch: %w", err)
	}

	if settings.DefaultBranch == nil || *settings.DefaultBranch == "" {
		return "", "", errors.New("read default branch: none reported")
	}

	sha, _, err = run.repo.Observer().GetRef(ctx, *settings.DefaultBranch)
	if err != nil {
		return "", "", fmt.Errorf("pin %s: %w", *settings.DefaultBranch, err)
	}

	return *settings.DefaultBranch, sha, nil
}

// prRun is one pull-request remediation.
type prRun struct {
	*remediationRun

	r             *Remediator
	branch        string
	main, mainSHA string

	// workflow turns the API changes into the control's workflow file.
	workflow control.WorkflowApply
}

// proposal is a fresh evaluation and the change it proposes.
type proposal struct {
	compliant bool
	ev        control.Evaluation
	cs        control.ChangeSet
}

// open is the no-branch case: a fresh read of main, then a reserved slot
// carrying the intent, then the branch, the commit and the PR.
func (p *prRun) open(ctx context.Context) error {
	prop, err := p.propose(ctx, p.mainSHA)
	if err != nil {
		return err
	}

	// Compliant on a fresh read, or nothing this control can fix: no PR,
	// and the generation is acknowledged (DESIGN-0031 D14).
	if prop.compliant || len(prop.cs.Fixed) == 0 || len(prop.cs.Files) == 0 {
		return p.record(ctx, "", "")
	}

	t := p.target

	rem, err := p.store.ReserveSlot(ctx, &store.SlotRequest{
		RepositoryID: p.in.RepositoryID, ControlID: p.in.Control, Epoch: t.Epoch, OperationKey: p.in.OperationKey,
		Intent: t.IntentRevision, Generation: t.Generation, TTL: p.r.reservationTTL,
		BaseSHA: p.mainSHA, Branch: p.branch, ExpectedHead: p.mainSHA,
	})
	if errors.Is(err, store.ErrPRCap) {
		return &holdError{hold: store.HoldPRCap}
	}

	if err != nil {
		return fmt.Errorf("reserve slot: %w", err)
	}

	p.outcome(resultReserved)

	return p.progress(ctx, rem, prop)
}

// progress drives a reserved row to an open PR from its journal: create
// the branch at base_sha, commit at expected_head, open the PR. A run
// that crashed part-way resumes where the journal and the branch say
// (IMPL-0030 4.14).
func (p *prRun) progress(ctx context.Context, rem *store.Remediation, prop *proposal) error {
	var processed string

	err := p.claim(ctx, rem.OperationKey, func(ctx context.Context, op store.ClaimedOperation) error {
		row := op.Remediation()

		steps, err := p.store.Steps(ctx, row.ID)
		if err != nil {
			return fmt.Errorf("read journal: %w", err)
		}

		if prop == nil {
			if prop, err = p.propose(ctx, row.BaseSHA); err != nil {
				return err
			}
		}

		committed, err := p.commitOnce(ctx, &row, steps, prop)
		if err != nil {
			return err
		}

		processed = committed

		return p.openPR(ctx, op, &row, prop)
	})
	if err != nil {
		return err
	}

	return p.record(ctx, rem.OperationKey, processed)
}

// commitOnce creates the branch and makes the run's commit unless the
// journal shows them done, and returns the commit.
func (p *prRun) commitOnce(ctx context.Context, row *store.Remediation, steps []store.RemediationStep, prop *proposal) (string, error) {
	w := p.repo.Writer()

	head, exists, err := p.repo.Observer().GetRef(ctx, p.branch)
	if err != nil {
		return "", fmt.Errorf("find %s: %w", p.branch, err)
	}

	if committed := lastCommitted(steps); committed != "" {
		if head != committed {
			return "", &holdError{hold: store.HoldConflict}
		}

		return committed, nil
	}

	if !exists {
		if err := p.fence(ctx); err != nil {
			return "", err
		}

		if err := w.CreateRef(ctx, p.branch, row.BaseSHA); err != nil {
			return "", fmt.Errorf("create %s: %w", p.branch, err)
		}

		p.journal(ctx, row.ID, &store.RemediationStep{Kind: stepBranchCreated, CommittedSHA: row.BaseSHA})
		head = row.BaseSHA
	}

	if head != row.ExpectedHead {
		return "", &deferError{reason: "branch moved before the commit"}
	}

	if err := p.fence(ctx); err != nil {
		return "", err
	}

	sha, err := w.Commit(ctx, p.branch, row.ExpectedHead, prop.cs.Files, commitMessage(p, prop))
	if errors.Is(err, control.ErrExpectedHeadMismatch) {
		return "", &deferError{reason: "expected head mismatch"}
	}

	if err != nil {
		return "", p.commitError(err)
	}

	p.journalFiles(ctx, row.ID, stepCommitted, sha, prop.cs.Files)

	return sha, nil
}

// openPR opens the PR, or adopts the one the App already opened for the
// branch (a replayed create), and marks the row open.
func (p *prRun) openPR(ctx context.Context, op store.ClaimedOperation, row *store.Remediation, prop *proposal) error {
	rendered, err := p.render(prop)
	if err != nil {
		return err
	}

	w := p.repo.Writer()

	if err := p.fence(ctx); err != nil {
		return err
	}

	pr, err := w.CreatePullRequest(ctx, p.branch, p.main, rendered.Title, rendered.Body)
	if err != nil {
		// A create replayed after GitHub opened the PR fails with 422
		// "already exists": adopt the App's own PR from the branch.
		adopted, aerr := p.ownPR(ctx)
		if aerr != nil || adopted == nil {
			return fmt.Errorf("create pull request: %w", err)
		}

		pr = adopted
	}

	p.journal(ctx, row.ID, &store.RemediationStep{Kind: stepPRCreated, PRNumber: pr.Number, CommittedSHA: pr.HeadSHA})

	if err := p.fence(ctx); err != nil {
		return err
	}

	if err := w.AddPRLabels(ctx, pr.Number, rendered.Labels); err != nil {
		return fmt.Errorf("label pull request: %w", err)
	}

	row.State, row.PRNumber, row.PRURL, row.ReservedUntil = store.StateOpen, pr.Number, pr.URL, nil

	if err := op.Update(ctx, row); err != nil {
		return fmt.Errorf("mark open: %w", err)
	}

	p.outcome(resultOpened)

	return nil
}

// ownPR is the open PR on the branch authored by the Remediation App's
// bot user from this repository, or nil.
func (p *prRun) ownPR(ctx context.Context) (*control.PullRequest, error) {
	prs, err := p.repo.Observer().ListPullRequests(ctx, p.branch)
	if err != nil {
		return nil, fmt.Errorf("list pull requests: %w", err)
	}

	for i := range prs {
		if p.isOwn(&prs[i]) {
			return &prs[i], nil
		}
	}

	return nil, nil //nolint:nilnil // no PR of ours is not an error
}

// isOwn is adoption by authenticated identity (DESIGN-0032 AR-0032-02):
// an open PR on exactly this branch, authored by the bot's user id with
// type Bot, whose head repository is this one. Commit author text is
// never consulted.
func (p *prRun) isOwn(pr *control.PullRequest) bool {
	return pr.State == prOpen && pr.HeadRef == p.branch && pr.AuthorID == p.r.bot.ID && pr.AuthorID != 0 &&
		pr.AuthorType == ghclient.UserTypeBot && pr.HeadRepoID != 0 && pr.HeadRepoID == p.target.ProviderRepoID
}

// untracked is a branch with no tracked PR: adopt the App's own open PR,
// or hold stale_branch (a human closed our PR and left the branch) or
// foreign_branch.
func (p *prRun) untracked(ctx context.Context, latest *store.Remediation, head string) error {
	own, err := p.ownPR(ctx)
	if err != nil {
		return err
	}

	if own != nil {
		return p.adopt(ctx, own)
	}

	if latest != nil && latest.State == store.StateClosedByUser {
		steps, err := p.store.Steps(ctx, latest.ID)
		if err != nil {
			return fmt.Errorf("read journal: %w", err)
		}

		if bot := lastCommitted(steps); bot != "" && bot == head {
			return &holdError{hold: store.HoldStaleBranch}
		}
	}

	return &holdError{hold: store.HoldForeignBranch}
}

// adopt inserts the row for the App's own PR first, so it counts against
// the cap, then treats it as tracked.
func (p *prRun) adopt(ctx context.Context, pr *control.PullRequest) error {
	t := p.target

	rem, err := p.store.ReserveSlot(ctx, &store.SlotRequest{
		RepositoryID: p.in.RepositoryID, ControlID: p.in.Control, Epoch: t.Epoch, OperationKey: p.in.OperationKey,
		Intent: t.IntentRevision, Generation: t.Generation, TTL: p.r.reservationTTL,
		BaseSHA: p.mainSHA, Branch: p.branch, ExpectedHead: pr.HeadSHA,
	})
	if errors.Is(err, store.ErrPRCap) {
		return &holdError{hold: store.HoldPRCap}
	}

	if err != nil {
		return fmt.Errorf("reserve slot: %w", err)
	}

	err = p.claim(ctx, rem.OperationKey, func(ctx context.Context, op store.ClaimedOperation) error {
		row := op.Remediation()
		row.State, row.PRNumber, row.PRURL, row.ReservedUntil = store.StateOpen, pr.Number, pr.URL, nil

		if err := op.Update(ctx, &row); err != nil {
			return fmt.Errorf("adopt #%d: %w", pr.Number, err)
		}

		*rem = row

		return nil
	})
	if err != nil {
		return err
	}

	p.event(ctx, rem.ID, "adopted", fmt.Sprintf(`{"pr_number":%d}`, pr.Number))
	p.log.Info("adopted the remediation app's own pull request", "pr_number", pr.Number)

	return p.tracked(ctx, rem)
}

// tracked updates a tracked PR (IMPL-0030 4.5-4.10): collaborate on its
// head, merge main into it, close it when main is compliant on a fresh
// read, or commit the remaining delta and refresh its text.
func (p *prRun) tracked(ctx context.Context, rem *store.Remediation) error {
	pr, err := p.repo.Observer().GetPullRequest(ctx, rem.PRNumber)
	if err != nil {
		return fmt.Errorf("read #%d: %w", rem.PRNumber, err)
	}

	if pr.State != prOpen {
		return nil
	}

	err = p.claim(ctx, rem.OperationKey, func(ctx context.Context, op store.ClaimedOperation) error {
		return p.updateTracked(ctx, op, pr)
	})
	if err != nil {
		return err
	}

	return p.record(ctx, rem.OperationKey, pr.HeadSHA)
}

func (p *prRun) updateTracked(ctx context.Context, op store.ClaimedOperation, pr *control.PullRequest) error {
	row := op.Remediation()

	steps, err := p.store.Steps(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("read journal: %w", err)
	}

	commits, err := p.repo.Observer().ListCommits(ctx, p.branch)
	if err != nil {
		return fmt.Errorf("list commits: %w", err)
	}

	if bot := lastCommitted(steps); bot != "" && !hasCommit(commits, bot) {
		// A human replaced the branch: never force-push over it.
		return p.hold(ctx, pr.Number, store.HoldConflict, "The branch no longer contains repo-guardian's last commit, so it was not updated.")
	}

	if err := p.rebase(ctx, &row, pr, commits); err != nil {
		return err
	}

	mainProp, err := p.propose(ctx, p.mainSHA)
	if err != nil {
		return err
	}

	if mainProp.compliant {
		return p.closeCompliant(ctx, op, &row, pr.Number)
	}

	headProp, err := p.propose(ctx, pr.HeadSHA)
	if err != nil {
		return err
	}

	reverts, err := p.reverts(ctx, steps, pr.HeadSHA, &headProp.cs)
	if err != nil {
		return err
	}

	if headProp.compliant || (len(headProp.cs.Files) == 0 && len(reverts) == 0) {
		return nil
	}

	return p.commitDelta(ctx, &row, pr, headProp, reverts)
}

// rebase re-points a PR at a renamed default branch, then merges main
// into a head that is behind it (IMPL-0030 4.5, 4.6). A merge GitHub
// accepted is a deferral: the next run re-pins the head it produced.
func (p *prRun) rebase(ctx context.Context, row *store.Remediation, pr *control.PullRequest, commits []control.Commit) error {
	w := p.repo.Writer()

	if pr.BaseRef != p.main {
		if err := p.fence(ctx); err != nil {
			return err
		}

		if err := w.UpdatePullRequestBase(ctx, pr.Number, p.main); err != nil {
			return fmt.Errorf("re-base #%d onto %s: %w", pr.Number, p.main, err)
		}

		p.journal(ctx, row.ID, &store.RemediationStep{Kind: stepPRUpdated, PRNumber: pr.Number})
	}

	if hasCommit(commits, p.mainSHA) {
		return nil
	}

	if err := p.fence(ctx); err != nil {
		return err
	}

	err := w.UpdateBranch(ctx, pr.Number, pr.HeadSHA)

	switch {
	case errors.Is(err, control.ErrMergeConflict):
		return p.hold(ctx, pr.Number, store.HoldConflict,
			"The default branch does not merge cleanly into this branch; resolve the conflict to resume.")
	case errors.Is(err, control.ErrExpectedHeadMismatch):
		return &deferError{reason: "update-branch: head moved"}
	case errors.Is(err, control.ErrUpdateBranchPending):
		return &deferError{reason: "update-branch accepted"}
	case err != nil:
		return fmt.Errorf("update-branch #%d: %w", pr.Number, err)
	}

	p.journal(ctx, row.ID, &store.RemediationStep{Kind: stepPRRebased, PRNumber: pr.Number})

	return &deferError{reason: "update-branch accepted"}
}

// closeCompliant closes a PR main no longer needs, decided on the fresh
// read; the branch is never deleted (IMPL-0030 4.10).
func (p *prRun) closeCompliant(ctx context.Context, op store.ClaimedOperation, row *store.Remediation, number int) error {
	if err := p.sticky(ctx, number, "The default branch is now compliant with this control, so this pull request is no longer needed."); err != nil {
		return err
	}

	if err := p.fence(ctx); err != nil {
		return err
	}

	if err := p.repo.Writer().ClosePullRequest(ctx, number); err != nil {
		return fmt.Errorf("close #%d: %w", number, err)
	}

	p.journal(ctx, row.ID, &store.RemediationStep{Kind: stepPRClosed, PRNumber: number})

	now := p.r.now()
	row.State, row.ClosedAt, row.CloseReason = store.StateClosedCompliant, &now, closeCompliantReason

	if err := op.Update(ctx, row); err != nil {
		return fmt.Errorf("mark closed: %w", err)
	}

	p.event(ctx, row.ID, store.StateClosedCompliant, "{}")
	p.outcome(resultClosedCompliant)

	return nil
}

// commitDelta commits what is still missing on the head, with the
// obsolete bot edits reverted, and refreshes the PR's text.
func (p *prRun) commitDelta(
	ctx context.Context,
	row *store.Remediation,
	pr *control.PullRequest,
	prop *proposal,
	reverts []control.FileChange,
) error {
	w := p.repo.Writer()
	changes := slices.Concat(prop.cs.Files, reverts)

	if err := p.fence(ctx); err != nil {
		return err
	}

	sha, err := w.Commit(ctx, p.branch, pr.HeadSHA, changes, commitMessage(p, prop))
	if errors.Is(err, control.ErrExpectedHeadMismatch) {
		return &deferError{reason: "expected head mismatch"}
	}

	if err != nil {
		return p.commitError(err)
	}

	p.journalFiles(ctx, row.ID, stepCommitted, sha, prop.cs.Files)
	p.journalFiles(ctx, row.ID, stepReverted, sha, reverts)

	rendered, err := p.render(prop)
	if err != nil {
		return err
	}

	if err := p.fence(ctx); err != nil {
		return err
	}

	if err := w.UpdatePullRequest(ctx, pr.Number, rendered.Title, rendered.Body); err != nil {
		return fmt.Errorf("update #%d: %w", pr.Number, err)
	}

	p.journal(ctx, row.ID, &store.RemediationStep{Kind: stepPRUpdated, PRNumber: pr.Number, CommittedSHA: sha})
	p.outcome(resultUpdated)

	return nil
}

// reverts are the obsolete bot edits (IMPL-0030 4.9): every path the
// journal shows the bot wrote that the control no longer owns and the
// current change set does not write, restored to main's content, but
// only where the head's blob is still the bot's. A human's edit to the
// path is left alone.
func (p *prRun) reverts(ctx context.Context, steps []store.RemediationStep, head string, cs *control.ChangeSet) ([]control.FileChange, error) {
	owned := make(map[string]bool)
	for _, r := range p.control.Owns() {
		if r.Kind == control.KindFile {
			owned[r.Key] = true
		}
	}

	for _, f := range cs.Files {
		owned[f.Path] = true
	}

	written := make(map[string]string) // path → the bot's latest blob

	for i := range steps {
		if path, ok := strings.CutPrefix(steps[i].ResourceKey, control.KindFile+":"); ok && steps[i].Kind == stepCommitted {
			written[path] = steps[i].BlobSHA
		}
	}

	var out []control.FileChange

	for _, path := range slicesSortedKeys(written) {
		if owned[path] {
			continue
		}

		_, blob, found, err := p.repo.Reader(head).GetContents(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("read %s at the head: %w", path, err)
		}

		if !found || blob != written[path] {
			continue
		}

		content, _, onMain, err := p.repo.Reader(p.mainSHA).GetContents(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("read %s on %s: %w", path, p.main, err)
		}

		out = append(out, control.FileChange{Path: path, Content: content, Delete: !onMain, Reason: "orphan"})
	}

	return out, nil
}

// hold upserts the sticky comment and stops the run with hold.
func (p *prRun) hold(ctx context.Context, number int, hold, message string) error {
	if err := p.sticky(ctx, number, message); err != nil {
		return err
	}

	return &holdError{hold: hold}
}

// sticky edits the one repo-guardian comment on the PR in place.
//
// TODO(IMPL-0030 OQ9): skip the call when the content hash is unchanged;
// the Writer has no comment read to compare against yet.
func (p *prRun) sticky(ctx context.Context, number int, message string) error {
	body := StickyMarker + "\n" + message + "\n"

	if err := p.fence(ctx); err != nil {
		return err
	}

	if err := p.repo.Writer().UpsertPRComment(ctx, number, StickyMarker, body); err != nil {
		return fmt.Errorf("comment on #%d: %w", number, err)
	}

	return nil
}

// propose evaluates the control fresh at sha through the Remediation
// App's Reader (D22: never stored status), runs its Remediate, validates
// the change set and self-checks it (IMPL-0030 4.7, 4.8).
func (p *prRun) propose(ctx context.Context, sha string) (*proposal, error) {
	ev, in, err := p.evaluate(ctx, sha, nil)
	if err != nil {
		return nil, err
	}

	if control.StatusOf(ev) == control.StatusCompliant {
		return &proposal{compliant: true, ev: ev}, nil
	}

	cs, err := p.control.Remediate(ctx, control.RemediationInput{EvalInput: in, Evaluation: ev})
	if err != nil {
		return nil, fmt.Errorf("remediate %s: %w", p.in.Control, err)
	}

	if err := control.ValidateChangeSet(p.control, ev, cs); err != nil {
		return nil, fmt.Errorf("change set of %s: %w", p.in.Control, err)
	}

	if p.workflow != nil {
		// The workflow applies the change on merge, so the changed
		// content cannot pass before then: no self-check.
		if err := p.asWorkflow(ctx, sha, &cs); err != nil {
			return nil, err
		}

		return &proposal{ev: ev, cs: cs}, nil
	}

	if err := p.selfCheck(ctx, sha, ev, &cs); err != nil {
		return nil, err
	}

	return &proposal{ev: ev, cs: cs}, nil
}

// asWorkflow replaces the API changes with the control's workflow file
// (IMPL-0030 5.4), dropped when sha already carries it byte for byte.
// Property values reach the workflow only through its env block
// (IMPL-0020 A2), which WorkflowFile renders.
func (p *prRun) asWorkflow(ctx context.Context, sha string, cs *control.ChangeSet) error {
	if len(cs.API) == 0 {
		return nil
	}

	f, err := p.workflow.WorkflowFile(p.def, cs.API)
	if err != nil {
		return fmt.Errorf("workflow file of %s: %w", p.in.Control, err)
	}

	cs.API = nil

	current, _, found, err := p.repo.Reader(sha).GetContents(ctx, f.Path)
	if err != nil {
		return fmt.Errorf("read %s: %w", f.Path, err)
	}

	if !found || !bytes.Equal(current, f.Content) {
		cs.Files = append(cs.Files, f)
	}

	return nil
}

// commitError names the Workflows permission when GitHub refuses a
// workflow file commit (IMPL-0030 5.4): Contents write alone is not
// enough under .github/workflows/.
func (p *prRun) commitError(err error) error {
	if p.workflow != nil && accessDenied(err) {
		return &holdError{
			hold:  store.HoldPermission,
			cause: fmt.Errorf("the Remediation App needs the Workflows: write permission to commit its workflow file: %w", err),
		}
	}

	return fmt.Errorf("commit: %w", err)
}

// selfCheck evaluates the changed content and refuses a change set
// under which a Fixed rule does not pass or a passing rule regresses.
func (p *prRun) selfCheck(ctx context.Context, sha string, before control.Evaluation, cs *control.ChangeSet) error {
	if len(cs.Files) == 0 {
		return nil
	}

	after, _, err := p.evaluate(ctx, sha, cs.Files)
	if err != nil {
		return err
	}

	status := make(map[control.RuleID]control.RuleStatus, len(after.Results))
	for _, r := range after.Results {
		status[r.RuleID] = r.Status
	}

	for _, id := range cs.Fixed {
		if status[id] != control.Pass {
			return fmt.Errorf("self-check of %s: rule %s does not pass under its own change", p.in.Control, id)
		}
	}

	for _, r := range before.Results {
		if r.Status == control.Pass && status[r.RuleID] != control.Pass {
			return fmt.Errorf("self-check of %s: rule %s regresses under the change", p.in.Control, r.RuleID)
		}
	}

	return nil
}

// evaluate runs the control at sha, with files overlaid when set, and
// validates the result. A throttled read is returned as the throttle.
func (p *prRun) evaluate(ctx context.Context, sha string, files []control.FileChange) (control.Evaluation, control.EvalInput, error) {
	watch := &throttleWatch{base: p.repo.Reader(sha)}

	var base control.Reader = watch
	if files != nil {
		base = newOverlayReader(watch, files)
	}

	t := p.target
	in := control.EvalInput{
		Repo: control.RepoContext{
			Org: t.Org, Name: t.Name, DefaultBranch: p.main, SHA: sha, AccountType: t.AccountType,
			Vars: template.Vars{Common: template.Common{Owner: t.Org, Repo: t.Name, DefaultBranch: p.main}, Org: t.Org},
		},
		Reader: control.NewEvalReader(base, p.r.now),
	}

	ev, err := p.control.Evaluate(ctx, in)
	if thr := watch.throttled(); thr != nil {
		return control.Evaluation{}, in, thr
	}

	if err != nil {
		return control.Evaluation{}, in, fmt.Errorf("evaluate %s: %w", p.in.Control, err)
	}

	if ev, err = control.Validate(p.control, ev); err != nil {
		return control.Evaluation{}, in, fmt.Errorf("validate %s: %w", p.in.Control, err)
	}

	return ev, in, nil
}

// render renders the PR's title, body and labels from the proposal.
func (p *prRun) render(prop *proposal) (template.RenderedPR, error) {
	t := p.target
	vars := &template.PRVars{
		Common:  template.Common{Owner: t.Org, Repo: t.Name, DefaultBranch: p.main, Date: p.r.now().UTC().Format(time.RFC3339)},
		Control: template.ControlRef{ID: p.control.ID().String(), Slug: p.in.Control, Title: p.def.Title},
		Fixed:   ruleRefs(p.control, prop.cs.Fixed),
		Manual:  ruleRefs(p.control, slices.Concat(prop.cs.Manual, prop.cs.Blocked)),
		Notes:   prop.cs.Notes,
	}

	if vars.Control.Title == "" {
		vars.Control.Title = p.in.Control
	}

	rendered, err := template.RenderPR(&p.def.PR, vars)
	if err != nil {
		return template.RenderedPR{}, fmt.Errorf("render pull request for %s: %w", p.in.Control, err)
	}

	return rendered, nil
}

// record writes the finished run (IMPL-0030 4.15): RecordRun, then
// last_remediation_at. An empty key records a run that opened nothing.
func (p *prRun) record(ctx context.Context, operationKey, processed string) error {
	t := p.target

	ok, err := p.store.RecordRun(ctx, &store.RunRecord{
		Org: t.Org, RepositoryID: p.in.RepositoryID, ControlID: p.in.Control, Epoch: t.Epoch, OperationKey: operationKey,
		Generation: t.Generation, IntentRevision: t.IntentRevision, ProcessedHead: processed,
	})
	if err != nil {
		return fmt.Errorf("record run: %w", err)
	}

	if !ok {
		p.log.Info("assignment moved under the remediation; its record was discarded")

		return nil
	}

	if err := p.store.TouchLastRemediation(ctx, p.in.RepositoryID); err != nil {
		return fmt.Errorf("record run: %w", err)
	}

	return nil
}

// claim runs fn under the operation's row lock. A hold, a deferral or a
// moved epoch from fn still commits the row's updates and is returned
// after the lock is released; any other error rolls them back.
func (run *remediationRun) claim(ctx context.Context, key string, fn func(context.Context, store.ClaimedOperation) error) error {
	var soft error

	err := run.store.ClaimOperation(ctx, key, func(ctx context.Context, op store.ClaimedOperation) error {
		err := fn(ctx, op)
		if isSoft(err) {
			soft = err

			return nil
		}

		return err
	})
	if err != nil {
		return err
	}

	return soft
}

// isSoft reports whether err ends a run without undoing its row writes.
func isSoft(err error) bool {
	var (
		held     *holdError
		deferred *deferError
	)

	return errors.As(err, &held) || errors.As(err, &deferred) || errors.Is(err, ErrStaleEpoch)
}

// journal appends one step; a failed append is logged, never fatal: the
// step happened.
func (run *remediationRun) journal(ctx context.Context, remediationID int64, step *store.RemediationStep) {
	step.RemediationID, step.OperationKey = remediationID, run.in.OperationKey
	if step.Status == "" {
		step.Status = stepDone
	}

	if _, err := run.store.AppendStep(ctx, step); err != nil {
		run.log.Warn("journal step failed", "kind", step.Kind, "error", err)
	}
}

// journalFiles journals one step per file of a commit, with its blob.
func (run *remediationRun) journalFiles(ctx context.Context, remediationID int64, kind, sha string, files []control.FileChange) {
	for _, f := range files {
		step := &store.RemediationStep{Kind: kind, ResourceKey: control.KindFile + ":" + f.Path, CommittedSHA: sha}
		if !f.Delete {
			step.BlobSHA = gitBlobSHA(f.Content)
		}

		run.journal(ctx, remediationID, step)
	}
}

// event records a remediation event; best effort.
func (run *remediationRun) event(ctx context.Context, remediationID int64, kind, detail string) {
	if err := run.store.AppendEvent(ctx, remediationID, kind, []byte(detail)); err != nil {
		run.log.Warn("remediation event failed", "kind", kind, "error", err)
	}
}

// lastCommitted is the newest commit the journal shows the bot made.
func lastCommitted(steps []store.RemediationStep) string {
	for i := len(steps) - 1; i >= 0; i-- {
		if s := steps[i]; (s.Kind == stepCommitted || s.Kind == stepReverted) && s.CommittedSHA != "" {
			return s.CommittedSHA
		}
	}

	return ""
}

func hasCommit(commits []control.Commit, sha string) bool {
	return slices.ContainsFunc(commits, func(c control.Commit) bool { return c.SHA == sha })
}

// commitMessage names the control and what the commit fixes.
func commitMessage(p *prRun, prop *proposal) string {
	ids := make([]string, len(prop.cs.Fixed))
	for i, id := range prop.cs.Fixed {
		ids[i] = string(id)
	}

	return fmt.Sprintf("chore(repo-guardian): remediate %s\n\nFixes: %s", p.control.ID(), strings.Join(ids, ", "))
}

// ruleRefs names ids with the control's rule titles.
func ruleRefs(c control.Control, ids []control.RuleID) []template.RuleRef {
	titles := make(map[control.RuleID]string)
	for _, r := range c.Rules() {
		titles[r.ID] = r.Title
	}

	out := make([]template.RuleRef, 0, len(ids))
	for _, id := range ids {
		out = append(out, template.RuleRef{ID: string(id), Title: titles[id]})
	}

	return out
}

// gitBlobSHA is git's blob id for content, as GitHub reports it.
func gitBlobSHA(content []byte) string {
	sum := sha1.Sum(fmt.Appendf(nil, "blob %d\x00%s", len(content), content)) //nolint:gosec // git object ids are sha1

	return hex.EncodeToString(sum[:])
}

func slicesSortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}
