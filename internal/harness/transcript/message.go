package transcript

import (
	"context"
	"time"
)

func (t *Decoder) Message(ctx context.Context, r Record, id string, line int, timestamp time.Time) {
	role, ok := RecognizedRole(Str(r, "role"))
	if !ok {
		return
	}
	for _, part := range MessageParts(r["content"], role, true) {
		t.Capture(ctx, part.Role, part.Text, id, line, timestamp)
	}
	if len(r["tool_calls"]) > 0 {
		t.Capture(ctx, "tool", string(r["tool_calls"]), id, line, timestamp)
	}
}

// CaptureModel records the model of an assistant message. Placeholder
// models that native tools write for locally generated replies are ignored.
func (t *Decoder) CaptureModel(message Record) {
	if Str(message, "role") != "assistant" {
		return
	}
	if model := Str(message, "model"); model != "" && model != "<synthetic>" {
		t.Conversation.Model = model
	}
}
