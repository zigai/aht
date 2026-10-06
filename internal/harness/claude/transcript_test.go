package claude

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func readTranscript(t *testing.T, conversation *transcript.Conversation, includeTools bool, lines ...string) bool {
	t.Helper()
	var recognized bool
	decoder := &transcript.Decoder{Conversation: conversation, Recognized: &recognized, IncludeTools: includeTools}
	reader := claudeHarness{}.Transcript()
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
	return recognized
}

func TestTranscriptTitlePrecedence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{"generated only", []string{
			`{"type":"ai-title","aiTitle":"Old"}`,
			`{"type":"ai-title","aiTitle":"Generated"}`,
		}, "Generated"},
		{"custom before generated", []string{
			`{"type":"custom-title","customTitle":"Manual"}`,
			`{"type":"ai-title","aiTitle":"Generated"}`,
		}, "Manual"},
		{"custom after generated", []string{
			`{"type":"ai-title","aiTitle":"Generated"}`,
			`{"type":"custom-title","customTitle":"Manual"}`,
		}, "Manual"},
		{"latest custom wins", []string{
			`{"type":"custom-title","customTitle":"Old"}`,
			`{"type":"ai-title","aiTitle":"Generated"}`,
			`{"type":"custom-title","customTitle":"Manual"}`,
		}, "Manual"},
		{"cleared custom falls back", []string{
			`{"type":"custom-title","customTitle":"Old"}`,
			`{"type":"ai-title","aiTitle":"Generated"}`,
			`{"type":"custom-title","customTitle":""}`,
		}, "Generated"},
		{"malformed title keeps previous", []string{
			`{"type":"custom-title","customTitle":"Manual"}`,
			`{"type":"custom-title","customTitle":null}`,
			`{"type":"ai-title","aiTitle":7}`,
		}, "Manual"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var conversation transcript.Conversation
			readTranscript(t, &conversation, false, tt.lines...)
			if conversation.Title != tt.want {
				t.Fatalf("title = %q, want %q", conversation.Title, tt.want)
			}
		})
	}
}

func TestTranscriptTitleSurvivesResume(t *testing.T) {
	t.Parallel()
	var conversation transcript.Conversation
	readTranscript(t, &conversation, false, `{"type":"custom-title","customTitle":"Manual"}`)
	var resumed transcript.Conversation
	if err := json.Unmarshal(mustMarshal(t, conversation), &resumed); err != nil {
		t.Fatal(err)
	}
	readTranscript(t, &resumed, false, `{"type":"ai-title","aiTitle":"Generated"}`)
	if resumed.Title != "Manual" {
		t.Fatalf("title = %q, want Manual", resumed.Title)
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestTranscriptRecognizesSessionWithoutMessages(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		lines      []string
		recognized bool
		sessionID  string
	}{
		{"mode", []string{`{"type":"mode","mode":"normal","sessionId":"native"}`}, true, "native"},
		{"permission mode", []string{`{"type":"permission-mode","permissionMode":"plan","sessionId":"native"}`}, true, "native"},
		{"both with system record", []string{
			`{"type":"mode","mode":"normal","sessionId":"native"}`,
			`{"type":"permission-mode","permissionMode":"plan","sessionId":"native"}`,
			`{"type":"system","subtype":"local_command","content":"x"}`,
		}, true, "native"},
		{"mode without identity", []string{`{"type":"mode","mode":"normal"}`}, false, ""},
		{"empty identity", []string{`{"type":"permission-mode","permissionMode":"plan","sessionId":""}`}, false, ""},
		{"unrelated record with identity", []string{`{"type":"last-prompt","sessionId":"native"}`}, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var conversation transcript.Conversation
			recognized := readTranscript(t, &conversation, false, tt.lines...)
			if recognized != tt.recognized || conversation.SessionID != tt.sessionID {
				t.Fatalf("recognized = %t id = %q, want %t %q", recognized, conversation.SessionID, tt.recognized, tt.sessionID)
			}
		})
	}
}

func TestTranscriptBranchAndModel(t *testing.T) {
	t.Parallel()
	lines := []string{
		`{"type":"user","sessionId":"native","gitBranch":"main","message":{"role":"user","model":"ignored","content":"hello"}}`,
		`{"type":"assistant","sessionId":"native","gitBranch":"main","message":{"role":"assistant","model":"model-a","content":[{"type":"text","text":"hi"},{"type":"tool_use","name":"Read","input":{}}]}}`,
		`{"type":"progress","gitBranch":"progress-branch","message":{"role":"assistant","model":"progress-model"}}`,
		`{"type":"assistant","sessionId":"native","message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"No response"}]}}`,
		`{"type":"assistant","sessionId":"native","gitBranch":"feature","message":{"role":"assistant","model":"model-b","content":"done"}}`,
		`{"type":"assistant","sessionId":"native","gitBranch":"","message":{"role":"assistant","content":"done"}}`,
	}
	for _, includeTools := range []bool{false, true} {
		var conversation transcript.Conversation
		readTranscript(t, &conversation, includeTools, lines...)
		want := transcript.Conversation{SessionID: "native", GitBranch: "feature", Model: "model-b"}
		if diff := cmp.Diff(want, conversation); diff != "" {
			t.Fatalf("includeTools=%t conversation mismatch (-want +got):\n%s", includeTools, diff)
		}
	}
}

func TestTranscriptModelIgnoresSyntheticOnly(t *testing.T) {
	t.Parallel()
	var conversation transcript.Conversation
	readTranscript(t, &conversation, false, `{"type":"assistant","sessionId":"native","message":{"role":"assistant","model":"<synthetic>","content":"x"}}`)
	if conversation.Model != "" {
		t.Fatalf("model = %q, want empty", conversation.Model)
	}
}
