package manage_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/manage"
)

func TestReadTrackerHealthMissing(t *testing.T) {
	t.Parallel()
	nonexistent := filepath.Join(t.TempDir(), "nonexistent.observer-health.json")
	now := time.Now().UTC()

	health, err := manage.ReadTrackerHealth(nonexistent, now, time.Minute)
	if !errors.Is(err, manage.ErrHealthMissing) {
		t.Fatalf("err = %v, want ErrHealthMissing", err)
	}
	if health.Status != manage.HealthStatusMissing {
		t.Fatalf("health.Status = %q, want %q", health.Status, manage.HealthStatusMissing)
	}
	if health.Fresh {
		t.Fatal("health.Fresh should be false for missing sidecar")
	}
}

func TestReadTrackerHealthCorrupt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.observer-health.json")
	if err := os.WriteFile(path, []byte("{invalid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	health, err := manage.ReadTrackerHealth(path, now, time.Minute)
	if !errors.Is(err, manage.ErrHealthCorrupt) {
		t.Fatalf("err = %v, want ErrHealthCorrupt", err)
	}
	if health.Status != manage.HealthStatusCorrupt {
		t.Fatalf("health.Status = %q, want %q", health.Status, manage.HealthStatusCorrupt)
	}
	if health.Fresh {
		t.Fatal("health.Fresh should be false for corrupt sidecar")
	}
}

func TestReadTrackerHealthDegraded(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()

	t.Run("enumeration error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "degraded.observer-health.json")
		data := `{
			"pid": 1234,
			"interval": 300000000,
			"started_at": "2026-09-19T20:00:00Z",
			"last_attempt_at": "2026-09-19T20:01:00Z",
			"last_success_at": "2026-09-19T20:00:30Z",
			"last_enumeration_error": "permission denied on /proc/999/status",
			"degraded": true
		}`
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}

		health, err := manage.ReadTrackerHealth(path, now, time.Minute)
		if !errors.Is(err, manage.ErrHealthDegraded) {
			t.Fatalf("err = %v, want ErrHealthDegraded", err)
		}
		if health.Status != manage.HealthStatusDegraded {
			t.Fatalf("health.Status = %q, want %q", health.Status, manage.HealthStatusDegraded)
		}
		if health.Message != "permission denied on /proc/999/status" {
			t.Fatalf("health.Message = %q", health.Message)
		}
		if health.Fresh {
			t.Fatal("health.Fresh should be false for degraded tracker")
		}
	})

	t.Run("degraded flag without error string", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "degraded-flag.observer-health.json")
		data := `{
			"pid": 1234,
			"interval": 300000000,
			"started_at": "2026-09-19T20:00:00Z",
			"last_attempt_at": "2026-09-19T20:01:00Z",
			"last_success_at": "2026-09-19T20:00:30Z",
			"degraded": true
		}`
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}

		health, err := manage.ReadTrackerHealth(path, now, time.Minute)
		if !errors.Is(err, manage.ErrHealthDegraded) {
			t.Fatalf("err = %v, want ErrHealthDegraded", err)
		}
		if health.Status != manage.HealthStatusDegraded {
			t.Fatalf("health.Status = %q, want %q", health.Status, manage.HealthStatusDegraded)
		}
	})
}

func TestReadTrackerHealthIncomplete(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "incomplete.observer-health.json")
	data := `{
		"pid": 1234,
		"interval": 300000000,
		"started_at": "2026-09-19T20:00:00Z",
		"last_attempt_at": "2026-09-19T20:00:05Z",
		"last_success_at": "0001-01-01T00:00:00Z",
		"degraded": false
	}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	health, err := manage.ReadTrackerHealth(path, now, time.Minute)
	if !errors.Is(err, manage.ErrReconciliationIncomplete) {
		t.Fatalf("err = %v, want ErrReconciliationIncomplete", err)
	}
	if health.Fresh {
		t.Fatal("health.Fresh should be false for incomplete reconciliation")
	}
}

func TestReadTrackerHealthStale(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 19, 20, 10, 0, 0, time.UTC)
	dir := t.TempDir()
	path := filepath.Join(dir, "stale.observer-health.json")
	data := `{
		"pid": 1234,
		"interval": 300000000,
		"started_at": "2026-09-19T20:00:00Z",
		"last_attempt_at": "2026-09-19T20:05:00Z",
		"last_success_at": "2026-09-19T20:05:00Z",
		"degraded": false
	}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	// 5 minutes old with 1-minute maxAge must be classified as stale
	health, err := manage.ReadTrackerHealth(path, now, time.Minute)
	if !errors.Is(err, manage.ErrHealthStale) {
		t.Fatalf("err = %v, want ErrHealthStale", err)
	}
	if health.Status != manage.HealthStatusStale {
		t.Fatalf("health.Status = %q, want %q", health.Status, manage.HealthStatusStale)
	}
	if health.Fresh {
		t.Fatal("health.Fresh should be false for stale tracker")
	}
	if health.Age != 5*time.Minute {
		t.Fatalf("health.Age = %v, want 5m", health.Age)
	}
}

func TestReadTrackerHealthHealthy(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 19, 20, 10, 0, 0, time.UTC)
	dir := t.TempDir()
	path := filepath.Join(dir, "healthy.observer-health.json")
	data := `{
		"pid": 1234,
		"interval": 300000000,
		"started_at": "2026-09-19T20:00:00Z",
		"last_attempt_at": "2026-09-19T20:09:55Z",
		"last_success_at": "2026-09-19T20:09:55Z",
		"cycles": 120,
		"observations": 45,
		"sessions": 3,
		"degraded": false
	}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	// 5 seconds old with 1-minute maxAge is healthy
	health, err := manage.ReadTrackerHealth(path, now, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if health.Status != manage.HealthStatusHealthy {
		t.Fatalf("health.Status = %q, want %q", health.Status, manage.HealthStatusHealthy)
	}
	if !health.Fresh {
		t.Fatal("health.Fresh should be true for recent reconciliation")
	}
	if health.Cycles != 120 || health.Observations != 45 || health.Sessions != 3 {
		t.Fatalf("unexpected counters: cycles=%d, obs=%d, sess=%d", health.Cycles, health.Observations, health.Sessions)
	}

	// Test JSON marshaling preserves stable fields
	marshaled, err := json.Marshal(health)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(marshaled, &raw); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"pid", "interval", "started_at", "last_attempt_at", "last_success_at", "cycles", "observations", "sessions", "degraded", "fresh", "age", "status"} {
		if _, ok := raw[expected]; !ok {
			t.Errorf("missing expected field %q in serialized tracker health", expected)
		}
	}
}

func TestManagerTrackerHealth(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	storePath := filepath.Join(dir, "sessions.json")
	healthPath := storePath + ".observer-health.json"
	data := `{
		"pid": 5678,
		"interval": 300000000,
		"started_at": "2026-09-19T20:00:00Z",
		"last_attempt_at": "2026-09-19T20:00:01Z",
		"last_success_at": "2026-09-19T20:00:01Z",
		"degraded": false
	}`
	if err := os.WriteFile(healthPath, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	m := manage.New(manage.Config{
		Binary:             "aht",
		StorePath:          storePath,
		TrackerInterval:    0,
		TrackerGracePeriod: 0,
	})

	if m.HealthPath() != healthPath {
		t.Fatalf("m.HealthPath() = %q, want %q", m.HealthPath(), healthPath)
	}

	health, err := m.TrackerHealth(t.Context())
	// With time.Now() >> 2026-09-19, this should be classified as stale
	if !errors.Is(err, manage.ErrHealthStale) {
		t.Fatalf("expected ErrHealthStale for 2026-09-19 file against current time, got %v", err)
	}
	if health.PID != 5678 {
		t.Fatalf("health.PID = %d, want 5678", health.PID)
	}
}
