package grok

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func (grokHarness) Transcript() transcript.Reader {
	return transcript.Reader{
		DefaultFormat: transcript.FormatDatabase,
		Patterns:      []string{"grok.db"},
		Sources:       transcriptSources,
		Query:         transcriptQuery,
	}
}

func transcriptSources(home string) ([]string, error) {
	root := grokHome(home)
	if root == "" {
		return nil, nil
	}

	return []string{filepath.Join(root, "grok.db")}, nil
}
