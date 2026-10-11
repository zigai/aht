//go:build integration

package launch

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	gotmux "github.com/zigai/gotmux/tmux"

	"github.com/zigai/aht/v2/internal/harness"
)

func registerTerminalCleanup(t *testing.T, term *Terminal) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := term.Close(ctx); err != nil {
			t.Error(err)
		}
	})
}

func TestTerminalCaptureSendAndClose(t *testing.T) {
	ctx := t.Context()
	argv := []string{"/bin/sh", "-c", "printf ready; read line; printf 'got %s' \"$line\"; sleep 30"}
	term, err := startTerminal(ctx, argv, t.TempDir(), gotmux.Size{Width: 80, Height: 24})
	if err != nil {
		t.Fatal(err)
	}
	registerTerminalCleanup(t, term)

	if _, err := term.WaitForText(ctx, "ready"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendText(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(ctx, gotmux.KeyEnter); err != nil {
		t.Fatal(err)
	}
	if _, err := term.WaitForText(ctx, "got hello"); err != nil {
		t.Fatal(err)
	}
	if exited, err := term.Exited(ctx); err != nil || exited {
		t.Fatalf("Exited() = %t, %v; want a running program", exited, err)
	}
	if want := []string{term.tmux, "-S", term.Socket(), "attach"}; !slices.Equal(term.AttachCommand(), want) {
		t.Fatalf("AttachCommand() = %q, want %q", term.AttachCommand(), want)
	}
	if _, err := os.Stat(term.Socket()); err != nil {
		t.Fatalf("socket missing while running: %v", err)
	}

	if err := term.Close(ctx); err != nil {
		t.Fatal(err)
	}
	assertTerminalReleased(t, term)
}

func assertTerminalReleased(t *testing.T, term *Terminal) {
	t.Helper()
	if _, err := os.Stat(term.directory); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("socket directory survived Close: %v", err)
	}
	if _, err := term.server.Probe(t.Context()); !errors.Is(err, gotmux.ErrNoServer) {
		t.Fatalf("tmux server still answers after Close: %v", err)
	}
}

func TestTerminalReportsExitedProgram(t *testing.T) {
	ctx := t.Context()
	argv := []string{"/bin/sh", "-c", "printf done; read line"}
	term, err := startTerminal(ctx, argv, t.TempDir(), gotmux.Size{Width: 80, Height: 24})
	if err != nil {
		t.Fatal(err)
	}
	registerTerminalCleanup(t, term)

	if _, err := term.WaitForText(ctx, "done"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendText(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(ctx, gotmux.KeyEnter); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		exited, err := term.Exited(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if exited {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("program did not exit: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestStartTerminalRunsHarnessWithLaunchEnvironmentOnly(t *testing.T) {
	t.Setenv("AHT_LAUNCH_TEST_LEAK", "leaked")
	ctx := t.Context()
	plan := harness.LaunchPlan{
		Executable: "/bin/sh",
		Interactive: func(string) ([]string, error) {
			return []string{"-c", `printf 'cwd=%s home=%s leak=%s' "$(pwd)" "$HOME" "${AHT_LAUNCH_TEST_LEAK-unset}"; sleep 30`}, nil
		},
	}
	s, _, err := newFixtureHarness(ctx, t, plan, Options{})
	if err != nil {
		t.Fatal(err)
	}
	term, err := s.StartTerminal(ctx, "", gotmux.Size{Width: 240, Height: 24})
	if err != nil {
		t.Fatal(err)
	}
	registerTerminalCleanup(t, term)

	work, err := filepath.EvalSymlinks(s.Work)
	if err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(s.Home)
	if err != nil {
		t.Fatal(err)
	}
	want := "cwd=" + work + " home=" + home + " leak=unset"
	if _, err := term.WaitForText(ctx, want); err != nil {
		t.Fatal(err)
	}
}
