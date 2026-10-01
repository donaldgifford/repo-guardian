package temporal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
)

// credentialFiles serves the client's TLS material from disk and
// reloads it when the files change (DESIGN-0028), so a certificate
// renewed by cert-manager, or a rolled CA, is used without a restart.
//
// Handshakes never touch disk: they read an atomic snapshot that
// reload swaps. The first load is strict (newCredentialFiles fails, and
// so does Dial); later loads keep the last good snapshot on any error,
// because a read can straddle kubelet's swap of the mounted Secret and
// the next poll will see a consistent pair.
type credentialFiles struct {
	certPath, keyPath, caPath string
	// serverName is the name the frontend's certificate must carry:
	// TEMPORAL_TLS_SERVER_NAME, or the host part of TEMPORAL_ADDRESS.
	serverName string
	logger     *slog.Logger
	now        func() time.Time

	current atomic.Pointer[tlsMaterial]

	mu  sync.Mutex // serializes reload
	mod fileTimes  // modification times behind current
}

// tlsMaterial is one consistent snapshot of the files.
type tlsMaterial struct {
	cert  *tls.Certificate // nil without a client certificate
	roots *x509.CertPool   // nil without a CA file
}

// fileTimes are the modification times of the files a snapshot was
// read from; a zero time means the file is not configured.
type fileTimes struct {
	cert, key, ca time.Time
}

func newCredentialFiles(certPath, keyPath, caPath, serverName string, logger *slog.Logger) (*credentialFiles, error) {
	c := &credentialFiles{
		certPath:   certPath,
		keyPath:    keyPath,
		caPath:     caPath,
		serverName: serverName,
		logger:     logger,
		now:        time.Now,
	}

	if err := c.reload(); err != nil {
		return nil, err
	}

	return c, nil
}

// run reloads every interval until ctx is done. Errors are logged and
// counted by reload; the last good snapshot stays in use.
func (c *credentialFiles) run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := c.reload(); err != nil {
				c.logger.Warn("temporal: reloading TLS files failed; keeping the previous ones", "error", err)
			}
		}
	}
}

// reload re-reads the files when any modification time changed, and
// swaps the snapshot only when everything that changed parsed and
// validated.
func (c *credentialFiles) reload() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	times, err := c.stat()
	if err != nil {
		return err
	}

	prev := c.current.Load()
	certChanged := prev == nil || times.cert != c.mod.cert || times.key != c.mod.key
	caChanged := prev == nil || times.ca != c.mod.ca

	if !certChanged && !caChanged {
		return nil
	}

	next, err := c.load(prev, certChanged, caChanged)
	if err != nil {
		return err
	}

	c.current.Store(next)
	c.mod = times

	if next.cert != nil {
		metrics.TemporalClientCertExpiry.WithLabelValues().Set(float64(next.cert.Leaf.NotAfter.Unix()))
	}

	if prev != nil {
		c.recordChange(certChanged && c.certPath != "", caChanged && c.caPath != "")
	}

	return nil
}

// load builds the next snapshot from prev, re-reading only what
// changed. An error leaves prev in place and is counted against the
// credential that failed.
func (c *credentialFiles) load(prev *tlsMaterial, certChanged, caChanged bool) (*tlsMaterial, error) {
	next := &tlsMaterial{}
	if prev != nil {
		*next = *prev
	}

	if certChanged && c.certPath != "" {
		cert, err := c.loadKeyPair()
		if err != nil {
			metrics.TemporalCredentialReloadsTotal.WithLabelValues(metrics.CredentialClientCert, metrics.OutcomeError).Inc()

			return nil, err
		}

		next.cert = cert
	}

	if caChanged && c.caPath != "" {
		roots, err := loadPool(c.caPath)
		if err != nil {
			metrics.TemporalCredentialReloadsTotal.WithLabelValues(metrics.CredentialCA, metrics.OutcomeError).Inc()

			return nil, err
		}

		next.roots = roots
	}

	return next, nil
}

func (c *credentialFiles) recordChange(cert, ca bool) {
	if cert {
		metrics.TemporalCredentialReloadsTotal.WithLabelValues(metrics.CredentialClientCert, metrics.OutcomeChanged).Inc()
		c.logger.Info("temporal: loaded a renewed client certificate", "not_after", c.current.Load().cert.Leaf.NotAfter)
	}

	if ca {
		metrics.TemporalCredentialReloadsTotal.WithLabelValues(metrics.CredentialCA, metrics.OutcomeChanged).Inc()
		c.logger.Info("temporal: loaded a changed CA bundle")
	}
}

// stat reads the modification times, following symlinks so kubelet's
// atomic ..data swap of a mounted Secret registers as a change.
func (c *credentialFiles) stat() (fileTimes, error) {
	var (
		t    fileTimes
		errs []error
	)

	for _, f := range []struct {
		path       string
		dst        *time.Time
		credential string
	}{
		{c.certPath, &t.cert, metrics.CredentialClientCert},
		{c.keyPath, &t.key, metrics.CredentialClientCert},
		{c.caPath, &t.ca, metrics.CredentialCA},
	} {
		if f.path == "" {
			continue
		}

		fi, err := os.Stat(f.path)
		if err != nil {
			metrics.TemporalCredentialReloadsTotal.WithLabelValues(f.credential, metrics.OutcomeError).Inc()
			errs = append(errs, fmt.Errorf("temporal: %w", err))

			continue
		}

		*f.dst = fi.ModTime()
	}

	return t, errors.Join(errs...)
}

// loadKeyPair reads the client certificate and key, refusing a pair
// that does not match or a certificate that has already expired.
func (c *credentialFiles) loadKeyPair() (*tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(c.certPath, c.keyPath)
	if err != nil {
		return nil, fmt.Errorf("temporal: loading client certificate: %w", err)
	}

	if now := c.now(); !now.Before(cert.Leaf.NotAfter) {
		return nil, fmt.Errorf("temporal: client certificate %s expired at %s", c.certPath, cert.Leaf.NotAfter.Format(time.RFC3339))
	}

	return &cert, nil
}

func loadPool(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path) //nolint:gosec // G304: the path is operator configuration (TEMPORAL_TLS_CA_PATH)
	if err != nil {
		return nil, fmt.Errorf("temporal: reading CA: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("temporal: %s holds no PEM certificate", path)
	}

	return pool, nil
}

// getClientCertificate is tls.Config.GetClientCertificate: the current
// snapshot's certificate, read on every handshake.
func (c *credentialFiles) getClientCertificate(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	return c.current.Load().cert, nil
}

// verifyConnection is tls.Config.VerifyConnection, used when a CA file
// is configured. crypto/tls cannot reload RootCAs, so the config sets
// InsecureSkipVerify and this does the verification crypto/tls would
// have done (chain to the current roots, server name, server-auth EKU,
// validity), against the snapshot's pool.
//
//nolint:gocritic // hugeParam: the signature is fixed by tls.Config.VerifyConnection
func (c *credentialFiles) verifyConnection(cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("temporal: the server presented no certificate")
	}

	intermediates := x509.NewCertPool()
	for _, cert := range cs.PeerCertificates[1:] {
		intermediates.AddCert(cert)
	}

	_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{
		DNSName:       c.serverName,
		Roots:         c.current.Load().roots,
		Intermediates: intermediates,
		CurrentTime:   c.now(),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err != nil {
		return fmt.Errorf("temporal: verifying the server certificate: %w", err)
	}

	return nil
}
