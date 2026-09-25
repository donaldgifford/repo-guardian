package config

import (
	"strings"
	"testing"
	"time"
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

func TestLoadV2Durations(t *testing.T) {
	cfg := &Config{}
	if err := loadV2Durations(cfg); err != nil {
		t.Fatal(err)
	}

	if cfg.CheckInterval != 24*time.Hour || cfg.PolicyRolloutWindow != 24*time.Hour || cfg.ChecksRetention != 2160*time.Hour {
		t.Errorf("defaults = %v %v %v", cfg.CheckInterval, cfg.PolicyRolloutWindow, cfg.ChecksRetention)
	}

	t.Setenv("CHECKS_RETENTION", "0s")

	if err := loadV2Durations(cfg); err == nil {
		t.Error("CHECKS_RETENTION=0s accepted, want an error")
	}
}

func setAPIEnv(t *testing.T) {
	t.Helper()

	t.Setenv("STORE_RO_DSN", "postgres://ro")
	t.Setenv("OIDC_ISSUER", "https://idp.example")
	t.Setenv("OIDC_AUDIENCE", "repo-guardian-api")
	t.Setenv("API_AUTHZ_CONFIG", "/etc/repo-guardian/authz.yaml")
}

func TestLoadRole_API(t *testing.T) {
	t.Run("needs no Temporal, App key or read-write DSN", func(t *testing.T) {
		setAPIEnv(t)

		cfg, err := LoadRole(RoleAPI)
		if err != nil {
			t.Fatalf("LoadRole(api) = %v", err)
		}

		if cfg.APIListenAddr(RoleAPI) != ":8080" || cfg.APIStoreDSN(RoleAPI) != "postgres://ro" {
			t.Errorf("listen %s, dsn %s", cfg.APIListenAddr(RoleAPI), cfg.APIStoreDSN(RoleAPI))
		}
	})

	for _, env := range []string{"STORE_RO_DSN", "OIDC_ISSUER", "OIDC_AUDIENCE", "API_AUTHZ_CONFIG"} {
		t.Run("requires "+env, func(t *testing.T) {
			setAPIEnv(t)
			t.Setenv("STORE_DSN", "postgres://rw")
			t.Setenv(env, "")

			if _, err := LoadRole(RoleAPI); err == nil || !strings.Contains(err.Error(), env) {
				t.Errorf("LoadRole(api) without %s = %v", env, err)
			}
		})
	}

	t.Run("disabled auth needs only the DSN", func(t *testing.T) {
		t.Setenv("STORE_RO_DSN", "postgres://ro")
		t.Setenv("API_AUTH_ENABLED", "false")

		if _, err := LoadRole(RoleAPI); err != nil {
			t.Errorf("LoadRole(api) with auth disabled = %v", err)
		}
	})
}

func TestLoadRole_AllRunsTheAPIOnItsOwnListener(t *testing.T) {
	setWorkerEnv(t)
	setAPIEnv(t)
	t.Setenv("STORE_RO_DSN", "")

	cfg, err := LoadRole(RoleAll)
	if err != nil {
		t.Fatalf("LoadRole(all) = %v", err)
	}

	if cfg.APIListenAddr(RoleAll) != ":8081" || cfg.APIStoreDSN(RoleAll) != "postgres://x" {
		t.Errorf("all: api listen %s, dsn %s; want :8081 and the STORE_DSN fallback", cfg.APIListenAddr(RoleAll), cfg.APIStoreDSN(RoleAll))
	}

	t.Setenv("API_LISTEN_ADDR", ":8080")

	if _, err := LoadRole(RoleAll); err == nil || !strings.Contains(err.Error(), "API_LISTEN_ADDR") {
		t.Errorf("LoadRole(all) with the API on the main port = %v, want a conflict error", err)
	}
}
