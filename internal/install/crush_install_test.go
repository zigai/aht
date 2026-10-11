package install

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/shlex"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestInstallCrushHookKeepsUserConfigAndQuoting(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CRUSH_GLOBAL_CONFIG", configDir)
	t.Setenv(registry.StateDirEnv, t.TempDir())
	path := filepath.Join(configDir, "crushrc")
	userConfig := "provider add local --type openai-compat --base-url http://127.0.0.1:1/v1\nhook add PreToolUse --name mine --command ./mine.sh\n"
	if err := os.WriteFile(path, []byte(userConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "tools dir", "it's", "aht")
	opts := Options{Harness: registry.Harness("crush"), Binary: binary, TargetBinary: "", DryRun: false, Force: false, UseShim: false}

	result, err := Run(t.Context(), opts)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if result.Path != path || !result.Changed {
		t.Fatalf("install result = %+v, want a change to %s", result, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), userConfig) {
		t.Fatalf("installed crushrc dropped user config:\n%s", data)
	}
	requireCrushReportCommand(t, installedCrushHookCommand(t, string(data)), binary)
	if status, err := Inspect(t.Context(), registry.Harness("crush"), binary); err != nil || status.Status != ArtifactCurrent {
		t.Fatalf("status = %+v, %v", status, err)
	}
	if again, err := Run(t.Context(), opts); err != nil || again.Changed {
		t.Fatalf("reinstall = %+v, %v; want no change", again, err)
	}
	requireCrushRemoveKeepsUserConfig(t, opts, path, userConfig)
}

func requireCrushReportCommand(t *testing.T, command string, binary string) {
	t.Helper()
	words, err := shlex.Split(command)
	if err != nil {
		t.Fatalf("split hook command %q: %v", command, err)
	}
	if len(words) < 3 || words[0] != binary || words[1] != "report" || words[2] != "crush" || !slices.Contains(words, "--raw-stdin") {
		t.Fatalf("hook command words = %q, want an aht report for crush from %s", words, binary)
	}
}

func requireCrushRemoveKeepsUserConfig(t *testing.T, opts Options, path string, userConfig string) {
	t.Helper()
	if _, err := Remove(t.Context(), opts); err != nil {
		t.Fatalf("remove: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != strings.TrimSpace(userConfig) {
		t.Fatalf("crushrc after remove:\n%s\nwant only the user config", data)
	}
}

func installedCrushHookCommand(t *testing.T, crushrc string) string {
	t.Helper()
	var commands []string
	for line := range strings.Lines(crushrc) {
		words, err := shlex.Split(line)
		if err != nil {
			t.Fatalf("split crushrc line %q: %v", line, err)
		}
		if len(words) < 3 || words[0] != "hook" || words[1] != "add" || words[2] != "PreToolUse" || !slices.Contains(words, "aht") {
			continue
		}
		index := slices.Index(words, "--command")
		if index < 0 || index+1 >= len(words) {
			t.Fatalf("managed hook line %q has no command", line)
		}
		commands = append(commands, words[index+1])
	}
	if len(commands) != 1 {
		t.Fatalf("managed PreToolUse hooks = %q, want exactly one in:\n%s", commands, crushrc)
	}
	return commands[0]
}
