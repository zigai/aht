//go:build compatibility

package hostcompat

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	gotmux "github.com/zigai/gotmux/tmux"

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

	pane := host.startGrokPane(t, command, nativePath)
	capture := func() string {
		out, err := pane.Capture(t.Context(), gotmux.CaptureOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	t.Cleanup(func() {
		if t.Failed() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := pane.Capture(ctx, gotmux.CaptureOptions{})
			if err != nil {
				t.Logf("capture native Grok failure screen: %v", err)
			}
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
	if err := pane.SendKeys(t.Context(), gotmux.KeyEscape); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	beforeQuit := capture()
	if err := pane.SendKeys(t.Context(), gotmux.Key("C-q")); err != nil {
		t.Fatal(err)
	}
	confirmationDeadline := time.Now().Add(800 * time.Millisecond)
	for capture() == beforeQuit {
		if time.Now().After(confirmationDeadline) {
			t.Fatal("native Grok did not present quit confirmation")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := pane.SendKeys(t.Context(), gotmux.Key("C-q")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		state, err := pane.Info(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		status, known := state.DeadStatus.Get()
		if state.Dead && known && status == 0 {
			break
		}
		if state.Dead || time.Now().After(deadline) {
			t.Fatalf("native Grok did not quit successfully: dead=%t status=%v", state.Dead, state.DeadStatus)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if count := len(host.provider.Requests()); count != 1 {
		t.Fatalf("interrupted native Grok sent %d model requests, want one held request", count)
	}
	// Process exit is only cleanup evidence; the shared caller still requires
	// SessionEnd from the installed native hook integration.
}

func (host isolatedHost) startGrokPane(t *testing.T, command *exec.Cmd, nativePath string) gotmux.Pane {
	t.Helper()
	server := testtmux.New(t, gotmux.NewSessionOptions{
		Name: "grok", Size: gotmux.Size{Width: 160, Height: 45}, Program: gotmux.Exec("/bin/sh"),
	})
	panes, err := server.Tmux.Panes(t.Context())
	if err != nil || len(panes) != 1 {
		t.Fatalf("native Grok initial pane: count=%d error=%v", len(panes), err)
	}
	pane := panes[0].Handle()
	if err := pane.Options().SetRemainOnExit(t.Context(), gotmux.RemainOn); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"-i"}, command.Env...)
	args = append(args, nativePath, "--model", "aht-compat", "--always-approve", "--disable-web-search", "--no-memory", compatibilityPrompt)
	if err := pane.Respawn(t.Context(), gotmux.RespawnOptions{
		Dir: host.work, Program: gotmux.Exec("env", args...), KillRunning: true,
	}); err != nil {
		t.Fatal(err)
	}
	return pane
}
