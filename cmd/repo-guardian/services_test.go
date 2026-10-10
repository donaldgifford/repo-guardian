package main

import (
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/config"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// TestServiceStarter_SchedulesPerRole is IMPL-0028 task 4.4: each role
// ensures only its own Schedules.
func TestServiceStarter_SchedulesPerRole(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{DiscoveryEnabled: true, DiscoveryInterval: time.Hour, ComplianceSnapshotInterval: 24 * time.Hour}
	s := &serviceStarter{cfg: cfg, taskQueue: "repo-guardian"}

	ensure, remove := s.schedules(config.RoleWorker)
	if len(ensure) != 2 || ensure[0].ID != workflows.DiscoveryScheduleID || ensure[1].ID != workflows.SnapshotScheduleID || len(remove) != 0 {
		t.Errorf("rc worker schedules = %+v, remove %v; want discovery and snapshot", ensure, remove)
	}

	for _, role := range []config.Role{config.RoleEvaluator, config.RoleRemediator, config.RoleIngest} {
		if ensure, remove := s.schedules(role); len(ensure) != 0 || len(remove) != 0 {
			t.Errorf("schedules(%v) = %+v, remove %v; want none of the rc's", role, ensure, remove)
		}
	}

	cfg.DiscoveryEnabled = false

	if ensure, remove := s.schedules(config.RoleWorker); len(ensure) != 1 || len(remove) != 1 || remove[0] != workflows.DiscoveryScheduleID {
		t.Errorf("rc worker with discovery off = %+v, remove %v; want snapshot only and discovery removed", ensure, remove)
	}
}
