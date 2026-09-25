//go:build integration

package systemtest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode"

	"github.com/zigai/aht/v2/pkg/broker"
	"github.com/zigai/aht/v2/pkg/registry"
)

const systemTestTimeout = 30 * time.Second

type runningTestCommand struct {
	command *exec.Cmd
	stdout  bytes.Buffer
	stderr  bytes.Buffer
	done    chan error
}

type systemWatchEvent struct {
	Action    string             `json:"action"`
	SessionID string             `json:"session_id"`
	Harness   registry.Harness   `json:"harness"`
	Presence  registry.Presence  `json:"presence"`
	Activity  *registry.Activity `json:"activity"`
}

func TestBuiltBinaryTrackingWorkflow(t *testing.T) {
	root, err := shortSystemTestRoot()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	binary := buildSystemTestBinary(t, root)
	home := filepath.Join(root, "home")
	configHome := filepath.Join(root, "config")
	stateDir := filepath.Join(root, "state")
	workingDir := filepath.Join(root, "work")
	for _, directory := range []string{home, configHome, stateDir, workingDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create isolated directory %q: %v", directory, err)
		}
	}

	environment := systemTestEnvironment(home, configHome, stateDir)
	storePath := filepath.Join(stateDir, "sessions.json")
	transcript := bytes.Buffer{}
	tracker := startSystemTestCommand(t, binary, workingDir, environment, "--store", storePath, "manage", "tracker", "run", "--quiet")
	waitForBrokerSocket(t, tracker, storePath)

	watchCommand, watchEvents := startJSONWatch(t, binary, workingDir, environment, storePath)
	initial := receiveWatchEvent(t, watchCommand, watchEvents)
	if initial.Action != "snapshot_empty" {
		t.Fatalf("initial watch event = %#v, want empty snapshot", initial)
	}

	const sensitiveSentinel = "AHT_PHASE_ONE_PRIVATE_PROMPT"
	maliciousSessionID := "phase-one-session\x1b]0;owned\x07\u202ereordered"
	maliciousCWD := workingDir + "/\x1b[31mcolored\x1b[0m"
	reportInput := strings.NewReader(`{"session_id":"phase-one-session","cwd":"` + workingDir + `","hook_event_name":"UserPromptSubmit","model":"gpt-5","prompt":"` + sensitiveSentinel + `","tool_input":{"command":"private"}}`)
	reportOutput := runSystemTestCommand(
		t,
		binary,
		workingDir,
		environment,
		reportInput,
		"--store", storePath, "--json", "report", "codex",
		"--session-id", maliciousSessionID,
		"--presence", "live",
		"--activity", "running",
		"--event", "UserPromptSubmit",
		"--cwd", maliciousCWD,
		"--raw-stdin-defaults-only",
		"--no-tmux",
		"--pid", "2147483647",
	)
	transcript.Write(reportOutput)
	var reported registry.Session
	if err := json.Unmarshal(reportOutput, &reported); err != nil {
		t.Fatalf("decode report output %q: %v", reportOutput, err)
	}
	assertPhaseOneSession(t, reported, maliciousSessionID, maliciousCWD)

	observed := receiveSessionWatchEvent(t, watchCommand, watchEvents, reported.SessionID)
	if observed.Harness != registry.Harness("codex") || observed.Presence != registry.PresenceLive || observed.Activity == nil || *observed.Activity != registry.ActivityUnknown {
		t.Fatalf("watch event = %#v, want live Codex session with screen-authoritative activity pending", observed)
	}
	stopSystemTestCommand(t, watchCommand)

	listOutput := runSystemTestCommand(t, binary, workingDir, environment, nil, "--store", storePath, "--json", "list")
	transcript.Write(listOutput)
	listed := decodeSessionByID(t, "list", listOutput, reported.SessionID)
	infoOutput := runSystemTestCommand(t, binary, workingDir, environment, nil, "--store", storePath, "--json", "info", reported.SessionID)
	transcript.Write(infoOutput)
	var info registry.Session
	if err := json.Unmarshal(infoOutput, &info); err != nil {
		t.Fatalf("decode info output %q: %v", infoOutput, err)
	}
	assertSamePhaseOneSession(t, reported, listed)
	assertSamePhaseOneSession(t, reported, info)

	assertHumanCommandSafe(t, &transcript, binary, workingDir, environment, []string{"--store", storePath, "list", "--full"})
	assertHumanCommandSafe(t, &transcript, binary, workingDir, environment, []string{"--store", storePath, "info", reported.SessionID})
	assertHumanWatchSafe(t, &transcript, binary, workingDir, environment, storePath)

	stopSystemTestCommand(t, tracker)
	if _, err := os.Stat(storePath); err != nil {
		t.Fatalf("persisted registry after tracker stop: %v", err)
	}

	restartedTracker := startSystemTestCommand(t, binary, workingDir, environment, "--store", storePath, "manage", "tracker", "run", "--quiet")
	waitForBrokerSocket(t, restartedTracker, storePath)
	restartedListOutput := runSystemTestCommand(t, binary, workingDir, environment, nil, "--store", storePath, "--json", "list")
	transcript.Write(restartedListOutput)
	restarted := decodeSessionByID(t, "restarted list", restartedListOutput, reported.SessionID)
	assertSamePhaseOneSession(t, reported, restarted)
	if restarted.UpdatedAt.Before(reported.UpdatedAt) {
		t.Fatalf("restarted session updated_at = %s, before reported %s", restarted.UpdatedAt, reported.UpdatedAt)
	}
	stopSystemTestCommand(t, restartedTracker)

	assertSensitiveSentinelAbsent(t, root, sensitiveSentinel, transcript.Bytes())
}

func shortSystemTestRoot() (string, error) {
	return os.MkdirTemp("/tmp", "aht-systest-")
}

func buildSystemTestBinary(t *testing.T, root string) string {
	t.Helper()
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	binary := filepath.Join(root, "aht")
	command := exec.Command("go", "build", "-o", binary, ".")
	command.Dir = repositoryRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build aht test binary: %v; output=%q", err, output)
	}
	return binary
}

func systemTestEnvironment(home string, configHome string, stateDir string) []string {
	environment := []string{
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + configHome,
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"XDG_STATE_HOME=" + stateDir,
		"AHT_STATE_DIR=" + stateDir,
		"LANG=C",
		"TERM=dumb",
	}
	if path := os.Getenv("PATH"); path != "" {
		environment = append(environment, "PATH="+path)
	}
	if temporaryDirectory := os.Getenv("TMPDIR"); temporaryDirectory != "" {
		environment = append(environment, "TMPDIR="+temporaryDirectory)
	}
	return environment
}

func startSystemTestCommand(t *testing.T, binary string, directory string, environment []string, args ...string) *runningTestCommand {
	t.Helper()
	process := &runningTestCommand{command: exec.Command(binary, args...), done: make(chan error, 1)}
	process.command.Dir = directory
	process.command.Env = environment
	process.command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	process.command.Stdout = &process.stdout
	process.command.Stderr = &process.stderr
	if err := process.command.Start(); err != nil {
		t.Fatalf("start %q: %v", args, err)
	}
	go func() {
		process.done <- process.command.Wait()
	}()
	t.Cleanup(func() {
		if process.command.ProcessState == nil && process.command.Process != nil && process.command.Process.Pid > 0 {
			_ = syscall.Kill(-process.command.Process.Pid, syscall.SIGKILL)
			<-process.done
		}
	})
	return process
}

func stopSystemTestCommand(t *testing.T, process *runningTestCommand) {
	t.Helper()
	if process.command.ProcessState != nil {
		return
	}
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupt %q: %v", process.command.Args, err)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case err := <-process.done:
		if err != nil {
			t.Fatalf("wait for %q: %v; stdout=%q stderr=%q", process.command.Args, err, process.stdout.String(), process.stderr.String())
		}
	case <-timer.C:
		_ = process.command.Process.Kill()
		<-process.done
		t.Fatalf("command did not stop: %q; stdout=%q stderr=%q", process.command.Args, process.stdout.String(), process.stderr.String())
	}
}

func waitForBrokerSocket(t *testing.T, tracker *runningTestCommand, storePath string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	socketPath := broker.SocketPath(storePath)
	for {
		select {
		case err := <-tracker.done:
			t.Fatalf("tracker exited before readiness: %v; stdout=%q stderr=%q", err, tracker.stdout.String(), tracker.stderr.String())
		case <-deadline.C:
			t.Fatalf("broker socket %q was not created; stdout=%q stderr=%q", socketPath, tracker.stdout.String(), tracker.stderr.String())
		case <-ticker.C:
			if info, err := os.Stat(socketPath); err == nil && info.Mode()&os.ModeSocket != 0 {
				return
			}
		}
	}
}

type watchResult struct {
	event systemWatchEvent
	raw   []byte
	err   error
}

func startJSONWatch(t *testing.T, binary string, directory string, environment []string, storePath string) (*runningTestCommand, <-chan watchResult) {
	t.Helper()
	process := &runningTestCommand{command: exec.Command(binary, "--store", storePath, "--json", "watch"), done: make(chan error, 1)}
	process.command.Dir = directory
	process.command.Env = environment
	stdout, err := process.command.StdoutPipe()
	if err != nil {
		t.Fatalf("create watch stdout pipe: %v", err)
	}
	process.command.Stderr = &process.stderr
	if err := process.command.Start(); err != nil {
		t.Fatalf("start JSON watch: %v", err)
	}
	go func() {
		process.done <- process.command.Wait()
	}()
	results := make(chan watchResult, 16)
	go decodeWatchEvents(stdout, results)
	t.Cleanup(func() {
		if process.command.ProcessState == nil {
			_ = process.command.Process.Kill()
			<-process.done
		}
	})
	return process, results
}

func decodeWatchEvents(reader io.Reader, results chan<- watchResult) {
	defer close(results)
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event systemWatchEvent
		if err := json.Unmarshal(line, &event); err != nil {
			results <- watchResult{raw: append([]byte(nil), line...), err: fmt.Errorf("malformed watch JSON line %q: %w", string(line), err)}
			return
		}
		results <- watchResult{event: event, raw: append([]byte(nil), line...), err: nil}
	}
	if err := scanner.Err(); err != nil {
		results <- watchResult{err: fmt.Errorf("reading watch output: %w", err)}
	}
}

func receiveWatchEvent(t *testing.T, process *runningTestCommand, results <-chan watchResult) systemWatchEvent {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case res, ok := <-results:
		if !ok {
			t.Fatalf("watch output closed; stderr=%q", process.stderr.String())
		}
		if res.err != nil {
			t.Fatalf("watch output error: %v; raw=%q stderr=%q", res.err, string(res.raw), process.stderr.String())
		}
		return res.event
	case err := <-process.done:
		t.Fatalf("watch exited before event: %v; stderr=%q", err, process.stderr.String())
	case <-timer.C:
		t.Fatalf("timed out waiting for watch event; stderr=%q", process.stderr.String())
	}
	return systemWatchEvent{}
}

func receiveSessionWatchEvent(t *testing.T, process *runningTestCommand, results <-chan watchResult, sessionID string) systemWatchEvent {
	t.Helper()
	for {
		event := receiveWatchEvent(t, process, results)
		if event.SessionID == sessionID {
			return event
		}
	}
}

func runSystemTestCommand(t *testing.T, binary string, directory string, environment []string, stdin io.Reader, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), systemTestTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = directory
	command.Env = environment
	command.Stdin = stdin
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("run %q: %v; stdout=%q stderr=%q", args, err, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("run %q wrote stderr=%q", args, stderr.String())
	}
	return stdout.Bytes()
}

func decodeSessionByID(t *testing.T, source string, data []byte, sessionID string) registry.Session {
	t.Helper()
	var sessions []registry.Session
	if err := json.Unmarshal(data, &sessions); err != nil {
		t.Fatalf("decode %s output: %v", source, err)
	}
	for _, session := range sessions {
		if session.SessionID == sessionID {
			return session
		}
	}
	t.Fatalf("%s: session %q not found among %d listed sessions", source, sessionID, len(sessions))
	return registry.Session{}
}

func assertPhaseOneSession(t *testing.T, session registry.Session, sessionID string, cwd string) {
	t.Helper()
	if session.SchemaVersion != 3 || session.SessionID != sessionID || session.Harness != registry.Harness("codex") {
		t.Fatalf("session identity = %#v, want schema-v3 phase-one Codex session", session)
	}
	if session.Presence() != registry.PresenceLive || session.Activity() == nil || *session.Activity() != registry.ActivityUnknown {
		t.Fatalf("effective session state = presence %q activity %v, want live/unknown until screen evidence", session.Presence(), session.Activity())
	}
	if session.Observations.Native == nil || session.Observations.Native.Activity == nil || *session.Observations.Native.Activity != registry.ActivityRunning {
		t.Fatalf("reported activity = %#v, want running native observation", session.Observations.Native)
	}
	if session.CWD != cwd {
		t.Fatalf("session cwd = %q, want %q", session.CWD, cwd)
	}
}

func assertSamePhaseOneSession(t *testing.T, want registry.Session, got registry.Session) {
	t.Helper()
	assertPhaseOneSession(t, got, want.SessionID, want.CWD)
	if got.ID != want.ID || !got.CreatedAt.Equal(want.CreatedAt) || got.UpdatedAt.Before(want.UpdatedAt) {
		t.Fatalf("session changed across surfaces: want=%#v got=%#v", want, got)
	}
}

func assertHumanCommandSafe(t *testing.T, transcript *bytes.Buffer, binary string, directory string, environment []string, args []string) {
	t.Helper()
	output := runSystemTestCommand(t, binary, directory, environment, nil, args...)
	transcript.Write(output)
	assertTerminalSafe(t, output)
	if !bytes.Contains(output, []byte("owned")) || !bytes.Contains(output, []byte("reordered")) {
		t.Fatalf("human output lost recognizable safe text: %q", output)
	}
}

func assertHumanWatchSafe(t *testing.T, transcript *bytes.Buffer, binary string, directory string, environment []string, storePath string) {
	t.Helper()
	process := &runningTestCommand{command: exec.Command(binary, "--store", storePath, "watch", "--format", "plain"), done: make(chan error, 1)}
	process.command.Dir = directory
	process.command.Env = environment
	process.command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := process.command.StdoutPipe()
	if err != nil {
		t.Fatalf("create human watch stdout pipe: %v", err)
	}
	process.command.Stderr = &process.stderr
	if err := process.command.Start(); err != nil {
		t.Fatalf("start human watch: %v", err)
	}
	waitDone := make(chan struct{})
	var doneOnce sync.Once
	go func() {
		waitErr := process.command.Wait()
		doneOnce.Do(func() { close(waitDone) })
		process.done <- waitErr
	}()
	scanner := bufio.NewScanner(stdout)
	line := make(chan []byte, 1)
	scannerDone := make(chan struct{})
	go func() {
		defer close(scannerDone)
		if scanner.Scan() {
			line <- bytes.Clone(scanner.Bytes())
		}
		close(line)
	}()
	t.Cleanup(func() {
		select {
		case <-waitDone:
			<-scannerDone
			return
		default:
		}
		if process.command.Process != nil && process.command.Process.Pid > 0 {
			_ = syscall.Kill(-process.command.Process.Pid, syscall.SIGKILL)
			<-waitDone
		}
		<-scannerDone
	})
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case output, ok := <-line:
		if !ok {
			t.Fatalf("human watch closed without output; stderr=%q", process.stderr.String())
		}
		transcript.Write(output)
		assertTerminalSafe(t, output)
		if !bytes.Contains(output, []byte("owned")) || !bytes.Contains(output, []byte("reordered")) {
			t.Fatalf("human watch lost recognizable safe text: %q", output)
		}
	case err := <-process.done:
		t.Fatalf("human watch exited before snapshot: %v; stderr=%q", err, process.stderr.String())
	case <-timer.C:
		t.Fatalf("timed out waiting for human watch snapshot; stderr=%q", process.stderr.String())
	}
	stopSystemTestCommand(t, process)
	<-scannerDone
}

func assertTerminalSafe(t *testing.T, output []byte) {
	t.Helper()
	for _, character := range string(output) {
		if character == '\n' || character == '\t' {
			continue
		}
		if unicode.IsControl(character) || isSystemTestBidiControl(character) {
			t.Fatalf("human output contains forbidden rune %U: %q", character, output)
		}
	}
}

func isSystemTestBidiControl(character rune) bool {
	switch character {
	case '\u061c', '\u200e', '\u200f':
		return true
	default:
		return character >= '\u202a' && character <= '\u202e' ||
			character >= '\u2066' && character <= '\u2069'
	}
}

func assertSensitiveSentinelAbsent(t *testing.T, root string, sentinel string, transcript []byte) {
	t.Helper()
	if bytes.Contains(transcript, []byte(sentinel)) {
		t.Fatalf("sensitive sentinel appears in command output")
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if bytes.Contains(data, []byte(sentinel)) {
			return fmt.Errorf("sensitive sentinel appears in %s", path)
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
