package checker

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// gatedAbsentRule builds an absent rule forbidding .github/dependabot.yml
// gated on the referee named by refereeName.
func gatedAbsentRule(name, refereeName string) policy.FileRuleConfig {
	return policy.FileRuleConfig{
		Type:  "file",
		Name:  name,
		Check: string(policy.CheckAbsent),
		Paths: []string{".github/dependabot.yml"},
		When:  &policy.WhenConfig{RuleSatisfied: refereeName},
	}
}

// renovateExistsRule is an exists-mode referee on renovate.json.
func renovateExistsRule() policy.FileRuleConfig {
	return policy.FileRuleConfig{
		Type:     "file",
		Name:     "renovate_config",
		Check:    string(policy.CheckExists),
		Paths:    []string{"renovate.json"},
		Target:   "renovate.json",
		Template: "codeowners",
	}
}

// renovateContainsRule is a contains-mode referee whose assertion requires
// a team owner in spec.owner.
func renovateContainsRule() policy.FileRuleConfig {
	return policy.FileRuleConfig{
		Type:     "file",
		Name:     "renovate_config",
		Check:    string(policy.CheckContains),
		Paths:    []string{"renovate.json"},
		Target:   "renovate.json",
		Template: "codeowners",
		Assertions: []policy.AssertionConfig{
			{YAMLPath: "spec.owner", Contains: "team", Message: "must have team owner"},
		},
	}
}

func ruleNames(rules []policy.FileRuleConfig) []string {
	names := make([]string, 0, len(rules))
	for i := range rules {
		names = append(names, rules[i].Name)
	}

	return names
}

// TestFindActionableRules_SemanticsMatrix covers all five DESIGN-0020
// semantics-matrix rows for no_dependabot gated on renovate_config,
// asserting the actionable set and the gate-closed counter per row.
func TestFindActionableRules_SemanticsMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		owner           string
		referee         policy.FileRuleConfig
		refereePresent  bool
		refereeContent  string // fileContents for renovate.json when present
		dependabotThere bool
		wantActionable  []string
		wantGateReason  string // "" when the gate stays open
	}{
		{
			name:            "renovate satisfied, dependabot present -> actionable",
			owner:           "org-sm1",
			referee:         renovateExistsRule(),
			refereePresent:  true,
			dependabotThere: true,
			wantActionable:  []string{"no_dependabot"},
			wantGateReason:  "",
		},
		{
			name:            "renovate satisfied, dependabot absent -> converged",
			owner:           "org-sm2",
			referee:         renovateExistsRule(),
			refereePresent:  true,
			dependabotThere: false,
			wantActionable:  nil,
			wantGateReason:  "",
		},
		{
			name:            "renovate missing, dependabot present -> gate closed",
			owner:           "org-sm3",
			referee:         renovateExistsRule(),
			refereePresent:  false,
			dependabotThere: true,
			wantActionable:  []string{"renovate_config"},
			wantGateReason:  gateReasonNotSatisfied,
		},
		{
			name:            "renovate assertion fails -> gate closed",
			owner:           "org-sm4",
			referee:         renovateContainsRule(),
			refereePresent:  true,
			refereeContent:  "spec:\n  owner: nobody\n",
			dependabotThere: true,
			wantActionable:  []string{"renovate_config"},
			wantGateReason:  gateReasonNotSatisfied,
		},
		{
			name:            "renovate missing, dependabot absent -> gate closed",
			owner:           "org-sm5",
			referee:         renovateExistsRule(),
			refereePresent:  false,
			dependabotThere: false,
			wantActionable:  []string{"renovate_config"},
			wantGateReason:  gateReasonNotSatisfied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &policy.PolicyConfig{
				Guardian: policy.BuiltinDefaults().Guardian,
				FileRules: []policy.FileRuleConfig{
					tt.referee,
					gatedAbsentRule("no_dependabot", "renovate_config"),
				},
			}

			engine := testPolicyEngine(cfg)
			client := newMockClient()

			if tt.refereePresent {
				client.contents[tt.owner+"/repo/renovate.json"] = true
				client.fileContents[tt.owner+"/repo/renovate.json"] = tt.refereeContent
			}

			if tt.dependabotThere {
				client.contents[tt.owner+"/repo/.github/dependabot.yml"] = true
			}

			gate := newGateEvaluator(engine, client, tt.owner, "repo")

			actionable, err := engine.findActionableRules(
				context.Background(), slog.Default(), client, tt.owner, "repo", nil, gate,
			)
			if err != nil {
				t.Fatalf("findActionableRules: %v", err)
			}

			if got := ruleNames(actionable); !equalStrings(got, tt.wantActionable) {
				t.Errorf("actionable = %v, want %v", got, tt.wantActionable)
			}

			assertGateClosed(t, tt.owner, tt.wantGateReason)
		})
	}
}

// assertGateClosed checks the RuleGateClosedTotal counter for no_dependabot
// under owner: exactly one increment on wantReason, and zero on the other
// reason. When wantReason is "", both reasons must be zero.
func assertGateClosed(t *testing.T, owner, wantReason string) {
	t.Helper()

	for _, reason := range []string{gateReasonNotSatisfied, gateReasonError} {
		want := 0.0
		if reason == wantReason {
			want = 1.0
		}

		got := testutil.ToFloat64(metrics.RuleGateClosedTotal.WithLabelValues("no_dependabot", owner, reason))
		if got != want {
			t.Errorf("RuleGateClosedTotal{no_dependabot, %s, %s} = %v, want %v", owner, reason, got, want)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

// TestGateOpen_RefereeError_FailsClosed is the IMPL-0019 task 1.5 fail-closed
// guard: when evaluating the referee errors, the gate closes with
// reason="error" rather than opening — a destructive gated action must never
// proceed from an unknown referee state.
func TestGateOpen_RefereeError_FailsClosed(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{
			renovateContainsRule(),
			gatedAbsentRule("no_dependabot", "renovate_config"),
		},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.contents["org/repo/renovate.json"] = true    // referee path exists...
	client.getFileContentErr = errors.New("api glitch") // ...but content fetch errors

	gate := newGateEvaluator(engine, client, "org", "repo")
	gatedRule := &cfg.FileRules[1]

	open, reason := gate.gateOpen(context.Background(), slog.Default(), gatedRule)
	if open {
		t.Error("gate must be closed when referee evaluation errors")
	}

	if reason != gateReasonError {
		t.Errorf("gate reason = %q, want %q", reason, gateReasonError)
	}
}

// TestGateOpen_MissingReferee_FailsClosed guards the runtime invariant path:
// load validation guarantees the referee exists and is enabled, so a missing
// referee at engine time is an invariant violation that must fail closed with
// reason="error", never panic.
func TestGateOpen_MissingReferee_FailsClosed(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian:  policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{gatedAbsentRule("no_dependabot", "does_not_exist")},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	gate := newGateEvaluator(engine, client, "org", "repo")

	open, reason := gate.gateOpen(context.Background(), slog.Default(), &cfg.FileRules[0])
	if open || reason != gateReasonError {
		t.Errorf("gateOpen = (%v, %q), want (false, %q)", open, reason, gateReasonError)
	}
}

// countingClient counts referee content fetches to prove per-repo-check
// memoization bounds them.
type countingClient struct {
	*mockClient
	getFileContentCalls atomic.Int32
}

func (c *countingClient) GetFileContent(ctx context.Context, owner, repo, path string) (string, error) {
	c.getFileContentCalls.Add(1)

	return c.mockClient.GetFileContent(ctx, owner, repo, path)
}

// TestGate_Memoization_OneRefereeEvalPerRepoCheck asserts that two gated
// rules sharing one contains-mode referee, evaluated across both engine
// passes, trigger exactly one gate-side referee content fetch: the referee's
// own evaluation fetches once, and the memoized gate adds exactly one more
// regardless of how many gated rules reference it or how many passes run.
func TestGate_Memoization_OneRefereeEvalPerRepoCheck(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{
			renovateContainsRule(),
			gatedAbsentRule("no_dependabot_a", "renovate_config"),
			gatedAbsentRule("no_dependabot_b", "renovate_config"),
		},
	}

	engine := testPolicyEngine(cfg)
	base := newMockClient()
	base.contents["org/repo/renovate.json"] = true
	base.fileContents["org/repo/renovate.json"] = "spec:\n  owner: team-a\n" // assertion passes
	client := &countingClient{mockClient: base}

	gate := newGateEvaluator(engine, client, "org", "repo")

	// Both passes share the same gate, as in checkRepoWithPolicy.
	if _, err := engine.findActionableRules(
		context.Background(), slog.Default(), client, "org", "repo", nil, gate,
	); err != nil {
		t.Fatalf("findActionableRules: %v", err)
	}

	engine.runReconcilers(context.Background(), slog.Default(), client, "org", "repo", "main", nil, gate)

	// 1 fetch for the referee's own evaluation + 1 memoized gate fetch = 2.
	// Without memoization it would be 5 (own + gate per gated rule per pass).
	if got := client.getFileContentCalls.Load(); got != 2 {
		t.Errorf("referee GetFileContent calls = %d, want 2 (memoized)", got)
	}
}

// TestFindActionableRules_GateOpenDespiteOpenRefereePR pins the short-circuit
// distinction: ruleSatisfiedOnDefault omits the foreignPRForRule
// short-circuit, so an open PR matching the referee's search terms — which
// makes the referee's OWN evaluation short-circuit to not-actionable — does
// NOT flip the gate closed. The gated rule still fires because the referee is
// satisfied on the default branch.
func TestFindActionableRules_GateOpenDespiteOpenRefereePR(t *testing.T) {
	t.Parallel()

	const owner = "org-oprpr"

	referee := renovateExistsRule()
	referee.PR = &policy.PRConfig{SearchTerms: []string{"add renovate"}}

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{
			referee,
			gatedAbsentRule("no_dependabot", "renovate_config"),
		},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.contents[owner+"/repo/renovate.json"] = true          // referee satisfied on default
	client.contents[owner+"/repo/.github/dependabot.yml"] = true // forbidden file present
	openPRs := []*ghclient.PullRequest{
		{Number: 7, Title: "Add renovate config", Head: "add-renovate", State: "open"},
	}

	gate := newGateEvaluator(engine, client, owner, "repo")

	actionable, err := engine.findActionableRules(
		context.Background(), slog.Default(), client, owner, "repo", openPRs, gate,
	)
	if err != nil {
		t.Fatalf("findActionableRules: %v", err)
	}

	// The referee short-circuits on its open PR (not actionable), but the gate
	// stays open and no_dependabot fires.
	if got := ruleNames(actionable); !equalStrings(got, []string{"no_dependabot"}) {
		t.Errorf("actionable = %v, want [no_dependabot]", got)
	}

	assertGateClosed(t, owner, "")
}
