package ghapi

import (
	"time"
)

// PullRequest is the subset of a pull request rgctl reads.
type PullRequest struct {
	// Repo is owner/name.
	Repo   string
	Number int
	URL    string
	// State is "open" or "closed".
	State  string
	Title  string
	Author string
	// HeadRef is the head branch name.
	HeadRef string
	HeadSHA string
	// HeadRepoID and BaseRepoID differ when the head is a fork.
	HeadRepoID int64
	BaseRepoID int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// SearchHit is one search result: a pull request to read back through the
// Pull Requests API before it is trusted.
type SearchHit struct {
	Repo   string
	Number int
}

// SearchStatus reports how complete a search was.
type SearchStatus struct {
	// Total is the total_count the index reported.
	Total int
	// Pages is the number of search requests made.
	Pages int
	// Incomplete is true when any page reported incomplete_results.
	Incomplete bool
	// Capped is true when the index holds more than the 1000 results search
	// can return.
	Capped bool
}

// Commit is one commit on a pull request. A login is empty when GitHub could
// not resolve the commit's author or committer to an account.
type Commit struct {
	AuthorLogin    string
	CommitterLogin string
}

// Comment is one issue comment on a pull request.
type Comment struct {
	ID     int64
	Author string
	Body   string
}

// Installation is one installation of the App.
type Installation struct {
	ID      int64
	Account string
}
