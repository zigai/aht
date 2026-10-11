package launch

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
)

func TestCloseStopsProcessesThatLeftTheHarnessProcessGroup(t *testing.T) {
	t.Parallel()
	pidFile := filepath.Join(t.TempDir(), "detached.pid")
	plan := harness.LaunchPlan{
		Executable: "/bin/sh",
		Setup:      [][]string{{"-c", "setsid sh -c 'echo $$ > \"$0\"; exec sleep 300' " + shellQuote(pidFile) + " </dev/null >/dev/null 2>&1 & while [ ! -s " + shellQuote(pidFile) + " ]; do sleep 0.01; done"}},
	}
	s, _, err := newFixtureHarness(t.Context(), t, plan, Options{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("detached process is not running before Close: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		environ, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
		if len(environ) > 0 {
			t.Fatalf("detached harness process %d survived Close", pid)
		}
	}
}
