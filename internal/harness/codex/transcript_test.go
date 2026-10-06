package codex

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func readTranscript(t *testing.T, includeTools bool, lines ...string) transcript.Conversation {
	t.Helper()
	var conversation transcript.Conversation
	var recognized bool
	decoder := &transcript.Decoder{Conversation: &conversation, Recognized: &recognized, IncludeTools: includeTools}
	reader := codexHarness{}.Transcript()
	for i, line := range lines {
		if reader.FastRecord(t.Context(), decoder, []byte(line), i+1) {
			continue
		}
		var record transcript.Record
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		reader.Record(t.Context(), decoder, record, i+1)
	}
	return conversation
}

func TestTranscriptBranchAndModel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		lines []string
		want  transcript.Conversation
	}{
		{"branch from session metadata", []string{
			`{"type":"session_meta","payload":{"id":"native","cwd":"/work","git":{"branch":"main","commit_hash":"abc"}}}`,
		}, transcript.Conversation{SessionID: "native", CWD: "/work", GitBranch: "main"}},
		{"session without git", []string{
			`{"type":"session_meta","payload":{"id":"native","cwd":"/work"}}`,
		}, transcript.Conversation{SessionID: "native", CWD: "/work"}},
		{"repeated metadata keeps known branch", []string{
			`{"type":"session_meta","payload":{"id":"native","git":{"branch":"main"}}}`,
			`{"type":"session_meta","payload":{"id":"native","git":{"commit_hash":"abc"}}}`,
		}, transcript.Conversation{SessionID: "native", GitBranch: "main"}},
		{"latest turn model wins", []string{
			`{"type":"session_meta","payload":{"id":"native"}}`,
			`{"type":"turn_context","payload":{"cwd":"/work","model":"model-a","collaboration_mode":{"settings":{"model":"nested"}}}}`,
			`{"type":"turn_context","payload":{"cwd":"/work","model":"model-b"}}`,
		}, transcript.Conversation{SessionID: "native", Model: "model-b"}},
		{"turn without model keeps previous", []string{
			`{"type":"session_meta","payload":{"id":"native"}}`,
			`{"type":"turn_context","payload":{"model":"model-a"}}`,
			`{"type":"turn_context","payload":{"cwd":"/work"}}`,
		}, transcript.Conversation{SessionID: "native", Model: "model-a"}},
		{"model before metadata", []string{
			`{"type":"turn_context","payload":{"model":"model-a"}}`,
			`{"type":"session_meta","payload":{"id":"native"}}`,
		}, transcript.Conversation{SessionID: "native", Model: "model-a"}},
		{"non-turn records carry no model", []string{
			`{"type":"session_meta","payload":{"id":"native"}}`,
			`{"type":"event_msg","payload":{"type":"token_count","model":"event-model"}}`,
			`{"type":"response_item","payload":{"type":"message","role":"assistant","model":"item-model","content":[{"type":"output_text","text":"hi"}]}}`,
		}, transcript.Conversation{SessionID: "native"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, includeTools := range []bool{false, true} {
				if diff := cmp.Diff(tt.want, readTranscript(t, includeTools, tt.lines...)); diff != "" {
					t.Fatalf("includeTools=%t conversation mismatch (-want +got):\n%s", includeTools, diff)
				}
			}
		})
	}
}
