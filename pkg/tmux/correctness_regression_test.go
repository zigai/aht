//go:build integration

package tmux

import (
	"testing"

	"github.com/zigai/aht/internal/testtmux"
)

func TestTmuxFormatQuotesBareVariableNames(t *testing.T) {
	t.Parallel()

	got := currentFormat()
	want := "tmuxctx:#{q:session_id} tmuxctx:#{q:session_name} tmuxctx:#{q:window_id} " +
		"tmuxctx:#{q:window_index} tmuxctx:#{q:window_name} tmuxctx:#{q:pane_id} " +
		"tmuxctx:#{q:pane_index} tmuxctx:#{q:pane_current_path} tmuxctx:#{q:pane_pid} " +
		"tmuxctx:#{q:pane_tty} tmuxctx:#{q:client_tty}"
	if got != want {
		t.Fatalf("tmux format = %q, want %q", got, want)
	}
}

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
