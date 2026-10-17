package activities

import (
	"context"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/control/controltest"
	"github.com/donaldgifford/repo-guardian/internal/controls/builtin"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// labelControl owns the "bug" label and passes while it exists, so a
// colour change moves its read but not its status.
type labelControl struct{ fileControl }

func (*labelControl) Owns() []control.Resource {
	return []control.Resource{{Kind: control.KindLabel, Key: "bug"}}
}

func (*labelControl) Reads() []control.Resource { return nil }

//nolint:gocritic // hugeParam: control.Control's signature
func (*labelControl) Evaluate(ctx context.Context, in control.EvalInput) (control.Evaluation, error) {
	labels, err := in.Reader.ListLabels(ctx)
	if err != nil {
		return control.Evaluation{}, err
	}

	status := control.Fail
	if slices.ContainsFunc(labels, func(l control.Label) bool { return l.Name == "bug" }) {
		status = control.Pass
	}

	return control.Evaluation{Results: []control.RuleResult{{
		RuleID: "exists", Status: status, Evidence: map[string]any{control.EvidenceKind: "label", control.EvidenceVersion: 1},
	}}}, nil
}

// revisedPolicy serves fakePolicy's controls at another revision.
type revisedPolicy struct{ fakePolicy }

func (p revisedPolicy) Revision(id control.ID) string { return p.fakePolicy.Revision(id) + "-2" }

// changeRepo is one repository state the change table evaluates.
type changeRepo struct {
	files    map[string]string
	labels   []control.Label
	revision string
}

func baselineRepo() changeRepo {
	return changeRepo{
		files:  map[string]string{".github/CODEOWNERS": "* @acme/team\n"},
		labels: []control.Label{{Name: "bug", Color: "d73a4a"}},
	}
}

// evaluateState evaluates codeowners and labels against r and returns
// each control's staged payload by control id.
func evaluateState(t *testing.T, r changeRepo) map[string]store.ControlEvaluation {
	t.Helper()

	base := fakePolicy{
		"codeowners@1": newFileControl("codeowners", ".github/CODEOWNERS"),
		"labels@1":     &labelControl{*newFileControl("labels", "unused")},
	}

	var pol EvalPolicy = base

	a, b := assignment("codeowners", policy.AssignmentActive), assignment("labels", policy.AssignmentActive)
	if r.revision != "" {
		pol, a.Revision, b.Revision = revisedPolicy{base}, a.Revision+"-2", b.Revision+"-2"
	}

	st := &fakeEvalStore{target: &store.EvaluationTarget{
		RepositoryID: testRepoID, Org: "acme", Name: "widgets", Active: true, PolicyState: policy.StateManaged,
		InstallationID: testInstallation, Assignments: []store.AssignedControl{a, b},
	}}
	repo := &fakeEvalRepo{sha: testSHA, files: &controltest.FakeRepo{Files: r.files, Labels: r.labels}}

	if _, err := NewEvaluator(st, pol, repo, slog.New(slog.DiscardHandler)).Evaluate(t.Context(), &workflows.EvaluateInput{
		RepositoryID: testRepoID, CheckKey: testKey, Trigger: workflows.TriggerSchedule, All: true, Scheduled: true,
	}); err != nil {
		t.Fatal(err)
	}

	out := make(map[string]store.ControlEvaluation)
	for i := range st.staged.Controls {
		out[st.staged.Controls[i].ControlID] = st.staged.Controls[i]
	}

	return out
}

// TestEvaluate_ChangeDetection is IMPL-0029 task 5.11's activity table:
// which edits move a control's fingerprint (the store bumps the
// generation on exactly that) and which leave it alone.
func TestEvaluate_ChangeDetection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		edit        func(*changeRepo)
		wantChanged []string
		wantStatus  bool // the edit changes a status
	}{
		{
			name:        "status change",
			edit:        func(r *changeRepo) { delete(r.files, ".github/CODEOWNERS") },
			wantChanged: []string{"codeowners"}, wantStatus: true,
		},
		{
			name:        "file blob change",
			edit:        func(r *changeRepo) { r.files[".github/CODEOWNERS"] = "* @acme/other\n" },
			wantChanged: []string{"codeowners"},
		},
		{
			name:        "API value change with unchanged statuses",
			edit:        func(r *changeRepo) { r.labels[0].Color = "ffffff" },
			wantChanged: []string{"labels"},
		},
		{
			name:        "revision change",
			edit:        func(r *changeRepo) { r.revision = "2" },
			wantChanged: []string{"codeowners", "labels"},
		},
		{
			name: "unrelated push",
			edit: func(r *changeRepo) { r.files["README.md"] = "# widgets\n" },
		},
	}

	before := evaluateState(t, baselineRepo())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := baselineRepo()
			r.files = maps.Clone(r.files)
			r.labels = slices.Clone(r.labels)
			tt.edit(&r)

			after := evaluateState(t, r)

			var changed []string

			statusMoved := false

			for _, id := range slices.Sorted(maps.Keys(before)) {
				if before[id].Fingerprint != after[id].Fingerprint {
					changed = append(changed, id)
				}

				statusMoved = statusMoved || before[id].Status != after[id].Status
			}

			if !slices.Equal(changed, tt.wantChanged) || statusMoved != tt.wantStatus {
				t.Errorf("fingerprints moved for %v (status moved %v), want %v (status moved %v)",
					changed, statusMoved, tt.wantChanged, tt.wantStatus)
			}
		})
	}
}

// TestPolicyRevision_CosmeticTitleEdit: a title edit keeps the control's
// revision, so it moves no fingerprint and bumps no generation.
func TestPolicyRevision_CosmeticTitleEdit(t *testing.T) {
	t.Parallel()

	revision := func(title string) string {
		t.Helper()

		root := t.TempDir()
		for name, body := range map[string]string{
			"enterprise.hcl": "enterprise {\n  orgs     = [\"acme\"]\n  controls = [\"codeowners@1\"]\n}\n",
			"catalogue/codeowners.hcl": "control \"codeowners\" {\n  version  = 1\n  type     = \"codeowners\"\n  title    = \"" + title +
				"\"\n  template = \"codeowners\"\n\n  rule \"exists\" {\n    title     = \"" + title + "\"\n    remediate = true\n  }\n}\n",
		} {
			p := filepath.Join(root, name)
			if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		snap, err := policy.Load(root, builtin.Registry(), slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}

		return snap.Revision(control.ID{Slug: "codeowners", Version: 1})
	}

	if a, b := revision("CODEOWNERS"), revision("A CODEOWNERS file, reworded"); a == "" || a != b {
		t.Errorf("revisions %q and %q, want one non-empty revision across a title edit", a, b)
	}
}
