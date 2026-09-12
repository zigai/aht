//go:build integration

package tmux

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/internal/testtmux"
	"github.com/zigai/aht/pkg/registry"
)

func TestTmuxFormatWithRealTmuxEscapedFields(t *testing.T) {
	server := testtmux.New(t, "sleep", "60")
	weirdValue := "value with spaces 'quote $dollar back\\slash and-more"
	server.Run(t, "set-option", "-gq", "@aht_weird", weirdValue)
	output := server.Run(t,
		"display-message",
		"-p",
		"-F",
		tmuxFormat([]string{"@aht_weird"}),
	)
	fields, err := parseTmuxFields(output, 1)
	if err != nil {
		t.Fatalf("parseTmuxFields returned error: %v; output=%q", err, output)
	}
	if len(fields) != 1 {
		t.Fatalf("fields = %#v, want one field", fields)
	}
	if fields[0] != weirdValue {
		t.Fatalf("field = %q, want %q", fields[0], weirdValue)
	}
}

func tmuxFormat(fields []string) string {
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, escapedFieldPrefix+"#{q:"+field+"}")
	}
	return strings.Join(parts, " ")
}

func TestGotmuxCapturePaneAndCurrent(t *testing.T) {
	server := testtmux.New(t, "-s", "gotmux-test", "sh", "-c", "echo 'hello gotmux'; sleep 60")
	server.Run(t, "new-window", "-d", "sleep", "60")
	panes, err := server.Tmux.Panes(t.Context())
	if err != nil || len(panes) == 0 {
		t.Fatalf("server.Tmux.Panes error = %v, len = %d", err, len(panes))
	}
	paneID := string(panes[0].ID)
	pid := strconv.Itoa(panes[0].PID)

	pane := Pane{
		Tmux: registry.TmuxContext{
			Inside:       true,
			ServerSocket: server.Socket,
			SessionName:  "gotmux-test",
			PaneID:       paneID,
		},
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
