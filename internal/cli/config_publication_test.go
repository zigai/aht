package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/internal/config"
)

func TestCLIConfigPublicationSkipsInvalidCommands(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "unknown command", args: []string{"unknown"}},
		{name: "extra operand", args: []string{"list", "extra"}},
		{name: "summary sort", args: []string{"list", "--summary", "--sort", "updated"}},
		{name: "unknown sort", args: []string{"list", "--sort", "invalid"}},
		{name: "empty sort", args: []string{"list", "--sort="}},
		{name: "invalid filter", args: []string{"list", "--presence", "invalid"}},
		{name: "missing info reference", args: []string{"info"}},
		{name: "conflicting info references", args: []string{"info", "session", "--pane", "%1"}},
		{name: "info config without explanation", args: []string{"info", "session", "--config-dir", "/tmp"}},
		{name: "watch format", args: []string{"watch", "--format", "invalid"}},
		{name: "watch JSON format", args: []string{"watch", "--json", "--format", "plain"}},
		{name: "tracker interval", args: []string{"manage", "tracker", "run", "--interval", "0s"}},
		{name: "tracker enable interval", args: []string{"manage", "tracker", "enable", "--interval", "0s"}},
		{name: "clean selection", args: []string{"manage", "state", "clean", "--all", "--older-than", "1h"}},
		{name: "clean negative age", args: []string{"manage", "state", "clean", "--older-than=-1h"}},
		{name: "detection harness", args: []string{"manage", "detection", "test", "invalid", "--screen", "-"}},
		{name: "search limit", args: []string{"search", "query", "--limit=-1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path, store := configureFirstRunTest(t)
			args := append([]string{"--store", store}, test.args...)
			if err := runTestCLI(t.Context(), args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
				t.Fatal("expected invalid command to fail")
			}
			assertConfigNotPublished(t, path)
		})
	}
}

func TestCLIConfigPublicationSkipsPreviewsAndProtocols(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "help", args: []string{"list", "--help"}},
		{name: "version", args: []string{"--version"}},
		{name: "completion", args: []string{"completion", "bash"}},
		{name: "no config", args: []string{"--no-config", "list"}},
		{name: "setup preview", args: []string{"manage", "setup", "codex", "--binary", "/bin/aht", "--dry-run"}},
		{name: "tracker preview", args: []string{"manage", "tracker", "enable", "--binary", "/bin/aht", "--dry-run"}},
		{name: "report", args: []string{"report", "codex", "--session-id", "first-run", "--event", "start", "--quiet", "--no-tmux"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path, store := configureFirstRunTest(t)
			args := append([]string{"--store", store}, test.args...)
			var stdout, stderr bytes.Buffer
			if err := runTestCLI(t.Context(), args, &stdout, &stderr); err != nil {
				t.Fatalf("command failed: %v; stderr=%s", err, stderr.String())
			}
			assertConfigNotPublished(t, path)
		})
	}
}

func TestCLIConfigResolutionDoesNotPublishAndStaysCached(t *testing.T) {
	path, _ := configureFirstRunTest(t)
	app := &application{}
	first, err := app.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	assertConfigNotPublished(t, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[ui]\nsort = 'created'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := app.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if second.UI.Sort != first.UI.Sort {
		t.Fatalf("cached sort changed from %q to %q", first.UI.Sort, second.UI.Sort)
	}
}

func TestCLIInvalidConfigDoesNotPublish(t *testing.T) {
	path, store := configureFirstRunTest(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[ui]\nsort = 'invalid'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := executeCLI(t.Context(), []string{"--store", store, "list"}, nil, &stdout, &stderr)
	if code != exitCodeUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%s", code, exitCodeUsage, stderr.String())
	}
}

func TestCLIDoctorDoesNotCreateConfigBeforeDiagnostics(t *testing.T) {
	path, store := configureFirstRunTest(t)
	app := &application{storePath: store, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	command := app.newDoctorCommand()
	diagnosticsStarted := false
	command.RunE = func(_ *cobra.Command, _ []string) error {
		diagnosticsStarted = true
		assertConfigNotPublished(t, path)
		return nil
	}
	app.configureCommandTree(command)
	command.SetArgs([]string{})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !diagnosticsStarted {
		t.Fatal("doctor did not reach diagnostics")
	}
}

func TestCLIDoctorInvalidConfigKeepsStructuredFailure(t *testing.T) {
	path, store := configureFirstRunTest(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[ui]\nsort = 'invalid'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := executeCLI(t.Context(), []string{"--store", store, "manage", "doctor", "--json"}, nil, &stdout, &stderr)
	if code != exitCodeGeneral {
		t.Fatalf("exit code = %d, want %d; stderr=%s", code, exitCodeGeneral, stderr.String())
	}
	var result doctorResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("doctor did not emit structured diagnostics: %v; stdout=%s", err, stdout.String())
	}
	if result.OK || len(result.Checks) != 1 {
		t.Fatalf("unexpected doctor result: %+v", result)
	}
	check := result.Checks[0]
	if check.Name != "tracker configuration" || check.Status != doctorError || !strings.Contains(check.Message, config.ErrInvalidSort.Error()) {
		t.Fatalf("configuration diagnostic = %+v", check)
	}
}

func TestCLIFirstRunResolvesLayersWithoutWritingConfig(t *testing.T) {
	dir := t.TempDir()
	userDir := filepath.Join(dir, "user")
	systemDir := filepath.Join(dir, "system")
	t.Setenv(config.ConfigEnv, "")
	t.Setenv("XDG_CONFIG_HOME", userDir)
	t.Setenv("XDG_CONFIG_DIRS", systemDir)
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(systemDir, "aht"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(systemDir, "aht", "config.toml"), []byte("[ui]\nsort = 'created'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AHT_UI_DEFAULT_PRESENCE", "live")
	var stdout bytes.Buffer
	if err := runTestCLI(t.Context(), []string{"manage", "config", "show", "--json"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := json.Unmarshal(stdout.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Sort != "created" || cfg.UI.DefaultPresence != "live" {
		t.Fatalf("first-run settings lost existing layers: %+v", cfg.UI)
	}
	assertConfigNotPublished(t, filepath.Join(userDir, "aht", "config.toml"))
}

func configureFirstRunTest(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config", "config.toml")
	t.Setenv(config.ConfigEnv, configPath)
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("CODEX_HOME", filepath.Join(dir, ".codex"))
	return configPath, filepath.Join(dir, "store.json")
}

func assertConfigNotPublished(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first-run configuration directory was created: stat error = %v", err)
	}
}
