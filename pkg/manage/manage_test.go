package manage_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zigai/aht/pkg/manage"
	"github.com/zigai/aht/pkg/registry"
)

func TestSupportedHarnesses(t *testing.T) {
	t.Parallel()

	harnesses := manage.SupportedHarnesses()
	for _, expected := range []registry.Harness{registry.Harness("claude"), registry.Harness("codex"), registry.Harness("opencode")} {
		if !slices.Contains(harnesses, expected) {
			t.Errorf("SupportedHarnesses() missing %s", expected)
		}
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

func assertMissingStatus(t *testing.T, mgr *manage.Manager, hookPath, shimPath string) {
	t.Helper()
	status, err := mgr.IntegrationStatus(context.Background(), registry.Harness("codex"))
	if err != nil {
		t.Fatalf("IntegrationStatus(Codex) missing error = %v", err)
	}
	if status.Harness != registry.Harness("codex") {
		t.Errorf("status.Harness = %s, want %s", status.Harness, registry.Harness("codex"))
	}
	if status.Status != manage.ArtifactMissing {
		t.Errorf("status.Status = %s, want %s", status.Status, manage.ArtifactMissing)
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
	status, err := mgr.IntegrationStatus(context.Background(), registry.Harness("codex"))
	if err != nil {
		t.Fatalf("IntegrationStatus(Codex) foreign error = %v", err)
	}
	if status.Status != manage.ArtifactForeign {
		t.Errorf("status.Status = %s, want %s", status.Status, manage.ArtifactForeign)
	}
}

func assertCurrentStatus(t *testing.T, mgr *manage.Manager, shimPath string) {
	t.Helper()
	if err := os.Remove(shimPath); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.InstallIntegration(context.Background(), registry.Harness("codex"), manage.IntegrationOptions{
		TargetBinary: "",
		DryRun:       false,
		Force:        true,
		UseShim:      false,
	}); err != nil {
		t.Fatalf("InstallIntegration(Codex) error = %v", err)
	}
	status, err := mgr.IntegrationStatus(context.Background(), registry.Harness("codex"))
	if err != nil {
		t.Fatalf("IntegrationStatus(Codex) current error = %v", err)
	}
	if status.Status != manage.ArtifactCurrent {
		t.Errorf("status.Status = %s, want %s", status.Status, manage.ArtifactCurrent)
	}
}

func TestManagerIntegrationStatus(t *testing.T) {
	homeDir := t.TempDir()
	codexDir := t.TempDir()
	stateDir := t.TempDir()
	binDir := t.TempDir()
	binary := filepath.Join(binDir, "aht")
	writeExecutableFixture(t, binary, []byte("#!/bin/sh\nexit 0\n"))

	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(homeDir, ".config"))
	t.Setenv("CODEX_HOME", codexDir)
	t.Setenv(registry.StateDirEnv, stateDir)

	mgr := manage.New(manage.Config{
		Binary:    binary,
		StorePath: filepath.Join(t.TempDir(), "sessions.json"),
	})

	hookPath := filepath.Join(codexDir, "hooks.json")
	shimPath := filepath.Join(stateDir, "shims", "codex")

	assertMissingStatus(t, mgr, hookPath, shimPath)
	assertForeignStatus(t, mgr, shimPath)
	assertCurrentStatus(t, mgr, shimPath)
}
