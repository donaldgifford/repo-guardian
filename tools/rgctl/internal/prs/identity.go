// Package prs decides which pull requests are repo-guardian's, classifies
// them, records them, and closes exactly the recorded ones.
//
// Identity is author plus branch (DESIGN-0035 D1): a pull request is
// repo-guardian's when it is open, the App's bot login authored it, its head
// branch starts with repo-guardian/ (or equals a --branch name), and its head
// is not a fork. Titles are operator-templated and never consulted.
package prs

import (
	"strings"

	"github.com/donaldgifford/repo-guardian/tools/rgctl/internal/ghapi"
)

// v1's PR identity, copied as literals because rgctl imports nothing from
// the product module. Sources, frozen by TestPRIdentity_IsFrozen there:
//   - BranchName: internal/checker/engine.go
//   - CatalogInfoBranchName, PropertiesBranchName:
//     internal/reconciler/custom_properties.go
//   - reconcileLogMarker: internal/checker/drift.go
const (
	// DefaultPrefix is the head-branch prefix every repo-guardian branch
	// shares, v1 and v2 alike.
	DefaultPrefix = "repo-guardian/"

	BranchAddMissingFiles  = "repo-guardian/add-missing-files"
	BranchAddCatalogInfo   = "repo-guardian/add-catalog-info"
	BranchSetCustomProps   = "repo-guardian/set-custom-properties"
	ReconcileLogMarker     = "<!-- repo-guardian:reconcile-log:v1 -->"
	unresolvedCommitAuthor = "(unresolved)"
)

// stateOpen is the state of an open pull request.
const stateOpen = "open"

// Reasons a pull request is not repo-guardian's.
const (
	ReasonClosed = "closed"
	ReasonAuthor = "author is not the bot"
	ReasonBranch = "head branch does not match"
	ReasonFork   = "head is a fork"
)

// Identity is the rule a pull request must satisfy.
type Identity struct {
	// BotLogin is <slug>[bot], compared case-insensitively.
	BotLogin string
	// Prefix is the head-branch prefix, normally DefaultPrefix. Ignored when
	// Branches is set.
	Prefix string
	// Branches, when set, narrows the rule to exact head-branch names.
	Branches []string
}

// Match reports whether pr is repo-guardian's, and why not when it is not.
func (id *Identity) Match(pr *ghapi.PullRequest) (ok bool, reason string) {
	switch {
	case pr.State != stateOpen:
		return false, ReasonClosed
	case !id.isBot(pr.Author):
		return false, ReasonAuthor
	case !id.branchMatches(pr.HeadRef):
		return false, ReasonBranch
	case pr.HeadRepoID != pr.BaseRepoID:
		return false, ReasonFork
	}
	return true, ""
}

// SearchHeads is the head: qualifier values to search with: the prefix, or
// each exact branch (head: is a prefix match, so it finds the exact name).
func (id *Identity) SearchHeads() []string {
	if len(id.Branches) > 0 {
		return id.Branches
	}
	return []string{id.Prefix}
}

func (id *Identity) branchMatches(ref string) bool {
	if len(id.Branches) > 0 {
		for _, b := range id.Branches {
			if ref == b {
				return true
			}
		}
		return false
	}
	return strings.HasPrefix(ref, id.Prefix)
}

func (id *Identity) isBot(login string) bool {
	return login != "" && strings.EqualFold(login, id.BotLogin)
}

// foreignLogins returns the non-bot accounts on a commit, empty when the
// commit is the bot's own. A commit whose author GitHub could not resolve
// counts as foreign unless the bot committed it.
func (id *Identity) foreignLogins(c ghapi.Commit) []string {
	var out []string
	if c.AuthorLogin != "" && !id.isBot(c.AuthorLogin) {
		out = append(out, c.AuthorLogin)
	}
	if c.CommitterLogin != "" && !id.isBot(c.CommitterLogin) && c.CommitterLogin != c.AuthorLogin {
		out = append(out, c.CommitterLogin)
	}
	if c.AuthorLogin == "" && !id.isBot(c.CommitterLogin) && len(out) == 0 {
		out = append(out, unresolvedCommitAuthor)
	}
	return out
}
