package github

import (
	"net/url"
	"testing"

	gh "github.com/google/go-github/v68/github"
)

func TestGraphQLURL(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ base, want string }{
		{"https://api.github.com/", "https://api.github.com/graphql"},
		{"https://ghe.example.com/api/v3/", "https://ghe.example.com/api/graphql"},
		{"http://127.0.0.1:4000/api/v3/", "http://127.0.0.1:4000/api/graphql"},
	} {
		u, err := url.Parse(tt.base)
		if err != nil {
			t.Fatal(err)
		}

		c := gh.NewClient(nil)
		c.BaseURL = u

		if got := graphQLURL(c); got != tt.want {
			t.Errorf("graphQLURL(%s) = %s, want %s", tt.base, got, tt.want)
		}
	}
}
