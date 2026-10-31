package launch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var errMalformedEnvironment = errors.New("environment entry must be KEY=VALUE with a non-empty key")

func commonEnvironment(root string, store string) []string {
	env := []string{"LANG=C.UTF-8", "TERM=xterm-256color", "NO_COLOR=1"}

	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "PNPM_HOME", "BUN_INSTALL", "UV_TOOL_DIR"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}

	return append(env,
		"HOME="+filepath.Join(root, "home"),
		"XDG_CONFIG_HOME="+filepath.Join(root, "config"),
		"XDG_DATA_HOME="+filepath.Join(root, "data"),
		"XDG_STATE_HOME="+filepath.Join(root, "state"),
		"XDG_CACHE_HOME="+filepath.Join(root, "cache"),
		"TMPDIR="+filepath.Join(root, "tmp"),
		"AHT_STATE_DIR="+filepath.Join(root, "aht-state"),
		"AHT_STORE="+store,
	)
}

func mergeEnvironment(layers ...[]string) ([]string, error) {
	positions := make(map[string]int)

	var merged []string

	for _, layer := range layers {
		for _, entry := range layer {
			name, _, found := strings.Cut(entry, "=")
			if !found || name == "" {
				return nil, fmt.Errorf("%q: %w", entry, errMalformedEnvironment)
			}

			if position, exists := positions[name]; exists {
				merged[position] = entry
				continue
			}

			positions[name] = len(merged)
			merged = append(merged, entry)
		}
	}

	return merged, nil
}

func envValue(env []string, name string) (string, bool) {
	for _, entry := range env {
		key, value, found := strings.Cut(entry, "=")
		if found && key == name {
			return value, true
		}
	}

	return "", false
}
