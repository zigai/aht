package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/v2/internal/harness/catalog"
)

func TestWireRejectsUnsupportedInvocationBeforeSideEffects(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"hook", "wire", "codex", "--"},
		{"hook", "wire", "kimi-code"},
		{"hook", "wire", "kimi-code", "--", "--print"},
		{"hook", "wire", "kimi-code", "--", "--session", "native-session", "web"},
	} {
		store := filepath.Join(t.TempDir(), "state.json")
		var stdout, stderr bytes.Buffer
		if err := runTestCLI(t.Context(), append([]string{"--store", store}, args...), &stdout, &stderr); err == nil {
			t.Fatalf("accepted unsupported invocation: %v", args)
		}
		if _, err := os.Stat(store); !os.IsNotExist(err) {
			t.Fatalf("invocation touched store: %v", err)
		}
		if stdout.Len() != 0 {
			t.Fatal("parse failure wrote protocol data")
		}
	}
}

func TestWireNativeValuesAreNotReinterpretedAsModes(t *testing.T) {
	t.Parallel()
	args := []string{"--prompt", "--print", "--model=web", "--session", "native-session"}
	runner, ok := catalog.WireRunnerFor("kimi-code")
	if !ok {
		t.Fatal("missing Wire adapter")
	}
	if err := runner.ValidateWireArgs(args); err != nil {
		t.Fatalf("native option value was reinterpreted: %v", err)
	}
}
