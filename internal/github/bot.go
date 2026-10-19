package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	gh "github.com/google/go-github/v68/github"
)

// BotIdentity is a GitHub App's bot user: the author of every commit and
// pull request the App makes. Authorship is matched by ID, never by
// login (DESIGN-0032 § Pull requests).
type BotIdentity struct {
	// Slug is the App's slug; Login is "<slug>[bot]".
	Slug  string
	Login string
	ID    int64
}

// UserTypeBot is the user type GitHub gives an App's bot account.
const UserTypeBot = "Bot"

// ErrNotABot means the App's "<slug>[bot]" user is not a Bot account.
var ErrNotABot = errors.New("app bot user is not a Bot")

// ResolveBotIdentity resolves the App's bot user (IMPL-0030 3.7): GET /app
// with the App's JWT for its slug, then GET /users/<slug>[bot] for the id,
// which must have type Bot. /users is not an App endpoint and refuses a
// JWT, so that read goes out unauthenticated through the same
// instrumented transport and base URL. Call it on the App client, never
// an installation-scoped one.
func (c *GitHubClient) ResolveBotIdentity(ctx context.Context) (BotIdentity, error) {
	app, _, err := c.appClient.Apps.Get(ctx, "")
	if err != nil {
		return BotIdentity{}, fmt.Errorf("get app: %w", err)
	}

	if app.GetSlug() == "" {
		return BotIdentity{}, errors.New("get app: no slug")
	}

	users := gh.NewClient(instrumentedClient(http.DefaultTransport))
	users.BaseURL = c.appClient.BaseURL

	login := app.GetSlug() + "[bot]"

	u, _, err := users.Users.Get(ctx, login)
	if err != nil {
		return BotIdentity{}, fmt.Errorf("get user %s: %w", login, err)
	}

	if u.GetType() != UserTypeBot || u.GetID() == 0 {
		return BotIdentity{}, fmt.Errorf("%w: %s has type %q", ErrNotABot, login, u.GetType())
	}

	return BotIdentity{Slug: app.GetSlug(), Login: u.GetLogin(), ID: u.GetID()}, nil
}
