package agy

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func (agyHarness) Transcript() transcript.Reader {
	return transcript.Reader{
		Sources: transcriptSources,
	}
}

func transcriptSources(home string) ([]string, error) {
	return []string{filepath.Join(home, ".gemini", "antigravity-cli")}, nil
}
