package tmux

import (
	"os"
	"syscall"
	"testing"
)

func TestServerDiscoveryToleratesVanishedProcesses(t *testing.T) {
	t.Parallel()
	for _, err := range []error{os.ErrNotExist, os.ErrPermission, errNotTmuxServer, &os.PathError{Op: "read", Path: "/proc/42/cmdline", Err: syscall.ESRCH}} {
		if !skippableServerProcessError(err) {
			t.Fatalf("discovery rejected a transient process: %v", err)
		}
	}
	if skippableServerProcessError(syscall.EIO) {
		t.Fatal("discovery hid an unexpected failure")
	}
}
