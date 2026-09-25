package config

import (
	"strings"
	"testing"
)

// Tests here use t.Setenv, so none is parallel.

func setWorkerEnv(t *testing.T) {
	t.Helper()

	t.Setenv("TEMPORAL_ADDRESS", "temporal:7233")
	t.Setenv("GITHUB_APP_ID", "1")
	t.Setenv("GITHUB_PRIVATE_KEY", "key")
	t.Setenv("STORE_DSN", "postgres://x")
	t.Setenv("GUARDIAN_CONFIG", "/etc/repo-guardian/guardian.hcl")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "s")
}

func TestLoadRole_Ingest(t *testing.T) {
	t.Run("webhook secret and temporal suffice", func(t *testing.T) {
		t.Setenv("TEMPORAL_ADDRESS", "temporal:7233")
		t.Setenv("GITHUB_WEBHOOK_SECRET", "s")

		if _, err := LoadRole(RoleIngest); err != nil {
			t.Fatalf("LoadRole(ingest) = %v, want no error without App key, database or Valkey", err)
		}
	})

	for _, tt := range []struct{ env, want string }{
		{"GITHUB_PRIVATE_KEY", "App key"},
		{"GITHUB_PRIVATE_KEY_PATH", "App key"},
		{"STORE_DSN", "database credentials"},
	} {
		t.Run("refuses "+tt.env, func(t *testing.T) {
			t.Setenv("TEMPORAL_ADDRESS", "temporal:7233")
			t.Setenv("GITHUB_WEBHOOK_SECRET", "s")
			t.Setenv(tt.env, "x")

			if _, err := LoadRole(RoleIngest); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("LoadRole(ingest) with %s = %v, want a refusal naming %q", tt.env, err, tt.want)
			}
		})
	}

	t.Run("requires the webhook secret", func(t *testing.T) {
		t.Setenv("TEMPORAL_ADDRESS", "temporal:7233")

		if _, err := LoadRole(RoleIngest); err == nil || !strings.Contains(err.Error(), "GITHUB_WEBHOOK_SECRET") {
			t.Errorf("LoadRole(ingest) = %v, want GITHUB_WEBHOOK_SECRET required", err)
		}
	})
}

func TestLoadRole_Worker(t *testing.T) {
	t.Run("complete", func(t *testing.T) {
		setWorkerEnv(t)

		if _, err := LoadRole(RoleWorker); err != nil {
			t.Fatalf("LoadRole(worker) = %v", err)
		}
	})

	for _, env := range []string{"TEMPORAL_ADDRESS", "GITHUB_APP_ID", "GITHUB_PRIVATE_KEY", "STORE_DSN", "GUARDIAN_CONFIG"} {
		t.Run("requires "+env, func(t *testing.T) {
			setWorkerEnv(t)
			t.Setenv(env, "")

			if _, err := LoadRole(RoleWorker); err == nil {
				t.Errorf("LoadRole(worker) without %s = nil, want an error", env)
			}
		})
	}
}

func TestLoadRole_AllRunsTheWorkerInProcess(t *testing.T) {
	setWorkerEnv(t)

	if _, err := LoadRole(RoleIngest | RoleWorker); err != nil {
		t.Errorf("LoadRole(ingest|worker) = %v: ingest's key refusal must not apply to all", err)
	}
}
