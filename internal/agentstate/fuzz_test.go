package agentstate

import (
	"testing"

	"github.com/zigai/aht/pkg/registry"
)

func FuzzNormalizeSnapshotBounded(f *testing.F) {
	f.Add("\x1b[31mworking\x1b[0m\nline", "\x1b]0;codex\a")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, screen string, title string) {
		snapshot := NormalizeSnapshot(screen, title)
		if len(snapshot.Lines) > maxSnapshotLines {
			t.Fatalf("snapshot has %d lines, maximum is %d", len(snapshot.Lines), maxSnapshotLines)
		}
	})
}

func FuzzParseManifest(f *testing.F) {
	f.Add([]byte("version=1\nagent='codex'\n[[rules]]\nid='idle'\nstate='idle'\nany=['ready']\n"))
	f.Add([]byte("not toml"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseManifest(data, registry.Harness("codex"))
	})
}
