package tmux

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestContextFromEnvBuildsMinimalContext(t *testing.T) {
	t.Parallel()

	ctx := ContextFromEnv(Env{TMUX: "/tmp/tmux-1000/default,123,0", TMUXPane: "%4"})
	if (ctx.Kind != registry.MultiplexerTmux) || ctx.ServerID != "/tmp/tmux-1000/default" || ctx.PaneID != "%4" {
		t.Fatalf("unexpected minimal tmux context: %#v", ctx)
	}
}

func TestTmuxServerSocketPreservesCommasInDirectory(t *testing.T) {
	t.Parallel()

	if got := tmuxServerSocket("/tmp/path,with,commas/default,1234,0"); got != "/tmp/path,with,commas/default" {
		t.Fatalf("tmuxServerSocket() = %q, want %q", got, "/tmp/path,with,commas/default")
	}
}

func TestCurrentWithEnvPreservesCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := CurrentWithEnv(ctx, Env{TMUX: "/tmp/tmux/default,1,0", TMUXPane: "%1"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CurrentWithEnv() error = %v, want context.Canceled", err)
	}
}

func TestSendInterruptRequiresPaneID(t *testing.T) {
	t.Parallel()

	err := SendInterruptTo(context.Background(), "default", "")
	if !errors.Is(err, errMissingTmuxPaneID) {
		t.Fatalf("SendInterruptTo with empty pane ID error = %v, want errMissingTmuxPaneID", err)
	}
}

func TestSendInterruptRejectsInvalidServerIdentity(t *testing.T) {
	t.Parallel()

	err := SendInterruptTo(context.Background(), "-L:", "%1")
	if !errors.Is(err, errInvalidServerIdentity) {
		t.Fatalf("SendInterruptTo with invalid server identity error = %v, want errInvalidServerIdentity", err)
	}
}

func TestServerSpecFromArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		args       []string
		identity   string
		wantSocket string
		wantName   string
		ok         bool
	}{
		{name: "socket", args: []string{"tmux: server", "-S", "/tmp/custom"}, identity: "/tmp/custom", wantSocket: "/tmp/custom", ok: true},
		{name: "named", args: []string{"tmux: server", "-L", "other"}, identity: "-L:other", wantName: "other", ok: true},
		{name: "listed named server", args: []string{"tmux", "-L", "other", "new-session", "-d"}, identity: "-L:other", wantName: "other", ok: true},
		{name: "other", args: []string{"bash"}, ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := serverSpecFromArgs(test.args)
			if ok != test.ok {
				t.Fatalf("ok = %v, want %v", ok, test.ok)
			}
			if !ok {
				return
			}
			if got.Identity != test.identity {
				t.Fatalf("server = %#v, want identity %q", got, test.identity)
			}
			cfg, err := gotmuxConfigForIdentity(got.Identity)
			if err != nil {
				t.Fatalf("gotmuxConfigForIdentity(%q) failed: %v", got.Identity, err)
			}
			if cfg.SocketPath != test.wantSocket || cfg.SocketName != test.wantName {
				t.Fatalf("config = %#v, want socket %q name %q", cfg, test.wantSocket, test.wantName)
			}
		})
	}
}

func TestListPanesWithOptionsDoesNotProbeMissingDefaultServer(t *testing.T) {
	t.Parallel()
	panes, err := ListPanesWithOptions(context.Background(), ListOptions{
		SocketPaths:     []string{},
		Env:             Env{TMUX: "", TMUXPane: ""},
		ServerProcesses: func(context.Context) ([]ServerProcess, error) { return nil, nil },
	})
	if err != nil || len(panes) != 0 {
		t.Fatalf("no-server discovery = panes %#v, error %v", panes, err)
	}
}

func TestListPanesWithOptionsIgnoresUnreachableDiscoveredServer(t *testing.T) {
	t.Parallel()
	panes, err := ListPanesWithOptions(context.Background(), ListOptions{
		SocketPaths: []string{},
		Env:         Env{TMUX: "", TMUXPane: ""},
		ServerProcesses: func(context.Context) ([]ServerProcess, error) {
			return []ServerProcess{{PID: 42, Args: []string{"tmux", "-S", "/tmp/stale.sock", "new-session", "-d"}}}, nil
		},
	})
	if err != nil || len(panes) != 0 {
		t.Fatalf("stale-server discovery = panes %#v, error %v", panes, err)
	}
}

func TestListPanesWithOptionsReportsUnreachableCurrentServer(t *testing.T) {
	t.Parallel()
	const socket = "/tmp/current-unreachable.sock"
	panes, err := ListPanesWithOptions(context.Background(), ListOptions{
		SocketPaths:     []string{},
		Env:             Env{TMUX: socket + ",123,0", TMUXPane: "%1"},
		ServerProcesses: func(context.Context) ([]ServerProcess, error) { return nil, nil },
	})
	if err == nil || len(panes) != 0 {
		t.Fatalf("current-server discovery = panes %#v, error %v", panes, err)
	}
}

func TestAppendCanonicalPanesDeduplicates(t *testing.T) {
	t.Parallel()
	const socket = "/tmp/tmux-1000/default"
	seen := make(map[string]struct{})
	pane := Pane{
		Tmux: registry.Location{
			Kind:            registry.MultiplexerTmux,
			ServerID:        socket,
			SessionID:       "",
			SessionName:     "",
			WindowID:        "",
			WindowIndex:     "",
			WindowName:      "",
			PaneID:          "%1",
			PaneIndex:       "",
			PaneCurrentPath: "",
			PanePID:         100,
			PaneTTY:         "/dev/pts/1",
			ClientTTY:       "",
		},
		ServerIdentity: socket,
		PanePID:        100,
		PaneTTY:        "/dev/pts/1",
	}
	panes := appendCanonicalPanes(nil, []Pane{pane}, socket, seen)
	panes = appendCanonicalPanes(panes, []Pane{pane}, socket, seen)
	if len(panes) != 1 {
		t.Fatalf("expected 1 deduplicated pane, got %d", len(panes))
	}
}

func TestDiscoverServersCombinesSocketAndProcessCandidates(t *testing.T) {
	t.Parallel()
	servers, err := discoverServers(t.Context(), ListOptions{
		Env:         Env{TMUX: "/tmp/current.sock,123,0"},
		SocketPaths: []string{"/tmp/current.sock", "/tmp/named.sock", "/tmp/named.sock"},
		ServerProcesses: func(context.Context) ([]ServerProcess, error) {
			return []ServerProcess{
				{Args: []string{"tmux", "-S", "/tmp/current.sock"}},
				{Args: []string{"tmux", "-S/tmp/custom.sock"}},
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	identities := make([]string, 0, len(servers))
	for _, server := range servers {
		identities = append(identities, server.Identity)
	}
	want := []string{"/tmp/current.sock", "/tmp/named.sock", "/tmp/custom.sock"}
	if !slices.Equal(identities, want) {
		t.Fatalf("discovered servers = %q, want %q", identities, want)
	}
}

func TestListPanesPreservesDiscoveryCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err := ListPanesWithOptions(ctx, ListOptions{
		SocketPaths: []string{},
		ServerProcesses: func(context.Context) ([]ServerProcess, error) {
			cancel()
			return nil, nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled discovery = %v, want cancellation", err)
	}
}
