package prs

import (
	"slices"
	"testing"

	"github.com/donaldgifford/repo-guardian/tools/rgctl/internal/ghapi"
)

const bot = "repo-guardian[bot]"

func TestIdentity_Match(t *testing.T) {
	t.Parallel()
	ours := ghapi.PullRequest{State: "open", Author: bot, HeadRef: BranchAddMissingFiles, HeadRepoID: 1, BaseRepoID: 1}
	with := func(f func(*ghapi.PullRequest)) ghapi.PullRequest { pr := ours; f(&pr); return pr }

	tests := []struct {
		name       string
		id         Identity
		pr         ghapi.PullRequest
		want       bool
		wantReason string
	}{
		{name: "ours", id: Identity{BotLogin: bot, Prefix: DefaultPrefix}, pr: ours, want: true},
		{
			name: "author case differs",
			id:   Identity{BotLogin: bot, Prefix: DefaultPrefix},
			pr:   with(func(p *ghapi.PullRequest) { p.Author = "Repo-Guardian[BOT]" }),
			want: true,
		},
		{
			name:       "closed",
			id:         Identity{BotLogin: bot, Prefix: DefaultPrefix},
			pr:         with(func(p *ghapi.PullRequest) { p.State = "closed" }),
			wantReason: ReasonClosed,
		},
		{
			name:       "human author",
			id:         Identity{BotLogin: bot, Prefix: DefaultPrefix},
			pr:         with(func(p *ghapi.PullRequest) { p.Author = "octocat" }),
			wantReason: ReasonAuthor,
		},
		{
			name:       "other bot",
			id:         Identity{BotLogin: bot, Prefix: DefaultPrefix},
			pr:         with(func(p *ghapi.PullRequest) { p.Author = "renovate[bot]" }),
			wantReason: ReasonAuthor,
		},
		{
			name:       "branch outside prefix",
			id:         Identity{BotLogin: bot, Prefix: DefaultPrefix},
			pr:         with(func(p *ghapi.PullRequest) { p.HeadRef = "feature/x" }),
			wantReason: ReasonBranch,
		},
		{
			name:       "prefix needs the slash",
			id:         Identity{BotLogin: bot, Prefix: DefaultPrefix},
			pr:         with(func(p *ghapi.PullRequest) { p.HeadRef = "repo-guardian-x" }),
			wantReason: ReasonBranch,
		},
		{
			name:       "fork head",
			id:         Identity{BotLogin: bot, Prefix: DefaultPrefix},
			pr:         with(func(p *ghapi.PullRequest) { p.HeadRepoID = 2 }),
			wantReason: ReasonFork,
		},
		{name: "exact branch listed", id: Identity{BotLogin: bot, Branches: []string{BranchAddMissingFiles}}, pr: ours, want: true},
		{name: "exact branch not listed", id: Identity{BotLogin: bot, Branches: []string{BranchAddCatalogInfo}}, pr: ours, wantReason: ReasonBranch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, reason := tt.id.Match(&tt.pr)
			if got != tt.want || reason != tt.wantReason {
				t.Errorf("Match(%+v) = %v, %q, want %v, %q", tt.pr, got, reason, tt.want, tt.wantReason)
			}
		})
	}
}

func TestIdentity_ForeignLogins(t *testing.T) {
	t.Parallel()
	id := Identity{BotLogin: bot}
	tests := []struct {
		name   string
		commit ghapi.Commit
		want   []string
	}{
		{name: "bot commit", commit: ghapi.Commit{AuthorLogin: bot, CommitterLogin: bot}},
		{name: "human author", commit: ghapi.Commit{AuthorLogin: "octocat", CommitterLogin: bot}, want: []string{"octocat"}},
		{name: "bot author, web-flow committer", commit: ghapi.Commit{AuthorLogin: bot, CommitterLogin: "web-flow"}},
		{name: "human web edit", commit: ghapi.Commit{AuthorLogin: "octocat", CommitterLogin: "web-flow"}, want: []string{"octocat"}},
		{name: "unresolved author, web-flow committer", commit: ghapi.Commit{CommitterLogin: "web-flow"}, want: []string{unresolvedCommitAuthor}},
		{name: "unresolved author, bot committer", commit: ghapi.Commit{CommitterLogin: bot}},
		{name: "unresolved author, human committer", commit: ghapi.Commit{CommitterLogin: "octocat"}, want: []string{"octocat"}},
		{name: "nothing resolved", commit: ghapi.Commit{}, want: []string{unresolvedCommitAuthor}},
	}
	for _, tt := range tests {
		if got := id.foreignLogins(tt.commit); !slices.Equal(got, tt.want) {
			t.Errorf("%s: foreignLogins(%+v) = %v, want %v", tt.name, tt.commit, got, tt.want)
		}
	}
}

func TestIdentity_SearchHeads(t *testing.T) {
	t.Parallel()
	if got := (&Identity{Prefix: DefaultPrefix}).SearchHeads(); !slices.Equal(got, []string{DefaultPrefix}) {
		t.Errorf("SearchHeads() = %v, want the prefix", got)
	}
	branches := []string{BranchAddCatalogInfo, BranchSetCustomProps}
	if got := (&Identity{Prefix: DefaultPrefix, Branches: branches}).SearchHeads(); !slices.Equal(got, branches) {
		t.Errorf("SearchHeads() = %v, want the exact branches", got)
	}
}
