package catalog

import (
	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/transcript"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TranscriptFor(id registry.Harness) transcript.Reader {
	if adapter, ok := Find(id); ok {
		if provider, ok := adapter.(harness.TranscriptProvider); ok {
			return provider.Transcript()
		}
	}

	return transcript.Reader{}
}
