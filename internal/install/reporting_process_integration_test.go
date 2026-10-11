//go:build integration

package install

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestReportingPluginsBoundAndReapChildren(t *testing.T) {
	for _, name := range []registry.Harness{registry.Harness("cline"), registry.Harness("openclaw"), registry.Harness("hermes")} {
		t.Run(string(name), func(t *testing.T) {
			binary, capture := stalledReportingBinary(t)
			dir := writeReportingPlugin(t, name, binary)
			fixture := reportingDriver(t, name)
			runReportingDriver(t, fixture, dir, capture)
			assertBoundedFIFOReports(t, readReportingRecords(t, capture), fixture.terminal)
		})
	}
}

func assertBoundedFIFOReports(t *testing.T, records []reportingRecord, terminal string) {
	t.Helper()
	if len(records) != 65 {
		t.Fatalf("reported %d events; want one active plus 64 pending", len(records))
	}
	for _, record := range records {
		if record.Overlap {
			t.Fatal("reporting spawned a child before its predecessor exited")
		}
	}
	for index, record := range records[1:64] {
		want := strconv.Itoa(137 + index)
		if !slices.Contains(record.Args, "cline_run_id="+want) &&
			!slices.Contains(record.Args, "openclaw_run_id="+want) &&
			!slices.Contains(record.Args, "hermes_turn_id="+want) {
			t.Fatalf("pending event %d lost FIFO order: %v", index, record.Args)
		}
	}
	if !slices.Contains(records[64].Args, terminal) {
		t.Fatalf("last event = %v, want %s", records[64].Args, terminal)
	}
}

func TestReportingPluginsMissingBinaryIsNonfatal(t *testing.T) {
	for _, name := range []registry.Harness{registry.Harness("cline"), registry.Harness("openclaw"), registry.Harness("hermes")} {
		t.Run(string(name), func(t *testing.T) {
			dir := writeReportingPlugin(t, name, filepath.Join(t.TempDir(), "missing-aht"))
			fixture := reportingDriver(t, name)
			t.Setenv("AHT_TEST_SKIP_HANDSHAKE", "1")
			path := filepath.Join(dir, fixture.driverName())
			writeTestFile(t, path, fixture.driver, 0o600)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, requireRuntimeTool(t, fixture.tool), path)
			command.Dir = dir
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("missing reporter failed host: %v\n%s", err, output)
			}
			if strings.Count(string(output), "aht: reporting failed or overloaded") != 1 {
				t.Fatalf("expected exactly one safe warning, got %s", output)
			}
		})
	}
}

func TestOpenClawGatewayStopJoinsReporter(t *testing.T) {
	binary, capture := stalledReportingBinary(t)
	dir := writeReportingPlugin(t, registry.Harness("openclaw"), binary)
	fixture := reportingDriver(t, registry.Harness("openclaw"))
	t.Setenv("AHT_TEST_GATEWAY_STOP", "1")
	runReportingDriver(t, fixture, dir, capture)
	records := readReportingRecords(t, capture)
	if len(records) != 2 || records[1].Overlap || !slices.Contains(records[1].Args, fixture.terminal) {
		t.Fatalf("shutdown did not join the active child and preserve newest state: %+v", records)
	}
}

func TestTypeScriptReportingOwnsProcesses(t *testing.T) {
	for _, harness := range []registry.Harness{
		registry.Harness("pi"), registry.Harness("omp"), registry.Harness("opencode"), registry.Harness("kilo"),
	} {
		t.Run(string(harness), func(t *testing.T) {
			command, capture := stalledReportingBinary(t)
			dir := t.TempDir()
			writeTestFile(t, filepath.Join(dir, "package.json"), `{"type":"module"}`, 0o600)
			writeTestFile(t, filepath.Join(dir, "extension.ts"), generatedTypeScriptModule(t, harness, captureExecutable{command: command, path: capture}), 0o600)
			fixture := reportingFixture{tool: "node", terminal: "session_shutdown", driver: runtimeScript(t, "node/extension-process-ownership.mjs")}
			if harness == registry.Harness("opencode") || harness == registry.Harness("kilo") {
				fixture.driver = runtimeScript(t, "node/plugin-process-ownership.mjs")
				fixture.terminal = "session.deleted"
			}
			runReportingDriver(t, fixture, dir, capture)
			assertOwnedReports(t, readReportingRecords(t, capture), fixture.terminal)
		})
	}
}

func generatedTypeScriptModule(t *testing.T, harness registry.Harness, binary captureExecutable) string {
	t.Helper()
	for _, artifact := range collectGeneratedArtifacts(t, binary) {
		if artifact.harness == harness && strings.HasSuffix(artifact.path, "aht-state.ts") {
			return artifact.content
		}
	}
	t.Fatal("missing generated extension")
	return ""
}

func assertOwnedReports(t *testing.T, records []reportingRecord, terminalEvent string) {
	t.Helper()
	if len(records) < 2 || len(records) > 65 {
		t.Fatalf("executed %d reports, want initial plus bounded pending FIFO", len(records))
	}
	for _, record := range records {
		if record.Overlap {
			t.Fatal("report started before previous subprocess was reaped")
		}
		if !strings.Contains(strings.Join(record.Args, "\n"), "owned-session") {
			t.Fatalf("report lost session identity: %v", record.Args)
		}
	}
	if !strings.Contains(strings.Join(records[len(records)-1].Args, "\n"), terminalEvent) {
		t.Fatalf("final report was not retained: %v", records[len(records)-1].Args)
	}
}

func TestTypeScriptReportingRejectsMalformedEventFields(t *testing.T) {
	for _, harness := range []registry.Harness{registry.Harness("pi"), registry.Harness("omp")} {
		t.Run(string(harness), func(t *testing.T) {
			capture := captureBinary(t)
			t.Setenv("AHT_CAPTURE", capture.path)
			module := generatedArtifactContent(t, harness, "aht-state.ts")
			runNodeRuntime(t, "extension.ts", module, runtimeScript(t, "node/extension-malformed-events.mjs"), nil)
			data, err := os.ReadFile(capture.path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "DO_NOT_REPORT") || strings.Contains(string(data), "not-a-string") {
				t.Fatalf("malformed or sensitive fields escaped projection: %s", data)
			}
			requireCapturedArguments(t, capture.path, "report", string(harness), "--session-id", "boundary-session", "--activity", "interrupted")
		})
	}
}

func TestTypeScriptReportingMissingExecutableIsNonfatal(t *testing.T) {
	for _, harness := range []registry.Harness{
		registry.Harness("pi"), registry.Harness("omp"), registry.Harness("opencode"), registry.Harness("kilo"),
	} {
		t.Run(string(harness), func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "missing-reporter")
			for _, artifact := range collectGeneratedArtifacts(t, captureExecutable{command: binary}) {
				if artifact.harness != harness || !strings.HasSuffix(artifact.path, "aht-state.ts") {
					continue
				}
				driver := runtimeScript(t, "node/extension-missing-reporter.mjs")
				if harness == registry.Harness("opencode") || harness == registry.Harness("kilo") {
					driver = runtimeScript(t, "node/plugin-missing-reporter.mjs")
				}
				runNodeRuntime(t, "extension.ts", artifact.content, driver, nil)
				return
			}
			t.Fatal("missing generated extension")
		})
	}
}

type reportingRecord struct {
	PID     int      `json:"pid"`
	Args    []string `json:"args"`
	Overlap bool     `json:"overlap"`
}

func writeReportingPlugin(t *testing.T, name registry.Harness, binary string) string {
	t.Helper()
	t.Setenv("AHT_TEST_SKIP_HANDSHAKE", "0")
	t.Setenv("AHT_TEST_GATEWAY_STOP", "0")
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "package.json"), `{"type":"module"}`, 0o600)
	module := reportingDriver(t, name).module
	for _, artifact := range collectGeneratedArtifacts(t, captureExecutable{command: binary}) {
		if artifact.harness == name && strings.HasSuffix(artifact.path, module) {
			writeTestFile(t, filepath.Join(dir, module), artifact.content, 0o600)
		}
	}
	if name == registry.Harness("openclaw") {
		writeTestFile(t, filepath.Join(dir, "node_modules/openclaw/package.json"), `{"type":"module","exports":{"./plugin-sdk/plugin-entry":"./plugin-entry.js"}}`, 0o600)
		writeTestFile(t, filepath.Join(dir, "node_modules/openclaw/plugin-entry.js"), runtimeScript(t, "node/openclaw-plugin-entry.mjs"), 0o600)
	}
	return dir
}

func stalledReportingBinary(t *testing.T) (string, string) {
	t.Helper()
	node := requireRuntimeTool(t, "node")
	dir := t.TempDir()
	capture := filepath.Join(dir, "reports.jsonl")
	t.Setenv("AHT_PROCESS_CAPTURE", capture)
	binary := filepath.Join(dir, "reporter")
	script := "#!" + node + "\n" + runtimeScript(t, "node/stalled-reporter.cjs")
	writeTestFile(t, binary, script, 0o700)
	return binary, capture
}

func readReportingRecords(t *testing.T, capture string) []reportingRecord {
	t.Helper()
	data, err := os.ReadFile(capture)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var records []reportingRecord
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var record reportingRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode captured report: %v", err)
		}
		records = append(records, record)
	}
	return records
}

func runReportingDriver(t *testing.T, fixture reportingFixture, dir string, capture string) {
	t.Helper()
	runtime := requireRuntimeTool(t, fixture.tool)
	path := filepath.Join(dir, fixture.driverName())
	writeTestFile(t, path, fixture.driver, 0o600)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	watcher := watchCaptureDirectory(t, capture)
	args := []string{path}
	if fixture.tool == "node" {
		args = []string{"--experimental-strip-types", path}
	}
	process := startReportingDriver(t, ctx, cancel, dir, runtime, args...)
	for phase := range 2 {
		process.awaitReport(t, ctx, watcher, capture, phase, fixture.terminal)
		if _, err := process.input.Write([]byte("\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err := process.input.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-process.done:
		if process.waitErr != nil {
			t.Fatalf("report driver: %v\n%s", process.waitErr, process.output.String())
		}
	case <-ctx.Done():
		t.Fatalf("report driver did not join: %v", ctx.Err())
	}
}

type reportingDriverProcess struct {
	input   io.WriteCloser
	output  bytes.Buffer
	done    chan struct{}
	waitErr error
}

func startReportingDriver(t *testing.T, ctx context.Context, cancel context.CancelFunc, dir string, runtime string, args ...string) *reportingDriverProcess {
	t.Helper()
	process := &reportingDriverProcess{done: make(chan struct{})}
	command := exec.CommandContext(ctx, runtime, args...)
	command.Dir = dir
	command.Stdout, command.Stderr = &process.output, &process.output
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	process.input = input
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		process.waitErr = command.Wait()
		close(process.done)
	}()
	t.Cleanup(func() {
		cancel()
		_ = input.Close()
		<-process.done
	})
	return process
}

func (process *reportingDriverProcess) awaitReport(t *testing.T, ctx context.Context, watcher *fsnotify.Watcher, capture string, phase int, terminal string) {
	t.Helper()
	for {
		records := readReportingRecords(t, capture)
		if len(records) > 0 && (phase == 0 || slices.Contains(records[len(records)-1].Args, terminal)) {
			return
		}
		select {
		case <-watcher.Events:
		case err := <-watcher.Errors:
			t.Fatalf("watch reports: %v", err)
		case <-process.done:
			t.Fatalf("driver exited before report phase %d: %v\n%s", phase, process.waitErr, process.output.String())
		case <-ctx.Done():
			t.Fatalf("report phase %d: %v", phase, ctx.Err())
		}
	}
}

func watchCaptureDirectory(t *testing.T, capture string) *fsnotify.Watcher {
	t.Helper()
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := watcher.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := watcher.Add(filepath.Dir(capture)); err != nil {
		t.Fatal(err)
	}
	return watcher
}

type reportingFixture struct {
	module   string
	tool     string
	terminal string
	driver   string
}

func (fixture reportingFixture) driverName() string {
	if fixture.tool == "python3" {
		return "driver.py"
	}
	return "driver.mjs"
}

func reportingDriver(t *testing.T, name registry.Harness) reportingFixture {
	t.Helper()
	switch name {
	case registry.Harness("cline"):
		return reportingFixture{module: "index.js", tool: "node", terminal: "afterRun", driver: runtimeScript(t, "node/cline-report-queue.mjs")}
	case registry.Harness("openclaw"):
		return reportingFixture{module: "index.js", tool: "node", terminal: "session_end", driver: runtimeScript(t, "node/openclaw-report-queue.mjs")}
	default:
		return reportingFixture{module: "__init__.py", tool: "python3", terminal: "on_session_finalize", driver: runtimeScript(t, "python/hermes-report-queue.py")}
	}
}
