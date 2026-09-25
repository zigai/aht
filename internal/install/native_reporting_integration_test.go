//go:build integration

package install

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestPiReportingAllowsSlowSuccessfulWrite(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "reporter")
	completed := filepath.Join(dir, "completed")
	t.Setenv("AHT_REPORT_COMPLETED", completed)
	writeTestFile(t, binary, "#!"+requireRuntimeTool(t, "node")+"\n"+runtimeScript(t, "node/slow-reporter.cjs"), 0o700)
	runNodeRuntime(t, "extension.ts", piReportingArtifact(t, binary), runtimeScript(t, "node/pi-slow-report.mjs"), nil)
}

func TestPiReportingRetainsSafeNativeDiagnosticsAndRecovers(t *testing.T) {
	for _, hasUI := range []string{"true", "false"} {
		t.Run("hasUI="+hasUI, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, "reporter")
			t.Setenv("AHT_REPORT_RECOVERED", filepath.Join(dir, "recovered"))
			t.Setenv("AHT_TEST_HAS_UI", hasUI)
			writeTestFile(t, binary, "#!"+requireRuntimeTool(t, "node")+"\n"+runtimeScript(t, "node/failing-reporter.cjs"), 0o700)
			runNodeRuntime(t, "extension.ts", piReportingArtifact(t, binary), runtimeScript(t, "node/pi-report-recovery.mjs"), nil)
		})
	}
}

func TestPiReportingMissingBinaryAndUnwritableSessionAreNonfatal(t *testing.T) {
	runNodeRuntime(t, "extension.ts", piReportingArtifact(t, filepath.Join(t.TempDir(), "missing")), runtimeScript(t, "node/pi-missing-report-diagnostics.mjs"), nil)
}

func piReportingArtifact(t *testing.T, binary string) string {
	t.Helper()
	for _, artifact := range collectGeneratedArtifacts(t, captureExecutable{command: binary}) {
		if artifact.harness == registry.Harness("pi") && strings.HasSuffix(artifact.path, "aht-state.ts") {
			return artifact.content
		}
	}
	t.Fatal("missing generated Pi extension")
	return ""
}

func TestOmpReportsRootSessionsWithAndWithoutUI(t *testing.T) {
	tests := []struct {
		name     string
		hasUI    bool
		subagent bool
		mode     string
	}{
		{name: "interactive", hasUI: true, mode: "tui"},
		{name: "rpc", mode: "rpc"},
		{name: "json", mode: "json"},
		{name: "print", mode: "print"},
		{name: "unknown-mode", mode: "unknown"},
		{name: "subagent", subagent: true, mode: "tui"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capture := captureBinary(t)
			t.Setenv("AHT_CAPTURE", capture.path)
			t.Setenv("AHT_TEST_HAS_UI", strconv.FormatBool(test.hasUI))
			t.Setenv("AHT_TEST_MODE", test.mode)
			t.Setenv("AHT_TEST_SUBAGENT", strconv.FormatBool(test.subagent))
			module := generatedArtifactContent(t, registry.Harness("omp"), "aht-state.ts")
			runNodeRuntime(t, "extension.ts", module, runtimeScript(t, "node/omp-root-session.mjs"), nil)
			if test.subagent {
				data, err := os.ReadFile(capture.path)
				if err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if len(data) != 0 {
					t.Fatalf("subagent emitted root session reports: %s", data)
				}
				return
			}
			requireCapturedArguments(t, capture.path, "--lifecycle", "start", "--session-id", "omp-session", "--session-path", "/tmp/omp.jsonl", "--cwd", "/tmp/project")
			requireCapturedArguments(t, capture.path, "--activity", "failed")
			requireCapturedArguments(t, capture.path, "--lifecycle", "end", "--presence", "gone")
			requireOmpReportMetadata(t, capture.path, test.mode)
		})
	}
}

func requireOmpReportMetadata(t *testing.T, capturePath, mode string) {
	t.Helper()
	data, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range parseCapturedInvocations(string(data)) {
		if !matchInvocation(args, []string{"report", "omp", "--resume-command", "omp", "--resume-command", "--session", "--resume-command", "/tmp/omp.jsonl"}) {
			t.Fatalf("report lost native resume command: %v", args)
		}
		if mode == "unknown" {
			if strings.Contains(strings.Join(args, "\n"), "aht_interaction_mode=") {
				t.Fatalf("reported unsupported mode: %v", args)
			}
		} else if !matchInvocation(args, []string{"--attribute", "aht_interaction_mode=" + mode}) {
			t.Fatalf("report lost interaction mode: %v", args)
		}
	}
}

func TestOmpQueuedReportsRetainCapturedSessionAndTerminalState(t *testing.T) {
	binary, capture := stalledReportingBinary(t)
	dir := t.TempDir()
	for _, artifact := range collectGeneratedArtifacts(t, captureExecutable{command: binary}) {
		if artifact.harness == registry.Harness("omp") && strings.HasSuffix(artifact.path, "aht-state.ts") {
			writeTestFile(t, filepath.Join(dir, "extension.ts"), artifact.content, 0o600)
		}
	}
	runReportingDriver(t, "node", dir, runtimeScript(t, "node/omp-queued-reports.mjs"), capture, "session_shutdown")
	records := readReportingRecords(t, capture)
	want := [][]string{
		{"--lifecycle", "start", "--session-id", "original"},
		{"--activity", "running", "--session-id", "original"},
		{"--lifecycle", "resume", "--session-id", "resumed", "--attribute", "aht_interaction_mode=rpc"},
		{"--activity", "interrupted", "--session-id", "original", "--session-path", "/sessions/original.jsonl", "--cwd", "/work/original", "--attribute", "aht_interaction_mode=tui", "--event", "agent_end", "--attribute", "omp_reason=interrupted", "--attribute", "omp_tool_name=bash", "--attribute", "omp_approval_mode=ask", "--attribute", "omp_approved=false", "--attribute", "agent_state_message=interrupted"},
		{"--lifecycle", "end", "--presence", "gone", "--session-id", "resumed", "--session-path", "/sessions/resumed.jsonl", "--cwd", "/work/resumed", "--attribute", "aht_interaction_mode=rpc"},
	}
	if len(records) != len(want) {
		t.Fatalf("reported %d events, want coalesced start/running/resume/interruption/shutdown: %+v", len(records), records)
	}
	for index, expected := range want {
		if records[index].Overlap || !matchInvocation(records[index].Args, expected) {
			t.Fatalf("report %d lost FIFO order or captured state: %+v; want %v", index, records[index], expected)
		}
	}
	// Delivery can lag newer lifecycle reports; sequence retains observation order.
	var previous int64
	for _, index := range []int{0, 1, 3, 2, 4} {
		sequence := capturedReportSequence(t, records[index].Args)
		if sequence <= previous {
			t.Fatalf("report %d lost enqueue-time sequence: %d <= %d", index, sequence, previous)
		}
		previous = sequence
	}
}

func capturedReportSequence(t *testing.T, args []string) int64 {
	t.Helper()
	for index, arg := range args {
		if arg == "--sequence" && index+1 < len(args) {
			value, err := strconv.ParseInt(args[index+1], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			return value
		}
	}
	t.Fatalf("report missing sequence: %v", args)
	return 0
}

func TestOmpRetryAndContinuationRemainRunning(t *testing.T) {
	for _, test := range []struct {
		name  string
		event string
	}{
		{name: "retryable", event: `{"type":"agent_end","messages":[{"role":"assistant","stopReason":"error","errorMessage":"503 service unavailable"}]}`},
		{name: "continuing", event: `{"type":"agent_end","willContinue":true,"messages":[]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			capture := captureBinary(t)
			t.Setenv("AHT_CAPTURE", capture.path)
			t.Setenv("AHT_TEST_EVENT", test.event)
			module := generatedArtifactContent(t, registry.Harness("omp"), "aht-state.ts")
			runNodeRuntime(t, "extension.ts", module, runtimeScript(t, "node/omp-retry-continuation.mjs"), nil)
			data, err := os.ReadFile(capture.path)
			if err != nil {
				t.Fatal(err)
			}
			invocations := parseCapturedInvocations(string(data))
			if len(invocations) != 3 || !matchInvocation(invocations[1], []string{"--activity", "running", "--session-id", "retry-session"}) {
				t.Fatalf("retry/continuation reported a terminal state: %v", invocations)
			}
		})
	}
}

func TestOmpApprovalReportsWaitingState(t *testing.T) {
	capture := captureBinary(t)
	t.Setenv("AHT_CAPTURE", capture.path)
	module := generatedArtifactContent(t, registry.Harness("omp"), "aht-state.ts")
	runNodeRuntime(t, "extension.ts", module, runtimeScript(t, "node/omp-approval.mjs"), nil)
	requireCapturedArguments(t, capture.path, "report", "omp", "--activity", "waiting", "--session-id", "approval-session", "--event", "tool_approval_requested", "--attribute", "omp_approval_reason=Run command?", "--attribute", "agent_state_message=Run command?")
}

func TestExtensionsReportNativeInteractionMode(t *testing.T) {
	for _, harness := range []registry.Harness{registry.Harness("pi"), registry.Harness("omp")} {
		for _, mode := range []string{"tui", "print", "json", "rpc"} {
			t.Run(string(harness)+"/"+mode, func(t *testing.T) {
				capture := captureBinary(t)
				t.Setenv("AHT_CAPTURE", capture.path)
				t.Setenv("AHT_TEST_MODE", mode)
				module := generatedArtifactContent(t, harness, "aht-state.ts")
				runNodeRuntime(t, "extension.ts", module, runtimeScript(t, "node/extension-interaction-mode.mjs"), nil)
				requireCapturedArguments(t, capture.path, "--attribute", "aht_interaction_mode="+mode)
			})
		}
	}
}

func TestClinePluginReportsNativeAbort(t *testing.T) {
	requireRuntimeTool(t, "node")
	capture := captureBinary(t)
	t.Setenv("AHT_CAPTURE", capture.path)
	module := generatedArtifactContent(t, registry.Harness("cline"), "index.js")
	runNodeRuntime(t, "index.js", module, runtimeScript(t, "node/cline-native-abort.mjs"), nil)
	requireCapturedArguments(t, capture.path, "report", "cline", "--activity", "interrupted")
}
