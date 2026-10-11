//go:build linux && integration

package zellij_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/zellij"
)

const (
	startingSessionRuns  = 5
	startingSessionPolls = 4
	discoveryTimeout     = 8 * time.Second
)

type discoveredSessions struct {
	mu    sync.Mutex
	names map[string]bool
}

func (d *discoveredSessions) add(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.names[name] = true
}

func (d *discoveredSessions) has(name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.names[name]
}

func TestListPanesDoesNotCrashStartingZellijSessions(t *testing.T) {
	zellijBinary, err := exec.LookPath("zellij")
	if err != nil {
		t.Skip("zellij is not installed")
	}
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script is required to allocate Zellij's controlling terminal")
	}
	root := isolateZellij(t)
	logPath := filepath.Join(root, "tmp", fmt.Sprintf("zellij-%d", os.Getuid()), "zellij-log", "zellij.log")

	found := &discoveredSessions{names: make(map[string]bool)}
	ctx := pollSessions(t, found)

	const panicSite = "zellij-server/src/lib.rs:1462"
	crashes := 0
	base := "base"
	startSession(t, script, zellijBinary, root, base)
	waitDiscovered(t, found, base)

	for run := range startingSessionRuns {
		session := fmt.Sprintf("start-%d-%d", time.Now().UnixNano()%1_000_000, run)
		stop := startSession(t, script, zellijBinary, root, session)
		discovered := waitDiscovered(t, found, session)
		stop()
		if total := strings.Count(readFile(logPath), panicSite); total > crashes {
			t.Errorf("run %d: zellij server panicked at %s while aht polled (session discovered: %t)", run, panicSite, discovered)
			crashes = total
			continue
		}
		if !discovered {
			t.Errorf("run %d: session %q was not discovered within %s", run, session, discoveryTimeout)
		}
	}
	t.Logf("%d of %d session starts crashed", crashes, startingSessionRuns)

	requireCapturable(ctx, t, base)
}

func pollSessions(t *testing.T, found *discoveredSessions) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	var pollers sync.WaitGroup
	for range startingSessionPolls {
		pollers.Go(func() {
			for ctx.Err() == nil {
				panes, _ := zellij.ListPanes(ctx)
				for _, pane := range panes {
					found.add(pane.Location.SessionName)
				}
			}
		})
	}
	t.Cleanup(func() {
		cancel()
		pollers.Wait()
	})
	return ctx
}

func requireCapturable(ctx context.Context, t *testing.T, session string) {
	t.Helper()
	deadline := time.Now().Add(discoveryTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		panes, listErr := zellij.ListPanes(ctx)
		lastErr = listErr
		for _, pane := range panes {
			if pane.Location.SessionName != session {
				continue
			}
			if _, err := zellij.CapturePane(ctx, pane); err != nil {
				lastErr = err
				continue
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("established session %q was not discoverable and capturable after other sessions started: %v", session, lastErr)
}

func waitDiscovered(t *testing.T, found *discoveredSessions, session string) bool {
	t.Helper()
	deadline := time.Now().Add(discoveryTimeout)
	for !found.has(session) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	return found.has(session)
}

func isolateZellij(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "azs-") //nolint:usetesting // reason: Unix socket paths under t.TempDir exceed the 108 byte limit; cleanup is registered below.
	if err != nil {
		t.Fatalf("create isolated Zellij root: %v", err)
	}
	t.Cleanup(func() {
		for range 100 {
			_ = os.RemoveAll(root)
			if _, err := os.Lstat(root); errors.Is(err, fs.ErrNotExist) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Errorf("remove isolated Zellij root %s", root)
	})
	for _, dir := range []string{"sock", "config", "home", "tmp", "run"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatalf("create isolated Zellij %s dir: %v", dir, err)
		}
	}
	config := "show_startup_tips false\nshow_release_notes false\n"
	if err := os.WriteFile(filepath.Join(root, "config", "config.kdl"), []byte(config), 0o600); err != nil {
		t.Fatalf("write Zellij config: %v", err)
	}
	t.Setenv("ZELLIJ_SOCKET_DIR", filepath.Join(root, "sock"))
	t.Setenv("ZELLIJ_CONFIG_DIR", filepath.Join(root, "config"))
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("TMPDIR", filepath.Join(root, "tmp"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "run"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "home", ".cache"))
	for _, name := range []string{"ZELLIJ", "ZELLIJ_SESSION_NAME", "ZELLIJ_PANE_ID"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
	}
	return root
}

func startSession(t *testing.T, script string, zellijBinary string, root string, session string) func() {
	t.Helper()
	configDir := filepath.Join(root, "config")
	commandLine := strings.Join([]string{zellijBinary, "--config-dir", configDir, "--session", session}, " ")
	process := exec.CommandContext(t.Context(), script, "-q", "-c", commandLine, "/dev/null")
	stdin, err := process.StdinPipe()
	if err != nil {
		t.Fatalf("create Zellij stdin pipe: %v", err)
	}
	if err := process.Start(); err != nil {
		t.Fatalf("start Zellij session: %v", err)
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			socketPath := filepath.Join(root, "sock", "contract_version_1", session)
			for ctx.Err() == nil {
				_ = exec.CommandContext(ctx, zellijBinary, "--config-dir", configDir, "kill-session", session).Run()
				if _, err := os.Lstat(socketPath); errors.Is(err, fs.ErrNotExist) {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			_ = stdin.Close()
			_ = process.Process.Kill()
			_ = process.Wait()
		})
	}
	t.Cleanup(stop)
	return stop
}

func readFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}
