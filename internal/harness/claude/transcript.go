package claude

import (
	"cmp"
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

//nolint:tagliatelle // Claude Code's native transcript fields use camelCase.
type transcriptEntry struct {
	Type             string          `json:"type"`
	SessionID        string          `json:"sessionId"`
	CWD              string          `json:"cwd"`
	GitBranch        string          `json:"gitBranch"`
	Timestamp        json.RawMessage `json:"timestamp"`
	UUID             string          `json:"uuid"`
	CustomTitle      *string         `json:"customTitle"`
	AITitle          *string         `json:"aiTitle"`
	Message          json.RawMessage `json:"message"`
	Content          json.RawMessage `json:"content"`
	Text             json.RawMessage `json:"text"`
	IsMeta           bool            `json:"isMeta"`
	IsCompactSummary bool            `json:"isCompactSummary"`
}

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
		readMessage(ctx, t, r, line)
	}
}

func readMessage(ctx context.Context, t *transcript.Decoder, r transcript.Record, line int) {
	message := transcript.Obj(r, "message")
	t.CaptureModel(message)
	if message != nil && (string(r["isMeta"]) == "true" || string(r["isCompactSummary"]) == "true") {
		message["role"] = json.RawMessage(`"context"`)
	}
	t.Message(ctx, message, transcript.Str(r, "uuid"), line, transcript.ParseTime(r["timestamp"]))
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

func readFastRecord(ctx context.Context, decoder *transcript.Decoder, data []byte, line int) bool {
	var entry transcriptEntry
	if jsonv2.Unmarshal(data, &entry) != nil {
		return false
	}
	switch entry.Type {
	case "progress", "file-history-snapshot", "queue-operation", "system":
		return true
	case "attachment", "atis-latch", "last-prompt", "file-history-delta", "cost-state", "pr-link":
		return len(entry.Message) == 0 && len(entry.Content) == 0 && len(entry.Text) == 0
	case "custom-title", "ai-title":
		entry.readTitle(decoder.Conversation)
		return true
	case "mode", "permission-mode":
		if entry.SessionID != "" {
			*decoder.Recognized = true
			decoder.Conversation.SessionID = entry.SessionID
		}
		return true
	case "user", "assistant":
		return entry.readMessage(ctx, decoder, line)
	}
	return false
}

func (entry transcriptEntry) readTitle(conversation *transcript.Conversation) {
	if entry.Type == "custom-title" && entry.CustomTitle != nil {
		conversation.CustomTitle = *entry.CustomTitle
		conversation.Title = cmp.Or(conversation.CustomTitle, conversation.AITitle)
	}
	if entry.Type == "ai-title" && entry.AITitle != nil {
		conversation.AITitle = *entry.AITitle
		conversation.Title = cmp.Or(conversation.CustomTitle, conversation.AITitle)
	}
}

func (entry transcriptEntry) readMessage(ctx context.Context, decoder *transcript.Decoder, line int) bool {
	var message transcript.Message
	if jsonv2.Unmarshal(entry.Message, &message) != nil {
		return false
	}
	if entry.SessionID != "" {
		decoder.Conversation.SessionID = entry.SessionID
	}
	if entry.CWD != "" {
		decoder.Conversation.CWD = entry.CWD
	}
	if entry.GitBranch != "" {
		decoder.Conversation.GitBranch = entry.GitBranch
	}
	*decoder.Recognized = true
	message.CaptureModel(decoder)
	if entry.IsMeta || entry.IsCompactSummary {
		message.Role = "context"
	}
	if !decoder.IncludeTools && message.Role == "user" && len(message.ToolCalls) == 0 && captureToolResults(ctx, decoder, message.Content, entry.UUID, line, entry.Timestamp) {
		return true
	}
	message.Capture(ctx, decoder, entry.UUID, line, transcript.ParseTime(entry.Timestamp))
	return true
}

func captureToolResults(ctx context.Context, decoder *transcript.Decoder, content json.RawMessage, id string, line int, timestamp json.RawMessage) bool {
	var blocks []struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
	}
	if len(content) == 0 || jsonv2.Unmarshal(content, &blocks) != nil || len(blocks) == 0 {
		return false
	}
	hasText := false
	for _, block := range blocks {
		if block.Type != "tool_result" {
			return false
		}
		if len(block.Content) > 0 && block.Content[0] == '"' {
			hasText = hasText || len(block.Content) > len(`""`)
		} else {
			hasText = hasText || transcript.ContentText(block.Content) != ""
		}
	}
	if hasText {
		decoder.Capture(ctx, "tool", "", id, line, transcript.ParseTime(timestamp))
	}
	return true
}

func (claudeHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"*.jsonl"}, Sources: transcriptSources, SkipDirectory: nil, SkipFile: nil, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: readTranscriptRecord, FastRecord: readFastRecord, Document: nil, Query: nil, LocalTitles: false, RewritableHeaders: nil, Parent: subagentParent}
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
