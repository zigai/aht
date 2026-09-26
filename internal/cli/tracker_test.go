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

	catalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/observer"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/internal/service"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestTrackerLifecycleCommandsUseHumanOutputUnlessJSONRequested(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv(registry.StateDirEnv, filepath.Join(home, "state"))
	storePath := filepath.Join(home, "sessions.json")

	for _, args := range [][]string{{"manage", "tracker", "enable", "--dry-run"}, {"manage", "tracker", "status"}, {"manage", "tracker", "disable", "--dry-run"}} {
		var stdout bytes.Buffer
		if err := runTestCLI(context.Background(), append([]string{"--store", storePath}, args...), &stdout, &bytes.Buffer{}); err != nil {
			t.Fatalf("%v failed: %v", args, err)
		}
		if strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") || !strings.Contains(stdout.String(), "Manager:") {
			t.Fatalf("%v default output = %q", args, stdout.String())
		}
	}

	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", storePath, "--json", "manage", "tracker", "enable", "--dry-run"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var result service.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Manager == "" {
		t.Fatalf("tracker JSON = %q, %v", stdout.String(), err)
	}
}

func TestTrackerRunOnceSupportsHumanAndJSONOutput(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "sessions.json")
	var human bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", storePath, "manage", "tracker", "run", "--once"}, &human, &bytes.Buffer{}); err != nil && !errors.Is(err, errObserverRunDegraded) {
		t.Fatal(err)
	}
	if strings.HasPrefix(strings.TrimSpace(human.String()), "{") || !strings.Contains(human.String(), "processes=") {
		t.Fatalf("tracker run human output = %q", human.String())
	}

	var machine bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", storePath, "--json", "manage", "tracker", "run", "--once"}, &machine, &bytes.Buffer{}); err != nil && !errors.Is(err, errObserverRunDegraded) {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(machine.Bytes(), &result); err != nil {
		t.Fatalf("tracker run JSON = %q, %v", machine.String(), err)
	}
}

var errTestPaneList = errors.New("pane inventory unavailable")

func TestQuietLongRunningObserverStreamsRequestedJSONLines(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := &application{outputJSON: true, stdout: &stdout, stderr: &stderr}
	watcher := observer.New(observer.Options{
		Store: registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{}),
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			cancel()
			return nil, nil
		},
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]observer.CatalogEntry, error) { return nil, nil },
	})
	if err := app.runObserver(ctx, observeOptions{interval: time.Second, quiet: true}, watcher); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("JSON line count = %d, want 1; output=%q", len(lines), stdout.String())
	}
	var result observer.Result
	if err := json.Unmarshal([]byte(lines[0]), &result); err != nil {
		t.Fatalf("expected compact JSON line: %v; output=%q", err, stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("quiet observer wrote diagnostics: %q", stderr.String())
	}
}

func TestRunObserverOnceReturnsDegradedErrorAfterWritingResult(t *testing.T) {
	t.Parallel()
	for _, outputJSON := range []bool{false, true} {
		var stdout bytes.Buffer
		app := &application{outputJSON: outputJSON, stdout: &stdout, stderr: &bytes.Buffer{}}
		watcher := observer.New(observer.Options{
			Store:       registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{}),
			ProcessList: func(context.Context) ([]processinfo.Process, error) { return nil, nil },
			PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, errTestPaneList },
			CatalogList: func(context.Context) ([]observer.CatalogEntry, error) { return nil, nil },
		})
		err := app.runObserver(context.Background(), observeOptions{once: true}, watcher)
		if !errors.Is(err, errObserverRunDegraded) {
			t.Fatalf("outputJSON=%t error = %v", outputJSON, err)
		}
		if outputJSON {
			var result observer.Result
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || !result.Degraded || result.Error != errTestPaneList.Error() {
				t.Fatalf("JSON degraded result = %q, %#v, %v", stdout.String(), result, err)
			}
			continue
		}
		if !strings.Contains(stdout.String(), "degraded=true") || !strings.Contains(stdout.String(), errTestPaneList.Error()) {
			t.Fatalf("human degraded result = %q", stdout.String())
		}
	}
}
