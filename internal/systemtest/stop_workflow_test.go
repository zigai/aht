//go:build integration

package systemtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	gotmux "github.com/zigai/gotmux/tmux"

	harnesspkg "github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/internal/processinfo"
	"github.com/zigai/aht/internal/testtmux"
	"github.com/zigai/aht/pkg/registry"
	"github.com/zigai/aht/pkg/tmux"
)

type systemStopResult struct {
	Stoppable int                       `json:"stoppable"`
	Stopped   int                       `json:"stopped"`
	Skipped   int                       `json:"skipped"`
	DryRun    bool                      `json:"dry_run"`
	Results   []systemStopSessionResult `json:"results"`
}

type systemStopSessionResult struct {
	Status string `json:"status"`
	Method string `json:"method"`
	Target string `json:"target"`
	Reason string `json:"reason"`
}

func TestStopOwnedTargets(t *testing.T) {
	t.Run("process identity", testStopOwnedProcess)
	t.Run("tmux server identity", testStopOwnedTmuxTarget)
}

func testStopOwnedProcess(t *testing.T) {
	root, err := shortSystemTestRoot()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	binary := buildSystemTestBinary(t, root)
	codexBinary := filepath.Join(root, "codex")
	if err := os.Link(binary, codexBinary); err != nil {
		t.Fatalf("create Codex test binary: %v", err)
	}
	workingDir := filepath.Join(root, "work")
	stateDir := filepath.Join(root, "state")
	for _, directory := range []string{workingDir, stateDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create isolated directory %q: %v", directory, err)
		}
	}
	environment := systemTestEnvironment(filepath.Join(root, "home"), filepath.Join(root, "config"), stateDir)

	selectedStore := filepath.Join(stateDir, "selected-child.json")
	controlStore := filepath.Join(stateDir, "control-child.json")
	selected := startSystemTestCommand(t, codexBinary, workingDir, environment, "--store", selectedStore, "manage", "tracker", "run", "--quiet")
	control := startSystemTestCommand(t, codexBinary, workingDir, environment, "--store", controlStore, "manage", "tracker", "run", "--quiet")
	waitForBrokerSocket(t, selected, selectedStore)
	waitForBrokerSocket(t, control, controlStore)

	selectedIdentity := requireProcessStartIdentity(t, selected.command.Process.Pid)
	controlIdentity := requireProcessStartIdentity(t, control.command.Process.Pid)
	registryPath := filepath.Join(stateDir, "stop-sessions.json")
	selectedSession := reportProcessSession(t, binary, workingDir, environment, registryPath, "selected-process", selected.command.Process.Pid, selectedIdentity)

	dryRunOutput := runSystemTestCommand(t, binary, workingDir, environment, nil, "--store", registryPath, "--json", "stop", selectedSession.SessionID, "--dry-run")
	dryRun := decodeSystemStopResult(t, dryRunOutput)
	if !dryRun.DryRun || dryRun.Stoppable != 1 || dryRun.Stopped != 0 || len(dryRun.Results) != 1 || dryRun.Results[0].Status != "would_stop" || dryRun.Results[0].Method != "pid-interrupt" {
		t.Fatalf("process dry-run result = %#v", dryRun)
	}
	assertSystemTestCommandRunning(t, selected)
	assertSystemTestCommandRunning(t, control)

	staleSession := reportProcessSession(t, binary, workingDir, environment, registryPath, "stale-process", control.command.Process.Pid, controlIdentity+":stale")
	staleOutput, staleError := runFailingSystemTestCommand(t, binary, workingDir, environment, "--store", registryPath, "--json", "stop", staleSession.SessionID)
	stale := decodeSystemStopResult(t, staleOutput)
	if stale.Skipped != 1 || len(stale.Results) != 1 || stale.Results[0].Status != "skipped" || stale.Results[0].Reason != "process identity changed" {
		t.Fatalf("stale process result = %#v; stderr=%q", stale, staleError)
	}
	assertSystemTestCommandRunning(t, selected)
	assertSystemTestCommandRunning(t, control)

	stopOutput := runSystemTestCommand(t, binary, workingDir, environment, nil, "--store", registryPath, "--json", "stop", selectedSession.SessionID)
	stopped := decodeSystemStopResult(t, stopOutput)
	if stopped.Stopped != 1 || len(stopped.Results) != 1 || stopped.Results[0].Status != "stopped" || stopped.Results[0].Target != strconv.Itoa(selected.command.Process.Pid) {
		t.Fatalf("process stop result = %#v", stopped)
	}
	waitForSystemTestCommandExit(t, selected)
	assertSystemTestCommandRunning(t, control)
	stopSystemTestCommand(t, control)
}

func testStopOwnedTmuxTarget(t *testing.T) {
	testtmux.Executable(t)
	root, err := shortSystemTestRoot()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	binary := buildSystemTestBinary(t, root)
	codexBinary := filepath.Join(root, "codex")
	if err := os.Link(binary, codexBinary); err != nil {
		t.Fatalf("create Codex tmux fixture binary: %v", err)
	}
	stateDir := filepath.Join(root, "state")
	workingDir := filepath.Join(root, "work")
	for _, directory := range []string{stateDir, workingDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create isolated directory %q: %v", directory, err)
		}
	}
	environment := systemTestEnvironment(filepath.Join(root, "home"), filepath.Join(root, "config"), stateDir)

	targetServer := startTmuxAgentSession(t, "target", codexBinary, filepath.Join(stateDir, "target-pane.json"))
	controlServer := startTmuxAgentSession(t, "control", codexBinary, filepath.Join(stateDir, "control-pane.json"))

	targetPane := requireTmuxPane(t, targetServer.Socket)
	controlPane := requireTmuxPane(t, controlServer.Socket)
	if targetPane.Tmux.PaneID != controlPane.Tmux.PaneID {
		t.Fatalf("tmux fixtures must share a pane id across distinct servers: target=%q control=%q", targetPane.Tmux.PaneID, controlPane.Tmux.PaneID)
	}

	registryPath := filepath.Join(stateDir, "tmux-stop-sessions.json")
	store := registry.NewFileStore(registryPath)
	targetSession := seedTmuxStopSession(t, store, "target-tmux", targetPane)
	_ = seedTmuxStopSession(t, store, "control-tmux", controlPane)

	stopOutput := runSystemTestCommand(t, binary, workingDir, environment, nil, "--store", registryPath, "--json", "stop", targetSession.ID)
	stopped := decodeSystemStopResult(t, stopOutput)
	if stopped.Stopped != 1 || len(stopped.Results) != 1 || stopped.Results[0].Status != "stopped" || stopped.Results[0].Method != "tmux-interrupt" || stopped.Results[0].Target != targetPane.Tmux.PaneID {
		t.Fatalf("tmux stop result = %#v", stopped)
	}
	waitForTmuxSessionExit(t, targetServer, "target")
	assertTmuxSessionRunning(t, controlServer, "control")
}

func requireProcessStartIdentity(t *testing.T, pid int) string {
	t.Helper()
	identity := processinfo.StartIdentity(t.Context(), pid)
	if identity == "" {
		t.Fatalf("process %d has no start identity", pid)
	}
	return identity
}

func reportProcessSession(t *testing.T, binary string, directory string, environment []string, storePath string, sessionID string, pid int, startIdentity string) registry.Session {
	t.Helper()
	output := runSystemTestCommand(
		t,
		binary,
		directory,
		environment,
		nil,
		"--store", storePath,
		"--json",
		"report", "codex",
		"--session-id", sessionID,
		"--presence", "live",
		"--evidence", "process",
		"--pid", strconv.Itoa(pid),
		"--start-identity", startIdentity,
		"--no-tmux",
	)
	var session registry.Session
	if err := json.Unmarshal(output, &session); err != nil {
		t.Fatalf("decode process report %q: %v", output, err)
	}
	if session.Process == nil || session.Process.PID != pid || session.Process.StartIdentity != startIdentity {
		t.Fatalf("recorded process identity = %#v, want pid=%d start=%q", session.Process, pid, startIdentity)
	}
	return session
}

func decodeSystemStopResult(t *testing.T, output []byte) systemStopResult {
	t.Helper()
	var result systemStopResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode stop output %q: %v", output, err)
	}
	return result
}

func runFailingSystemTestCommand(t *testing.T, binary string, directory string, environment []string, args ...string) ([]byte, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), systemTestTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = directory
	command.Env = environment
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err == nil {
		t.Fatalf("run %q unexpectedly succeeded; stdout=%q stderr=%q", args, stdout.String(), stderr.String())
	}
	if stdout.Len() == 0 || stderr.Len() == 0 {
		t.Fatalf("run %q did not preserve structured output and error diagnostics; stdout=%q stderr=%q", args, stdout.String(), stderr.String())
	}
	return stdout.Bytes(), stderr.Bytes()
}

func assertSystemTestCommandRunning(t *testing.T, process *runningTestCommand) {
	t.Helper()
	select {
	case err := <-process.done:
		t.Fatalf("command exited unexpectedly: %v; args=%q stdout=%q stderr=%q", err, process.command.Args, process.stdout.String(), process.stderr.String())
	default:
	}
}

func waitForSystemTestCommandExit(t *testing.T, process *runningTestCommand) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case err := <-process.done:
		if err != nil {
			t.Fatalf("command exited with error: %v; args=%q stdout=%q stderr=%q", err, process.command.Args, process.stdout.String(), process.stderr.String())
		}
	case <-timer.C:
		t.Fatalf("command did not exit after stop: args=%q stdout=%q stderr=%q", process.command.Args, process.stdout.String(), process.stderr.String())
	}
}

func startTmuxAgentSession(t *testing.T, session string, binary string, storePath string) *testtmux.Server {
	t.Helper()
	shellCommand := "exec " + harnesspkg.ShellQuote(binary) + " --store " + harnesspkg.ShellQuote(storePath) + " manage tracker run --quiet"
	return testtmux.New(t, "-s", session, shellCommand)
}

func requireTmuxPane(t *testing.T, socket string) tmux.Pane {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		panes, err := tmux.ListPanes(t.Context())
		if err == nil {
			for _, pane := range panes {
				if pane.ServerIdentity == socket {
					return pane
				}
			}
		}
		select {
		case <-deadline.C:
			t.Fatalf("tmux pane for socket %q was not discovered: %v", socket, err)
		case <-ticker.C:
		}
	}
}

func seedTmuxStopSession(t *testing.T, store *registry.FileStore, sessionID string, pane tmux.Pane) registry.Session {
	t.Helper()
	process := &registry.ProcessIdentity{
		PID:           pane.PanePID,
		StartIdentity: requireProcessStartIdentity(t, pane.PanePID),
	}
	present := true
	at := time.Now().UTC()
	if _, err := store.Observe(t.Context(), registry.Observation{
		Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
		Harness: registry.HarnessCodex, Identity: registry.ObservationIdentity{SessionID: sessionID},
		ProcessPresent: &present, Process: process, ObservedAt: at,
	}); err != nil {
		t.Fatalf("record tmux process observation: %v", err)
	}
	session, err := store.Observe(t.Context(), registry.Observation{
		Source: registry.ObservationSourceTmux, Evidence: registry.ObservationEvidenceTmuxLocation,
		Harness: registry.HarnessCodex, Process: process, Tmux: &pane.Tmux, ObservedAt: at.Add(time.Nanosecond),
	})
	if err != nil {
		t.Fatalf("record tmux location observation: %v", err)
	}
	return session
}

func waitForTmuxSessionExit(t *testing.T, server *testtmux.Server, session string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err := server.Tmux.FindSession(t.Context(), session)
		if errors.Is(err, gotmux.ErrNotFound) || errors.Is(err, gotmux.ErrNoServer) {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("tmux session %q on %q did not exit", session, server.Socket)
		case <-ticker.C:
		}
	}
}

func assertTmuxSessionRunning(t *testing.T, server *testtmux.Server, session string) {
	t.Helper()
	if _, err := server.Tmux.FindSession(t.Context(), session); err != nil {
		t.Fatalf("control tmux session %q on %q exited: %v", session, server.Socket, err)
	}
}
