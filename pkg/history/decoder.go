package history

import (
	"context"
	"time"

	native "github.com/zigai/aht/v2/internal/harness/transcript"
)

func (s *search) decoder(source Source, t *transcript, buffer *fileIssueBuffer) *native.Decoder {
	return &native.Decoder{
		Conversation: &t.match.Conversation,
		Recognized:   &t.recognized,
		Emit: func(_ context.Context, role, body, id string, line int, at time.Time) {
			s.capture(t, role, body, id, line, at)
		},
		Issue:        func(path string, err error) { buffer.issue(source, path, err) },
		Metadata:     s.sourceMetadata,
		Projection:   t.projection,
		IncludeTools: s.query.IncludeTools,
	}
}
