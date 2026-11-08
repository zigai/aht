package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	gotmux "github.com/zigai/gotmux/tmux"

	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestLocationFromEnvBuildsMinimalLocation(t *testing.T) {
	t.Parallel()

	location := locationFromEnv(Env{TMUX: "/tmp/tmux-1000/default,123,0", TMUXPane: "%4"})
	if (location.Kind != registry.MultiplexerTmux) ||
		location.ServerID != "/tmp/tmux-1000/default" ||
		location.PaneID != "%4" {
		t.Fatalf("unexpected minimal tmux context: %#v", location)
	}
}

func TestTmuxServerSocketPreservesCommasInDirectory(t *testing.T) {
	t.Parallel()

	if got := tmuxServerSocket(
		"/tmp/path,with,commas/default,1234,0",
	); got != "/tmp/path,with,commas/default" {
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
		t.Fatalf(
			"SendInterruptTo with invalid server identity error = %v, want errInvalidServerIdentity",
			err,
		)
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
		{
			name:       "socket",
			args:       []string{"tmux: server", "-S", "/tmp/custom"},
			identity:   "/tmp/custom",
			wantSocket: "/tmp/custom",
			ok:         true,
		},
		{
			name:     "named",
			args:     []string{"tmux: server", "-L", "other"},
			identity: "-L:other",
			wantName: "other",
			ok:       true,
		},
		{
			name:     "listed named server",
			args:     []string{"tmux", "-L", "other", "new-session", "-d"},
			identity: "-L:other",
			wantName: "other",
			ok:       true,
		},
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
				t.Fatalf(
					"config = %#v, want socket %q name %q",
					cfg,
					test.wantSocket,
					test.wantName,
				)
			}
		})
	}
}

func TestListPanesWithOptionsDoesNotProbeMissingDefaultServer(t *testing.T) {
	t.Parallel()

	panes, err := ListPanesWithOptions(context.Background(), ListOptions{
		SocketPaths: []string{},
		Env:         Env{TMUX: "", TMUXPane: ""},
		Processes:   []processinfo.Process{},
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
		Processes: []processinfo.Process{
			{
				PID:  42,
				Name: "tmux: server",
				Args: []string{"tmux", "-S", "/tmp/stale.sock", "new-session", "-d"},
			},
		},
	})
	if err != nil || len(panes) != 0 {
		t.Fatalf("stale-server discovery = panes %#v, error %v", panes, err)
	}
}

func TestListPanesWithOptionsIgnoresUnreachableCurrentServer(t *testing.T) {
	t.Parallel()

	const socket = "/tmp/current-unreachable.sock"

	panes, err := ListPanesWithOptions(context.Background(), ListOptions{
		SocketPaths: []string{},
		Env:         Env{TMUX: socket + ",123,0", TMUXPane: "%1"},
		Processes:   []processinfo.Process{},
	})
	if err != nil || len(panes) != 0 {
		t.Fatalf("current-server discovery = panes %#v, error %v", panes, err)
	}
}

func TestListPanesWithOptionsPreservesEndpointErrors(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name             string
		partial, current bool
	}{
		{name: "outside all failed"},
		{name: "outside partial", partial: true},
		{name: "inside all failed", current: true},
		{name: "inside partial", partial: true, current: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			opts := ListOptions{
				SocketPaths: []string{
					"/sample/denied",
					"/sample/broken",
					"/sample/stale",
					"/sample/live",
				},
				Processes: []processinfo.Process{},
			}
			if test.current {
				opts.Env.TMUX = "/sample/denied,123,0"
			}

			panes, err := listPanesWithOptions(
				t.Context(),
				opts,
				func(_ context.Context, server serverSpec) ([]Pane, error) {
					switch server.Identity {
					case "/sample/denied":
						return nil, os.ErrPermission
					case "/sample/broken":
						return nil, gotmux.ErrInvalidArgument
					case "/sample/stale":
						return nil, errors.Join(gotmux.ErrNoServer)
					default:
						if test.partial {
							return []Pane{
								{
									Location: registry.Location{
										Kind:   registry.MultiplexerTmux,
										PaneID: "%1",
									},
								},
							}, nil
						}

						return nil, nil
					}
				},
			)
			if !errors.Is(err, os.ErrPermission) || !errors.Is(err, gotmux.ErrInvalidArgument) {
				t.Fatalf("endpoint failures lost: %v", err)
			}

			if !strings.Contains(err.Error(), "/sample/denied") ||
				!strings.Contains(err.Error(), "/sample/broken") ||
				strings.Contains(err.Error(), "/sample/stale") {
				t.Fatalf("error endpoint identities = %v", err)
			}

			want := 0
			if test.partial {
				want = 1
			}

			if len(panes) != want {
				t.Fatalf("successful panes = %v, want %d", panes, want)
			}
		})
	}
}

func TestAppendCanonicalPanesDeduplicates(t *testing.T) {
	t.Parallel()

	const socket = "/tmp/tmux-1000/default"

	seen := make(map[string]struct{})
	pane := Pane{
		Location: registry.Location{
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
		PanePID: 100,
		PaneTTY: "/dev/pts/1",
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
		Processes: []processinfo.Process{
			{PID: 10, Name: "tmux: server", Args: []string{"tmux", "-S", "/tmp/current.sock"}},
			{PID: 11, Name: "tmux: server", Args: []string{"tmux", "-S/tmp/custom.sock"}},
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

func TestDiscoverServersQueriesServerFoundByPathAndNameOnce(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("TMUX_TMPDIR", directory)
	// A bare tmux client follows $TMUX, so the default server is only the
	// socket-directory default outside tmux.
	t.Setenv("TMUX", "")

	socketDirectory := filepath.Join(directory, "tmux-"+strconv.Itoa(os.Getuid()))

	servers, err := discoverServers(t.Context(), ListOptions{
		Env: Env{TMUX: "", TMUXPane: ""},
		SocketPaths: []string{
			filepath.Join(socketDirectory, "default"),
			filepath.Join(socketDirectory, "named"),
		},
		Processes: []processinfo.Process{
			{PID: 10, Name: "tmux: server", Args: []string{"tmux"}},
			{PID: 11, Name: "tmux: server", Args: []string{"tmux", "-L", "named"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	identities := make([]string, 0, len(servers))
	for _, server := range servers {
		identities = append(identities, server.Identity)
	}

	want := []string{
		filepath.Join(socketDirectory, "default"),
		filepath.Join(socketDirectory, "named"),
	}
	if !slices.Equal(identities, want) {
		t.Fatalf("discovered servers = %q, want %q", identities, want)
	}
}

func TestListPanesPreservesDiscoveryCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := ListPanesWithOptions(ctx, ListOptions{
		SocketPaths: []string{},
		Processes:   []processinfo.Process{},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled discovery = %v, want cancellation", err)
	}
}
