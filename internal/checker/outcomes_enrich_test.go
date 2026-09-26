package checker

import (
	"context"
	"testing"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// TestReconcilerPass_RecordsNoOutcome pins the double-iteration contract
// for outcomes: runReconcilers walks the file rules a second time, and
// only the primary pass may record. Both an applying rule (reconciler
// runs) and an ignored one (reconcilerRuleApplies short-circuits) must
// appear exactly once.
func TestReconcilerPass_RecordsNoOutcome(t *testing.T) {
	t.Parallel()

	rec := &trackingReconciler{name: "test_rec"}
	withRec := func(name string, ignore *policy.IgnoreConfig) policy.FileRuleConfig {
		return policy.FileRuleConfig{
			Type: "file", Name: name, Paths: []string{name + ".txt"}, Target: name + ".txt",
			Template: "codeowners", Check: "exists", Ignore: ignore,
			Reconcilers: []policy.ReconcilerConfig{{Type: "test_rec"}},
		}
	}
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{
			withRec("applies", nil),
			withRec("ignored", &policy.IgnoreConfig{Repos: []string{"org/repo"}}),
		},
	}

	engine := testPolicyEngineWithReconciler(cfg, rec)
	client := newMockClient()
	client.repo = &ghclient.Repository{Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main"}
	client.contents["org/repo/applies.txt"] = true
	client.fileContents["org/repo/applies.txt"] = "content"

	res, err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if rec.callCount() != 1 {
		t.Fatalf("reconciler calls = %d, want 1 (the pass under test must have run)", rec.callCount())
	}

	assertOutcomes(t, res, []string{
		"file/applies=satisfied",
		"file/ignored=not_applicable:ignored_rule",
	})
}

// TestCheckRepo_CarriesRepositoryIdentity pins that the provider id and
// canonical name from GetRepository ride on every non-nil result,
// including repository-level skips.
func TestCheckRepo_CarriesRepositoryIdentity(t *testing.T) {
	t.Parallel()

	for _, hasBranch := range []bool{true, false} {
		client := newMockClient()
		client.repo = &ghclient.Repository{ID: 42, Owner: "Org", Name: "Repo", HasBranch: hasBranch, DefaultRef: "main"}
		client.branchSHAs["org/repo/main"] = "abc123"

		if !hasBranch {
			client.repo.DefaultRef = ""
		}

		res, err := testPolicyEngine(policy.BuiltinDefaults()).CheckRepo(context.Background(), client, "org", "repo")
		if err != nil {
			t.Fatalf("CheckRepo: %v", err)
		}

		want := RepositoryIdentity{ID: 42, Owner: "Org", Name: "Repo"}
		if res.Repository == nil || *res.Repository != want {
			t.Errorf("hasBranch=%v: Repository = %+v, want %+v", hasBranch, res.Repository, want)
		}
	}
}
