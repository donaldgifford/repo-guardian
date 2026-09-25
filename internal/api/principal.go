// Package api is the read-only API role (DESIGN-0027): an oapi-codegen
// strict server behind OIDC authentication and org-scoped
// authorization. It holds no GitHub client and never writes; every read
// is scoped in SQL by the caller's visible orgs.
package api

import (
	"context"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// Claims are the verified token claims the API uses.
type Claims struct {
	Subject string
	// Client is the azp of the token; the authz clients mapping keys on it.
	Client string
	Name   string
	Groups []string
}

// Principal is an authenticated, authorized caller.
type Principal struct {
	Claims

	// Visible is the orgs the caller may see.
	Visible store.APIScope
}

type (
	claimsKey    struct{}
	principalKey struct{}
)

func withClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsKey{}, c)
}

func claimsFrom(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(claimsKey{}).(*Claims)

	return c, ok
}

func withPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// principalFrom returns the request's principal. Handlers behind the
// authz middleware always have one; a missing one is a wiring bug and
// the handler must fail, never default to a wider scope.
func principalFrom(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(*Principal)

	return p, ok
}
