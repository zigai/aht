package codex

import (
	"context"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func readTranscriptRecord(ctx context.Context, t *transcript.Decoder, r transcript.Record, line int) {
	payload := transcript.Obj(r, "payload")
	switch transcript.Str(r, "type") {
	case "session_meta":
		*t.Recognized = true
		t.Conversation.SessionID = transcript.Str(payload, "id")
		t.Conversation.CWD = transcript.Str(payload, "cwd")
		t.Conversation.CreatedAt = transcript.ParseTime(payload["timestamp"])
	case "response_item":
		timestamp := transcript.ParseTime(r["timestamp"])
		switch transcript.Str(payload, "type") {
		case "message":
			if transcript.Str(payload, "channel") != "analysis" {
				t.Message(ctx, payload, transcript.Str(payload, "id"), line, timestamp)
			}
		case "function_call", "custom_tool_call":
			t.Capture(ctx, "tool", transcript.Str(payload, "name")+" "+transcript.FirstString(payload, "arguments", "input"), transcript.Str(payload, "call_id"), line, timestamp)
		case "function_call_output", "custom_tool_call_output":
			t.Capture(ctx, "tool", transcript.ContentText(payload["output"]), transcript.Str(payload, "call_id"), line, timestamp)
		}
	}
}

func (codexHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"rollout-*.jsonl"}, Sources: transcriptSources, SkipDirectory: nil, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: readTranscriptRecord, Document: nil, Query: nil}
}

func transcriptSources(home string) []string {
	return []string{filepath.Join(transcript.EnvPath("CODEX_HOME", filepath.Join(home, ".codex")), "sessions"), filepath.Join(transcript.EnvPath("CODEX_HOME", filepath.Join(home, ".codex")), "archived_sessions")}
}
