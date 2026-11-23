//go:build darwin

package tmux

import (
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/pkg/mux"
)

func isServerProcess(process mux.Process) bool {
	return process.PID > 0 && strings.HasPrefix(filepath.Base(process.Name), "tmux")
}
