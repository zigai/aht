//go:build linux

package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var errManagerTestFailure = errors.New("manager test failure")

type recordingExecutor struct {
	calls  [][]string
	err    error
	failAt int
}

func (r *recordingExecutor) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	r.calls = append(r.calls, call)
	if r.err != nil {
		return nil, r.err
	}
	if r.failAt > 0 && len(r.calls) == r.failAt {
		return nil, errManagerTestFailure
	}
	return nil, nil
}

func TestDefaultObserverServiceIntervalIsResponsive(t *testing.T) {
	t.Parallel()

	options, err := normalizeOptions(Options{Binary: "/bin/aht", StorePath: "state.json"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Interval != 300*time.Millisecond {
		t.Fatalf("default observer service interval = %s, want 300ms", options.Interval)
	}
}

func TestRenderSystemdUnit(t *testing.T) {
	got, err := RenderSystemdUnit(Options{Binary: "/tmp/agent sessions", StorePath: "/tmp/state.json", Interval: 3 * time.Second, GracePeriod: 0})
	if err != nil {
		t.Fatal(err)
	}
	want := "# aht managed observer service\n# version: 7\n[Unit]\nDescription=AHT observer\n\n[Service]\nExecStart=\"/tmp/agent sessions\" --store /tmp/state.json manage tracker run --interval 3s --grace-period 0s --quiet\nRestart=on-failure\n\n[Install]\nWantedBy=default.target\n"
	if got != want {
		t.Fatalf("rendered unit = %q, want %q", got, want)
	}
}

func TestRenderSystemdUnitEscapesLiteralPercentSigns(t *testing.T) {
	got, err := RenderSystemdUnit(Options{Binary: "/tmp/%h/aht", StorePath: "/tmp/%u/state.json", Interval: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "ExecStart=/tmp/%%h/aht --store /tmp/%%u/state.json ") {
		t.Fatalf("systemd specifiers were not escaped: %s", got)
	}
}

func TestInstallUsesAtomicContentAndManagerArgv(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	executor := &recordingExecutor{}
	options := Options{Binary: "/bin/aht", StorePath: filepath.Join(config, "store.json"), Interval: 3 * time.Second}
	result, err := New(executor).Install(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || !result.Current || !result.Installed {
		t.Fatalf("unexpected result: %+v", result)
	}
	content, err := os.ReadFile(filepath.Join(config, "systemd", "user", linuxUnitName))
	if err != nil {
		t.Fatal(err)
	}
	if !isManaged(string(content)) {
		t.Fatal("managed marker missing")
	}
	wantCalls := [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", "--now", linuxUnitName},
	}
	if !reflect.DeepEqual(executor.calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", executor.calls, wantCalls)
	}
}

func TestInstallDryRunAndForeignRefusal(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	path := filepath.Join(config, "systemd", "user", linuxUnitName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(&recordingExecutor{}).Update(context.Background(), Options{Binary: "/bin/aht", StorePath: "/tmp/store"})
	if !errors.Is(err, ErrForeign) {
		t.Fatalf("error = %v, want ErrForeign", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{}
	result, err := New(executor).Install(context.Background(), Options{Binary: "/bin/aht", StorePath: "/tmp/store", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed || result.Installed {
		t.Fatalf("dry-run mutated result: %+v", result)
	}
	if len(executor.calls) != 0 {
		t.Fatalf("dry-run manager calls = %#v", executor.calls)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run path error = %v", err)
	}
}

func TestStatusManagerStoppedIsRepresented(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	executor := &recordingExecutor{}
	options := Options{Binary: "/bin/aht", StorePath: "/tmp/store"}
	if _, err := New(executor).Install(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	executor.err = exec.CommandContext(t.Context(), "sh", "-c", "exit 3").Run()
	if executor.err == nil {
		t.Fatal("expected inactive exit status")
	}
	result, err := New(executor).Status(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if result.Running {
		t.Fatalf("stopped service marked running: %+v", result)
	}
	if result.Message != "not running" {
		t.Fatalf("message = %q", result.Message)
	}
}

func TestStatusPropagatesManagerCancellation(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	options := Options{Binary: "/bin/aht", StorePath: "/tmp/store"}
	manager := New(&recordingExecutor{})
	if _, err := manager.Install(context.Background(), options); err != nil {
		t.Fatal(err)
	}

	_, err := New(&recordingExecutor{err: context.Canceled}).Status(context.Background(), options)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Status() error = %v, want context.Canceled", err)
	}
}

func TestStatusReportsRunningStaleService(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	options := Options{Binary: "/bin/aht", StorePath: "/tmp/store"}
	path := filepath.Join(config, "systemd", "user", linuxUnitName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# "+ManagedMarker+"\n# version: 2\nstale"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := New(&recordingExecutor{}).Status(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if result.Current || !result.Running || result.Message != "stale; running" {
		t.Fatalf("stale service status = %+v", result)
	}
}

func TestUpdateRestartsManagedService(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	options := Options{Binary: "/bin/aht", StorePath: "/tmp/store"}
	if err := os.MkdirAll(filepath.Join(config, "systemd", "user"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, "systemd", "user", linuxUnitName)
	if err := os.WriteFile(path, []byte("# "+ManagedMarker+"\n# version: 5\nstale"), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{}
	result, err := New(executor).Update(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Message != "updated" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(executor.calls) != 2 || executor.calls[1][2] != "restart" {
		t.Fatalf("calls = %#v, want daemon-reload and restart", executor.calls)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "# "+ManagedMarker) || !strings.Contains(string(content), "# version: 7") || !strings.Contains(string(content), " manage tracker run ") {
		t.Fatalf("updated service did not migrate command surface: %s", content)
	}
}

func TestUpdateCurrentRunningServiceRestartsExecutable(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	options := Options{Binary: "/bin/aht", StorePath: "/tmp/store"}
	executor := &recordingExecutor{}
	manager := New(executor)
	if _, err := manager.Install(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	executor.calls = nil
	result, err := manager.Update(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || !result.Installed || !result.Current || !result.Running || result.Message != "restarted" {
		t.Fatalf("unexpected update result: %+v", result)
	}
	if len(executor.calls) != 2 || executor.calls[1][2] != "restart" {
		t.Fatalf("manager calls = %#v, want status and restart", executor.calls)
	}
}

func TestUpdateCurrentRunningServiceDryRunAndFailure(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	options := Options{Binary: "/bin/aht", StorePath: "/tmp/store"}
	executor := &recordingExecutor{}
	manager := New(executor)
	if _, err := manager.Install(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	executor.calls = nil
	options.DryRun = true
	result, err := manager.Update(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed || result.Message != "would restart" || len(executor.calls) != 1 {
		t.Fatalf("dry run = %+v, calls = %#v", result, executor.calls)
	}
	options.DryRun = false
	executor.calls = nil
	executor.failAt = 2
	result, err = manager.Update(t.Context(), options)
	if !errors.Is(err, errManagerTestFailure) || result.Changed {
		t.Fatalf("failed restart = %+v, error = %v", result, err)
	}
}

func TestUpdateManagerFailureRestoresRetryableDefinition(t *testing.T) {
	for _, test := range []struct {
		name   string
		failAt int
	}{{name: "reload", failAt: 1}, {name: "restart", failAt: 2}} {
		t.Run(test.name, func(t *testing.T) {
			requireRetryableUpdateAfterManagerFailure(t, test.failAt)
		})
	}
}

func requireRetryableUpdateAfterManagerFailure(t *testing.T, failAt int) {
	t.Helper()
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	path := filepath.Join(config, "systemd", "user", linuxUnitName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	previous := []byte("# " + ManagedMarker + "\n# version: 2\nstale\n")
	//nolint:gosec // The fixture mirrors systemd's intentionally world-readable user-unit mode.
	if err := os.WriteFile(path, previous, 0o644); err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{failAt: failAt}
	manager := New(executor)
	options := Options{Binary: "/bin/aht", StorePath: "/tmp/store"}
	if _, err := manager.Update(context.Background(), options); !errors.Is(err, errManagerTestFailure) {
		t.Fatalf("Update() error = %v, want manager failure", err)
	}
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != string(previous) {
		t.Fatalf("service definition after rollback = %q, want %q", restored, previous)
	}
	executor.calls = nil
	executor.failAt = 0
	result, err := manager.Update(context.Background(), options)
	if err != nil {
		t.Fatalf("retry Update() error = %v", err)
	}
	if !result.Changed || !result.Current {
		t.Fatalf("retry result = %#v", result)
	}
}

func TestInstallManagerFailureRemovesPublishedDefinition(t *testing.T) {
	for _, test := range []struct {
		name   string
		failAt int
	}{{name: "reload", failAt: 1}, {name: "load", failAt: 2}} {
		t.Run(test.name, func(t *testing.T) {
			requireRetryableInstallAfterManagerFailure(t, test.failAt)
		})
	}
}

func requireRetryableInstallAfterManagerFailure(t *testing.T, failAt int) {
	t.Helper()
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	executor := &recordingExecutor{failAt: failAt}
	manager := New(executor)
	options := Options{Binary: "/bin/aht", StorePath: "/tmp/store"}
	result, err := manager.Install(context.Background(), options)
	if !errors.Is(err, errManagerTestFailure) {
		t.Fatalf("Install() error = %v, want manager failure", err)
	}
	if _, statErr := os.Stat(result.Path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed install definition remains: %v", statErr)
	}
	executor.calls = nil
	executor.failAt = 0
	if _, err := manager.Install(context.Background(), options); err != nil {
		t.Fatalf("retry Install() error = %v", err)
	}
}

func TestUninstallRemovesManagedServiceAndIsIdempotent(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	options := Options{Binary: "/bin/aht", StorePath: "/tmp/store"}
	executor := &recordingExecutor{}
	manager := New(executor)
	installed, err := manager.Install(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	executor.calls = nil
	result, err := manager.Uninstall(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Installed || result.Message != "uninstalled" {
		t.Fatalf("unexpected uninstall result: %+v", result)
	}
	if _, err := os.Stat(installed.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed service remains: %v", err)
	}
	if len(executor.calls) == 0 {
		t.Fatal("uninstall did not invoke the service manager")
	}

	executor.calls = nil
	result, err = manager.Uninstall(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed || result.Message != "not installed" || len(executor.calls) != 0 {
		t.Fatalf("second uninstall was not idempotent: %+v calls=%v", result, executor.calls)
	}
}

func TestUninstallReloadFailureRestoresRetryableDefinition(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	executor := &recordingExecutor{}
	manager := New(executor)
	options := Options{Binary: "/bin/aht", StorePath: "/tmp/store"}
	installed, err := manager.Install(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	executor.calls = nil
	executor.failAt = 2
	if _, err := manager.Uninstall(context.Background(), options); !errors.Is(err, errManagerTestFailure) {
		t.Fatalf("Uninstall() error = %v, want manager failure", err)
	}
	if _, err := os.Stat(installed.Path); err != nil {
		t.Fatalf("service definition was not restored: %v", err)
	}
	executor.calls = nil
	executor.failAt = 0
	result, err := manager.Uninstall(context.Background(), options)
	if err != nil {
		t.Fatalf("retry Uninstall() error = %v", err)
	}
	if !result.Changed || result.Installed {
		t.Fatalf("retry uninstall result = %#v", result)
	}
}
