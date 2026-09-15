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

func TestRuntimeFailureDoesNotPrintUsage(t *testing.T) {
	var stdout bytes.Buffer
	err := runTestCLI(context.Background(), []string{"--store", filepath.Join(t.TempDir(), "sessions.json"), "info", "missing"}, &stdout, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected missing session error")
	}
	if strings.Contains(stdout.String(), "USAGE:") || strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("runtime failure printed usage: %q", stdout.String())
	}

	stdout.Reset()
	err = runTestCLI(context.Background(), []string{"info"}, &stdout, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected invocation error")
	}
	if stdout.Len() != 0 {
		t.Fatalf("invocation error wrote stdout: %q", stdout.String())
	}
}

func TestInfoValidatesReferenceAndExplanationFlags(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		args []string
		want error
	}{
		{args: []string{"info"}, want: errInfoReference},
		{args: []string{"info", "session", "--pane", "%1"}, want: errInfoReference},
		{args: []string{"info", "session", "--config-dir", t.TempDir()}, want: errInfoConfig},
	} {
		if err := runTestCLI(context.Background(), test.args, &bytes.Buffer{}, &bytes.Buffer{}); !errors.Is(err, test.want) {
			t.Errorf("%v error = %v, want %v", test.args, err, test.want)
		}
	}
}

func TestInfoResolvesShortIDAndRequiresJSONExplicitly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewFileStore(path)
	session := observeTestSession(t, store, "info-session", time.Now())
	reference := shortRegistryID(session.ID)

	var human bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "info", reference}, &human, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human.String(), "Session ID:") || !strings.Contains(human.String(), "info-session") || strings.HasPrefix(strings.TrimSpace(human.String()), "{") {
		t.Fatalf("info default output is not human-readable: %q", human.String())
	}

	var machine bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "--json", "info", reference}, &machine, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var decoded registry.Session
	if err := json.Unmarshal(machine.Bytes(), &decoded); err != nil || decoded.ID != session.ID {
		t.Fatalf("info JSON = %q, %v", machine.String(), err)
	}
}
