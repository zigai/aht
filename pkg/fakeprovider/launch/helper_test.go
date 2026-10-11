package launch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const helperArgument = "launch-test-helper"

const (
	sessionsFileEnv = "AHT_LAUNCH_TEST_SESSIONS"
	installLogEnv   = "AHT_LAUNCH_TEST_INSTALL_LOG"
	trackerModeEnv  = "AHT_LAUNCH_TEST_TRACKER_MODE"
	helperLifetime  = 10 * time.Minute
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == helperArgument {
		os.Exit(runHelper(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func runHelper(args []string) int {
	if len(args) == 0 {
		return 2
	}
	switch args[0] {
	case "listen":
		if len(args) != 2 {
			return 2
		}
		if err := listen("tcp", args[1]); err != nil {
			return reportHelperError(err)
		}
		return sleepHelper()
	case "sleep":
		return sleepHelper()
	case "exit":
		return 3
	case "record":
		if len(args) != 2 {
			return 2
		}
		return recordHelper(args[1])
	case "aht":
		return runFakeAHT(args[1:])
	}
	return 2
}

func sleepHelper() int {
	time.Sleep(helperLifetime)
	return 0
}

func reportHelperError(err error) int {
	fmt.Fprintln(os.Stderr, err)
	return 1
}

func recordHelper(path string) int {
	cwd, err := os.Getwd()
	if err != nil {
		return reportHelperError(err)
	}
	content := fmt.Sprintf("cwd=%s\nhome=%s\n", cwd, os.Getenv("HOME"))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil { //nolint:gosec // G703: the path is passed by the fixture that starts this helper.
		return reportHelperError(err)
	}
	return 0
}

func runFakeAHT(args []string) int {
	if len(args) < 2 || args[0] != "--store" {
		return 2
	}
	store, rest := args[1], args[2:]
	switch {
	case slices.Equal(rest, []string{"manage", "tracker", "run", "--quiet"}):
		return runTracker(store)
	case len(rest) >= 2 && rest[0] == "--json" && rest[1] == "list":
		return listSessions()
	case len(rest) >= 3 && rest[0] == "manage" && rest[1] == "integrations" && rest[2] == "install":
		return fakeInstall(args)
	}
	return 2
}

func runTracker(store string) int {
	if os.Getenv(trackerModeEnv) == "exit" {
		return 1
	}
	if err := listen("unix", store+".sock"); err != nil {
		return reportHelperError(err)
	}
	return sleepHelper()
}

func listSessions() int {
	data, err := os.ReadFile(os.Getenv(sessionsFileEnv)) //nolint:gosec // G703: the path is set by the fixture that starts this helper.
	if err != nil {
		return reportHelperError(err)
	}
	if _, err := os.Stdout.Write(data); err != nil {
		return reportHelperError(err)
	}
	return 0
}

func listen(network, address string) error {
	var config net.ListenConfig
	if _, err := config.Listen(context.Background(), network, address); err != nil {
		return fmt.Errorf("listen %s %s: %w", network, address, err)
	}
	return nil
}

func fakeInstall(args []string) int {
	log, err := os.OpenFile(os.Getenv(installLogEnv), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // G703: the path is set by the fixture that starts this helper.
	if err != nil {
		return reportHelperError(err)
	}
	if _, err := fmt.Fprintln(log, strings.Join(args, " ")); err != nil {
		return reportHelperError(errors.Join(err, log.Close()))
	}
	if err := log.Close(); err != nil {
		return reportHelperError(err)
	}
	integration := filepath.Join(os.Getenv("HOME"), ".fake-integration")
	if err := os.WriteFile(integration, []byte("installed\n"), 0o600); err != nil { //nolint:gosec // G703: the path is under the HOME that the fixture sets.
		return reportHelperError(err)
	}
	return 0
}

type fakePlanner struct {
	plan  harness.LaunchPlan
	paths harness.LaunchPaths
	model harness.LaunchModel
}

func (p *fakePlanner) LaunchPlan(paths harness.LaunchPaths, model harness.LaunchModel) (harness.LaunchPlan, error) {
	p.paths = paths
	p.model = model
	return p.plan, nil
}

func newFixtureHarness(ctx context.Context, t *testing.T, plan harness.LaunchPlan, options Options) (*Harness, *fakePlanner, error) {
	t.Helper()
	if options.Root == "" {
		options.Root = filepath.Join(t.TempDir(), "root")
	}
	if options.Endpoint == "" {
		options.Endpoint = "http://127.0.0.1:9"
	}
	if options.Harness == "" {
		options.Harness = registry.Harness("fixture")
	}
	if _, set := envValue(options.Env, installLogEnv); !set {
		options.Env = append(slices.Clone(options.Env), installLogEnv+"="+filepath.Join(t.TempDir(), "install.log"))
	}
	if plan.Executable == "" {
		plan.Executable = testExecutable(t)
	}
	planner := &fakePlanner{plan: plan}
	s, err := newHarness(ctx, options, planner)
	if err != nil {
		return nil, planner, err
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, planner, nil
}

func testExecutable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeAHT(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aht")
	script := "#!/bin/sh\nexec " + shellQuote(testExecutable(t)) + " " + helperArgument + " aht \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // reason: G306; the fake aht must be executable by the launch.
		t.Fatal(err)
	}
	return path
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func shortRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "aht-launch-test-") //nolint:usetesting // reason: Unix socket paths in /tmp must stay short.
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	return filepath.Join(dir, "root")
}

func freeAddress(t *testing.T) string {
	t.Helper()
	var config net.ListenConfig
	listener, err := config.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func accepting(ctx context.Context, address string) bool {
	dialer := net.Dialer{Timeout: time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return false
	}
	return conn.Close() == nil
}

func sessionFixture(id, cwd, nativeID string) map[string]any {
	return map[string]any{
		"id":         id,
		"harness":    "fixture",
		"cwd":        cwd,
		"session_id": nativeID,
		"observations": map[string]any{
			"native": map[string]any{
				"session_id": nativeID,
				"reporter":   map[string]any{"integration": "fixture", "version": 1, "multi_session": false},
				"raw_payload": map[string]any{
					"prompt": "hidden",
				},
			},
		},
		"liveness": map[string]any{"kind": "live", "activity": "running"},
	}
}

func writeSessions(path string, sessions ...map[string]any) error {
	data, err := json.Marshal(sessions)
	if err != nil {
		return fmt.Errorf("encode sessions: %w", err)
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write sessions: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("publish sessions: %w", err)
	}
	return nil
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func waitForPath(t *testing.T, path string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s never appeared: %v", path, ctx.Err())
		case <-ticker.C:
		}
	}
}
