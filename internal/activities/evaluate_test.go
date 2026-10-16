package activities

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"testing"
	"time"

	gh "github.com/google/go-github/v68/github"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/control/controltest"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

const testSHA = "0123456789abcdef0123456789abcdef01234567"

// fileControl is a one-rule control that passes when its file exists.
type fileControl struct {
	id   control.ID
	owns control.Resource
}

func newFileControl(name, path string) *fileControl {
	return &fileControl{id: control.ID{Slug: name, Version: 1}, owns: control.MustCanonical(control.KindFile, path)}
}

func (c *fileControl) ID() control.ID            { return c.id }
func (c *fileControl) Owns() []control.Resource  { return []control.Resource{c.owns} }
func (c *fileControl) Reads() []control.Resource { return []control.Resource{c.owns} }
func (*fileControl) Rules() []control.Rule       { return []control.Rule{{ID: "exists", Remediate: true}} }
func (*fileControl) Remediate(context.Context, control.RemediationInput) (control.ChangeSet, error) {
	return control.ChangeSet{}, nil
}

//nolint:gocritic // hugeParam: control.Control's signature
func (c *fileControl) Evaluate(ctx context.Context, in control.EvalInput) (control.Evaluation, error) {
	ev := map[string]any{control.EvidenceKind: "file", control.EvidenceVersion: 1}

	_, _, found, err := in.Reader.GetContents(ctx, c.owns.Key)

	status := control.Pass

	switch {
	case errors.Is(err, control.ErrPermission):
		status, ev["reason"] = control.Unknown, control.ReasonPermission
	case err != nil:
		status, ev["reason"] = control.Error, "read"
	case !found:
		status = control.Fail
	}

	return control.Evaluation{Results: []control.RuleResult{{RuleID: "exists", Status: status, Evidence: ev}}}, nil
}

// apiControl owns one API resource and reads no file.
type apiControl struct{ fileControl }

func (*apiControl) Owns() []control.Resource {
	return []control.Resource{{Kind: control.KindLabel, Key: "bug"}}
}
func (*apiControl) Reads() []control.Resource { return nil }

// fakePolicy serves controls at revision "rev-<name>".
type fakePolicy map[string]control.Control

func (fakePolicy) Version() string { return "controls:test" }

func (p fakePolicy) Control(id control.ID) control.Control { return p[id.String()] }

func (fakePolicy) Definition(control.ID) (control.Definition, bool) {
	return control.Definition{}, true
}

func (p fakePolicy) Revision(id control.ID) string {
	if p[id.String()] == nil {
		return ""
	}

	return "rev-" + id.Slug
}

// fakeEvalStore serves one target and records the stage.
type fakeEvalStore struct {
	target *store.EvaluationTarget
	staged *store.EvaluationStage
}

func (s *fakeEvalStore) EvaluationTarget(context.Context, int64) (*store.EvaluationTarget, error) {
	return s.target, nil
}

func (s *fakeEvalStore) StageEvaluation(_ context.Context, st *store.EvaluationStage) error {
	s.staged = st

	return nil
}

func (*fakeEvalStore) RecordEvaluation(context.Context, string) (store.RecordOutcome, error) {
	return store.RecordOutcome{}, nil
}

func (*fakeEvalStore) Park(context.Context, int64, string) error { return nil }

// fakeEvalRepo is one repository at testSHA.
type fakeEvalRepo struct {
	headErr error
	sha     string
	files   *controltest.FakeRepo
}

func (r *fakeEvalRepo) Repo(context.Context, int64, string, string) (EvalRepo, error) { return r, nil }

func (r *fakeEvalRepo) Head(context.Context) (branch, sha string, err error) {
	return "main", r.sha, r.headErr
}

func (r *fakeEvalRepo) Reader(string) control.Reader { return r.files }

func assignment(name string, state policy.AssignmentState) store.AssignedControl {
	return store.AssignedControl{
		ControlID: name + "@1", ControlVersion: 1, Revision: "rev-" + name, Epoch: 9, State: state,
		RequestedMode: "evaluate", EffectiveMode: "evaluate",
	}
}

type evalFixture struct {
	st   *fakeEvalStore
	repo *fakeEvalRepo
	e    *Evaluator
}

func newEvalFixture(t *testing.T) *evalFixture {
	t.Helper()

	pol := fakePolicy{
		"codeowners@1": newFileControl("codeowners", ".github/CODEOWNERS"),
		"renovate@1":   newFileControl("renovate", "renovate.json"),
		"labels@1":     &apiControl{*newFileControl("labels", "unused")},
		"conflicted@1": newFileControl("conflicted", "conflicted.txt"),
	}

	st := &fakeEvalStore{target: &store.EvaluationTarget{
		RepositoryID: testRepoID, Org: "acme", Name: "widgets", Active: true, PolicyState: policy.StateManaged,
		InstallationID: testInstallation,
		Assignments: []store.AssignedControl{
			assignment("codeowners", policy.AssignmentActive),
			assignment("renovate", policy.AssignmentActive),
			assignment("labels", policy.AssignmentActive),
			assignment("conflicted", policy.AssignmentConflict),
			{ControlID: "renovate@1", Revision: "rev-old", State: policy.AssignmentActive},
		},
	}}

	files := &controltest.FakeRepo{Files: map[string]string{".github/CODEOWNERS": "* @acme/team\n"}, Errors: map[string]error{}}
	repo := &fakeEvalRepo{sha: testSHA, files: files}

	return &evalFixture{st: st, repo: repo, e: NewEvaluator(st, pol, repo, slog.New(slog.DiscardHandler))}
}

func (f *evalFixture) evaluate(t *testing.T, in *workflows.EvaluateInput) *workflows.EvaluateResult {
	t.Helper()

	in.RepositoryID, in.CheckKey = testRepoID, testKey

	res, err := f.e.Evaluate(t.Context(), in)
	if err != nil {
		t.Fatalf("Evaluate() err = %v", err)
	}

	return res
}

func stagedIDs(st *store.EvaluationStage) []string {
	if st == nil {
		return nil
	}

	ids := make([]string, 0, len(st.Controls))
	for i := range st.Controls {
		ids = append(ids, st.Controls[i].ControlID)
	}

	return ids
}

func TestEvaluate_ScheduleStagesEveryActiveControl(t *testing.T) {
	t.Parallel()

	f := newEvalFixture(t)
	res := f.evaluate(t, &workflows.EvaluateInput{Trigger: workflows.TriggerSchedule, All: true, Scheduled: true})

	if res.Kind != workflows.CheckChecked || res.Controls != 3 {
		t.Fatalf("Evaluate() = %+v, want Checked with 3 controls", res)
	}

	if got := fmt.Sprint(stagedIDs(f.st.staged)); got != "[codeowners@1 renovate@1 labels@1]" {
		t.Errorf("staged controls = %s, want the three active assignments at the loaded revision", got)
	}

	codeowners, renovate := f.st.staged.Controls[0], f.st.staged.Controls[1]

	if codeowners.Status != string(control.StatusCompliant) || renovate.Status != string(control.StatusNonCompliant) {
		t.Errorf("statuses = %s/%s, want compliant/non_compliant", codeowners.Status, renovate.Status)
	}

	if !renovate.Remediable || codeowners.Remediable {
		t.Errorf("remediable = %t/%t, want only the failing remediable control", codeowners.Remediable, renovate.Remediable)
	}

	if codeowners.EvaluatedSHA != testSHA || codeowners.Epoch != 9 || codeowners.Rules[0].EvidenceKind != "file" {
		t.Errorf("staged codeowners = %+v, want the pinned sha, the assignment epoch and lifted evidence", codeowners)
	}

	if f.st.staged.PolicyVersion != "controls:test" || f.st.staged.Trigger != workflows.TriggerSchedule {
		t.Errorf("stage = %+v, want the loaded policy version and the trigger", f.st.staged)
	}
}

func TestEvaluate_FingerprintIgnoresOtherControlsFiles(t *testing.T) {
	t.Parallel()

	f := newEvalFixture(t)
	f.evaluate(t, &workflows.EvaluateInput{All: true, Scheduled: true})
	before := f.st.staged.Controls[0].Fingerprint

	f.repo.files.Files["renovate.json"] = "{}"
	f.evaluate(t, &workflows.EvaluateInput{All: true, Scheduled: true})

	if after := f.st.staged.Controls[0].Fingerprint; after != before {
		t.Errorf("codeowners fingerprint moved %s → %s when only renovate.json changed", before, after)
	}
}

func TestEvaluate_PushSelectsByChangedPath(t *testing.T) {
	t.Parallel()

	f := newEvalFixture(t)
	f.evaluate(t, &workflows.EvaluateInput{Trigger: workflows.TriggerPush, Paths: []string{"docs/readme.md", "renovate.json"}})

	if got := fmt.Sprint(stagedIDs(f.st.staged)); got != "[renovate@1]" {
		t.Errorf("staged = %s, want only the control owning the changed path", got)
	}
}

func TestEvaluate_UnknownChangeSkipsAPIOnlyControls(t *testing.T) {
	t.Parallel()

	f := newEvalFixture(t)
	f.evaluate(t, &workflows.EvaluateInput{Trigger: workflows.TriggerPush, All: true})

	if got := fmt.Sprint(stagedIDs(f.st.staged)); got != "[codeowners@1 renovate@1]" {
		t.Errorf("staged = %s, want every file control and no API-only control off the schedule", got)
	}
}

func TestEvaluate_ThrottleDefersAndStagesNothing(t *testing.T) {
	t.Parallel()

	f := newEvalFixture(t)
	f.repo.files.Errors["file:renovate.json"] = fmt.Errorf("read: %w", &ghclient.ThrottledError{ResetAt: time.Now().Add(time.Hour)})

	res := f.evaluate(t, &workflows.EvaluateInput{All: true, Scheduled: true})

	if res.Kind != workflows.CheckDeferred || res.Until.IsZero() || f.st.staged != nil {
		t.Errorf("Evaluate() = %+v, staged %v; want Deferred with a due time and nothing staged", res, stagedIDs(f.st.staged))
	}
}

func TestEvaluate_LostRepositoryParks(t *testing.T) {
	t.Parallel()

	f := newEvalFixture(t)
	f.repo.headErr = fmt.Errorf("pin head: %w", &gh.ErrorResponse{Response: &http.Response{StatusCode: http.StatusNotFound}})

	res := f.evaluate(t, &workflows.EvaluateInput{All: true, Scheduled: true})

	if res.Kind != workflows.CheckParked || res.ParkReason != workflows.ParkAccessDenied || f.st.staged != nil {
		t.Errorf("Evaluate() = %+v, want Parked{access_denied} and nothing staged", res)
	}
}

func TestEvaluate_ControlLocalPermissionIsUnknown(t *testing.T) {
	t.Parallel()

	f := newEvalFixture(t)
	f.repo.files.Errors["file:renovate.json"] = fmt.Errorf("read: %w", control.ErrPermission)

	res := f.evaluate(t, &workflows.EvaluateInput{All: true, Scheduled: true})

	if res.Kind != workflows.CheckChecked || f.st.staged.Controls[1].Status != string(control.StatusUnknown) {
		t.Errorf("Evaluate() = %+v, renovate = %+v; want Checked with renovate unknown and nothing parked", res, f.st.staged.Controls[1])
	}
}

func TestEvaluate_Skips(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(*evalFixture)
		want  workflows.CheckKind
	}{
		{"unmanaged", func(f *evalFixture) { f.st.target.PolicyState = policy.StateExcluded }, workflows.CheckSkipped},
		{"no evaluation app access", func(f *evalFixture) { f.st.target.InstallationID = 0 }, workflows.CheckSkipped},
		{"empty repository", func(f *evalFixture) { f.repo.sha = "" }, workflows.CheckSkipped},
		{"already parked", func(f *evalFixture) { f.st.target.Active = false }, workflows.CheckParked},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newEvalFixture(t)
			tt.setup(f)

			res := f.evaluate(t, &workflows.EvaluateInput{All: true, Scheduled: true})
			if res.Kind != tt.want || res.ParkReason != "" || f.st.staged != nil {
				t.Errorf("Evaluate() = %+v, want %s with no park reason and nothing staged", res, tt.want)
			}
		})
	}
}

// recordingStore records Park and answers RecordEvaluation.
type recordingStore struct {
	fakeEvalStore

	parked []string
}

func (*recordingStore) RecordEvaluation(context.Context, string) (store.RecordOutcome, error) {
	return store.RecordOutcome{Recorded: []string{"a", "b"}, Changed: []string{"a"}, Discarded: []string{"c"}}, nil
}

func (s *recordingStore) Park(_ context.Context, repoID int64, reason string) error {
	s.parked = append(s.parked, fmt.Sprintf("%d:%s", repoID, reason))

	return nil
}

func TestRecordEvaluation_CountsTheOutcome(t *testing.T) {
	t.Parallel()

	e := NewEvaluator(&recordingStore{}, fakePolicy{}, nil, slog.New(slog.DiscardHandler))

	got, err := e.RecordEvaluation(t.Context(), &workflows.RecordEvaluationInput{CheckKey: testKey})
	if err != nil {
		t.Fatal(err)
	}

	if got.Recorded != 2 || got.Changed != 1 || got.Discarded != 1 || got.AlreadyFinal {
		t.Errorf("RecordEvaluation() = %+v, want 2 recorded, 1 changed, 1 discarded", got)
	}
}

func TestParkRepository_ParksWithTheReason(t *testing.T) {
	t.Parallel()

	st := &recordingStore{}
	rec := &logRecorder{}
	e := NewEvaluator(st, fakePolicy{}, nil, slog.New(rec))

	err := e.ParkRepository(t.Context(), &workflows.ParkRepositoryInput{
		RepositoryID: testRepoID, CheckKey: testKey, Reason: workflows.ParkAccessDenied, Cause: "404",
	})
	if err != nil {
		t.Fatal(err)
	}

	if fmt.Sprint(st.parked) != "[42:access_denied]" {
		t.Errorf("parked = %v, want [42:access_denied]", st.parked)
	}

	if len(rec.records) != 1 || rec.records[0].Message != "parking repository until discovery sees it again" ||
		rec.records[0].Level != slog.LevelError {
		t.Errorf("log records = %+v, want CheckRepo's park line at error level", rec.records)
	}
}
