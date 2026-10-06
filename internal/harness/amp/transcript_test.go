package amp

import (
	"testing"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func TestTranscriptBranchFromRepositoryRef(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, ref, want string }{
		{"branch", `"refs/heads/feature/x"`, "feature/x"},
		{"tag", `"refs/tags/v1"`, ""},
		{"missing", `null`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data := `{"id":"T-1","env":{"initial":{"trees":[{"uri":"file:///work","repository":{"ref":` + tt.ref + `}}]}},"messages":[]}`
			var conversation transcript.Conversation
			var recognized bool
			decoder := &transcript.Decoder{Conversation: &conversation, Recognized: &recognized}
			if err := (ampHarness{}).Transcript().Document(t.Context(), decoder, []byte(data)); err != nil {
				t.Fatal(err)
			}
			if conversation.GitBranch != tt.want || conversation.CWD != "/work" {
				t.Fatalf("branch = %q cwd = %q, want %q /work", conversation.GitBranch, conversation.CWD, tt.want)
			}
		})
	}
}

func TestTranscriptWithoutTrees(t *testing.T) {
	t.Parallel()
	var conversation transcript.Conversation
	var recognized bool
	decoder := &transcript.Decoder{Conversation: &conversation, Recognized: &recognized}
	if err := (ampHarness{}).Transcript().Document(t.Context(), decoder, []byte(`{"id":"T-1","messages":[]}`)); err != nil {
		t.Fatal(err)
	}
	if conversation.GitBranch != "" || conversation.CWD != "" {
		t.Fatalf("conversation = %+v", conversation)
	}
}
