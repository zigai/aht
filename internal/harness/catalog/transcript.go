package catalog

import (
	"github.com/zigai/aht/v2/internal/harness/transcript"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TranscriptFor(id registry.Harness) transcript.Reader {
	if adapter, ok := Find(id); ok {
		return adapter.Transcript()
	}

	return transcript.Reader{}
}
