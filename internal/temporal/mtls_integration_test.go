//go:build integration

package temporal_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/temporal"
	"github.com/donaldgifford/repo-guardian/internal/temporal/temporaltest"
)

// proxyServerName is the name on the proxy's certificate, which the
// client verifies through TEMPORAL_TLS_SERVER_NAME.
const proxyServerName = "temporal-frontend"

// TestDial_ReloadsClientCertificateAcrossReconnect is the end-to-end
// reload check (IMPL-0026 task 1.9). The dev server has no TLS, so an
// in-process proxy terminates mTLS in front of it, requiring a client
// certificate and speaking h2. After the client certificate on disk is
// rotated and the proxy drops every connection, the same client (no new
// Dial) must reconnect presenting the new certificate.
func TestDial_ReloadsClientCertificateAcrossReconnect(t *testing.T) {
	srv := temporaltest.Start(t)
	ca := newIntegrationCA(t)
	proxy := newMTLSProxy(t, ca, srv.Config.Address)

	dir := t.TempDir()
	writeClientPair(t, ca, dir, 1, time.Now())

	cfg := srv.Config
	cfg.Address = proxy.addr
	cfg.TLSCertPath = filepath.Join(dir, "tls.crt")
	cfg.TLSKeyPath = filepath.Join(dir, "tls.key")
	cfg.TLSCAPath = filepath.Join(dir, "ca.crt")
	cfg.TLSServerName = proxyServerName
	cfg.TLSReloadInterval = 5 * time.Second // the configured floor

	c, stop, err := temporal.Dial(t.Context(), &cfg, temporal.DialOptions{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(stop)
	t.Cleanup(c.Close)

	if err := temporal.Ping(t.Context(), c); err != nil {
		t.Fatalf("Ping through the proxy: %v", err)
	}

	if got := proxy.lastSerial(); got != 1 {
		t.Fatalf("first handshake presented serial %d, want 1", got)
	}

	changed := metrics.TemporalCredentialReloadsTotal.WithLabelValues(metrics.CredentialClientCert, metrics.OutcomeChanged)
	before := testutil.ToFloat64(changed)

	// A later mtime than the first pair, as a renewed Secret has.
	writeClientPair(t, ca, dir, 2, time.Now().Add(time.Minute))
	waitFor(t, 20*time.Second, "the poller to load the rotated certificate", func() bool {
		return testutil.ToFloat64(changed) > before
	})

	proxy.dropAll()

	// gRPC reconnects with backoff, so allow a few attempts.
	waitFor(t, 30*time.Second, "a call to succeed after the reconnect", func() bool {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()

		return temporal.Ping(ctx, c) == nil
	})

	if got := proxy.lastSerial(); got != 2 {
		t.Errorf("handshake after the reconnect presented serial %d, want 2 (the rotated certificate)", got)
	}
}

// waitFor polls cond until it holds or the timeout passes.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}

		time.Sleep(100 * time.Millisecond)
	}
}

// integrationCA signs the proxy's and the client's certificates.
type integrationCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newIntegrationCA(t *testing.T) *integrationCA {
	t.Helper()

	key := newECKey(t)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Temporal test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CA certificate: %v", err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}

	return &integrationCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue signs a leaf for usage and returns it and its key as PEM.
func (ca *integrationCA) issue(t *testing.T, serial int64, usage x509.ExtKeyUsage, dnsNames ...string) (certPEM, keyPEM []byte) {
	t.Helper()

	key := newECKey(t)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "repo-guardian"},
		DNSNames:     dnsNames,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("leaf certificate: %v", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func newECKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	return key
}

// writeClientPair writes a client certificate with the given serial,
// its key and the CA into dir, stamping mtime on all three.
func writeClientPair(t *testing.T, ca *integrationCA, dir string, serial int64, mtime time.Time) {
	t.Helper()

	certPEM, keyPEM := ca.issue(t, serial, x509.ExtKeyUsageClientAuth)

	for name, data := range map[string][]byte{"tls.crt": certPEM, "tls.key": keyPEM, "ca.crt": ca.pem} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}

		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

// mTLSProxy terminates mTLS (client certificate required, ALPN h2) and
// forwards the plaintext HTTP/2 stream to the dev server. It records the
// serial of every client certificate it accepts.
type mTLSProxy struct {
	addr string

	mu      sync.Mutex
	serial  int64
	clients []net.Conn
}

func newMTLSProxy(t *testing.T, ca *integrationCA, backend string) *mTLSProxy {
	t.Helper()

	certPEM, keyPEM := ca.issue(t, 100, x509.ExtKeyUsageServerAuth, proxyServerName)

	serverCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("proxy key pair: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		NextProtos:   []string{"h2"},
	})
	if err != nil {
		t.Fatalf("proxy listen: %v", err)
	}

	p := &mTLSProxy{addr: ln.Addr().String()}

	t.Cleanup(func() {
		_ = ln.Close()
		p.dropAll()
	})

	go p.serve(t.Context(), ln, backend)

	return p
}

func (p *mTLSProxy) serve(ctx context.Context, ln net.Listener, backend string) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}

		go p.forward(ctx, conn, backend)
	}
}

// forward completes the handshake, records the client's serial, and
// pipes bytes both ways until either side closes.
func (p *mTLSProxy) forward(ctx context.Context, conn net.Conn, backend string) {
	defer conn.Close()

	tc, ok := conn.(*tls.Conn)
	if !ok || tc.HandshakeContext(ctx) != nil {
		return
	}

	peers := tc.ConnectionState().PeerCertificates
	if len(peers) == 0 {
		return
	}

	var d net.Dialer

	up, err := d.DialContext(ctx, "tcp", backend)
	if err != nil {
		return
	}
	defer up.Close()

	p.mu.Lock()
	p.serial = peers[0].SerialNumber.Int64()
	p.clients = append(p.clients, conn)
	p.mu.Unlock()

	done := make(chan struct{}, 2)

	go func() { _, _ = io.Copy(up, tc); done <- struct{}{} }()
	go func() { _, _ = io.Copy(tc, up); done <- struct{}{} }()

	<-done
}

// lastSerial is the serial of the most recently accepted client
// certificate.
func (p *mTLSProxy) lastSerial() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.serial
}

// dropAll closes every client connection, forcing a reconnect and so a
// new handshake.
func (p *mTLSProxy) dropAll() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, c := range p.clients {
		_ = c.Close() // already closed by its own forward goroutine is fine
	}

	p.clients = nil
}
