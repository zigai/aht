package goose

import (
	"os"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

// Goose resolves its directories with etcetera's app strategy, which uses
// XDG locations on Linux and macOS. An absolute GOOSE_PATH_ROOT replaces them.
func pathRoot() string {
	if root := os.Getenv("GOOSE_PATH_ROOT"); filepath.IsAbs(root) {
		return root
	}

	return ""
}

func sessionDatabasePath(home string) string {
	if root := pathRoot(); root != "" {
		return filepath.Join(root, "data", "sessions", "sessions.db")
	}

	if home == "" {
		return ""
	}

	return filepath.Join(transcript.DataHome(home), "goose", "sessions", "sessions.db")
}

func configDir(home string) string {
	if root := pathRoot(); root != "" {
		return filepath.Join(root, "config")
	}

	if home == "" {
		return ""
	}

	return filepath.Join(home, ".config", "goose")
}
