package temporal

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc"
)

// Config tests use t.Setenv and so cannot run in parallel.

func TestConfigFromEnv_Defaults(t *testing.T) {
	t.Setenv("TEMPORAL_ADDRESS", "temporal:7233")

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}

	if cfg.Namespace != DefaultNamespace || cfg.TaskQueue != DefaultTaskQueue {
		t.Errorf("cfg = %+v, want default namespace and task queue", cfg)
	}
}

func TestConfigFromEnv_Invalid(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"no address", map[string]string{}},
		{"cert without key", map[string]string{"TEMPORAL_ADDRESS": "t:7233", "TEMPORAL_TLS_CERT_PATH": "c.pem"}},
		{"CA without cert", map[string]string{"TEMPORAL_ADDRESS": "t:7233", "TEMPORAL_TLS_CA_PATH": "ca.pem"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"TEMPORAL_ADDRESS", "TEMPORAL_TLS_CERT_PATH", "TEMPORAL_TLS_KEY_PATH", "TEMPORAL_TLS_CA_PATH"} {
				t.Setenv(k, tt.env[k])
			}

			if _, err := ConfigFromEnv(); err == nil {
				t.Error("ConfigFromEnv succeeded, want an error")
			}
		})
	}
}

// writeCert writes a self-signed certificate and key, returning paths.
func writeCert(t *testing.T) (certPath, keyPath string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "repo-guardian"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("certificate: %v", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	dir := t.TempDir()
	certPath, keyPath = filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")

	for path, block := range map[string]*pem.Block{
		certPath: {Type: "CERTIFICATE", Bytes: der},
		keyPath:  {Type: "EC PRIVATE KEY", Bytes: keyDER},
	} {
		if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	return certPath, keyPath
}

func TestTLSConfig(t *testing.T) {
	t.Parallel()

	cert, key := writeCert(t)

	plain := &Config{Address: "t:7233"}
	if got, err := plain.tlsConfig(); err != nil || got != nil {
		t.Errorf("plaintext tlsConfig = %v, %v; want nil, nil", got, err)
	}

	mtls := &Config{Address: "t:7233", TLSCertPath: cert, TLSKeyPath: key, TLSCAPath: cert, TLSServerName: "temporal-frontend"}

	got, err := mtls.tlsConfig()
	if err != nil {
		t.Fatalf("tlsConfig: %v", err)
	}

	if len(got.Certificates) != 1 || got.RootCAs == nil || got.ServerName != "temporal-frontend" {
		t.Errorf("tlsConfig = %+v, want a client cert, a CA pool and the server name", got)
	}

	badCA := &Config{Address: "t:7233", TLSCertPath: cert, TLSKeyPath: key, TLSCAPath: key}
	if _, err := badCA.tlsConfig(); err == nil {
		t.Error("a CA file with no certificate was accepted")
	}
}

// fakeClient answers the two calls the startup checks make.
type fakeClient struct {
	client.Client

	version   string
	healthErr error
}

type fakeService struct {
	workflowservice.WorkflowServiceClient

	version string
}

func (f fakeService) GetClusterInfo(
	context.Context, *workflowservice.GetClusterInfoRequest, ...grpc.CallOption,
) (*workflowservice.GetClusterInfoResponse, error) {
	return &workflowservice.GetClusterInfoResponse{ServerVersion: f.version}, nil
}

func (f *fakeClient) WorkflowService() workflowservice.WorkflowServiceClient {
	return fakeService{version: f.version}
}

func (f *fakeClient) CheckHealth(context.Context, *client.CheckHealthRequest) (*client.CheckHealthResponse, error) {
	return &client.CheckHealthResponse{}, f.healthErr
}

func TestCheckServerVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		version string
		wantErr error
		anyErr  bool
	}{
		{version: "1.31.0"},
		{version: "1.32.0"},
		{version: "2.0.0"},
		{version: "1.30.4", wantErr: ErrServerTooOld},
		{version: "garbage", anyErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			t.Parallel()

			err := CheckServerVersion(t.Context(), &fakeClient{version: tt.version}, MinServerVersion)

			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("err = %v, want %v", err, tt.wantErr)
				}
			case tt.anyErr:
				if err == nil {
					t.Error("err = nil, want an error")
				}
			case err != nil:
				t.Errorf("err = %v, want nil", err)
			}
		})
	}
}

func TestPing(t *testing.T) {
	t.Parallel()

	if err := Ping(t.Context(), &fakeClient{}); err != nil {
		t.Errorf("Ping healthy = %v", err)
	}

	if err := Ping(t.Context(), &fakeClient{healthErr: errors.New("down")}); err == nil {
		t.Error("Ping unhealthy = nil")
	}
}
