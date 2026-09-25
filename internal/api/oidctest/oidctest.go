// Package oidctest is a test OIDC issuer (IMPL-0025 OQ21): discovery,
// a JWKS, and a minter for every token shape the API must accept or
// refuse. It is hand-written so each malformed case is exactly the one
// named, which a general-purpose mock issuer makes hard to mint.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

const keyID = "oidctest-1"

// Issuer is a running test issuer. Its tokens are for Audience.
type Issuer struct {
	URL      string
	Audience string

	key   *rsa.PrivateKey
	rogue *rsa.PrivateKey
	now   func() time.Time
}

// Start runs an issuer for audience until the test ends.
func Start(tb testing.TB, audience string) *Issuer {
	tb.Helper()

	iss := &Issuer{Audience: audience, key: newKey(tb), rogue: newKey(tb), now: time.Now}

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	tb.Cleanup(srv.Close)

	iss.URL = srv.URL

	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                iss.URL,
			"jwks_uri":                              iss.URL + "/keys",
			"authorization_endpoint":                iss.URL + "/authorize",
			"token_endpoint":                        iss.URL + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: &iss.key.PublicKey, KeyID: keyID, Algorithm: string(jose.RS256), Use: "sig",
		}}})
	})

	return iss
}

func newKey(tb testing.TB) *rsa.PrivateKey {
	tb.Helper()

	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		tb.Fatalf("oidctest: generate key: %v", err)
	}

	return k
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// Token describes a token to mint. Zero fields take a valid default.
type Token struct {
	Subject  string
	Name     string
	Groups   []string
	NoGroups bool
	Client   string

	Issuer   string
	Audience string
	Expiry   time.Time
	// NotBefore is omitted when zero.
	NotBefore time.Time

	// IDToken shapes the token as an OIDC ID token (nonce, at_hash,
	// typ "ID").
	IDToken bool
	// Rogue signs with a key the issuer never published.
	Rogue bool
	// HMAC signs with HS256, an algorithm the API refuses.
	HMAC bool
}

// Mint signs t.
func (i *Issuer) Mint(tb testing.TB, t *Token) string {
	tb.Helper()

	claims := map[string]any{
		"iss":                orDefault(t.Issuer, i.URL),
		"aud":                orDefault(t.Audience, i.Audience),
		"sub":                orDefault(t.Subject, "user-1"),
		"exp":                expiry(t.Expiry, i.now()).Unix(),
		"iat":                i.now().Unix(),
		"preferred_username": orDefault(t.Name, "Test User"),
	}

	if !t.NoGroups {
		groups := t.Groups
		if groups == nil {
			groups = []string{}
		}

		claims["groups"] = groups
	}

	if t.Client != "" {
		claims["azp"] = t.Client
	}

	if !t.NotBefore.IsZero() {
		claims["nbf"] = t.NotBefore.Unix()
	}

	if t.IDToken {
		claims["nonce"], claims["at_hash"], claims["typ"] = "n-0S6_WzA2Mj", "77QmUPtjPfzWtF2AnpK9RQ", "ID"
	}

	key := jose.SigningKey{Algorithm: jose.RS256, Key: i.key}

	switch {
	case t.Rogue:
		key.Key = i.rogue
	case t.HMAC:
		key = jose.SigningKey{Algorithm: jose.HS256, Key: []byte("0123456789abcdef0123456789abcdef")}
	}

	signer, err := jose.NewSigner(key, (&jose.SignerOptions{}).WithType("at+jwt").WithHeader("kid", keyID))
	if err != nil {
		tb.Fatalf("oidctest: signer: %v", err)
	}

	payload, err := json.Marshal(claims)
	if err != nil {
		tb.Fatalf("oidctest: claims: %v", err)
	}

	jws, err := signer.Sign(payload)
	if err != nil {
		tb.Fatalf("oidctest: sign: %v", err)
	}

	raw, err := jws.CompactSerialize()
	if err != nil {
		tb.Fatalf("oidctest: serialize: %v", err)
	}

	return raw
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

func expiry(t, now time.Time) time.Time {
	if t.IsZero() {
		return now.Add(time.Hour)
	}

	return t
}

// Valid mints a valid access token carrying groups.
func (i *Issuer) Valid(tb testing.TB, groups ...string) string {
	tb.Helper()

	return i.Mint(tb, &Token{Groups: groups})
}

// Expired mints a token that expired well past the clock skew.
func (i *Issuer) Expired(tb testing.TB) string {
	tb.Helper()

	return i.Mint(tb, &Token{Expiry: i.now().Add(-10 * time.Minute)})
}

// WrongAudience mints a token for another audience.
func (i *Issuer) WrongAudience(tb testing.TB) string {
	tb.Helper()

	return i.Mint(tb, &Token{Audience: "someone-else"})
}

// WrongIssuer mints a token naming another issuer.
func (i *Issuer) WrongIssuer(tb testing.TB) string {
	tb.Helper()

	return i.Mint(tb, &Token{Issuer: "https://issuer.invalid"})
}

// BadSignature mints a token signed by an unpublished key.
func (i *Issuer) BadSignature(tb testing.TB) string {
	tb.Helper()

	return i.Mint(tb, &Token{Rogue: true})
}

// IDTokenShaped mints an ID token for the API's audience.
func (i *Issuer) IDTokenShaped(tb testing.TB) string {
	tb.Helper()

	return i.Mint(tb, &Token{IDToken: true})
}

// NoGroups mints a valid token with no groups claim at all.
func (i *Issuer) NoGroups(tb testing.TB) string {
	tb.Helper()

	return i.Mint(tb, &Token{NoGroups: true})
}
