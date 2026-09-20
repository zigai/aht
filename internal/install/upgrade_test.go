package install

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

func isolateUpgradeHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, key := range []string{"XDG_CONFIG_HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "COPILOT_HOME", "CLINE_DIR", "CLINE_HOOKS_DIR", "KIMI_SHARE_DIR", "GROK_HOME", "PI_CODING_AGENT_DIR", "AGY_CONFIG_HOME", "HERMES_HOME", "OPENCODE_CONFIG_DIR", "KILO_CONFIG_DIR", registry.StateDirEnv} {
		t.Setenv(key, filepath.Join(home, key))
	}
}

func TestUpgradeOnlyInstalledIntegrationsAndPreservesUserHooks(t *testing.T) {
	isolateUpgradeHome(t)
	results, err := Upgrade(t.Context(), "/bin/new-aht", false)
	if err != nil || len(results) != 0 {
		t.Fatalf("empty upgrade = %+v, %v", results, err)
	}
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "settings.json")
	user := `{"theme":"custom","hooks":{"Stop":[{"hooks":[{"type":"command","command":"user-hook"}]}]}}`
	if err := os.WriteFile(path, []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	old, err := RunContext(t.Context(), Options{Harness: registry.HarnessClaude, Binary: "/bin/old-aht"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(old.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, dry := range []bool{true, false, false} {
		results, err = Upgrade(t.Context(), "/bin/new-aht", dry)
		if err != nil || len(results) != 1 || results[0].Harness != "claude" {
			t.Fatalf("upgrade = %+v, %v", results, err)
		}
		assertUpgradedHooks(t, old.Path, before, dry)
	}
	if results[0].Changed {
		t.Fatal("repeated upgrade is not idempotent")
	}
}

func assertUpgradedHooks(t *testing.T, path string, before []byte, dry bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if dry {
		if string(data) != string(before) {
			t.Fatal("dry run changed hooks")
		}
		return
	}
	for _, want := range []string{"user-hook", "custom", "/bin/new-aht"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("lost %q: %s", want, data)
		}
	}
	if strings.Contains(string(data), "/bin/old-aht") {
		t.Fatal("stale hook remains")
	}
}

func TestUpgradePreservesShimTargetWithoutInstallingNativeHooks(t *testing.T) {
	isolateUpgradeHome(t)
	target := "/custom/harness with 'quotes'"
	old, err := installShim(Options{Binary: "/bin/old-aht", TargetBinary: target}, registry.HarnessCodex)
	if err != nil {
		t.Fatal(err)
	}
	results, err := Upgrade(t.Context(), "/bin/new-aht", false)
	if err != nil || len(results) != 1 || results[0].Path != old.Path {
		t.Fatalf("upgrade = %+v, %v", results, err)
	}
	got, exists, err := installedShim(registry.HarnessCodex)
	if err != nil || !exists || got != target {
		t.Fatalf("target = %q, %v, %v", got, exists, err)
	}
	native, err := installedNative(registry.HarnessCodex, "/bin/new-aht")
	if err != nil || native {
		t.Fatalf("shim migrated to native: %v, %v", native, err)
	}
}

func TestUpgradeContinuesAfterFailureAndHonorsCancellation(t *testing.T) {
	isolateUpgradeHome(t)
	claude, err := RunContext(t.Context(), Options{Harness: registry.HarnessClaude, Binary: "/bin/old-aht"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude.Path, []byte(`{"aht managed integration":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RunContext(t.Context(), Options{Harness: registry.HarnessCodex, Binary: "/bin/old-aht"}); err != nil {
		t.Fatal(err)
	}
	results, err := Upgrade(t.Context(), "/bin/new-aht", false)
	if err == nil || len(results) != 2 {
		t.Fatalf("upgrade = %+v, %v", results, err)
	}
	status, err := InspectContext(t.Context(), registry.HarnessCodex, "/bin/new-aht")
	if err != nil || status.Status != ArtifactCurrent {
		t.Fatalf("healthy integration was not upgraded: %+v, %v", status, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	results, err = Upgrade(ctx, "/bin/another-aht", false)
	if !errors.Is(err, context.Canceled) || len(results) != 0 {
		t.Fatalf("canceled upgrade = %+v, %v", results, err)
	}
}

func TestUpgradeAllInstalledArtifactShapes(t *testing.T) {
	isolateUpgradeHome(t)
	installFakeOpenClawCLI(t)
	installFakeHermesCLI(t)
	for _, id := range AllHarnesses() {
		// pi and omp intentionally share the explicit PI_CODING_AGENT_DIR; exercise
		// one at a time to verify that ownership does not select the other adapter.
		t.Run(string(id), func(t *testing.T) {
			if _, err := RunContext(t.Context(), Options{Harness: id, Binary: "/bin/old-aht"}); err != nil {
				t.Fatal(err)
			}
			firstResults, err := Upgrade(t.Context(), "/bin/new-aht", false)
			if err != nil || len(firstResults) != 1 || firstResults[0].Harness != string(id) || !firstResults[0].Changed {
				t.Fatalf("upgrade = %+v, %v", firstResults, err)
			}
			plan, _, err := installPlanForHarness(id, "/bin/new-aht")
			if err != nil {
				t.Fatal(err)
			}
			before := snapshotUpgradeArtifacts(t, planPaths(plan))
			repeatedResults, err := Upgrade(t.Context(), "/bin/new-aht", false)
			if err != nil || len(repeatedResults) != 1 || repeatedResults[0].Changed {
				t.Fatalf("repeated upgrade = %+v, %v", repeatedResults, err)
			}
			assertUpgradeNextStep(t, id, firstResults[0], repeatedResults[0])
			assertUpgradeArtifactsUntouched(t, before)
			if _, err := RemoveContext(t.Context(), Options{Harness: id, Binary: "/bin/new-aht"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUpgradePreservesDisabledPluginAndPermissionChoices(t *testing.T) {
	isolateUpgradeHome(t)
	hermes := installFakeHermesCLI(t)
	openclaw := installFakeOpenClawCLI(t)
	for _, id := range []registry.Harness{registry.HarnessHermes, registry.HarnessOpenClaw} {
		if _, err := RunContext(t.Context(), Options{Harness: id, Binary: "/bin/old-aht"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{filepath.Join(hermes.state, "enabled"), filepath.Join(openclaw, "policy")} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	results, err := Upgrade(t.Context(), "/bin/new-aht", false)
	if err != nil || len(results) != 2 {
		t.Fatalf("upgrade = %+v, %v", results, err)
	}
	for _, path := range []string{filepath.Join(hermes.state, "enabled"), filepath.Join(openclaw, "policy")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("upgrade changed plugin choice: %s, %v", path, err)
		}
	}
}

func assertUpgradeNextStep(t *testing.T, id registry.Harness, first, repeated Result) {
	t.Helper()
	if (id == registry.HarnessOpenClaw || id == registry.HarnessHermes) && first.NextStep == "" {
		t.Fatalf("first upgrade for %s should set NextStep restart notice: %+v", id, first)
	}
	if repeated.NextStep != "" {
		t.Fatalf("repeated upgrade for %s unexpectedly set NextStep: %+v", id, repeated)
	}
}

type artifactSnapshot struct {
	info    os.FileInfo
	content []byte
}

// Backdate mtime so an unnecessary write is detected even on filesystems with
// coarse timestamp precision; SameFile also catches atomic replacement.
func snapshotUpgradeArtifacts(t *testing.T, paths []string) map[string]artifactSnapshot {
	t.Helper()
	snapshots := make(map[string]artifactSnapshot)
	for _, path := range paths {
		snapshotUpgradePath(t, path, snapshots)
	}
	return snapshots
}

func snapshotUpgradePath(t *testing.T, path string, snapshots map[string]artifactSnapshot) {
	t.Helper()
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	err = fs.WalkDir(root.FS(), filepath.Base(path), func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		stamp := time.Unix(1234567890, 0)
		if err := root.Chtimes(relative, stamp, stamp); err != nil {
			return fmt.Errorf("backdate artifact: %w", err)
		}
		info, err := root.Stat(relative)
		if err != nil {
			return fmt.Errorf("stat artifact: %w", err)
		}
		var content []byte
		if !entry.IsDir() {
			content, err = root.ReadFile(relative)
			if err != nil {
				return fmt.Errorf("read artifact: %w", err)
			}
		}
		snapshots[filepath.Join(filepath.Dir(path), relative)] = artifactSnapshot{info: info, content: content}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertUpgradeArtifactsUntouched(t *testing.T, snapshots map[string]artifactSnapshot) {
	t.Helper()
	for path, before := range snapshots {
		after, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(before.info, after) || !before.info.ModTime().Equal(after.ModTime()) || before.info.Mode() != after.Mode() {
			t.Fatalf("upgrade touched current artifact %s", path)
		}
		if after.IsDir() {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(content, before.content) {
			t.Fatalf("upgrade rewrote current artifact %s", path)
		}
	}
}

func TestCodexBinaryOnlyUpgradeLeavesReviewedHooksUntouched(t *testing.T) {
	isolateUpgradeHome(t)
	binary := filepath.Join(t.TempDir(), "aht")
	if err := os.WriteFile(binary, []byte("old binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	installed, err := RunContext(t.Context(), Options{Harness: registry.HarnessCodex, Binary: binary})
	if err != nil {
		t.Fatal(err)
	}
	// A reviewed file need not use the installer's JSON indentation. Reformatting
	// identical hook definitions must not create a new file for Codex to review.
	data, err := os.ReadFile(installed.Path)
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed.Path, compact.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotUpgradeArtifacts(t, []string{installed.Path})
	if err := os.WriteFile(binary, []byte("updated binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	results, err := Upgrade(t.Context(), binary, false)
	if err != nil || len(results) != 1 {
		t.Fatalf("upgrade = %+v, %v", results, err)
	}
	if results[0].Changed || results[0].NextStep != "" {
		t.Fatalf("unchanged hooks requested review: %+v", results[0])
	}
	assertUpgradeArtifactsUntouched(t, before)
}
