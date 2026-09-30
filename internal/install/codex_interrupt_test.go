package install

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestInstallCodexReportsInterruptWithinNativeTimeout(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())

	result, err := Run(t.Context(), Options{Harness: registry.Harness("codex"), Binary: defaultBinary})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	hooks, ok := config["hooks"].(map[string]any)
	if !ok {
		t.Fatal("missing hooks object")
	}
	command := requireTestHookCommand(t, hooks, "Interrupt")
	if !strings.Contains(command, "--activity interrupted --event Interrupt") || !strings.Contains(command, "--quiet") {
		t.Fatalf("Interrupt command = %q", command)
	}
	if timeoutSeconds := requireTestHookTimeoutSeconds(t, hooks, "Interrupt"); timeoutSeconds != 3 {
		t.Fatalf("Interrupt timeout = %v, want 3", timeoutSeconds)
	}
}
