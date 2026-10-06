package cline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func TestTranscriptManifestBranchAndModel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, manifest, branch, model string
	}{
		{"complete", `{"session_id":"native","model":"model-a","cwd":"/work","metadata":{"git":{"branch":"main"},"title":"t"}}`, "main", "model-a"},
		{"no git", `{"session_id":"native","model":"model-a","metadata":{}}`, "", "model-a"},
		{"other session", `{"session_id":"other","model":"model-a","metadata":{"git":{"branch":"main"}}}`, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "native.messages.json")
			if err := os.WriteFile(path, []byte(`{"sessionId":"native","messages":[]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "native.json"), []byte(tt.manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			conversation := transcript.Conversation{Path: path}
			var recognized bool
			decoder := &transcript.Decoder{Conversation: &conversation, Recognized: &recognized, Issue: func(path string, err error) { t.Errorf("%s: %v", path, err) }}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := (clineHarness{}).Transcript().Document(t.Context(), decoder, data); err != nil {
				t.Fatal(err)
			}
			if conversation.GitBranch != tt.branch || conversation.Model != tt.model {
				t.Fatalf("branch = %q model = %q, want %q %q", conversation.GitBranch, conversation.Model, tt.branch, tt.model)
			}
		})
	}
}
