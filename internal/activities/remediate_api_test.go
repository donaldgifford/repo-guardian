package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/controls/builtin"
	"github.com/donaldgifford/repo-guardian/internal/github/ghfake"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

func boolp(b bool) *bool { return &b }

// The API controls the Phase 5 tests remediate, by slug, with the apply
// value spliced in.
var apiControls = map[string]string{
	"settings": `control "settings" {
  version = 1
  type    = "repo_settings"
  title   = "Settings"

  rule "alerts" {
    kind      = "equals"
    property  = "vulnerability_alerts"
    value     = true
    remediate = true
  }

  rule "delete-branch" {
    kind      = "equals"
    property  = "delete_branch_on_merge"
    value     = true
    remediate = true
  }

  rule "no-wiki" {
    kind      = "equals"
    property  = "has_wiki"
    value     = false
    remediate = true
  }

  remediation {
    apply = %q
  }
}
`,
	"labels": `control "labels" {
  version = 1
  type    = "labels"
  title   = "Labels"

  rule "bug" {
    kind        = "present"
    name        = "bug"
    color       = "d73a4a"
    description = "Something isn't working"
    remediate   = true
  }

  rule "no-wontfix" {
    kind      = "absent"
    name      = "wontfix"
    remediate = true
  }

  remediation {
    apply = %q
  }
}
`,
	"props": `control "props" {
  version = 1
  type    = "custom_properties"
  title   = "Properties"

  rule "matches" {
    remediate = true
  }

  remediation {
    apply = %q
  }
}
`,
}

// apiPolicy loads the named API controls with apply.
func apiPolicy(t *testing.T, apply string, slugs ...string) *policy.Snapshot {
	t.Helper()

	files := make(map[string]string)
	ids := make([]string, 0, len(slugs))

	for _, slug := range slugs {
		ids = append(ids, fmt.Sprintf("%q", slug+"@1"))
		files["catalogue/"+slug+".hcl"] = fmt.Sprintf(apiControls[slug], apply)
	}

	files["enterprise.hcl"] = fmt.Sprintf("enterprise {\n  orgs     = [\"acme\"]\n  controls = [%s]\n}\n", strings.Join(ids, ", "))

	snap, err := policy.Load(writeRoot(t, files), builtin.Registry(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	return snap
}

func newAPIHarness(t *testing.T, apply, slug string, files map[string]string) *prHarness {
	t.Helper()

	h := newHarness(t, 3, apiPolicy(t, apply, slug), []string{slug}, files)
	t.Cleanup(h.noDestructiveCalls)

	return h
}

// reapply rolls out apply for slug: a new revision and a new intent,
// with no new generation and nothing due, the way an apply edit with no
// file change arrives (D23).
func (h *prHarness) reapply(apply, slug string) {
	h.t.Helper()

	snap := apiPolicy(h.t, apply, slug)
	h.r.policy = snap

	t := h.st.targets[slug]
	t.Revision = snap.Revision(control.ID{Slug: slug, Version: 1})
	t.IntentRevision += "-" + apply
}

// botWrites lists the Writer calls the bot made.
func (h *prHarness) botWrites() []string {
	methods := writerMethods()

	var out []string

	for _, r := range h.gh.Requests() {
		if r.Actor == prBot.Login && methods[r.Method] {
			out = append(out, r.Method)
		}
	}

	return out
}

// runAs runs slug once and requires kind.
func (h *prHarness) runAs(slug string, kind workflows.RemediationKind) *workflows.RemediateResult {
	h.t.Helper()

	res, err := h.run(slug)
	if err != nil {
		h.t.Fatalf("Remediate(%s) = %v", slug, err)
	}

	if res.Kind != kind {
		h.t.Fatalf("Remediate(%s) = %+v, want %s", slug, res, kind)
	}

	return res
}

func (h *prHarness) latestOf(slug string) store.Remediation {
	r, _ := h.st.LatestRemediation(context.Background(), prRepo, slug)
	if r == nil {
		return store.Remediation{}
	}

	return *r
}

func (h *prHarness) settings() control.RepositorySettings {
	h.t.Helper()

	s, err := h.repo.As(alice).GetRepository(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}

	return s
}

// seedSettings fails all three settings rules while a human keeps
// unrelated settings of their own.
func seedSettings(t *testing.T, h *prHarness) {
	t.Helper()

	err := h.repo.As(alice).UpdateRepository(context.Background(), control.RepositorySettings{
		HasWiki: boolp(true), DeleteBranchOnMerge: boolp(false), HasIssues: boolp(true), AllowSquashMerge: boolp(true),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func stepsOf(h *prHarness, id int64) []string {
	steps := h.st.steps[id]
	out := make([]string, 0, len(steps))

	for i := range steps {
		out = append(out, steps[i].ResourceKey+"="+steps[i].Status)
	}

	return out
}

func TestAPI_RecommendWritesNothing(t *testing.T) {
	t.Parallel()

	h := newAPIHarness(t, control.ApplyRecommend, "settings", nil)
	seedSettings(t, h)
	h.runAs("settings", workflows.RemediationDone)

	if w := h.botWrites(); len(w) != 0 {
		t.Errorf("recommend wrote to GitHub: %v", w)
	}

	row := h.latestOf("settings")
	if row.Kind != store.RemediationAPI || row.State != store.StateRecommended {
		t.Fatalf("row = %+v, want a recommended api row", row)
	}

	var proposal []proposedChange
	if err := json.Unmarshal(row.Proposal, &proposal); err != nil {
		t.Fatal(err)
	}

	keys := make([]string, 0, len(proposal))
	for _, p := range proposal {
		keys = append(keys, fmt.Sprintf("%s:%v->%v", p.Key, p.Before, p.After))
	}

	want := []string{"setting:vulnerability_alerts:false->true", "setting:delete_branch_on_merge:false->true", "setting:has_wiki:true->false"}
	if !slices.Equal(keys, want) {
		t.Errorf("proposal = %v, want %v", keys, want)
	}

	if h.st.targets["settings"].Due {
		t.Error("the recommendation did not acknowledge the generation")
	}
}

func TestAPI_ToggleRecommendToDirectWithoutAFileChange(t *testing.T) {
	t.Parallel()

	h := newAPIHarness(t, control.ApplyRecommend, "settings", nil)
	seedSettings(t, h)
	h.runAs("settings", workflows.RemediationDone)

	recommended := h.latestOf("settings")

	h.reapply(control.ApplyDirect, "settings")
	h.runAs("settings", workflows.RemediationDone)

	if got := h.st.rows[recommended.ID-1].State; got != store.StateSuperseded {
		t.Errorf("the recommendation is %q, want superseded", got)
	}

	row := h.latestOf("settings")
	if row.ID == recommended.ID || row.State != store.StateApplied {
		t.Fatalf("latest row = %+v, want a new applied row", row)
	}

	s := h.settings()
	if *s.HasWiki || !*s.DeleteBranchOnMerge || !*s.HasIssues || !*s.AllowSquashMerge {
		t.Errorf("settings = %+v, want the control's applied and the human's kept", s)
	}

	want := []string{"setting:vulnerability_alerts=done", "setting:delete_branch_on_merge=done", "setting:has_wiki=done"}
	if got := stepsOf(h, row.ID); !slices.Equal(got, want) {
		t.Errorf("journal = %v, want %v", got, want)
	}
}

func TestAPI_DirectFailsTheSecondOfThreeWritesAndResumes(t *testing.T) {
	t.Parallel()

	h := newAPIHarness(t, control.ApplyDirect, "settings", nil)
	seedSettings(t, h)
	h.repo.FailNext("UpdateRepository", errors.New("502 bad gateway"))

	if _, err := h.run("settings"); err == nil {
		t.Fatal("a partly failed apply returned no error")
	}

	row := h.latestOf("settings")
	want := []string{"setting:vulnerability_alerts=done", "setting:delete_branch_on_merge=failed", "setting:has_wiki=done"}

	if got := stepsOf(h, row.ID); row.State != store.StateFailed || !slices.Equal(got, want) {
		t.Fatalf("row %q with journal %v, want failed with %v", row.State, got, want)
	}

	h.runAs("settings", workflows.RemediationDone)

	if row := h.latestOf("settings"); row.State != store.StateApplied {
		t.Errorf("after the resume the row is %q, want applied", row.State)
	}

	writes := h.botWrites()
	if n := strings.Count(strings.Join(writes, ","), "SetVulnerabilityAlerts"); n != 1 {
		t.Errorf("the completed alerts write ran %d times, want once (writes %v)", n, writes)
	}

	if s := h.settings(); !*s.DeleteBranchOnMerge || *s.HasWiki || !*s.HasIssues {
		t.Errorf("settings = %+v", s)
	}
}

// mutatingWriter is a Writer under which a human edits the label right
// after each write: a concurrent change between write and read-back.
type mutatingWriter struct {
	control.Writer

	repo *ghfake.Repo
}

func (w mutatingWriter) UpsertLabel(ctx context.Context, l control.Label) error {
	if err := w.Writer.UpsertLabel(ctx, l); err != nil {
		return err
	}

	return w.repo.As(alice).UpsertLabel(ctx, control.Label{Name: l.Name, Color: "000000"})
}

func TestAPI_ConcurrentMutationFailsTheReadBack(t *testing.T) {
	t.Parallel()

	h := newAPIHarness(t, control.ApplyDirect, "labels", nil)
	ctx := context.Background()

	for _, l := range []control.Label{{Name: "needs-triage", Color: "ededed"}, {Name: "wontfix", Color: "ffffff"}} {
		if err := h.repo.As(alice).UpsertLabel(ctx, l); err != nil {
			t.Fatal(err)
		}
	}

	clients := h.r.clients.(*ghfakeRemediationClients) //nolint:forcetypeassert // the harness's clients
	clients.wrap = func(w control.Writer) control.Writer { return mutatingWriter{Writer: w, repo: h.repo} }

	if _, err := h.run("labels"); err == nil {
		t.Fatal("a read-back mismatch returned no error")
	}

	row := h.latestOf("labels")
	want := []string{"label:bug=failed", "label:wontfix=done"}

	if got := stepsOf(h, row.ID); row.State != store.StateFailed || !slices.Equal(got, want) {
		t.Fatalf("row %q with journal %v, want failed with %v", row.State, got, want)
	}

	clients.wrap = nil
	h.runAs("labels", workflows.RemediationDone)

	labels, _ := h.repo.As(alice).ListLabels(ctx)
	names := make([]string, 0, len(labels))

	for _, l := range labels {
		names = append(names, l.Name+":"+l.Color)
	}

	slices.Sort(names)

	if want := []string{"bug:d73a4a", "needs-triage:ededed"}; !slices.Equal(names, want) {
		t.Errorf("labels = %v, want %v", names, want)
	}
}

func TestAPI_RestoredAccessApplies(t *testing.T) {
	t.Parallel()

	h := newAPIHarness(t, control.ApplyDirect, "settings", nil)
	seedSettings(t, h)
	h.st.targets["settings"].Remediable = false
	h.runAs("settings", workflows.RemediationDone)

	if w := h.botWrites(); len(w) != 0 {
		t.Fatalf("wrote without access: %v", w)
	}

	// Access is restored: the intent moves and the control is due.
	h.st.targets["settings"].Remediable = true
	h.st.due("settings")
	h.runAs("settings", workflows.RemediationDone)

	if row := h.latestOf("settings"); row.State != store.StateApplied {
		t.Errorf("row = %q, want applied", row.State)
	}
}

func seedProps(t *testing.T, h *prHarness) {
	t.Helper()

	h.repo.SetSchema("Owner", "Component")

	if err := h.repo.As(alice).SetCustomProperties(context.Background(), map[string]*string{
		"Owner": strp("team-a"), "Component": strp("svc"), "Unmanaged": strp("keep"),
	}); err != nil {
		t.Fatal(err)
	}
}

const catalogInfo = `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: svc
spec:
  owner: team-b
`

func TestAPI_CatalogParseFailureNeverClearsProperties(t *testing.T) {
	t.Parallel()

	h := newAPIHarness(t, control.ApplyDirect, "props", map[string]string{"catalog-info.yaml": "::: not: [yaml"})
	seedProps(t, h)
	h.runAs("props", workflows.RemediationDone)

	if w := h.botWrites(); len(w) != 0 {
		t.Errorf("a malformed catalog-info.yaml led to writes: %v", w)
	}

	props, _ := h.repo.As(alice).GetCustomProperties(context.Background())
	if props["Owner"] == nil || *props["Owner"] != "team-a" {
		t.Errorf("Owner = %v, want it untouched", props["Owner"])
	}
}

func TestAPI_DirectPropertiesKeepUnmanagedState(t *testing.T) {
	t.Parallel()

	h := newAPIHarness(t, control.ApplyDirect, "props", map[string]string{"catalog-info.yaml": catalogInfo})
	seedProps(t, h)
	h.runAs("props", workflows.RemediationDone)

	props, _ := h.repo.As(alice).GetCustomProperties(context.Background())
	if *props["Owner"] != "team-b" || *props["Unmanaged"] != "keep" {
		t.Errorf("properties = Owner %q, Unmanaged %q; want team-b and keep", *props["Owner"], *props["Unmanaged"])
	}
}

func TestAPI_WorkflowApplyOpensAPullRequest(t *testing.T) {
	t.Parallel()

	h := newAPIHarness(t, control.ApplyWorkflow, "props", map[string]string{"catalog-info.yaml": catalogInfo})
	seedProps(t, h)
	h.runAs("props", workflows.RemediationDone)

	if w := h.botWrites(); slices.Contains(w, "SetCustomProperties") {
		t.Errorf("workflow apply wrote the properties directly: %v", w)
	}

	prs := h.openPRs()
	if len(prs) != 1 {
		t.Fatalf("open PRs = %v, want one", prs)
	}

	body, ok := h.repo.File(h.repo.Head(branchName("props")), ".github/workflows/repo-guardian-props.yml")
	if !ok || !strings.Contains(body, `"team-b"`) || !strings.Contains(body, "$RG_PROP_Owner") {
		t.Errorf("workflow file = %q, want the value only through the env block", body)
	}

	// The PR proposes the workflow once; a re-run adds no commit.
	h.st.due("props")
	h.runAs("props", workflows.RemediationDone)

	if n := h.botCommits(branchName("props")); n != 1 {
		t.Errorf("bot commits = %d, want 1", n)
	}
}

func TestAPI_WorkflowWithoutTheWorkflowsPermissionHolds(t *testing.T) {
	t.Parallel()

	h := newAPIHarness(t, control.ApplyWorkflow, "props", map[string]string{"catalog-info.yaml": catalogInfo})
	seedProps(t, h)
	h.repo.FailNext("Commit", fmt.Errorf("committing: %w", control.ErrPermission))

	if res := h.runAs("props", workflows.RemediationHeld); res.Hold != store.HoldPermission {
		t.Errorf("hold = %q, want permission", res.Hold)
	}
}

// TestAPI_ResumeWritesOnlyWhatIsNotJournaled is 5.3's resume rule: a
// resource the journal shows done is not written again by the resumed
// run, even when the fresh read proposes it again (a human reverted it);
// the next evaluation's generation takes that up as new work.
func TestAPI_ResumeWritesOnlyWhatIsNotJournaled(t *testing.T) {
	t.Parallel()

	h := newAPIHarness(t, control.ApplyDirect, "settings", nil)
	seedSettings(t, h)
	h.repo.FailNext("UpdateRepository", errors.New("502 bad gateway"))

	if _, err := h.run("settings"); err == nil {
		t.Fatal("a partly failed apply returned no error")
	}

	if err := h.repo.As(alice).SetVulnerabilityAlerts(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	h.runAs("settings", workflows.RemediationDone)

	if n := strings.Count(strings.Join(h.botWrites(), ","), "SetVulnerabilityAlerts"); n != 1 {
		t.Errorf("the journaled alerts write ran %d times, want once", n)
	}
}
