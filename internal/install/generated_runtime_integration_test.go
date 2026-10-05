//go:build integration

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	harnesspkg "github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

const generatedRuntimeSensitiveSentinel = "AHT_SENSITIVE_SENTINEL"

func TestGeneratedRuntimeFamilies(t *testing.T) {
	t.Setenv("AHT_TEST_SENSITIVE_SENTINEL", generatedRuntimeSensitiveSentinel)
	requireRuntimeTool(t, "node")
	requireRuntimeTool(t, "python3")
	requireRuntimeTool(t, "sh")
	t.Run("command-hook", func(t *testing.T) {
		capture := captureBinary(t)
		t.Setenv("AHT_CAPTURE", capture.path)
		command := generatedCommandHook(t, registry.Harness("claude"))
		runGeneratedCommand(t, `{"session_id":"command-session","prompt":"`+generatedRuntimeSensitiveSentinel+`"}`, "sh", "-c", command)
		requireCapturedArguments(t, capture.path, "report", "claude", "--activity", "idle")
	})

	t.Run("goose-shell-wrapper", func(t *testing.T) {
		capture := captureBinary(t)
		t.Setenv("AHT_CAPTURE", capture.path)
		script := writeRuntimeArtifact(t, "report.sh", generatedArtifactContent(t, registry.Harness("goose"), "scripts/report.sh"))
		runGeneratedCommand(t, `{"session_id":"goose-session","prompt":"`+generatedRuntimeSensitiveSentinel+`"}`, "sh", script, "running", "UserPromptSubmit")
		requireCapturedArguments(t, capture.path, "report", "goose", "--activity", "running")
	})

	t.Run("cline-plugin", func(t *testing.T) {
		capture := captureBinary(t)
		t.Setenv("AHT_CAPTURE", capture.path)
		module := generatedArtifactContent(t, registry.Harness("cline"), "index.js")
		runNodeRuntime(t, "index.js", module, runtimeScript(t, "node/cline-failed.mjs"), nil)
		requireCapturedArguments(t, capture.path, "report", "cline", "--activity", "failed")
	})

	t.Run("openclaw-plugin", func(t *testing.T) {
		capture := captureBinary(t)
		t.Setenv("AHT_CAPTURE", capture.path)
		module := generatedArtifactContent(t, registry.Harness("openclaw"), "index.js")
		extra := map[string]string{
			"node_modules/openclaw/package.json":    `{"name":"openclaw","type":"module","exports":{"./plugin-sdk/plugin-entry":"./plugin-entry.js"}}`,
			"node_modules/openclaw/plugin-entry.js": runtimeScript(t, "node/openclaw-plugin-entry.mjs"),
		}
		runNodeRuntime(t, "index.js", module, runtimeScript(t, "node/openclaw-failed.mjs"), extra)
		requireCapturedArguments(t, capture.path, "report", "openclaw", "--activity", "failed")

		captureAborted := captureBinary(t)
		t.Setenv("AHT_CAPTURE", captureAborted.path)
		runNodeRuntime(t, "index.js", module, runtimeScript(t, "node/openclaw-interrupted.mjs"), extra)
		requireCapturedArguments(t, captureAborted.path, "report", "openclaw", "--activity", "interrupted")
	})

	t.Run("hermes-plugin", func(t *testing.T) {
		capture := captureBinary(t)
		t.Setenv("AHT_CAPTURE", capture.path)
		module := generatedArtifactContent(t, registry.Harness("hermes"), "__init__.py")
		dir := t.TempDir()
		modulePath := filepath.Join(dir, "aht_state.py")
		writeTestFile(t, modulePath, module, 0o600)
		driver := runtimeScript(t, "python/hermes-session-end.py")
		driverPath := filepath.Join(dir, "driver.py")
		writeTestFile(t, driverPath, driver, 0o600)
		runGeneratedCommand(t, "", "python3", driverPath, modulePath)
		requireCapturedArguments(t, capture.path, "report", "hermes", "--activity", "failed")
	})

	t.Run("pi-extension", func(t *testing.T) {
		capture := captureBinary(t)
		t.Setenv("AHT_CAPTURE", capture.path)
		module := generatedArtifactContent(t, registry.Harness("pi"), "aht-state.ts")
		runNodeRuntime(t, "extension.ts", module, runtimeScript(t, "node/pi-failed.mjs"), nil)
		requireCapturedArguments(t, capture.path, "report", "pi", "--activity", "failed")
	})

	t.Run("omp-extension", func(t *testing.T) {
		capture := captureBinary(t)
		t.Setenv("AHT_CAPTURE", capture.path)
		module := generatedArtifactContent(t, registry.Harness("omp"), "aht-state.ts")
		runNodeRuntime(t, "extension.ts", module, runtimeScript(t, "node/omp-failed.mjs"), nil)
		requireCapturedArguments(t, capture.path, "report", "omp", "--activity", "failed")
	})

	t.Run("opencode-plugin", func(t *testing.T) {
		capture := captureBinary(t)
		t.Setenv("AHT_CAPTURE", capture.path)
		module := generatedArtifactContent(t, registry.Harness("opencode"), "aht-state.ts")
		runNodeRuntime(t, "plugin.ts", module, runtimeScript(t, "node/opencode-session-error.mjs"), nil)
		requireCapturedArguments(t, capture.path, "report", "opencode", "--activity", "failed")
		requireCapturedArguments(t, capture.path, "report", "opencode", "--activity", "idle")
	})

	t.Run("opencode-plugin-v2", func(t *testing.T) {
		capture := captureBinary(t)
		t.Setenv("AHT_CAPTURE", capture.path)
		module := generatedArtifactContent(t, registry.Harness("opencode"), "aht-state.ts")
		runNodeRuntime(t, "plugin.ts", module, runtimeScript(t, "node/opencode-v2-session.mjs"), nil)
		requireCapturedArguments(t, capture.path, "report", "opencode", "--activity", "failed")
		requireCapturedArguments(t, capture.path, "report", "opencode", "--activity", "idle")
	})

	t.Run("kilo-plugin", func(t *testing.T) {
		capture := captureBinary(t)
		t.Setenv("AHT_CAPTURE", capture.path)
		module := generatedArtifactContent(t, registry.Harness("kilo"), "aht-state.ts")
		runNodeRuntime(t, "plugin.ts", module, runtimeScript(t, "node/kilo-session-error.mjs"), nil)
		requireCapturedArguments(t, capture.path, "report", "kilo", "--activity", "failed")
		requireCapturedArguments(t, capture.path, "report", "kilo", "--activity", "idle")
	})

	t.Run("amp-plugin", func(t *testing.T) {
		capture := captureBinary(t)
		t.Setenv("AHT_CAPTURE", capture.path)
		module := generatedArtifactContent(t, registry.Harness("amp"), "aht-state.ts")
		runNodeRuntime(t, "plugin.ts", module, runtimeScript(t, "node/amp-failed.mjs"), nil)
		requireCapturedArguments(t, capture.path, "report", "amp", "--activity", "failed")
		requireCapturedArguments(t, capture.path, "report", "amp", "--activity", "idle")
	})

	t.Run("missing-binary-nonfatal", func(t *testing.T) {
		runNodeRuntime(t, "opencode_absent.ts", renderedRuntimeModule(t, registry.Harness("opencode")), runtimeScript(t, "node/opencode-missing-reporter.mjs"), nil)
		runNodeRuntime(t, "opencode_v2_absent.ts", renderedRuntimeModule(t, registry.Harness("opencode")), runtimeScript(t, "node/opencode-v2-missing-reporter.mjs"), nil)

		runNodeRuntime(t, "kilo_absent.ts", renderedRuntimeModule(t, registry.Harness("kilo")), runtimeScript(t, "node/kilo-missing-reporter.mjs"), nil)

		runNodeRuntime(t, "pi_absent.ts", renderedRuntimeModule(t, registry.Harness("pi")), runtimeScript(t, "node/pi-missing-reporter.mjs"), nil)

		runNodeRuntime(t, "omp_absent.ts", renderedRuntimeModule(t, registry.Harness("omp")), runtimeScript(t, "node/omp-missing-reporter.mjs"), nil)
		runNodeRuntime(t, "plugin.ts", renderedRuntimeModule(t, registry.Harness("amp")), runtimeScript(t, "node/amp-missing-reporter.mjs"), nil)
	})
}

func TestGeneratedNativeTitleWatcherCommands(t *testing.T) {
	t.Setenv("AHT_TEST_SENSITIVE_SENTINEL", generatedRuntimeSensitiveSentinel)
	requireRuntimeTool(t, "sh")
	for _, event := range []string{"SessionStart", "FileChanged"} {
		t.Run(event, func(t *testing.T) {
			capture := captureBinary(t)
			t.Setenv("AHT_CAPTURE", capture.path)
			runGeneratedNativeTitleWatchers(t, capture.command, capture.path, event)
		})
	}
}

func runGeneratedNativeTitleWatchers(t *testing.T, binary, capturePath, event string) {
	t.Helper()
	adapter, ok := catalog.Find(registry.Harness("claude"))
	if !ok {
		t.Fatal("missing Claude adapter")
	}
	installer, ok := adapter.(harnesspkg.Installable)
	if !ok {
		t.Fatal("expected installable native watcher adapter")
	}
	found := false
	for _, action := range installer.InstallPlan(binary).Actions {
		hooks, ok := action.(harnesspkg.JSONCommandHooksAction)
		if !ok {
			continue
		}
		for _, hook := range hooks.Plan.Hooks {
			if hook.Event == event && strings.Contains(hook.Command, " --json hook ") {
				found = true
				runGeneratedCommand(t, `{"session_id":"native","transcript_path":"/tmp/native.jsonl","cwd":"/tmp","hook_event_name":"`+event+`","file_path":"/tmp/native.jsonl","event":"change"}`, "sh", "-c", hook.Command)
				requireCapturedArguments(t, capturePath, "--json", "hook", "claude", "--event", event)
			}
		}
	}
	if !found {
		t.Fatalf("no generated %s watcher", event)
	}
}

func renderedRuntimeModule(t *testing.T, h registry.Harness) string {
	t.Helper()
	const binary = "/nonexistent/binary/absent-aht"
	adapter, ok := catalog.Find(h)
	if !ok {
		t.Fatalf("find harness %s", h)
	}
	installer, ok := adapter.(harnesspkg.Installable)
	if !ok {
		t.Fatalf("harness %s is not installable", h)
	}
	for _, action := range installer.InstallPlan(binary).Actions {
		rf, ok := action.(harnesspkg.RenderedFileAction)
		if !ok {
			continue
		}
		if !strings.Contains(rf.Plan.Content, binary) {
			t.Fatalf("rendered %s template did not select absent binary %q", h, binary)
		}
		return rf.Plan.Content
	}
	t.Fatalf("no rendered action found for %s", h)
	return ""
}

func TestGeneratedWaitingDetailsAndCorrelatedResolution(t *testing.T) {
	for _, id := range []registry.Harness{registry.Harness("omp"), registry.Harness("opencode"), registry.Harness("kilo")} {
		t.Run(string(id), func(t *testing.T) {
			capture := captureBinary(t)
			t.Setenv("AHT_CAPTURE", capture.path)
			module := generatedArtifactContent(t, id, "aht-state.ts")
			driver := "node/plugin-waiting-details.mjs"
			filename := "plugin.ts"
			expected := []string{"idle:", "waiting:permission", "waiting:permission", "waiting:permission", "waiting:permission", "waiting:permission", "idle:", "running:", "running:", "failed:", "running:"}
			if id == registry.Harness("opencode") {
				t.Setenv("AHT_TEST_QUESTIONS", "1")
				expected = []string{"idle:", "waiting:permission", "waiting:permission", "waiting:permission", "waiting:permission", "waiting:permission", "idle:", "running:", "waiting:clear", "waiting:question", "running:", "failed:", "running:"}
			} else {
				t.Setenv("AHT_TEST_QUESTIONS", "0")
			}
			if id == registry.Harness("omp") {
				driver, filename = "node/omp-waiting-details.mjs", "extension.ts"
				expected = []string{"idle:", "running:", "waiting:permission", "waiting:clear", "waiting:question", "running:"}
			}
			runNodeRuntime(t, filename, module, runtimeScript(t, driver), nil)
			data, err := os.ReadFile(capture.path)
			if err != nil {
				t.Fatal(err)
			}
			states := capturedActivityStates(parseCapturedInvocations(string(data)))
			if strings.Join(states, ",") != strings.Join(expected, ",") {
				t.Fatalf("generated states = %v, want %v", states, expected)
			}
		})
	}
}

func capturedActivityStates(invocations [][]string) []string {
	var states []string
	for _, invocation := range invocations {
		activity, detail := "", ""
		for i := 0; i+1 < len(invocation); i++ {
			switch invocation[i] {
			case "--activity":
				activity = invocation[i+1]
			case "--detail":
				detail = invocation[i+1]
			}
		}
		if activity != "" {
			states = append(states, activity+":"+detail)
		}
	}
	return states
}

func TestAmpTitleReportsMetadataWithoutReplacingWaitingEvidence(t *testing.T) {
	capture := captureBinary(t)
	t.Setenv("AHT_CAPTURE", capture.path)
	module := generatedArtifactContent(t, registry.Harness("amp"), "aht-state.ts")
	runNodeRuntime(t, "plugin.ts", module, runtimeScript(t, "node/amp-title-metadata.mjs"), nil)
	data, err := os.ReadFile(capture.path)
	if err != nil {
		t.Fatal(err)
	}
	invocations := parseCapturedInvocations(string(data))
	if len(invocations) != 3 || !matchInvocation(invocations[1], []string{"--activity", "waiting"}) || !matchInvocation(invocations[2], []string{"--event", "thread.title"}) {
		t.Fatalf("Amp reports = %v", invocations)
	}
	for _, arg := range invocations[2] {
		if arg == "--activity" || arg == "--presence" || arg == "--detail" {
			t.Fatal("title metadata replaced state evidence")
		}
	}
}
