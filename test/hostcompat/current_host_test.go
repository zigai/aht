//go:build compatibility

package hostcompat

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
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
	oracle := buildCompatibilityOracle(t)
	host := newIsolatedHost(t, contract, oracle)
	host.installIntegration(t)
	host.assertIntegrationCurrent(t)
	host.assertVersion(t)
	if contract.Level == compatibilityDiscovery {
		t.Logf("%s discovery-only: installation, current integration, and installed version checked; no lifecycle, interruption, permission, or resume claim. Scope: %s", contract.ID, discoveryScope(contract.ID))
		return
	}
	t.Run("successful_native_terminal", host.runLifecycle)
	t.Run("active_interruption", func(t *testing.T) {
		interrupted := newIsolatedHost(t, contract, oracle)
		interrupted.installIntegration(t)
		interrupted.interrupt = true
		interrupted.runLifecycle(t)
	})
	runPermissionScenarios(t, contract, oracle)
}

func (host *isolatedHost) runLifecycle(t *testing.T) {
	t.Helper()

	toolName := lifecycleToolName(host.contract.ID)
	host.provider = newScriptedProvider(t, host.contract.Protocol, toolName, lifecycleToolArgs(host.contract.ID), "aht-compat-marker")
	host.provider.checkpoints = make(chan int)
	host.provider.release = make(chan struct{}, 1)
	if host.contract.ID == registry.HarnessOpenClaw {
		host.provider.callID = "callcompat" // OpenClaw strips punctuation from tool IDs.
	}
	host.startTracker(t)
	command, setup := host.lifecycleCommand(t)
	for _, setupCommand := range setup {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		output, err := compatibilityOutput(ctx, setupCommand)
		cancel()
		if err != nil {
			t.Fatalf("%s provider setup failed: %v\n%s", host.contract.ID, err, output)
		}
	}
	if host.interrupt {
		host.runInterruption(t, command)
		host.assertInterrupted(t)
		return
	}
	hostOutput := host.runHostCommand(t, command)
	if providerErr := host.provider.Error(); providerErr != nil {
		t.Fatalf("%v\nprovider requests: %s", providerErr, providerRequestSummary(host.provider))
	}
	if len(host.provider.Requests()) != 2 {
		t.Fatalf("%s provider request count = %d, want exactly 2\n%s", host.contract.ID, len(host.provider.Requests()), hostOutput)
	}

	host.waitForSession(t, hostOutput)
	host.assertNativeEvents(t)
	t.Run("recorded_resume", func(t *testing.T) { host.runResume(t, command) })
}

func (host isolatedHost) runInterruption(t *testing.T, command *exec.Cmd) {
	t.Helper()
	switch host.contract.ID {
	case registry.HarnessDroid:
		host.runDroidRPC(t, command.Env, nil, true)
	case registry.HarnessKimiCode:
		host.runKimiWire(t, command, true)
	case registry.HarnessOpenCode, registry.HarnessKilo:
		host.runServerInterruption(t, command.Env)
	case registry.HarnessPi, registry.HarnessOmp:
		runRPCInterruption(t, host, command.Env)
	case registry.HarnessHermes:
		runPythonInterruption(t, host, command.Env)
	case registry.HarnessCline:
		runCLIInterruption(t, host, command.Env)
	case registry.HarnessGrok:
		host.runGrokInterruption(t, command)
	default:
		host.runHostCommand(t, command)
	}
}

func (host isolatedHost) waitForSession(t *testing.T, hostOutput []byte) {
	t.Helper()
	defer func() {
		if t.Failed() {
			t.Logf("completed %s host output:\n%s", host.contract.ID, hostOutput)
		}
	}()
	host.waitForObservation(t, "matching native session evidence after completed provider lifecycle", host.validateSession)
}

func (host isolatedHost) runHostCommand(t *testing.T, command *exec.Cmd) []byte {
	t.Helper()
	if host.contract.ID == registry.HarnessDroid {
		return host.runDroidRPC(t, command.Env, nil, false)
	}
	if host.contract.ID == registry.HarnessKimiCode {
		host.runKimiWire(t, command, false)
		return nil
	}
	if host.contract.ID == registry.HarnessPi {
		host.runPiCompletion(t, command)
		return nil
	}

	logPath := filepath.Join(host.root, fmt.Sprintf("%s-host.log", host.contract.ID))
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
			<-wait
		}
		_ = logFile.Close()
		if t.Failed() {
			output, _ := os.ReadFile(logPath)
			t.Logf("isolated host output:\n%s\nprovider:\n%s", output, providerRequestSummary(host.provider))
		}
	}()
	var runErr error
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for !finished {
		select {
		case runErr = <-wait:
			finished = true
		case step := <-host.provider.checkpoints:
			if step == 0 {
				host.waitForActiveSession(t)
			}
			if host.interrupt {
				if host.contract.ID == registry.HarnessOpenClaw {
					host.interruptOpenClaw(t)
				} else if err := syscall.Kill(-command.Process.Pid, syscall.SIGINT); err != nil {
					t.Fatalf("interrupting active host: %v", err)
				}
			} else {
				host.provider.release <- struct{}{}
			}
		case <-deadline.C:
			output, _ := os.ReadFile(logPath)
			t.Fatalf("%s lifecycle timed out after 30s\nprovider requests: %s\n%s", host.contract.ID, providerRequestSummary(host.provider), output)
		}
	}
	if err := logFile.Close(); err != nil {
		t.Fatal(err)
	}
	output, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if runErr != nil && !host.interrupt {
		t.Fatalf("%s lifecycle command failed: %v\nprovider requests: %s\n%s", host.contract.ID, runErr, providerRequestSummary(host.provider), output)
	}
	return output
}
