package claude

import (
	"cmp"
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func readTranscriptRecord(ctx context.Context, t *transcript.Decoder, r transcript.Record, line int) {
	c := t.Conversation
	switch transcript.Str(r, "type") {
	case "custom-title":
		if _, title, ok := titleRecord(r); ok {
			c.CustomTitle = title
			c.Title = cmp.Or(c.CustomTitle, c.AITitle)
		}
	case "ai-title":
		if _, title, ok := titleRecord(r); ok {
			c.AITitle = title
			c.Title = cmp.Or(c.CustomTitle, c.AITitle)
		}
	case "mode", "permission-mode":
		if id := transcript.Str(r, "sessionId"); id != "" {
			*t.Recognized = true
			c.SessionID = id
		}
	case "user", "assistant":
		*t.Recognized = true
		if id := transcript.Str(r, "sessionId"); id != "" {
			c.SessionID = id
		}
		if cwd := transcript.Str(r, "cwd"); cwd != "" {
			c.CWD = cwd
		}
		if branch := transcript.Str(r, "gitBranch"); branch != "" {
			c.GitBranch = branch
		}
		message := transcript.Obj(r, "message")
		t.CaptureModel(message)
		t.Message(ctx, message, transcript.Str(r, "uuid"), line, transcript.ParseTime(r["timestamp"]))
	}
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
	return transcript.Reader{Patterns: []string{"*.jsonl"}, Sources: transcriptSources, SkipDirectory: nil, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: readTranscriptRecord, FastRecord: readFastRecord, Document: nil, Query: nil, LocalTitles: false, Parent: subagentParent}
}

func transcriptSources(home string) ([]string, error) {
	return []string{filepath.Join(transcript.EnvPath("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude")), "projects")}, nil
}

// subagentParent maps <session>/subagents/agent-<id>.jsonl, which Claude Code
// writes for each subagent of a session, to the session's <session>.jsonl.
func subagentParent(path string) string {
	dir := filepath.Dir(path)
	if filepath.Base(dir) != "subagents" {
		return ""
	}
	return filepath.Dir(dir) + ".jsonl"
}
