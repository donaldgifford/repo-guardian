// Package api is the API role (DESIGN-0027, DESIGN-0032 § API): an
// oapi-codegen strict server behind OIDC authentication and org-scoped
// authorization over the controls schema. It holds no GitHub client and
// writes nothing to the store; every read is scoped in SQL by the
// caller's visible orgs, and an org or repository outside them answers
// 404, exactly like an unknown one. /policies is the one view filtered
// in Go (policiesOut), because the policy summary is stored whole.
//
// Every compliance object is a Posture. Totals summed across compliance
// rows go through store.ControlPosture.Plus, which recomputes percent
// and coverage from the summed counts with store.FloorPercent, the
// floor_percent SQL function.
//
// The one write is POST /repositories/{id}/evaluate: a Rechecker signals
// the evaluation workflow, deduplicated per repository per minute in
// each replica. Without a Rechecker (no Temporal client) it answers 501.
//
// Lists page by keyset: the cursor is base64url JSON of the last row's
// sort key plus a hash of the request's filters, so a cursor replayed
// under different filters is refused 400.
//
// The status page (StatusPage) is computed every 30s by a refresher into
// an in-process cache; a request never queries. Its schema carries only
// enums, counts and fixed detail templates, never names or error text.
package api

import (
	"context"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// Claims are the verified token claims the API uses.
type Claims struct {
	Subject string
	// Client is the value of the claim OIDC_CLIENT_CLAIM names (azp by
	// default), empty when the token lacks it; authz clients keys on it.
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
