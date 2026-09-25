package goose

import (
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
)

func TestMatchersOmitInvalidWildcard(t *testing.T) {
	hooks, ok := gooseHookConfig()["hooks"].(map[string]any)
	if !ok {
		t.Fatal("expected Goose hooks map")
	}
	for _, event := range []string{harness.HookEventPreToolUse, "PostToolUse", "BeforeShellExecution"} {
		groups, ok := hooks[event].([]any)
		if !ok || len(groups) != 1 {
			t.Fatalf("expected one %s hook group, got %#v", event, hooks[event])
		}
		group, ok := groups[0].(map[string]any)
		if !ok {
			t.Fatalf("expected %s hook group object", event)
		}
		if _, ok := group["matcher"]; ok {
			t.Fatalf("expected %s matcher to be omitted", event)
		}
	}
}
