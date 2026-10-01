package temporal

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
)

// testCA signs leaf certificates for the reload and handshake tests.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTestCA(t *testing.T, name string) *testCA {
	t.Helper()

	key := newKey(t)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
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

	return &testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// leaf describes a certificate for testCA.issue.
type leaf struct {
	serial   int64
	notAfter time.Time
	usage    x509.ExtKeyUsage
	dnsNames []string
}

// issue signs a leaf and returns its certificate and key as PEM.
func (ca *testCA) issue(t *testing.T, l leaf) (certPEM, keyPEM []byte) {
	t.Helper()

	if l.notAfter.IsZero() {
		l.notAfter = time.Now().Add(time.Hour)
	}

	key := newKey(t)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(l.serial),
		Subject:      pkix.Name{CommonName: "repo-guardian"},
		DNSNames:     l.dnsNames,
		NotBefore:    time.Now().Add(-2 * time.Hour),
		NotAfter:     l.notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{l.usage},
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

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	return key
}

// secretMount lays files out the way kubelet mounts a Secret: each
// update writes a fresh timestamped directory and atomically swaps the
// ..data symlink, and the visible names are symlinks through ..data.
type secretMount struct {
	t   *testing.T
	dir string
	gen int
}

func newSecretMount(t *testing.T) *secretMount {
	t.Helper()

	return &secretMount{t: t, dir: t.TempDir()}
}

func (m *secretMount) path(name string) string { return filepath.Join(m.dir, name) }

// update publishes a new generation of files. Each generation gets a
// distinct modification time, as kubelet's freshly written files do.
func (m *secretMount) update(files map[string][]byte) {
	m.t.Helper()

	m.gen++
	gen := fmt.Sprintf("..gen_%d", m.gen)
	mtime := time.Now().Add(time.Duration(m.gen) * time.Second)

	if err := os.Mkdir(filepath.Join(m.dir, gen), 0o700); err != nil {
		m.t.Fatal(err)
	}

	for name, data := range files {
		p := filepath.Join(m.dir, gen, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			m.t.Fatal(err)
		}

		if err := os.Chtimes(p, mtime, mtime); err != nil {
			m.t.Fatal(err)
		}

		if _, err := os.Lstat(m.path(name)); os.IsNotExist(err) {
			if err := os.Symlink(filepath.Join("..data", name), m.path(name)); err != nil {
				m.t.Fatal(err)
			}
		}
	}

	tmp := m.path("..data_tmp")
	if err := os.Symlink(gen, tmp); err != nil {
		m.t.Fatal(err)
	}

	if err := os.Rename(tmp, m.path("..data")); err != nil {
		m.t.Fatal(err)
	}
}

// clientFiles mounts a client pair (and the CA) and returns the
// credentialFiles serving them.
func clientFiles(t *testing.T, m *secretMount, ca *testCA, l leaf) *credentialFiles {
	t.Helper()

	certPEM, keyPEM := ca.issue(t, l)
	m.update(map[string][]byte{"tls.crt": certPEM, "tls.key": keyPEM, "ca.crt": ca.pem})

	files, err := newCredentialFiles(m.path("tls.crt"), m.path("tls.key"), m.path("ca.crt"), "temporal-frontend", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("newCredentialFiles: %v", err)
	}

	return files
}

func serial(t *testing.T, files *credentialFiles) int64 {
	t.Helper()

	cert, err := files.getClientCertificate(nil)
	if err != nil || cert == nil {
		t.Fatalf("getClientCertificate = %v, %v", cert, err)
	}

	return cert.Leaf.SerialNumber.Int64()
}

func reloads(credential, outcome string) float64 {
	return testutil.ToFloat64(metrics.TemporalCredentialReloadsTotal.WithLabelValues(credential, outcome))
}

// Reload tests read process-global counters, so they do not run in
// parallel with each other.

func TestCredentialFiles_PicksUpRotation(t *testing.T) {
	ca := newTestCA(t, "Temporal CA")
	m := newSecretMount(t)
	files := clientFiles(t, m, ca, leaf{serial: 1, usage: x509.ExtKeyUsageClientAuth})

	changed := reloads(metrics.CredentialClientCert, metrics.OutcomeChanged)

	// An unchanged poll swaps nothing and counts nothing.
	if err := files.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if got := reloads(metrics.CredentialClientCert, metrics.OutcomeChanged); got != changed {
		t.Errorf("an unchanged poll counted a change (%v -> %v)", changed, got)
	}

	renewed := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	certPEM, keyPEM := ca.issue(t, leaf{serial: 2, usage: x509.ExtKeyUsageClientAuth, notAfter: renewed})
	m.update(map[string][]byte{"tls.crt": certPEM, "tls.key": keyPEM, "ca.crt": ca.pem})

	if err := files.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if got := serial(t, files); got != 2 {
		t.Errorf("serial after rotation = %d, want 2", got)
	}

	if got := reloads(metrics.CredentialClientCert, metrics.OutcomeChanged); got != changed+1 {
		t.Errorf("changed counter = %v, want %v", got, changed+1)
	}

	if got := testutil.ToFloat64(metrics.TemporalClientCertExpiry.WithLabelValues()); got != float64(renewed.Unix()) {
		t.Errorf("expiry gauge = %v, want %v", got, renewed.Unix())
	}
}

func TestCredentialFiles_KeepsLastGoodSnapshot(t *testing.T) {
	ca := newTestCA(t, "Temporal CA")
	other := newTestCA(t, "Other CA")

	goodCert, goodKey := ca.issue(t, leaf{serial: 9, usage: x509.ExtKeyUsageClientAuth})
	_, otherKey := other.issue(t, leaf{serial: 9, usage: x509.ExtKeyUsageClientAuth})
	expiredCert, expiredKey := ca.issue(t, leaf{serial: 9, usage: x509.ExtKeyUsageClientAuth, notAfter: time.Now().Add(-time.Minute)})

	tests := []struct {
		name       string
		files      map[string][]byte
		credential string
	}{
		// A read that straddled the swap: the new cert with a key that
		// does not belong to it.
		{"mismatched pair", map[string][]byte{"tls.crt": goodCert, "tls.key": otherKey, "ca.crt": ca.pem}, metrics.CredentialClientCert},
		{"empty certificate", map[string][]byte{"tls.crt": {}, "tls.key": otherKey, "ca.crt": ca.pem}, metrics.CredentialClientCert},
		{"expired certificate", map[string][]byte{"tls.crt": expiredCert, "tls.key": expiredKey, "ca.crt": ca.pem}, metrics.CredentialClientCert},
		{"CA without PEM", map[string][]byte{"tls.crt": goodCert, "tls.key": goodKey, "ca.crt": []byte("not a certificate")}, metrics.CredentialCA},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newSecretMount(t)
			files := clientFiles(t, m, ca, leaf{serial: 1, usage: x509.ExtKeyUsageClientAuth})
			before := reloads(tt.credential, metrics.OutcomeError)

			m.update(tt.files)

			if err := files.reload(); err == nil {
				t.Fatal("reload accepted bad files")
			}

			if got := serial(t, files); got != 1 {
				t.Errorf("serial = %d, want the last good certificate (1)", got)
			}

			if got := reloads(tt.credential, metrics.OutcomeError); got != before+1 {
				t.Errorf("error counter = %v, want %v", got, before+1)
			}
		})
	}
}

func TestCredentialFiles_MissingFileKeepsLastGood(t *testing.T) {
	ca := newTestCA(t, "Temporal CA")
	m := newSecretMount(t)
	files := clientFiles(t, m, ca, leaf{serial: 1, usage: x509.ExtKeyUsageClientAuth})
	before := reloads(metrics.CredentialCA, metrics.OutcomeError)

	if err := os.Remove(m.path("ca.crt")); err != nil {
		t.Fatal(err)
	}

	if err := files.reload(); err == nil {
		t.Fatal("reload with a missing CA succeeded")
	}

	if got := serial(t, files); got != 1 {
		t.Errorf("serial = %d, want 1", got)
	}

	if got := reloads(metrics.CredentialCA, metrics.OutcomeError); got != before+1 {
		t.Errorf("CA error counter = %v, want %v", got, before+1)
	}
}

func TestNewCredentialFiles_StrictStartup(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "Temporal CA")
	_, otherKey := newTestCA(t, "Other").issue(t, leaf{serial: 1, usage: x509.ExtKeyUsageClientAuth})
	certPEM, _ := ca.issue(t, leaf{serial: 1, usage: x509.ExtKeyUsageClientAuth})

	m := newSecretMount(t)
	m.update(map[string][]byte{"tls.crt": certPEM, "tls.key": otherKey, "ca.crt": ca.pem})

	if _, err := newCredentialFiles(m.path("tls.crt"), m.path("tls.key"), m.path("ca.crt"), "x", slog.New(slog.DiscardHandler)); err == nil {
		t.Error("newCredentialFiles accepted a mismatched pair at startup")
	}

	if _, err := newCredentialFiles(m.path("absent.crt"), m.path("tls.key"), "", "x", slog.New(slog.DiscardHandler)); err == nil {
		t.Error("newCredentialFiles accepted a missing certificate at startup")
	}
}

func TestCredentialFiles_RunReloadsOnTick(t *testing.T) {
	ca := newTestCA(t, "Temporal CA")
	m := newSecretMount(t)
	files := clientFiles(t, m, ca, leaf{serial: 1, usage: x509.ExtKeyUsageClientAuth})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)
		files.run(ctx, 10*time.Millisecond)
	}()

	certPEM, keyPEM := ca.issue(t, leaf{serial: 2, usage: x509.ExtKeyUsageClientAuth})
	m.update(map[string][]byte{"tls.crt": certPEM, "tls.key": keyPEM, "ca.crt": ca.pem})

	deadline := time.Now().Add(5 * time.Second)
	for serial(t, files) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("run never picked up the rotated certificate")
		}

		time.Sleep(10 * time.Millisecond)
	}

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after cancel")
	}
}

func TestCredentialFiles_StartReloadsUntilStopped(t *testing.T) {
	ca := newTestCA(t, "Temporal CA")
	m := newSecretMount(t)
	files := clientFiles(t, m, ca, leaf{serial: 1, usage: x509.ExtKeyUsageClientAuth})

	// A context that is already done must not stop the poller: Dial's
	// context bounds the connect, not reloading.
	dialCtx, cancelDial := context.WithCancel(t.Context())
	cancelDial()

	stop := files.start(dialCtx, 10*time.Millisecond)

	certPEM, keyPEM := ca.issue(t, leaf{serial: 2, usage: x509.ExtKeyUsageClientAuth})
	m.update(map[string][]byte{"tls.crt": certPEM, "tls.key": keyPEM, "ca.crt": ca.pem})

	deadline := time.Now().Add(5 * time.Second)
	for serial(t, files) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("the poller never picked up the rotated certificate (stopped by the dial context?)")
		}

		time.Sleep(10 * time.Millisecond)
	}

	stopped := make(chan struct{})

	go func() {
		defer close(stopped)
		stop()
		stop() // idempotent
	}()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return")
	}

	// Stopped means no more reloads.
	certPEM, keyPEM = ca.issue(t, leaf{serial: 3, usage: x509.ExtKeyUsageClientAuth})
	m.update(map[string][]byte{"tls.crt": certPEM, "tls.key": keyPEM, "ca.crt": ca.pem})
	time.Sleep(100 * time.Millisecond)

	if got := serial(t, files); got != 2 {
		t.Errorf("serial after stop = %d, want 2: the poller kept running", got)
	}
}

func TestCredentialFiles_StartWithNothingToReload(t *testing.T) {
	t.Parallel()

	var files *credentialFiles

	stop := files.start(t.Context(), time.Second)
	stop()
	stop()
}
