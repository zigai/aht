package kilo

import (
	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func (kiloHarness) Transcript() transcript.Reader {
	return transcript.Reader{
		DefaultFormat: transcript.FormatDatabase,
		Patterns: []string{
			"kilo*.db",
			"opencode*.db",
		},
		Sources:       transcriptSources,
		SkipDirectory: skipTranscriptDirectory,
		Query:         transcriptQuery,
	}
}

func transcriptSources(home string) ([]string, error) {
	path := kiloPaths(home).history
	if path == "" {
		return nil, nil
	}

	return []string{path}, nil
}
func skipTranscriptDirectory(path string) bool { return path != "." }
