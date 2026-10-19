package github

// BotIdentity is a GitHub App's bot user: the author of every commit and
// pull request the App makes. Authorship is matched by ID, never by
// login (DESIGN-0032 § Pull requests).
type BotIdentity struct {
	// Slug is the App's slug; Login is "<slug>[bot]".
	Slug  string
	Login string
	ID    int64
}
