package github_test

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
)

// botServer answers GET /app with slug and GET /users/<slug>[bot] with
// the given type.
func botServer(t *testing.T, slug, userType string) *ghclient.GitHubClient {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/app", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"id":1,"slug":%q}`, slug)
	})
	mux.HandleFunc("GET /api/v3/users/{login}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "a JWT is refused here", http.StatusUnauthorized)

			return
		}

		if r.PathValue("login") != slug+"[bot]" {
			http.NotFound(w, r)

			return
		}

		fmt.Fprintf(w, `{"id":42,"login":%q,"type":%q}`, slug+"[bot]", userType)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c, err := ghclient.NewClientForBaseURL(srv.URL+"/api/v3", nil, slog.New(slog.DiscardHandler), 0.1)
	if err != nil {
		t.Fatal(err)
	}

	return c
}

// TestResolveBotIdentity is IMPL-0030 3.7: the slug from GET /app, then
// the id from GET /users/<slug>[bot], which must be a Bot.
func TestResolveBotIdentity(t *testing.T) {
	t.Parallel()

	got, err := botServer(t, "rg-remediate", ghclient.UserTypeBot).ResolveBotIdentity(t.Context())
	if err != nil {
		t.Fatalf("ResolveBotIdentity = %v", err)
	}

	if want := (ghclient.BotIdentity{Slug: "rg-remediate", Login: "rg-remediate[bot]", ID: 42}); got != want {
		t.Errorf("ResolveBotIdentity = %+v, want %+v", got, want)
	}

	if _, err := botServer(t, "rg-remediate", "User").ResolveBotIdentity(t.Context()); !errors.Is(err, ghclient.ErrNotABot) {
		t.Errorf("ResolveBotIdentity(type User) = %v, want ErrNotABot", err)
	}
}
