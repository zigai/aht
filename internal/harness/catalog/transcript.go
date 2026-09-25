package catalog

import (
	"github.com/zigai/aht/v2/internal/harness/transcript"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TranscriptFor(id registry.Harness) transcript.Reader {
	if adapter, ok := Find(id); ok {
		if reader, ok := adapter.(interface{ Transcript() transcript.Reader }); ok {
			return reader.Transcript()
		}
	}
	return transcript.Reader{Patterns: nil, Sources: nil, SkipDirectory: nil, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: nil, Document: nil, Query: nil}
}
