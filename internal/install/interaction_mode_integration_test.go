//go:build integration

package install

import (
	"strconv"
	"testing"

	"github.com/zigai/aht/pkg/registry"
)

func TestExtensionsReportNativeInteractionMode(t *testing.T) {
	for _, harness := range []registry.Harness{registry.HarnessPi, registry.HarnessOmp} {
		for _, mode := range []string{"tui", "print", "json", "rpc"} {
			t.Run(string(harness)+"/"+mode, func(t *testing.T) {
				capture := captureBinary(t)
				t.Setenv("AHT_CAPTURE", capture.path)
				module := generatedArtifactContent(t, harness, "aht-state.ts")
				runNodeRuntime(t, "extension.ts", module, `
import extension from "./extension.ts";
process.title = "pi";
const hooks = new Map();
extension({on: (name, handler) => hooks.set(name, handler)});
const ctx = {
  mode: `+strconv.Quote(mode)+`,
  hasUI: true,
  cwd: "/tmp/project",
  sessionManager: {
    getSessionId: () => "mode-session",
    getSessionFile: () => "/tmp/mode.jsonl",
    getBranch: () => [],
  },
};
await hooks.get("session_start")({type: "session_start"}, ctx);
await hooks.get("session_shutdown")({type: "session_shutdown"}, ctx);
`, nil)
				requireCapturedArguments(t, capture.path, "--attribute", "aht_interaction_mode="+mode)
			})
		}
	}
}
