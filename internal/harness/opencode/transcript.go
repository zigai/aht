package opencode

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func (opencodeHarness) Transcript() transcript.Reader {
	return transcript.Reader{
		DefaultFormat: transcript.FormatDatabase,
		Patterns: []string{
			"opencode*.db",
		},
		Sources:       transcriptSources,
		SkipDirectory: skipTranscriptDirectory,
		Query:         transcriptQuery,
	}
}

func transcriptSources(home string) ([]string, error) {
	return []string{
		transcript.DatabaseLocation(
			filepath.Join(transcript.DataHome(home), "opencode"),
			"OPENCODE_DB",
		),
	}, nil
}
func skipTranscriptDirectory(path string) bool { return path != "." }
