package goose

import (
	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func (gooseHarness) Transcript() transcript.Reader {
	return transcript.Reader{
		DefaultFormat: transcript.FormatDatabase,
		Patterns:      []string{"sessions.db"},
		Sources:       transcriptSources,
		Query:         transcriptQuery,
	}
}

func transcriptSources(home string) ([]string, error) {
	return []string{sessionDatabasePath(home)}, nil
}
