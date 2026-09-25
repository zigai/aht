package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/install"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestIntegrationUpgradeRewritesStaleArtifact(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, key := range []string{"XDG_CONFIG_HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "COPILOT_HOME", "CLINE_DIR", "CLINE_HOOKS_DIR", "KIMI_SHARE_DIR", "GROK_HOME", "PI_CODING_AGENT_DIR", "AGY_CONFIG_HOME", "HERMES_HOME", "OPENCODE_CONFIG_DIR", "KILO_CONFIG_DIR", registry.StateDirEnv} {
		t.Setenv(key, filepath.Join(home, key))
	}
	path := filepath.Join(home, "PI_CODING_AGENT_DIR", "extensions", "aht-state.ts")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	stale := []byte("// aht managed integration\n// AHT_INTEGRATION_ID=pi\n// AHT_INTEGRATION_VERSION=1\nargs.push(\"--reporter-version\", \"1\");\nargs.push(\"--reporter\", currentSessionSource);\n")
	if err := os.WriteFile(path, stale, 0o600); err != nil {
		t.Fatal(err)
	}
	for index := range 2 {
		var stdout bytes.Buffer
		if err := runTestCLI(t.Context(), []string{"--json", "manage", "integrations", "upgrade", "--binary", "/bin/new-aht"}, &stdout, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		var results []install.Result
		if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].Harness != "pi" || results[0].Changed != (index == 0) {
			t.Fatalf("upgrade results=%+v", results)
		}
		assertTypedReporterArtifact(t, path)
	}
}

func assertTypedReporterArtifact(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"--reporter", currentSessionSource`) || strings.Contains(string(data), `"--reporter-version", "1"`) {
		t.Fatal("stale artifact was not upgraded to the current reporter version")
	}
}
