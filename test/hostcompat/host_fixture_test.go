//go:build compatibility

package hostcompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gotmux "github.com/zigai/gotmux/tmux"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/testharness"
	"github.com/zigai/aht/v2/internal/testtmux"
	"github.com/zigai/aht/v2/pkg/fakeprovider"
	"github.com/zigai/aht/v2/pkg/fakeprovider/launch"
)

const (
	fakeModel  = "fake"
	fakeAPIKey = "fake"
)

type isolatedHost struct {
	launched     *launch.Harness
	contract     hostContract
	provider     *fakeprovider.Server
	aht          string
	nativeEvents string
	callID       string
	interrupt    bool
}

func newIsolatedHost(t *testing.T, contract hostContract, oracle string, hold bool) *isolatedHost {
	t.Helper()

	titleRequest, err := launch.TitleRequestFor(contract.ID)
	if err != nil {
		t.Fatal(err)
	}

	provider, err := fakeprovider.Start(fakeprovider.Options{Hold: hold, SideRequest: titleRequest})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	})

	aht, nativeEvents := stageOracle(t, oracle)

	launched, err := launch.New(t.Context(), launch.Options{
		Harness:    contract.ID,
		Root:       filepath.Join(t.TempDir(), "root"),
		Endpoint:   provider.URL(),
		Model:      fakeModel,
		APIKey:     fakeAPIKey,
		AHT:        aht,
		Executable: contract.Executable,
		Env:        discoveryEnvironment(t, contract),
	})
	if err != nil {
		t.Fatalf("launch %s: %v", contract.ID, err)
	}

	t.Cleanup(func() {
		if err := launched.Close(t.Context()); err != nil {
			t.Error(err)
		}
	})

	return &isolatedHost{
		launched:     launched,
		contract:     contract,
		provider:     provider,
		aht:          aht,
		nativeEvents: nativeEvents,
	}
}

func stageOracle(t *testing.T, oracle string) (string, string) {
	t.Helper()

	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatal(err)
	}

	aht := filepath.Join(binDir, "aht-compat-oracle")
	copyExecutable(t, oracle, aht+".real")
	nativeEvents := filepath.Join(t.TempDir(), "native-events")
	writeObservationLauncher(t, aht, nativeEvents)

	return aht, nativeEvents
}

func (host *isolatedHost) assertIntegrationCurrent(t *testing.T) {
	t.Helper()
	output := host.mustRunAHT(
		t,
		"--json",
		"manage",
		"integrations",
		"status",
		string(host.contract.ID),
		"--binary",
		host.aht,
	)

	var statuses []struct {
		Status string   `json:"status"`
		Paths  []string `json:"paths"`
	}
	if err := json.Unmarshal(output, &statuses); err != nil {
		t.Fatalf("decoding integration status: %v\n%s", err, output)
	}

	if len(statuses) != 1 || statuses[0].Status != "current" {
		t.Fatalf("installed integration is not current:\n%s", output)
	}

	if adapter, ok := catalog.Find(host.contract.ID); ok {
		plan, err := adapter.InstallPlan(host.aht)
		if err != nil {
			t.Fatal(err)
		}

		if _, native := plan.NativeCatalog(); native {
			return
		}
	}

	for _, path := range statuses[0].Paths {
		if pathReferences(t, path, []byte(host.aht)) {
			return
		}
	}

	t.Fatalf(
		"current integration does not reference compatibility oracle %s:\n%s",
		host.aht,
		output,
	)
}

func pathReferences(t *testing.T, path string, needle []byte) bool {
	t.Helper()

	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}

	if err != nil {
		t.Fatalf("inspect integration path %s: %v", path, err)
	}

	if !info.IsDir() {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read integration file %s: %v", path, err)
		}

		return bytes.Contains(content, needle)
	}

	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatalf("open integration directory %s: %v", path, err)
	}

	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()

	found := false

	err = fs.WalkDir(
		root.FS(),
		".",
		func(candidate string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}

			if !entry.Type().IsRegular() {
				return nil
			}

			content, err := root.ReadFile(candidate)
			if err != nil {
				return fmt.Errorf("read %s: %w", candidate, err)
			}

			if bytes.Contains(content, needle) {
				found = true
				return fs.SkipAll
			}

			return nil
		},
	)
	if err != nil {
		t.Fatalf("search integration directory %s: %v", path, err)
	}

	return found
}

func (host *isolatedHost) assertVersion(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, host.launched.Executable, host.contract.VersionArgs...)
	command.Env = host.launched.Env
	command.Dir = host.launched.Work

	var stdout, stderr bytes.Buffer

	command.Stdout = &stdout
	command.Stderr = &stderr
	err := runCompatibilityCommand(ctx, command)

	output := stdout.Bytes()
	if err != nil {
		t.Fatalf(
			"%s version probe failed: %v\n%s\n%s",
			host.contract.ID,
			err,
			output,
			stderr.Bytes(),
		)
	}

	if len(bytes.TrimSpace(output)) == 0 {
		t.Fatalf("%s version probe returned no version", host.contract.ID)
	}

	t.Logf("current %s: %s", host.contract.ID, bytes.TrimSpace(output))
}

// assertHostLoadsIntegration asks the host itself which integration it loaded,
// so an installed artifact at a location the host does not read fails here.
func (host *isolatedHost) assertHostLoadsIntegration(t *testing.T) {
	t.Helper()

	for _, check := range host.contract.LoadChecks {
		if check.Prepare != nil {
			check.Prepare(t, host)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		command := exec.CommandContext(ctx, host.launched.Executable, check.Args...)
		command.Env = host.launched.Env
		command.Dir = host.launched.Work

		var stdout, stderr bytes.Buffer

		command.Stdout = &stdout
		command.Stderr = &stderr
		err := runCompatibilityCommand(ctx, command)

		cancel()

		if err != nil {
			t.Fatalf(
				"%s %v failed: %v\n%s\n%s",
				host.contract.ID,
				check.Args,
				err,
				stdout.Bytes(),
				stderr.Bytes(),
			)
		}

		if needle := check.Needle(host); !bytes.Contains(stdout.Bytes(), []byte(needle)) {
			t.Fatalf(
				"%s %v does not show the installed integration (no %q):\n%s",
				host.contract.ID,
				check.Args,
				needle,
				stdout.Bytes(),
			)
		}
	}
}

func (host *isolatedHost) startTracker(t *testing.T) {
	t.Helper()

	if err := host.launched.StartTracker(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func (host *isolatedHost) writeFile(t *testing.T, path string, content string) {
	t.Helper()

	name, err := filepath.Rel(host.launched.Root, path)
	if err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(host.launched.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()

	if err := root.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := root.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (host *isolatedHost) mustRunAHT(t *testing.T, args ...string) []byte {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	output, err := compatibilityOutput(ctx, host.launched.AHTCommand(ctx, args...))
	if err != nil {
		t.Fatalf("aht %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}

	return output
}

func (host *isolatedHost) awaitHold(
	t *testing.T,
	process *permissionProcess,
	expected int,
) *fakeprovider.Hold {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	var done <-chan struct{}
	if process != nil {
		done = process.done
	}

	hold, err := testharness.AwaitHold(ctx, host.provider, done)
	if err != nil {
		var exitErr error

		if process != nil {
			select {
			case <-process.done:
				exitErr = process.waitErr()
			default:
			}
		}

		t.Fatalf(
			"%s provider checkpoint %d: %v; host exit: %v\n%s",
			host.contract.ID,
			expected,
			err,
			exitErr,
			providerRequestSummary(host.provider),
		)
	}

	if hold.Request.Index != expected {
		t.Fatalf(
			"%s provider checkpoint=%d, want %d",
			host.contract.ID,
			hold.Request.Index,
			expected,
		)
	}

	return hold
}

func (host *isolatedHost) startNativePane(t *testing.T, command *exec.Cmd) gotmux.Pane {
	t.Helper()
	server := testtmux.New(t, gotmux.NewSessionOptions{
		Name: "native", Size: gotmux.Size{Width: 160, Height: 45}, Program: gotmux.Exec("/bin/sh"),
	})

	panes, err := server.Tmux.Panes(t.Context())
	if err != nil || len(panes) != 1 {
		t.Fatalf("native initial pane: count=%d error=%v", len(panes), err)
	}

	pane := panes[0].Handle()
	if err := pane.Options().SetRemainOnExit(t.Context(), gotmux.RemainOn); err != nil {
		t.Fatal(err)
	}

	args := append([]string{"-i"}, command.Env...)
	args = append(args, command.Path)

	args = append(args, command.Args[1:]...)
	if err := pane.Respawn(t.Context(), gotmux.RespawnOptions{
		Dir: host.launched.Work, Program: gotmux.Exec("env", args...), KillRunning: true,
	}); err != nil {
		t.Fatal(err)
	}

	return pane
}
