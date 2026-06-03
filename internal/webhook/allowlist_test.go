package webhook

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestAllowlist(failOpen, trustProxy bool) *GitHubIPAllowlist {
	return &GitHubIPAllowlist{
		failOpen:   failOpen,
		trustProxy: trustProxy,
		logger:     slog.Default(),
		metaURL:    defaultMetaURL,
	}
}

func setNetworks(a *GitHubIPAllowlist, cidrs ...string) {
	networks := make([]*net.IPNet, 0, len(cidrs))

	for _, cidr := range cidrs {
		_, network, _ := net.ParseCIDR(cidr)
		networks = append(networks, network)
	}

	a.mu.Lock()
	a.networks = networks
	a.loaded = true
	a.mu.Unlock()
}

func TestIsAllowed_GitHubIP(t *testing.T) {
	t.Parallel()

	a := newTestAllowlist(false, false)
	setNetworks(a, "192.30.252.0/22", "185.199.108.0/22")

	ip := net.ParseIP("192.30.252.1")
	if !a.IsAllowed(ip) {
		t.Errorf("expected IP %s to be allowed", ip)
	}
}

func TestIsAllowed_NonGitHubIP(t *testing.T) {
	t.Parallel()

	a := newTestAllowlist(false, false)
	setNetworks(a, "192.30.252.0/22")

	ip := net.ParseIP("10.0.0.1")
	if a.IsAllowed(ip) {
		t.Errorf("expected IP %s to be rejected", ip)
	}
}

func TestIsAllowed_IPv6(t *testing.T) {
	t.Parallel()

	a := newTestAllowlist(false, false)
	setNetworks(a, "2a0a:a440::/29")

	ip := net.ParseIP("2a0a:a440::1")
	if !a.IsAllowed(ip) {
		t.Errorf("expected IPv6 %s to be allowed", ip)
	}

	ip = net.ParseIP("2001:db8::1")
	if a.IsAllowed(ip) {
		t.Errorf("expected IPv6 %s to be rejected", ip)
	}
}

func TestIsAllowed_NotLoaded_FailClosed(t *testing.T) {
	t.Parallel()

	a := newTestAllowlist(false, false)
	// Not calling setNetworks, so loaded=false.
	ip := net.ParseIP("192.30.252.1")

	if a.IsAllowed(ip) {
		t.Error("expected fail-closed when not loaded")
	}
}

func TestIsAllowed_NotLoaded_FailOpen(t *testing.T) {
	t.Parallel()

	a := newTestAllowlist(true, false)
	// Not calling setNetworks, so loaded=false.
	ip := net.ParseIP("10.0.0.1")

	if !a.IsAllowed(ip) {
		t.Error("expected fail-open when not loaded")
	}
}

func TestRefresh_Success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := metaResponse{
			Hooks: []string{"192.30.252.0/22", "185.199.108.0/22"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	a := newTestAllowlist(false, false)
	a.metaURL = srv.URL

	if err := a.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	a.mu.RLock()
	loaded := a.loaded
	count := len(a.networks)
	a.mu.RUnlock()

	if !loaded {
		t.Error("expected loaded=true after successful refresh")
	}

	if count != 2 {
		t.Errorf("expected 2 networks, got %d", count)
	}

	if !a.IsAllowed(net.ParseIP("192.30.252.1")) {
		t.Error("expected 192.30.252.1 to be allowed after refresh")
	}
}

func TestRefresh_Failure_KeepsPrevious(t *testing.T) {
	t.Parallel()

	a := newTestAllowlist(false, false)
	setNetworks(a, "192.30.252.0/22")

	// Point to a server that returns an error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	a.metaURL = srv.URL

	_ = a.Refresh(context.Background())

	// Previous ranges should still be intact regardless of refresh failure.
	if !a.IsAllowed(net.ParseIP("192.30.252.1")) {
		t.Error("expected previous ranges to be kept after failed refresh")
	}
}

func TestRefresh_InvalidCIDR_Skipped(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := metaResponse{
			Hooks: []string{"192.30.252.0/22", "not-a-cidr", "185.199.108.0/22"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	a := newTestAllowlist(false, false)
	a.metaURL = srv.URL

	if err := a.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	a.mu.RLock()
	count := len(a.networks)
	a.mu.RUnlock()

	if count != 2 {
		t.Errorf("expected 2 valid networks (invalid skipped), got %d", count)
	}
}

func TestExtractIP_RemoteAddr(t *testing.T) {
	t.Parallel()

	a := newTestAllowlist(false, false)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/github", http.NoBody)
	r.RemoteAddr = "192.30.252.1:12345"

	ip := a.extractIP(r)
	if ip == nil {
		t.Fatal("expected non-nil IP")
	}

	if ip.String() != "192.30.252.1" {
		t.Errorf("extractIP = %s, want 192.30.252.1", ip)
	}
}

func TestExtractIP_XForwardedFor(t *testing.T) {
	t.Parallel()

	a := newTestAllowlist(false, true)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/github", http.NoBody)
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("X-Forwarded-For", "192.30.252.1, 10.0.0.1")

	ip := a.extractIP(r)
	if ip == nil {
		t.Fatal("expected non-nil IP")
	}

	if ip.String() != "192.30.252.1" {
		t.Errorf("extractIP = %s, want 192.30.252.1 (leftmost XFF)", ip)
	}
}

func TestExtractIP_XForwardedFor_Ignored(t *testing.T) {
	t.Parallel()

	a := newTestAllowlist(false, false)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/github", http.NoBody)
	r.RemoteAddr = "10.0.0.1:12345"
	r.Header.Set("X-Forwarded-For", "192.30.252.1, 10.0.0.1")

	ip := a.extractIP(r)
	if ip == nil {
		t.Fatal("expected non-nil IP")
	}

	if ip.String() != "10.0.0.1" {
		t.Errorf("extractIP = %s, want 10.0.0.1 (RemoteAddr, XFF ignored)", ip)
	}
}

func TestMiddleware_AllowedIP(t *testing.T) {
	t.Parallel()

	a := newTestAllowlist(false, false)
	setNetworks(a, "192.30.252.0/22")

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := a.Middleware(next)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/github", http.NoBody)
	r.RemoteAddr = "192.30.252.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestMiddleware_BlockedIP(t *testing.T) {
	t.Parallel()

	a := newTestAllowlist(false, false)
	setNetworks(a, "192.30.252.0/22")

	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next handler should not be called for blocked IP")
	})

	handler := a.Middleware(next)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/github", http.NoBody)
	r.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestMiddleware_NotLoaded_FailClosed(t *testing.T) {
	t.Parallel()

	a := newTestAllowlist(false, false)

	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next handler should not be called when fail-closed and not loaded")
	})

	handler := a.Middleware(next)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/github", http.NoBody)
	r.RemoteAddr = "192.30.252.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}
