package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	harnesspkg "github.com/zigai/aht/internal/harness"
	harnesscatalog "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/pkg/registry"
)

type fakeHermesCLI struct {
	state string
	home  string
}

func installFakeHermesCLI(t *testing.T) fakeHermesCLI {
	t.Helper()

	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	hermesHome := filepath.Join(dir, "home")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatalf("creating fake Hermes state: %v", err)
	}
	script := `#!/bin/sh
set -eu
state=${HERMES_TEST_STATE:?}
plugin=${HERMES_HOME:?}/plugins/aht-state
printf '%s\n' "$*" >> "$state/calls"
if [ "$1 $2" = "plugins list" ]; then
  if [ ! -f "$plugin/plugin.yaml" ]; then
    printf '[]\n'
    exit 0
  fi
  status="not enabled"
  if [ -f "$state/enabled" ]; then status=enabled; fi
  printf '[{"name":"aht-state","status":"%s","version":"0.0.9","description":"test","source":"user"}]\n' "$status"
  exit 0
fi
if [ "$1 $2" = "plugins enable" ]; then
  test -f "$plugin/plugin.yaml"
  : > "$state/enabled"
  exit 0
fi
if [ "$1 $2" = "plugins disable" ]; then
  rm -f "$state/enabled"
  exit 0
fi
if [ "$1 $2" = "plugins remove" ]; then
  rm -rf "$plugin"
  rm -f "$state/enabled"
  exit 0
fi
printf 'unexpected fake Hermes command: %s\n' "$*" >&2
exit 2
`
	path := filepath.Join(dir, "hermes")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatalf("writing fake Hermes CLI: %v", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("making fake Hermes CLI executable: %v", err)
	}
	t.Setenv("HERMES_TEST_STATE", state)
	t.Setenv("HERMES_HOME", hermesHome)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return fakeHermesCLI{state: state, home: hermesHome}
}

func TestHermesInstallUsesNativePluginCLIAndIsIdempotent(t *testing.T) {
	fake := installFakeHermesCLI(t)

	first, err := Run(Options{Harness: registry.Harness("hermes"), Binary: testInstallBinary})
	if err != nil {
		t.Fatalf("installing Hermes plugin: %v", err)
	}
	if !first.Changed {
		t.Fatal("expected first Hermes install to change state")
	}
	for _, name := range []string{"plugin.yaml", "__init__.py", ".aht-managed"} {
		if _, err := os.Stat(filepath.Join(first.Path, name)); err != nil {
			t.Fatalf("expected installed Hermes file %s: %v", name, err)
		}
	}
	calls := string(readTestFile(t, filepath.Join(fake.state, "calls"), "reading fake Hermes calls"))
	for _, call := range []string{
		"plugins list --user --json",
		"plugins enable aht-state --no-allow-tool-override",
	} {
		if !strings.Contains(calls, call) {
			t.Fatalf("expected native Hermes call %q in:\n%s", call, calls)
		}
	}

	second, err := Run(Options{Harness: registry.Harness("hermes"), Binary: testInstallBinary})
	if err != nil {
		t.Fatalf("reinstalling Hermes plugin: %v", err)
	}
	if second.Changed {
		t.Fatal("expected current Hermes integration to be idempotent")
	}
}

//nolint:cyclop // assertions cover each documented hook and privacy boundary independently
func TestHermesPluginShapeUsesDocumentedHooksWithoutSensitiveContent(t *testing.T) {
	t.Setenv("HERMES_HOME", t.TempDir())
	adapter, ok := harnesscatalog.Find(registry.Harness("hermes"))
	if !ok {
		t.Fatal("Hermes adapter not found")
	}
	installer, ok := adapter.(harnesspkg.Installable)
	if !ok {
		t.Fatal("Hermes adapter is not installable")
	}
	plan := installer.InstallPlan(testInstallBinary)
	pluginAction, ok := plan.Actions[0].(harnesspkg.PluginDirectoryAction)
	if !ok {
		t.Fatalf("unexpected Hermes install action: %T", plan.Actions[0])
	}
	plugin := pluginAction.Plan
	if plugin.Registration == nil || plugin.Registration.ID() != "aht-state" {
		t.Fatalf("unexpected Hermes activation plan: %#v", plugin.Registration)
	}
	var manifest, source string
	for _, file := range plugin.Files {
		switch file.Name {
		case "plugin.yaml":
			manifest = file.Content
		case "__init__.py":
			source = file.Content
		}
	}
	for _, hook := range []string{
		"on_session_start", "pre_llm_call", "on_session_end", "on_session_finalize",
		"on_session_reset", "pre_approval_request", "post_approval_response",
	} {
		if !strings.Contains(manifest, "  - "+hook) || !strings.Contains(source, `ctx.register_hook("`+hook+`"`) {
			t.Fatalf("expected documented Hermes hook %q in manifest and source", hook)
		}
	}
	for _, required := range []string{
		`transition["lifecycle"] = "resume"`, `"lifecycle": "end"`, `"activity": "waiting"`,
		`"activity": "running"`, `"activity": "idle"`, `"--no-tmux"`,
		`"hermes", "--resume", session_id`, "child.wait(timeout=",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("expected Hermes plugin source to contain %q", required)
		}
	}
	for _, prohibited := range []string{
		`values.get("user_message")`, `values.get("conversation_history")`,
		`values.get("command")`, `values.get("description")`, `hermes sessions list`,
	} {
		if strings.Contains(source, prohibited) {
			t.Fatalf("Hermes plugin must not consume sensitive content %q", prohibited)
		}
	}
}

func TestHermesRepairsDisabledPlugin(t *testing.T) {
	fake := installFakeHermesCLI(t)
	if _, err := Run(Options{Harness: registry.Harness("hermes"), Binary: testInstallBinary}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(fake.state, "enabled")); err != nil {
		t.Fatal(err)
	}

	repaired, err := Run(Options{Harness: registry.Harness("hermes"), Binary: testInstallBinary})
	if err != nil {
		t.Fatalf("repairing disabled Hermes plugin: %v", err)
	}
	if !repaired.Changed {
		t.Fatal("expected disabled Hermes plugin to be repaired")
	}
	if _, err := os.Stat(filepath.Join(fake.state, "enabled")); err != nil {
		t.Fatalf("expected Hermes plugin to be enabled: %v", err)
	}
}

func TestHermesManagedModeFailsBeforeWriting(t *testing.T) {
	fake := installFakeHermesCLI(t)
	t.Setenv("HERMES_MANAGED", "nixos")
	pluginPath := filepath.Join(fake.home, "plugins", "aht-state")

	_, err := Run(Options{Harness: registry.Harness("hermes"), Binary: testInstallBinary})
	if err == nil || !strings.Contains(err.Error(), "package-manager-managed") {
		t.Fatalf("expected managed mode error, got %v", err)
	}
	if _, statErr := os.Stat(pluginPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no plugin files in managed mode, got %v", statErr)
	}
}

func TestHermesRefusesForeignPluginDirectory(t *testing.T) {
	fake := installFakeHermesCLI(t)
	pluginPath := filepath.Join(fake.home, "plugins", "aht-state")
	if err := os.MkdirAll(pluginPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginPath, "plugin.yaml"), []byte("name: foreign\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Run(Options{Harness: registry.Harness("hermes"), Binary: testInstallBinary})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected foreign plugin refusal, got %v", err)
	}
	replaced, err := Run(Options{Harness: registry.Harness("hermes"), Binary: testInstallBinary, Force: true})
	if err != nil || !replaced.Changed {
		t.Fatalf("forced Hermes replacement = %+v, %v", replaced, err)
	}
}

func TestHermesRemoveUsesNativePluginCLI(t *testing.T) {
	fake := installFakeHermesCLI(t)
	installed, err := Run(Options{Harness: registry.Harness("hermes"), Binary: testInstallBinary})
	if err != nil {
		t.Fatal(err)
	}
	removed, err := Remove(Options{Harness: registry.Harness("hermes"), Binary: testInstallBinary})
	if err != nil {
		t.Fatal(err)
	}
	if !removed.Changed {
		t.Fatal("expected Hermes removal to change state")
	}
	if _, err := os.Stat(installed.Path); !os.IsNotExist(err) {
		t.Fatalf("managed Hermes plugin still exists: %v", err)
	}
	calls := string(readTestFile(t, filepath.Join(fake.state, "calls"), "reading fake Hermes calls"))
	for _, call := range []string{
		"plugins disable aht-state",
		"plugins remove aht-state",
	} {
		if !strings.Contains(calls, call) {
			t.Fatalf("native Hermes removal call %q not found:\n%s", call, calls)
		}
	}
}
