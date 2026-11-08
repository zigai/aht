package launch

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
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

func TestProviderUsesAdapterTitleRecognition(t *testing.T) {
	titleRequest, err := TitleRequestFor(registry.Harness("kilo"))
	if err != nil {
		t.Fatal(err)
	}

	server, err := fakeprovider.Start(fakeprovider.Options{SideRequest: titleRequest})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	})

	for _, body := range []string{
		`{"messages":[{"role":"system","content":"You are a title generator. You output O` +
			`NLY a thread title. Nothing else."},{"role":"user","content":"Refine the parser"` +
			`}]}`,
		`{"messages":[{"role":"user","content":"Explain the phrase You are a title generator."}]}`,
	} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
			server.URL()+"/v1/chat/completions", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}

		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := io.Copy(io.Discard, response.Body); err != nil {
			t.Error(err)
		}

		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}

		if response.StatusCode != http.StatusOK {
			t.Fatalf("provider status = %d", response.StatusCode)
		}
	}

	if err := server.Close(); err != nil {
		t.Fatal(err)
	}

	requests := server.Requests()
	if len(requests) != 1 ||
		requests[0].UserText != "Explain the phrase You are a title generator." || len(server.SideRequests()) != 1 {
		t.Fatalf("conversation = %+v, side = %+v", requests, server.SideRequests())
	}
}

func TestNewRejectsUnregisteredHarness(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "root")

	_, err := New(
		t.Context(),
		Options{
			Harness:  registry.Harness("unregistered"),
			Root:     root,
			Endpoint: "http://127.0.0.1:9",
		},
	)
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
		{
			name:    "origin with plan",
			options: Options{Root: absolute, Endpoint: origin},
			hasPlan: true,
		},
		{
			name:    "origin without plan",
			options: Options{Root: absolute, Endpoint: origin},
			hasPlan: false,
		},
		{name: "no endpoint without plan", options: Options{Root: absolute}, hasPlan: false},
		{
			name:    "no endpoint with plan",
			options: Options{Root: absolute},
			hasPlan: true,
			wantErr: errEndpointRequired,
		},
		{
			name:    "relative root",
			options: Options{Root: "root", Endpoint: origin},
			hasPlan: true,
			wantErr: errRelativeRoot,
		},
		{
			name:    "empty root",
			options: Options{Endpoint: origin},
			hasPlan: true,
			wantErr: errRelativeRoot,
		},
		{
			name:    "provider path",
			options: Options{Root: absolute, Endpoint: origin + "/v1"},
			hasPlan: true,
			wantErr: errEndpoint,
		},
		{
			name:    "trailing slash",
			options: Options{Root: absolute, Endpoint: origin + "/"},
			hasPlan: true,
			wantErr: errEndpoint,
		},
		{
			name:    "query",
			options: Options{Root: absolute, Endpoint: origin + "?x=1"},
			hasPlan: true,
			wantErr: errEndpoint,
		},
		{
			name:    "fragment",
			options: Options{Root: absolute, Endpoint: origin + "#x"},
			hasPlan: true,
			wantErr: errEndpoint,
		},
		{
			name:    "unsupported scheme",
			options: Options{Root: absolute, Endpoint: "ftp://127.0.0.1"},
			hasPlan: true,
			wantErr: errEndpoint,
		},
		{
			name:    "no host",
			options: Options{Root: absolute, Endpoint: "http://"},
			hasPlan: true,
			wantErr: errEndpoint,
		},
		{
			name:    "malformed endpoint without plan",
			options: Options{Root: absolute, Endpoint: origin + "/v1"},
			hasPlan: false,
			wantErr: errEndpoint,
		},
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
		Protocol: harness.LaunchProtocolOpenAIChat,
		Env:      []string{"FIXTURE_HOME=" + filepath.Join(root, "plan"), "FIXTURE_PLAN=yes"},
	}
	options := Options{
		Root:     root,
		Endpoint: "http://127.0.0.1:9",
		Env:      []string{"FIXTURE_HOME=/from/options"},
	}

	s, planner, err := newFixtureHarness(t.Context(), t, plan, options)
	if err != nil {
		t.Fatal(err)
	}

	assertRootLayout(t, s)

	wantPaths := harness.LaunchPaths{
		Root: root,
		Home: filepath.Join(root, "home"),
		Work: filepath.Join(root, "work"),
	}
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

	if s.Store != filepath.Join(root, "sessions.json") || s.Home != wantPaths.Home ||
		s.Work != wantPaths.Work {
		t.Errorf("paths store=%q home=%q work=%q", s.Store, s.Home, s.Work)
	}

	if s.Protocol != plan.Protocol {
		t.Errorf("Protocol = %q, want %q", s.Protocol, plan.Protocol)
	}

	if s.Executable != testExecutable(t) {
		t.Errorf("Executable = %q, want the test binary", s.Executable)
	}
}

func assertRootLayout(t *testing.T, launched *Harness) {
	t.Helper()

	keys := []string{
		"TMPDIR",
		"XDG_CONFIG_HOME",
		"XDG_DATA_HOME",
		"XDG_STATE_HOME",
		"XDG_CACHE_HOME",
	}
	paths := make([]string, 0, 3+len(keys))

	paths = append(paths, launched.Root, launched.Home, launched.Work)
	for _, key := range keys {
		path, ok := envValue(launched.Env, key)
		if !ok || path == "" {
			t.Fatalf("launch omits %s", key)
		}

		paths = append(paths, path)
	}

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}

		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Errorf("%s: mode %v, want directory 0700", path, info.Mode())
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

	wantInstall := strings.Join(
		[]string{
			"--store",
			s.Store,
			"manage",
			"integrations",
			"install",
			"fixture",
			"--binary",
			s.aht,
		},
		" ",
	) + "\n"
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

	_, err := newHarness(
		t.Context(),
		Options{Harness: registry.Harness("fixture"), Root: root},
		nil,
	)
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

	if _, err := s.StartTerminal(
		t.Context(),
		"",
		gotmux.Size{Width: 80, Height: 24},
	); !errors.Is(
		err,
		ErrUnsupported,
	) {
		t.Errorf("StartTerminal() error = %v, want ErrUnsupported", err)
	}
}

func TestHeadlessAndInteractiveArgs(t *testing.T) {
	t.Parallel()

	plan := harness.LaunchPlan{
		Headless: func(prompt string) ([]string, error) {
			return []string{helperArgument, "record", prompt}, nil
		},
		Interactive: func(string) ([]string, error) {
			return nil, errLaunchRefused
		},
	}

	s, _, err := newFixtureHarness(t.Context(), t, plan, Options{})
	if err != nil {
		t.Fatal(err)
	}

	record := filepath.Join(t.TempDir(), "launch record.txt")

	args, err := s.HeadlessArgs(record)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Command(t.Context(), args...).Run(); err != nil {
		t.Fatal(err)
	}

	work, err := filepath.EvalSymlinks(s.Work)
	if err != nil {
		t.Fatal(err)
	}

	if got := readText(t, record); got != "cwd="+work+"\nhome="+s.Home+"\n" {
		t.Fatalf("headless command ran outside its launch environment: %q", got)
	}

	if _, err := s.InteractiveArgs(
		"",
	); !errors.Is(err, errLaunchRefused) ||
		errors.Is(err, ErrUnsupported) {
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

	var config net.ListenConfig

	listener, err := config.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = listener.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	cmd := s.Command(
		ctx,
		helperArgument, "group", listener.Addr().String(),
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	t.Cleanup(func() {
		cancel()

		if err := <-done; err == nil {
			t.Error("parent exited successfully instead of being canceled")
		}
	})

	accepted := make(chan net.Conn, 1)
	acceptErrors := make(chan error, 1)

	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)

		connection, err := listener.Accept()
		if err != nil {
			acceptErrors <- err
			return
		}

		accepted <- connection
	}()

	t.Cleanup(func() {
		_ = listener.Close()

		<-acceptDone

		select {
		case connection := <-accepted:
			_ = connection.Close()
		default:
		}
	})

	select {
	case connection := <-accepted:
		defer func() { _ = connection.Close() }()

		cancel()

		if err := connection.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}

		var data [1]byte
		if _, err := connection.Read(data[:]); !errors.Is(err, io.EOF) {
			t.Fatalf("background child connection after cancellation = %v, want EOF", err)
		}
	case err := <-acceptErrors:
		t.Fatalf("child connection: %v", err)
	case err := <-done:
		done <- err

		t.Fatalf("parent exited before child connected: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("child did not connect")
	}
}
