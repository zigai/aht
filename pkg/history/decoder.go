package history

import (
	"context"
	"time"

	native "github.com/zigai/aht/v2/internal/harness/transcript"
)

func (s *search) decoder(source Source, t *transcript) *native.Decoder {
	return &native.Decoder{Conversation: &t.match.Conversation, Recognized: &t.recognized, Emit: func(ctx context.Context, role, body, id string, line int, at time.Time) {
		s.capture(ctx, t, role, body, id, line, at)
	}, Issue: func(path string, err error) { s.issue(source, path, err) }, Metadata: s.sourceMetadata}
}
