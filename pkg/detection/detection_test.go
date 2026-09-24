package detection_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/pkg/detection"
	"github.com/zigai/aht/pkg/registry"
)

func TestInspectUsesNormalizedBoundedScreen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	screen := "Would you like to run the following command?\n" + strings.Repeat("older text\n", 100) + "\x1b[32mThinking… esc to interrupt\x1b[0m"
	got, err := detection.Inspect(t.Context(), registry.Harness("claude"), screen, detection.Options{ConfigDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if got.Decision.Activity != registry.ActivityRunning || got.WinningRule != "working_interruptible" || got.LinesEvaluated != 100 {
		t.Fatalf("unexpected inspection: %+v", got)
	}
	if got.Screen != "" {
		t.Fatal("screen content returned without opt-in")
	}
}

func TestInspectExplicitManifestAndTrace(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "codex.toml")
	manifest := `version = 1
agent = "codex"
[[rules]]
id = "higher"
state = "waiting"
priority = 20
all = ["approval"]
[[rules]]
id = "lower"
state = "running"
priority = 10
any = ["approval"]
`
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := detection.Inspect(t.Context(), registry.Harness("codex"), "approval", detection.Options{ManifestPath: path, IncludeScreen: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.WinningRule != "higher" || len(got.Candidates) != 2 || !got.Candidates[1].Matched || got.Candidates[1].Winner || got.Screen != "approval" {
		t.Fatalf("unexpected priority trace: %+v", got)
	}
	if _, err := detection.Inspect(t.Context(), registry.Harness("claude"), "approval", detection.Options{ManifestPath: path}); err == nil {
		t.Fatal("mismatched harness accepted")
	}
}

func TestInspectInputFailures(t *testing.T) {
	t.Parallel()
	_, err := detection.Inspect(t.Context(), registry.Harness("codex"), strings.Repeat("x", detection.MaxScreenBytes+1), detection.Options{})
	if !errors.Is(err, detection.ErrScreenTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
	_, err = detection.Inspect(t.Context(), registry.Harness("codex"), "", detection.Options{ManifestPath: "rules.toml", ConfigDir: "rules"})
	if !errors.Is(err, detection.ErrManifestOptions) {
		t.Fatalf("conflicting options error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = detection.Inspect(ctx, registry.Harness("codex"), "", detection.Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}
}
