package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	catalog "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/pkg/registry"
)

//nolint:cyclop // one sequential scenario proves both safety and explicit cleanup modes
func TestStateCleanRequiresExplicitPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(path, catalog.Rules{})
	presence := registry.PresenceGone
	if _, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("codex"), At: time.Now().Add(-time.Hour), Subject: registry.ObservationIdentity{SessionID: "gone"}, Evidence: &registry.Report{Claim: &presence}}); err != nil {
		t.Fatal(err)
	}

	if err := runTestCLI(context.Background(), []string{"--store", path, "manage", "state", "clean", "--all", "--older-than", "1h"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("unsafe clean error = %v", err)
	}
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("unsafe clean changed registry: %v, %#v", err, sessions)
	}
	if err := runTestCLI(context.Background(), []string{"--store", path, "gc"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("legacy gc without an explicit policy unexpectedly succeeded")
	}

	var machine bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "--json", "manage", "state", "clean", "--older-than", "0s"}, &machine, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var cleanResult registry.GCResult
	if err := json.Unmarshal(machine.Bytes(), &cleanResult); err != nil || cleanResult.Deleted != 1 {
		t.Fatalf("state clean JSON = %q, %v", machine.String(), err)
	}
	if _, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("codex"), At: time.Now().Add(-time.Hour), Subject: registry.ObservationIdentity{SessionID: "gone-again"}, Evidence: &registry.Report{Claim: &presence}}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "manage", "state", "clean", "--all", "--yes"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "deleted=1") {
		t.Fatalf("clean output = %q", stdout.String())
	}
}

func TestStatePathAndResetCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(path, catalog.Rules{})
	observeTestSession(t, store, "reset-session", time.Now())

	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "manage", "state", "path"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != path {
		t.Fatalf("state path output = %q", stdout.String())
	}

	stdout.Reset()
	if err := runTestCLI(context.Background(), []string{"--store", path, "manage", "state", "reset"}, &stdout, &bytes.Buffer{}); !errors.Is(err, errStateResetForce) {
		t.Fatalf("state reset without force error = %v", err)
	}
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("state reset without force changed state: %v, %#v", err, sessions)
	}

	if err := runTestCLI(context.Background(), []string{"--store", path, "manage", "state", "reset", "--force"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Cleared:    1") {
		t.Fatalf("state reset output = %q", stdout.String())
	}
}

func TestStateResetCommandRecoversMalformedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":2,"sessions":`), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "manage", "state", "reset", "--force"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Cleared:    0") {
		t.Fatalf("state reset output = %q", stdout.String())
	}
	if _, err := registry.NewJournal(path, catalog.Rules{}).List(context.Background(), registry.Filter{}); err != nil {
		t.Fatalf("registry remains unreadable after reset: %v", err)
	}
}
