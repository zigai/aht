package tmux

import (
	"testing"
)

func TestServerSpecFromArgsPreservesArgumentBoundaries(t *testing.T) {
	t.Parallel()

	server, ok := serverSpecFromArgs([]string{"tmux", "-S", "/tmp/agent sessions.sock", "server"})
	if !ok || server.Identity != "/tmp/agent sessions.sock" {
		t.Fatalf("server spec lost socket argument boundary: %#v, %t", server, ok)
	}

	cfg, err := gotmuxConfigForIdentity(server.Identity)
	if err != nil || cfg.SocketPath != "/tmp/agent sessions.sock" {
		t.Fatalf("gotmux config = %#v, %v, want SocketPath /tmp/agent sessions.sock", cfg, err)
	}
}

func TestServerSpecFromArgsSupportsBinarySuffixAndFlags(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		args         []string
		wantOK       bool
		wantIdentity string
	}{
		{
			name: "tmux daemon flag",
			args: []string{
				"/home/user/.local/bin/tmux.bin",
				"-D",
			},
			wantOK:       true,
			wantIdentity: "default",
		},
		{
			name: "tmux named socket",
			args: []string{
				"/usr/bin/tmux",
				"-L",
				"popup",
			},
			wantOK:       true,
			wantIdentity: "-L:popup",
		},
		{
			name: "tmux.bin custom socket",
			args: []string{
				"/usr/local/bin/tmux.bin",
				"-S",
				"/tmp/custom.sock",
			},
			wantOK:       true,
			wantIdentity: "/tmp/custom.sock",
		},
		{name: "tmux.real binary", args: []string{"/usr/bin/tmux.real", "-d"}, wantOK: true, wantIdentity: "default"},
		{name: "attached named socket", args: []string{"tmux", "-Lpopup"}, wantOK: true, wantIdentity: "-L:popup"},
		{
			name: "bundled socket flags",
			args: []string{
				"tmux",
				"-uS/tmp/agent sessions.sock",
				"server",
			},
			wantOK:       true,
			wantIdentity: "/tmp/agent sessions.sock",
		},
		{
			name: "child command flag is not a socket",
			args: []string{
				"tmux",
				"new-session",
				"sh",
				"-c",
				"script",
				"-S",
				"/tmp/child.sock",
			},
			wantOK:       true,
			wantIdentity: "default",
		},
		{name: "missing socket value", args: []string{"tmux", "-S"}, wantOK: false},
		{name: "unrelated binary", args: []string{"/usr/bin/bash", "-c", "echo"}, wantOK: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server, ok := serverSpecFromArgs(tc.args)
			if ok != tc.wantOK {
				t.Fatalf("serverSpecFromArgs(%v) ok = %t, want %t", tc.args, ok, tc.wantOK)
			}

			if ok && server.Identity != tc.wantIdentity {
				t.Fatalf(
					"serverSpecFromArgs(%v) identity = %q, want %q",
					tc.args,
					server.Identity,
					tc.wantIdentity,
				)
			}
		})
	}
}
