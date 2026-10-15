package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/history"
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

func TestHistoryRefreshSingletonSkipsContenderAndReleasesGuard(t *testing.T) {
	path := searchCLIFixture(t)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	guard, err := lockHistoryRefresh()
	if err != nil || guard == nil {
		t.Fatalf("first refresh guard=%v err=%v", guard, err)
	}
	t.Cleanup(func() { _ = guard.Close() })
	args := []string{"--no-config", "manage", "history", "refresh", "--source", "pi=" + path}
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- executeCLI(t.Context(), args, strings.NewReader(""), &stdout, &stderr) }()
	select {
	case code := <-done:
		if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("contender exit=%d stdout=%q stderr=%q", code, &stdout, &stderr)
		}
	case <-time.After(5 * time.Second):
		_ = guard.Close()
		<-done
		t.Fatal("contending refresh waited for the active worker")
	}
	index := filepath.Join(cache, "aht", "history-v1.sqlite")
	if _, err = os.Stat(index); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("contending refresh touched the index: %v", err)
	}
	if err = guard.Close(); err != nil {
		t.Fatal(err)
	}
	if code := executeCLI(t.Context(), args, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("refresh after release exit=%d stderr=%q", code, &stderr)
	}
	if strings.TrimSpace(stdout.String()) != "Checked 1 histories; indexed 1 changed." {
		t.Fatalf("refresh after release stdout=%q", &stdout)
	}
}

func TestHistoryRefreshRecordsSourceFailureForDoctor(t *testing.T) {
	searchCLIHome(t)
	missing := filepath.Join(t.TempDir(), "missing")
	var stdout, stderr bytes.Buffer
	args := []string{"--no-config", "--json", "manage", "history", "refresh", "--source", "pi=" + missing}
	if code := executeCLI(t.Context(), args, strings.NewReader(""), &stdout, &stderr); code != 1 {
		t.Fatalf("refresh exit=%d stdout=%q stderr=%q", code, &stdout, &stderr)
	}
	var catalog history.Catalog
	status, err := catalog.RefreshStatus(t.Context())
	if err != nil || !status.Incomplete || status.IssueCount != 1 {
		t.Fatalf("failed refresh status=%+v err=%v", status, err)
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
