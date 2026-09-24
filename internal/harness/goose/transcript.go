package goose

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/zigai/aht/internal/harness/transcript"
)

func (gooseHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"sessions.db"}, Sources: transcriptSources, SkipDirectory: nil, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: nil, Document: nil, Query: transcriptQuery}
}
func transcriptSources(home string) []string { return []string{transcriptGoosePath(home)} }
func transcriptGoosePath(home string) string {
	root := filepath.Join(transcript.DataHome(home), "goose")
	if runtime.GOOS == "darwin" {
		root = filepath.Join(home, "Library", "Application Support", "Block", "goose")
	}
	if override := os.Getenv("GOOSE_PATH_ROOT"); filepath.IsAbs(override) {
		root = filepath.Join(override, "data")
	}
	return filepath.Join(root, "sessions", "sessions.db")
}
