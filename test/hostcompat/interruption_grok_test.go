//go:build compatibility

package hostcompat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gotmux "github.com/zigai/gotmux/tmux"

	"github.com/zigai/aht/v2/pkg/fakeprovider"
	"github.com/zigai/aht/v2/pkg/fakeprovider/launch"
	"github.com/zigai/aht/v2/pkg/registry"
)

func (host *isolatedHost) runGrokInterruption(t *testing.T, command *exec.Cmd) {
	t.Helper()

	if _, err := exec.LookPath("tmux"); err != nil && os.Getenv("AHT_TEST_TMUX_EXECUTABLE") == "" {
		t.Fatal("Grok native interruption requires tmux")
	}

	host.provider.Reset(
		fakeprovider.ScriptFunc(func(fakeprovider.Request) (fakeprovider.Reply, error) {
			return fakeprovider.Reply{Text: "Reply in progress", HoldMidStream: true}, nil
		}),
	)
	// The installed launcher can update itself. Run a disposable copy of its
	// resolved executable, never modify the developer's native installation.
	sourcePath, err := filepath.EvalSymlinks(host.launched.Executable)
	if err != nil {
		t.Fatal(err)
	}

	// Keep the native command name so report and the observer recognize the
	// process as Grok.
	nativeDir := filepath.Join(host.launched.Root, "native")
	if err := os.Mkdir(nativeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	nativePath := filepath.Join(nativeDir, "grok")
	copyExecutable(t, sourcePath, nativePath)

	nativeCommand := host.command(
		t,
		command.Env,
		"--model",
		launch.ProviderName,
		"--always-approve",
		"--disable-web-search",
		"--no-memory",
		compatibilityPrompt,
	)
	nativeCommand.Path = nativePath
	pane := host.startNativePane(t, nativeCommand)
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

	beforeReply := host.awaitHold(t, nil, 0)
	if beforeReply.Stage != fakeprovider.StageBeforeReply {
		t.Fatalf("initial Grok checkpoint = %s, want before-reply", beforeReply.Stage)
	}

	beforeReply.Release()

	midStream := host.awaitHold(t, nil, 0)
	if midStream.Stage != fakeprovider.StageMidStream {
		t.Fatalf("active Grok checkpoint = %s, want mid-stream", midStream.Stage)
	}

	host.waitForActiveSession(t)

	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(captureGrokPane(t, pane), "Reply in progress") {
		if time.Now().After(deadline) {
			t.Fatal("native Grok did not present its streamed reply")
		}

		time.Sleep(25 * time.Millisecond)
	}

	host.cancelAndQuitGrok(t, pane)
	waitForGrokExit(t, pane)

	requests := host.provider.Requests()
	if len(requests) != 1 {
		t.Fatalf(
			"interrupted native Grok sent %d model requests, want one held request",
			len(requests),
		)
	}

	if requests[0].Outcome != fakeprovider.OutcomeCanceled {
		t.Fatalf("interrupted native Grok request outcome = %s, want canceled", requests[0].Outcome)
	}
	// Process exit is only cleanup evidence; the shared caller still requires
	// SessionEnd from the installed native hook integration.
}

func captureGrokPane(t *testing.T, pane gotmux.Pane) string {
	t.Helper()

	out, err := pane.Capture(t.Context(), gotmux.CaptureOptions{})
	if err != nil {
		t.Fatal(err)
	}

	return string(out)
}

func (host *isolatedHost) cancelAndQuitGrok(t *testing.T, pane gotmux.Pane) {
	t.Helper()

	if err := pane.SendKeys(t.Context(), gotmux.Key("C-c")); err != nil {
		t.Fatal(err)
	}

	host.waitForObservation(
		t,
		"native interruption before quitting",
		func(session registry.Session) bool {
			native := session.Observations.Native

			return native != nil && native.Activity != nil &&
				*native.Activity == registry.ActivityInterrupted &&
				effectiveActivityMatches(session, registry.ActivityInterrupted)
		},
	)

	beforeQuit := captureGrokPane(t, pane)
	if err := pane.SendKeys(t.Context(), gotmux.Key("C-q")); err != nil {
		t.Fatal(err)
	}

	confirmationDeadline := time.Now().Add(800 * time.Millisecond)
	for captureGrokPane(t, pane) == beforeQuit {
		if time.Now().After(confirmationDeadline) {
			t.Fatal("native Grok did not present quit confirmation")
		}

		time.Sleep(10 * time.Millisecond)
	}

	if err := pane.SendKeys(t.Context(), gotmux.Key("C-q")); err != nil {
		t.Fatal(err)
	}
}

func waitForGrokExit(t *testing.T, pane gotmux.Pane) {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)

	for {
		state, err := pane.Info(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		status, known := state.DeadStatus.Get()
		if state.Dead {
			if known && status != 0 {
				t.Fatalf("native Grok quit with status %d", status)
			}
			// The CI pane displayed status 0 while pane_dead_status was
			// unavailable. The caller still requires SessionEnd and gone
			// presence, so a terminated process alone cannot pass.
			if !known {
				t.Log("native Grok pane has no reported exit status; checking SessionEnd")
			}

			return
		}

		if time.Now().After(deadline) {
			t.Fatalf(
				"native Grok did not quit within 15s: dead=%t status=%v",
				state.Dead,
				state.DeadStatus,
			)
		}

		time.Sleep(25 * time.Millisecond)
	}
}
