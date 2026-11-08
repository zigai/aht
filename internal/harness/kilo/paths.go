package kilo

import (
	"os"
	"path/filepath"
	"strings"
)

type nativePaths struct {
	config   string
	database string
	history  string
}

func kiloPaths(home string) nativePaths {
	paths := nativePaths{config: "", database: "", history: ""}

	config := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if config == "" && home != "" {
		config = filepath.Join(home, ".config")
	}

	if value := strings.TrimSpace(os.Getenv("KILO_CONFIG_DIR")); value != "" {
		paths.config = value
	} else if config != "" {
		paths.config = filepath.Join(config, "kilo")
	}

	name := strings.TrimSpace(os.Getenv("KILO_DB"))
	if name == ":memory:" {
		return paths
	}

	if filepath.IsAbs(name) {
		paths.database = name
		paths.history = name

		return paths
	}

	data := os.Getenv("XDG_DATA_HOME")
	if data == "" && home != "" {
		data = filepath.Join(home, ".local", "share")
	}

	if data != "" {
		data = filepath.Join(data, "kilo")
		paths.history = data

		if name == "" {
			name = "kilo.db"
		} else {
			paths.history = filepath.Join(data, name)
		}

		paths.database = filepath.Join(data, name)
	}

	return paths
}
