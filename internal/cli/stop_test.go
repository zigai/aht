package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

func TestStopExplicitSkippedTargetReturnsReasonAndError(t *testing.T) {
	path, session := createSkippedStopSession(t)
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "stop", shortRegistryID(session.ID), "--dry-run"}, &stdout, &bytes.Buffer{}); !errors.Is(err, errStopTargetSkipped) {
		t.Fatalf("single skipped stop error = %v", err)
	}
	if !strings.Contains(stdout.String(), "process no longer exists") || !strings.Contains(stdout.String(), "skipped=1") {
		t.Fatalf("single stop output = %q", stdout.String())
	}
}

func TestStopAllKeepsSkippedResultsMachineReadable(t *testing.T) {
	path, _ := createSkippedStopSession(t)
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "--json", "stop", "--all", "--dry-run"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result["stoppable"] != float64(0) || result["skipped"] != float64(1) || result["dry_run"] != true {
		t.Fatalf("stop JSON = %q, %v", stdout.String(), err)
	}
	if _, exists := result["Stoppable"]; exists {
		t.Fatalf("stop JSON retained exported Go field casing: %q", stdout.String())
	}
}

func TestStopRejectsInvalidSelection(t *testing.T) {
	path, session := createSkippedStopSession(t)
	for _, args := range [][]string{{"stop"}, {"stop", shortRegistryID(session.ID), "--all"}} {
		if err := runTestCLI(context.Background(), append([]string{"--store", path}, args...), &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatalf("invalid stop selection accepted: %v", args)
		}
	}
}

func TestStopMultipleSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewFileStore(path)
	now := time.Now().UTC()
	live := registry.PresenceLive
	s1, err := store.Observe(context.Background(), registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: registry.HarnessCodex, Identity: registry.ObservationIdentity{SessionID: "session-1"},
		Presence: &live, NativeEvent: "start", ObservedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := store.Observe(context.Background(), registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: registry.HarnessClaude, Identity: registry.ObservationIdentity{SessionID: "session-2"},
		Presence: &live, NativeEvent: "start", ObservedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "stop", shortRegistryID(s1.ID), shortRegistryID(s2.ID), "--dry-run"}, &stdout, &bytes.Buffer{}); !errors.Is(err, errStopTargetSkipped) {
		t.Fatalf("expected skipped error, got %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "skipped=2") || !strings.Contains(output, "no stop target") {
		t.Fatalf("stop output missing both session results:\n%s", output)
	}
}

func TestStopAllConfirmationHandling(t *testing.T) {
	path, _ := createSkippedStopSession(t)

	// Refused / non-interactive confirmation without --yes fails with error
	var stdout, stderr bytes.Buffer
	err := runTestCLI(context.Background(), []string{"--store", path, "stop", "--all"}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected unconfirmed stop error in non-TTY")
	}

	// Accepted via -y flag
	stdout.Reset()
	if err := runTestCLI(context.Background(), []string{"--store", path, "stop", "--all", "-y"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("expected successful stop with -y, got %v", err)
	}

	// Accepted via --yes flag
	stdout.Reset()

	if err := runTestCLI(context.Background(), []string{"--store", path, "stop", "--all", "--yes"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("expected successful stop with --yes, got %v", err)
	}
}

func createSkippedStopSession(t *testing.T) (string, registry.Session) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewFileStore(path)
	present := true
	session, err := store.Observe(context.Background(), registry.Observation{
		Harness: registry.HarnessCodex, Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
		Identity: registry.ObservationIdentity{SessionID: "stop-session"}, ProcessPresent: &present,
		Process: &registry.ProcessIdentity{PID: 1_000_000_000, StartIdentity: "missing:1000000000"}, ObservedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return path, session
}
