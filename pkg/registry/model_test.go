package registry_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestFilterUsesSnakeCaseWireNames(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(
		registry.Filter{
			Harness:            registry.Harness("catalog"),
			Presence:           registry.PresenceLive,
			Activity:           registry.ActivityIdle,
			MultiplexerSession: "work",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	var old map[string]string
	if err := json.Unmarshal(encoded, &old); err != nil {
		t.Fatal(err)
	}

	if old["harness"] != "catalog" || old["presence"] != "live" || old["activity"] != "idle" ||
		old["multiplexer_session"] != "work" {
		t.Fatalf("invalid filter encoding: %s", encoded)
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
