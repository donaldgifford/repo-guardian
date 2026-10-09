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

	for _, stale := range []string{"30m", "8761h"} {
		t.Run("rejects PR_STALE_AFTER="+stale, func(t *testing.T) {
			setAPIEnv(t)
			t.Setenv("PR_STALE_AFTER", stale)

			if _, err := LoadRole(RoleAPI); err == nil || !strings.Contains(err.Error(), "PR_STALE_AFTER") {
				t.Errorf("LoadRole(api) with PR_STALE_AFTER=%s = %v", stale, err)
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

func setAppEnv(t *testing.T, prefix string) {
	t.Helper()

	t.Setenv(prefix+"_GITHUB_APP_ID", "7")
	t.Setenv(prefix+"_GITHUB_PRIVATE_KEY_PATH", "/keys/"+prefix+".pem")
	t.Setenv(prefix+"_WEBHOOK_SECRET", "s-"+prefix)
}

// TestLoadRole_ControlsWorkers is IMPL-0028 task 3.1: each controls
// worker role needs its own App's credential set, and the other App's
// set does not stand in for it.
func TestLoadRole_ControlsWorkers(t *testing.T) {
	for _, tt := range []struct {
		name          string
		role          Role
		prefix, other string
	}{
		{"evaluator", RoleEvaluator, envPrefixEval, envPrefixRemediate},
		{"remediator", RoleRemediator, envPrefixRemediate, envPrefixEval},
	} {
		t.Run(tt.name+" complete", func(t *testing.T) {
			setWorkerEnv(t)
			setAppEnv(t, tt.prefix)

			if _, err := LoadRole(tt.role); err != nil {
				t.Fatalf("LoadRole(%s) = %v, want nil", tt.name, err)
			}
		})

		for _, env := range []string{"_GITHUB_APP_ID", "_GITHUB_PRIVATE_KEY_PATH"} {
			t.Run(tt.name+" requires "+tt.prefix+env, func(t *testing.T) {
				setWorkerEnv(t)
				setAppEnv(t, tt.prefix)
				t.Setenv(tt.prefix+env, "")

				if _, err := LoadRole(tt.role); err == nil || !strings.Contains(err.Error(), tt.prefix+env) {
					t.Errorf("LoadRole(%s) without %s = %v, want an error naming it", tt.name, tt.prefix+env, err)
				}
			})
		}

		t.Run(tt.name+" refuses the other App's set", func(t *testing.T) {
			setWorkerEnv(t)
			setAppEnv(t, tt.other)

			if _, err := LoadRole(tt.role); err == nil {
				t.Errorf("LoadRole(%s) with only the %s set = nil, want a refusal", tt.name, tt.other)
			}
		})
	}
}

func TestParse_AppCredentials(t *testing.T) {
	setAppEnv(t, envPrefixEval)
	t.Setenv("REMEDIATE_GITHUB_APP_ID", "8")

	cfg, err := parse()
	if err != nil {
		t.Fatalf("parse = %v", err)
	}

	if got := cfg.Credentials(AppEval); got != (AppCredentials{AppID: 7, PrivateKeyPath: "/keys/EVAL.pem", WebhookSecret: "s-EVAL"}) {
		t.Errorf("Credentials(eval) = %+v", got)
	}

	if got := cfg.Credentials(AppRemediate).AppID; got != 8 {
		t.Errorf("Credentials(remediate).AppID = %d, want 8", got)
	}

	t.Setenv("EVAL_GITHUB_APP_ID", "nope")

	if _, err := parse(); err == nil || !strings.Contains(err.Error(), "EVAL_GITHUB_APP_ID") {
		t.Errorf("parse with a bad EVAL_GITHUB_APP_ID = %v, want an error naming it", err)
	}
}
