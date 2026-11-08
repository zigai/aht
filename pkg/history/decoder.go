package history

import (
	"context"
	"time"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	native "github.com/zigai/aht/v2/internal/harness/transcript"
)

func (s *parser) decoder(source Source, t *transcript, buffer *fileIssueBuffer) *native.Decoder {
	return &native.Decoder{
		Conversation: &t.match.Conversation,
		Recognized:   &t.recognized,
		Emit: func(_ context.Context, role, body, id string, line int, at time.Time) {
			s.capture(t, role, body, id, line, at)
		},
		Issue:        func(path string, err error) { buffer.issue(source, path, err) },
		Metadata:     s.sourceMetadata,
		Projection:   t.projection,
		IncludeTools: s.includeTools,
		UserParts:    catalog.TranscriptFor(source.Harness).UserParts,
	}
}
