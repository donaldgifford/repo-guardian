package temporal

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// tlsServer is a frontend stand-in: it requires a client certificate
// from clientCA, serves whatever certificate is current, and reports
// the serial of each client certificate it accepts.
type tlsServer struct {
	addr    string
	cert    atomic.Pointer[tls.Certificate]
	serials chan int64
}

func newTLSServer(t *testing.T, clientCA *testCA, serverCert *tls.Certificate) *tlsServer {
	t.Helper()

	pool := x509.NewCertPool()
	pool.AddCert(clientCA.cert)

	s := &tlsServer{serials: make(chan int64, 16)}
	s.cert.Store(serverCert)

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		MinVersion: tls.VersionTLS12,
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  pool,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return s.cert.Load(), nil
		},
	})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	s.addr = ln.Addr().String()

	ctx := t.Context()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			go func() {
				defer conn.Close()

				tc, ok := conn.(*tls.Conn)
				if !ok || tc.HandshakeContext(ctx) != nil {
					return
				}

				if peers := tc.ConnectionState().PeerCertificates; len(peers) > 0 {
					s.serials <- peers[0].SerialNumber.Int64()
				}
			}()
		}
	}()

	return s
}

// handshake dials the server with cfg and returns the serial the server
// saw, or the client's handshake error.
func (s *tlsServer) handshake(t *testing.T, cfg *tls.Config) (int64, error) {
	t.Helper()

	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: cfg}

	conn, err := d.DialContext(t.Context(), "tcp", s.addr)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	select {
	case n := <-s.serials:
		return n, nil
	case <-time.After(5 * time.Second):
		t.Fatal("the server never reported a client certificate")

		return 0, nil
	}
}

func serverKeyPair(t *testing.T, ca *testCA, l leaf) *tls.Certificate {
	t.Helper()

	certPEM, keyPEM := ca.issue(t, l)

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("server key pair: %v", err)
	}

	return &cert
}

// mtlsClient mounts a client certificate and CA and builds the client
// TLS config the way Dial does.
func mtlsClient(t *testing.T, m *secretMount, ca *testCA, l leaf, trust []byte) (*tls.Config, *credentialFiles) {
	t.Helper()

	certPEM, keyPEM := ca.issue(t, l)
	m.update(map[string][]byte{"tls.crt": certPEM, "tls.key": keyPEM, "ca.crt": trust})

	cfg := &Config{
		Address:     "temporal-frontend:7233",
		TLSCertPath: m.path("tls.crt"), TLSKeyPath: m.path("tls.key"), TLSCAPath: m.path("ca.crt"),
	}

	tlsCfg, files, err := cfg.tlsConfig(slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("tlsConfig: %v", err)
	}

	return tlsCfg, files
}

var frontend = leaf{serial: 100, usage: x509.ExtKeyUsageServerAuth, dnsNames: []string{"temporal-frontend"}}

func TestHandshake_PresentsRotatedClientCertificate(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "Temporal CA")
	srv := newTLSServer(t, ca, serverKeyPair(t, ca, frontend))
	m := newSecretMount(t)
	cfg, files := mtlsClient(t, m, ca, leaf{serial: 1, usage: x509.ExtKeyUsageClientAuth}, ca.pem)

	if got, err := srv.handshake(t, cfg); err != nil || got != 1 {
		t.Fatalf("first handshake = %d, %v; want serial 1", got, err)
	}

	certPEM, keyPEM := ca.issue(t, leaf{serial: 2, usage: x509.ExtKeyUsageClientAuth})
	m.update(map[string][]byte{"tls.crt": certPEM, "tls.key": keyPEM, "ca.crt": ca.pem})

	if err := files.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	// Same *tls.Config, no re-dial of anything: the next handshake asks
	// for the certificate again and gets the renewed one.
	if got, err := srv.handshake(t, cfg); err != nil || got != 2 {
		t.Fatalf("handshake after rotation = %d, %v; want serial 2", got, err)
	}
}

func TestHandshake_CARolloverWithoutRestart(t *testing.T) {
	t.Parallel()

	oldCA, newCA := newTestCA(t, "Temporal CA 1"), newTestCA(t, "Temporal CA 2")
	srv := newTLSServer(t, oldCA, serverKeyPair(t, oldCA, frontend))
	m := newSecretMount(t)
	clientCert := leaf{serial: 1, usage: x509.ExtKeyUsageClientAuth}
	cfg, files := mtlsClient(t, m, oldCA, clientCert, oldCA.pem)

	if _, err := srv.handshake(t, cfg); err != nil {
		t.Fatalf("handshake on the old CA: %v", err)
	}

	// The frontend moves to a certificate from the new CA before the
	// client trusts it: the handshake must fail, not pass unverified.
	srv.cert.Store(serverKeyPair(t, newCA, frontend))

	if _, err := srv.handshake(t, cfg); err == nil {
		t.Fatal("handshake succeeded against a CA the client does not trust")
	}

	// The trust bundle gains the new CA; after a reload the same config
	// connects.
	certPEM, keyPEM := oldCA.issue(t, clientCert)
	m.update(map[string][]byte{"tls.crt": certPEM, "tls.key": keyPEM, "ca.crt": append(append([]byte{}, oldCA.pem...), newCA.pem...)})

	if err := files.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if _, err := srv.handshake(t, cfg); err != nil {
		t.Fatalf("handshake after the CA reload: %v", err)
	}
}

// TestVerifyConnection_Rejects is what makes InsecureSkipVerify safe:
// every check crypto/tls would have made still fails the handshake.
func TestVerifyConnection_Rejects(t *testing.T) {
	t.Parallel()

	ca, stranger := newTestCA(t, "Temporal CA"), newTestCA(t, "Stranger CA")
	m := newSecretMount(t)
	_, files := mtlsClient(t, m, ca, leaf{serial: 1, usage: x509.ExtKeyUsageClientAuth}, ca.pem)

	tests := []struct {
		name string
		cert []byte
	}{
		{"wrong host", first(ca.issue(t, leaf{serial: 2, usage: x509.ExtKeyUsageServerAuth, dnsNames: []string{"elsewhere"}}))},
		{"unknown CA", first(stranger.issue(t, frontend))},
		{"expired", first(ca.issue(t, leaf{
			serial: 3, usage: x509.ExtKeyUsageServerAuth, dnsNames: []string{"temporal-frontend"}, notAfter: time.Now().Add(-time.Minute),
		}))},
		{"client-auth EKU only", first(ca.issue(t, leaf{serial: 4, usage: x509.ExtKeyUsageClientAuth, dnsNames: []string{"temporal-frontend"}}))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := files.verifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{parse(t, tt.cert)}}); err == nil {
				t.Error("verifyConnection accepted the certificate")
			}
		})
	}

	good := parse(t, first(ca.issue(t, frontend)))
	if err := files.verifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{good}}); err != nil {
		t.Errorf("verifyConnection rejected a valid certificate: %v", err)
	}

	if err := files.verifyConnection(tls.ConnectionState{}); err == nil {
		t.Error("verifyConnection accepted a server with no certificate")
	}
}

func first(certPEM, _ []byte) []byte { return certPEM }

func parse(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()

	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("no PEM block")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}

	return cert
}
