//go:build integration

package temporal_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/temporal"
	"github.com/donaldgifford/repo-guardian/internal/temporal/temporaltest"
)

// TestDial_OIDCPlaintext dials the dev server with an OIDC bearer token
// and TLS disabled. The dev server does not authorize, so this proves
// the wiring: the SDK calls the token source on real traffic, and
// TLSDisabled keeps the API-key credentials from forcing TLS on a
// plaintext frontend.
func TestDial_OIDCPlaintext(t *testing.T) {
	t.Parallel()

	srv := temporaltest.Start(t)

	var requests atomic.Int32

	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 3600})
	}))
	t.Cleanup(idp.Close)

	secret := filepath.Join(t.TempDir(), "client-secret")
	if err := os.WriteFile(secret, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := srv.Config
	cfg.TLSDisabled = true
	cfg.OIDC = &temporal.OIDCConfig{TokenURL: idp.URL, ClientID: "repo-guardian", ClientSecretPath: secret}

	c, err := temporal.Dial(t.Context(), &cfg, temporal.DialOptions{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(c.Close)

	if err := temporal.Ping(t.Context(), c); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	if err := temporal.CheckServerVersion(t.Context(), c, temporal.MinServerVersion); err != nil {
		t.Fatalf("CheckServerVersion: %v", err)
	}

	if requests.Load() == 0 {
		t.Error("the token endpoint was never called; the bearer token is not attached")
	}
}
