package install

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	harnesspkg "github.com/zigai/aht/internal/harness"
	harnesscatalog "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/pkg/registry"
)

func installFakeOpenClawCLI(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatalf("creating fake OpenClaw state: %v", err)
	}
	script := `#!/bin/sh
set -eu
state=${OPENCLAW_TEST_STATE:?}
printf '%s\n' "$*" >> "$state/calls"
if [ "$1 $2" = "plugins inspect" ]; then
  if [ -f "$state/inspect-json" ]; then
    cat "$state/inspect-json"
    exit 0
  fi
  if [ ! -f "$state/source" ]; then
    printf '[]\n'
    exit 0
  fi
  source=$(cat "$state/source")
  policy=false
  if [ -f "$state/policy" ]; then policy=true; fi
  version="0.0.9"
  if [ -f "$source/package.json" ]; then
    version=$(sed -n 's/.*"version": "\([^"]*\)".*/\1/p' "$source/package.json" | head -n1)
  fi
  printf '[{"plugin":{"id":"aht-state","status":"loaded","source":"path","version":"%s"},"policy":{"allowConversationAccess":%s},"install":{"source":"path","sourcePath":"%s","installPath":"%s","version":"%s"}}]\n' "$version" "$policy" "$source" "$source" "$version"
  exit 0
fi
if [ "$1 $2" = "plugins install" ]; then
  printf '%s' "$3" > "$state/source"
  exit 0
fi
if [ "$1 $2" = "plugins uninstall" ]; then
  rm -f "$state/source" "$state/policy"
  exit 0
fi
if [ "$1 $2" = "config set" ]; then
  : > "$state/policy"
  exit 0
fi
printf 'unexpected fake OpenClaw command: %s\n' "$*" >&2
exit 2
`
	path := filepath.Join(dir, "openclaw")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatalf("writing fake OpenClaw CLI: %v", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("making fake OpenClaw CLI executable: %v", err)
	}
	t.Setenv("OPENCLAW_TEST_STATE", state)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return state
}

func TestOpenClawInstallUsesNativePluginCLIAndIsIdempotent(t *testing.T) {
	state := installFakeOpenClawCLI(t)
	t.Setenv(registry.StateDirEnv, t.TempDir())

	first, err := Run(Options{Harness: registry.HarnessOpenClaw, Binary: testInstallBinary})
	if err != nil {
		t.Fatalf("installing OpenClaw plugin: %v", err)
	}
	if !first.Changed {
		t.Fatal("expected first OpenClaw install to change state")
	}
	for _, name := range []string{"package.json", "openclaw.plugin.json", "index.js", ".aht-managed"} {
		if _, err := os.Stat(filepath.Join(first.Path, name)); err != nil {
			t.Fatalf("expected installed OpenClaw file %s: %v", name, err)
		}
	}
	calls := string(readTestFile(t, filepath.Join(state, "calls"), "reading fake OpenClaw calls"))
	for _, call := range []string{
		"plugins inspect --all --json",
		"plugins install " + first.Path + " --link --force --accept-capabilities",
		"config set plugins.entries.aht-state.hooks.allowConversationAccess true --strict-json",
	} {
		if !strings.Contains(calls, call) {
			t.Fatalf("expected native OpenClaw call %q in:\n%s", call, calls)
		}
	}

	second, err := Run(Options{Harness: registry.HarnessOpenClaw, Binary: testInstallBinary})
	if err != nil {
		t.Fatalf("reinstalling OpenClaw plugin: %v", err)
	}
	if second.Changed {
		t.Fatal("expected current OpenClaw integration to be idempotent")
	}
}

func TestOpenClawPluginShapeUsesDocumentedTypedHooksWithoutConversationContent(t *testing.T) { //nolint:cyclop // One shape test validates the complete generated native plugin contract.
	t.Setenv(registry.StateDirEnv, t.TempDir())
	adapter, ok := harnesscatalog.Find(registry.HarnessOpenClaw)
	if !ok {
		t.Fatal("OpenClaw adapter not found")
	}
	installer, ok := adapter.(harnesspkg.Installable)
	if !ok {
		t.Fatal("OpenClaw adapter is not installable")
	}
	plan := installer.InstallPlan(testInstallBinary)
	pluginAction, ok := plan.Actions[0].(harnesspkg.PluginDirectoryAction)
	if !ok {
		t.Fatalf("unexpected OpenClaw install action: %T", plan.Actions[0])
	}
	plugin := pluginAction.Plan
	if plugin.Registration == nil || plugin.Registration.ID() != "aht-state" {
		t.Fatalf("unexpected OpenClaw registration plan: %#v", plugin.Registration)
	}
	var source string
	var manifest map[string]any
	for _, file := range plugin.Files {
		if file.Name == "index.js" {
			source = file.Content
		}
		if file.Name == "openclaw.plugin.json" {
			manifest, _ = file.JSONContent.(map[string]any)
		}
	}
	activation, ok := manifest["activation"].(map[string]any)
	if !ok || !reflect.DeepEqual(activation["onCapabilities"], []string{"hook"}) {
		t.Fatalf("unexpected OpenClaw hook activation: %#v", activation)
	}
	for _, required := range []string{
		`api.on("session_start"`, `api.on("before_agent_run"`, `api.on("agent_end"`, `api.on("session_end"`,
		`"--lifecycle"`, `"--no-tmux"`, `openclaw_session_key`, `openclaw_run_id`,
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("expected OpenClaw plugin source to contain %q", required)
		}
	}
	for _, prohibited := range []string{"event.prompt", "event.messages", "event.error", "openclaw sessions"} {
		if strings.Contains(source, prohibited) {
			t.Fatalf("OpenClaw plugin must not consume conversation content %q", prohibited)
		}
	}
}

func TestOpenClawNixModeFailsBeforeWriting(t *testing.T) {
	installFakeOpenClawCLI(t)
	stateDir := t.TempDir()
	t.Setenv(registry.StateDirEnv, stateDir)
	t.Setenv("OPENCLAW_NIX_MODE", "1")

	_, err := Run(Options{Harness: registry.HarnessOpenClaw, Binary: testInstallBinary})
	if err == nil || !strings.Contains(err.Error(), "OPENCLAW_NIX_MODE") {
		t.Fatalf("expected Nix mode error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(stateDir, "integrations")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no source files in Nix mode, got %v", statErr)
	}
}

func TestOpenClawRepairsStaleRegistration(t *testing.T) {
	state := installFakeOpenClawCLI(t)
	t.Setenv(registry.StateDirEnv, t.TempDir())

	if _, err := Run(Options{Harness: registry.HarnessOpenClaw, Binary: testInstallBinary}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(state, "policy")); err != nil {
		t.Fatal(err)
	}
	second, err := Run(Options{Harness: registry.HarnessOpenClaw, Binary: testInstallBinary})
	if err != nil {
		t.Fatalf("repairing stale OpenClaw registration: %v", err)
	}
	if !second.Changed {
		t.Fatal("expected stale OpenClaw registration to be repaired")
	}
	if _, err := os.Stat(filepath.Join(state, "policy")); err != nil {
		t.Fatalf("expected conversation-hook permission to be restored: %v", err)
	}
}

func TestOpenClawRefusesForeignRegistration(t *testing.T) {
	state := installFakeOpenClawCLI(t)
	stateDir := t.TempDir()
	t.Setenv(registry.StateDirEnv, stateDir)
	foreign := `[{"plugin":{"id":"aht-state","status":"loaded","version":"9.9.9"},"policy":{"allowConversationAccess":true},"install":{"source":"path","sourcePath":"/foreign/plugin","installPath":"/foreign/plugin","version":"9.9.9"}}]`
	if err := os.WriteFile(filepath.Join(state, "inspect-json"), []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Run(Options{Harness: registry.HarnessOpenClaw, Binary: testInstallBinary})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected foreign registration refusal, got %v", err)
	}
	managedPath := filepath.Join(stateDir, "integrations", "openclaw", "aht-state")
	if _, statErr := os.Stat(managedPath); !os.IsNotExist(statErr) {
		t.Fatalf("foreign refusal wrote managed source: %v", statErr)
	}

	replaced, err := Run(Options{Harness: registry.HarnessOpenClaw, Binary: testInstallBinary, Force: true})
	if err != nil || !replaced.Changed {
		t.Fatalf("forced foreign replacement = %+v, %v", replaced, err)
	}
	if _, statErr := os.Stat(managedPath); statErr != nil {
		t.Fatalf("forced replacement did not install managed source: %v", statErr)
	}
	if _, err := Remove(Options{Harness: registry.HarnessOpenClaw, Binary: testInstallBinary}); err == nil {
		t.Fatal("removal must refuse a registration still reported as foreign")
	}
	if _, statErr := os.Stat(managedPath); statErr != nil {
		t.Fatalf("foreign removal refusal removed managed source: %v", statErr)
	}
}

func TestOpenClawRemoveUsesNativeUninstall(t *testing.T) {
	state := installFakeOpenClawCLI(t)
	t.Setenv(registry.StateDirEnv, t.TempDir())
	installed, err := Run(Options{Harness: registry.HarnessOpenClaw, Binary: testInstallBinary})
	if err != nil {
		t.Fatal(err)
	}
	removed, err := Remove(Options{Harness: registry.HarnessOpenClaw, Binary: testInstallBinary})
	if err != nil {
		t.Fatal(err)
	}
	if !removed.Changed {
		t.Fatal("expected OpenClaw removal to change state")
	}
	if _, err := os.Stat(installed.Path); !os.IsNotExist(err) {
		t.Fatalf("managed OpenClaw source still exists: %v", err)
	}
	calls := string(readTestFile(t, filepath.Join(state, "calls"), "reading fake OpenClaw calls"))
	if !strings.Contains(calls, "plugins uninstall aht-state --force") {
		t.Fatalf("native OpenClaw uninstall was not called:\n%s", calls)
	}
}
