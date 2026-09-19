package aht_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/aht"
	"github.com/zigai/aht/pkg/registry"
)

func TestInvalidModeDoesNotCreateState(t *testing.T) {
	t.Parallel()
	storePath := filepath.Join(t.TempDir(), "state", "sessions.json")
	c := aht.New(aht.Config{Mode: "typo", StorePath: storePath})
	if _, err := c.List(t.Context(), aht.Filter{}); !errors.Is(err, aht.ErrInvalidMode) {
		t.Fatalf("List error = %v, want ErrInvalidMode", err)
	}
	if _, err := os.Stat(filepath.Dir(storePath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid mode touched state directory: %v", err)
	}
}

func TestAhtPackageTypesAndDefaults(t *testing.T) {
	t.Parallel()

	if aht.PresenceLive != registry.PresenceLive {
		t.Fatalf("PresenceLive = %q, want %q", aht.PresenceLive, registry.PresenceLive)
	}
	if aht.HarnessPi != registry.HarnessPi {
		t.Fatalf("HarnessPi = %q, want %q", aht.HarnessPi, registry.HarnessPi)
	}
	if aht.ActivityRunning != registry.ActivityRunning {
		t.Fatalf("ActivityRunning = %q, want %q", aht.ActivityRunning, registry.ActivityRunning)
	}

	c := aht.New(aht.Config{
		StorePath:  filepath.Join(t.TempDir(), "sessions.json"),
		SocketPath: filepath.Join(t.TempDir(), "nonexistent.sock"),
		Mode:       aht.ModeRealtimeOnly,
	})

	if c.Mode() != aht.ModeRealtimeOnly {
		t.Fatalf("Mode() = %q, want %q", c.Mode(), aht.ModeRealtimeOnly)
	}

	_, err := c.List(t.Context(), aht.Filter{Presence: aht.PresenceLive})
	if !aht.IsUnavailable(err) {
		t.Fatalf("List() err = %v, want ErrUnavailable", err)
	}
}

func TestAhtFacadeResolveAndCurrent(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewFileStore(storePath)

	live := aht.PresenceLive
	activity := aht.ActivityRunning
	obs := aht.Observation{
		Source:      registry.ObservationSourceNative,
		Evidence:    registry.ObservationEvidenceNativeEvent,
		Harness:     aht.HarnessClaude,
		Identity:    aht.ObservationIdentity{SessionID: "sess-facade-1"},
		Lifecycle:   nil,
		Presence:    &live,
		Activity:    &activity,
		Attributes:  nil,
		Process:     nil,
		Tmux:        nil,
		Multiplexer: nil,
		Catalog:     nil,
		Screen:      nil,
		RawPayload:  nil,
		ObservedAt:  time.Now().UTC(),
	}

	created, err := store.Observe(t.Context(), obs)
	if err != nil {
		t.Fatal(err)
	}

	c := aht.New(aht.Config{
		StorePath:  storePath,
		SocketPath: "",
		Mode:       aht.ModeDurableOnly,
	})

	// Test aht.Resolve
	resolved, err := aht.Resolve(t.Context(), c, aht.Selector{
		Reference: "sess-facade-1",
	})
	if err != nil {
		t.Fatalf("aht.Resolve failed: %v", err)
	}
	if resolved.ID != created.ID {
		t.Fatalf("resolved ID = %q, want %q", resolved.ID, created.ID)
	}

	// Test aht.Current when no agent is active
	_, err = aht.Current(t.Context(), c)
	if !errors.Is(err, aht.ErrNoCurrentSession) {
		t.Fatalf("expected ErrNoCurrentSession, got: %v", err)
	}
}

func TestAhtErrorSentinels(t *testing.T) {
	t.Parallel()

	sentinels := []struct {
		name   string
		ahtErr error
		target error
	}{
		{"ErrSessionNotFound", aht.ErrSessionNotFound, registry.ErrSessionNotFound},
		{"ErrObservationConflict", aht.ErrObservationConflict, registry.ErrObservationConflict},
		{"ErrInvalidObservation", aht.ErrInvalidObservation, registry.ErrInvalidObservation},
		{"ErrCorruptStore", aht.ErrCorruptStore, registry.ErrCorruptStore},
		{"ErrStoreTooLarge", aht.ErrStoreTooLarge, registry.ErrStoreTooLarge},
		{"ErrHarnessRequired", aht.ErrHarnessRequired, registry.ErrHarnessRequired},
		{"ErrObservationIdentity", aht.ErrObservationIdentity, registry.ErrObservationIdentity},
		{"ErrUnknownHarness", aht.ErrUnknownHarness, registry.ErrUnknownHarness},
		{"ErrUnknownPresence", aht.ErrUnknownPresence, registry.ErrUnknownPresence},
		{"ErrUnknownActivity", aht.ErrUnknownActivity, registry.ErrUnknownActivity},
	}

	for _, s := range sentinels {
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			if !errors.Is(s.ahtErr, s.target) {
				t.Fatalf("errors.Is(%v, %v) = false, want true", s.ahtErr, s.target)
			}
			if !errors.Is(s.target, s.ahtErr) {
				t.Fatalf("errors.Is(%v, %v) = false, want true", s.target, s.ahtErr)
			}
		})
	}
}

func TestAhtFacadeDiagnostics(t *testing.T) {
	t.Parallel()

	// Capabilities
	caps, ok := aht.Capabilities(aht.HarnessPi)
	if !ok || caps.Harness != aht.HarnessPi {
		t.Fatalf("aht.Capabilities(HarnessPi) = %+v, %v", caps, ok)
	}
	all := aht.AllCapabilities()
	if len(all) == 0 {
		t.Fatal("aht.AllCapabilities() is empty")
	}

	// ReadTrackerHealth on nonexistent path returns ErrHealthMissing
	_, err := aht.ReadTrackerHealth(filepath.Join(t.TempDir(), "nonexistent.json"), time.Time{}, 0)
	if !errors.Is(err, aht.ErrHealthMissing) {
		t.Fatalf("ReadTrackerHealth err = %v, want ErrHealthMissing", err)
	}

	// ExplainSession
	running := aht.ActivityRunning
	session := aht.Session{
		ID:       "s-facade",
		Harness:  aht.HarnessPi,
		Presence: aht.PresenceLive,
		Activity: &running,
	}
	exp, err := aht.ExplainSession(t.Context(), session, aht.ExplainOptions{})
	if err != nil {
		t.Fatalf("ExplainSession err = %v", err)
	}
	if exp.SessionID != "s-facade" {
		t.Fatalf("exp.SessionID = %q, want s-facade", exp.SessionID)
	}
}
