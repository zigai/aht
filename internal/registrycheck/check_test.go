package registrycheck

import (
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestSessionsReportsEveryViolation(t *testing.T) {
	t.Parallel()

	end := registry.NativeLifecycleEnd
	sessions := []registry.Session{
		{
			ID:        "broken",
			Harness:   registry.Harness("fixture"),
			CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			Liveness: registry.NewLiveness(
				registry.PresenceLive,
				registry.ActivityRunning,
				nil,
			),
			Observations: registry.Observations{
				Native: &registry.NativeObservation{Lifecycle: &end},
			},
		},
		{ID: "broken"},
	}

	err := Sessions(sessions)
	if err == nil {
		t.Fatal("expected invariant violations")
	}

	for _, part := range []string{
		"session identity",
		"session timestamps",
		"session liveness",
		"native end is terminal",
		`session "broken"`,
	} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("violations %q omit %q", err, part)
		}
	}
}

func TestCoverageReportsMissingSituations(t *testing.T) {
	t.Parallel()

	coverage := NewCoverage()
	coverage.Hit("end")

	missing := coverage.Missing("end", "reopen", "gc")
	if len(missing) != 2 || missing[0] != "reopen" || missing[1] != "gc" {
		t.Fatalf("missing = %v", missing)
	}
}
