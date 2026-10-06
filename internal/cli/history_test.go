package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryRefreshIndexesChangedHistoriesOnce(t *testing.T) {
	home := searchCLIHome(t)
	sessions := filepath.Join(home, ".pi", "agent", "sessions", "project")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSearchFixtureBody(t, filepath.Join(sessions, "alpha.jsonl"), piSession("alpha", "/work/a", "2026-09-01T12:00:00Z"))
	args := []string{"--no-config", "--store", filepath.Join(t.TempDir(), "state.json"), "manage", "history", "refresh", "--harness", "pi"}
	for _, want := range []string{"Checked 1 histories; indexed 1 changed.", "Checked 1 histories; indexed 0 changed."} {
		var stdout, stderr bytes.Buffer
		if code := executeCLI(t.Context(), args, strings.NewReader(""), &stdout, &stderr); code != 0 {
			t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
		}
		if strings.TrimSpace(stdout.String()) != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	}
}

func TestHistoryRefreshRejectsUnknownHarness(t *testing.T) {
	searchCLIHome(t)
	var stdout, stderr bytes.Buffer
	args := []string{"--no-config", "manage", "history", "refresh", "--harness", "nope"}
	if code := executeCLI(t.Context(), args, strings.NewReader(""), &stdout, &stderr); code != exitCodeUsage || stdout.Len() != 0 {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}
