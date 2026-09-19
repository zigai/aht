package registry_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/pkg/registry"
)

func TestFilterPreservesBrokerWireNames(t *testing.T) {
	t.Parallel()
	encoded, err := json.Marshal(registry.Filter{Harness: registry.HarnessClaude, Presence: registry.PresenceLive, Activity: registry.ActivityIdle, TmuxSession: "work", MultiplexerSession: "work"})
	if err != nil {
		t.Fatal(err)
	}
	var old map[string]string
	if err := json.Unmarshal(encoded, &old); err != nil {
		t.Fatal(err)
	}
	if old["Harness"] != "claude" || old["Presence"] != "live" || old["Activity"] != "idle" || old["TmuxSession"] != "work" || old["MultiplexerSession"] != "work" {
		t.Fatalf("old broker could not decode filter: %s", encoded)
	}
}

func TestProjectSubtreeAllowsDotPrefixedChild(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if !registry.PathWithinOrEqual(filepath.Join(root, "..notes"), root) {
		t.Fatal("legitimate child rejected")
	}
	if registry.PathWithinOrEqual(filepath.Join(root, "..", "sibling"), root) {
		t.Fatal("sibling accepted")
	}
}
