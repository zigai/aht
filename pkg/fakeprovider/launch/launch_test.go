package launch

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	gotmux "github.com/zigai/gotmux/tmux"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/fakeprovider"
	"github.com/zigai/aht/v2/pkg/registry"
)

var errLaunchRefused = errors.New("launch refused by fixture")

func TestNewRejectsUnregisteredHarness(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "root")
	_, err := New(t.Context(), Options{Harness: registry.Harness("unregistered"), Root: root, Endpoint: "http://127.0.0.1:9"})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("New() error = %v, want ErrUnsupported", err)
	}
	if _, statErr := os.Stat(root); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("unregistered harness created the root: %v", statErr)
	}
}

func TestOptionsValidate(t *testing.T) {
	t.Parallel()
	absolute := filepath.Join(t.TempDir(), "root")
	const origin = "http://127.0.0.1:8080"
	tests := []struct {
		name    string
		options Options
		hasPlan bool
		wantErr error
	}{
		{name: "origin with plan", options: Options{Root: absolute, Endpoint: origin}, hasPlan: true},
		{name: "origin without plan", options: Options{Root: absolute, Endpoint: origin}, hasPlan: false},
		{name: "no endpoint without plan", options: Options{Root: absolute}, hasPlan: false},
		{name: "no endpoint with plan", options: Options{Root: absolute}, hasPlan: true, wantErr: errEndpointRequired},
		{name: "relative root", options: Options{Root: "root", Endpoint: origin}, hasPlan: true, wantErr: errRelativeRoot},
		{name: "empty root", options: Options{Endpoint: origin}, hasPlan: true, wantErr: errRelativeRoot},
		{name: "provider path", options: Options{Root: absolute, Endpoint: origin + "/v1"}, hasPlan: true, wantErr: errEndpoint},
		{name: "trailing slash", options: Options{Root: absolute, Endpoint: origin + "/"}, hasPlan: true, wantErr: errEndpoint},
		{name: "query", options: Options{Root: absolute, Endpoint: origin + "?x=1"}, hasPlan: true, wantErr: errEndpoint},
		{name: "fragment", options: Options{Root: absolute, Endpoint: origin + "#x"}, hasPlan: true, wantErr: errEndpoint},
		{name: "unsupported scheme", options: Options{Root: absolute, Endpoint: "ftp://127.0.0.1"}, hasPlan: true, wantErr: errEndpoint},
		{name: "no host", options: Options{Root: absolute, Endpoint: "http://"}, hasPlan: true, wantErr: errEndpoint},
		{name: "malformed endpoint without plan", options: Options{Root: absolute, Endpoint: origin + "/v1"}, hasPlan: false, wantErr: errEndpoint},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.options.validate(test.hasPlan)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("validate() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestNewRejectsNonEmptyRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := newFixtureHarness(t.Context(), t, harness.LaunchPlan{}, Options{Root: root})
	if !errors.Is(err, errRootNotEmpty) {
		t.Fatalf("New() error = %v, want errRootNotEmpty", err)
	}
}

func TestNewBuildsLayoutEnvironmentAndModel(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "root")
	plan := harness.LaunchPlan{
		Protocol: fakeprovider.ProtocolOpenAIChat,
		Env:      []string{"FIXTURE_HOME=" + filepath.Join(root, "plan"), "FIXTURE_PLAN=yes"},
	}
	options := Options{Root: root, Endpoint: "http://127.0.0.1:9", Env: []string{"FIXTURE_HOME=/from/options"}}
	s, planner, err := newFixtureHarness(t.Context(), t, plan, options)
	if err != nil {
		t.Fatal(err)
	}
	assertRootLayout(t, root)
	wantPaths := harness.LaunchPaths{Root: root, Home: filepath.Join(root, "home"), Work: filepath.Join(root, "work")}
	if planner.paths != wantPaths {
		t.Errorf("plan paths = %+v, want %+v", planner.paths, wantPaths)
	}
	wantModel := harness.LaunchModel{Endpoint: "http://127.0.0.1:9", Model: "fake", APIKey: "fake"}
	if planner.model != wantModel {
		t.Errorf("plan model = %+v, want %+v", planner.model, wantModel)
	}
	assertEnvironment(t, s.Env, map[string]string{
		"HOME":            filepath.Join(root, "home"),
		"XDG_CONFIG_HOME": filepath.Join(root, "config"),
		"TMPDIR":          filepath.Join(root, "tmp"),
		"AHT_STORE":       filepath.Join(root, "sessions.json"),
		"FIXTURE_HOME":    "/from/options",
		"FIXTURE_PLAN":    "yes",
	})
	if s.Store != filepath.Join(root, "sessions.json") || s.Home != wantPaths.Home || s.Work != wantPaths.Work {
		t.Errorf("paths store=%q home=%q work=%q", s.Store, s.Home, s.Work)
	}
	if s.Protocol != plan.Protocol {
		t.Errorf("Protocol = %q, want %q", s.Protocol, plan.Protocol)
	}
	if s.Executable != testExecutable(t) {
		t.Errorf("Executable = %q, want the test binary", s.Executable)
	}
}

func assertRootLayout(t *testing.T, root string) {
	t.Helper()
	for _, name := range rootDirectories {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Errorf("%s: mode %v, want directory 0700", name, info.Mode())
		}
	}
}

func assertEnvironment(t *testing.T, env []string, want map[string]string) {
	t.Helper()
	for name, value := range want {
		if got, ok := envValue(env, name); !ok || got != value {
			t.Errorf("%s = %q (present %t), want %q", name, got, ok, value)
		}
	}
}

func TestNewInstallsIntegrationBeforePlanFiles(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "root")
	installLog := filepath.Join(t.TempDir(), "install.log")
	recordPath := filepath.Join(t.TempDir(), "record.txt")
	integration := filepath.Join(root, "home", ".fake-integration")
	plan := harness.LaunchPlan{
		Files: []harness.LaunchFile{{
			Path: integration,
			Update: func(existing []byte) ([]byte, error) {
				return append(slices.Clone(existing), "configured\n"...), nil
			},
		}},
		Setup: [][]string{{helperArgument, "record", recordPath}},
	}
	options := Options{Root: root, AHT: fakeAHT(t), Env: []string{installLogEnv + "=" + installLog}}
	s, _, err := newFixtureHarness(t.Context(), t, plan, options)
	if err != nil {
		t.Fatal(err)
	}
	if got := readText(t, integration); got != "installed\nconfigured\n" {
		t.Errorf("integration file = %q, want install output followed by plan output", got)
	}
	wantInstall := strings.Join([]string{"--store", s.Store, "manage", "integrations", "install", "fixture", "--binary", s.aht}, " ") + "\n"
	if got := readText(t, installLog); got != wantInstall {
		t.Errorf("install argv = %q, want %q", got, wantInstall)
	}
	work, err := filepath.EvalSymlinks(s.Work)
	if err != nil {
		t.Fatal(err)
	}
	wantRecord := "cwd=" + work + "\nhome=" + s.Home + "\n"
	if got := readText(t, recordPath); got != wantRecord {
		t.Errorf("setup ran with %q, want %q", got, wantRecord)
	}
}

func TestNewReportsSetupFailure(t *testing.T) {
	t.Parallel()
	plan := harness.LaunchPlan{Setup: [][]string{{helperArgument, "exit"}}}
	_, _, err := newFixtureHarness(t.Context(), t, plan, Options{})
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if !ok || exitErr.ExitCode() != 3 {
		t.Fatalf("New() error = %v, want the setup exit status 3", err)
	}
}

func TestNewWithoutPlanNeedsExecutable(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "root")
	_, err := newHarness(t.Context(), Options{Harness: registry.Harness("fixture"), Root: root}, nil)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("newHarness() error = %v, want ErrUnsupported", err)
	}
	if _, statErr := os.Stat(root); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("rejected run created the root: %v", statErr)
	}
}

func TestNewWithoutPlanLaunchesNothing(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "root")
	options := Options{
		Harness:    registry.Harness("fixture"),
		Root:       root,
		Executable: testExecutable(t),
		Env:        []string{"FIXTURE_MODE=discovery"},
	}
	s, err := newHarness(t.Context(), options, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	if s.Protocol != "" {
		t.Errorf("Protocol = %q, want empty", s.Protocol)
	}
	if got, _ := envValue(s.Env, "FIXTURE_MODE"); got != "discovery" {
		t.Errorf("FIXTURE_MODE = %q, want discovery", got)
	}
	if _, err := s.HeadlessArgs("prompt"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("HeadlessArgs() error = %v, want ErrUnsupported", err)
	}
	if _, err := s.StartTerminal(t.Context(), "", gotmux.Size{Width: 80, Height: 24}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("StartTerminal() error = %v, want ErrUnsupported", err)
	}
}

func TestHeadlessAndInteractiveArgs(t *testing.T) {
	t.Parallel()
	plan := harness.LaunchPlan{
		Headless: func(prompt string) ([]string, error) {
			return []string{"run", prompt}, nil
		},
		Interactive: func(string) ([]string, error) {
			return nil, errLaunchRefused
		},
	}
	s, _, err := newFixtureHarness(t.Context(), t, plan, Options{})
	if err != nil {
		t.Fatal(err)
	}
	args, err := s.HeadlessArgs("hello")
	if err != nil || !slices.Equal(args, []string{"run", "hello"}) {
		t.Fatalf("HeadlessArgs() = %q, %v; want run hello", args, err)
	}
	if _, err := s.InteractiveArgs(""); !errors.Is(err, errLaunchRefused) || errors.Is(err, ErrUnsupported) {
		t.Fatalf("InteractiveArgs() error = %v, want the plan's own error", err)
	}

	withoutHeadless, _, err := newFixtureHarness(t.Context(), t, harness.LaunchPlan{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := withoutHeadless.HeadlessArgs("hello"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("HeadlessArgs() without a headless launch error = %v, want ErrUnsupported", err)
	}
}

func TestCommandCancellationKillsProcessGroup(t *testing.T) {
	t.Parallel()
	s, _, err := newFixtureHarness(t.Context(), t, harness.LaunchPlan{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s.Executable = "/bin/sh"
	ready := filepath.Join(s.Work, "ready")
	marker := filepath.Join(s.Work, "marker")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd := s.Command(ctx, "-c", "touch ready; (sleep 0.5; touch marker) & sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitForPath(t, ready)
	cancel()
	_ = cmd.Wait()
	time.Sleep(time.Second)
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("background child survived cancellation: stat error %v", err)
	}
}
