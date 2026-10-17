package claude

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

type transcriptCapture struct {
	Role, Body, ID string
	Line           int
	At             time.Time
}

type transcriptReadState struct {
	Conversation transcript.Conversation
	Recognized   bool
	Captures     []transcriptCapture
}

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

func TestFastTranscriptPreservesConversationAndCaptures(t *testing.T) {
	t.Parallel()
	lines := []string{
		`{"type":"mode","sessionId":"native"}`,
		`{"type":"permission-mode","sessionId":"native"}`,
		`{"type":"user","sessionId":"native","cwd":"/work","gitBranch":"main","uuid":"u","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","uuid":"a","timestamp":1788256860000,"message":{"role":"assistant","model":"model-a","content":[{"type":"text","text":"answer"},{"type":"tool_use","name":"Read","input":{"path":"file"}}]}}`,
		`{"type":"user","cwd":"/new","gitBranch":"feature","uuid":"t","timestamp":"2026-09-01T10:02:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"output"}]}}`,
		`{"type":"user","uuid":"empty","timestamp":"2026-09-01T10:03:00Z","message":{"role":"user","content":[{"type":"tool_result","content":""}]}}`,
		`{"type":"user","uuid":"mixed","message":{"role":"user","content":[{"type":"tool_result","content":"output"},{"type":"text","text":"followup"}]}}`,
		`{"type":"user","uuid":"future","message":{"role":"user","content":[{"type":"future","text":"not known"},{"type":"tool_result","content":[{"type":"text","text":"nested"}]}]}}`,
		`{"type":"ai-title","aiTitle":"Generated"}`,
		`{"type":"custom-title","customTitle":"Manual"}`,
		`{"type":"custom-title","customTitle":null}`,
		`{"type":"attachment","cwd":"/ignored","gitBranch":"ignored","timestamp":"2026-09-02T10:00:00Z"}`,
		`{"type":"assistant","sessionId":7,"message":{"role":"assistant","model":"model-b","content":"wrong identity shape"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"old"},"message":{"role":"user","content":"last duplicate"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"old","text":null}]}}`,
		`{"type":"assistant","message":{"Role":"assistant","content":"case-sensitive role"}}`,
		`{"type":"assistant","message":{"role":"assistant","MODEL":"ignored","content":"case-sensitive model"}}`,
		`{"type":"assistant","uuid":"summary","isCompactSummary":true,"timestamp":"2026-09-01T10:04:00Z","message":{"role":"assistant","model":"summary-model","content":[{"type":"text","text":"compact context"},{"type":"tool_use","name":"Read","input":{}}]}}`,
		`{"type":"user","uuid":"meta","isMeta":true,"timestamp":"2026-09-01T10:05:00Z","message":{"role":"user","model":"ignored","content":[{"type":"text","text":"injected context"},{"type":"tool_result","content":"result"}]}}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":[{"type":"tool_result","content":"meta tool result"}]}}`,
		`{"type":"user","isMeta":true,"message":{"role":"future","content":"context overrides unknown role"}}`,
		`{"type":"user","isMeta":false,"isCompactSummary":false,"message":{"role":"user","content":"still user"}}`,
		`{"type":"user","isMeta":"true","message":{"role":"user","content":"not a boolean"}}`,
		`{"type":"user","isMeta":true,"isMeta":false,"message":{"role":"user","content":"last flag wins"}}`,
		`{"type":"user","isMeta":true,"message":null}`,
	}
	for _, includeTools := range []bool{false, true} {
		want := replayTranscript(t, false, includeTools, lines)
		got := replayTranscript(t, true, includeTools, lines)
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("includeTools=%t (-ordinary +fast):\n%s", includeTools, diff)
		}
	}
}

func TestFastTranscriptRejectsMalformedAndUnrecognizedShapes(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		`{"type":"attachment","attachment":`,
		`{"type":"atis-latch"} trailing`,
		`{"type":"last-prompt","message":{"role":"user","content":"future text"}}`,
		`{"type":"file-history-delta","text":"future text"}`,
		`{"type":"cost-state","content":"future text"}`,
		`{"type":"pr-link","type":"unknown"}`,
		`{"type":"user","cwd":"/partial","isMeta":"true","message":{"role":"user","content":"fallback"}}`,
		`{"type":"assistant","isCompactSummary":{},"message":{"role":"assistant","model":"ignored","content":"fallback"}}`,
		`{"type":"user","isMeta":true,"message":{"role":7,"content":"fallback"}}`,
		`{"type":"user","cwd":"/partial","message":{"role":"user","content":[{"type":"tool_result","content":"broken"}]}`,
	} {
		var conversation transcript.Conversation
		var recognized bool
		decoder := &transcript.Decoder{Conversation: &conversation, Recognized: &recognized}
		if readFastRecord(t.Context(), decoder, []byte(line), 1) {
			t.Fatalf("handled malformed or unrecognized record: %s", line)
		}
		if recognized || conversation != (transcript.Conversation{}) {
			t.Fatalf("rejected record changed state: recognized=%t conversation=%+v", recognized, conversation)
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

func TestTranscriptContextProjectionPreservesMetadata(t *testing.T) {
	t.Parallel()
	lines := []string{
		`{"type":"user","sessionId":"native","cwd":"/work","gitBranch":"main","isMeta":true,"message":{"role":"user","content":"injected"}}`,
		`{"type":"assistant","isCompactSummary":true,"message":{"role":"assistant","model":"summary-model","content":"summary"}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"output"}]}}`,
	}
	for _, fast := range []bool{false, true} {
		for _, includeTools := range []bool{false, true} {
			state := replayTranscript(t, fast, includeTools, lines)
			want := []transcriptCapture{{Role: "context", Body: "injected", Line: 1}, {Role: "context", Body: "summary", Line: 2}, {Role: "tool", Line: 3}}
			if includeTools {
				want[2].Body = "output"
			}
			if diff := cmp.Diff(want, state.Captures); diff != "" {
				t.Fatalf("fast=%t includeTools=%t captures (-want +got):\n%s", fast, includeTools, diff)
			}
			wantConversation := transcript.Conversation{SessionID: "native", CWD: "/work", GitBranch: "main", Model: "summary-model"}
			if diff := cmp.Diff(wantConversation, state.Conversation); diff != "" || !state.Recognized {
				t.Fatalf("fast=%t includeTools=%t recognized=%t conversation (-want +got):\n%s", fast, includeTools, state.Recognized, diff)
			}
		}
	}
}

func replayTranscript(t *testing.T, fast, includeTools bool, lines []string) transcriptReadState {
	t.Helper()
	var state transcriptReadState
	decoder := &transcript.Decoder{Conversation: &state.Conversation, Recognized: &state.Recognized, IncludeTools: includeTools, Emit: func(_ context.Context, role, body, id string, line int, at time.Time) {
		if role == "tool" && !includeTools {
			body = ""
		}
		state.Captures = append(state.Captures, transcriptCapture{role, body, id, line, at})
	}}
	for index, line := range lines {
		if fast && readFastRecord(t.Context(), decoder, []byte(line), index+1) {
			continue
		}
		var record transcript.Record
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		readTranscriptRecord(t.Context(), decoder, record, index+1)
	}
	return state
}
