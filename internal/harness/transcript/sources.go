package transcript

import (
	"os"
	"path/filepath"
)

func EnvPath(name, fallback string) string {
	if path := os.Getenv(name); path != "" {
		return path
	}
	return fallback
}

func DatabaseLocation(root, variable string) string {
	value := os.Getenv(variable)
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
