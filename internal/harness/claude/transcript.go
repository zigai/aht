package claude

import (
	"context"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func readTranscriptRecord(ctx context.Context, t *transcript.Decoder, r transcript.Record, line int) {
	kind := transcript.Str(r, "type")
	if kind == "custom-title" {
		t.Conversation.Title = transcript.Str(r, "customTitle")
		return
	}
	if kind != "user" && kind != "assistant" {
		return
	}
	*t.Recognized = true
	c := t.Conversation
	if id := transcript.Str(r, "sessionId"); id != "" {
		c.SessionID = id
	}
	if cwd := transcript.Str(r, "cwd"); cwd != "" {
		c.CWD = cwd
	}
	t.Message(ctx, transcript.Obj(r, "message"), transcript.Str(r, "uuid"), line, transcript.ParseTime(r["timestamp"]))
}

func (claudeHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"*.jsonl"}, Sources: transcriptSources, SkipDirectory: nil, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: readTranscriptRecord, Document: nil, Query: nil}
}

func transcriptSources(home string) []string {
	return []string{filepath.Join(transcript.EnvPath("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude")), "projects")}
}
