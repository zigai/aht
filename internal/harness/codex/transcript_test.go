package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

type transcriptState struct {
	Conversation transcript.Conversation
	Recognized   bool
	Captures     []transcriptCapture
	Metadata     []transcript.Conversation
}

func (state *transcriptState) omitToolBodies() {
	for index := range state.Captures {
		if state.Captures[index].Role == "tool" {
			state.Captures[index].Body = ""
		}
	}
}

func replayTranscript(
	t *testing.T,
	projection string,
	includeTools bool, fast bool,
	lines []string,
) transcriptState {
	t.Helper()

	var result transcriptState

	decoder := &transcript.Decoder{
		Conversation: &result.Conversation,
		Recognized:   &result.Recognized,
		Projection:   projection,
		IncludeTools: includeTools,
		Emit: func(_ context.Context, role, body, id string, line int, at time.Time) {
			result.Captures = append(result.Captures, transcriptCapture{role, body, id, line, at})
		},
	}

	reader := codexHarness{}.Transcript()
	for index, line := range lines {
		if !fast || !reader.FastRecord(t.Context(), decoder, []byte(line), index+1) {
			var record transcript.Record
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				t.Fatal(err)
			}

			reader.Record(t.Context(), decoder, record, index+1)
		}

		result.Metadata = append(result.Metadata, result.Conversation)
	}

	return result
}

func readTranscript(t *testing.T, includeTools bool, lines ...string) transcript.Conversation {
	t.Helper()

	var (
		conversation transcript.Conversation
		recognized   bool
	)

	decoder := &transcript.Decoder{
		Conversation: &conversation,
		Recognized:   &recognized,
		IncludeTools: includeTools,
	}

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

func TestTranscriptProjectionAcrossLongRecords(t *testing.T) {
	t.Parallel()

	nativeMessage := func(text string) string {
		return `{"type":"event_msg","payload":{"type":"item_completed",` +
			`"item":{"type":"UserMessage","content":[{"type":"text",` +
			`"text":"` + text + `"}]}}}` + "\n"
	}
	longText := strings.Repeat("x", 12<<10)

	oversized := strings.Repeat("x", transcript.MaxRecordBytes)
	for _, test := range []struct {
		name, body string
		want       string
	}{
		{"empty transcript", "", ""},
		{"native event without newline", strings.TrimSuffix(nativeMessage("typed"), "\n"), typedUserProjection},
		{"response user only", `{"type":"response_item","payload":{"type":"message",` +
			`"role":"user","content":"legacy"}}`, ""},
		{"incomplete user event", `{"type":"event_msg","payload":{"type":"item_started",` +
			`"item":{"type":"UserMessage"}}}`, ""},
		{"assistant event", `{"type":"event_msg","payload":{"type":"item_completed",` +
			`"item":{"type":"AgentMessage"}}}`, ""},
		{"long ignored event", `{"type":"event_msg","payload":{"type":"token_count",` +
			`"padding":"` + longText + `"}}` + "\n" + nativeMessage("typed"), typedUserProjection},
		{"long native event", nativeMessage(longText), typedUserProjection},
		{"oversized ignored event", `{"type":"event_msg","payload":{"type":"token_count",` +
			`"padding":"` + oversized + `"}}` + "\n" + nativeMessage("typed"), typedUserProjection},
		{"oversized native event", nativeMessage(oversized), ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rollout.jsonl")
			if err := os.WriteFile(path, []byte(test.body), 0o600); err != nil {
				t.Fatal(err)
			}

			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() {
				if err := file.Close(); err != nil {
					t.Error(err)
				}
			})

			projection, err := (codexHarness{}).TranscriptProjection(
				t.Context(),
				file,
				int64(len(test.body)),
			)
			if err != nil || projection != test.want {
				t.Fatalf("projection=%q want=%q err=%v", projection, test.want, err)
			}
		})
	}
}

func TestFastTranscriptPreservesConversationAndCaptures(t *testing.T) {
	t.Parallel()

	lines := []string{
		`{"type":"session_meta","payload":{"id":"native","cwd":"/` +
			`work","timestamp":"2026-09-01T10:00:00Z",` +
			`"git":{"branch":"main"}}}`,
		`{"type":"response_item","timestamp":"2026-09-01T10:01:00Z",` +
			`"payload":{"type":"message","id":"u","role":"user",` +
			`"content":[{"type":"input_text","text":"hello"}]}}`,
		`{"type":"response_item","timestamp":1788256920000,` +
			`"payload":{"type":"message","id":"a","role":"assistant",` +
			`"model":"not a turn model","content":[{"type":"output_text",` +
			`"text":"answer"}]}}`,
		`{"type":"response_item","payload":{"type":"message",` +
			`"role":"assistant","channel":"analysis",` +
			`"content":[{"type":"output_text","text":"not indexed"}]}}`,
		`{"type":"response_item","timestamp":"2026-09-01T10:03:00Z",` +
			`"payload":{"type":"function_call","name":"Read",` +
			`"arguments":"{}","call_id":"t"}}`,
		`{"type":"response_item","timestamp":"2026-09-01T10:04:00Z",` +
			`"payload":{"type":"function_call_output","output":"output",` +
			`"call_id":"t"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"Edit","input":"patch","call_id":"c"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call_` +
			`output","output":[{"type":"text","text":"nested"}],` +
			`"call_id":"c"}}`,
		`{"type":"response_item","payload":{"type":"message",` +
			`"role":"user","content":[{"type":"future"},` +
			`{"type":"input_text","text":"still indexed"}]}}`,
		`{"type":"session_meta","payload":{"id":"native","cwd":"/new","git":{"branch":null}}}`,
		`{"type":"response_item","payload":{"type":"message","role":7,"content":"wrong shape"}}`,
		`{"type":"response_item","payload":{"type":"message",` +
			`"role":"assistant","content":"old"},` +
			`"payload":{"type":"message","role":"user","content":"last ` +
			`duplicate"}}`,
		`{"type":"session_meta","payload":{"id":"old","id":null,"cwd":"/new"}}`,
		`{"type":"response_item","payload":{"type":"message","Role":"user","content":"case-sensitive role"}}`,
		`{"type":"event_msg","timestamp":"2026-09-01T10:05:00Z",` +
			`"payload":{"type":"item_completed",` +
			`"item":{"type":"UserMessage","id":"typed",` +
			`"content":[{"type":"text","text":"<literal>typed input</` +
			`literal>"}]}}}`,
		`{"type":"response_item","timestamp":1788257160,` +
			`"payload":{"type":"message","id":"echo","role":"user",` +
			`"content":"<environment_context>work</` +
			`environment_context>\ntyped input"}}`,
		`{"type":"response_item","payload":{"type":"message",` +
			`"role":"user","content":"echo",` +
			`"tool_calls":[{"id":"embedded"}]}}`,
		`{"type":"session_meta","payload":{"id":"native","cwd":"/` +
			`work","timestamp":1788256800000,"git":{"branch":"next"}}}`,
		`{"type":"session_meta","payload":{"id":"native","cwd":"/work","timestamp":"bad","git":{"branch":7}}}`,
		`{"type":"session_meta","payload":{"id":"native","cwd":"/work","timestamp":null,"git":null}}`,
	}

	for _, projection := range []string{"", typedUserProjection} {
		for _, includeTools := range []bool{false, true} {
			ordinary := replayTranscript(t, projection, includeTools, false, lines)

			fast := replayTranscript(t, projection, includeTools, true, lines)
			if !includeTools {
				ordinary.omitToolBodies()
				fast.omitToolBodies()
			}

			if diff := cmp.Diff(ordinary, fast); diff != "" {
				t.Fatalf(
					"projection=%q includeTools=%t (-ordinary +fast):\n%s",
					projection,
					includeTools,
					diff,
				)
			}
		}
	}
}

func TestTranscriptUserProjectionCaptures(t *testing.T) {
	t.Parallel()

	lines := []string{
		`{"type":"session_meta","payload":{"id":"native","cwd":"/` +
			`work","timestamp":"2026-09-01T10:00:00Z",` +
			`"git":{"branch":"main"}}}`,
		`{"type":"event_msg","timestamp":"2026-09-01T10:01:00Z",` +
			`"payload":{"type":"item_completed",` +
			`"item":{"type":"UserMessage","id":"typed",` +
			`"content":[{"type":"text","text":"<literal>typed input</` +
			`literal>"},{"type":"tool_result","content":"not user ` +
			`text"}]}}}`,
		`{"type":"response_item","timestamp":1788256920000,` +
			`"payload":{"type":"message","id":"echo","role":"user",` +
			`"content":[{"type":"input_text",` +
			`"text":"<environment_context>work</` +
			`environment_context>\ntyped input"}]}}`,
		`{"type":"response_item","timestamp":"2026-09-01T10:03:00Z",` +
			`"payload":{"type":"message","id":"analysis","role":"user",` +
			`"channel":"analysis","content":"not indexed"}}`,
		`{"type":"response_item","timestamp":1788257040,` +
			`"payload":{"type":"message","id":"answer",` +
			`"role":"assistant","content":"answer"}}`,
		`{"type":"event_msg","payload":{"type":"item_started",` +
			`"item":{"type":"UserMessage","content":"not completed"}}}`,
		`{"type":"event_msg","payload":{"type":"item_completed",` +
			`"item":{"type":"AgentMessage","content":"not a user"}}}`,
	}

	for _, test := range []struct {
		name, projection string
		want             []transcriptCapture
	}{
		{"legacy", "", []transcriptCapture{
			{
				"user",
				"<environment_context>work</environment_context>\ntyped input",
				"echo",
				3,
				time.Date(
					2026,
					9,
					1,
					10,
					2,
					0,
					0,
					time.UTC,
				),
			},
			{"assistant", "answer", "answer", 5, time.Date(2026, 9, 1, 10, 4, 0, 0, time.UTC)},
		}},
		{"typed users", typedUserProjection, []transcriptCapture{
			{
				transcript.TypedUserRole,
				"<literal>typed input</literal>",
				"typed",
				2,
				time.Date(
					2026,
					9,
					1,
					10,
					1,
					0,
					0,
					time.UTC,
				),
			},
			{
				transcript.InjectedUserRole,
				"<environment_context>work</environment_context>\ntyped input",
				"echo",
				3,
				time.Date(
					2026,
					9,
					1,
					10,
					2,
					0,
					0,
					time.UTC,
				),
			},
			{"assistant", "answer", "answer", 5, time.Date(2026, 9, 1, 10, 4, 0, 0, time.UTC)},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, includeTools := range []bool{false, true} {
				result := replayTranscript(t, test.projection, includeTools, true, lines)
				if diff := cmp.Diff(test.want, result.Captures); diff != "" {
					t.Fatalf("includeTools=%t captures (-want +got):\n%s", includeTools, diff)
				}

				wantConversation := transcript.Conversation{
					SessionID: "native",
					CWD:       "/work",
					GitBranch: "main",
					CreatedAt: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC),
				}
				if diff := cmp.Diff(
					wantConversation,
					result.Conversation,
				); !result.Recognized ||
					diff != "" {
					t.Fatalf(
						"includeTools=%t recognized=%t conversation (-want +got):\n%s",
						includeTools,
						result.Recognized,
						diff,
					)
				}
			}
		})
	}
}

func TestFastTranscriptIgnoredResponseItemsKeepMetadata(t *testing.T) {
	t.Parallel()

	for _, includeTools := range []bool{false, true} {
		conversation := transcript.Conversation{SessionID: "native", CWD: "/work", Model: "model-a"}
		recognized := true
		decoder := &transcript.Decoder{
			Conversation: &conversation,
			Recognized:   &recognized,
			IncludeTools: includeTools,
			Emit: func(context.Context, string, string, string, int, time.Time) {
				t.Error("ignored response item emitted text or timestamps")
			},
		}

		for _, kind := range []string{
			"reasoning",
			"agent_message",
			"ghost_snapshot",
			"image_generation_call",
			"tool_search_call",
			"tool_search_output",
			"web_search_call",
		} {
			line := `{"type":"response_item","timestamp":"2026-09-02T00:00:00Z",` +
				`"payload":{"type":"` + kind + `","summary":[{"type":"summary_text","text":"internal text"}]}}`
			if !readFastRecord(t.Context(), decoder, []byte(line), 1) {
				t.Fatalf("includeTools=%t: ignored %s item reached full decode", includeTools, kind)
			}
		}

		for _, line := range []string{
			`{"type":"response_item","payload":{"type":"reasoning","summary":`,
			`{"type":"response_item","payload":{"type":"future","content":"new text"}}`,
			`{"type":"response_item","payload":{"type":"reasoning","role":"user","content":"future message shape"}}`,
		} {
			if readFastRecord(t.Context(), decoder, []byte(line), 1) {
				t.Fatalf("handled malformed or unrecognized response item: %s", line)
			}
		}

		if !recognized ||
			conversation != (transcript.Conversation{SessionID: "native", CWD: "/work", Model: "model-a"}) {
			t.Fatalf(
				"ignored response changed metadata: recognized=%t conversation=%+v",
				recognized,
				conversation,
			)
		}
	}
}

func TestTranscriptBranchAndModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		lines []string
		want  transcript.Conversation
	}{
		{
			"branch from session metadata",
			[]string{
				`{"type":"session_meta","payload":{"id":"native","cwd":"/` +
					`work","git":{"branch":"main","commit_hash":"abc"}}}`,
			},
			transcript.Conversation{SessionID: "native", CWD: "/work", GitBranch: "main"},
		},
		{"session without git", []string{
			`{"type":"session_meta","payload":{"id":"native","cwd":"/work"}}`,
		}, transcript.Conversation{SessionID: "native", CWD: "/work"}},
		{"repeated metadata keeps known branch", []string{
			`{"type":"session_meta","payload":{"id":"native","git":{"branch":"main"}}}`,
			`{"type":"session_meta","payload":{"id":"native","git":{"commit_hash":"abc"}}}`,
		}, transcript.Conversation{SessionID: "native", GitBranch: "main"}},
		{
			"latest turn model wins",
			[]string{
				`{"type":"session_meta","payload":{"id":"native"}}`,
				`{"type":"turn_context","payload":{"cwd":"/work",` +
					`"model":"model-a","collaboration_mode":{"settings":{"model":` +
					`"nested"}}}}`,
				`{"type":"turn_context","payload":{"cwd":"/work","model":"model-b"}}`,
			},
			transcript.Conversation{SessionID: "native", Model: "model-b"},
		},
		{"turn without model keeps previous", []string{
			`{"type":"session_meta","payload":{"id":"native"}}`,
			`{"type":"turn_context","payload":{"model":"model-a"}}`,
			`{"type":"turn_context","payload":{"cwd":"/work"}}`,
		}, transcript.Conversation{SessionID: "native", Model: "model-a"}},
		{"model before metadata", []string{
			`{"type":"turn_context","payload":{"model":"model-a"}}`,
			`{"type":"session_meta","payload":{"id":"native"}}`,
		}, transcript.Conversation{SessionID: "native", Model: "model-a"}},
		{
			"non-turn records carry no model",
			[]string{
				`{"type":"session_meta","payload":{"id":"native"}}`,
				`{"type":"event_msg","payload":{"type":"token_count","model":"event-model"}}`,
				`{"type":"response_item","payload":{"type":"message",` +
					`"role":"assistant","model":"item-model",` +
					`"content":[{"type":"output_text","text":"hi"}]}}`,
			},
			transcript.Conversation{SessionID: "native"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, includeTools := range []bool{false, true} {
				if diff := cmp.Diff(
					tt.want,
					readTranscript(t, includeTools, tt.lines...),
				); diff != "" {
					t.Fatalf(
						"includeTools=%t conversation mismatch (-want +got):\n%s",
						includeTools,
						diff,
					)
				}
			}
		})
	}
}
