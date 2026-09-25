//go:build linux && integration

package observer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/herdr"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
	"github.com/zigai/aht/v2/pkg/zellij"
)

func TestCurrentZellijDiscoveryAndCapture(t *testing.T) {
	zellijBinary := requireDeclaredMultiplexer(t, "zellij")
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script is required to allocate Zellij's controlling terminal")
	}

	session := fmt.Sprintf("aht-zellij-%d", time.Now().UnixNano())
	configDir := t.TempDir()
	t.Setenv("ZELLIJ_CONFIG_DIR", configDir)
	configPath := filepath.Join(configDir, "config.kdl")
	if err := os.WriteFile(configPath, []byte("show_startup_tips false\nshow_release_notes false\n"), 0o600); err != nil {
		t.Fatalf("write Zellij config: %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "zellij.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create Zellij log: %v", err)
	}
	commandLine := strings.Join([]string{
		harness.ShellQuote(zellijBinary), "--config-dir", harness.ShellQuote(configDir),
		"--session", harness.ShellQuote(session),
	}, " ")
	process := exec.Command(script, "-q", "-c", commandLine, logPath)
	process.Stdout = logFile
	process.Stderr = logFile
	stdin, err := process.StdinPipe()
	if err != nil {
		_ = logFile.Close()
		t.Fatalf("create Zellij stdin pipe: %v", err)
	}
	if err := process.Start(); err != nil {
		_ = stdin.Close()
		_ = logFile.Close()
		t.Fatalf("start Zellij session: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, zellijBinary, "--config-dir", configDir, "kill-session", session).Run()
		_ = stdin.Close()
		if process.Process != nil {
			_ = process.Process.Kill()
		}
		_ = process.Wait()
		_ = logFile.Close()
		if t.Failed() {
			t.Logf("Zellij startup log:\n%s", readProbeLog(logPath))
		}
	})

	waitForCommandSuccess(t, 15*time.Second, zellijBinary, "--config-dir", configDir, "--session", session, "action", "list-panes", "--all", "--json")
	marker := "AHT_ZELLIJ_CURRENT_VERSION_CAPTURE"
	run := exec.Command(zellijBinary, "--config-dir", configDir, "--session", session, "run", "--", "sh", "-c", "printf '%s\\n' "+harness.ShellQuote(marker)+"; sleep 30")
	output, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("create Zellij test pane: %v\n%s\n%s", err, output, readProbeLog(logPath))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pane := waitForMultiplexerPane(t, ctx, registry.MultiplexerZellij, session, strings.TrimSpace(string(output)), func(ctx context.Context) ([]mux.Pane, error) {
		return zellij.ListPanes(ctx)
	})
	waitForPaneCapture(t, ctx, pane, marker, zellij.CapturePane)
}

func TestCurrentHerdrDiscoveryAndCapture(t *testing.T) {
	herdrBinary := requireDeclaredMultiplexer(t, "herdr")

	session := fmt.Sprintf("aht-herdr-%d", time.Now().UnixNano())
	home, err := os.MkdirTemp("/tmp", "aht-herdr-home-")
	if err != nil {
		t.Fatalf("create short Herdr home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("HERDR_SESSION", session)
	logPath := filepath.Join(t.TempDir(), "herdr.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create Herdr log: %v", err)
	}
	process := exec.Command(herdrBinary, "server")
	process.Stdout = logFile
	process.Stderr = logFile
	if err := process.Start(); err != nil {
		_ = logFile.Close()
		t.Fatalf("start Herdr server: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stop := exec.CommandContext(ctx, herdrBinary, "session", "stop", session, "--json")
		stop.Env = os.Environ()
		_ = stop.Run()
		if process.Process != nil {
			_ = process.Process.Kill()
		}
		_ = process.Wait()
		_ = logFile.Close()
	})

	waitForCommandSuccess(t, 10*time.Second, herdrBinary, "api", "snapshot")
	workspace := exec.Command(herdrBinary, "workspace", "create", "--cwd", t.TempDir(), "--label", "aht-test", "--no-focus")
	workspace.Env = os.Environ()
	output, err := workspace.CombinedOutput()
	if err != nil {
		t.Fatalf("create Herdr workspace: %v\n%s\n%s", err, output, readProbeLog(logPath))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pane := waitForMultiplexerPane(t, ctx, registry.MultiplexerHerdr, session, "", func(ctx context.Context) ([]mux.Pane, error) {
		return herdr.ListPanes(ctx)
	})
	marker := "AHT_HERDR_CURRENT_VERSION_CAPTURE"
	run := exec.Command(herdrBinary, "pane", "run", pane.Location.PaneID, "printf '%s\\n' "+harness.ShellQuote(marker))
	run.Env = os.Environ()
	output, err = run.CombinedOutput()
	if err != nil {
		t.Fatalf("write Herdr test pane: %v\n%s\n%s", err, output, readProbeLog(logPath))
	}
	waitForPaneCapture(t, ctx, pane, marker, herdr.CapturePane)
}

func waitForCommandSuccess(t *testing.T, timeout time.Duration, name string, args ...string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var output []byte
	var err error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		command := exec.CommandContext(ctx, name, args...)
		command.Env = os.Environ()
		output, err = command.CombinedOutput()
		cancel()
		if err == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("command %s %s did not become ready: %v\n%s", name, strings.Join(args, " "), err, output)
}

func waitForMultiplexerPane(
	t *testing.T,
	ctx context.Context,
	kind registry.MultiplexerKind,
	session string,
	paneID string,
	list func(context.Context) ([]mux.Pane, error),
) mux.Pane {
	t.Helper()
	var lastErr error
	for ctx.Err() == nil {
		panes, err := list(ctx)
		if err != nil {
			lastErr = err
		} else {
			for _, pane := range panes {
				if pane.Location.Kind != kind || pane.Location.SessionName != session {
					continue
				}
				if paneID == "" || pane.Location.PaneID == paneID {
					return pane
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s pane for session %q was not discovered: %v", kind, session, lastErr)
	return mux.Pane{}
}

func waitForPaneCapture(
	t *testing.T,
	ctx context.Context,
	pane mux.Pane,
	marker string,
	capture func(context.Context, mux.Pane) (mux.ScreenSnapshot, error),
) {
	t.Helper()
	var last mux.ScreenSnapshot
	var lastErr error
	for ctx.Err() == nil {
		last, lastErr = capture(ctx, pane)
		if lastErr == nil && strings.Contains(last.Text, marker) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("pane %s capture did not contain %q: %v\n%s", pane.Location.PaneID, marker, lastErr, last.Text)
}

func readProbeLog(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return err.Error()
	}
	return string(data)
}

func requireDeclaredMultiplexer(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		if os.Getenv("AHT_REQUIRE_MULTIPLEXERS") != "" {
			t.Fatalf("%s is required in declared compatibility environment but is not installed: %v", name, err)
		}
		t.Skipf("%s is not installed", name)
	}
	return path
}
