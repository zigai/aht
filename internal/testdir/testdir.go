// Package testdir creates temporary directories for tests that bind Unix sockets.
package testdir

import (
	"os"
	"testing"

	"github.com/zigai/aht/v2/internal/terminal"
)

// Short returns a new directory under /tmp that is removed when the test ends.
// Use it instead of t.TempDir when the directory holds a Unix socket: t.TempDir
// paths can exceed the socket path limit (104 bytes on Darwin, 108 on Linux).
func Short(tb testing.TB, prefix string) string {
	tb.Helper()

	dir, err := terminal.NewSocketDir(prefix)
	if err != nil {
		tb.Fatalf("create short temporary directory: %v", err)
	}

	tb.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			tb.Errorf("remove short temporary directory: %v", err)
		}
	})

	return dir
}
