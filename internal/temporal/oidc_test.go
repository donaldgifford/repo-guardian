package temporal

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// tokenServer is a client-credentials endpoint that counts requests and
// checks what they carry.
func tokenServer(t *testing.T, expiresIn int) (url string, requests *atomic.Int32) {
	t.Helper()

	requests = &atomic.Int32{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)

		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}

		id, secret, ok := r.BasicAuth()
		if !ok {
			id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
		}

		if got := r.PostForm.Get("grant_type"); got != "client_credentials" {
			t.Errorf("grant_type = %q", got)
		}

		if id != "repo-guardian" || secret != "s3cret" {
			t.Errorf("client credentials = %q/%q, want repo-guardian/s3cret (secret file trimmed)", id, secret)
		}

		if got := r.PostForm.Get("scope"); got != "openid temporal" {
			t.Errorf("scope = %q", got)
		}

		if got := r.PostForm.Get("audience"); got != "temporal" {
			t.Errorf("audience = %q", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": fmt.Sprintf("tok-%d", n), "token_type": "Bearer", "expires_in": expiresIn,
		})
	}))
	t.Cleanup(srv.Close)

	return srv.URL, requests
}

func testOIDC(t *testing.T, url string) *OIDCConfig {
	t.Helper()

	path := filepath.Join(t.TempDir(), "client-secret")
	if err := os.WriteFile(path, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	return &OIDCConfig{TokenURL: url, ClientID: "repo-guardian", ClientSecretPath: path, Scopes: []string{"openid", "temporal"}, Audience: "temporal"}
}

func TestTokenCallback_CachesUntilNearExpiry(t *testing.T) {
	t.Parallel()

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	url, requests := tokenServer(t, 3600)

	ts, err := testOIDC(t, url).tokenSource(t.Context())
	if err != nil {
		t.Fatalf("tokenSource: %v", err)
	}

	cb := tokenCallback(ts, quiet)

	for range 5 {
		tok, err := cb(t.Context())
		if err != nil || tok != "tok-1" {
			t.Fatalf("callback = %q, %v; want tok-1", tok, err)
		}
	}

	if n := requests.Load(); n != 1 {
		t.Errorf("token endpoint hit %d times for 5 calls, want 1", n)
	}
}

func TestTokenCallback_RenewsShortLivedTokens(t *testing.T) {
	t.Parallel()

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Inside tokenRefreshEarly, so every call must fetch a fresh one.
	url, requests := tokenServer(t, 30)

	ts, err := testOIDC(t, url).tokenSource(t.Context())
	if err != nil {
		t.Fatalf("tokenSource: %v", err)
	}

	cb := tokenCallback(ts, quiet)

	first, _ := cb(t.Context())
	second, _ := cb(t.Context())

	if first == second || requests.Load() != 2 {
		t.Errorf("tokens %q then %q after %d requests; a token expiring within a minute must be renewed", first, second, requests.Load())
	}
}

func TestTokenCallback_EndpointError(t *testing.T) {
	t.Parallel()

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"unauthorized_client"}`, http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	ts, err := testOIDC(t, srv.URL).tokenSource(t.Context())
	if err != nil {
		t.Fatalf("tokenSource: %v", err)
	}

	if tok, err := tokenCallback(ts, quiet)(t.Context()); err == nil || tok != "" {
		t.Errorf("callback = %q, %v; want an error and no token", tok, err)
	}
}

func TestTokenSource_MissingSecretFile(t *testing.T) {
	t.Parallel()

	o := &OIDCConfig{TokenURL: "https://idp/token", ClientID: "rg", ClientSecretPath: filepath.Join(t.TempDir(), "absent")}
	if _, err := o.tokenSource(t.Context()); err == nil {
		t.Error("tokenSource with a missing secret file succeeded")
	}
}
