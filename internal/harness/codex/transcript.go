package codex

import (
	"context"
	"encoding/json"
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
		if branch := transcript.Str(transcript.Obj(payload, "git"), "branch"); branch != "" {
			t.Conversation.GitBranch = branch
		}
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

// readFastRecord decodes only the envelope of records the search reader ignores
// (and the model of turn contexts) or, for excluded tool calls, the metadata
// that Capture would update. Hints only select candidates; each handled record
// is validated as complete JSON.
func readFastRecord(ctx context.Context, t *transcript.Decoder, data []byte, line int) bool {
	if !fastCandidate(data, t.IncludeTools) {
		return false
	}
	var entry struct {
		Type      string          `json:"type"`
		Timestamp json.RawMessage `json:"timestamp"`
		Payload   json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(data, &entry) != nil {
		return false
	}
	switch entry.Type {
	case "event_msg", "compacted":
		return true
	case "turn_context":
		var turn struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(entry.Payload, &turn) == nil && turn.Model != "" {
			t.Conversation.Model = turn.Model
		}
		return true
	case "response_item":
		return !t.IncludeTools && fastResponseItem(ctx, t, entry.Payload, entry.Timestamp, line)
	}
	return false
}

func fastCandidate(data []byte, includeTools bool) bool {
	ignored := transcript.HasRecordHint(data, []byte(`"type":"event_msg"`)) ||
		transcript.HasRecordHint(data, []byte(`"type":"turn_context"`)) ||
		transcript.HasRecordHint(data, []byte(`"type":"compacted"`))
	if ignored || includeTools {
		return ignored
	}
	return transcript.HasRecordHint(data, []byte(`"type":"function_call"`)) ||
		transcript.HasRecordHint(data, []byte(`"type":"custom_tool_call"`)) ||
		transcript.HasRecordHint(data, []byte(`"type":"function_call_output"`)) ||
		transcript.HasRecordHint(data, []byte(`"type":"custom_tool_call_output"`)) ||
		transcript.HasRecordHint(data, []byte(`"channel":"analysis"`))
}

func fastResponseItem(ctx context.Context, t *transcript.Decoder, raw, timestamp json.RawMessage, line int) bool {
	var payload struct {
		Type    string `json:"type"`
		Channel string `json:"channel"`
		CallID  string `json:"call_id"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return false
	}
	switch payload.Type {
	case "message":
		return payload.Channel == "analysis"
	case "function_call", "custom_tool_call", "function_call_output", "custom_tool_call_output":
		t.Capture(ctx, "tool", "", payload.CallID, line, transcript.ParseTime(timestamp))
		return true
	}
	return false
}

func (codexHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"rollout-*.jsonl"}, Sources: transcriptSources, SkipDirectory: nil, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: readTranscriptRecord, FastRecord: readFastRecord, Document: nil, Query: nil, LocalTitles: true, Parent: nil}
}

func transcriptSources(home string) ([]string, error) {
	return []string{filepath.Join(transcript.EnvPath("CODEX_HOME", filepath.Join(home, ".codex")), "sessions"), filepath.Join(transcript.EnvPath("CODEX_HOME", filepath.Join(home, ".codex")), "archived_sessions")}, nil
}
