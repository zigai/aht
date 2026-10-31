//go:build unix

package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

func terminalWidth(w any) int {
	f, ok := w.(*os.File)
	if !ok || f == nil {
		return 0
	}

	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws == nil || ws.Col <= 0 {
		return 0
	}

	return int(ws.Col)
}
