package launch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/testdir"
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

		return reportHelperError(listen("tcp", args[1]))
	case "sleep":
		return sleepHelper()
	case "exit":
		return 3
	case "group":
		if len(args) != 2 {
			return 2
		}

		child := exec.CommandContext(
			context.Background(),
			os.Args[0],
			helperArgument,
			"connect",
			args[1],
		)
		if err := child.Run(); err != nil {
			return reportHelperError(err)
		}

		return 0
	case "connect":
		if len(args) != 2 {
			return 2
		}

		var dialer net.Dialer

		connection, err := dialer.DialContext(context.Background(), "tcp", args[1])
		if err != nil {
			return reportHelperError(err)
		}

		defer func() { _ = connection.Close() }()

		_, err = io.Copy(io.Discard, connection)
		if err != nil {
			return reportHelperError(err)
		}

		return 0
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
	if err := os.WriteFile(
		path,
		[]byte(content),
		0o600,
	); err != nil {
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

	return reportHelperError(listen("unix", store+".sock"))
}

func listSessions() int {
	data, err := os.ReadFile(
		os.Getenv(sessionsFileEnv),
	)
	if err != nil {
		return reportHelperError(err)
	}

	if _, err := os.Stdout.Write(data); err != nil {
		return reportHelperError(err)
	}

	return 0
}

func listen(network string, address string) error {
	var config net.ListenConfig

	listener, err := config.Listen(context.Background(), network, address)
	if err != nil {
		return fmt.Errorf("listen %s %s: %w", network, address, err)
	}

	defer func() { _ = listener.Close() }()

	runtime.GC()

	for {
		connection, err := listener.Accept()
		if err != nil {
			return fmt.Errorf("accept %s %s: %w", network, address, err)
		}

		if err := connection.Close(); err != nil {
			return fmt.Errorf("close connection: %w", err)
		}
	}
}

func fakeInstall(args []string) int {
	log, err := os.OpenFile(
		os.Getenv(installLogEnv),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o600,
	)
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
	if err := os.WriteFile(
		integration,
		[]byte("installed\n"),
		0o600,
	); err != nil {
		return reportHelperError(err)
	}

	return 0
}

type fakePlanner struct {
	plan  Plan
	paths Paths
	model Model
}

func (p *fakePlanner) LaunchPlan(
	paths Paths,
	model Model,
) (Plan, error) {
	p.paths = paths
	p.model = model

	return p.plan, nil
}

func newFixtureHarness(
	ctx context.Context,
	t *testing.T,
	plan Plan,
	opts Options,
) (*Harness, *fakePlanner, error) {
	t.Helper()

	if opts.Root == "" {
		opts.Root = filepath.Join(t.TempDir(), "root")
	}

	if opts.Endpoint == "" {
		opts.Endpoint = "http://127.0.0.1:9"
	}

	if opts.Harness == "" {
		opts.Harness = registry.Harness("fixture")
	}

	if _, set := envValue(opts.Env, installLogEnv); !set {
		opts.Env = append(
			slices.Clone(opts.Env),
			installLogEnv+"="+filepath.Join(t.TempDir(), "install.log"),
		)
	}

	if plan.Executable == "" {
		plan.Executable = testExecutable(t)
	}

	planner := &fakePlanner{plan: plan}

	s, err := newHarness(ctx, opts, planner.LaunchPlan)
	if err != nil {
		return nil, planner, err
	}

	t.Cleanup(func() {
		if err := s.Close(ctx); err != nil {
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

	script := "#!/bin/sh\nexec " + shellQuote(
		testExecutable(t),
	) + " " + helperArgument + " aht \"$@\"\n"
	if err := os.WriteFile(
		path,
		[]byte(script),
		0o700,
	); err != nil {
		t.Fatal(err)
	}

	return path
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func shortRoot(t *testing.T) string {
	t.Helper()

	dir := testdir.Short(t, "aht-launch-test-")

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

func sessionFixture(id string, cwd string, nativeID string) map[string]any {
	return map[string]any{
		"id":         id,
		"harness":    "fixture",
		"cwd":        cwd,
		"session_id": nativeID,
		"observations": map[string]any{
			"native": map[string]any{
				"session_id": nativeID,
				"reporter": map[string]any{
					"integration":   "fixture",
					"version":       1,
					"multi_session": false,
				},
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
