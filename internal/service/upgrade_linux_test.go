//go:build linux

package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type upgradeExecutor struct {
	calls      [][]string
	stopped    bool
	failReload bool
}

func (e *upgradeExecutor) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	e.calls = append(e.calls, append([]string{name}, args...))
	if args[1] == "is-active" && e.stopped {
		err := exec.CommandContext(ctx, "/bin/sh", "-c", "exit 3").Run()
		return nil, fmt.Errorf("inactive service: %w", err)
	}
	if args[1] == "daemon-reload" && e.failReload {
		e.failReload = false
		return nil, errManagerTestFailure
	}
	return nil, nil
}

func TestUpgradePreservesTrackerSettingsAndState(t *testing.T) {
	for _, test := range []struct {
		name                   string
		stopped, stale, dryRun bool
	}{
		{name: "running current"},
		{name: "running stale", stale: true},
		{name: "stopped current", stopped: true},
		{name: "stopped stale", stopped: true, stale: true},
		{name: "preview running current", dryRun: true},
		{name: "preview running stale", dryRun: true, stale: true},
		{name: "preview stopped current", dryRun: true, stopped: true},
		{name: "preview stopped stale", dryRun: true, stopped: true, stale: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			verifyTrackerUpgrade(t, test.stopped, test.stale, test.dryRun)
		})
	}
}

func verifyTrackerUpgrade(t *testing.T, stopped, stale, dryRun bool) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	options := Options{Binary: "/tmp/new aht", StorePath: filepath.Join(dir, "state with % and \"quotes\".json"), Interval: 7 * time.Second, GracePeriod: 19 * time.Second}
	want, err := RenderSystemdUnit(options)
	if err != nil {
		t.Fatal(err)
	}
	previous := want
	if stale {
		previous = strings.ReplaceAll(strings.ReplaceAll(want, "version: 7", "version: 6"), "/tmp/new aht", "/tmp/old aht")
	}
	path := filepath.Join(dir, "systemd", "user", linuxUnitName)
	if err := writeAtomic(path, []byte(previous)); err != nil {
		t.Fatal(err)
	}
	executor := &upgradeExecutor{stopped: stopped}
	result, err := New(executor).Upgrade(t.Context(), options.Binary, dryRun)
	if err != nil {
		t.Fatal(err)
	}
	if result.Running == stopped || !result.Installed || result.Changed != (!dryRun && (stale || !stopped)) {
		t.Fatalf("result = %+v", result)
	}
	wantMessage := expectedTrackerUpgradeMessage(stopped, stale, dryRun)
	if result.Message != wantMessage {
		t.Fatalf("result.Message = %q, want %q", result.Message, wantMessage)
	}
	expected := want
	if dryRun {
		expected = previous
	}
	assertServiceContent(t, path, expected)
	assertUpgradeManagerCalls(t, executor.calls, stopped, stale, dryRun)
}

func expectedTrackerUpgradeMessage(stopped, stale, dryRun bool) string {
	if dryRun {
		if stopped {
			return "would update (stopped)"
		}
		return "would update and restart"
	}
	if stopped {
		if stale {
			return "stopped (updated)"
		}
		return "stopped (up to date)"
	}
	return "updated and restarted"
}

func assertUpgradeManagerCalls(t *testing.T, got [][]string, stopped, stale, dryRun bool) {
	t.Helper()
	calls := [][]string{{"systemctl", "--user", "is-active", "--quiet", linuxUnitName}}
	if !dryRun && stale {
		calls = append(calls, []string{"systemctl", "--user", "daemon-reload"})
	}
	if !dryRun && !stopped {
		calls = append(calls, []string{"systemctl", "--user", "restart", linuxUnitName})
	}
	if !reflect.DeepEqual(got, calls) {
		t.Fatalf("calls = %v, want %v", got, calls)
	}
}

func assertServiceContent(t *testing.T, path, expected string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if expected == "" {
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing service created: %v", err)
		}
		return
	}
	if err != nil || string(got) != expected {
		t.Fatalf("service content = %q, %v; want %q", got, err, expected)
	}
}

func TestUpgradeLeavesMissingForeignAndInvalidServicesAlone(t *testing.T) {
	for _, content := range []string{"", "user service", "# aht managed observer service\n# version: 7\nExecStart=/bin/aht --unknown setting\n"} {
		t.Run(content, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			path, err := systemdUnitPath()
			if err != nil {
				t.Fatal(err)
			}
			if content != "" {
				if err := writeAtomic(path, []byte(content)); err != nil {
					t.Fatal(err)
				}
			}
			executor := &upgradeExecutor{}
			result, err := New(executor).Upgrade(t.Context(), "/bin/aht", false)
			if (err != nil) != (content != "") || result.Changed || len(executor.calls) != 0 {
				t.Fatalf("result = %+v, err = %v, calls = %v", result, err, executor.calls)
			}
			assertServiceContent(t, path, content)
		})
	}
}

func TestUpgradeRollbackDoesNotStartStoppedTracker(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	previous, err := RenderSystemdUnit(Options{Binary: "/bin/old-aht", StorePath: "/tmp/state", Interval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	path, err := systemdUnitPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(path, []byte(previous)); err != nil {
		t.Fatal(err)
	}
	executor := &upgradeExecutor{stopped: true, failReload: true}
	_, err = New(executor).Upgrade(t.Context(), "/bin/new-aht", false)
	if !errors.Is(err, errManagerTestFailure) {
		t.Fatalf("error = %v", err)
	}
	assertServiceContent(t, path, previous)
	for _, call := range executor.calls {
		if call[2] != "is-active" && call[2] != "daemon-reload" {
			t.Fatalf("rollback started stopped tracker: %v", executor.calls)
		}
	}
}
