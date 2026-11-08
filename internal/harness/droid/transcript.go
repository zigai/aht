package droid

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func (droidHarness) Transcript() transcript.Reader {
	return transcript.Reader{
		Sources: transcriptSources,
	}
}

func transcriptSources(home string) ([]string, error) {
	return []string{filepath.Join(home, ".factory", "sessions")}, nil
}
