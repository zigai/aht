package catalog

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/registry"
)

const leadingArgumentCount = 2

// ResolveProcess identifies the harness a process runs from its executable,
// its leading arguments, or, for a wrapper such as "env FOO=1 codex", any
// later argument.
func ResolveProcess(process processinfo.Process) (registry.Harness, bool) {
	if harnessID, ok := FromCommand(process.Executable); ok {
		return harnessID, true
	}

	// Interpreters and launch shims, as in "node /path/to/agent", name the
	// agent right after themselves.
	leading := min(leadingArgumentCount, len(process.Args))
	for _, arg := range process.Args[:leading] {
		if harnessID, ok := FromCommand(arg); ok {
			return harnessID, true
		}
	}

	if IsWrapper(process) {
		for _, arg := range process.Args[leading:] {
			if harnessID, ok := FromCommand(arg); ok {
				return harnessID, true
			}
		}
	}

	return "", false
}

// IsWrapper reports whether a process is a launcher, sandbox, environment
// manager, or interpreter that commonly runs an agent. An agent can itself
// look like a wrapper (pi runs as node).
func IsWrapper(process processinfo.Process) bool {
	command := filepath.Base(process.Executable)
	if command == "" && len(process.Args) > 0 {
		command = filepath.Base(process.Args[0])
	}

	switch command {
	case "env",
		"fence",
		"bwrap",
		"bubblewrap",
		"mise",
		"nix-shell",
		"nix",
		"direnv",
		"node",
		"bun",
		"python",
		"python3",
		"sh",
		"bash":
		return true
	default:
		return false
	}
}
