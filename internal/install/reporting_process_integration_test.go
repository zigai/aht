//go:build integration

package install

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/zigai/aht/pkg/registry"
)

func TestReportingPluginsBoundAndReapChildren(t *testing.T) {
	for _, name := range []registry.Harness{registry.HarnessCline, registry.HarnessOpenClaw, registry.HarnessHermes} {
		t.Run(string(name), func(t *testing.T) {
			binary, capture := stalledReportingBinary(t)
			dir := writeReportingPlugin(t, name, binary)
			_, tool, terminal, driver := reportingDriver(t, name)
			runReportingDriver(t, tool, dir, driver, capture, terminal)
			records := readReportingRecords(t, capture)
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
		})
	}
}

func TestReportingPluginsMissingBinaryIsNonfatal(t *testing.T) {
	for _, name := range []registry.Harness{registry.HarnessCline, registry.HarnessOpenClaw, registry.HarnessHermes} {
		t.Run(string(name), func(t *testing.T) {
			dir := writeReportingPlugin(t, name, filepath.Join(t.TempDir(), "missing-aht"))
			_, tool, _, driver := reportingDriver(t, name)
			t.Setenv("AHT_TEST_SKIP_HANDSHAKE", "1")
			extension := ".mjs"
			if tool == "python3" {
				extension = ".py"
			}
			path := filepath.Join(dir, "driver"+extension)
			writeTestFile(t, path, driver, 0o600)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, requireRuntimeTool(t, tool), path)
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
	dir := writeReportingPlugin(t, registry.HarnessOpenClaw, binary)
	_, tool, terminal, driver := reportingDriver(t, registry.HarnessOpenClaw)
	t.Setenv("AHT_TEST_GATEWAY_STOP", "1")
	runReportingDriver(t, tool, dir, driver, capture, terminal)
	records := readReportingRecords(t, capture)
	if len(records) != 2 || records[1].Overlap || !slices.Contains(records[1].Args, terminal) {
		t.Fatalf("shutdown did not join the active child and preserve newest state: %+v", records)
	}
}

func TestTypeScriptReportingOwnsProcesses(t *testing.T) {
	for _, harness := range []registry.Harness{
		registry.HarnessPi, registry.HarnessOmp, registry.HarnessOpenCode, registry.HarnessKilo,
	} {
		t.Run(string(harness), func(t *testing.T) {
			command, capture := stalledReportingBinary(t)
			var module string
			for _, artifact := range collectGeneratedArtifacts(t, captureExecutable{command: command, path: capture}) {
				if artifact.harness == harness && strings.HasSuffix(artifact.path, "aht-state.ts") {
					module = artifact.content
					break
				}
			}
			if module == "" {
				t.Fatal("missing generated extension")
			}
			dir := t.TempDir()
			writeTestFile(t, filepath.Join(dir, "package.json"), `{"type":"module"}`, 0o600)
			writeTestFile(t, filepath.Join(dir, "extension.ts"), module, 0o600)
			driver := runtimeScript(t, "node/extension-process-ownership.mjs")
			terminalEvent := "session_shutdown"
			if harness == registry.HarnessOpenCode || harness == registry.HarnessKilo {
				driver = runtimeScript(t, "node/plugin-process-ownership.mjs")
				terminalEvent = "session.deleted"
			}
			runReportingDriver(t, "node", dir, driver, capture, terminalEvent)
			records := readReportingRecords(t, capture)
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
		})
	}
}

func TestTypeScriptReportingRejectsMalformedEventFields(t *testing.T) {
	for _, harness := range []registry.Harness{registry.HarnessPi, registry.HarnessOmp} {
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
		registry.HarnessPi, registry.HarnessOmp, registry.HarnessOpenCode, registry.HarnessKilo,
	} {
		t.Run(string(harness), func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "missing-reporter")
			for _, artifact := range collectGeneratedArtifacts(t, captureExecutable{command: binary}) {
				if artifact.harness != harness || !strings.HasSuffix(artifact.path, "aht-state.ts") {
					continue
				}
				driver := runtimeScript(t, "node/extension-missing-reporter.mjs")
				if harness == registry.HarnessOpenCode || harness == registry.HarnessKilo {
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
	suffix, _, _, _ := reportingDriver(t, name)
	for _, artifact := range collectGeneratedArtifacts(t, captureExecutable{command: binary}) {
		if artifact.harness == name && strings.HasSuffix(artifact.path, suffix) {
			writeTestFile(t, filepath.Join(dir, suffix), artifact.content, 0o600)
		}
	}
	if name == registry.HarnessOpenClaw {
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
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
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

func runReportingDriver(t *testing.T, tool, dir, driver, capture, terminal string) {
	t.Helper()
	runtime := requireRuntimeTool(t, tool)
	extension := ".mjs"
	if tool == "python3" {
		extension = ".py"
	}
	path := filepath.Join(dir, "driver"+extension)
	writeTestFile(t, path, driver, 0o600)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
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
	args := []string{path}
	if tool == "node" {
		args = []string{"--experimental-strip-types", path}
	}
	command := exec.CommandContext(ctx, runtime, args...)
	command.Dir = dir
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	var waitErr error
	done := make(chan struct{})
	go func() {
		waitErr = command.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		_ = input.Close()
		<-done
	})
	for phase := range 2 {
		for {
			records := readReportingRecords(t, capture)
			if len(records) > 0 && (phase == 0 || slices.Contains(records[len(records)-1].Args, terminal)) {
				break
			}
			select {
			case <-watcher.Events:
			case err := <-watcher.Errors:
				t.Fatalf("watch reports: %v", err)
			case <-done:
				t.Fatalf("driver exited before report phase %d: %v\n%s", phase, waitErr, output.String())
			case <-ctx.Done():
				t.Fatalf("report phase %d: %v", phase, ctx.Err())
			}
		}
		if _, err := input.Write([]byte("\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		if waitErr != nil {
			t.Fatalf("report driver: %v\n%s", waitErr, output.String())
		}
	case <-ctx.Done():
		t.Fatalf("report driver did not join: %v", ctx.Err())
	}
}

func reportingDriver(t *testing.T, name registry.Harness) (string, string, string, string) {
	t.Helper()
	switch name {
	case registry.HarnessCline:
		return "index.js", "node", "afterRun", runtimeScript(t, "node/cline-report-queue.mjs")
	case registry.HarnessOpenClaw:
		return "index.js", "node", "session_end", runtimeScript(t, "node/openclaw-report-queue.mjs")
	default:
		return "__init__.py", "python3", "on_session_finalize", runtimeScript(t, "python/hermes-report-queue.py")
	}
}
