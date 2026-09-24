//go:build integration

package install

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	harnesspkg "github.com/zigai/aht/internal/harness"
	harnesscatalog "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/pkg/registry"
)

const generatedRuntimeSensitiveSentinel = "AHT_PHASE3_SENSITIVE_SENTINEL"

func TestGeneratedRuntimeFamilies(t *testing.T) {
	t.Setenv("AHT_TEST_SENSITIVE_SENTINEL", generatedRuntimeSensitiveSentinel)
	requireRuntimeTool(t, "node")
	requireRuntimeTool(t, "python3")
	requireRuntimeTool(t, "sh")
	t.Run("command-hook", func(t *testing.T) {
		capture := captureBinary(t)
		t.Setenv("AHT_CAPTURE", capture.path)
		command := generatedCommandHook(t, registry.Harness("claude"))
		runGeneratedCommand(t, exec.Command("sh", "-c", command), `{"session_id":"command-session","prompt":"`+generatedRuntimeSensitiveSentinel+`"}`)
		requireCapturedArguments(t, capture.path, "report", "claude", "--activity", "idle")
	})

	t.Run("goose-shell-wrapper", func(t *testing.T) {
		capture := captureBinary(t)
		t.Setenv("AHT_CAPTURE", capture.path)
		script := writeRuntimeArtifact(t, "report.sh", generatedArtifactContent(t, registry.Harness("goose"), "scripts/report.sh"))
		runGeneratedCommand(t, exec.Command("sh", script, "running", "UserPromptSubmit"), `{"session_id":"goose-session","prompt":"`+generatedRuntimeSensitiveSentinel+`"}`)
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
		runGeneratedCommand(t, exec.Command("python3", driverPath, modulePath), "")
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
		const absentBinary = "/nonexistent/binary/absent-aht"
		renderAbsentModule := func(h registry.Harness) string {
			t.Helper()
			adapter, ok := harnesscatalog.Find(h)
			if !ok {
				t.Fatalf("find harness %s", h)
			}
			installer, ok := adapter.(harnesspkg.Installable)
			if !ok {
				t.Fatalf("harness %s is not installable", h)
			}
			for _, action := range installer.InstallPlan(absentBinary).Actions {
				if rf, ok := action.(harnesspkg.RenderedFileAction); ok {
					if !strings.Contains(rf.Plan.Content, absentBinary) {
						t.Fatalf("rendered %s template did not select absent binary %q", h, absentBinary)
					}
					return rf.Plan.Content
				}
			}
			t.Fatalf("no rendered action found for %s", h)
			return ""
		}

		runNodeRuntime(t, "opencode_absent.ts", renderAbsentModule(registry.Harness("opencode")), runtimeScript(t, "node/opencode-missing-reporter.mjs"), nil)
		runNodeRuntime(t, "opencode_v2_absent.ts", renderAbsentModule(registry.Harness("opencode")), runtimeScript(t, "node/opencode-v2-missing-reporter.mjs"), nil)

		runNodeRuntime(t, "kilo_absent.ts", renderAbsentModule(registry.Harness("kilo")), runtimeScript(t, "node/kilo-missing-reporter.mjs"), nil)

		runNodeRuntime(t, "pi_absent.ts", renderAbsentModule(registry.Harness("pi")), runtimeScript(t, "node/pi-missing-reporter.mjs"), nil)

		runNodeRuntime(t, "omp_absent.ts", renderAbsentModule(registry.Harness("omp")), runtimeScript(t, "node/omp-missing-reporter.mjs"), nil)
		runNodeRuntime(t, "plugin.ts", renderAbsentModule(registry.Harness("amp")), runtimeScript(t, "node/amp-missing-reporter.mjs"), nil)
	})
}
