package pi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/pkg/registry"
)

func TestSessionTitlesUsesLatestNativeNameAndIdentity(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pi.jsonl")
	body := `{"type":"session","id":"pi"}
{"type":"session_info","name":"First name"}
` + `{"type":"message","message":{"content":"` + strings.Repeat("x", 65<<10) + `"}}` + "\n" + `{"type":"session_info","name":"Latest name"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	titles, err := New().SessionTitles(t.Context(), []registry.ObservationIdentity{
		{SessionID: "pi", SessionPath: path},
		{SessionID: "wrong", SessionPath: path},
	})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"Latest name", ""}, titles); diff != "" {
		t.Fatalf("titles mismatch (-want +got):\n%s", diff)
	}
	if err := os.WriteFile(path, []byte(`{"type":"session","id":"pi"}
{"type":"session_info","name":"Former name"}
{"type":"session_info","name":""}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	titles, err = New().SessionTitles(t.Context(), []registry.ObservationIdentity{{SessionID: "pi", SessionPath: path}})
	if err != nil || titles[0] != "" {
		t.Fatalf("cleared name = %q, %v", titles[0], err)
	}
}
