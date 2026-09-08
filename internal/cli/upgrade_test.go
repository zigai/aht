//go:build linux

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/internal/install"
	"github.com/zigai/aht/internal/service"
	"github.com/zigai/aht/pkg/registry"
)

func TestUpgradeReportsPartialFailureAndStillRestartsTracker(t *testing.T) {
	calls := prepareUpgradeFailure(t)
	var stdout bytes.Buffer
	root := NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"manage", "upgrade", "--binary", "/bin/new-aht", "--json"})
	if err := root.ExecuteContext(t.Context()); err == nil {
		t.Fatal("partial failure returned success")
	}
	var result upgradeResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Integrations) != 1 || result.Integrations[0].Error == "" || !result.Tracker.Running || !result.Tracker.Changed || result.TrackerError != "" {
		t.Fatalf("result = %+v", result)
	}
	data, err := os.ReadFile(calls)
	if err != nil || !strings.Contains(string(data), "restart aht-observer.service") {
		t.Fatalf("manager calls = %s, %v", data, err)
	}
}

func prepareUpgradeFailure(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, key := range []string{"XDG_CONFIG_HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "COPILOT_HOME", "CLINE_DIR", "CLINE_HOOKS_DIR", "KIMI_SHARE_DIR", "GROK_HOME", "PI_CODING_AGENT_DIR", "AGY_CONFIG_HOME", "HERMES_HOME", "OPENCODE_CONFIG_DIR", "KILO_CONFIG_DIR", registry.StateDirEnv} {
		t.Setenv(key, filepath.Join(home, key))
	}
	installed, err := install.RunContext(t.Context(), install.Options{Harness: registry.HarnessCodex, Binary: "/bin/old-aht"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed.Path, []byte(`{"aht managed integration":`), 0o600); err != nil {
		t.Fatal(err)
	}
	unit, err := service.RenderSystemdUnit(service.Options{Binary: "/bin/old-aht", StorePath: filepath.Join(home, "custom-state"), Interval: 7 * time.Second, GracePeriod: 19 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	unitPath := filepath.Join(home, "XDG_CONFIG_HOME", "systemd", "user", "aht-observer.service")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unitPath, []byte(unit), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	t.Setenv("AHT_TEST_MANAGER_CALLS", calls)
	manager := filepath.Join(bin, "systemctl")
	if err := os.WriteFile(manager, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$AHT_TEST_MANAGER_CALLS\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(manager, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return calls
}
