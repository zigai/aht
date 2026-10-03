package transcript

import (
	"bytes"
	"context"
	"encoding/json"
)

const fastRecordHintBytes = 512

// FastTreeRecord handles entries the tree transcript reader never indexes and
// tool results that the query explicitly excludes. Candidate records are
// validated as complete JSON; invalid records still reach the ordinary reader.
func FastTreeRecord(ctx context.Context, t *Decoder, data []byte, line int) bool {
	if fastIgnoredTreeEntry(data) {
		return true
	}
	if t.IncludeTools || !HasRecordHint(data, []byte(`"role":"toolResult"`)) {
		return false
	}
	return fastTreeToolResult(ctx, t, data, line)
}

// HasRecordHint selects a likely record shape from its bounded prefix. Callers
// must decode and verify the top-level fields before handling the candidate.
func HasRecordHint(data, hint []byte) bool {
	return bytes.Contains(data[:min(len(data), fastRecordHintBytes)], hint)
}

func fastIgnoredTreeEntry(data []byte) bool {
	if !typeHint(data, "compaction", "branch_summary", "context_edit", "custom", "custom_message", "label", "model_change", "thinking_level_change", "usage") {
		return false
	}
	var entry struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(data, &entry) != nil {
		return false
	}
	switch entry.Type {
	case "compaction", "branch_summary", "context_edit", "custom", "custom_message", "label", "model_change", "thinking_level_change", "usage":
		return true
	}
	return false
}

func fastTreeToolResult(ctx context.Context, t *Decoder, data []byte, line int) bool {
	var entry struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		Timestamp json.RawMessage `json:"timestamp"`
		Message   json.RawMessage `json:"message"`
	}
	if json.Unmarshal(data, &entry) != nil || entry.Type != "message" {
		return false
	}
	var message struct {
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		ToolCalls json.RawMessage `json:"tool_calls"`
	}
	if json.Unmarshal(entry.Message, &message) != nil || message.Role != "toolResult" || len(message.ToolCalls) != 0 {
		return false
	}
	hasText, supported := toolResultContent(message.Content)
	if !supported {
		return false
	}
	if hasText {
		t.Capture(ctx, "tool", "", entry.ID, line, ParseTime(entry.Timestamp))
	}
	return true
}

// MessageParts captures a JSON string or null even if its decoded text is
// empty. For arrays, only known text and image blocks can be classified here.
func toolResultContent(content json.RawMessage) (bool, bool) {
	if bytes.Equal(content, []byte("null")) || len(content) > 0 && content[0] == '"' {
		return true, true
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if len(content) != 0 && json.Unmarshal(content, &blocks) != nil {
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

func typeHint(data []byte, kinds ...string) bool {
	for _, kind := range kinds {
		if HasRecordHint(data, []byte(`"type":"`+kind+`"`)) {
			return true
		}
	}
	return false
}
