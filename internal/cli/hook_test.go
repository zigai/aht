package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	catalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestManagedHookRequiresExplicitJSON(t *testing.T) {
	t.Parallel()
	app := &application{storePath: filepath.Join(t.TempDir(), "sessions.json"), stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	err := app.runManagedHook(context.Background(), strings.NewReader(`{"conversationId":"session-1"}`), "agy", managedHookOptions{event: "PreInvocation"})
	if !errors.Is(err, errManagedHookJSONRequired) {
		t.Fatalf("error = %v, want %v", err, errManagedHookJSONRequired)
	}
}

func TestManagedHookEmitsProtocolJSONWhenRequested(t *testing.T) {
	t.Parallel()
	storePath := filepath.Join(t.TempDir(), "sessions.json")
	var stdout bytes.Buffer
	app := &application{storePath: storePath, outputJSON: true, stdout: &stdout, stderr: &bytes.Buffer{}}
	payload := `{"conversationId":"session-1","workspacePaths":["/repo"],"transcriptPath":"/tmp/transcript.jsonl","invocationNum":0,"initialNumSteps":0}`
	if err := app.runManagedHook(context.Background(), strings.NewReader(payload), "agy", managedHookOptions{event: "PreInvocation"}); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("expected hook protocol JSON: %v; output=%q", err, stdout.String())
	}
	if len(response) != 0 {
		t.Fatalf("expected empty response map for PreInvocation, got %#v", response)
	}

	store := registry.NewJournal(storePath, catalog.Rules{})
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d, err=%v", len(sessions), err)
	}
	if sessions[0].Harness != registry.Harness("agy") || sessions[0].SessionID != "session-1" {
		t.Fatalf("unexpected session: %#v", sessions[0])
	}
	if sessions[0].Observations.Native == nil || sessions[0].Observations.Native.Event != "PreInvocation" {
		t.Fatalf("unexpected native observation: %#v", sessions[0].Observations.Native)
	}
	if sessions[0].Activity() == nil || *sessions[0].Activity() != registry.ActivityRunning {
		t.Fatalf("unexpected activity for PreInvocation: %v", sessions[0].Activity())
	}
}

func TestManagedHookGeneratedCommandExecutes(t *testing.T) {
	t.Parallel()
	storePath := filepath.Join(t.TempDir(), "sessions.json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	payload := `{"conversationId":"session-tool","workspacePaths":["/repo"],"transcriptPath":"/tmp/transcript.jsonl","toolCall":{"name":"read_file"}}`
	code := executeCLI(
		context.Background(),
		[]string{"--store", storePath, "--json", "hook", "agy", "--event", "PreToolUse"},
		strings.NewReader(payload),
		&stdout,
		&stderr,
	)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr=%s", code, stderr.String())
	}
	var response map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("expected protocol JSON, got %q: %v", stdout.String(), err)
	}
	if response["decision"] != "allow" {
		t.Fatalf("expected decision=allow, got %#v", response)
	}
	if stderr.Len() > 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}

	assertStoredHookSession(t, storePath, "session-tool", "PreToolUse")
}

func assertStoredHookSession(t *testing.T, storePath, expectedID, expectedEvent string) {
	t.Helper()
	store := registry.NewJournal(storePath, catalog.Rules{})
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d, err=%v", len(sessions), err)
	}
	if sessions[0].Harness != registry.Harness("agy") || sessions[0].SessionID != expectedID {
		t.Fatalf("unexpected session: %#v", sessions[0])
	}
	if sessions[0].Observations.Native == nil || sessions[0].Observations.Native.Event != expectedEvent {
		t.Fatalf("unexpected native event: %#v", sessions[0].Observations.Native)
	}
	if sessions[0].Activity() == nil || *sessions[0].Activity() != registry.ActivityRunning {
		t.Fatalf("unexpected activity for %s: %v", expectedEvent, sessions[0].Activity())
	}
}

func TestManagedHookRejectsOversizedPayload(t *testing.T) {
	t.Parallel()

	app := &application{storePath: filepath.Join(t.TempDir(), "sessions.json"), outputJSON: true, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	err := app.runManagedHook(
		context.Background(),
		strings.NewReader(strings.Repeat("x", maxPayloadInputBytes+1)),
		"agy",
		managedHookOptions{event: "PreInvocation"},
	)
	if !errors.Is(err, errPayloadInputTooLarge) {
		t.Fatalf("error = %v, want %v", err, errPayloadInputTooLarge)
	}
}
