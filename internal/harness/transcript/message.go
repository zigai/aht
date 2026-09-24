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
