package transcript

import (
	"os"
	"path/filepath"
	"strings"
)

func EnvPath(name string, fallback string) string {
	if path := strings.TrimSpace(os.Getenv(name)); path != "" {
		return path
	}

	return fallback
}

func DatabaseLocation(root string, variable string) string {
	value := strings.TrimSpace(os.Getenv(variable))
	if value == "" || value == ":memory:" {
		return root
	}

	if filepath.IsAbs(value) {
		return value
	}

	return filepath.Join(root, value)
}

func DataHome(home string) string {
	return EnvPath("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
}
