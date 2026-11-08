package manage_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/install"

	"github.com/zigai/aht/v2/pkg/manage"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestDisableTrackerIgnoresConfig(t *testing.T) {
	home := isolateDoctorEnvironment(t)
	manager := manage.New(manage.Config{
		Binary: "missing-binary", StorePath: filepath.Join(home, "state.json"),
		ConfigPath: filepath.Join(home, "missing.toml"),
	})

	result, err := manager.DisableTracker(t.Context(), manage.TrackerOptions{DryRun: true})
	if err != nil || result.Message != "not installed" {
		t.Fatalf("disable with missing config = %+v, %v", result, err)
	}
}

func TestInstallIntegrationPreservesPartialResult(t *testing.T) {
	home := isolateDoctorEnvironment(t)
	t.Setenv("PATH", filepath.Join(home, "missing-bin"))

	manager := manage.New(manage.Config{Binary: "/bin/fixture-aht"})

	result, err := manager.InstallIntegration(
		t.Context(),
		registry.Harness("codex"),
		manage.IntegrationOptions{},
	)
	if err == nil {
		t.Fatal("installation succeeded without the native trust CLI")
	}

	wantPath := filepath.Join(os.Getenv("CODEX_HOME"), "hooks.json")
	if _, statErr := os.Stat(wantPath); statErr != nil {
		t.Fatalf("hook file was not installed before trust failed: %v", statErr)
	}

	if !result.Changed || result.Path != wantPath || result.Error == "" {
		t.Fatalf("partial installation lost its effects: %+v, %v", result, err)
	}
}

func TestInstallIntegrationPreservesNoSetupOutcome(t *testing.T) {
	isolateDoctorEnvironment(t)

	result, err := manage.New(manage.Config{}).InstallIntegration(
		t.Context(), registry.Harness("prime-agent"), manage.IntegrationOptions{},
	)
	if err != nil || result.Message != "no setup required" || result.Changed {
		t.Fatalf("no-setup installation = %+v, %v", result, err)
	}
}

func writeExecutableFixture(t *testing.T, path string, content []byte) {
	t.Helper()

	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertMissingStatus(t *testing.T, mgr *manage.Manager, hookPath string, shimPath string) {
	t.Helper()

	status, err := mgr.IntegrationStatus(context.Background(), registry.Harness("claude"))
	if err != nil {
		t.Fatalf("IntegrationStatus(Claude) missing error = %v", err)
	}

	if status.Harness != registry.Harness("claude") {
		t.Errorf("status.Harness = %s, want %s", status.Harness, registry.Harness("claude"))
	}

	if status.Status != install.ArtifactMissing {
		t.Errorf("status.Status = %s, want %s", status.Status, install.ArtifactMissing)
	}

	if !slices.Contains(status.Paths, hookPath) {
		t.Errorf("status.Paths %v missing hookPath %s", status.Paths, hookPath)
	}

	if !slices.Contains(status.Paths, shimPath) {
		t.Errorf("status.Paths %v missing shimPath %s", status.Paths, shimPath)
	}
}

func assertForeignStatus(t *testing.T, mgr *manage.Manager, shimPath string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(shimPath), 0o755); err != nil {
		t.Fatal(err)
	}

	writeExecutableFixture(t, shimPath, []byte("#!/bin/sh\n# custom user shim\nexit 0\n"))

	status, err := mgr.IntegrationStatus(context.Background(), registry.Harness("claude"))
	if err != nil {
		t.Fatalf("IntegrationStatus(Claude) foreign error = %v", err)
	}

	if status.Status != install.ArtifactForeign {
		t.Errorf("status.Status = %s, want %s", status.Status, install.ArtifactForeign)
	}
}

func assertCurrentStatus(t *testing.T, mgr *manage.Manager, shimPath string) {
	t.Helper()

	if err := os.Remove(shimPath); err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.InstallIntegration(
		context.Background(),
		registry.Harness("claude"),
		manage.IntegrationOptions{
			TargetBinary: "",
			DryRun:       false,
			Force:        true,
			UseShim:      false,
		},
	); err != nil {
		t.Fatalf("InstallIntegration(Claude) error = %v", err)
	}

	status, err := mgr.IntegrationStatus(context.Background(), registry.Harness("claude"))
	if err != nil {
		t.Fatalf("IntegrationStatus(Claude) current error = %v", err)
	}

	if status.Status != install.ArtifactCurrent {
		t.Errorf("status.Status = %s, want %s", status.Status, install.ArtifactCurrent)
	}
}

func TestManagerInstallDryRunKeepsPreviewOnCurrentIntegration(t *testing.T) {
	home := isolateDoctorEnvironment(t)

	mgr := manage.New(manage.Config{Binary: filepath.Join(home, "bin", "aht")})
	if _, err := mgr.InstallIntegration(
		t.Context(),
		registry.Harness("pi"),
		manage.IntegrationOptions{},
	); err != nil {
		t.Fatal(err)
	}

	result, err := mgr.InstallIntegration(
		t.Context(),
		registry.Harness("pi"),
		manage.IntegrationOptions{DryRun: true},
	)
	if err != nil {
		t.Fatal(err)
	}

	if result.Changed || !strings.HasPrefix(result.Message, "dry run:") {
		t.Fatalf(
			"preview of current integration: changed=%t message=%q",
			result.Changed,
			result.Message,
		)
	}
}

func TestManagerIntegrationStatus(t *testing.T) {
	homeDir := t.TempDir()
	integrationDir := t.TempDir()
	stateDir := t.TempDir()
	binDir := t.TempDir()
	binary := filepath.Join(binDir, "aht")
	writeExecutableFixture(t, binary, []byte("#!/bin/sh\nexit 0\n"))

	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(homeDir, ".config"))
	t.Setenv("CLAUDE_CONFIG_DIR", integrationDir)
	t.Setenv(registry.StateDirEnv, stateDir)

	mgr := manage.New(manage.Config{
		Binary:    binary,
		StorePath: filepath.Join(t.TempDir(), "sessions.json"),
	})

	hookPath := filepath.Join(integrationDir, "settings.json")
	shimPath := filepath.Join(stateDir, "shims", "claude")

	assertMissingStatus(t, mgr, hookPath, shimPath)
	assertForeignStatus(t, mgr, shimPath)
	assertCurrentStatus(t, mgr, shimPath)
}
