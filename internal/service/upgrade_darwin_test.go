//go:build darwin

package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type launchUpgradeExecutor struct {
	state string
	calls []string
}

func (e *launchUpgradeExecutor) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	e.calls = append(e.calls, args[0])
	if args[0] == "print" {
		return []byte("job = {\n state = " + e.state + "\n}\n"), nil
	}
	return nil, nil
}

func TestLaunchAgentUpgradePreservesSettingsAndRunningState(t *testing.T) {
	for _, state := range []string{"running", "not running"} {
		t.Run(state, func(t *testing.T) {
			verifyLaunchAgentUpgrade(t, state)
		})
	}
}

func verifyLaunchAgentUpgrade(t *testing.T, state string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	options := Options{Binary: "/tmp/new aht", StorePath: "/tmp/state & <data>.json", Interval: 7 * time.Second, GracePeriod: 19 * time.Second}
	want, err := RenderLaunchAgent(options)
	if err != nil {
		t.Fatal(err)
	}
	previous := strings.ReplaceAll(want, "/tmp/new aht", "/tmp/old aht")
	path, err := launchAgentPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(path, []byte(previous)); err != nil {
		t.Fatal(err)
	}
	executor := &launchUpgradeExecutor{state: state}
	result, err := New(executor).Upgrade(t.Context(), options.Binary, false)
	if err != nil || !result.Current || !result.Changed || result.Running != (state == "running") {
		t.Fatalf("upgrade = %+v, %v", result, err)
	}
	got, err := os.ReadFile(filepath.Clean(path))
	if err != nil || string(got) != want {
		t.Fatalf("settings = %q, %v; want %q", got, err, want)
	}
	calls := []string{"print", "bootout"}
	if state == "running" {
		calls = append(calls, "bootstrap")
	}
	if !reflect.DeepEqual(executor.calls, calls) {
		t.Fatalf("calls = %v, want %v", executor.calls, calls)
	}
}

func TestLaunchAgentArgumentParsing(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/tmp/plain", "/tmp/space & <xml> \"quotes\""} {
		t.Run(path, func(t *testing.T) {
			options := Options{Binary: "/bin/aht", StorePath: path, Interval: time.Second}
			content, err := RenderLaunchAgent(options)
			if err != nil {
				t.Fatal(err)
			}
			args, err := installedArguments([]byte(content))
			if err != nil {
				t.Fatal(err)
			}
			recovered, err := recoverOptions(args, options.Binary)
			if err != nil || recovered.StorePath != path {
				t.Fatal(fmt.Sprintf("options = %+v, %v", recovered, err))
			}
		})
	}
}
