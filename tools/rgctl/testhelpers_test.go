package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/tools/rgctl/internal/ghapi/ghapitest"
)

const (
	testBot   = "repo-guardian[bot]"
	testToken = "ghp_operator"
)

var testNow = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

// fake starts a GitHub fake with the App installed on acme and globex.
func fake(t *testing.T) *ghapitest.Server {
	t.Helper()
	srv := ghapitest.New()
	t.Cleanup(srv.Close)
	srv.AppID = 7
	srv.Slug = "repo-guardian"
	srv.Installations = []ghapitest.Installation{{ID: 41, Account: "acme"}, {ID: 42, Account: "globex"}}
	return srv
}

type result struct {
	code   int
	stdout string
	stderr string
}

// tokenEnv is the environment for token auth.
func tokenEnv() map[string]string {
	return map[string]string{envToken: testToken, envBotLogin: testBot}
}

// runFake runs rgctl against srv with env, a fixed clock and no sleeping.
func runFake(t *testing.T, srv *ghapitest.Server, env map[string]string, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	a := newApp(&stdout, &stderr)
	a.getenv = func(k string) string { return env[k] }
	a.now = func() time.Time { return testNow }
	a.sleep = func(context.Context, time.Duration) error { return nil }
	code := a.run(append(args, "--no-color", "--github-host", srv.URL))
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// appKey writes a throwaway App private key.
func appKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "app.pem")
	if err := os.WriteFile(
		path,
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
		0o600,
	); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return path
}
