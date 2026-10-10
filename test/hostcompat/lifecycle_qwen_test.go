//go:build compatibility

package hostcompat

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	gotmux "github.com/zigai/gotmux/tmux"

	"github.com/zigai/aht/v2/internal/testtmux"
	"github.com/zigai/aht/v2/pkg/registry"
)

// Qwen Code 0.25 fires SessionEnd only from its interactive UI's exit cleanup
// and ACP shutdown, never from headless -p runs (packages/cli/src/ui/
// AppContainer.tsx). Drive the native TUI: -i submits the prompt, Esc cancels
// a turn, and Ctrl+C twice quits (docs/users/reference/keyboard-shortcuts.md).
func (host *isolatedHost) runQwenTUI(t *testing.T, command *exec.Cmd, interrupt bool) []byte {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil && os.Getenv("AHT_TEST_TMUX_EXECUTABLE") == "" {
		t.Fatal("Qwen Code native lifecycle requires tmux")
	}
	pane := host.startQwenPane(t, command)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("native Qwen Code presented screen:\n%s", captureQwenPane(t, pane))
		}
	})
	for step := range 2 {
		awaitQwenCheckpoint(t, host, step)
		if step == 0 {
			host.waitForActiveSession(t)
		}
		if interrupt {
			if err := pane.SendKeys(t.Context(), gotmux.KeyEscape); err != nil {
				t.Fatal(err)
			}
			host.waitForQwenIdle(t, "Notification")
			break
		}
		host.provider.release <- struct{}{}
	}
	if !interrupt {
		host.waitForQwenIdle(t, "Stop", "Notification")
	}
	screen := captureQwenPane(t, pane)
	quitQwen(t, pane)
	return []byte(screen)
}

// The default approval mode asks before shell commands; Qwen Code's own
// default, auto mode, would spend a model request on its classifier. The
// dialog's first option allows the call once; Esc rejects it.
func runQwenPermissionScenarios(t *testing.T, contract hostContract, oracle string) {
	t.Helper()
	for _, allow := range []bool{true, false} {
		name := "deny"
		if allow {
			name = "allow"
		}
		t.Run(name, func(t *testing.T) {
			host := newPermissionHost(t, contract, oracle, allow)
			command, _ := host.lifecycleCommand(t)
			args := make([]string, 0, len(command.Args)+1)
			for _, arg := range command.Args {
				if arg == "--yolo" {
					args = append(args, "--approval-mode", "default")
				} else {
					args = append(args, arg)
				}
			}
			command.Args = args
			pane := host.startQwenPane(t, command)
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("native Qwen Code presented screen:\n%s", captureQwenPane(t, pane))
				}
			})
			waiting := assertPermissionWaiting(t, host)
			key := gotmux.KeyEscape
			if allow {
				key = gotmux.KeyEnter
			}
			answerQwenDialog(t, pane, key)
			host.waitForQwenIdle(t, "Stop", "Notification")
			assertPermissionOutcome(t, host, waiting, allow)
			quitQwen(t, pane)
		})
	}
}

func (host *isolatedHost) startQwenPane(t *testing.T, command *exec.Cmd) gotmux.Pane {
	t.Helper()
	server := testtmux.New(t, gotmux.NewSessionOptions{
		Name: "qwen", Size: gotmux.Size{Width: 160, Height: 45}, Program: gotmux.Exec("/bin/sh"),
	})
	panes, err := server.Tmux.Panes(t.Context())
	if err != nil || len(panes) != 1 {
		t.Fatalf("native Qwen Code initial pane: count=%d error=%v", len(panes), err)
	}
	pane := panes[0].Handle()
	if err := pane.Options().SetRemainOnExit(t.Context(), gotmux.RemainOn); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"-i"}, command.Env...)
	args = append(args, command.Path)
	args = append(args, command.Args[1:]...)
	if err := pane.Respawn(t.Context(), gotmux.RespawnOptions{
		Dir: host.work, Program: gotmux.Exec("env", args...), KillRunning: true,
	}); err != nil {
		t.Fatal(err)
	}
	return pane
}

func awaitQwenCheckpoint(t *testing.T, host *isolatedHost, expected int) {
	t.Helper()
	select {
	case step := <-host.provider.checkpoints:
		if step != expected {
			t.Fatalf("Qwen Code provider checkpoint = %d, want %d", step, expected)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("Qwen Code did not reach provider checkpoint %d\nprovider requests: %s", expected, providerRequestSummary(host.provider))
	}
}

// The interactive UI follows Stop with an idle_prompt notification, so either
// may be the latest native idle evidence after a completed turn.
func (host *isolatedHost) waitForQwenIdle(t *testing.T, events ...string) {
	t.Helper()
	host.waitForObservation(t, "native Qwen Code idle after "+strings.Join(events, " or "), func(session registry.Session) bool {
		native := session.Observations.Native
		return native != nil && slices.Contains(events, native.Event) && native.Activity != nil && *native.Activity == registry.ActivityIdle &&
			effectiveActivityMatches(session, registry.ActivityIdle) && session.Presence() == registry.PresenceLive
	})
}

func captureQwenPane(t *testing.T, pane gotmux.Pane) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := pane.Capture(ctx, gotmux.CaptureOptions{})
	if err != nil {
		t.Logf("capture native Qwen Code screen: %v", err)
	}
	return string(out)
}

// The permission_prompt notification fires before the dialog is painted, and
// the dialog subscribes to keys only after its first paint, so a key sent too
// early is lost. Repeat it until the dialog closes.
func answerQwenDialog(t *testing.T, pane gotmux.Pane, key gotmux.Key) {
	t.Helper()
	const dialog = "Yes, allow once"
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(captureQwenPane(t, pane), dialog) {
		if time.Now().After(deadline) {
			t.Fatal("native Qwen Code did not present its approval dialog")
		}
		time.Sleep(50 * time.Millisecond)
	}
	for strings.Contains(captureQwenPane(t, pane), dialog) {
		if time.Now().After(deadline) {
			t.Fatal("native Qwen Code approval dialog ignored the answer")
		}
		if err := pane.SendKeys(t.Context(), key); err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func quitQwen(t *testing.T, pane gotmux.Pane) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		state, err := pane.Info(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if state.Dead {
			if status, known := state.DeadStatus.Get(); known && status != 0 {
				t.Fatalf("native Qwen Code quit with status %d", status)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("native Qwen Code did not quit within 15s")
		}
		if err := pane.SendKeys(t.Context(), gotmux.Key("C-c")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
	}
}
