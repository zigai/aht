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

//go:embed testdata/runtimes/cline_driver.js
var clineDriverScript string

//go:embed testdata/runtimes/openclaw_driver.js
var openclawDriverScript string

//go:embed testdata/runtimes/hermes_driver.py
var hermesDriverScript string

type reportingRecord struct {
	PID     int      `json:"pid"`
	Args    []string `json:"args"`
	Overlap bool     `json:"overlap"`
}

func TestReportingPluginsBoundAndReapChildren(t *testing.T) {
	for _, name := range []registry.Harness{registry.HarnessCline, registry.HarnessOpenClaw, registry.HarnessHermes} {
		t.Run(string(name), func(t *testing.T) {
			binary, capture := stalledReportingBinary(t)
			dir := writeReportingPlugin(t, name, binary)
			_, tool, terminal, driver := reportingDriver(name)
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
			_, tool, _, driver := reportingDriver(name)
			driver = strings.ReplaceAll(driver, "await lines.next();", "")
			driver = strings.ReplaceAll(driver, "input()", "")
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
	_, tool, terminal, driver := reportingDriver(registry.HarnessOpenClaw)
	driver = strings.ReplaceAll(driver, `await hooks.get("session_end")({}, ctx);`, `hooks.get("session_end")({}, ctx);
await hooks.get("gateway_stop")();`)
	runReportingDriver(t, tool, dir, driver, capture, terminal)
	records := readReportingRecords(t, capture)
	if len(records) != 2 || records[1].Overlap || !slices.Contains(records[1].Args, terminal) {
		t.Fatalf("shutdown did not join the active child and preserve newest state: %+v", records)
	}
}

func writeReportingPlugin(t *testing.T, name registry.Harness, binary string) string {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "package.json"), `{"type":"module"}`, 0o600)
	suffix, _, _, _ := reportingDriver(name)
	for _, artifact := range collectGeneratedArtifacts(t, captureExecutable{command: binary}) {
		if artifact.harness == name && strings.HasSuffix(artifact.path, suffix) {
			writeTestFile(t, filepath.Join(dir, suffix), artifact.content, 0o600)
		}
	}
	if name == registry.HarnessOpenClaw {
		writeTestFile(t, filepath.Join(dir, "node_modules/openclaw/package.json"), `{"type":"module","exports":{"./plugin-sdk/plugin-entry":"./plugin-entry.js"}}`, 0o600)
		writeTestFile(t, filepath.Join(dir, "node_modules/openclaw/plugin-entry.js"), "export function definePluginEntry(value) { return value; }", 0o600)
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
	script := "#!" + node + "\n" + `
const fs = require("node:fs");
const capture = process.env.AHT_PROCESS_CAPTURE;
const first = !fs.existsSync(capture);
let overlap = false;
if (!first) {
  const previous = JSON.parse(fs.readFileSync(capture, "utf8").trim().split("\n").at(-1));
  try { process.kill(previous.pid, 0); overlap = true; } catch {}
}
if (first) process.on("SIGTERM", () => {});
fs.appendFileSync(capture, JSON.stringify({pid: process.pid, args: process.argv.slice(2), overlap}) + "\n");
if (first) {
  setInterval(() => {}, 1000);
  // Self-clean even when exercising the old detached-child bug.
  setTimeout(() => process.exit(0), 10000);
}
`
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

func reportingDriver(name registry.Harness) (string, string, string, string) {
	switch name {
	case registry.HarnessCline:
		return "index.js", "node", "afterRun", clineDriverScript
	case registry.HarnessOpenClaw:
		return "index.js", "node", "session_end", openclawDriverScript
	default:
		return "__init__.py", "python3", "on_session_finalize", hermesDriverScript
	}
}
