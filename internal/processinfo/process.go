package processinfo

import (
	"fmt"
	"slices"
	"strings"
)

// Process is a point-in-time snapshot of a process. Args and Env are transient
// inventory data and must not be persisted by callers.
type Process struct {
	PID            int
	Name           string
	PPID           int
	ProcessGroupID int
	Foreground     bool
	StartIdentity  string
	Executable     string
	CWD            string
	TTY            string
	// Env holds the trimmed values of the environment keys requested from
	// ListWithEnv. The first occurrence of a key wins, even when empty.
	Env  map[string]string `json:"-"`
	Args []string          `json:"-"`
}

// PermissionError reports an inability to inspect a process table or entry.
type PermissionError struct {
	Path string
	Err  error
}

// TableError reports malformed or otherwise unusable process table data.
type TableError struct {
	Path string
	Err  error
}

func (e *PermissionError) Error() string {
	return fmt.Sprintf("permission denied reading process information at %s: %v", e.Path, e.Err)
}

func (e *PermissionError) Unwrap() error { return e.Err }

func (e *TableError) Error() string {
	return fmt.Sprintf("invalid process table data at %s: %v", e.Path, e.Err)
}

func (e *TableError) Unwrap() error { return e.Err }

func parseEnvironment(data []byte, keys []string) map[string]string {
	var env map[string]string

	for entry := range strings.SplitSeq(string(data), "\x00") {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !slices.Contains(keys, key) {
			continue
		}

		if _, seen := env[key]; seen {
			continue
		}

		if env == nil {
			env = make(map[string]string, len(keys))
		}
		// Clone so the map does not retain the whole environment buffer.
		env[key] = strings.Clone(strings.TrimSpace(value))
	}

	return env
}
