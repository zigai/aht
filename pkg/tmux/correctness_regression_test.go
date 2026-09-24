//go:build integration

package tmux

import (
	"strconv"
	"strings"
	"testing"
	"time"

	gotmux "github.com/zigai/gotmux/tmux"

	"github.com/zigai/aht/internal/testtmux"
	"github.com/zigai/aht/pkg/registry"
)

func TestGotmuxCapturePaneAndCurrent(t *testing.T) {
	server := testtmux.New(t, gotmux.NewSessionOptions{Name: "gotmux-test", Program: gotmux.Exec("sh", "-c", "echo 'hello gotmux'; sleep 60")})
	if _, err := server.Session.NewWindow(t.Context(), gotmux.NewWindowOptions{Program: gotmux.Exec("sleep", "60")}); err != nil {
		t.Fatal(err)
	}
	panes, err := server.Tmux.Panes(t.Context())
	if err != nil || len(panes) == 0 {
		t.Fatalf("server.Tmux.Panes error = %v, len = %d", err, len(panes))
	}
	paneID := string(panes[0].ID)
	pid := strconv.Itoa(server.Session.Identity().PID)

	pane := Pane{
		Tmux:           registry.Location{Kind: registry.MultiplexerTmux, ServerID: server.Socket, SessionName: "gotmux-test", PaneID: paneID},
		ServerIdentity: server.Socket,
		PanePID:        0,
		PaneTTY:        "",
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
