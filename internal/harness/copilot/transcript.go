package copilot

import (
	"context"
	"path/filepath"

	"github.com/zigai/aht/internal/harness/transcript"
)

func readTranscriptRecord(ctx context.Context, t *transcript.Decoder, r transcript.Record, line int) {
	data := transcript.Obj(r, "data")
	timestamp := transcript.ParseTime(r["timestamp"])
	switch transcript.Str(r, "type") {
	case "session.start":
		*t.Recognized = true
		t.Conversation.SessionID = transcript.Str(data, "sessionId")
		t.Conversation.CWD = transcript.Str(transcript.Obj(data, "context"), "cwd")
		t.Conversation.ProjectRoot = transcript.Str(transcript.Obj(data, "context"), "gitRoot")
		t.Conversation.CreatedAt = transcript.ParseTime(data["startTime"])
	case "session.title_changed":
		t.Conversation.Title = transcript.Str(data, "title")
	case "user.message":
		t.Capture(ctx, "user", transcript.Str(data, "content"), transcript.Str(r, "id"), line, timestamp)
	case "assistant.message":
		t.Capture(ctx, "assistant", transcript.Str(data, "content"), transcript.Str(r, "id"), line, timestamp)
		t.Capture(ctx, "tool", string(data["toolRequests"]), transcript.Str(r, "id"), line, timestamp)
	case "tool.execution_complete":
		t.Capture(ctx, "tool", transcript.FirstString(transcript.Obj(data, "result"), "detailedContent", "content"), transcript.Str(r, "id"), line, timestamp)
	}
}

func (copilotHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"events.jsonl"}, Sources: transcriptSources, SkipDirectory: nil, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: readTranscriptRecord, Document: nil, Query: nil}
}

func transcriptSources(home string) []string {
	return []string{filepath.Join(home, ".copilot", "session-state")}
}
