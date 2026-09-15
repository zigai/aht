package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

func TestListRejectsModeSpecificFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	tests := [][]string{
		{"--store", path, "list", "--format", "plain"},
		{"--store", path, "list", "--no-snapshot"},
		{"--store", path, "list", "--watch", "--summary"},
		{"--store", path, "list", "--watch", "--summary=false"},
		{"--store", path, "list", "--watch", "--sort", "updated"},
		{"--store", path, "list", "--summary", "--desc"},
		{"--store", path, "list", "--summary", "--absolute-time"},
		{"--store", path, "--json", "list", "--absolute-time"},
		{"--store", path, "list", "--sort", ""},
		{"--store", path, "watch", "--format", ""},
	}
	for _, args := range tests {
		if err := runTestCLI(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Errorf("arguments unexpectedly accepted: %v", args)
		}
	}
}

func TestListDefaultsToLatestUpdateLastWithUsefulLabelsAndShortIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewFileStore(path)
	old := observeTestSession(t, store, "older-session", time.Now().Add(-time.Hour))
	newer := observeTestSession(t, store, "newer-session", time.Now())

	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "list"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if strings.Index(output, "older-session") > strings.Index(output, "newer-session") {
		t.Fatalf("list does not put the latest update last:\n%s", output)
	}
	if !strings.Contains(output, "Session") || strings.Contains(output, old.ID) || strings.Contains(output, newer.ID) {
		t.Fatalf("list did not use a label and abbreviated IDs:\n%s", output)
	}

	stdout.Reset()
	if err := runTestCLI(context.Background(), []string{"--store", path, "--json", "list"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var sessions []registry.Session
	if err := json.Unmarshal(stdout.Bytes(), &sessions); err != nil || len(sessions) != 2 || sessions[0].ID != old.ID || sessions[1].ID != newer.ID {
		t.Fatalf("list JSON = %q, %v", stdout.String(), err)
	}
}

func TestListDisplaysAndFiltersZellijLocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewFileStore(path)
	location := &registry.MultiplexerContext{
		Kind: registry.MultiplexerZellij, SessionName: "work", TabName: "agents", PaneID: "terminal_7",
	}
	if _, err := store.Observe(context.Background(), registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: registry.HarnessCodex, Identity: registry.ObservationIdentity{SessionID: "zellij-session"},
		NativeEvent: "turn_complete", Multiplexer: location, ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "list", "--multiplexer-session", "work"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	for _, expected := range []string{"Location", "zellij", "work"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("list output missing %q:\n%s", expected, output)
		}
	}
}

func TestAbbreviatedRegistryIDsExpandCollidingPrefixes(t *testing.T) {
	t.Parallel()
	sessions := []registry.Session{{ID: "codex-12345678aaaa"}, {ID: "codex-12345678bbbb"}, {ID: "claude-12345678cccc"}}
	ids := abbreviatedRegistryIDs(sessions)
	if ids[sessions[0].ID] != "codex-12345678a" || ids[sessions[1].ID] != "codex-12345678b" {
		t.Fatalf("colliding IDs were not expanded: %#v", ids)
	}
	if ids[sessions[2].ID] != "claude-12345678" {
		t.Fatalf("different agent prefix was unnecessarily expanded: %#v", ids)
	}
}
