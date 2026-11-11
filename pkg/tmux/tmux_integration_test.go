//go:build integration

package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	gotmux "github.com/zigai/gotmux/tmux"

	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/internal/testtmux"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestPanePresenceQueriesSavedServer(t *testing.T) {
	server := testtmux.New(
		t,
		gotmux.NewSessionOptions{Name: "presence", Program: gotmux.Exec("sleep", "60")},
	)

	panes, err := server.Tmux.Panes(t.Context())
	if err != nil || len(panes) != 1 {
		t.Fatalf("server panes = %v, %v", panes, err)
	}

	for _, test := range []struct {
		name   string
		server string
		pane   string
		want   registry.Presence
	}{
		{name: "existing pane", server: server.Socket, pane: string(panes[0].ID), want: registry.PresenceLive},
		{name: "missing pane", server: server.Socket, pane: "%99999", want: registry.PresenceGone},
		{
			name: "missing server with same pane id",
			server: filepath.Join(
				t.TempDir(),
				"missing.sock",
			),
			pane: string(
				panes[0].ID,
			),
			want: registry.PresenceGone,
		},
		{name: "unknown server", pane: string(panes[0].ID), want: registry.PresenceUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := PanePresence(
				t.Context(),
				registry.Location{ServerID: test.server, PaneID: test.pane},
			)
			if err != nil || got != test.want {
				t.Fatalf("pane presence = %s, %v; want %s", got, err, test.want)
			}
		})
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	got, err := PanePresence(
		ctx,
		registry.Location{ServerID: server.Socket, PaneID: string(panes[0].ID)},
	)
	if got != registry.PresenceUnknown || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled pane check = %s, %v", got, err)
	}
}

func TestGotmuxCapturePaneAndCurrent(t *testing.T) {
	server := testtmux.New(
		t,
		gotmux.NewSessionOptions{
			Name:    "gotmux-test",
			Program: gotmux.Exec("sh", "-c", "echo 'hello gotmux'; sleep 60"),
		},
	)
	if _, err := server.Session.NewWindow(
		t.Context(),
		gotmux.NewWindowOptions{Program: gotmux.Exec("sleep", "60")},
	); err != nil {
		t.Fatal(err)
	}

	panes, err := server.Tmux.Panes(t.Context())
	if err != nil || len(panes) == 0 {
		t.Fatalf("server.Tmux.Panes error = %v, len = %d", err, len(panes))
	}

	paneID := string(panes[0].ID)
	pid := strconv.Itoa(server.Session.ServerIdentity().PID)

	pane := Pane{
		Location: registry.Location{
			Kind:        registry.MultiplexerTmux,
			ServerID:    server.Socket,
			SessionName: "gotmux-test",
			PaneID:      paneID,
		},
		PanePID: 0,
		PaneTTY: "",
	}

	deadline := time.Now().Add(3 * time.Second)

	for {
		snapshot, err := CapturePane(t.Context(), pane)
		if err == nil && strings.Contains(snapshot.Text, "hello gotmux") {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for pane capture, last text: %q", snapshot.Text)
		}

		time.Sleep(20 * time.Millisecond)
	}

	tmuxEnv := server.Socket + "," + pid + ",0"

	current, err := CurrentWithEnv(t.Context(), Env{TMUX: tmuxEnv, TMUXPane: paneID})
	if err != nil {
		t.Fatalf("CurrentWithEnv failed: %v", err)
	}

	if current.PaneID != paneID || current.SessionName != "gotmux-test" {
		t.Fatalf("unexpected current context: %#v", current)
	}

	if err := SendInterruptTo(t.Context(), server.Socket, paneID); err != nil {
		t.Fatalf("SendInterruptTo failed: %v", err)
	}
}

func TestCapturePanesMatchesEachPaneAcrossServers(t *testing.T) {
	first := testtmux.New(t, gotmux.NewSessionOptions{
		Name:    "first",
		Program: gotmux.Exec("sh", "-c", "echo 'first server, first pane'; sleep 60"),
	})
	if _, err := first.Session.NewWindow(t.Context(), gotmux.NewWindowOptions{
		Program: gotmux.Exec("sh", "-c", "echo 'first server, second pane'; sleep 60"),
	}); err != nil {
		t.Fatal(err)
	}

	second := testtmux.New(t, gotmux.NewSessionOptions{
		Name:    "second",
		Program: gotmux.Exec("sh", "-c", "echo 'second server'; sleep 60"),
	})

	pane := func(serverID string, paneID string) Pane {
		return Pane{
			Location: registry.Location{
				Kind:     registry.MultiplexerTmux,
				ServerID: serverID,
				PaneID:   paneID,
			},
			PanePID: 0,
			PaneTTY: "",
		}
	}
	panes := []Pane{
		pane(second.Socket, "%0"),
		pane(first.Socket, ""),
		pane(first.Socket, "%1"),
		pane(first.Socket, "%999"),
		pane("-L:", "%0"),
		pane(first.Socket, "%0"),
	}
	want := []struct {
		text string
		err  error
	}{
		{text: "second server", err: nil},
		{text: "", err: errMissingCapturePane},
		{text: "first server, second pane", err: nil},
		{text: "", err: gotmux.ErrNotFound},
		{text: "", err: errInvalidServerIdentity},
		{text: "first server, first pane", err: nil},
	}

	deadline := time.Now().Add(3 * time.Second)

	for {
		captures := CapturePanes(t.Context(), panes)
		if len(captures) != len(panes) {
			t.Fatalf("captures = %d, want %d", len(captures), len(panes))
		}

		matched := true

		for i, capture := range captures {
			if !errors.Is(capture.Err, want[i].err) ||
				!strings.Contains(capture.Snapshot.Text, want[i].text) ||
				(capture.Err != nil && capture.Snapshot.Text != "") {
				matched = false
			}
		}

		if matched {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("captures = %#v, want %#v", captures, want)
		}

		time.Sleep(20 * time.Millisecond)
	}
}

func TestListPanesReadsProgramStatus(t *testing.T) {
	server := testtmux.New(t, gotmux.NewSessionOptions{
		Name: "program-status",
		Program: gotmux.Exec(
			"sh", "-c",
			`printf '\033]7501;state=blocked:kind=permission:id=child:app=other\033\\'; `+
				`printf '\033]7501;state=working:app=demo\033\\'; sleep 60`,
		),
	})

	if !programStatusSupported(t, server) {
		t.Skip("this tmux keeps no OSC 7501 program status records")
	}

	want := mux.ProgramStatus{App: "demo", State: mux.ProgramStateWorking, Kind: ""}
	deadline := time.Now().Add(3 * time.Second)

	for {
		panes, err := ListPanesWithOptions(t.Context(), ListOptions{
			Processes:   []processinfo.Process{},
			SocketPaths: []string{server.Socket},
		})
		if err != nil || len(panes) != 1 {
			t.Fatalf("list panes = %#v, %v", panes, err)
		}

		if got := panes[0].ToMuxPane().Program; got != nil && *got == want {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("program status = %#v, want %#v", panes[0].Program, want)
		}

		time.Sleep(20 * time.Millisecond)
	}
}

func programStatusSupported(t *testing.T, server *testtmux.Server) bool {
	t.Helper()

	return exec.CommandContext(
		t.Context(), testtmux.Executable(t),
		"-S", server.Socket, "show-hooks", "-g", programStatusHook,
	).Run() == nil
}

func TestWatchProgramStatusDeliversStateAndKindChanges(t *testing.T) {
	server := testtmux.New(t, gotmux.NewSessionOptions{
		Name:    "program-status-watch",
		Program: gotmux.Exec("sleep", "60"),
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	changes := make(chan []mux.Pane, 16)
	done := make(chan error, 1)

	go func() {
		done <- WatchProgramStatus(ctx, server.Socket, func(panes []mux.Pane) { changes <- panes })
	}()

	if !programStatusSupported(t, server) {
		if err := <-done; !errors.Is(err, mux.ErrProgramStatusUnsignaled) {
			t.Fatalf("watch on a tmux without program status = %v", err)
		}

		return
	}

	var panes []mux.Pane

	select {
	case panes = <-changes:
	case err := <-done:
		t.Fatalf("watch ended with %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the watch delivered no panes after it was set")
	}

	if len(panes) != 1 || panes[0].Program != nil {
		t.Fatalf("panes of the watched server = %#v", panes)
	}

	tty, err := os.OpenFile(panes[0].Location.PaneTTY, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := tty.Close(); err != nil {
			t.Error(err)
		}
	})

	report := func(record string) []mux.ProgramStatus {
		t.Helper()

		if _, err := tty.WriteString("\x1b]7501;" + record + "\x1b\\"); err != nil {
			t.Fatal(err)
		}

		time.Sleep(500 * time.Millisecond)

		delivered := make([]mux.ProgramStatus, 0, len(changes))

		for range len(changes) {
			status := mux.ProgramStatus{}
			if program := (<-changes)[0].Program; program != nil {
				status = *program
			}

			delivered = append(delivered, status)
		}

		return delivered
	}

	working := mux.ProgramStatus{App: "demo", State: mux.ProgramStateWorking, Kind: ""}
	permission := mux.ProgramStatus{App: "demo", State: mux.ProgramStateBlocked, Kind: "permission"}
	question := mux.ProgramStatus{App: "demo", State: mux.ProgramStateBlocked, Kind: "question"}

	for _, step := range []struct {
		record string
		want   []mux.ProgramStatus
	}{
		{record: "state=working:app=demo", want: []mux.ProgramStatus{working}},
		{record: "state=working:app=demo:msg=aGVsbG8=", want: nil},
		{record: "state=blocked:kind=permission:id=child:app=demo", want: nil},
		{record: "state=blocked:kind=permission:app=demo", want: []mux.ProgramStatus{permission}},
		{record: "state=blocked:kind=question:app=demo", want: []mux.ProgramStatus{question}},
		{record: "state=clear", want: []mux.ProgramStatus{{}}},
	} {
		if got := report(step.record); !slices.Equal(got, step.want) {
			t.Fatalf("records delivered after %q = %v, want %v", step.record, got, step.want)
		}
	}

	cancel()

	if err := <-done; err != nil {
		t.Fatalf("watch ended with %v", err)
	}
}
