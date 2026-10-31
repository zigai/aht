package processinfo

import "fmt"

// Process is a point-in-time snapshot of a process. Args, AgentHint, and the
// multiplexer hints are transient inventory data and must not be persisted by
// callers.
type Process struct {
	PID                int
	Name               string
	PPID               int
	ProcessGroupID     int
	Foreground         bool
	StartIdentity      string
	Executable         string
	CWD                string
	TTY                string
	AgentHint          string
	MultiplexerKind    string
	MultiplexerServer  string
	MultiplexerSession string
	MultiplexerPane    string
	Args               []string `json:"-"`
}

// UnsupportedError reports that process enumeration is unavailable on the
// current operating system.
type UnsupportedError struct {
	Platform string
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

func (e *UnsupportedError) Error() string {
	if e.Platform == "" {
		return "process enumeration is unsupported"
	}

	return "process enumeration is unsupported on " + e.Platform
}

func (e *PermissionError) Error() string {
	return fmt.Sprintf("permission denied reading process information at %s: %v", e.Path, e.Err)
}

func (e *PermissionError) Unwrap() error { return e.Err }

func (e *TableError) Error() string {
	return fmt.Sprintf("invalid process table data at %s: %v", e.Path, e.Err)
}

func (e *TableError) Unwrap() error { return e.Err }
