package temporal

import (
	"runtime/debug"
	"testing"
)

func TestWorkerConfigFromEnv(t *testing.T) {
	cfg := &Config{TaskQueue: DefaultTaskQueue}

	t.Run("defaults", func(t *testing.T) {
		wc, err := WorkerConfigFromEnv(cfg)
		if err != nil {
			t.Fatalf("WorkerConfigFromEnv: %v", err)
		}

		if wc.ActivityConcurrency != DefaultActivityConcurrency || wc.TaskQueue != DefaultTaskQueue || wc.BuildID == "" {
			t.Errorf("config = %+v", wc)
		}
	})

	t.Run("overrides", func(t *testing.T) {
		t.Setenv("WORKER_ACTIVITY_CONCURRENCY", "25")
		t.Setenv("TEMPORAL_BUILD_ID", "v2.0.0-rc.1")

		wc, err := WorkerConfigFromEnv(cfg)
		if err != nil {
			t.Fatalf("WorkerConfigFromEnv: %v", err)
		}

		if wc.ActivityConcurrency != 25 || wc.BuildID != "v2.0.0-rc.1" {
			t.Errorf("config = %+v", wc)
		}
	})

	for _, bad := range []string{"0", "-1", "ten"} {
		t.Run("rejects "+bad, func(t *testing.T) {
			t.Setenv("WORKER_ACTIVITY_CONCURRENCY", bad)

			if _, err := WorkerConfigFromEnv(cfg); err == nil {
				t.Error("want an error")
			}
		})
	}
}

func TestBuildIDFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		info debug.BuildInfo
		want string
	}{
		{"tagged", debug.BuildInfo{Main: debug.Module{Version: "v2.0.0"}}, "v2.0.0"},
		{
			"devel with revision",
			debug.BuildInfo{
				Main:     debug.Module{Version: develVersion},
				Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}},
			},
			"0123456789ab",
		},
		{"nothing", debug.BuildInfo{Main: debug.Module{Version: develVersion}}, devBuildID},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := buildIDFrom(&tt.info); got != tt.want {
				t.Errorf("buildIDFrom = %q, want %q", got, tt.want)
			}
		})
	}
}
