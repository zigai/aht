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

func createTestStoreSession(t *testing.T, storePath, sessionID string, presence registry.Presence, activity registry.Activity) registry.Session {
	t.Helper()
	store := registry.NewFileStore(storePath)
	var act *registry.Activity
	if activity != "" {
		act = &activity
	}
	pres := presence
	observed, err := store.Observe(t.Context(), registry.Observation{
		Source:     registry.ObservationSourceNative,
		Evidence:   registry.ObservationEvidenceNativeEvent,
		Harness:    registry.HarnessCodex,
		Identity:   registry.ObservationIdentity{SessionID: sessionID},
		Presence:   &pres,
		Activity:   act,
		ObservedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("failed to observe test session: %v", err)
	}
	return observed
}

func TestWaitCLIHelpAndMetavariables(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := executeCLI(context.Background(), []string{"wait", "--help"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("wait --help exit code = %d, want 0; stderr: %s", code, stderr.String())
	}

	help := stdout.String()
	if !strings.Contains(help, "Usage:\n  aht wait <session> [flags]") {
		t.Errorf("help missing usage: %s", help)
	}

	for _, required := range []string{
		"--activity <val>",
		"--presence <val>",
		"--timeout <duration>",
		"--stable-for <duration>",
	} {
		if !strings.Contains(help, required) {
			t.Errorf("wait --help missing placeholder %q:\n%s", required, help)
		}
	}

	for _, forbidden := range []string{
		"--activity string",
		"--presence string",
		"--timeout duration",
		"--stable-for duration",
		"(default 0s)",
	} {
		if strings.Contains(help, forbidden) {
			t.Errorf("wait --help contains forbidden text %q:\n%s", forbidden, help)
		}
	}
}

func TestWaitCLIMissingArgs(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := executeCLI(context.Background(), []string{"wait"}, strings.NewReader(""), &stdout, &stderr)
	if code != exitCodeUsage {
		t.Fatalf("wait without args exit code = %d, want %d", code, exitCodeUsage)
	}
	if stderr.Len() == 0 {
		t.Error("wait without args omitted stderr diagnostic")
	}
}

func TestWaitCLIValidationErrorsExitUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{
			name: "no condition flags",
			args: []string{"wait", "sess-1"},
		},
		{
			name: "contradictory presence gone with activity",
			args: []string{"wait", "sess-1", "--presence", "gone", "--activity", "idle"},
		},
		{
			name: "negative timeout",
			args: []string{"wait", "sess-1", "--activity", "idle", "--timeout", "-1s"},
		},
		{
			name: "negative stable-for",
			args: []string{"wait", "sess-1", "--activity", "idle", "--stable-for", "-1s"},
		},
		{
			name: "stable-for exceeds timeout",
			args: []string{"wait", "sess-1", "--activity", "idle", "--timeout", "1s", "--stable-for", "2s"},
		},
		{
			name: "invalid presence",
			args: []string{"wait", "sess-1", "--presence", "bogus"},
		},
		{
			name: "invalid activity",
			args: []string{"wait", "sess-1", "--activity", "bogus"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := executeCLI(context.Background(), tt.args, strings.NewReader(""), &stdout, &stderr)
			if code != exitCodeUsage {
				t.Fatalf("%v exit code = %d, want %d; stderr: %s", tt.args, code, exitCodeUsage, stderr.String())
			}
			if stderr.Len() == 0 {
				t.Fatalf("%v omitted stderr output", tt.args)
			}
		})
	}
}

func TestWaitCLISessionNotFound(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "sessions.json")
	var stdout, stderr bytes.Buffer
	code := executeCLI(context.Background(), []string{"--store", storePath, "wait", "nonexistent", "--activity", "idle"}, strings.NewReader(""), &stdout, &stderr)
	if code != exitCodeGeneral {
		t.Fatalf("exit code = %d, want %d", code, exitCodeGeneral)
	}
	if !strings.Contains(stderr.String(), "session not found") {
		t.Fatalf("stderr = %q, want session not found", stderr.String())
	}
}

func TestWaitCLISuccessHuman(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "sessions.json")
	session := createTestStoreSession(t, storePath, "wait-human", registry.PresenceLive, registry.ActivityIdle)

	var stdout, stderr bytes.Buffer
	code := executeCLI(context.Background(), []string{"--store", storePath, "wait", session.ID, "--activity", "idle"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, session.ID) || !strings.Contains(out, "idle") {
		t.Fatalf("stdout = %q, want session details", out)
	}
}

func TestWaitCLISuccessJSON(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "sessions.json")
	session := createTestStoreSession(t, storePath, "wait-json", registry.PresenceLive, registry.ActivityIdle)

	var stdout, stderr bytes.Buffer
	code := executeCLI(context.Background(), []string{"--store", storePath, "--json", "wait", session.ID, "--activity", "idle"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
	}

	var parsed registry.Session
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("failed to parse JSON output: %v; stdout: %s", err, stdout.String())
	}
	if parsed.ID != session.ID {
		t.Errorf("parsed.ID = %q, want %q", parsed.ID, session.ID)
	}
	if parsed.Activity == nil || *parsed.Activity != registry.ActivityIdle {
		t.Errorf("parsed.Activity = %v, want idle", parsed.Activity)
	}
}

func TestWaitCLISuccessPresenceGone(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "sessions.json")
	session := createTestStoreSession(t, storePath, "wait-gone", registry.PresenceGone, "")

	var stdout, stderr bytes.Buffer
	code := executeCLI(context.Background(), []string{"--store", storePath, "wait", session.ID, "--presence", "gone"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, session.ID) || !strings.Contains(out, "gone") {
		t.Fatalf("stdout = %q, want gone session details", out)
	}
}

func TestWaitCLITimeout(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "sessions.json")
	session := createTestStoreSession(t, storePath, "wait-timeout", registry.PresenceLive, registry.ActivityRunning)

	var stdout, stderr bytes.Buffer
	code := executeCLI(context.Background(), []string{"--store", storePath, "wait", session.ID, "--activity", "idle", "--timeout", "30ms"}, strings.NewReader(""), &stdout, &stderr)
	if code != exitCodeGeneral {
		t.Fatalf("exit code = %d, want %d", code, exitCodeGeneral)
	}
	if !strings.Contains(stderr.String(), "wait condition timed out") {
		t.Fatalf("stderr = %q, want wait condition timed out", stderr.String())
	}
}

func TestWaitCLISessionDisappeared(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "sessions.json")
	session := createTestStoreSession(t, storePath, "wait-disappeared", registry.PresenceGone, "")

	var stdout, stderr bytes.Buffer
	code := executeCLI(context.Background(), []string{"--store", storePath, "wait", session.ID, "--activity", "idle"}, strings.NewReader(""), &stdout, &stderr)
	if code != exitCodeGeneral {
		t.Fatalf("exit code = %d, want %d", code, exitCodeGeneral)
	}
	if !strings.Contains(stderr.String(), "session disappeared before wait condition was met") {
		t.Fatalf("stderr = %q, want session disappeared error", stderr.String())
	}
}

func TestWaitCLICancellation(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "sessions.json")
	session := createTestStoreSession(t, storePath, "wait-cancel", registry.PresenceLive, registry.ActivityRunning)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	var stdout, stderr bytes.Buffer
	code := executeCLI(ctx, []string{"--store", storePath, "wait", session.ID, "--activity", "idle"}, strings.NewReader(""), &stdout, &stderr)
	if code != exitCodeInterrupted {
		t.Fatalf("exit code = %d, want %d (exitCodeInterrupted); stderr: %s", code, exitCodeInterrupted, stderr.String())
	}
}

func TestWaitCLIReferenceResolution(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "sessions.json")
	session := createTestStoreSession(t, storePath, "unique-ref-prefix", registry.PresenceLive, registry.ActivityIdle)

	var stdout, stderr bytes.Buffer
	// Pass prefix of session ID (e.g. first 6 characters)
	prefix := session.ID[:6]
	code := executeCLI(context.Background(), []string{"--store", storePath, "wait", prefix, "--activity", "idle"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, session.ID) {
		t.Fatalf("stdout = %q, want session ID %s", out, session.ID)
	}
}
