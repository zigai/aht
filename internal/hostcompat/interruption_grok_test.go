//go:build compatibility

package hostcompat

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/internal/testtmux"
)

// Grok 1.0.3 advertises ACP session/close and acknowledges it, but neither
// cancellation nor that disposal path emits SessionEnd. The native TUI's quit
// path does: docs/user-guide/03-keyboard-shortcuts.md documents Esc cancellation
// and double Ctrl+Q quit; 10-hooks.md documents SessionEnd and shutdown Stop.
// Keep the first main provider response held through cancellation and quitting.
func (host isolatedHost) runGrokInterruption(t *testing.T, command *exec.Cmd) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil && os.Getenv("AHT_TEST_TMUX_EXECUTABLE") == "" {
		t.Fatal("Grok native interruption requires tmux")
	}
	// The installed launcher can update itself. Run a disposable copy of its
	// resolved executable, never modify the developer's native installation.
	sourcePath, err := filepath.EvalSymlinks(host.hostPath)
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	nativePath := filepath.Join(host.root, "grok-native")
	destination, err := os.OpenFile(nativePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}

	server := testtmux.New(t, "-s", "grok", "-x", "160", "-y", "45", "/bin/sh")
	server.Run(t, "set-option", "-w", "-t", "grok:0.0", "remain-on-exit", "on")
	args := []string{"respawn-pane", "-k", "-t", "grok:0.0", "-c", host.work, "env", "-i"}
	args = append(args, command.Env...)
	args = append(args, nativePath, "--model", "aht-compat", "--always-approve", "--disable-web-search", "--no-memory", compatibilityPrompt)
	server.Run(t, args...)
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := server.Command(context.Background(), "capture-pane", "-p", "-t", "grok:0.0").CombinedOutput()
			t.Logf("native Grok presented screen:\n%s", out)
		}
	})
	select {
	case step := <-host.provider.checkpoints:
		if step != 0 {
			t.Fatalf("first native Grok provider request step = %d, want 0", step)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("native Grok did not reach its first active provider request")
	}
	host.waitForActiveSession(t)
	server.Run(t, "send-keys", "-t", "grok:0.0", "Escape")
	time.Sleep(500 * time.Millisecond)
	beforeQuit := server.Run(t, "capture-pane", "-p", "-t", "grok:0.0")
	server.Run(t, "send-keys", "-t", "grok:0.0", "C-q")
	confirmationDeadline := time.Now().Add(800 * time.Millisecond)
	for server.Run(t, "capture-pane", "-p", "-t", "grok:0.0") == beforeQuit {
		if time.Now().After(confirmationDeadline) {
			t.Fatal("native Grok did not present quit confirmation")
		}
		time.Sleep(10 * time.Millisecond)
	}
	server.Run(t, "send-keys", "-t", "grok:0.0", "C-q")
	deadline := time.Now().Add(15 * time.Second)
	for {
		state := strings.TrimSpace(server.Run(t, "display-message", "-p", "-t", "grok:0.0", "#{pane_dead}:#{pane_dead_status}"))
		if state == "1:0" {
			break
		}
		if strings.HasPrefix(state, "1:") || time.Now().After(deadline) {
			t.Fatalf("native Grok did not quit successfully: pane state %q", state)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if count := len(host.provider.Requests()); count != 1 {
		t.Fatalf("interrupted native Grok sent %d model requests, want one held request", count)
	}
	// Process exit is only cleanup evidence; the shared caller still requires
	// SessionEnd from the installed native hook integration.
}
