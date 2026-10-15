package transcript

import (
	"bytes"
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
)

const fastRecordHintBytes = 512

type treeEnvelope struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	CWD       string          `json:"cwd"`
	Title     string          `json:"title"`
	Name      string          `json:"name"`
	Timestamp json.RawMessage `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
	Content   json.RawMessage `json:"content"`
}

// FastTreeRecord decodes recognized tree entries into typed fields, retaining
// only metadata for tool results that the query excludes.
func FastTreeRecord(ctx context.Context, decoder *Decoder, data []byte, line int) bool {
	var entry treeEnvelope
	if jsonv2.Unmarshal(data, &entry) != nil {
		return false
	}
	switch entry.Type {
	case "compaction", "branch_summary", "context_edit", "custom", "label", "model_change", "thinking_level_change", "usage":
		return true
	case "mode_change", "credential_pin", "session_init", "ttsr_injection", "model_usage", "service_tier_change":
		return len(entry.Message) == 0
	case "session":
		entry.captureSession(decoder)
	case "session_info":
		decoder.Conversation.Title = entry.Name
	case "title", "title_change":
		if entry.Title != "" {
			decoder.Conversation.Title = entry.Title
		}
	case "custom_message":
		for _, part := range MessageParts(entry.Content, "context", false) {
			decoder.Capture(ctx, part.Role, part.Text, entry.ID, line, ParseTime(entry.Timestamp))
		}
	case "message":
		return readTreeMessage(ctx, decoder, entry.Message, entry.ID, line, entry.Timestamp)
	default:
		return false
	}
	return true
}

// HasRecordHint selects a likely record shape from its bounded prefix. Callers
// must decode and verify the top-level fields before handling the candidate.
func HasRecordHint(data, hint []byte) bool {
	return bytes.Contains(data[:min(len(data), fastRecordHintBytes)], hint)
}

func (entry treeEnvelope) captureSession(decoder *Decoder) {
	*decoder.Recognized = true
	decoder.Conversation.SessionID = entry.ID
	decoder.Conversation.CWD = entry.CWD
	if at := ParseTime(entry.Timestamp); !at.IsZero() {
		decoder.Conversation.CreatedAt = at
	}
	if entry.Title != "" {
		decoder.Conversation.Title = entry.Title
	}
}

func readTreeMessage(ctx context.Context, decoder *Decoder, raw json.RawMessage, id string, line int, timestamp json.RawMessage) bool {
	var message Message
	if jsonv2.Unmarshal(raw, &message) != nil {
		return false
	}
	if message.Role == "custom" {
		message.Role = "context"
	}
	if !decoder.IncludeTools && message.Role == "toolResult" && len(message.ToolCalls) == 0 {
		if hasText, supported := toolResultContent(message.Content); supported {
			if hasText {
				decoder.Capture(ctx, "tool", "", id, line, ParseTime(timestamp))
			}
			return true
		}
	}
	message.CaptureModel(decoder)
	message.Capture(ctx, decoder, id, line, ParseTime(timestamp))
	return true
}

func toolResultContent(content json.RawMessage) (bool, bool) {
	if bytes.Equal(content, []byte("null")) || len(content) > 0 && content[0] == '"' {
		return true, true
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if len(content) != 0 && jsonv2.Unmarshal(content, &blocks) != nil {
		return false, false
	}
	hasText := false
	for _, block := range blocks {
		switch block.Type {
		case "text", "input_text", "output_text":
			hasText = true
		case "image":
		default:
			return false, false
		}
	}
	return hasText, true
}
