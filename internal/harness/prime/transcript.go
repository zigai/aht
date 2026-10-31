package prime

import (
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func (primeHarness) Transcript() transcript.Reader {
	return transcript.Reader{
		Patterns: []string{
			"*.jsonl",
		},
		Sources:           transcriptSources,
		SkipDirectory:     nil,
		SkipFile:          skipPrimeArtifactMetadata,
		SourceMetadata:    nil,
		Initialize:        nil,
		Extra:             nil,
		Record:            transcript.TreeRecord,
		FastRecord:        transcript.FastTreeRecord,
		Document:          nil,
		Query:             nil,
		LocalTitles:       true,
		RewritableHeaders: nil,
		Parent:            nil,
	}
}

func transcriptSources(home string) ([]string, error) {
	agentDir := primeAgentDirUnder(home)
	if agentDir == "" {
		return nil, nil
	}

	artifacts := filepath.Join(agentDir, "session-artifacts")
	sessions := primeSessionsDir(home)
	// A worker sets PRIME_AGENT_SESSION_DIR to its own child directory. That
	// path is not the global sessions root, and scanning it beside artifacts
	// would duplicate the child's conversation.
	if withinPrimePath(sessions, artifacts) || withinPrimePath(artifacts, sessions) {
		sessions = filepath.Join(agentDir, "sessions")
	}

	if sessions == "" {
		return []string{artifacts}, nil
	}

	return []string{sessions, artifacts}, nil
}

func withinPrimePath(path string, root string) bool {
	if path == "" || root == "" {
		return false
	}

	relative, err := filepath.Rel(root, path)

	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func skipPrimeArtifactMetadata(path string) bool {
	return filepath.Base(path) == "semantic-edges.jsonl"
}
