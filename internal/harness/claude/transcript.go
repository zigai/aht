package claude

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func readTranscriptRecord(ctx context.Context, t *transcript.Decoder, r transcript.Record, line int) {
	kind := transcript.Str(r, "type")
	if kind == "custom-title" {
		_, title, _ := titleRecord(r)
		t.Conversation.Title = title
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

func titleRecord(r transcript.Record) (string, string, bool) {
	kind := transcript.Str(r, "type")
	var field string
	switch kind {
	case "custom-title":
		field = "customTitle"
	case "ai-title":
		field = "aiTitle"
	default:
		return "", "", false
	}
	var title string
	if raw := r[field]; len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &title) != nil {
		return "", "", false
	}
	return kind, title, true
}

// Claude has no published JSONL schema. Only known records already ignored by
// readTranscriptRecord use this path; unrecognized shapes keep the full reader.
func readFastRecord(_ context.Context, _ *transcript.Decoder, data []byte, _ int) bool {
	if !transcript.HasRecordHint(data, []byte(`"type":"progress"`)) &&
		!transcript.HasRecordHint(data, []byte(`"type":"file-history-snapshot"`)) &&
		!transcript.HasRecordHint(data, []byte(`"type":"queue-operation"`)) &&
		!transcript.HasRecordHint(data, []byte(`"type":"system"`)) {
		return false
	}
	var entry struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(data, &entry) != nil {
		return false
	}
	switch entry.Type {
	case "progress", "file-history-snapshot", "queue-operation", "system":
		return true
	}
	return false
}

func (claudeHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"*.jsonl"}, Sources: transcriptSources, SkipDirectory: nil, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: readTranscriptRecord, FastRecord: readFastRecord, Document: nil, Query: nil}
}

func transcriptSources(home string) []string {
	return []string{filepath.Join(transcript.EnvPath("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude")), "projects")}
}
