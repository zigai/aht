package tmux

import (
	"testing"

	"github.com/zigai/aht/v2/internal/processinfo"
)

func TestIsServerProcessSeparatesServersFromClients(t *testing.T) {
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

			process := processinfo.Process{PID: 42, Name: test.comm, Args: test.args}
			if server := isServerProcess(process); server != test.server {
				t.Fatalf(
					"isServerProcess(%q, %q) = %t, want %t",
					test.comm,
					test.args,
					server,
					test.server,
				)
			}
		})
	}
}
