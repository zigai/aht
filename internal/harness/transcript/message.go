package transcript

import (
	"context"
	"encoding/json"
	"time"
)

type Message struct {
	Role      string          `json:"role"`
	Model     string          `json:"model"`
	Content   json.RawMessage `json:"content"`
	ToolCalls json.RawMessage `json:"tool_calls"`
}

func (message Message) Capture(
	ctx context.Context,
	decoder *Decoder,
	id string,
	line int,
	timestamp time.Time,
) {
	role, ok := RecognizedRole(message.Role)
	if !ok {
		return
	}

	for _, part := range MessageParts(message.Content, role, true) {
		decoder.Capture(ctx, part.Role, part.Text, id, line, timestamp)
	}

	if len(message.ToolCalls) > 0 {
		decoder.Capture(ctx, "tool", string(message.ToolCalls), id, line, timestamp)
	}
}

func (message Message) CaptureModel(decoder *Decoder) {
	if message.Role == "assistant" && message.Model != "" && message.Model != "<synthetic>" {
		decoder.Conversation.Model = message.Model
	}
}

func (t *Decoder) Message(ctx context.Context, r Record, id string, line int, timestamp time.Time) {
	message := Message{
		Role:      Str(r, "role"),
		Model:     "",
		Content:   r["content"],
		ToolCalls: r["tool_calls"],
	}
	message.Capture(ctx, t, id, line, timestamp)
}

// CaptureModel records the model of an assistant message. Placeholder
// models that native tools write for locally generated replies are ignored.
func (t *Decoder) CaptureModel(message Record) {
	entry := Message{
		Role:      Str(message, "role"),
		Model:     Str(message, "model"),
		Content:   nil,
		ToolCalls: nil,
	}
	entry.CaptureModel(t)
}
