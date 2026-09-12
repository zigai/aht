//go:build compatibility

package hostcompat

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

const permissionMarker = ".aht-permission-marker"

func runPermissionScenarios(t *testing.T, contract hostContract) {
	t.Helper()
	switch contract.ID {
	case registry.HarnessClaude, registry.HarnessCodex, registry.HarnessCopilot:
		runCLIPermissionScenarios(t, contract)
	case registry.HarnessPi, registry.HarnessOmp:
		runRPCPermissionScenarios(t, contract)
	case registry.HarnessKimiCode, registry.HarnessHermes:
		runPythonPermissionScenarios(t, contract)
	case registry.HarnessOpenCode, registry.HarnessKilo:
		runServerPermissionScenarios(t, contract)
	default:
		// These lifecycle adapters do not advertise permission-wait coverage.
		// Cursor and Antigravity are separately tested as discovery-only hosts.
	}
}

func newPermissionHost(t *testing.T, contract hostContract, allow bool) isolatedHost {
	t.Helper()
	host := newIsolatedHost(t, contract)
	host.installIntegration(t)
	args := lifecycleToolArgs(contract.ID)
	command := "printf aht-compat-marker > " + permissionMarker + "; cat " + permissionMarker
	if contract.ID == registry.HarnessHermes {
		// Hermes asks only for dangerous shell operations. This disposable,
		// nonexistent relative target cannot affect anything outside host.work.
		command = "rm -rf .aht-permission-unused; " + command
	}
	if contract.ID == registry.HarnessCodex {
		args["cmd"] = command
	} else {
		args["command"] = command
	}
	host.provider = newScriptedProvider(t, contract.Protocol, lifecycleToolName(contract.ID), args, "aht-compat-marker")
	host.provider.expectRejectedTool = !allow
	host.startTracker(t)
	return host
}

func assertPermissionWaiting(t *testing.T, host isolatedHost) registry.Session {
	t.Helper()
	waiting := host.waitForObservation(t, "live native permission wait before tool execution", func(session registry.Session) bool {
		native := session.Observations.Native
		return native != nil && native.SessionID != "" && native.Activity != nil &&
			*native.Activity == registry.ActivityWaiting && session.Presence == registry.PresenceLive &&
			filepath.Clean(session.CWD) == filepath.Clean(host.work)
	})
	if waiting.Activity == nil || (*waiting.Activity != registry.ActivityWaiting && *waiting.Activity != registry.ActivityUnknown) {
		t.Fatalf("native permission wait has incompatible effective activity: %+v", waiting)
	}
	assertPermissionMarker(t, host, false)
	if count := len(host.provider.Requests()); count != 1 {
		t.Fatalf("permission decision must precede tool continuation; provider requests = %d", count)
	}
	return waiting
}

func assertPermissionOutcome(t *testing.T, host isolatedHost, waiting registry.Session, allow bool) {
	t.Helper()
	host.waitForObservation(t, "same native session leaving permission wait", func(session registry.Session) bool {
		native := session.Observations.Native
		return session.ID == waiting.ID && native != nil &&
			native.SessionID == waiting.Observations.Native.SessionID &&
			((native.Presence != nil && *native.Presence == registry.PresenceGone) ||
				(native.Activity != nil && *native.Activity != registry.ActivityWaiting)) &&
			(session.Activity == nil || *session.Activity != registry.ActivityWaiting)
	})
	assertPermissionMarker(t, host, allow)
	if err := host.provider.Error(); err != nil {
		t.Fatalf("permission continuation failed: %v\n%s", err, providerRequestSummary(host.provider))
	}
	// Rejection may terminate the native turn without asking the model again.
	// Drivers verify that native outcome; the marker must still be absent.
	if count := len(host.provider.Requests()); allow && count != 2 {
		t.Fatalf("approved continuation requests = %d, want 2\n%s", count, providerRequestSummary(host.provider))
	}
}

func assertPermissionMarker(t *testing.T, host isolatedHost, exists bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(host.work, permissionMarker))
	if !exists {
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("tool executed before approval or after rejection: marker=%q error=%v", data, err)
		}
		return
	}
	if err != nil || string(data) != "aht-compat-marker" {
		t.Fatalf("approved tool did not produce exact marker: marker=%q error=%v", data, err)
	}
}

type permissionProcess struct {
	command *exec.Cmd
	done    <-chan struct{}
	err     error
}

func startPermissionProcess(t *testing.T, host isolatedHost, command *exec.Cmd) *permissionProcess {
	t.Helper()
	logFile, err := os.CreateTemp(host.root, "permission-*.log")
	if err != nil {
		t.Fatal(err)
	}
	if command.Stdout == nil {
		command.Stdout = logFile
	}
	if command.Stderr == nil {
		command.Stderr = logFile
	}
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		t.Fatalf("start permission host: %v", err)
	}
	done := make(chan struct{})
	process := &permissionProcess{command: command, done: done}
	go func() {
		process.err = command.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		select {
		case <-done:
		default:
			// A production transport owns a separate native process group.
			// Let its signal handler reap that group before forcing this one down.
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(4 * time.Second):
			}
		}
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("permission host was not reaped after killing its owned process group")
		}
		_ = logFile.Close()
		if t.Failed() {
			log, _ := os.ReadFile(logFile.Name())
			events, _ := os.ReadFile(filepath.Join(host.root, "native-events"))
			t.Logf("isolated permission host output:\n%s\nnative events:\n%s\nprovider:\n%s", log, events, providerRequestSummary(host.provider))
			sessions, _ := json.MarshalIndent(host.sessions(t), "", "  ")
			t.Logf("owned launcher PID=%d; isolated native session state:\n%s", command.Process.Pid, sessions)
		}
	})
	return process
}

func (process *permissionProcess) waitErr() error {
	<-process.done
	return process.err
}

func (process *permissionProcess) wait(t *testing.T) {
	t.Helper()
	select {
	case <-process.done:
		if err := process.waitErr(); err != nil {
			t.Fatalf("permission host exited unsuccessfully: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("permission host did not complete within 30 seconds")
	}
}

func (process *permissionProcess) interrupt(t *testing.T) {
	t.Helper()
	if err := syscall.Kill(-process.command.Process.Pid, syscall.SIGINT); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("interrupt owned permission host: %v", err)
	}
}
