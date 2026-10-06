package transcript

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

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
		{"unknown block", `{"type":"message","id":"t","message":{"role":"toolResult","content":[{"type":"tool_result","content":"output"}]}}`, false},
		{"duplicate message", `{"type":"message","message":{"role":"toolResult"},"message":{"role":"user","content":"needle"}}`, false},
		{"assistant model", `{"type":"message","id":"a","message":{"role":"assistant","model":"model-a","content":"answer"}}`, false},
		{"tool result model", `{"type":"message","id":"t","message":{"role":"toolResult","model":"model-a","content":"output"}}`, true},
		{"ignored model change", `{"type":"model_change","id":"m","model":"provider/model-a"}`, true},
		{"ignored compaction", `{"type":"compaction","summary":"large data"}`, true},
		{"malformed compaction", `{"type":"compaction","summary":`, false},
		{"escaped key", `{"type":"message","message":{"role":"tool\u0052esult","content":"output"}}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			newState := func() (*Decoder, *[]struct {
				role, id string
				line     int
				at       time.Time
			},
			) {
				var conversation Conversation
				var recognized bool
				var captures []struct {
					role, id string
					line     int
					at       time.Time
				}
				decoder := &Decoder{Conversation: &conversation, Recognized: &recognized, Emit: func(_ context.Context, role, _ string, id string, line int, at time.Time) {
					captures = append(captures, struct {
						role, id string
						line     int
						at       time.Time
					}{role, id, line, at})
				}}
				return decoder, &captures
			}
			ctx := t.Context()
			wantDecoder, want := newState()
			var record Record
			parseErr := json.Unmarshal([]byte(tt.line), &record)
			if parseErr == nil {
				TreeRecord(ctx, wantDecoder, record, 5)
			}
			gotDecoder, got := newState()
			handled := FastTreeRecord(ctx, gotDecoder, []byte(tt.line), 5)
			if handled != tt.fast {
				t.Fatalf("fast record handled = %t, want %t", handled, tt.fast)
			}
			if !handled && parseErr == nil {
				TreeRecord(ctx, gotDecoder, record, 5)
			}
			if !reflect.DeepEqual(*got, *want) || !reflect.DeepEqual(*gotDecoder.Conversation, *wantDecoder.Conversation) || *gotDecoder.Recognized != *wantDecoder.Recognized {
				t.Fatalf("captures=%#v want=%#v", *got, *want)
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
