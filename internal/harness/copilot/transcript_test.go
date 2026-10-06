package copilot

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func TestTranscriptBranchAndModel(t *testing.T) {
	t.Parallel()
	lines := []string{
		`{"type":"session.start","data":{"sessionId":"native","context":{"cwd":"/work","gitRoot":"/work","branch":"main"}}}`,
		`{"type":"tool.execution_complete","data":{"model":"model-a","result":{"content":"ok"}}}`,
		`{"type":"session.model_change","data":{"previousModel":"model-a","newModel":"model-b"}}`,
		`{"type":"session.model_change","data":{"reasoningEffort":"high"}}`,
		`{"type":"assistant.message","data":{"content":"hi","model":"ignored"}}`,
		`{"type":"session.shutdown","data":{"currentModel":"model-c"}}`,
	}
	var conversation transcript.Conversation
	var recognized bool
	decoder := &transcript.Decoder{Conversation: &conversation, Recognized: &recognized}
	reader := copilotHarness{}.Transcript()
	for i, line := range lines {
		var record transcript.Record
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		reader.Record(t.Context(), decoder, record, i+1)
	}
	want := transcript.Conversation{SessionID: "native", CWD: "/work", ProjectRoot: "/work", GitBranch: "main", Model: "model-c"}
	if diff := cmp.Diff(want, conversation); diff != "" {
		t.Fatalf("conversation mismatch (-want +got):\n%s", diff)
	}
}
