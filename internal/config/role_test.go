package config

import (
	"strings"
	"testing"
	"time"
)

// Tests here use t.Setenv, so none is parallel.

// setWorkerEnv sets what every controls worker role shares; a test
// adds the App's credential set with setAppEnv.
func setWorkerEnv(t *testing.T) {
	t.Helper()

	t.Setenv("TEMPORAL_ADDRESS", "temporal:7233")
	t.Setenv("STORE_DSN", "postgres://x")
	t.Setenv("GUARDIAN_CONFIG", "/etc/repo-guardian/policy")
}

// setIngestRoute gives ingest the Evaluation App's route.
func setIngestRoute(t *testing.T) {
	t.Helper()

	t.Setenv("TEMPORAL_ADDRESS", "temporal:7233")
	t.Setenv("EVAL_GITHUB_APP_ID", "1")
	t.Setenv("EVAL_WEBHOOK_SECRET", "e")
}

func TestLoadRole_Ingest(t *testing.T) {
	t.Run("a route and temporal suffice", func(t *testing.T) {
		setIngestRoute(t)

		if _, err := LoadRole(RoleIngest); err != nil {
			t.Fatalf("LoadRole(ingest) = %v, want no error without App key, database or Valkey", err)
		}
	})

	for _, tt := range []struct{ env, want string }{
		{"EVAL_GITHUB_PRIVATE_KEY_PATH", "App key"},
		{"STORE_DSN", "database credentials"},
		{"STORE_DSN_EVALUATOR", "database credentials"},
		{"STORE_DSN_REMEDIATOR", "database credentials"},
	} {
		t.Run("refuses "+tt.env, func(t *testing.T) {
			setIngestRoute(t)
			t.Setenv(tt.env, "x")

			if _, err := LoadRole(RoleIngest); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("LoadRole(ingest) with %s = %v, want a refusal naming %q", tt.env, err, tt.want)
			}
		})
	}

	// The single-App route is gone (IMPL-0029 7.1): its secret alone
	// mounts nothing.
	t.Run("requires a controls App route", func(t *testing.T) {
		t.Setenv("TEMPORAL_ADDRESS", "temporal:7233")
		t.Setenv("GITHUB_WEBHOOK_SECRET", "s")

		if _, err := LoadRole(RoleIngest); err == nil || !strings.Contains(err.Error(), "EVAL_WEBHOOK_SECRET or REMEDIATE_WEBHOOK_SECRET") {
			t.Errorf("LoadRole(ingest) = %v, want a controls App route required", err)
		}
	})
}

// TestLoadRole_AllRunsTheWorkersInProcess: ingest's key refusal does
// not apply when the evaluator runs in the same process.
func TestLoadRole_AllRunsTheWorkersInProcess(t *testing.T) {
	setWorkerEnv(t)
	setAppEnv(t, envPrefixEval)

	if _, err := LoadRole(RoleIngest | RoleEvaluator); err != nil {
		t.Errorf("LoadRole(ingest|evaluator) = %v: ingest's key refusal must not apply to all", err)
	}
}

func TestLoadV2Durations(t *testing.T) {
	cfg := &Config{}
	if err := loadV2Durations(cfg); err != nil {
		t.Fatal(err)
	}

	if cfg.EvalInterval != 24*time.Hour || cfg.PolicyRolloutWindow != 24*time.Hour || cfg.ChecksRetention != 2160*time.Hour {
		t.Errorf("defaults = %v %v %v", cfg.EvalInterval, cfg.PolicyRolloutWindow, cfg.ChecksRetention)
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

	t.Run("OIDC_CLIENT_CLAIM defaults to azp, takes a value, refuses a blank", func(t *testing.T) {
		for _, tt := range []struct {
			name, value string
			set         bool
			want        string
			wantErr     bool
		}{
			{name: "unset", want: "azp"},
			{name: "okta", value: "cid", set: true, want: "cid"},
			{name: "blank", value: "  ", set: true, wantErr: true},
		} {
			t.Run(tt.name, func(t *testing.T) {
				setAPIEnv(t)
				if tt.set {
					t.Setenv("OIDC_CLIENT_CLAIM", tt.value)
				}

				cfg, err := LoadRole(RoleAPI)
				if tt.wantErr {
					if err == nil || !strings.Contains(err.Error(), "OIDC_CLIENT_CLAIM") {
						t.Errorf("LoadRole(api) with OIDC_CLIENT_CLAIM=%q = %v, want an error naming it", tt.value, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("LoadRole(api) = %v", err)
				}
				if cfg.API.OIDCClientClaim != tt.want {
					t.Errorf("OIDCClientClaim = %q, want %q", cfg.API.OIDCClientClaim, tt.want)
				}
			})
		}
	})

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
	setAppEnv(t, envPrefixEval)
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

func TestLoadRole_IngestAppRoutes(t *testing.T) {
	t.Run("a route secret needs its App id", func(t *testing.T) {
		t.Setenv("TEMPORAL_ADDRESS", "temporal:7233")
		t.Setenv("EVAL_WEBHOOK_SECRET", "e")

		if _, err := LoadRole(RoleIngest); err == nil || !strings.Contains(err.Error(), "EVAL_GITHUB_APP_ID") {
			t.Errorf("LoadRole(ingest) = %v, want EVAL_GITHUB_APP_ID required", err)
		}
	})

	t.Run("refuses an App key", func(t *testing.T) {
		setIngestRoute(t)
		t.Setenv("REMEDIATE_GITHUB_PRIVATE_KEY_PATH", "/keys/r.pem")

		if _, err := LoadRole(RoleIngest); err == nil || !strings.Contains(err.Error(), "App key") {
			t.Errorf("LoadRole(ingest) with a remediation key = %v, want a refusal", err)
		}
	})

	t.Run("both routes", func(t *testing.T) {
		t.Setenv("TEMPORAL_ADDRESS", "temporal:7233")
		t.Setenv("EVAL_GITHUB_APP_ID", "1")
		t.Setenv("EVAL_WEBHOOK_SECRET", "e")
		t.Setenv("REMEDIATE_GITHUB_APP_ID", "2")
		t.Setenv("REMEDIATE_WEBHOOK_SECRET", "r")

		if _, err := LoadRole(RoleIngest); err != nil {
			t.Errorf("LoadRole(ingest) with both routes = %v, want nil", err)
		}
	})
}

// TestLoadRole_AllRunsControlsHalvesOnlyWhenConfigured is IMPL-0028
// task 4.1: all includes both controls roles but runs a half only when
// its App is configured, and a configured App's half is then validated.
func TestLoadRole_AllRunsControlsHalvesOnlyWhenConfigured(t *testing.T) {
	t.Run("no App configured", func(t *testing.T) {
		cfg := &Config{}
		if cfg.RunsControls(RoleAll, RoleEvaluator) || cfg.RunsControls(RoleAll, RoleRemediator) {
			t.Error("all runs a controls half with no App configured")
		}
	})

	t.Run("a configured App is validated", func(t *testing.T) {
		setWorkerEnv(t)
		t.Setenv("EVAL_GITHUB_APP_ID", "7")

		if _, err := LoadRole(RoleAll &^ RoleAPI); err == nil || !strings.Contains(err.Error(), "EVAL_GITHUB_PRIVATE_KEY_PATH") {
			t.Errorf("LoadRole(all) with half an Evaluation App = %v, want its key required", err)
		}
	})

	t.Run("alone always runs", func(t *testing.T) {
		cfg := &Config{}
		if !cfg.RunsControls(RoleRemediator, RoleRemediator) {
			t.Error("RunsControls(remediator, remediator) = false, want true")
		}
	})
}

// TestParse_WorkerSizing is IMPL-0028 task 4.9: each controls role is
// sized by its own env vars, independent of the rc worker's.
func TestParse_WorkerSizing(t *testing.T) {
	t.Setenv("EVALUATOR_CONCURRENCY", "20")
	t.Setenv("EVALUATOR_DB_POOL_SIZE", "12")
	t.Setenv("REMEDIATOR_CONCURRENCY", "3")
	t.Setenv("WORKER_ACTIVITY_CONCURRENCY", "99")

	cfg, err := parse()
	if err != nil {
		t.Fatalf("parse = %v", err)
	}

	if want := (WorkerSizing{Concurrency: 20, DBPoolSize: 12}); cfg.Evaluator != want {
		t.Errorf("Evaluator = %+v, want %+v", cfg.Evaluator, want)
	}

	if want := (WorkerSizing{Concurrency: 3}); cfg.Remediator != want {
		t.Errorf("Remediator = %+v, want %+v", cfg.Remediator, want)
	}

	t.Setenv("REMEDIATOR_DB_POOL_SIZE", "-1")

	if _, err := parse(); err == nil || !strings.Contains(err.Error(), "REMEDIATOR_DB_POOL_SIZE") {
		t.Errorf("parse with a negative REMEDIATOR_DB_POOL_SIZE = %v, want an error naming it", err)
	}
}

// TestWorkerStoreDSN is IMPL-0030 1.12 (INV-0023 F7.1): each controls
// half connects with its own STORE_DSN_<HALF>, falls back to STORE_DSN
// only when that is unset, and a remediator that can resolve neither
// refuses to start.
func TestWorkerStoreDSN(t *testing.T) {
	cfg := &Config{StoreDSN: "postgres://owner", StoreDSNEvaluator: "postgres://rg_evaluator"}

	if dsn, env := cfg.WorkerStoreDSN(RoleEvaluator); dsn != "postgres://rg_evaluator" || env != "STORE_DSN_EVALUATOR" {
		t.Errorf("WorkerStoreDSN(evaluator) = %q, %q; want its own", dsn, env)
	}

	if dsn, env := cfg.WorkerStoreDSN(RoleRemediator); dsn != "postgres://owner" || env != "STORE_DSN" {
		t.Errorf("WorkerStoreDSN(remediator) = %q, %q; want the STORE_DSN fallback", dsn, env)
	}

	cfg.StoreDSNRemediator = "postgres://rg_remediator"
	if dsn, _ := cfg.WorkerStoreDSN(RoleRemediator); dsn != "postgres://rg_remediator" {
		t.Errorf("WorkerStoreDSN(remediator) = %q, want its own over STORE_DSN", dsn)
	}

	t.Run("remediator refuses without a DSN", func(t *testing.T) {
		t.Setenv("TEMPORAL_ADDRESS", "temporal:7233")
		t.Setenv("GUARDIAN_CONFIG", "/etc/repo-guardian/policy")
		t.Setenv("STORE_DSN_EVALUATOR", "postgres://rg_evaluator")
		setAppEnv(t, "REMEDIATE")

		if _, err := LoadRole(RoleRemediator); err == nil || !strings.Contains(err.Error(), "STORE_DSN_REMEDIATOR") {
			t.Errorf("LoadRole(remediator) = %v, want a refusal naming STORE_DSN_REMEDIATOR", err)
		}

		t.Setenv("STORE_DSN_REMEDIATOR", "postgres://rg_remediator")

		if _, err := LoadRole(RoleRemediator); err != nil {
			t.Errorf("LoadRole(remediator) with its own DSN = %v", err)
		}
	})
}
