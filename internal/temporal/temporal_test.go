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
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.temporal.io/api/serviceerror"
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

	if cfg.TLSReloadInterval != DefaultTLSReloadInterval {
		t.Errorf("TLSReloadInterval = %s, want %s", cfg.TLSReloadInterval, DefaultTLSReloadInterval)
	}
}

func TestConfigFromEnv_TLSReloadInterval(t *testing.T) {
	tests := []struct {
		value   string
		want    time.Duration
		wantErr bool
	}{
		{value: "", want: DefaultTLSReloadInterval},
		{value: "5s", want: 5 * time.Second},
		{value: "10m", want: 10 * time.Minute},
		{value: "1m30s", want: 90 * time.Second},
		{value: "4s", wantErr: true},
		{value: "11m", wantErr: true},
		{value: "soon", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			for _, k := range configEnv {
				t.Setenv(k, "")
			}

			t.Setenv("TEMPORAL_ADDRESS", "temporal:7233")
			t.Setenv("TEMPORAL_TLS_RELOAD_INTERVAL", tt.value)

			cfg, err := ConfigFromEnv()
			if tt.wantErr {
				if err == nil {
					t.Errorf("ConfigFromEnv = %s, want an error", cfg.TLSReloadInterval)
				}

				return
			}

			if err != nil || cfg.TLSReloadInterval != tt.want {
				t.Errorf("ConfigFromEnv = %s, %v; want %s", cfg.TLSReloadInterval, err, tt.want)
			}
		})
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
		{"OIDC token URL alone", map[string]string{"TEMPORAL_ADDRESS": "t:7233", "TEMPORAL_OIDC_TOKEN_URL": "https://idp/token"}},
		{"OIDC secret without URL", map[string]string{
			"TEMPORAL_ADDRESS": "t:7233", "TEMPORAL_OIDC_CLIENT_ID": "rg", "TEMPORAL_OIDC_CLIENT_SECRET_PATH": "s",
		}},
		{"OIDC URL not http", map[string]string{
			"TEMPORAL_ADDRESS": "t:7233", "TEMPORAL_OIDC_TOKEN_URL": "idp/token",
			"TEMPORAL_OIDC_CLIENT_ID": "rg", "TEMPORAL_OIDC_CLIENT_SECRET_PATH": "s",
		}},
		{"TLS disabled with a cert", map[string]string{
			"TEMPORAL_ADDRESS": "t:7233", "TEMPORAL_TLS_DISABLED": "true",
			"TEMPORAL_TLS_CERT_PATH": "c.pem", "TEMPORAL_TLS_KEY_PATH": "k.pem",
		}},
		{"TLS disabled not a bool", map[string]string{"TEMPORAL_ADDRESS": "t:7233", "TEMPORAL_TLS_DISABLED": "maybe"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range configEnv {
				t.Setenv(k, tt.env[k])
			}

			if _, err := ConfigFromEnv(); err == nil {
				t.Error("ConfigFromEnv succeeded, want an error")
			}
		})
	}
}

// configEnv is every variable ConfigFromEnv reads, reset per case.
var configEnv = []string{
	"TEMPORAL_ADDRESS", "TEMPORAL_TLS_CERT_PATH", "TEMPORAL_TLS_KEY_PATH", "TEMPORAL_TLS_CA_PATH",
	"TEMPORAL_TLS_SERVER_NAME", "TEMPORAL_TLS_DISABLED", "TEMPORAL_OIDC_TOKEN_URL", "TEMPORAL_OIDC_CLIENT_ID",
	"TEMPORAL_OIDC_CLIENT_SECRET_PATH", "TEMPORAL_OIDC_SCOPES", "TEMPORAL_OIDC_AUDIENCE",
	"TEMPORAL_TLS_RELOAD_INTERVAL",
}

func TestConfigFromEnv_OIDC(t *testing.T) {
	for _, k := range configEnv {
		t.Setenv(k, "")
	}

	t.Setenv("TEMPORAL_ADDRESS", "temporal:7233")
	t.Setenv("TEMPORAL_OIDC_TOKEN_URL", "https://keycloak/realms/lab/protocol/openid-connect/token")
	t.Setenv("TEMPORAL_OIDC_CLIENT_ID", "repo-guardian")
	t.Setenv("TEMPORAL_OIDC_CLIENT_SECRET_PATH", "/etc/secret")
	t.Setenv("TEMPORAL_OIDC_SCOPES", "openid  temporal")
	// Server-verified TLS: a CA and server name without a client cert.
	t.Setenv("TEMPORAL_TLS_CA_PATH", "ca.pem")
	t.Setenv("TEMPORAL_TLS_SERVER_NAME", "temporal.lab")

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}

	if cfg.OIDC == nil || cfg.OIDC.ClientID != "repo-guardian" || len(cfg.OIDC.Scopes) != 2 || cfg.OIDC.Scopes[1] != "temporal" {
		t.Errorf("OIDC = %+v", cfg.OIDC)
	}

	if cfg.TLSDisabled {
		t.Error("TLSDisabled defaulted to true")
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
	logger := slog.New(slog.DiscardHandler)

	plain := &Config{Address: "t:7233"}
	if got, files, err := plain.tlsConfig(logger); err != nil || got != nil || files != nil {
		t.Errorf("plaintext tlsConfig = %v, %v, %v; want nil, nil, nil", got, files, err)
	}

	mtls := &Config{Address: "t:7233", TLSCertPath: cert, TLSKeyPath: key, TLSCAPath: cert, TLSServerName: "temporal-frontend"}

	got, files, err := mtls.tlsConfig(logger)
	if err != nil {
		t.Fatalf("tlsConfig: %v", err)
	}

	// Certificates and roots come from the reloadable files, never the
	// static fields, or a renewal would go unused until a restart.
	if len(got.Certificates) != 0 || got.RootCAs != nil || got.GetClientCertificate == nil ||
		got.VerifyConnection == nil || !got.InsecureSkipVerify || got.ServerName != "temporal-frontend" || files == nil {
		t.Errorf("tlsConfig = %+v, want reloadable cert and CA callbacks and the server name", got)
	}

	badCA := &Config{Address: "t:7233", TLSCertPath: cert, TLSKeyPath: key, TLSCAPath: key}
	if _, _, err := badCA.tlsConfig(logger); err == nil {
		t.Error("a CA file with no certificate was accepted")
	}

	// OIDC always verifies the server: TLS with no client cert, the CA
	// when given, system roots otherwise.
	oidc := &OIDCConfig{TokenURL: "https://idp/token", ClientID: "rg", ClientSecretPath: "s"}

	got, _, err = (&Config{Address: "t:7233", OIDC: oidc, TLSCAPath: cert, TLSServerName: "temporal.lab"}).tlsConfig(logger)
	if err != nil || got == nil || got.GetClientCertificate != nil || got.VerifyConnection == nil || got.ServerName != "temporal.lab" {
		t.Errorf("OIDC with a CA: tlsConfig = %+v, %v; want server-verified TLS with the CA", got, err)
	}

	got, files, err = (&Config{Address: "t:7233", OIDC: oidc}).tlsConfig(logger)
	if err != nil || got == nil || got.InsecureSkipVerify || got.VerifyConnection != nil || files != nil {
		t.Errorf("OIDC alone: tlsConfig = %+v, %v; want standard TLS on system roots", got, err)
	}

	got, _, err = (&Config{Address: "t:7233", OIDC: oidc, TLSDisabled: true}).tlsConfig(logger)
	if err != nil || got != nil {
		t.Errorf("OIDC with TLS disabled: tlsConfig = %+v, %v; want nil, nil", got, err)
	}
}

func TestServerName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		cfg  Config
		want string
	}{
		{Config{Address: "temporal-frontend:7233"}, "temporal-frontend"},
		{Config{Address: "temporal-frontend:7233", TLSServerName: "temporal.lab"}, "temporal.lab"},
		{Config{Address: "10.0.0.1:7233"}, "10.0.0.1"},
		{Config{Address: "no-port"}, "no-port"},
	}

	for _, tt := range tests {
		if got := tt.cfg.serverName(); got != tt.want {
			t.Errorf("serverName(%+v) = %q, want %q", tt.cfg, got, tt.want)
		}
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

func (f fakeService) GetSystemInfo(
	context.Context, *workflowservice.GetSystemInfoRequest, ...grpc.CallOption,
) (*workflowservice.GetSystemInfoResponse, error) {
	return &workflowservice.GetSystemInfoResponse{ServerVersion: f.version}, nil
}

// GetClusterInfo answers the way a JWT-authorizing 1.32 frontend does
// for a namespace-scoped caller, so a regression to it fails the test
// the way it failed rc.2 in the homelab.
func (fakeService) GetClusterInfo(
	context.Context, *workflowservice.GetClusterInfoRequest, ...grpc.CallOption,
) (*workflowservice.GetClusterInfoResponse, error) {
	return nil, serviceerror.NewPermissionDenied("Request unauthorized.", "")
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
