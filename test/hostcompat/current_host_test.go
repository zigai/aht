//go:build compatibility

package hostcompat

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/testharness"
	"github.com/zigai/aht/v2/pkg/fakeprovider"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestCurrentHarnessLifecycle(t *testing.T) {
	harnessID := registry.Harness(strings.TrimSpace(os.Getenv("AHT_COMPAT_HARNESS")))
	if harnessID == "" {
		t.Fatal("AHT_COMPAT_HARNESS is required; use `just compatibility <harness>`")
	}

	contract, err := contractFor(harnessID)
	if err != nil {
		t.Fatal(err)
	}

	oracle := testharness.Build(t)
	host := newIsolatedHost(t, contract, oracle, true)
	host.assertIntegrationCurrent(t)
	host.assertVersion(t)
	host.assertHostLoadsIntegration(t)

	if contract.Level == compatibilityLevelDiscovery {
		t.Skipf(
			"%s discovery-only: installation, current integration, and "+
				"installed version checked; no lifecycle, interruption, "+
				"permission, or resume claim. Scope: %s",
			contract.ID,
			contract.Scope,
		)

		return
	}

	t.Run("successful_native_terminal", host.runLifecycle)
	t.Run("active_interruption", func(t *testing.T) {
		interrupted := newIsolatedHost(t, contract, oracle, true)
		interrupted.interrupt = true
		interrupted.runLifecycle(t)
	})
	runPermissionScenarios(t, contract, oracle)
}

func (host *isolatedHost) runLifecycle(t *testing.T) {
	t.Helper()

	host.scriptShellRoundTrip(
		host.scriptCallID(false),
		lifecycleShellCommand(host.contract.ID),
		false,
	)
	host.startTracker(t)

	command := host.lifecycleCommand(t)
	if host.interrupt {
		host.runInterruption(t, command)
		host.assertInterrupted(t)

		return
	}

	hostOutput := host.runHostCommand(t, command)
	if providerErr := host.provider.Err(); providerErr != nil {
		t.Fatalf("%v\nprovider requests: %s", providerErr, providerRequestSummary(host.provider))
	}

	if len(host.provider.Requests()) != 2 {
		t.Fatalf(
			"%s provider request count = %d, want exactly 2\n%s",
			host.contract.ID,
			len(host.provider.Requests()),
			hostOutput,
		)
	}

	host.waitForSession(t, hostOutput)
	host.assertNativeEvents(t)
	t.Run("recorded_resume", func(t *testing.T) { host.runResume(t, command) })
}

func (host *isolatedHost) runInterruption(t *testing.T, command *exec.Cmd) {
	t.Helper()

	switch host.contract.InterruptionDriver {
	case driverExecRPC:
		host.runDroidRPC(t, command.Env, nil, true)
	case driverACP:
		host.runKimiACP(t, command, nil, true)
	case driverExecutionServer:
		host.runOpenCodeInterruption(t, command.Env)
	case driverPluginServer:
		host.runServerInterruption(t, command.Env)
	case driverCompletionRPC, driverExtensionRPC:
		runRPCInterruption(t, host, command.Env)
	case driverPythonACP:
		runPythonInterruption(t, host, command.Env)
	case driverCLIACP:
		runCLIInterruption(t, host, command.Env)
	case driverTerminalQuit:
		host.runGrokInterruption(t, command)
	case driverTerminalDialog:
		host.runQwenTUI(t, command, true)
	case driverProcess:
		host.runHostCommand(t, command)
	case driverControlCLI, driverAppServer, driverSDKCLI:
		fallthrough
	default:
		t.Fatalf("unsupported interruption driver %q", host.contract.InterruptionDriver)
	}
}

func (host *isolatedHost) driveHostCheckpoints(
	t *testing.T,
	command *exec.Cmd,
	wait <-chan error,
	logPath string,
) error {
	t.Helper()

	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()

	for {
		select {
		case runErr := <-wait:
			return runErr
		case hold := <-host.provider.Holds():
			if hold.Request.Index == 0 {
				host.waitForActiveSession(t)
			}

			host.continueOrInterrupt(t, command, hold)
		case <-deadline.C:
			output, _ := os.ReadFile(logPath)
			t.Fatalf(
				"%s lifecycle timed out after 30s\nprovider requests: %s\n%s",
				host.contract.ID,
				providerRequestSummary(host.provider),
				output,
			)
		}
	}
}

func (host *isolatedHost) continueOrInterrupt(
	t *testing.T,
	command *exec.Cmd,
	hold *fakeprovider.Hold,
) {
	t.Helper()

	if !host.interrupt {
		hold.Release()
		return
	}

	if host.contract.Resume == resumeGateway {
		host.interruptOpenClaw(t)
		return
	}

	if err := syscall.Kill(-command.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatalf("interrupting active host: %v", err)
	}
}

func (host *isolatedHost) waitForSession(t *testing.T, hostOutput []byte) {
	t.Helper()
	defer func() {
		if t.Failed() {
			t.Logf("completed %s host output:\n%s", host.contract.ID, hostOutput)
		}
	}()

	host.waitForObservation(
		t,
		"matching native session evidence after completed provider lifecycle",
		host.validateSession,
	)
}

// runNativeDriver completes a turn through the hosts whose lifecycle needs
// their own protocol or terminal driver instead of a plain process run.
func (host *isolatedHost) runNativeDriver(t *testing.T, command *exec.Cmd) ([]byte, bool) {
	t.Helper()

	switch host.contract.LifecycleDriver {
	case driverExecRPC:
		return host.runDroidRPC(t, command.Env, nil, false), true
	case driverACP:
		host.runKimiACP(t, command, nil, false)
		return nil, true
	case driverCompletionRPC:
		host.runPiCompletion(t, command)
		return nil, true
	case driverTerminalDialog:
		return host.runQwenTUI(t, command, false), true
	case driverProcess:
		return nil, false
	case driverExtensionRPC,
		driverPythonACP,
		driverExecutionServer,
		driverPluginServer,
		driverTerminalQuit,
		driverCLIACP,
		driverControlCLI,
		driverAppServer,
		driverSDKCLI:
		fallthrough
	default:
		t.Fatalf("unsupported lifecycle driver %q", host.contract.LifecycleDriver)
		return nil, false
	}
}

func (host *isolatedHost) runHostCommand(t *testing.T, command *exec.Cmd) []byte {
	t.Helper()

	if output, handled := host.runNativeDriver(t, command); handled {
		return output
	}

	logPath := filepath.Join(host.launched.Root, fmt.Sprintf("%s-host.log", host.contract.ID))

	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}

	command.Stdout = logFile
	command.Stderr = logFile

	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		_ = logFile.Close()

		t.Fatal(err)
	}

	wait := make(chan error, 1)
	go func() {
		wait <- command.Wait()
	}()

	finished := false

	defer func() {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)

		if !finished {
			if err := <-wait; err != nil {
				t.Logf("isolated host exit after forced cleanup: %v", err)
			}
		}

		_ = logFile.Close()

		if t.Failed() {
			output, _ := os.ReadFile(logPath)
			t.Logf(
				"isolated host output:\n%s\nprovider:\n%s",
				output,
				providerRequestSummary(host.provider),
			)
		}
	}()

	runErr := host.driveHostCheckpoints(t, command, wait, logPath)
	finished = true

	if err := logFile.Close(); err != nil {
		t.Fatal(err)
	}

	output, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatal(readErr)
	}

	if runErr != nil && !host.interrupt {
		t.Fatalf(
			"%s lifecycle command failed: %v\nprovider requests: %s\n%s",
			host.contract.ID,
			runErr,
			providerRequestSummary(host.provider),
			output,
		)
	}

	return output
}
