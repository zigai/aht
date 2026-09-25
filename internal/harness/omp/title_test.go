package omp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestSessionTitlesUsesCurrentSlotAndNativeIdentity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	current := filepath.Join(root, "current.jsonl")
	legacy := filepath.Join(root, "legacy.jsonl")
	cleared := filepath.Join(root, "cleared.jsonl")
	for path, body := range map[string]string{
		current: `{"type":"title","title":"Current name"}
{"type":"session","id":"current","title":"Stale header"}
{"type":"title_change","title":"Stale audit"}
`,
		legacy: `{"type":"session","id":"legacy","title":"Header name"}` + "\n" +
			`{"type":"message","message":{"content":"` + strings.Repeat("x", 65<<10) + `"}}` + "\n" +
			`{"type":"title_change","title":"Later name"}` + "\n",
		cleared: `{"type":"title","title":""}
{"type":"session","id":"cleared","title":"Stale header"}
`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	titles, err := New().SessionTitles(t.Context(), []registry.ObservationIdentity{
		{SessionID: "current", SessionPath: current},
		{SessionID: "legacy", SessionPath: legacy},
		{SessionID: "cleared", SessionPath: cleared},
		{SessionID: "wrong", SessionPath: current},
		{SessionID: "missing", SessionPath: filepath.Join(root, "missing.jsonl")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"Current name", "Later name", "", "", ""}, titles); diff != "" {
		t.Fatalf("titles mismatch (-want +got):\n%s", diff)
	}
}
