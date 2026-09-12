package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/internal/harness/kimi"
)

func TestWireRejectsUnsupportedInvocationBeforeSideEffects(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"wire", "codex", "--"},
		{"wire", "kimi-code"},
		{"wire", "kimi-code", "--", "--print"},
		{"wire", "kimi-code", "--", "--session", "native-session", "web"},
	} {
		store := filepath.Join(t.TempDir(), "state.json")
		var stdout, stderr bytes.Buffer
		root := NewRootCommand(&stdout, &stderr)
		root.SetArgs(append([]string{"--store", store}, args...))
		if err := root.ExecuteContext(t.Context()); err == nil {
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
	if err := kimi.ValidateArgs(args); err != nil {
		t.Fatalf("native option value was reinterpreted: %v", err)
	}
}
