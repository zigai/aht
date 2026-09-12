package tmux

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zigai/aht/pkg/registry"
)

func TestContextFromEnvBuildsMinimalContext(t *testing.T) {
	t.Parallel()

	ctx := ContextFromEnv(Env{TMUX: "/tmp/tmux-1000/default,123,0", TMUXPane: "%4"})
	if !ctx.Inside || ctx.ServerSocket != "/tmp/tmux-1000/default" || ctx.PaneID != "%4" {
		t.Fatalf("unexpected minimal tmux context: %#v", ctx)
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

func TestParseCurrent(t *testing.T) {
	t.Parallel()

	ctx, err := ParseCurrent("$1\twork\t@2\t3\tapi\t%4\t1\t/home/me/project\t1234\t/dev/pts/5\t/dev/pts/1\n")
	if err != nil {
		t.Fatalf("ParseCurrent returned error: %v", err)
	}

	if !ctx.Inside {
		t.Fatal("expected tmux context to be marked inside")
	}

	if ctx.SessionName != "work" {
		t.Fatalf("expected session name work, got %q", ctx.SessionName)
	}

	if ctx.WindowIndex != "3" {
		t.Fatalf("expected window index 3, got %q", ctx.WindowIndex)
	}

	if ctx.PaneID != "%4" {
		t.Fatalf("expected pane id %%4, got %q", ctx.PaneID)
	}

	if ctx.PanePID != 1234 {
		t.Fatalf("expected pane pid 1234, got %d", ctx.PanePID)
	}

	if ctx.PaneTTY != "/dev/pts/5" {
		t.Fatalf("expected pane tty, got %q", ctx.PaneTTY)
	}
}

func TestParseCurrentAllowsTabInPaneCurrentPath(t *testing.T) {
	t.Parallel()

	ctx, err := ParseCurrent("$1\twork\t@2\t3\tapi\t%4\t1\t/home/me/dir\twith-tab\t1234\t/dev/pts/5\t/dev/pts/1\n")
	if err != nil {
		t.Fatalf("ParseCurrent returned error: %v", err)
	}
	if ctx.PaneCurrentPath != "/home/me/dir\twith-tab" {
		t.Fatalf("expected tab in pane current path, got %q", ctx.PaneCurrentPath)
	}
}

func TestParseCurrentEscapedFields(t *testing.T) {
	t.Parallel()

	output := "tmuxctx:\\$1 tmuxctx:work tmuxctx:@2 tmuxctx:3 tmuxctx:api " +
		"tmuxctx:%4 tmuxctx:1 tmuxctx:'/home/me/dir\twith-tab' " +
		"tmuxctx:1234 tmuxctx:/dev/pts/5 tmuxctx:/dev/pts/1\n"
	ctx, err := ParseCurrent(output)
	if err != nil {
		t.Fatalf("ParseCurrent returned error: %v", err)
	}
	if ctx.SessionID != "$1" || ctx.PaneCurrentPath != "/home/me/dir\twith-tab" {
		t.Fatalf("unexpected escaped tmux context: %#v", ctx)
	}
}

func TestParseCurrentUnquotedTabInField(t *testing.T) {
	t.Parallel()

	// Real tmux #{q:...} leaves raw tabs unquoted and unescaped
	output := "tmuxctx:\\$1 tmuxctx:work tmuxctx:@2 tmuxctx:3 tmuxctx:api " +
		"tmuxctx:%4 tmuxctx:1 tmuxctx:/home/me/dir\twith-tab " +
		"tmuxctx:1234 tmuxctx:/dev/pts/5 tmuxctx:/dev/pts/1\n"
	ctx, err := ParseCurrent(output)
	if err != nil {
		t.Fatalf("ParseCurrent returned error: %v", err)
	}
	if ctx.PaneCurrentPath != "/home/me/dir\twith-tab" {
		t.Fatalf("unexpected unquoted tab path: %#v", ctx.PaneCurrentPath)
	}
}

func TestParseTmuxFieldsHandlesQuoting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output string
		want   string
	}{
		{name: "plain dollar", output: `tmuxctx:value\ $dollar`, want: `value $dollar`},
		{name: "literal backslash", output: `tmuxctx:value\ \\\$dollar`, want: `value \$dollar`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fields, err := parseTmuxFields(test.output, 1)
			if err != nil {
				t.Fatalf("parseTmuxFields returned error: %v", err)
			}
			if len(fields) != 1 || fields[0] != test.want {
				t.Fatalf("fields = %#v, want [%q]", fields, test.want)
			}
		})
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

func TestParseListPanes(t *testing.T) {
	t.Parallel()

	panes, err := ParseListPanes("$1\twork\t@2\t3\tapi\t%4\t1\t/home/me/project\t1234\t/dev/pts/5\t/tmp/tmux-1000/default\n" +
		"$1\twork\t@2\t3\tapi\t%5\t2\t/home/me/project\t1235\t/dev/pts/6\t/tmp/tmux-1000/default\n")
	if err != nil {
		t.Fatalf("ParseListPanes returned error: %v", err)
	}

	if len(panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(panes))
	}

	if panes[0].PanePID != 1234 || panes[0].PaneTTY != "/dev/pts/5" || panes[0].ServerIdentity != "/tmp/tmux-1000/default" {
		t.Fatalf("unexpected first pane identity: %#v", panes[0])
	}

	if panes[1].Tmux.PaneID != "%5" {
		t.Fatalf("expected second pane id %%5, got %q", panes[1].Tmux.PaneID)
	}
}

func TestParseListPanesEscapedFields(t *testing.T) {
	t.Parallel()

	panes, err := ParseListPanes("tmuxctx:\\$1 tmuxctx:work tmuxctx:@2 tmuxctx:3 tmuxctx:api " +
		"tmuxctx:%4 tmuxctx:1 tmuxctx:'/home/me/dir\twith-tab' " +
		"tmuxctx:1234 tmuxctx:/dev/pts/5 tmuxctx:/tmp/tmux-1000/default\n")
	if err != nil {
		t.Fatalf("ParseListPanes returned error: %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("expected one pane, got %d", len(panes))
	}
	if panes[0].Tmux.PaneCurrentPath != "/home/me/dir\twith-tab" ||
		panes[0].PanePID != 1234 || panes[0].PaneTTY != "/dev/pts/5" ||
		panes[0].ServerIdentity != "/tmp/tmux-1000/default" {
		t.Fatalf("unexpected escaped pane: %#v", panes[0])
	}
}

func TestServerSpecFromArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		identity string
		tmuxArgs []string
		ok       bool
	}{
		{name: "socket", args: []string{"tmux: server", "-S", "/tmp/custom"}, identity: "/tmp/custom", tmuxArgs: []string{"-S", "/tmp/custom"}, ok: true},
		{name: "named", args: []string{"tmux: server", "-L", "other"}, identity: "-L:other", tmuxArgs: []string{"-L", "other"}, ok: true},
		{name: "listed named server", args: []string{"tmux", "-L", "other", "new-session", "-d"}, identity: "-L:other", tmuxArgs: []string{"-L", "other"}, ok: true},
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
			if got.Identity != test.identity || strings.Join(got.Args, "\x00") != strings.Join(test.tmuxArgs, "\x00") {
				t.Fatalf("server = %#v, want identity %q args %#v", got, test.identity, test.tmuxArgs)
			}
		})
	}
}

func TestListPanesWithOptionsDoesNotProbeMissingDefaultServer(t *testing.T) {
	t.Parallel()
	panes, err := ListPanesWithOptions(context.Background(), ListOptions{
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
		Env: Env{TMUX: "", TMUXPane: ""},
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
		Tmux: registry.TmuxContext{
			Inside:          true,
			ServerSocket:    socket,
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
