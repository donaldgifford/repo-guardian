package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
)

// Token verification policy (DESIGN-0027 § Authentication).
const (
	clockSkew = 60 * time.Second

	discoveryRetryFirst = time.Second
	discoveryRetryMax   = time.Minute
)

// supportedAlgs are the only signing algorithms accepted.
var supportedAlgs = []string{oidc.RS256, oidc.ES256}

// Auth failure reasons, the api_auth_failures_total label.
const (
	reasonMissing     = "missing"
	reasonMalformed   = "malformed"
	reasonAlgorithm   = "algorithm"
	reasonIssuer      = "issuer"
	reasonAudience    = "audience"
	reasonIDToken     = "id_token"
	reasonExpired     = "expired"
	reasonNotYetValid = "not_yet_valid"
	reasonSignature   = "signature"
	reasonUnavailable = "unavailable"
)

// AuthnConfig configures OIDC access-token verification.
type AuthnConfig struct {
	Issuer      string
	Audience    string
	NameClaim   string
	GroupsClaim string

	// Now overrides the clock in tests.
	Now func() time.Time
}

// Authenticator verifies bearer access tokens. Discovery runs in the
// background with retry; until it succeeds Ready is false and every
// request is refused with 503.
type Authenticator struct {
	cfg      AuthnConfig
	verifier atomic.Pointer[oidc.IDTokenVerifier]
	logger   *slog.Logger
}

// NewAuthenticator returns an Authenticator; call Start to discover.
func NewAuthenticator(cfg *AuthnConfig, logger *slog.Logger) *Authenticator {
	c := *cfg
	if c.Now == nil {
		c.Now = time.Now
	}

	return &Authenticator{cfg: c, logger: logger}
}

// Start runs OIDC discovery until it succeeds or ctx ends.
func (a *Authenticator) Start(ctx context.Context) {
	delay := discoveryRetryFirst

	for {
		provider, err := oidc.NewProvider(ctx, a.cfg.Issuer)
		if err == nil {
			a.verifier.Store(provider.Verifier(&oidc.Config{
				ClientID:             a.cfg.Audience,
				SupportedSigningAlgs: supportedAlgs,
				// exp and nbf are checked with our own 60s skew.
				SkipExpiryCheck: true,
			}))
			a.logger.Info("OIDC discovery complete", "issuer", a.cfg.Issuer)

			return
		}

		a.logger.Warn("OIDC discovery failed; retrying", "issuer", a.cfg.Issuer, "error", err, "retry_in", delay)

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		delay = min(2*delay, discoveryRetryMax)
	}
}

// Ready reports whether discovery has succeeded.
func (a *Authenticator) Ready() bool { return a.verifier.Load() != nil }

// authError is a refused token with its metric reason.
type authError struct{ reason string }

func (e *authError) Error() string { return "token refused: " + e.reason }

func refuse(reason string) error { return &authError{reason: reason} }

// Middleware authenticates every request it wraps.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := a.authenticate(r)
		if err != nil {
			reason := reasonMalformed

			var ae *authError
			if errors.As(err, &ae) {
				reason = ae.reason
			}

			metrics.APIAuthFailuresTotal.WithLabelValues(reason).Inc()

			if reason == reasonUnavailable {
				writeProblem(w, r, http.StatusServiceUnavailable, "authentication is not ready")

				return
			}

			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			if reason == reasonMissing {
				w.Header().Set("WWW-Authenticate", "Bearer")
			}

			writeProblem(w, r, http.StatusUnauthorized, "invalid bearer token: "+reason)

			return
		}

		next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), claims)))
	})
}

// tokenClaims are the claims read before and after verification.
type tokenClaims struct {
	Issuer    string   `json:"iss"`
	Subject   string   `json:"sub"`
	Audience  audience `json:"aud"`
	Expiry    *float64 `json:"exp"`
	NotBefore *float64 `json:"nbf"`
	AZP       string   `json:"azp"`
	Typ       string   `json:"typ"`
	Nonce     string   `json:"nonce"`
	AtHash    string   `json:"at_hash"`
}

// audience is aud as a string or an array.
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*a = audience{one}

		return nil
	}

	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}

	*a = many

	return nil
}

// authenticate classifies the token from its unverified header and
// claims first, so every refusal carries a precise reason, then has
// go-oidc verify the signature against the issuer's JWKS.
func (a *Authenticator) authenticate(r *http.Request) (*Claims, error) {
	verifier := a.verifier.Load()
	if verifier == nil {
		return nil, refuse(reasonUnavailable)
	}

	raw, ok := bearer(r)
	if !ok {
		return nil, refuse(reasonMissing)
	}

	header, payload, err := splitJWT(raw)
	if err != nil {
		return nil, refuse(reasonMalformed)
	}

	if !slices.Contains(supportedAlgs, header.Alg) {
		return nil, refuse(reasonAlgorithm)
	}

	var tc tokenClaims
	if err := json.Unmarshal(payload, &tc); err != nil {
		return nil, refuse(reasonMalformed)
	}

	if err := a.checkClaims(&tc); err != nil {
		return nil, err
	}

	tok, err := verifier.Verify(r.Context(), raw)
	if err != nil {
		return nil, refuse(reasonSignature)
	}

	var all map[string]any
	if err := tok.Claims(&all); err != nil {
		return nil, refuse(reasonMalformed)
	}

	return &Claims{
		Subject: tc.Subject,
		Client:  tc.AZP,
		Name:    stringClaim(all[a.cfg.NameClaim]),
		Groups:  stringsClaim(all[a.cfg.GroupsClaim]),
	}, nil
}

// checkClaims applies the claim checks go-oidc would, with 60s skew, and
// refuses ID tokens: the API takes access tokens only.
func (a *Authenticator) checkClaims(tc *tokenClaims) error {
	now := a.cfg.Now()

	switch {
	case tc.Issuer != a.cfg.Issuer:
		return refuse(reasonIssuer)
	case !slices.Contains(tc.Audience, a.cfg.Audience):
		return refuse(reasonAudience)
	case isIDToken(tc):
		return refuse(reasonIDToken)
	case tc.Expiry == nil || unix(*tc.Expiry).Add(clockSkew).Before(now):
		return refuse(reasonExpired)
	case tc.NotBefore != nil && unix(*tc.NotBefore).After(now.Add(clockSkew)):
		return refuse(reasonNotYetValid)
	}

	return nil
}

// isIDToken recognizes an OIDC ID token by what only ID tokens carry:
// a nonce or at_hash, or Keycloak's typ "ID".
func isIDToken(tc *tokenClaims) bool {
	return tc.Nonce != "" || tc.AtHash != "" || strings.EqualFold(tc.Typ, "ID")
}

func unix(sec float64) time.Time {
	return time.Unix(0, int64(sec*float64(time.Second)))
}

func bearer(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}

	return strings.TrimSpace(token), true
}

type jwtHeader struct {
	Alg string `json:"alg"`
}

// splitJWT decodes a compact JWS's header and payload without verifying.
func splitJWT(raw string) (*jwtHeader, []byte, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, nil, errors.New("not a compact JWS")
	}

	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, nil, err
	}

	var h jwtHeader
	if err := json.Unmarshal(hb, &h); err != nil {
		return nil, nil, err
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, nil, err
	}

	return &h, payload, nil
}

func stringClaim(v any) string {
	if s, ok := v.(string); ok {
		return s
	}

	return ""
}

// stringsClaim reads a groups claim given as an array or one string.
func stringsClaim(v any) []string {
	switch g := v.(type) {
	case string:
		return []string{g}
	case []any:
		out := make([]string, 0, len(g))
		for _, e := range g {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}

		return out
	default:
		return nil
	}
}
