package tmux

import (
	"os"
	"syscall"
	"testing"
)

func TestServerDiscoveryToleratesVanishedProcesses(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		os.ErrNotExist,
		os.ErrPermission,
		errNotTmuxServer,
		&os.PathError{
			Op:   "read",
			Path: "/proc/42/cmdline",
			Err:  syscall.ESRCH,
		},
	} {
		if !skippableServerProcessError(err) {
			t.Fatalf("discovery rejected a transient process: %v", err)
		}
	}

	if skippableServerProcessError(syscall.EIO) {
		t.Fatal("discovery hid an unexpected failure")
	}
}

func TestServerProcessFromSeparatesServersFromClients(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		comm   string
		args   []string
		server bool
	}{
		{name: "renamed server", comm: "tmux: server", args: []string{"tmux"}, server: true},
		{name: "daemon flag", comm: "tmux", args: []string{"/usr/bin/tmux", "-D"}, server: true},
		{name: "attached client", comm: "tmux", args: []string{"tmux", "attach"}, server: false},
		{name: "exited process", comm: "tmux: server", args: nil, server: false},
		{name: "other program", comm: "bash", args: []string{"bash"}, server: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if _, server := ServerProcessFrom(42, test.comm, test.args); server != test.server {
				t.Fatalf(
					"ServerProcessFrom(%q, %q) = %t, want %t",
					test.comm,
					test.args,
					server,
					test.server,
				)
			}
		})
	}
}
