package transcript

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

type treeCapture struct {
	role, body, id string
	line           int
	at             time.Time
}

type treeReadState struct {
	conversation Conversation
	recognized   bool
	captures     []treeCapture
}

func TestFastTreeRecordMatchesOrdinaryReader(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, line string
		fast       bool
	}{
		{"text", `{"type":"message","id":"t","timestamp":"2026-09-01T10:03:00Z","message":{"role":"toolResult","content":[{"type":"text","text":"large output"}]}}`, true},
		{"null", `{"type":"message","id":"t","timestamp":"2026-09-01T10:03:00Z","message":{"role":"toolResult","content":null}}`, true},
		{"image", `{"type":"message","id":"t","timestamp":"2026-09-01T10:03:00Z","message":{"role":"toolResult","content":[{"type":"image","data":"base64"}]}}`, true},
		{"missing content", `{"type":"message","id":"t","message":{"role":"toolResult"}}`, true},
		{"mixed", `{"type":"message","id":"t","message":{"role":"toolResult","content":[{"type":"image"},{"type":"text","text":"output"}]}}`, true},
		{"nested tool result", `{"type":"message","id":"t","message":{"role":"toolResult","content":[{"type":"tool_result","content":"output"}]}}`, true},
		{"duplicate message", `{"type":"message","message":{"role":"toolResult"},"message":{"role":"user","content":"needle"}}`, false},
		{"assistant model", `{"type":"message","id":"a","message":{"role":"assistant","model":"model-a","content":"answer"}}`, true},
		{"tool result model", `{"type":"message","id":"t","message":{"role":"toolResult","model":"model-a","content":"output"}}`, true},
		{"ignored model change", `{"type":"model_change","id":"m","model":"provider/model-a"}`, true},
		{"ignored compaction", `{"type":"compaction","summary":"large data"}`, true},
		{"malformed compaction", `{"type":"compaction","summary":`, false},
		{"escaped role", `{"type":"message","message":{"role":"tool\u0052esult","content":"output"}}`, true},
		{"session metadata", `{"type":"session","id":"native","cwd":"/work","timestamp":1788256800000,"title":"First"}`, true},
		{"session name", `{"type":"session_info","name":"Name"}`, true},
		{"title", `{"type":"title","title":"New"}`, true},
		{"title change", `{"type":"title_change","title":"Latest"}`, true},
		{"user text and tools", `{"type":"message","id":"u","message":{"role":"user","content":[{"type":"text","text":"hello"},{"type":"tool_result","content":"result"}]}}`, true},
		{"wrong field shape", `{"type":"message","id":7,"message":{"role":"assistant","model":"model-a","content":"answer"}}`, false},
		{"duplicate text", `{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"old","text":null}]}}`, false},
		{"case sensitive role", `{"type":"message","message":{"Role":"assistant","content":"not indexed"}}`, true},
		{"custom message text", `{"type":"custom_message","id":"c","timestamp":"2026-09-01T10:04:00Z","content":"injected context","message":{"role":"user","content":"not the content"}}`, true},
		{"custom message blocks", `{"type":"custom_message","id":"c","timestamp":1788257040000,"content":[{"type":"text","text":"context"},{"type":"tool_use","name":"Ignored","input":{}},{"type":"tool_result","content":"not context"}]}`, true},
		{"custom message future blocks", `{"type":"custom_message","content":[{"type":"future","text":"ignored"},{"type":"text","text":"context"}]}`, true},
		{"custom message wrong content", `{"type":"custom_message","content":{"text":"not a supported shape"}}`, true},
		{"custom message duplicate id", `{"type":"custom_message","id":"old","id":"new","content":"context"}`, false},
		{"nested custom role", `{"type":"message","id":"c","timestamp":"2026-09-01T10:04:00Z","message":{"role":"custom","model":"not assistant","content":[{"type":"text","text":"context"},{"type":"tool_result","content":"tool output"}],"tool_calls":[{"name":"Read"}]}}`, true},
		{"tool calls preserve body", `{"type":"message","message":{"role":"toolResult","content":"output","tool_calls":[]}}`, true},
		{"future tool block", `{"type":"message","message":{"role":"toolResult","content":[{"type":"tool-result","output":{"value":"output"}}]}}`, true},
		{"future entry with message", `{"type":"mode_change","message":{"role":"user","content":"future"}}`, false},
		{"wrong role shape", `{"type":"message","message":{"role":7,"content":"ignored"}}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, includeTools := range []bool{false, true} {
				compareTreeRecordReaders(t, tt.line, tt.fast, includeTools)
			}
		})
	}
}

func TestTreeRecordCapturesLatestAssistantModel(t *testing.T) {
	t.Parallel()
	lines := []string{
		`{"type":"session","id":"native","cwd":"/work"}`,
		`{"type":"message","id":"u","message":{"role":"user","model":"user-model","content":"hello"}}`,
		`{"type":"message","id":"a1","message":{"role":"assistant","model":"model-a","content":"one"}}`,
		`{"type":"message","id":"a2","message":{"role":"assistant","model":"<synthetic>","content":"two"}}`,
		`{"type":"message","id":"a3","message":{"role":"assistant","content":"three"}}`,
		`{"type":"message","id":"a4","message":{"role":"assistant","model":"model-b","content":"four"}}`,
		`{"type":"message","id":"a5","message":{"role":"assistant","model":"","content":"five"}}`,
	}
	for _, includeTools := range []bool{false, true} {
		var conversation Conversation
		var recognized bool
		decoder := &Decoder{Conversation: &conversation, Recognized: &recognized, IncludeTools: includeTools}
		for i, line := range lines {
			var record Record
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				t.Fatal(err)
			}
			TreeRecord(t.Context(), decoder, record, i+1)
		}
		if conversation.Model != "model-b" {
			t.Fatalf("includeTools=%t model = %q, want model-b", includeTools, conversation.Model)
		}
	}
}

func TestTreeRecordContextProjectionAndToolModes(t *testing.T) {
	t.Parallel()
	lines := []string{
		`{"type":"session","id":"native","cwd":"/work"}`,
		`{"type":"custom_message","content":"direct context"}`,
		`{"type":"custom_message","content":[{"type":"text","text":"block context"},{"type":"tool_result","content":"not emitted"}]}`,
		`{"type":"message","message":{"role":"custom","model":"not assistant","content":[{"type":"text","text":"nested context"},{"type":"tool_result","content":"nested output"}]}}`,
		`{"type":"message","message":{"role":"toolResult","content":"output"}}`,
	}
	for _, fast := range []bool{false, true} {
		for _, includeTools := range []bool{false, true} {
			state := replayTreeTranscript(t, fast, includeTools, lines)
			var captures []TextPart
			for _, capture := range state.captures {
				captures = append(captures, TextPart{Role: capture.role, Text: capture.body})
			}
			want := []TextPart{
				{Role: "context", Text: "direct context"},
				{Role: "context", Text: "block context"},
				{Role: "context", Text: "nested context"},
				{Role: "tool", Text: ""},
				{Role: "tool", Text: ""},
			}
			if includeTools {
				want[3].Text = "nested output"
				want[4].Text = "output"
			}
			if !reflect.DeepEqual(want, captures) {
				t.Fatalf("fast=%t includeTools=%t captures=%#v want=%#v", fast, includeTools, captures, want)
			}
			wantConversation := Conversation{SessionID: "native", CWD: "/work"}
			if state.conversation != wantConversation || !state.recognized {
				t.Fatalf("fast=%t includeTools=%t recognized=%t conversation=%+v want=%+v", fast, includeTools, state.recognized, state.conversation, wantConversation)
			}
		}
	}
}

func (state *treeReadState) decoder(includeTools bool) *Decoder {
	return &Decoder{Conversation: &state.conversation, Recognized: &state.recognized, IncludeTools: includeTools, Emit: func(_ context.Context, role, body, id string, line int, at time.Time) {
		if role == "tool" && !includeTools {
			body = ""
		}
		state.captures = append(state.captures, treeCapture{role, body, id, line, at})
	}}
}

func compareTreeRecordReaders(t *testing.T, line string, wantHandled, includeTools bool) {
	t.Helper()
	ctx := t.Context()
	var want treeReadState
	wantDecoder := want.decoder(includeTools)
	var record Record
	parseErr := json.Unmarshal([]byte(line), &record)
	if parseErr == nil {
		TreeRecord(ctx, wantDecoder, record, 5)
	}
	var got treeReadState
	gotDecoder := got.decoder(includeTools)
	handled := FastTreeRecord(ctx, gotDecoder, []byte(line), 5)
	if handled != wantHandled {
		t.Fatalf("fast record handled = %t, want %t", handled, wantHandled)
	}
	if !handled {
		if got.recognized || got.conversation != (Conversation{}) || len(got.captures) != 0 {
			t.Fatalf("rejected fast record changed state: %s", line)
		}
		if parseErr == nil {
			TreeRecord(ctx, gotDecoder, record, 5)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("includeTools=%t state=%#v want=%#v", includeTools, got, want)
	}
}

func replayTreeTranscript(t *testing.T, fast, includeTools bool, lines []string) treeReadState {
	t.Helper()
	var state treeReadState
	decoder := state.decoder(includeTools)
	for index, line := range lines {
		if fast && FastTreeRecord(t.Context(), decoder, []byte(line), index+1) {
			continue
		}
		var record Record
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		TreeRecord(t.Context(), decoder, record, index+1)
	}
	return state
}
