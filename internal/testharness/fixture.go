// Package testharness provides isolated fixtures for harness tests.
package testharness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/harness"

	"github.com/zigai/aht/v2/pkg/fakeprovider"
)

const buildTimeout = 2 * time.Minute

var (
	errProcessExited  = errors.New("host exited before provider request")
	errProviderClosed = errors.New("provider closed before held request")
)

func Build(t *testing.T) string {
	t.Helper()

	if binary := os.Getenv("AHT_COMPAT_BINARY"); binary != "" {
		path, err := filepath.Abs(binary)
		if err != nil {
			t.Fatal(err)
		}

		return path
	}

	path := filepath.Join(t.TempDir(), "aht")

	ctx, cancel := context.WithTimeout(t.Context(), buildTimeout)
	defer cancel()

	cmd := exec.CommandContext(
		ctx,
		"go",
		"build",
		"-ldflags",
		"-X github.com/zigai/aht/v2/internal/cli.version=compat-oracle",
		"-o",
		path,
		"github.com/zigai/aht/v2",
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second

	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}

		if err != nil {
			return fmt.Errorf("kill build process group: %w", err)
		}

		return nil
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build aht: %v\n%s", err, out)
	}

	return path
}

func Isolate(t *testing.T, adapters ...harness.Adapter) string {
	t.Helper()

	if len(adapters) == 0 {
		t.Fatal("isolation requires the adapters under test")
	}

	for _, name := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "APPDATA", "USERPROFILE"} {
		t.Setenv(name, "")
	}

	for _, adapter := range adapters {
		for _, name := range adapter.Definition().Env.Config {
			t.Setenv(name, "")
		}
	}

	home := t.TempDir()
	t.Setenv("HOME", home)

	return home
}

func CheckTitleRequests(t *testing.T, adapter harness.Launchable, cases map[string]bool) {
	t.Helper()

	plan, err := adapter.LaunchPlan(harness.LaunchPaths{Root: "", Home: "", Work: ""},
		harness.LaunchModel{Endpoint: "", Model: "", APIKey: ""})
	if err != nil {
		t.Fatal(err)
	}

	for body, want := range cases {
		var value map[string]any
		if err := json.Unmarshal([]byte(body), &value); err != nil {
			t.Fatal(err)
		}

		if got := plan.TitleRequest(value); got != want {
			t.Errorf("title request %s = %t, want %t", body, got, want)
		}
	}
}

func AwaitHold(
	ctx context.Context,
	provider *fakeprovider.Server,
	done <-chan struct{},
) (*fakeprovider.Hold, error) {
	select {
	case hold, ok := <-provider.Holds():
		if !ok || hold == nil {
			return nil, errProviderClosed
		}

		return hold, nil
	case <-done:
		return nil, errProcessExited
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for provider request: %w", ctx.Err())
	}
}

// WithoutHome leaves no home directory known and moves the working directory
// into an empty directory, so any path built from a missing home resolves there.
func WithoutHome(t *testing.T, adapters ...harness.Adapter) string {
	t.Helper()
	Isolate(t, adapters...)
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")

	if home := harness.HomeDir(); home != "" {
		t.Skipf("platform reports home directory %q without HOME", home)
	}

	workDir := t.TempDir()
	t.Chdir(workDir)

	return workDir
}

func LocationPaths(locations []harness.Location, kind harness.LocationKind) []string {
	var paths []string

	for _, location := range locations {
		if location.Kind == kind && location.Scope == harness.LocationScopeGlobal {
			paths = append(paths, location.Path)
		}
	}

	return paths
}

func PlantFile(t *testing.T, workDir string, content string, elements ...string) {
	t.Helper()

	path := filepath.Join(append([]string{workDir}, elements...)...)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func PlantDatabase(
	t *testing.T,
	workDir string,
	schema string,
	row string,
	elements ...string,
) {
	t.Helper()

	path := filepath.Join(append([]string{workDir}, elements...)...)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = db.Close() }()

	for _, statement := range []string{schema, row} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
}
